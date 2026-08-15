package emit

import (
	"regexp"
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// scopedCompanion renders the fictional tree with its companion datasource
// moved behind a parent-scoped collection path, which is the shape most of a
// parent-scoped document takes.
func scopedCompanion(t *testing.T) *ServiceFiles {
	t.Helper()
	m, b := fictionalModel(), fictionalBindings()

	ds := &m.Datasources[0]
	if ds.Names.Key != "http_server" {
		t.Fatalf("the fictional companion datasource moved: %q", ds.Names.Key)
	}
	ds.Operations.List = &ir.Operation{
		Kind: ir.OperationList, Method: "GET",
		PathTemplate:   "/v7/tenants/{tenantId}/http-servers",
		PathParameters: []ir.Parameter{{Name: "tenantId", Type: ir.TypeString}},
		SuccessCode:    200,
	}
	ds.Schema.Attributes = append([]ir.Attribute{{
		Name: "tenant_id", WireName: "tenantId", Kind: ir.TypeString,
		ComputedOptionalRequired: ir.Required,
	}}, ds.Schema.Attributes...)
	b.Datasources["http_server"].List.Params = []sdkbind.CallParam{
		{Local: "tenantId", GoType: "string", Wire: "tenantId"}}

	out, err := RenderServices(fictionalProviderCore(), m, b)
	if err != nil {
		t.Fatalf("a parent-scoped companion datasource must render: %v", err)
	}
	return out
}

// TestUnit_CompanionDatasource_DeclaresTheAddressingItsModelCarries is the
// invariant terraform enforces at decode time: it refuses a model whose
// struct has a field the schema does not declare, naming neither. The schema
// and the model are rendered separately here, so nothing else holds them
// together.
func TestUnit_CompanionDatasource_DeclaresTheAddressingItsModelCarries(t *testing.T) {
	out := scopedCompanion(t)
	dir := "internal/services/datasources/servers/v7/http_server/"

	schema := string(fileByPath(t, out, dir+"datasource.go").Content)
	if !strings.Contains(schema, `"tenant_id": schema.StringAttribute{`) {
		t.Errorf("the schema does not declare the addressing:\n%s", schema)
	}

	model := string(fileByPath(t, out, dir+"model.go").Content)
	if !regexp.MustCompile("TenantID\\s+types.String\\s+`tfsdk:\"tenant_id\"`").MatchString(model) {
		t.Errorf("the model does not carry the addressing:\n%s", model)
	}

	// Every tfsdk tag in the root model must have a schema attribute of the
	// same name, or terraform refuses to decode.
	root := model[strings.Index(model, "type HTTPServerDatasourceModel struct {"):]
	root = root[:strings.Index(root, "\n}")]
	for _, m := range regexp.MustCompile("tfsdk:\"([a-z0-9_]+)\"").FindAllStringSubmatch(root, -1) {
		if m[1] == "timeouts" {
			continue
		}
		if !strings.Contains(schema, `"`+m[1]+`": schema.`) {
			t.Errorf("the model carries %q with no schema attribute to decode it from", m[1])
		}
	}
}

// A companion datasource is only useful if a caller can name the object it
// wants. Without filters the collection comes back whole and HCL has to
// address a result by its position in it, which no API promises to keep.
func TestUnit_CompanionDatasource_FiltersOnEveryRootScalar(t *testing.T) {
	out := scopedCompanion(t)
	dir := "internal/services/datasources/servers/v7/http_server/"

	schema := string(fileByPath(t, out, dir+"datasource.go").Content)
	model := string(fileByPath(t, out, dir+"model.go").Content)
	read := string(fileByPath(t, out, dir+"read.go").Content)

	// The fictional item carries id and name as strings and enabled as a
	// bool; each is offered as an optional argument of its own type.
	for decl, want := range map[string]string{
		"id":      "schema.StringAttribute{",
		"name":    "schema.StringAttribute{",
		"enabled": "schema.BoolAttribute{",
	} {
		if !strings.Contains(schema, `"`+decl+`": `+want) {
			t.Errorf("%q is not offered as a filter of its own type:\n%s", decl, schema)
		}
	}
	if !regexp.MustCompile("Enabled\\s+types.Bool\\s+`tfsdk:\"enabled\"`").MatchString(model) {
		t.Errorf("the model carries no field for the enabled filter:\n%s", model)
	}

	// One check per filter, comparing terraform values, and skipping a
	// filter the configuration left null.
	for _, field := range []string{"ID", "Name", "Enabled"} {
		want := "if !config." + field + ".IsNull() && !config." + field + ".Equal(item." + field + ")"
		if !strings.Contains(read, want) {
			t.Errorf("the match does not consult %s:\n%s", field, read)
		}
	}
	if !strings.Contains(read, "if !matches(&data, &item) {") {
		t.Errorf("the listing does not apply the filters:\n%s", read)
	}
	// An id names one object and the API can answer for it, so the read
	// asks for it rather than listing the collection to discard the rest.
	if !strings.Contains(read, "if !data.ID.IsNull() {") {
		t.Errorf("the id filter does not reach the by-id read:\n%s", read)
	}
}

// TestUnit_CompanionDatasource_MocksAParameterisedCollectionByShape proves
// the generated mock matches the request the addressing produces, and that
// the configuration supplies what the schema requires.
func TestUnit_CompanionDatasource_MocksAParameterisedCollectionByShape(t *testing.T) {
	out := scopedCompanion(t)
	dir := "internal/services/datasources/servers/v7/http_server/"

	responders := string(fileByPath(t, out, dir+"mocks/responders.go").Content)
	if !strings.Contains(responders, "collectionURL = `=~^") {
		t.Errorf("a parameterised collection is not matched by shape:\n%s", responders)
	}

	config := string(fileByPath(t, out, dir+"tests/terraform/unit/datasource.tf").Content)
	if !strings.Contains(config, "tenant_id") {
		t.Errorf("the configuration omits an argument the schema requires:\n%s", config)
	}
	// Every filter is optional, so the fixture sets none: it asserts the
	// whole collection comes back, and a filter in it would assert that
	// every fixture object matched.
	schema := string(fileByPath(t, out, dir+"datasource.go").Content)
	if !strings.Contains(schema, `"name": schema.StringAttribute{`) {
		t.Errorf("a root field of the listed object is not offered as a filter:\n%s", schema)
	}
}
