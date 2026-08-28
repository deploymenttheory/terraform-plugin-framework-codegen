package emit

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/fixtures"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// actionData is the render context every action template consumes.
type actionData struct {
	Source        string
	Package       string
	PackagePath   string
	Key           string
	Pascal        string
	Type          string
	TerraformType string
	ClientType    string

	Imports       string
	ModelImports  string
	InvokeImports string
	TestImports   string

	SchemaDescription string
	SchemaAttributes  string
	Models            string

	HasBody             bool
	ConstructReturnType string
	WriteConstructor    string
	ConstructBody       string
	InvokePlan          finalisedAPIRequest

	InvokeMethod     string
	InvokeMatcher    string
	InvokeStatus     int
	ConfigValue      string
	TestClientConfig string
	AuthGitHubApp    bool
}

// action renders one action's file set.
func (e *serviceRenderer) action(a *ir.Action, ab *sdkbind.ActionBinding) ([]File, error) {
	if ab.Invoke == nil {
		return nil, unrenderable("an action needs a bound invoke call")
	}

	d := &actionData{
		Package:       a.Names.Package,
		PackagePath:   e.packagePath(kindActions, a.Names),
		Key:           a.Names.Key,
		Pascal:        a.Names.PascalCase,
		Type:          a.Names.PascalCase + "Action",
		TerraformType: a.Names.TerraformType,
		ClientType:    "*sdk." + e.bindings.SDK.ClientTypeName,
		HasBody:       a.RequestSchema != nil && ab.WriteModel != "",
		AuthGitHubApp: e.pc.AuthGitHubApp,
	}

	// The schema is the invocation's arguments: one attribute per path
	// parameter, then the request body's tree.
	paramNodes := actionParameterNodes(&a.InvokeOperation)
	bodyNodes := e.joinTree(bindingKindAction, a.Names.Key, a.RequestSchema, ab.Fields)
	nodes := append(append([]node{}, paramNodes...), invocable(bodyNodes)...)

	imports := newImportSet(e.pc.Module)
	imports.add("", "context")
	imports.add("", "fmt")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework/action")
	imports.add("schema", "github.com/hashicorp/terraform-plugin-framework/action/schema")
	imports.add("sdk", e.bindings.SDK.ImportPath)
	sb := &schemaBuilder{kind: schemaAction, imports: imports}
	d.SchemaAttributes = sb.attributeDeclarations(nodes, 3)
	description := entityDescription(a.RequestSchema, "Invokes the "+a.Names.Key+" operation.")
	if a.CoManagementNote != "" {
		description += " " + a.CoManagementNote
	}
	d.SchemaDescription = strconv.Quote(description)
	d.Imports = imports.render()

	declarations := buildModels(d.Type+"Model", d.Pascal, nodes, nil)
	d.Models = renderModelDeclarations(declarations)
	modelImports := newImportSet(e.pc.Module)
	if strings.Contains(d.Models, "types.") {
		modelImports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
	}
	if strings.Contains(d.Models, "attr.") {
		modelImports.add("", "github.com/hashicorp/terraform-plugin-framework/attr")
	}
	d.ModelImports = modelImports.render()

	invokeImports := newImportSet(e.pc.Module)
	invokeImports.add("", "context")
	invokeImports.add("", "github.com/hashicorp/terraform-plugin-framework/action")
	invokeImports.add("", e.pc.Module+"/internal/services/common/errors")

	if d.HasBody {
		d.ConstructReturnType = "*" + ab.WriteModel
		d.WriteConstructor = ab.WriteConstructor
		body, usesFmt, cerr := constructLinesFor(bodyNodes, d.Pascal, "data", "body", "", 1, false)
		if cerr != nil {
			return nil, cerr
		}
		d.ConstructBody = body
		if usesFmt {
			invokeImports.add("", "fmt")
			if strings.Contains(body, "basetypes.") {
				invokeImports.add("", "github.com/hashicorp/terraform-plugin-framework/types/basetypes")
			}
		}
		if strings.Contains(body, "convert.") {
			invokeImports.add("", e.pc.Module+"/internal/services/common/convert")
		}
	}

	plan, err := buildCallPlan(ab.Invoke, "", nodes, "data", respDiagnostics())
	if err != nil {
		return nil, fmt.Errorf("invoke: %w", err)
	}
	d.InvokePlan = plan
	e.addSDKImports(invokeImports, plan.Assign, d.ConstructBody, d.ConstructReturnType, d.WriteConstructor)
	addPlanImports(invokeImports, plan)
	d.InvokeImports = invokeImports.render()

	// Test wiring.
	spec := deriveFixtures(actionTree(paramNodes, a.RequestSchema), nodes)
	spec.PinNumeric(integerParsedParameters(ab.Invoke, nodes))
	d.InvokeMethod = a.InvokeOperation.Method
	d.InvokeStatus = successStatus(&a.InvokeOperation, 204)
	if len(a.InvokeOperation.PathParameters) > 0 {
		d.InvokeMatcher = mockPattern(a.InvokeOperation.PathTemplate)
	} else {
		d.InvokeMatcher = mockURL(a.InvokeOperation.PathTemplate)
	}
	d.ConfigValue = tftypesValue(spec.Entries, 1)
	d.TestClientConfig = e.testClientConfig()

	testImports := newImportSet(e.pc.Module)
	testImports.add("", "context")
	testImports.add("", "testing")
	testImports.add("", "github.com/hashicorp/terraform-plugin-framework/action")
	testImports.add("", "github.com/hashicorp/terraform-plugin-framework/tfsdk")
	testImports.add("", "github.com/hashicorp/terraform-plugin-go/tftypes")
	testImports.add("", "github.com/jarcoal/httpmock")
	testImports.add("", e.pc.Module+"/internal/client")
	testImports.add("", e.pc.Module+"/internal/mocks")
	testImports.add(d.Package, d.PackagePath)
	d.TestImports = testImports.render()

	dir := e.dir(kindActions, a.Names)
	var files []File
	renderGo := func(tmpl, out string) error {
		d.Source = "entity/action/" + tmpl
		f, ferr := e.renderServiceFile("action/"+tmpl, path.Join(dir, out), a.Names.Key, d)
		if ferr != nil {
			return ferr
		}
		files = append(files, f)
		return nil
	}
	for _, gf := range []struct{ tmpl, out string }{
		{"action.go.tmpl", "action.go"},
		{"invoke.go.tmpl", "invoke.go"},
		{"model.go.tmpl", "model.go"},
		{"action_test.go.tmpl", "action_test.go"},
	} {
		if err := renderGo(gf.tmpl, gf.out); err != nil {
			return nil, err
		}
	}

	exampleHeader := fmt.Sprintf("action %q %q", a.Names.TerraformType, "example")
	exampleBody := "  config {\n" + reindent(spec.HCL(fixtures.ConfigMaximal), "  ") + "  }\n"
	example, err := hclBlock(a.Names.Key, exampleHeader, "", exampleBody, nil)
	if err != nil {
		return nil, err
	}
	files = append(files, rawFile(path.Join("examples/actions", a.Names.TerraformType, "action.tf"), a.Names.Key, example))

	return files, nil
}

