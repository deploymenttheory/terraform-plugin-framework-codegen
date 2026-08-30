package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// scopedListResource is the fictional tree with its list resource moved
// behind a parent-scoped collection path, which is the shape most of a
// parent-scoped document takes.
func scopedListResource(t *testing.T) *ServiceFiles {
	t.Helper()
	m, b := fictionalModel(), fictionalBindings()
	m.ListResources[0].ListOperation.PathTemplate = "/v7/tenants/{tenantId}/audit-events"
	m.ListResources[0].ListOperation.PathParameters = []ir.URLPathParameter{{Name: "tenantId", Type: ir.TypeString}}
	m.ListResources[0].AddressingAttributes = &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "tenant_id", WireName: "tenantId", Type: ir.TypeString, ComputedOptionalRequired: ir.Required},
	}}
	b.ListResources["http_server"].List.Parameters = []sdkbind.CallParameter{
		{Local: "tenantId", GoType: "string", Wire: "tenantId"}}

	out, err := RenderServices(fictionalProviderCore(), m, b)
	if err != nil {
		t.Fatalf("a parent-scoped list resource must render: %v", err)
	}
	return out
}

// TestUnit_ListResource_GoesWithTheResourceItLists proves a list resource is
// withheld when its resource is not served. Terraform refuses to load a
// provider whose list resource names no resource, and refuses the whole
// provider rather than that one entity — so emitting it would cost every
// other entity too.
func TestUnit_ListResource_GoesWithTheResourceItLists(t *testing.T) {
	m, b := fictionalModel(), fictionalBindings()
	delete(b.Resources, "http_server")

	out, err := RenderServices(fictionalProviderCore(), m, b)
	if err != nil {
		t.Fatalf("an unserved resource must not fail the run: %v", err)
	}
	for _, f := range out.Files {
		if strings.Contains(f.Path, "list-resources/servers/v7/http_server") {
			t.Fatalf("a list resource was emitted for a resource that is not served: %s", f.Path)
		}
	}
	if len(out.Registrations.ListResources.Registrations) != 0 {
		t.Fatalf("a list resource was registered with no resource to match: %+v",
			out.Registrations.ListResources)
	}

	var said bool
	for _, e := range out.Excluded {
		if e.Key == "http_server" && strings.Contains(e.Reason, "names no resource") {
			said = true
		}
	}
	if !said {
		t.Fatalf("the report does not say why the list resource went: %+v", out.Excluded)
	}
}

// TestUnit_ListResource_ReadsItsPathParametersFromTheListBlock proves a
// collection path's parameters are declared as the list block's own
// configuration and read from there, rather than refusing the entity.
func TestUnit_ListResource_ReadsItsPathParametersFromTheListBlock(t *testing.T) {
	out := scopedListResource(t)
	dir := "internal/services/list-resources/servers/v7/http_server/"

	schema := string(fileByPath(t, out, dir+"list_resource.go").Content)
	for _, want := range []string{
		"Attributes: map[string]listschema.Attribute{",
		`"tenant_id": listschema.StringAttribute{`,
		"Required:",
	} {
		if !strings.Contains(schema, want) {
			t.Errorf("the config schema does not carry %q:\n%s", want, schema)
		}
	}

	model := string(fileByPath(t, out, dir+"model.go").Content)
	if !strings.Contains(model, "type listConfigModel struct {") ||
		!strings.Contains(model, "TenantID types.String `tfsdk:\"tenant_id\"`") {
		t.Errorf("model.go does not declare the config model:\n%s", model)
	}

	list := string(fileByPath(t, out, dir+"list.go").Content)
	for _, want := range []string{
		"var config listConfigModel",
		"req.Config.Get(ctx, &config)",
		"tenantId := config.TenantID.ValueString()",
	} {
		if !strings.Contains(list, want) {
			t.Errorf("List does not read its addressing from the configuration, missing %q:\n%s", want, list)
		}
	}
}

// TestUnit_ListResource_MocksAParameterisedPathByShape proves the generated
// unit test matches the request by pattern, because a parameterised path is
// requested with the addressing substituted in rather than as the template,
// and that it stands a configuration up for List to read.
func TestUnit_ListResource_MocksAParameterisedPathByShape(t *testing.T) {
	out := scopedListResource(t)
	test := string(fileByPath(t, out,
		"internal/services/list-resources/servers/v7/http_server/list_resource_test.go").Content)

	for _, want := range []string{
		"httpmock.RegisterResponder(\"GET\", `=~^",
		`/v7/tenants/([^/]+)/audit-events$`,
		"func listConfig(t *testing.T, lr list.ListResource) tfsdk.Config {",
		"listConfig(t, lr),",
	} {
		if !strings.Contains(test, want) {
			t.Errorf("the generated test does not carry %q:\n%s", want, test)
		}
	}
	if strings.Contains(test, `"{{`) {
		t.Errorf("the generated test carries an unrendered action:\n%s", test)
	}
}

