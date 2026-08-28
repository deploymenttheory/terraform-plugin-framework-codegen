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

// diagReturn is errReturn for a failure reported as diagnostics rather than
// an error: decoding a plan object into its generated struct.
func diagReturn(attrPath string) string {
	return fmt.Sprintf("return nil, fmt.Errorf(\"the %s attribute: %%v\", diags)", attrPath)
}

// constructLines renders the body statements mapping one level of plan
// fields onto the SDK write model. src is the model expression
// ("data", "data.Settings"), dst the settable SDK value ("body"),
// gateUpdates wraps attributes updates silently discard in an isCreate
// guard.
// constructLinesFor is constructLines' entry point, resolving the entity's
// nested model names once so a decoded plan object is spelled the same here
// as in the model declaration.
func constructLinesFor(nodes []node, modelPrefix, source, destination, attrPrefix string, depth int, gateUpdates bool) (string, bool, error) {
	return constructLines(newModelNamer(modelPrefix, nodes), "", nodes, source, destination, attrPrefix, depth, gateUpdates)
}

func constructLines(namer *modelNamer, path string, nodes []node, source, destination, attrPrefix string, depth int, gateUpdates bool) (string, bool, error) {
	var b strings.Builder
	usesFmt := false
	indent := strings.Repeat("\t", depth)

	for _, n := range nodes {
		if n.fb == nil || n.fb.Access.Set == "" {
			continue
		}
		attrPath := n.attribute.Name
		if attrPrefix != "" {
			attrPath = attrPrefix + "." + n.attribute.Name
		}

		var lines string
		var err error
		var nestedUsesFmt bool
		if n.attribute.Nested != nil {
			lines, nestedUsesFmt, err = constructNested(namer, childPath(path, n), n, source, destination, attrPath, depth)
			usesFmt = usesFmt || nestedUsesFmt
		} else if strings.HasSuffix(n.fb.Access.ConvertSet, "MapAdditionalData") {
			lines, err = constructAdditionalDataMap(n, source, destination, attrPath, indent)
			usesFmt = true
		} else {
			lines, nestedUsesFmt, err = constructScalar(n, source, destination, attrPath, indent)
			usesFmt = usesFmt || nestedUsesFmt
		}
		if err != nil {
			return "", false, err
		}

		if gateUpdates && n.attribute.SilentlyIgnoredOnUpdate {
			guarded := indent + "if isCreate {\n" + reindent(lines, "\t") + indent + "}\n"
			b.WriteString(guarded)
			continue
		}
		b.WriteString(lines)
	}
	return b.String(), usesFmt, nil
}

// constructAdditionalDataMap renders the write of a map the SDK carries as a
// model with an untyped additionalData bag: build the model, fill its bag
// from the plan, hand it to the parent's setter.
//
// Three statements rather than one call, because the value the parent setter
// takes is the model and the value the plan holds is the map, and nothing in
// the catalog can bridge those in one hop without naming both types.
func constructAdditionalDataMap(n node, source, destination, attrPath, indent string) (string, error) {
	plan, err := writeConvert(n.fb)
	if err != nil {
		return "", err
	}
	field := source + "." + ir.GoName(n.attribute.Name)
	local := lowerCamel(n.attribute.Name) + "Map"

	var b strings.Builder
	fmt.Fprintf(&b, "%sif !%s.IsNull() && !%s.IsUnknown() {\n", indent, field, field)
	fmt.Fprintf(&b, "%s\t%s := %s\n", indent, local, n.fb.NestedConstructor)
	fmt.Fprintf(&b, "%s\tif err := convert.%s(ctx, %s, %s.SetAdditionalData); err != nil {\n",
		indent, plan.fn, field, local)
	fmt.Fprintf(&b, "%s\t\t%s\n%s\t}\n", indent, errReturn(attrPath), indent)
	fmt.Fprintf(&b, "%s\t%s.%s(%s)\n", indent, destination, n.fb.Access.Set, local)
	fmt.Fprintf(&b, "%s}\n", indent)
	return b.String(), nil
}

// constructScalar renders one scalar field's write.
func constructScalar(n node, source, destination, attrPath, indent string) (string, bool, error) {
	plan, err := writeConvert(n.fb)
	if err != nil {
		return "", false, err
	}
	call := plan.call(source+"."+ir.GoName(n.attribute.Name), destination+"."+n.fb.Access.Set)
	if plan.returnsErr {
		return fmt.Sprintf("%sif err := %s; err != nil {\n%s\t%s\n%s}\n",
			indent, call, indent, errReturn(attrPath), indent), true, nil
	}
	return indent + call + "\n", false, nil
}

