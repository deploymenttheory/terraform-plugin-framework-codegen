package emit

import (
	"fmt"
	"strconv"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/sdkbind"
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
	var b strings.Builder
	indent := strings.Repeat("\t", depth)

	for _, n := range nodes {
		if n.fb == nil || n.fb.Access.Get == "" {
			continue
		}
		if n.attr.Nested != nil {
			lines, err := stateNested(n, modelPrefix, src, dst, depth)
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
func stateNested(n node, modelPrefix, src, dst string, depth int) (string, error) {
	indent := strings.Repeat("\t", depth)
	field := dst + "." + ir.GoName(n.attr.Name)
	modelType := nestedModelName(modelPrefix, n)

	if n.attr.Kind == ir.TypeList {
		elementsVar := "elements" + depthSuffix(depth)
		indexVar := "index" + depthSuffix(depth)
		listVar := lowerCamel(n.attr.Name) + "List" + depthSuffix(depth)
		elemVar := lowerCamel(n.attr.Name) + "Element" + depthSuffix(depth)

		inner, err := stateLines(n.children, modelPrefix, elementsVar+"["+indexVar+"]", elemVar, depth+2)
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
	inner, err := stateLines(n.children, modelPrefix, valueVar, nestedVar, depth+1)
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
}

// buildCallPlan renders one bound call. payloadName names the success
// payload local; nodes and modelVar say where parameter values come from.
func buildCallPlan(call *sdkbind.Call, payloadName string, nodes []node, modelVar string) (callPlan, error) {
	var plan callPlan

	var decls []string
	for _, p := range call.Params {
		field, err := paramField(p, nodes, len(call.Params) == 1)
		if err != nil {
			return callPlan{}, err
		}
		decls = append(decls, fmt.Sprintf("%s := %s.%s.%s()", p.Local, modelVar, field, valueMethod(p.GoType)))
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
	for _, n := range nodes {
		if n.attr.WireName == p.Wire {
			return ir.GoName(n.attr.Name), nil
		}
	}
	snake := ir.TerraformName(p.Wire)
	for _, n := range nodes {
		if n.attr.Name == snake {
			return ir.GoName(n.attr.Name), nil
		}
	}
	if idFallback {
		for _, n := range nodes {
			if n.attr.Name == "id" {
				return ir.GoName(n.attr.Name), nil
			}
		}
	}
	return "", fmt.Errorf("path parameter %q matches no attribute and the entity has no id attribute", p.Wire)
}

// valueMethod is the framework value accessor for one parameter type.
func valueMethod(goType string) string {
	switch goType {
	case "bool":
		return "ValueBool"
	case "int64":
		return "ValueInt64"
	case "float64":
		return "ValueFloat64"
	default:
		return "ValueString"
	}
}