// TestUnit_ListResource_ExampleSuppliesTheRequiredAddressing proves the
// emitted query example sets the attributes the list block requires, so it
// is a configuration terraform would accept rather than one it would reject.
func TestUnit_ListResource_ExampleSuppliesTheRequiredAddressing(t *testing.T) {
	out := scopedListResource(t)
	example := string(fileByPath(t, out,
		"examples/list-resources/petstore_http_server/list-resource.tfquery.hcl").Content)

	if !strings.Contains(example, "tenant_id = ") {
		t.Errorf("the example does not supply the required addressing:\n%s", example)
	}
}

// TestUnit_ListResource_WithoutAddressingDeclaresNoConfiguration proves a
// collection path that takes no parameters is unchanged: an empty list
// block, no config model, and the mock matched by exact URL.
func TestUnit_ListResource_WithoutAddressingDeclaresNoConfiguration(t *testing.T) {
	out := renderFictional(t)
	dir := "internal/services/list-resources/servers/v7/http_server/"

	if schema := string(fileByPath(t, out, dir+"list_resource.go").Content); strings.Contains(schema, "Attributes: map[string]listschema.Attribute{") {
		t.Errorf("an unparameterised collection path must declare an empty list block:\n%s", schema)
	}
	if model := string(fileByPath(t, out, dir+"model.go").Content); strings.Contains(model, "listConfigModel") {
		t.Errorf("an unparameterised collection path needs no config model:\n%s", model)
	}
	if list := string(fileByPath(t, out, dir+"list.go").Content); strings.Contains(list, "req.Config.Get") {
		t.Errorf("an unparameterised collection path reads no configuration:\n%s", list)
	}
	if test := string(fileByPath(t, out, dir+"list_resource_test.go").Content); !strings.Contains(test, `httpmock.RegisterResponder("GET", "https://unit.invalid/v7/http-servers"`) {
		t.Errorf("an unparameterised collection path is mocked by exact URL:\n%s", test)
	}
}

// An API that spells its key after the thing it identifies gives the element
// an id whose wire name is the item path key, which is what derivation now
// puts there. Emission has to publish that as the identity: refusing it lost
// every entity whose document simply worded its key differently.
func TestUnit_ListResource_PublishesAnIdentityKeyedTheAPIsWay(t *testing.T) {
	m, b := fictionalModel(), fictionalBindings()

	lr := &m.ListResources[0]
	if lr.Names.Key != "http_server" {
		t.Fatalf("the fictional list resource moved: %q", lr.Names.Key)
	}
	// The element carries the key as the API words it, not as "id".
	for i := range lr.Attributes.Attributes {
		if lr.Attributes.Attributes[i].Name == "id" {
			lr.Attributes.Attributes[i].WireName = "httpServerId"
		}
	}
	lb := b.ListResources["http_server"]
	fields := make([]sdkbind.FieldBinding, 0, len(lb.Fields))
	for _, f := range lb.Fields {
		if f.Attr == "id" {
			f.Wire = "httpServerId"
			f.Access = readOnly(kiotaAccess("HttpServerId", "*string", "FromPtrString", "", ""))
		}
		fields = append(fields, f)
	}
	lb.Fields = fields

	out, err := RenderServices(fictionalProviderCore(), m, b)
	if err != nil {
		t.Fatalf("an element keyed the API's way must still render: %v", err)
	}
	list := string(fileByPath(t, out, "internal/services/list-resources/servers/v7/http_server/list.go").Content)
	if !strings.Contains(list, "GetHttpServerId()") {
		t.Errorf("the identity is not read from the element's own key:\n%s", list)
	}
	if !strings.Contains(list, "identityModel{ID: types.StringValue(id)}") {
		t.Errorf("the element's key is not published as the identity:\n%s", list)
	}
}

// The refusal an element with no key at all still earns has to say what it
// looked for and what the element does carry, or the only way to write the
// correction is to read the toolkit.
func TestUnit_ListResource_ExclusionNamesWhatTheElementCarries(t *testing.T) {
	m, b := fictionalModel(), fictionalBindings()

	lr := &m.ListResources[0]
	kept := make([]ir.Attribute, 0, len(lr.Attributes.Attributes))
	for _, a := range lr.Attributes.Attributes {
		if a.Name == "id" {
			a.Name, a.WireName = "server_uid", "serverUid"
		}
		kept = append(kept, a)
	}
	lr.Attributes.Attributes = kept
	lb := b.ListResources["http_server"]
	fields := make([]sdkbind.FieldBinding, 0, len(lb.Fields))
	for _, f := range lb.Fields {
		if f.Attr == "id" {
			f.Attr, f.Wire = "server_uid", "serverUid"
			f.Access = readOnly(kiotaAccess("ServerUid", "*string", "FromPtrString", "", ""))
		}
		fields = append(fields, f)
	}
	lb.Fields = fields

	out, err := RenderServices(fictionalProviderCore(), m, b)
	if err != nil {
		t.Fatalf("one refused list resource must not fail the run: %v", err)
	}

	var reason string
	for _, e := range out.Excluded {
		if e.Key == "http_server" {
			reason = e.Reason
		}
	}
	if reason == "" {
		t.Fatalf("the refusal was not reported: %+v", out.Excluded)
	}
	for _, want := range []string{`no readable scalar "id"`, "serverUid"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the refusal does not mention %q: %s", want, reason)
		}
	}
}
