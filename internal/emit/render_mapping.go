package emit

import (
	"fmt"
	"strconv"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// depthSuffix disambiguates generated locals across nesting levels, so a
// tree nested under an attribute of its own name still renders distinct
// identifiers.
func depthSuffix(depth int) string {
	if depth <= 1 {
		return ""
	}
	return strconv.Itoa(depth)
}

// errReturn is what a failed conversion returns inside a construct
// function.
func errReturn(attrPath string) string {
	return fmt.Sprintf("return nil, fmt.Errorf(\"the %s attribute: %%w\", err)", attrPath)
}

// constructLines renders the body statements mapping one level of plan
// fields onto the SDK write model. src is the model expression
// ("data", "data.Settings"), dst the settable SDK value ("body"),
// gateUpdates wraps attributes updates silently discard in an isCreate
// guard.
func constructLines(nodes []node, src, dst, attrPrefix string, depth int, gateUpdates bool) (string, bool, error) {
	var b strings.Builder
	usesFmt := false
	indent := strings.Repeat("\t", depth)

	for _, n := range nodes {
		if n.fb == nil || n.fb.Access.Set == "" {
			continue
		}
		attrPath := n.attr.Name
		if attrPrefix != "" {
			attrPath = attrPrefix + "." + n.attr.Name
		}

		var lines string
		var err error
		var nestedUsesFmt bool
		if n.attr.Nested != nil {
			lines, nestedUsesFmt, err = constructNested(n, src, dst, attrPath, depth)
			usesFmt = usesFmt || nestedUsesFmt
		} else {
			lines, nestedUsesFmt, err = constructScalar(n, src, dst, attrPath, indent)
			usesFmt = usesFmt || nestedUsesFmt
		}
		if err != nil {
			return "", false, err
		}

		if gateUpdates && n.attr.SilentlyIgnoredOnUpdate {
			guarded := indent + "if isCreate {\n" + reindent(lines, "\t") + indent + "}\n"
			b.WriteString(guarded)
			continue
		}
		b.WriteString(lines)
	}
	return b.String(), usesFmt, nil
}

// constructScalar renders one scalar field's write.
func constructScalar(n node, src, dst, attrPath, indent string) (string, bool, error) {
	plan, err := writeConvert(n.fb)
	if err != nil {
		return "", false, err
	}
	call := plan.call(src+"."+ir.GoName(n.attr.Name), dst+"."+n.fb.Access.Set)
	if plan.returnsErr {
		return fmt.Sprintf("%sif err := %s; err != nil {\n%s\t%s\n%s}\n",
			indent, call, indent, errReturn(attrPath), indent), true, nil
	}
	return indent + call + "\n", false, nil
}

// constructNested renders a nested object or list-of-objects write.
func constructNested(n node, src, dst, attrPath string, depth int) (string, bool, error) {
	indent := strings.Repeat("\t", depth)
	field := src + "." + ir.GoName(n.attr.Name)
	elemType := strings.TrimPrefix(n.fb.Access.SDKType, "[]")
	// A concrete element type takes the constructed value dereferenced;
	// an interface or pointer element takes the constructor's pointer.
	deref := ""
	if elemType == n.fb.NestedWriteModel {
		deref = "*"
	}

	if n.attr.Kind == ir.TypeList {
		listVar := lowerCamel(n.attr.Name) + "List" + depthSuffix(depth)
		indexVar := "index" + depthSuffix(depth)
		elemVar := lowerCamel(n.attr.Name) + "Element" + depthSuffix(depth)

		inner, usesFmt, err := constructLines(n.children, field+"["+indexVar+"]", elemVar, attrPath, depth+2, false)
		if err != nil {
			return "", false, err
		}

		var b strings.Builder
		fmt.Fprintf(&b, "%sif %s != nil {\n", indent, field)
		fmt.Fprintf(&b, "%s\t%s := make([]%s, 0, len(%s))\n", indent, listVar, elemType, field)
		fmt.Fprintf(&b, "%s\tfor %s := range %s {\n", indent, indexVar, field)
		fmt.Fprintf(&b, "%s\t\t%s := %s\n", indent, elemVar, n.fb.NestedConstructor)
		b.WriteString(inner)
		fmt.Fprintf(&b, "%s\t\t%s = append(%s, %s%s)\n", indent, listVar, listVar, deref, elemVar)
		fmt.Fprintf(&b, "%s\t}\n", indent)
		fmt.Fprintf(&b, "%s\t%s.%s(%s)\n", indent, dst, n.fb.Access.Set, listVar)
		fmt.Fprintf(&b, "%s}\n", indent)
		return b.String(), usesFmt, nil
	}

	singleDeref := ""
	if n.fb.Access.SDKType == n.fb.NestedWriteModel {
		singleDeref = "*"
	}
	nestedVar := lowerCamel(n.attr.Name) + "Body" + depthSuffix(depth)
	inner, usesFmt, err := constructLines(n.children, field, nestedVar, attrPath, depth+1, false)
	if err != nil {
		return "", false, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%sif %s != nil {\n", indent, field)
	fmt.Fprintf(&b, "%s\t%s := %s\n", indent, nestedVar, n.fb.NestedConstructor)
	b.WriteString(inner)
	fmt.Fprintf(&b, "%s\t%s.%s(%s%s)\n", indent, dst, n.fb.Access.Set, singleDeref, nestedVar)
	fmt.Fprintf(&b, "%s}\n", indent)
	return b.String(), usesFmt, nil
}

// stateLines renders the body statements mapping one level of SDK fields
// onto the framework model. src is the readable SDK value ("remote"),
// dst the model expression ("data", "rulesElement").
func stateLines(nodes []node, modelPrefix string, src, dst string, depth int) (string, error) {
	return stateLinesWith(newModelNamer(modelPrefix, nodes), "", nodes, src, dst, depth)
}

// stateLinesWith is stateLines' recursion, carrying the entity's resolved
// model names and the attribute path reached so far. Both halves of the
// generated code — the struct declarations and these assignments — name a
// nested model through the one namer, so a collision-qualified struct is
// spelled identically in each.
func stateLinesWith(namer *modelNamer, path string, nodes []node, src, dst string, depth int) (string, error) {
	var b strings.Builder
	indent := strings.Repeat("\t", depth)

	for _, n := range nodes {
		if n.fb == nil || n.fb.Access.Get == "" {
			continue
		}
		if n.attr.Nested != nil {
			lines, err := stateNested(namer, childPath(path, n), n, src, dst, depth)
			if err != nil {
				return "", err
			}
			b.WriteString(lines)
			continue
		}
		fn, err := readConvert(n.fb)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s%s.%s = convert.%s(%s.%s())\n",
			indent, dst, ir.GoName(n.attr.Name), fn, src, n.fb.Access.Get)
	}
	return b.String(), nil
}

// stateNested renders a nested object or list-of-objects read.
func stateNested(namer *modelNamer, path string, n node, src, dst string, depth int) (string, error) {
	indent := strings.Repeat("\t", depth)
	field := dst + "." + ir.GoName(n.attr.Name)
	modelType := namer.name(path)

	if n.attr.Kind == ir.TypeList {
		elementsVar := "elements" + depthSuffix(depth)
		indexVar := "index" + depthSuffix(depth)
		listVar := lowerCamel(n.attr.Name) + "List" + depthSuffix(depth)
		elemVar := lowerCamel(n.attr.Name) + "Element" + depthSuffix(depth)

		inner, err := stateLinesWith(namer, path, n.children, elementsVar+"["+indexVar+"]", elemVar, depth+2)
		if err != nil {
			return "", err
		}

		var b strings.Builder
		fmt.Fprintf(&b, "%sif %s := %s.%s(); %s != nil {\n", indent, elementsVar, src, n.fb.Access.Get, elementsVar)
		fmt.Fprintf(&b, "%s\t%s := make([]%s, 0, len(%s))\n", indent, listVar, modelType, elementsVar)
		fmt.Fprintf(&b, "%s\tfor %s := range %s {\n", indent, indexVar, elementsVar)
		fmt.Fprintf(&b, "%s\t\t%s := %s{}\n", indent, elemVar, modelType)
		b.WriteString(inner)
		fmt.Fprintf(&b, "%s\t\t%s = append(%s, %s)\n", indent, listVar, listVar, elemVar)
		fmt.Fprintf(&b, "%s\t}\n", indent)
		fmt.Fprintf(&b, "%s\t%s = %s\n", indent, field, listVar)
		fmt.Fprintf(&b, "%s} else {\n%s\t%s = nil\n%s}\n", indent, indent, field, indent)
		return b.String(), nil
	}

	valueVar := "value" + depthSuffix(depth)
	nestedVar := lowerCamel(n.attr.Name) + "State" + depthSuffix(depth)
	inner, err := stateLinesWith(namer, path, n.children, valueVar, nestedVar, depth+1)
	if err != nil {
		return "", err
	}

	// A value-typed nested accessor cannot be nil; interface and pointer
	// ones can, and nil maps to a null object. The binder decides this from
	// the accessor's real type — the SDK-type spelling alone cannot, because a
	// kiota interface return spells identically to its own model name yet is
	// nil-comparable.
	nilable := n.fb.Access.NestedNilable

	var b strings.Builder
	if nilable {
		fmt.Fprintf(&b, "%sif %s := %s.%s(); %s != nil {\n", indent, valueVar, src, n.fb.Access.Get, valueVar)
	} else {
		fmt.Fprintf(&b, "%s{\n%s\t%s := %s.%s()\n", indent, indent, valueVar, src, n.fb.Access.Get)
	}
	fmt.Fprintf(&b, "%s\t%s := &%s{}\n", indent, nestedVar, modelType)
	b.WriteString(inner)
	fmt.Fprintf(&b, "%s\t%s = %s\n", indent, field, nestedVar)
	if nilable {
		fmt.Fprintf(&b, "%s} else {\n%s\t%s = nil\n%s}\n", indent, indent, field, indent)
	} else {
		fmt.Fprintf(&b, "%s}\n", indent)
	}
	return b.String(), nil
}

// reindent shifts every non-empty line of a rendered block one level
// deeper.
func reindent(block, extra string) string {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = extra + line
		}
	}
	return strings.Join(lines, "\n")
}