// constructNested renders a nested object or list-of-objects write.
func constructNested(namer *modelNamer, path string, n node, source, destination, attrPath string, depth int) (string, bool, error) {
	indent := strings.Repeat("\t", depth)
	field := source + "." + ir.GoName(n.attribute.Name)
	// Construction builds the type the setter takes. That is usually the
	// same as the getter's, and is not when the SDK emits one model per
	// direction.
	writeType := n.fb.Access.SDKType
	if n.fb.Access.SDKWriteType != "" {
		writeType = n.fb.Access.SDKWriteType
	}
	elemType := strings.TrimPrefix(writeType, "[]")
	// A concrete element type takes the constructed value dereferenced;
	// an interface or pointer element takes the constructor's pointer.
	deref := ""
	if elemType == n.fb.NestedWriteModel {
		deref = "*"
	}

	if n.attribute.Kind == ir.TypeList {
		listVar := lowerCamel(n.attribute.Name) + "List" + depthSuffix(depth)
		indexVar := "index" + depthSuffix(depth)
		elemVar := lowerCamel(n.attribute.Name) + "Element" + depthSuffix(depth)

		modelsVar := lowerCamel(n.attribute.Name) + "Models" + depthSuffix(depth)
		inner, _, err := constructLines(namer, path, n.children, modelsVar+"["+indexVar+"]", elemVar, attrPath, depth+2, false)
		if err != nil {
			return "", false, err
		}
		// No child is written, so there is nothing to build. Rendering the
		// loop anyway declares an index nothing reads, which does not
		// compile.
		if strings.TrimSpace(inner) == "" {
			return "", false, nil
		}

		var b strings.Builder
		// A null or unknown list writes nothing: the plan has no elements to
		// build from, and unknown is what a computed list carries before the
		// API has answered.
		fmt.Fprintf(&b, "%sif !%s.IsNull() && !%s.IsUnknown() {\n", indent, field, field)
		fmt.Fprintf(&b, "%s\tvar %s []%s\n", indent, modelsVar, namer.name(path))
		fmt.Fprintf(&b, "%s\tif diags := %s.ElementsAs(ctx, &%s, false); diags.HasError() {\n", indent, field, modelsVar)
		fmt.Fprintf(&b, "%s\t\t%s\n%s\t}\n", indent, diagReturn(attrPath), indent)
		fmt.Fprintf(&b, "%s\t%s := make([]%s, 0, len(%s))\n", indent, listVar, elemType, modelsVar)
		fmt.Fprintf(&b, "%s\tfor %s := range %s {\n", indent, indexVar, modelsVar)
		fmt.Fprintf(&b, "%s\t\t%s := %s\n", indent, elemVar, n.fb.NestedConstructor)
		b.WriteString(inner)
		fmt.Fprintf(&b, "%s\t\t%s = append(%s, %s%s)\n", indent, listVar, listVar, deref, elemVar)
		fmt.Fprintf(&b, "%s\t}\n", indent)
		fmt.Fprintf(&b, "%s\t%s.%s(%s)\n", indent, destination, n.fb.Access.Set, listVar)
		fmt.Fprintf(&b, "%s}\n", indent)
		return b.String(), true, nil
	}

	singleDeref := ""
	if writeType == n.fb.NestedWriteModel {
		singleDeref = "*"
	}
	nestedVar := lowerCamel(n.attribute.Name) + "Body" + depthSuffix(depth)
	modelVar := lowerCamel(n.attribute.Name) + "Model" + depthSuffix(depth)
	inner, _, err := constructLines(namer, path, n.children, modelVar, nestedVar, attrPath, depth+1, false)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(inner) == "" {
		return "", false, nil
	}

	var b strings.Builder
	// A null or unknown object writes nothing: unknown is what a computed
	// object carries before the API has answered, and neither state has
	// fields to build from.
	fmt.Fprintf(&b, "%sif !%s.IsNull() && !%s.IsUnknown() {\n", indent, field, field)
	fmt.Fprintf(&b, "%s\tvar %s %s\n", indent, modelVar, namer.name(path))
	fmt.Fprintf(&b, "%s\tif diags := %s.As(ctx, &%s, basetypes.ObjectAsOptions{}); diags.HasError() {\n", indent, field, modelVar)
	fmt.Fprintf(&b, "%s\t\t%s\n%s\t}\n", indent, diagReturn(attrPath), indent)
	fmt.Fprintf(&b, "%s\t%s := %s\n", indent, nestedVar, n.fb.NestedConstructor)
	b.WriteString(inner)
	fmt.Fprintf(&b, "%s\t%s.%s(%s%s)\n", indent, destination, n.fb.Access.Set, singleDeref, nestedVar)
	fmt.Fprintf(&b, "%s}\n", indent)
	return b.String(), true, nil
}