// actionParameterNodes synthesises the argument attributes an invocation's
// path parameters need: the model does not carry them as schema, but the
// caller must supply them somewhere.
func actionParameterNodes(operation *ir.Operation) []node {
	var out []node
	for _, p := range operation.PathParameters {
		kind := p.Type
		if kind == "" {
			kind = ir.TypeString
		}
		out = append(out, node{attribute: ir.Attribute{
			Name:                     ir.TerraformName(p.Name),
			WireName:                 p.Name,
			Kind:                     kind,
			ComputedOptionalRequired: ir.Required,
		}})
	}
	return out
}

// actionTree is the fixture tree for an action: the synthesised parameter
// attributes ahead of the request tree.
func actionTree(paramNodes []node, request *ir.AttributeTree) *ir.AttributeTree {
	tree := &ir.AttributeTree{}
	for _, n := range paramNodes {
		tree.Attributes = append(tree.Attributes, n.attribute)
	}
	if request != nil {
		tree.Attributes = append(tree.Attributes, request.Attributes...)
	}
	return tree
}

// tftypesValue renders the finished tftypes.NewValue expression carrying
// the fixture values — how the unit test hands Invoke a configuration
// without running terraform.
func tftypesValue(entries []fixtures.Entry, depth int) string {
	indent := strings.Repeat("\t", depth)
	var typeLines, valueLines strings.Builder
	for _, v := range entries {
		fmt.Fprintf(&typeLines, "%s\t\t%q: %s,\n", indent, v.Name, tftype(v))
		fmt.Fprintf(&valueLines, "%s\t\t%q: %s,\n", indent, v.Name, tftypeNewValue(v))
	}
	return fmt.Sprintf("tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{\n%s%s}}, map[string]tftypes.Value{\n%s%s})",
		typeLines.String(), indent, valueLines.String(), indent)
}

