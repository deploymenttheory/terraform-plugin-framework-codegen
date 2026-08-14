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
	if !strings.Contains(config, `filter_type = "all"`) {
		t.Errorf("the configuration omits the filter:\n%s", config)
	}
}