// callPlan is one SDK invocation rendered for a template: the parameter
// declarations, the assignment shape, and the expression.
type callPlan struct {
	// ParamDecls declares the locals the expression references, one
	// finished statement per line.
	ParamDecls string
	// Assign is the finished assignment statement including the
	// expression, e.g. "created, err := client.Tags().Post(ctx, body, nil)".
	Assign string
	// Payload is the local the success payload landed in, empty when the
	// call yields none.
	Payload string
	// ClosureBody is the call rendered as a delete-closure body: blanks
	// for non-error results, returning the error.
	ClosureBody string
	// Imports names the standard-library packages the parameter
	// declarations reference, e.g. "strconv" for a converted identifier.
	Imports []string
}

// buildCallPlan renders one bound call. payloadName names the success
// payload local; nodes and modelVar say where parameter values come from.
func buildCallPlan(call *sdkbind.Call, payloadName string, nodes []node, modelVar string) (callPlan, error) {
	var plan callPlan

	var decls []string
	for position, p := range call.Params {
		// The last path parameter addresses the object itself, which is what
		// the id attribute holds however the API spells the parameter. The
		// fallback used to need the call to take exactly one parameter, which
		// is only true of a flat API: /enterprises/{enterprise}/code-security/
		// configurations/{configuration_id} takes two, and its id was left
		// matching nothing because the response happened to declare an id of
		// its own and keep that spelling.
		n, err := paramNode(p, nodes, position == len(call.Params)-1)
		if err != nil {
			return callPlan{}, err
		}
		value, needs, err := paramValue(p, modelVar, ir.GoName(n.attr.Name), n.attr.Kind)
		if err != nil {
			return callPlan{}, err
		}
		if needs != "" {
			plan.Imports = append(plan.Imports, needs)
		}
		decls = append(decls, fmt.Sprintf("%s := %s", p.Local, value))
	}
	plan.ParamDecls = strings.Join(decls, "\n\t")

	results := call.Results
	if len(results) == 0 {
		results = []string{"error"}
	}
	lhs := make([]string, len(results))
	for i, r := range results {
		switch {
		case r == "error":
			lhs[i] = "err"
		case payloadName != "" && plan.Payload == "" && r != "*http.Response":
			plan.Payload = payloadName
			lhs[i] = payloadName
		default:
			lhs[i] = "_"
		}
	}
	plan.Assign = strings.Join(lhs, ", ") + " := " + call.Expr

	if len(results) == 1 && results[0] == "error" {
		plan.ClosureBody = "return " + call.Expr
	} else {
		closure := make([]string, len(results))
		for i, r := range results {
			closure[i] = "_"
			if r == "error" {
				closure[i] = "err"
			}
		}
		plan.ClosureBody = strings.Join(closure, ", ") + " := " + call.Expr + "\n\t\treturn err"
	}

	return plan, nil
}

