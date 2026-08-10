package emit

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/fixturespec"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/sdkbind"
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
	ListPlan          callPlan
	Collection        string
	ResultLines       string

	CollectionURL    string
	ListWrap         string
	ListResponse     string
	ExpectedFirstID  string
	TestClientConfig string
	AuthGitHubApp    bool
	ProviderName     string
}

// listResource renders one list-only entity's file set.
func (e *entityRenderer) listResource(lr *ir.ListResource, lb *sdkbind.ListResourceBinding) ([]File, error) {
	if lb.List == nil {
		return nil, fmt.Errorf("a list resource needs a bound list call")
	}
	nodes := joinTree(lr.Schema, lb.Fields)

	d := &listResourceData{
		Package:       lr.Names.Package,
		PackagePath:   e.packagePath(kindListResources, lr.Names),
		Key:           lr.Names.Key,
		Pascal:        lr.Names.Pascal,
		Type:          lr.Names.Pascal + "ListResource",
		TerraformType: lr.Names.TerraformType,
		ClientType:    "*sdk." + e.bindings.SDK.ClientTypeName,
		AuthGitHubApp: e.pc.AuthGitHubApp,
		ProviderName:  e.pc.ProviderName,
	}

	description := "Lists " + lr.Names.Key + " objects. The entity is enumerable but not addressable, so listing is the whole surface."
	if lr.CoManagementNote != "" {
		description += " " + lr.CoManagementNote
	}
	d.SchemaDescription = strconv.Quote(description)

	listOp := lr.ListOp
	plan, err := buildCallPlan(lb.List, "result", nodes, "data")
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	if plan.Payload == "" {
		return nil, fmt.Errorf("list: the bound list call yields no payload")
	}
	if plan.ParamDecls != "" {
		return nil, fmt.Errorf("list: a list resource cannot supply path parameters; the call needs %q", lb.List.Params[0].Wire)
	}
	d.ListPlan = plan
	d.Collection = "result"
	if lb.CollectionAccess != "" {
		d.Collection = "result." + lb.CollectionAccess
	}

	resultLines, err := listResultLines(nodes)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	d.ResultLines = resultLines

	imports := newImportSet(e.pc.Module)
	imports.add("", "context")
	imports.add("", "fmt")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework/list")
	imports.add("listschema", "github.com/hashicorp/terraform-plugin-framework/list/schema")
	imports.add("", "github.com/hashicorp/terraform-plugin-framework/resource")
	imports.add("sdk", e.bindings.SDK.ImportPath)
	d.Imports = imports.render()

	listImports := newImportSet(e.pc.Module)
	listImports.add("", "context")
	listImports.add("", "github.com/hashicorp/terraform-plugin-framework/diag")
	listImports.add("", "github.com/hashicorp/terraform-plugin-framework/list")
	listImports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
	e.addSDKImports(listImports, plan.Assign)
	d.ListImports = listImports.render()

	spec := deriveFixtures(lr.Schema, nodes)
	d.CollectionURL = mockURL(listOp.PathTemplate)
	d.ListWrap = "value"
	item := strings.TrimSuffix(string(spec.WireJSON(fixturespec.ResponseMaximal)), "\n")
	d.ListResponse = "{\n  \"value\": [\n" + reindentJSON(item, "    ") + "\n  ]\n}\n"
	d.ExpectedFirstID = expectedID(spec)
	d.TestClientConfig = e.testClientConfig()

	testImports := newImportSet(e.pc.Module)
	testImports.add("", "context")
	testImports.add("_", "embed")
	testImports.add("", "testing")
	testImports.add("", "github.com/hashicorp/terraform-plugin-framework/list")
	testImports.add("", "github.com/hashicorp/terraform-plugin-framework/resource")
	testImports.add("identityschema", "github.com/hashicorp/terraform-plugin-framework/resource/identityschema")
	testImports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
	testImports.add("", "github.com/jarcoal/httpmock")
	testImports.add("", e.pc.Module+"/internal/client")
	testImports.add("", e.pc.Module+"/internal/mocks")
	testImports.add(d.Package, d.PackagePath)
	d.TestImports = testImports.render()

	dir := e.dir(kindListResources, lr.Names)
	var files []File
	renderGo := func(tmpl, out string) error {
		d.Source = "entity/list-resource/" + tmpl
		f, ferr := e.renderEntityFile("list-resource/"+tmpl, path.Join(dir, out), lr.Names.Key, d)
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

	example, err := listExample(lr.Names.Key, lr.Names.TerraformType, e.pc.ProviderName)
	if err != nil {
		return nil, err
	}
	files = append(files, rawFile(
		path.Join("examples/list-resources", lr.Names.TerraformType, "list-resource.tfquery.hcl"),
		lr.Names.Key, example))

	return files, nil
}

// listResultLines renders the per-element body of the results iterator:
// the identity id, the display name, and the push.
func listResultLines(nodes []node) (string, error) {
	idNode, ok := findStringNode(nodes, "id")
	if !ok {
		return "", fmt.Errorf("the element carries no id attribute to publish as the list identity")
	}

	displayNode := idNode
	for _, name := range []string{"name", "display_name", "title"} {
		if n, found := findStringNode(nodes, name); found {
			displayNode = n
			break
		}
	}

	var b strings.Builder
	b.WriteString(readStringLocal("id", idNode, 3))
	if displayNode.attr.Name != idNode.attr.Name {
		b.WriteString(readStringLocal("displayName", displayNode, 3))
		b.WriteString("\t\t\tresult.DisplayName = displayName\n")
	} else {
		b.WriteString("\t\t\tresult.DisplayName = id\n")
	}
	b.WriteString("\t\t\tresult.Diagnostics.Append(result.Identity.Set(ctx, identityModel{ID: types.StringValue(id)})...)\n")
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

// readStringLocal declares one string local from an element accessor,
// dereferencing when the SDK carries a pointer.
func readStringLocal(local string, n node, depth int) string {
	indent := strings.Repeat("\t", depth)
	if strings.HasPrefix(n.fb.Access.SDKType, "*") {
		return fmt.Sprintf("%s%s := \"\"\n%sif value := element.%s(); value != nil {\n%s\t%s = *value\n%s}\n",
			indent, local, indent, n.fb.Access.Get, indent, local, indent)
	}
	return fmt.Sprintf("%s%s := element.%s()\n", indent, local, n.fb.Access.Get)
}

// expectedID is the fixture id the list test asserts on.
func expectedID(spec fixturespec.Spec) string {
	for _, v := range spec.Values {
		if v.Name == "id" {
			return checkValue(v.Scalar)
		}
	}
	return ""
}

// testClientConfig renders the client.Config literal fields a direct-call
// unit test needs beyond the endpoint, per auth method.
func (e *entityRenderer) testClientConfig() string {
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

// listExample renders the terraform query example.
func listExample(source, terraformType, providerName string) ([]byte, error) {
	header, err := hashHeader(source)
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf("%s\nlist %q \"example\" {\n  provider = %s\n}\n", header, terraformType, providerName)
	return []byte(body), nil
}