// remoteValue is the local every generated read maps from: the SDK value
// the call answered.
const remoteValue = "remote"

// stateLines renders the body statements mapping one level of SDK fields
// onto the framework model, from the remote value onto dst, the model
// expression ("data", "item").
func stateLines(nodes []node, modelPrefix string, destination string) (string, error) {
	return stateLinesWith(newModelNamer(modelPrefix, nodes), "", nodes, remoteValue, destination, 1)
}

// stateLinesWith is stateLines' recursion, carrying the entity's resolved
// model names and the attribute path reached so far. Both halves of the
// generated code — the struct declarations and these assignments — name a
// nested model through the one namer, so a collision-qualified struct is
// spelled identically in each.
func stateLinesWith(namer *modelNamer, path string, nodes []node, source, destination string, depth int) (string, error) {
	var b strings.Builder
	indent := strings.Repeat("\t", depth)

	for _, n := range nodes {
		if n.fb == nil || n.fb.Access.Get == "" {
			continue
		}
		if n.attribute.Nested != nil {
			lines, err := stateNested(namer, childPath(path, n), n, source, destination, depth)
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
		// A root string the API stores in a spelling of its own reads back
		// as the configured value where the answer is that spelling: the
		// model being filled still holds the planned or prior value.
		if path == "" && n.attribute.Normalisation != "" && n.attribute.Kind == ir.TypeString {
			fmt.Fprintf(&b, "%s%s.%s = convert.Normalised(%s.%s, convert.%s(%s.%s()), %q)\n",
				indent, destination, ir.GoName(n.attribute.Name), destination, ir.GoName(n.attribute.Name), fn, source, n.fb.Access.Get, n.attribute.Normalisation)
			continue
		}
		fmt.Fprintf(&b, "%s%s.%s = convert.%s(%s.%s())\n",
			indent, destination, ir.GoName(n.attribute.Name), fn, source, n.fb.Access.Get)
	}
	return b.String(), nil
}

// stateNested renders a nested object or list-of-objects read.
func stateNested(namer *modelNamer, path string, n node, source, destination string, depth int) (string, error) {
	indent := strings.Repeat("\t", depth)
	field := destination + "." + ir.GoName(n.attribute.Name)
	modelType := namer.name(path)

	if n.attribute.Kind == ir.TypeList {
		elementsVar := "elements" + depthSuffix(depth)
		indexVar := "index" + depthSuffix(depth)
		listVar := lowerCamel(n.attribute.Name) + "List" + depthSuffix(depth)
		elemVar := lowerCamel(n.attribute.Name) + "Element" + depthSuffix(depth)

		inner, err := stateLinesWith(namer, path, n.children, elementsVar+"["+indexVar+"]", elemVar, depth+2)
		if err != nil {
			return "", err
		}

		var b strings.Builder
		fmt.Fprintf(&b, "%sif %s := %s.%s(); %s != nil {\n", indent, elementsVar, source, n.fb.Access.Get, elementsVar)
		fmt.Fprintf(&b, "%s\t%s := make([]%s, 0, len(%s))\n", indent, listVar, modelType, elementsVar)
		fmt.Fprintf(&b, "%s\tfor %s := range %s {\n", indent, indexVar, elementsVar)
		fmt.Fprintf(&b, "%s\t\t%s := %s{}\n", indent, elemVar, modelType)
		b.WriteString(inner)
		fmt.Fprintf(&b, "%s\t\t%s = append(%s, %s)\n", indent, listVar, listVar, elemVar)
		fmt.Fprintf(&b, "%s\t}\n", indent)
		valueName := lowerCamel(n.attribute.Name) + "Value" + depthSuffix(depth)
		diagsName := lowerCamel(n.attribute.Name) + "Diags" + depthSuffix(depth)
		elemType := nestedObjectType(namer, path)
		fmt.Fprintf(&b, "%s\t%s, %s := types.ListValueFrom(ctx, %s, %s)\n",
			indent, valueName, diagsName, elemType, listVar)
		fmt.Fprintf(&b, "%s\tdiags.Append(%s...)\n", indent, diagsName)
		fmt.Fprintf(&b, "%s\t%s = %s\n", indent, field, valueName)
		fmt.Fprintf(&b, "%s} else {\n%s\t%s = types.ListNull(%s)\n%s}\n",
			indent, indent, field, elemType, indent)
		return b.String(), nil
	}

	valueVar := "value" + depthSuffix(depth)
	nestedVar := lowerCamel(n.attribute.Name) + "State" + depthSuffix(depth)
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
		fmt.Fprintf(&b, "%sif %s := %s.%s(); %s != nil {\n", indent, valueVar, source, n.fb.Access.Get, valueVar)
	} else {
		fmt.Fprintf(&b, "%s{\n%s\t%s := %s.%s()\n", indent, indent, valueVar, source, n.fb.Access.Get)
	}
	valueName := lowerCamel(n.attribute.Name) + "Value" + depthSuffix(depth)
	diagsName := lowerCamel(n.attribute.Name) + "Diags" + depthSuffix(depth)
	fmt.Fprintf(&b, "%s\t%s := %s{}\n", indent, nestedVar, modelType)
	b.WriteString(inner)
	fmt.Fprintf(&b, "%s\t%s, %s := types.ObjectValueFrom(ctx, %s(), %s)\n",
		indent, valueName, diagsName, attrTypesFuncName(modelType), nestedVar)
	fmt.Fprintf(&b, "%s\tdiags.Append(%s...)\n", indent, diagsName)
	fmt.Fprintf(&b, "%s\t%s = %s\n", indent, field, valueName)
	if nilable {
		fmt.Fprintf(&b, "%s} else {\n%s\t%s = types.ObjectNull(%s())\n%s}\n",
			indent, indent, field, attrTypesFuncName(modelType), indent)
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

// finalisedAPIRequest is one SDK invocation rendered for a template: the parameter
// declarations, the assignment shape, and the expression.
type finalisedAPIRequest struct {
	// ParameterDeclarations declares the locals the expression references, one
	// finished statement per line.
	ParameterDeclarations string
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

// parameterFailure is what a path-parameter conversion that fails does in the
// method its declaration lands in. Every lifecycle and invoke method carries
// a resp with Diagnostics; a list resource carries a results stream instead,
// and the two report an error by different names.
type parameterFailure struct {
	// report renders the statements a failed conversion runs, given the
	// terraform attribute at fault, the parameter's wire name, and the local
	// holding the error. It ends by leaving the method.
	report func(attribute, wire, errLocal string) string
	// imports names the packages report's statements reference.
	imports []string
}

// respDiagnostics reports a failed conversion against the attribute at fault.
// Every method a declaration lands in — Create, Read, Update, Delete, Invoke —
// carries a resp with Diagnostics and returns nothing.
func respDiagnostics() parameterFailure {
	return parameterFailure{
		report: func(attribute, wire, errLocal string) string {
			return fmt.Sprintf("\tresp.Diagnostics.AddAttributeError(path.Root(%q), \"Invalid %s\", %s.Error())\n\t\treturn",
				attribute, wire, errLocal)
		},
		imports: []string{"github.com/hashicorp/terraform-plugin-framework/path"},
	}
}

// streamDiagnostics reports a failed conversion into a list resource's
// results stream, which is the only channel List has: it takes no resp, and
// a stream carrying diagnostics is how the framework surfaces the failure.
func streamDiagnostics() parameterFailure {
	return parameterFailure{
		report: func(attribute, wire, errLocal string) string {
			return fmt.Sprintf("\tstream.Results = list.ListResultsStreamDiagnostics(diag.Diagnostics{\n\t\t\tdiag.NewErrorDiagnostic(\"Invalid %s\", %s.Error()),\n\t\t})\n\t\treturn",
				wire, errLocal)
		},
		imports: []string{
			"github.com/hashicorp/terraform-plugin-framework/diag",
			"github.com/hashicorp/terraform-plugin-framework/list",
		},
	}
}

// buildCallPlan renders one bound call. payloadName names the success
// payload local; nodes and modelVar say where parameter values come from;
// fail says how a conversion that cannot succeed reports itself.
func buildCallPlan(call *sdkbind.Call, payloadName string, nodes []node, modelVar string, fail parameterFailure) (finalisedAPIRequest, error) {
	var plan finalisedAPIRequest

	var declarations []string
	for position, p := range call.Parameters {
		// The last path parameter addresses the object itself, which is what
		// the id attribute holds however the API spells the parameter. The
		// fallback is offered on position rather than on the call taking
		// exactly one parameter: only a flat API is single-parameter, and
		// /enterprises/{enterprise}/code-security/configurations/{configuration_id}
		// takes two while still naming one object.
		n, err := parameterNode(p, nodes, position == len(call.Parameters)-1)
		if err != nil {
			return finalisedAPIRequest{}, err
		}
		declaration, needs, err := parameterDeclaration(p, modelVar, ir.GoName(n.attribute.Name), n.attribute.Kind, n.attribute.Name, fail)
		if err != nil {
			return finalisedAPIRequest{}, err
		}
		plan.Imports = append(plan.Imports, needs...)
		declarations = append(declarations, declaration)
	}
	plan.ParameterDeclarations = strings.Join(declarations, "\n\t")

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

// parameterField finds the model field a path parameter reads from: the
// attribute speaking the same wire name, its terraform spelling, or — for
// a single-parameter call only — the id attribute, which is how an item
// path names its key in every REST shape the derivation admits.
func parameterField(p sdkbind.CallParameter, nodes []node, idFallback bool) (string, error) {
	n, err := parameterNode(p, nodes, idFallback)
	if err != nil {
		return "", err
	}
	return ir.GoName(n.attribute.Name), nil
}

// parameterNode is paramField's answer before it is reduced to a spelling: the
// attribute itself, whose declared kind decides how its value is read.
func parameterNode(p sdkbind.CallParameter, nodes []node, idFallback bool) (node, error) {
	// A path parameter is a scalar in the URL. An attribute of the same name
	// that is an object is a different thing the document happens to spell
	// the same way — a repository's owner block beside the owner segment of
	// its path — and reading a value out of it does not compile.
	//
	// The first attribute carrying the parameter's wire name answers it.
	// Addressing attributes sit ahead of the id, so a parent the document
	// also spells `id` is answered by the attribute named for that parent;
	// an object's own key, which the document may declare as a property
	// too, is answered by the id, which the create and the import fill.
	for _, n := range nodes {
		if n.attribute.WireName == p.Wire && n.attribute.Nested == nil {
			return n, nil
		}
	}
	snake := ir.TerraformName(p.Wire)
	for _, n := range nodes {
		if n.attribute.Name == snake && n.attribute.Nested == nil {
			return n, nil
		}
	}
	if idFallback {
		for _, n := range nodes {
			if n.attribute.Name == idAttributeName && n.attribute.Nested == nil {
				return n, nil
			}
		}
	}
	return node{}, unrenderable("path parameter %q matches no scalar attribute and the entity has no id attribute", p.Wire)
}

// namesAnAttribute reports whether a path parameter matches an attribute by
// name, without the id fallback paramNode offers.
//
// A datasource plans from configuration alone. The fallback answers a
// parameter with the entity's id, which a resource knows before its read and
// a datasource never does — its id is computed, so at plan time it is empty
// and the call is made with nothing.
func namesAnAttribute(p sdkbind.CallParameter, nodes []node) bool {
	snake := ir.TerraformName(p.Wire)
	for _, n := range nodes {
		if n.attribute.Nested != nil {
			continue
		}
		if n.attribute.WireName == p.Wire || n.attribute.Name == snake {
			return true
		}
	}
	return false
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

// parameterValue renders the expression one path parameter is passed as: the
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
// This answers only the conversions that cannot fail. parameterDeclaration wraps
// it and takes the fallible ones first, because a conversion that can fail
// needs a statement and a diagnostic rather than an expression; a conversion
// neither can spell refuses the entity rather than guessing.
func parameterValue(p sdkbind.CallParameter, modelVar, field string, kind ir.AttributeType) (string, string, error) {
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

// parameterDeclaration renders the statements that bind one path parameter's
// local: an assignment for a conversion that cannot fail, and a parse
// guarded by a diagnostic for one that can. fail says how that diagnostic is
// reported in the method the declaration lands in.
func parameterDeclaration(p sdkbind.CallParameter, modelVar, field string, kind ir.AttributeType, attribute string, fail parameterFailure) (string, []string, error) {
	if kind == ir.TypeString {
		read := modelVar + "." + field + ".ValueString()"
		switch {
		case p.GoType == "uuid.UUID":
			return guardedParse(p, "uuid.Parse("+read+")", "", attribute, fail),
				append([]string{"github.com/google/uuid"}, fail.imports...), nil
		case isIntegerType(p.GoType):
			// The parse is sized to the SDK's own width, so a value the
			// parameter cannot hold is reported against the attribute rather
			// than wrapping in a cast.
			parse, cast := integerParse(p, read)
			return guardedParse(p, parse, cast, attribute, fail),
				append([]string{"strconv"}, fail.imports...), nil
		}
	}

	value, needs, err := parameterValue(p, modelVar, field, kind)
	if err != nil {
		return "", nil, err
	}
	var imports []string
	if needs != "" {
		imports = append(imports, needs)
	}
	return fmt.Sprintf("%s := %s", p.Local, value), imports, nil
}

// guardedParse renders a fallible conversion: the parse, the diagnostic that
// reports its failure against the attribute, and an optional trailing
// statement that narrows the parsed value to the local the call passes.
//
// parse must be a two-value expression yielding a value and an error. When
// cast is empty the parsed value is already the local, so the parse binds it
// directly; otherwise the parse binds an intermediate the cast reads.
func guardedParse(p sdkbind.CallParameter, parse, cast, attribute string, fail parameterFailure) string {
	bound, tail := p.Local, ""
	if cast != "" {
		bound = p.Local + "Parsed"
		tail = "\n\t" + p.Local + " := " + cast
	}
	errLocal := p.Local + "Err"
	return fmt.Sprintf("%s, %s := %s\n\tif %s != nil {\n%s\n\t}%s",
		bound, errLocal, parse, errLocal, fail.report(attribute, p.Wire, errLocal), tail)
}

// integerParse renders the strconv call that reads one integer path parameter
// out of a string field, and the cast that narrows it to the SDK's own type.
// The cast is empty when the parse already yields that type.
//
// Signed and unsigned take different functions because ParseInt rejects
// nothing a uint64 parameter would accept above its own range. A bit size of
// 0 is strconv's spelling for int and uint, whose width is the platform's.
func integerParse(p sdkbind.CallParameter, read string) (parse, cast string) {
	function, bits, parsed := "strconv.ParseInt", integerBits(p.GoType), "int64"
	if strings.HasPrefix(p.GoType, "uint") {
		function, parsed = "strconv.ParseUint", "uint64"
	}
	parse = fmt.Sprintf("%s(%s, 10, %d)", function, read, bits)
	if p.GoType != parsed {
		cast = p.GoType + "(" + p.Local + "Parsed)"
	}
	return parse, cast
}

// integerBits is the bit size strconv parses one integer type at, 0 for the
// platform-width int and uint.
func integerBits(goType string) int {
	switch goType {
	case "int8", "uint8":
		return 8
	case "int16", "uint16":
		return 16
	case "int32", "uint32":
		return 32
	case "int64", "uint64":
		return 64
	default:
		return 0
	}
}

// integerParsedParameters names the attributes a call reaches through a parse
// that only digits survive: the document declares them strings and the
// generated SDK takes an integer, so parameterDeclaration emits strconv.ParseInt.
//
// A fixture value is derived from the document, which says string, and would
// be refused by that parse before the generated test reached an assertion.
// This is what a caller consults to pin those values to something numeric.
func integerParsedParameters(call *sdkbind.Call, nodes []node) map[string]bool {
	if call == nil {
		return nil
	}
	out := map[string]bool{}
	for position, p := range call.Parameters {
		n, err := parameterNode(p, nodes, position == len(call.Parameters)-1)
		if err != nil {
			continue
		}
		if n.attribute.Kind == ir.TypeString && isIntegerType(p.GoType) {
			out[n.attribute.Name] = true
		}
	}
	return out
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
func addPlanImports(set *importSet, plans ...finalisedAPIRequest) {
	for _, plan := range plans {
		for _, name := range plan.Imports {
			set.add("", name)
		}
	}
}
