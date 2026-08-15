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

// listResourceData is the render context every list-resource template
// consumes.
type listResourceData struct {
	Source        string
	Package       string
	PackagePath   string
	Key           string
	Pascal        string
	Type          string
	TerraformType string
	ClientType    string

	Imports     string
	ListImports string
	TestImports string

	SchemaDescription string
	SchemaAttributes  string
	ConfigModel       string
	// IdentityFields are the struct fields a streamed identity decodes
	// into, and IdentitySchema the schema the generated test stands it up
	// against. Both come from the resource being listed.
	IdentityFields string
	IdentitySchema string
	// ResourceCtor builds the resource being listed, so the test can read
	// the schema terraform would supply.
	ResourceCtor string
	ListPlan     callPlan
	Collection   string
	ResultLines  string

	CollectionURL     string
	CollectionPattern string

	ListResponse     string
	ExpectedFirstID  string
	ConfigValue      string
	TestClientConfig string
	AuthGitHubApp    bool
	ProviderName     string
}

// listConfigModelName is the struct the list block's configuration decodes
// into, and the local List reads it as. Unexported and per-package, so it
// needs no entity prefix.
const listConfigModelName = "listConfigModel"

// resourcePackageAlias is what a list resource's test imports the resource
// package under. The two packages take the same name from one entity, so one
// of them has to be renamed at the import site.
const resourcePackageAlias = "listedresource"