// tftype is the tftypes type expression of one fixture value.
func tftype(v fixtures.Entry) string {
	switch {
	case v.Nested != nil && v.Kind == ir.TypeList:
		return "tftypes.List{ElementType: " + tftypeObject(v.Nested) + "}"
	case v.Nested != nil:
		return tftypeObject(v.Nested)
	case v.Kind == ir.TypeList:
		return "tftypes.List{ElementType: " + tftypeScalar(v.ElementType) + "}"
	case v.Kind == ir.TypeMap:
		return "tftypes.Map{ElementType: " + tftypeScalar(v.ElementType) + "}"
	default:
		return tftypeScalar(v.Kind)
	}
}

// tftypeObject renders a nested object's tftypes type.
func tftypeObject(values []fixtures.Entry) string {
	var b strings.Builder
	b.WriteString("tftypes.Object{AttributeTypes: map[string]tftypes.Type{")
	for i, v := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q: %s", v.Name, tftype(v))
	}
	b.WriteString("}}")
	return b.String()
}

// tftypeScalar is the tftypes primitive for one kind.
func tftypeScalar(k ir.AttributeType) string {
	switch k {
	case ir.TypeBool:
		return "tftypes.Bool"
	case ir.TypeInt64, ir.TypeFloat64:
		return "tftypes.Number"
	default:
		return "tftypes.String"
	}
}

// tftypeNewValue renders one fixture value as a tftypes.NewValue call.
func tftypeNewValue(v fixtures.Entry) string {
	switch {
	case v.Nested != nil && v.Kind == ir.TypeList:
		return fmt.Sprintf("tftypes.NewValue(%s, []tftypes.Value{%s})",
			tftype(v), tftypeNewObject(v.Nested))
	case v.Nested != nil:
		return tftypeNewObject(v.Nested)
	case v.Kind == ir.TypeList:
		return fmt.Sprintf("tftypes.NewValue(%s, []tftypes.Value{tftypes.NewValue(%s, %s)})",
			tftype(v), tftypeScalar(v.ElementType), tftypeScalarLiteral(v.ElementType, v.Scalar))
	case v.Kind == ir.TypeMap:
		// One entry, keyed by the attribute's own name: a map's keys are the
		// practitioner's, so the document names none to take.
		return fmt.Sprintf("tftypes.NewValue(%s, map[string]tftypes.Value{%q: tftypes.NewValue(%s, %s)})",
			tftype(v), v.Name, tftypeScalar(v.ElementType), tftypeScalarLiteral(v.ElementType, v.Scalar))
	default:
		return fmt.Sprintf("tftypes.NewValue(%s, %s)", tftypeScalar(v.Kind), tftypeScalarLiteral(v.Kind, v.Scalar))
	}
}

// tftypeNewObject renders a nested object's value expression.
func tftypeNewObject(values []fixtures.Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tftypes.NewValue(%s, map[string]tftypes.Value{", tftypeObject(values))
	for i, v := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q: %s", v.Name, tftypeNewValue(v))
	}
	b.WriteString("})")
	return b.String()
}

// tftypeScalarLiteral is the Go literal one scalar travels as.
func tftypeScalarLiteral(k ir.AttributeType, scalar any) string {
	switch k {
	case ir.TypeBool, ir.TypeInt64, ir.TypeFloat64:
		return checkValue(scalar)
	default:
		return strconv.Quote(fmt.Sprintf("%v", scalar))
	}
}

// invocable keeps the arguments an action can actually take.
//
// An action schema has no Computed: an invocation has arguments and a result,
// and nothing in between for the framework to fill in. The action package's
// attribute types do not declare the field, so an attribute derived as
// computed does not merely read oddly — the generated schema does not
// compile. It arrives that way from a request body property the document
// marks readOnly, which is a contradiction the document is entitled to
// contain and the generator is not entitled to pass on: the practitioner
// cannot send it, and there is nothing to read it back from.
func invocable(nodes []node) []node {
	kept := make([]node, 0, len(nodes))
	for _, n := range nodes {
		if n.attribute.ComputedOptionalRequired == ir.Computed {
			continue
		}
		if n.attribute.Nested != nil {
			n.children = invocable(n.children)
		}
		kept = append(kept, n)
	}
	return kept
}