// paramField finds the model field a path parameter reads from: the
// attribute speaking the same wire name, its terraform spelling, or — for
// a single-parameter call only — the id attribute, which is how an item
// path names its key in every REST shape the derivation admits.
func paramField(p sdkbind.CallParam, nodes []node, idFallback bool) (string, error) {
	n, err := paramNode(p, nodes, idFallback)
	if err != nil {
		return "", err
	}
	return ir.GoName(n.attr.Name), nil
}

// paramNode is paramField's answer before it is reduced to a spelling: the
// attribute itself, whose declared kind decides how its value is read.
func paramNode(p sdkbind.CallParam, nodes []node, idFallback bool) (node, error) {
	// A path parameter is a scalar in the URL. An attribute of the same name
	// that is an object is a different thing the document happens to spell
	// the same way — a repository's owner block beside the owner segment of
	// its path — and reading a value out of it does not compile.
	for _, n := range nodes {
		if n.attr.WireName == p.Wire && n.attr.Nested == nil {
			return n, nil
		}
	}
	snake := ir.TerraformName(p.Wire)
	for _, n := range nodes {
		if n.attr.Name == snake && n.attr.Nested == nil {
			return n, nil
		}
	}
	if idFallback {
		for _, n := range nodes {
			if n.attr.Name == idAttributeName && n.attr.Nested == nil {
				return n, nil
			}
		}
	}
	return node{}, unrenderable("path parameter %q matches no scalar attribute and the entity has no id attribute", p.Wire)
}