// listResource renders one list-only entity's file set.
func (e *serviceRenderer) listResource(lr *ir.ListResource, lb *sdkbind.ListResourceBinding) ([]File, error) {
	if lb.List == nil {
		return nil, unrenderable("a list resource needs a bound list call")
	}
	nodes := e.joinTree(bindingKindListResource, lr.Names.Key, lr.Schema, lb.Fields, addressingNames(&lr.ListOperation))

	d := &listResourceData{
		Package:       lr.Names.Package,
		PackagePath:   e.packagePath(kindListResources, lr.Names),
		Key:           lr.Names.Key,
		Pascal:        lr.Names.Pascal,
		Type:          lr.Names.Pascal + "ListResource",
		TerraformType: lr.Names.TerraformType,
		ClientType:    "*sdk." + e.bindings.SDK.ClientTypeName,
		ResourceCtor:  resourcePackageAlias + ".New" + lr.Names.Pascal + "Resource()",
		AuthGitHubApp: e.pc.AuthGitHubApp,
		ProviderName:  e.pc.ProviderName,
	}

	description := entityDescription(lr.Schema, "Lists "+lr.Names.Key+" objects. The entity is enumerable but not addressable, so listing is the whole surface.")
	if lr.CoManagementNote != "" {
		description += " " + lr.CoManagementNote
	}
	d.SchemaDescription = strconv.Quote(description)

	// The call's path parameters are read from the list block's own
	// configuration, not from an element: a list resource has no object to
	// address, so the practitioner supplies the scope the collection path
	// names.
	configNodes := joinTree(lr.AddressingSchema, nil, addressingNames(&lr.ListOperation))

	listOp := lr.ListOperation
	plan, err := buildCallPlan(lb.List, "result", configNodes, "config", streamDiagnostics())
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	if plan.Payload == "" {
		return nil, unrenderable("list: the bound list call yields no payload")
	}
	d.ListPlan = plan
	d.Collection = "result"
	if lb.CollectionAccess != "" {
		d.Collection = "result." + lb.CollectionAccess
	}

	identity := e.identities[lr.Names.Key]
	if len(identity) == 0 {
		return nil, unrenderable("the resource it lists declares no identity, and a list result is an identity")
	}
	resultLines, err := listResultLines(nodes, identity, configNodes)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	d.ResultLines = resultLines
	d.IdentityFields = identityModelFields(identity)
	d.IdentitySchema = identitySchemaDecls(identity, 2)

	imports := newImportSet(e.pc.Module)
	imports.add("", "context")
	imports.add("", "fmt")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework/list")
	imports.add("listschema", "github.com/hashicorp/terraform-plugin-framework/list/schema")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework/resource")
	imports.add("sdk", e.bindings.SDK.ImportPath)
	sb := &schemaBuilder{kind: schemaListResource, imports: imports}
	d.SchemaAttributes = sb.attributeDecls(configNodes, 3)
	d.Imports = imports.render()

	if len(configNodes) > 0 {
		d.ConfigModel = renderModelDecls(buildModels(listConfigModelName, lr.Names.Pascal+"ListConfig", configNodes, nil))
	}

	listImports := newImportSet(e.pc.Module)
	listImports.add("", "context")
	listImports.add("", "github.com/hashicorp/terraform-plugin-framework/diag")
	listImports.add("", "github.com/hashicorp/terraform-plugin-framework/list")
	listImports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
	if strings.Contains(d.ResultLines, "fmt.") {
		listImports.add("", "fmt")
	}
	e.addSDKImports(listImports, plan.Assign)
	addPlanImports(listImports, plan)
	d.ListImports = listImports.render()

	spec := deriveFixtures(lr.Schema, nodes)
	configSpec := deriveFixtures(lr.AddressingSchema, configNodes)
	configSpec.PinNumeric(integerParsedParams(lb.List, configNodes))
	// A parameterised collection path is requested with the addressing
	// substituted in, so the mock matches the shape rather than the template,
	// and the unit test stands a configuration up to be read from. The
	// pattern is a regex and travels separately: it carries backslashes a
	// quoted Go string cannot hold.
	if len(configNodes) > 0 {
		d.CollectionPattern = mockPattern(listOp.PathTemplate)
		d.ConfigValue = tftypesValue(configSpec.Entries, 1)
	} else {
		d.CollectionURL = mockURL(listOp.PathTemplate)
	}
	item := strings.TrimSuffix(string(spec.WireJSON(fixtures.ResponseMaximal)), "\n")
	d.ListResponse = listResponseJSON(lr.ListEnvelopeKey, item)
	d.ExpectedFirstID = expectedID(spec)
	d.TestClientConfig = e.testClientConfig()

	testImports := newImportSet(e.pc.Module)
	testImports.add("", "context")
	testImports.add("_", "embed")
	testImports.add("", "net/http")
	testImports.add("", "testing")
	testImports.add("", "github.com/hashicorp/terraform-plugin-framework/list")
	testImports.add("", "github.com/hashicorp/terraform-plugin-framework/resource")
	testImports.add("identityschema", "github.com/hashicorp/terraform-plugin-framework/resource/identityschema")
	testImports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
	if d.ConfigValue != "" {
		testImports.add("", "github.com/hashicorp/terraform-plugin-framework/tfsdk")
		testImports.add("", "github.com/hashicorp/terraform-plugin-go/tftypes")
	}
	testImports.add("", "github.com/jarcoal/httpmock")
	testImports.add("", e.pc.Module+"/internal/client")
	testImports.add("", e.pc.Module+"/internal/mocks")
	testImports.add(d.Package, d.PackagePath)
	// The resource being listed, for its schema and its identity schema:
	// terraform supplies both at runtime, and NewListResult reads them off
	// the request. Aliased because the two packages are named alike.
	testImports.add(resourcePackageAlias, e.packagePath(kindResources, lr.Names))
	d.TestImports = testImports.render()

	dir := e.dir(kindListResources, lr.Names)
	var files []File
	renderGo := func(tmpl, out string) error {
		d.Source = "entity/list-resource/" + tmpl
		f, ferr := e.renderServiceFile("list-resource/"+tmpl, path.Join(dir, out), lr.Names.Key, d)
		if ferr != nil {
			return ferr
		}
		files = append(files, f)
		return nil
	}
	for _, gf := range []struct{ tmpl, out string }{
		{"list_resource.go.tmpl", "list_resource.go"},
		{"list.go.tmpl", "list.go"},
		{"model.go.tmpl", "model.go"},
		{"list_resource_test.go.tmpl", "list_resource_test.go"},
	} {
		if err := renderGo(gf.tmpl, gf.out); err != nil {
			return nil, err
		}
	}

	files = append(files, rawFile(path.Join(dir, "tests/responses/list.json"), lr.Names.Key, []byte(d.ListResponse)))

	example, err := listExample(lr.Names.Key, lr.Names.TerraformType, e.pc.ProviderName,
		configSpec.HCL(fixtures.ConfigMinimal))
	if err != nil {
		return nil, err
	}
	files = append(files, rawFile(
		path.Join("examples/list-resources", lr.Names.TerraformType, "list-resource.tfquery.hcl"),
		lr.Names.Key, example))

	return files, nil
}

// listResultLines renders the per-element body of the results iterator: the
// identity, the display name, and the push.
//
// The identity is the resource's, not this entity's invention — terraform
// reads the schema it must conform to off the resource being listed. Its id
// comes from the element; every other attribute is addressing, which the list
// block's configuration supplied to make the call.
func listResultLines(nodes []node, identity []identityAttribute, config []node) (string, error) {
	idNode, ok := findIdentityNode(nodes)
	if !ok {
		return "", unrenderable("the element carries no scalar id attribute to publish as the list identity")
	}

	configured := map[string]bool{}
	for _, n := range config {
		configured[n.attr.Name] = true
	}
	fields := make([]string, 0, len(identity))
	for _, attribute := range identity {
		if attribute.Name == idAttributeName {
			fields = append(fields, "ID: types.StringValue(id)")
			continue
		}
		if !configured[attribute.Name] {
			return "", unrenderable(
				"the list block cannot supply %q, which the resource's identity names", attribute.Name)
		}
		fields = append(fields, ir.GoName(attribute.Name)+": config."+ir.GoName(attribute.Name))
	}

	displayNode := idNode
	for _, name := range []string{"name", "display_name", "title"} {
		if n, found := findStringNode(nodes, name); found {
			displayNode = n
			break
		}
	}

	var b strings.Builder
	b.WriteString(readStringLocal("id", idNode))
	if displayNode.attr.Name != idNode.attr.Name {
		b.WriteString(readStringLocal("displayName", displayNode))
		b.WriteString("\t\t\tresult.DisplayName = displayName\n")
	} else {
		b.WriteString("\t\t\tresult.DisplayName = id\n")
	}
	fmt.Fprintf(&b, "\t\t\tresult.Diagnostics.Append(result.Identity.Set(ctx, identityModel{%s})...)\n",
		strings.Join(fields, ", "))
	return b.String(), nil
}

// findStringNode finds a plain string attribute by name.
func findStringNode(nodes []node, name string) (node, bool) {
	for _, n := range nodes {
		if n.attr.Name == name && n.attr.Kind == ir.TypeString && n.attr.Nested == nil && n.fb != nil && n.fb.Access.Get != "" {
			return n, true
		}
	}
	return node{}, false
}

// findIdentityNode is findStringNode widened to any scalar, for the one
// attribute that needs it. A list identity is a string, but an API is not
// obliged to key its objects with one: a numeric id is at least as common,
// and requiring the string spelling excluded every entity that has one —
// repositories, issues and organizations among them.
func findIdentityNode(nodes []node) (node, bool) {
	for _, n := range nodes {
		if n.attr.Name != idAttributeName || n.attr.Nested != nil || n.fb == nil || n.fb.Access.Get == "" {
			continue
		}
		switch n.attr.Kind {
		case ir.TypeString, ir.TypeInt64, ir.TypeFloat64, ir.TypeBool:
			return n, true
		}
	}
	return node{}, false
}

// resultLineDepth is the indentation the per-element result body sits at,
// inside the stream closure's loop.
const resultLineDepth = 3

// readStringLocal declares one string local from an element accessor,
// dereferencing when the SDK carries a pointer.
func readStringLocal(local string, n node) string {
	indent := strings.Repeat("\t", resultLineDepth)
	render := func(value string) string { return value }
	// Decided from what the SDK hands back, not from the attribute's kind:
	// an identity declared as a string arrives as uuid.UUID or time.Time
	// often enough, and only the SDK type says whether an assignment
	// compiles. A value that is not already a string goes through fmt,
	// because the identity is a string whatever the API keys its objects
	// with.
	if strings.TrimPrefix(n.fb.Access.SDKType, "*") != "string" {
		render = func(value string) string { return "fmt.Sprintf(\"%v\", " + value + ")" }
	}
	if strings.HasPrefix(n.fb.Access.SDKType, "*") {
		return fmt.Sprintf("%s%s := \"\"\n%sif value := element.%s(); value != nil {\n%s\t%s = %s\n%s}\n",
			indent, local, indent, n.fb.Access.Get, indent, local, render("*value"), indent)
	}
	return fmt.Sprintf("%s%s := %s\n", indent, local, render("element."+n.fb.Access.Get+"()"))
}

// expectedID is the fixture id the list test asserts on.
func expectedID(spec fixtures.Fixture) string {
	for _, v := range spec.Entries {
		if v.Name == "id" {
			return checkValue(v.Scalar)
		}
	}
	return ""
}

// testClientConfig renders the client.Config literal fields a direct-call
// unit test needs beyond the endpoint, per auth method.
func (e *serviceRenderer) testClientConfig() string {
	switch {
	case e.pc.AuthBearerToken, e.pc.AuthAPIKeyHeader:
		return `, APIToken: "unit-test-token"`
	case e.pc.AuthBasic:
		return `, Username: "unit-test-user", Password: "unit-test-password"`
	case e.pc.AuthOAuth2ClientCredentials:
		return `, ClientID: "unit-test-client", ClientSecret: "unit-test-secret", TokenURL: mocks.UnitEndpoint + "/oauth2/token"`
	default:
		return ""
	}
}

// listExample renders the terraform query example. configBody is the
// addressing the list block requires, already rendered as HCL assignments,
// empty for a collection path that takes no parameters.
func listExample(source, terraformType, providerName, configBody string) ([]byte, error) {
	header, err := hashHeader(source)
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf("%s\nlist %q \"example\" {\n  provider = %s\n%s}\n",
		header, terraformType, providerName, configBody)
	return []byte(body), nil
}