// valueMethod is the framework value accessor for one attribute kind. It
// reads the model field, so it answers to the kind the model declares —
// never to what the SDK happens to take, which is a separate question
// paramValue answers on top of this one.
func valueMethod(kind ir.AttributeType) string {
	switch kind {
	case ir.TypeBool:
		return "ValueBool"
	case ir.TypeInt64:
		return "ValueInt64"
	case ir.TypeFloat64:
		return "ValueFloat64"
	default:
		return "ValueString"
	}
}

// paramValue renders the expression one path parameter is passed as: the
// model field read through its own accessor, converted to whatever the SDK
// method really takes.
//
// The two can differ, and routinely do. The model field's kind comes from
// the document, which has one integer type; the SDK's parameter type comes
// from the generator, which does not — an integer path parameter arrives as
// int32 about as often as int64, and sometimes as a string. Reading the
// field with an accessor chosen from the SDK's type was the bug: it produced
// data.ID.ValueString() on a types.Int64 field, which does not compile, and
// where both happened to be integers it passed an int64 to an int32
// parameter, which does not either.
//
// A conversion this cannot spell without inventing error handling — a string
// field into a numeric parameter — refuses the entity rather than guessing.
func paramValue(p sdkbind.CallParam, modelVar, field string, kind ir.AttributeType) (string, string, error) {
	read := modelVar + "." + field + "." + valueMethod(kind) + "()"

	switch {
	case sdkTypeMatches(kind, p.GoType):
		return read, "", nil
	case kind == ir.TypeInt64 && isIntegerType(p.GoType):
		// Narrowing an id to the width the SDK declares. A real identifier
		// does not approach the boundary, and the alternative is refusing
		// every operation whose generator chose int32.
		return p.GoType + "(" + read + ")", "", nil
	case kind == ir.TypeFloat64 && p.GoType == "float32":
		return "float32(" + read + ")", "", nil
	case kind == ir.TypeInt64 && p.GoType == "string":
		return "strconv.FormatInt(" + read + ", 10)", "strconv", nil
	case kind == ir.TypeFloat64 && p.GoType == "string":
		return "strconv.FormatFloat(" + read + ", 'f', -1, 64)", "strconv", nil
	case kind == ir.TypeBool && p.GoType == "string":
		return "strconv.FormatBool(" + read + ")", "strconv", nil
	}
	return "", "", unrenderable(
		"path parameter %q is %s in the schema but %s in the generated SDK, and no conversion between them is safe without a parse that can fail",
		p.Wire, kind, p.GoType)
}

// sdkTypeMatches reports whether the SDK takes exactly what the model field
// yields, so the value passes through unconverted.
func sdkTypeMatches(kind ir.AttributeType, goType string) bool {
	switch kind {
	case ir.TypeBool:
		return goType == "bool"
	case ir.TypeInt64:
		return goType == "int64"
	case ir.TypeFloat64:
		return goType == "float64"
	default:
		return goType == "string"
	}
}

// isIntegerType reports whether an SDK parameter type is an integer a
// framework Int64 converts into without a parse.
func isIntegerType(goType string) bool {
	switch goType {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

// addPlanImports adds whatever standard-library packages a set of rendered
// call plans reference in their parameter declarations. A plan that converts
// nothing adds nothing, which is the common case.
func addPlanImports(set *importSet, plans ...callPlan) {
	for _, plan := range plans {
		for _, name := range plan.Imports {
			set.add("", name)
		}
	}
}
