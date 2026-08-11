package emit

import (
	"bytes"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// renderFictional renders the full fictional tree once, failing the test
// on any render error.
func renderFictional(t *testing.T) *ServiceFiles {
	t.Helper()
	out, err := RenderServices(fictionalProviderCore(), fictionalModel(), fictionalBindings())
	if err != nil {
		t.Fatalf("RenderServices: %v", err)
	}
	return out
}

func fileByPath(t *testing.T, out *ServiceFiles, path string) File {
	t.Helper()
	for _, f := range out.Files {
		if f.Path == path {
			return f
		}
	}
	var have []string
	for _, f := range out.Files {
		have = append(have, f.Path)
	}
	t.Fatalf("no rendered file at %s; have:\n%s", path, strings.Join(have, "\n"))
	return File{}
}

// TestUnit_RenderServices_TheFileGrammarIsComplete holds the emitted tree
// to the promised grammar: the four service-kind file sets, their tests,
// mocks, fixtures and examples.
func TestUnit_RenderServices_TheFileGrammarIsComplete(t *testing.T) {
	out := renderFictional(t)

	expected := []string{
		// http_server resource, full featured.
		"internal/services/resources/servers/v7/http_server/resource.go",
		"internal/services/resources/servers/v7/http_server/model.go",
		"internal/services/resources/servers/v7/http_server/construct.go",
		"internal/services/resources/servers/v7/http_server/state.go",
		"internal/services/resources/servers/v7/http_server/crud.go",
		"internal/services/resources/servers/v7/http_server/modify_plan.go",
		"internal/services/resources/servers/v7/http_server/conditional_validators.go",
		"internal/services/resources/servers/v7/http_server/resource_test.go",
		"internal/services/resources/servers/v7/http_server/resource_acceptance_test.go",
		"internal/services/resources/servers/v7/http_server/mocks/responders.go",
		"internal/services/resources/servers/v7/http_server/tests/terraform/unit/resource_minimal.tf",
		"internal/services/resources/servers/v7/http_server/tests/terraform/unit/resource_maximal.tf",
		"internal/services/resources/servers/v7/http_server/tests/terraform/acceptance/resource_minimal.tf",
		"internal/services/resources/servers/v7/http_server/tests/terraform/acceptance/resource_maximal.tf",
		"internal/services/resources/servers/v7/http_server/tests/responses/resource_minimal.json",
		"internal/services/resources/servers/v7/http_server/tests/responses/resource_maximal.json",
		// alert_rule resource, missing update.
		"internal/services/resources/alerts/v7/alert_rule/resource.go",
		"internal/services/resources/alerts/v7/alert_rule/crud.go",
		// datasources.
		"internal/services/datasources/servers/v7/http_server/datasource.go",
		"internal/services/datasources/servers/v7/http_server/read.go",
		"internal/services/datasources/servers/v7/http_server/state.go",
		"internal/services/datasources/servers/v7/http_server/mocks/responders.go",
		"internal/services/datasources/servers/v7/http_server/tests/terraform/unit/datasource.tf",
		"internal/services/datasources/servers/v7/http_server/tests/responses/datasource.json",
		"internal/services/datasources/licenses/v7/license/datasource.go",
		"internal/services/datasources/licenses/v7/license/read.go",
		// list resource.
		"internal/services/list-resources/audit/v7/audit_event/list_resource.go",
		"internal/services/list-resources/audit/v7/audit_event/list.go",
		"internal/services/list-resources/audit/v7/audit_event/model.go",
		"internal/services/list-resources/audit/v7/audit_event/list_resource_test.go",
		"internal/services/list-resources/audit/v7/audit_event/tests/responses/list.json",
		// action.
		"internal/services/actions/servers/v7/http_server_restart/action.go",
		"internal/services/actions/servers/v7/http_server_restart/invoke.go",
		"internal/services/actions/servers/v7/http_server_restart/model.go",
		"internal/services/actions/servers/v7/http_server_restart/action_test.go",
		// examples.
		"examples/resources/petstore_http_server/resource.tf",
		"examples/resources/petstore_http_server/import.sh",
		"examples/data-sources/petstore_http_server/data-source.tf",
		"examples/data-sources/petstore_license/data-source.tf",
		"examples/list-resources/petstore_audit_event/list-resource.tfquery.hcl",
		"examples/actions/petstore_http_server_restart/action.tf",
	}
	for _, p := range expected {
		fileByPath(t, out, p)
	}

	for _, f := range out.Files {
		if f.Source == "" {
			t.Fatalf("%s carries no manifest source", f.Path)
		}
		if strings.HasSuffix(f.Path, ".json") {
			continue // JSON admits no comment, so no header
		}
		if !bytes.Contains(f.Content, []byte("DO NOT EDIT")) {
			t.Fatalf("%s carries no DO-NOT-EDIT header", f.Path)
		}
	}
}

// TestUnit_RenderServices_EveryGoFileParsesGofmtClean holds every
// rendered Go file to the fixed point: parseable, and byte-identical to
// its own gofmt rendering.
func TestUnit_RenderServices_EveryGoFileParsesGofmtClean(t *testing.T) {
	out := renderFictional(t)
	goFiles := 0
	for _, f := range out.Files {
		if !strings.HasSuffix(f.Path, ".go") {
			continue
		}
		goFiles++
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, f.Path, f.Content, parser.ParseComments); err != nil {
			t.Fatalf("%s does not parse: %v\n%s", f.Path, err, f.Content)
		}
		if err := gofmtClean(f.Path, f.Content); err != nil {
			t.Fatal(err)
		}
	}
	if goFiles < 20 {
		t.Fatalf("only %d Go files rendered", goFiles)
	}
}

// TestUnit_RenderServices_RenderingIsDeterministic renders the same model
// twice and holds every byte equal.
func TestUnit_RenderServices_RenderingIsDeterministic(t *testing.T) {
	first := renderFictional(t)
	second := renderFictional(t)

	if len(first.Files) != len(second.Files) {
		t.Fatalf("file counts differ: %d then %d", len(first.Files), len(second.Files))
	}
	for i := range first.Files {
		a, b := first.Files[i], second.Files[i]
		if a.Path != b.Path {
			t.Fatalf("file order differs at %d: %s then %s", i, a.Path, b.Path)
		}
		if !bytes.Equal(a.Content, b.Content) {
			t.Fatalf("%s renders differently across two runs", a.Path)
		}
	}

	for _, kind := range RegistrySlots {
		a, _ := first.Registrations.ByKind(kind)
		b, _ := second.Registrations.ByKind(kind)
		if strings.Join(a.Imports, "\n") != strings.Join(b.Imports, "\n") ||
			strings.Join(a.Registrations, "\n") != strings.Join(b.Registrations, "\n") {
			t.Fatalf("%s registrations render differently across two runs", kind)
		}
	}
}

// TestUnit_RenderServices_TheRenderedCodeCarriesTheDecisions spot-checks
// the load-bearing decisions in the rendered text.
func TestUnit_RenderServices_TheRenderedCodeCarriesTheDecisions(t *testing.T) {
	out := renderFictional(t)

	construct := string(fileByPath(t, out, "internal/services/resources/servers/v7/http_server/construct.go").Content)
	for _, want := range []string{
		"convert.FrameworkToAPIString(data.Name, body.SetName)",
		"convert.FrameworkToAPIInt64AsInt32(data.Port, body.SetPort)",
		"convert.FrameworkToAPIEnum(data.Kind, models.ParseHttpServerKind, body.SetKind)",
		"convert.FrameworkToAPIStringList(ctx, data.Tags, body.SetTags)",
		"if isCreate {",
		"models.NewHttpServerSettings()",
		"models.NewHttpServerRule()",
	} {
		if !strings.Contains(construct, want) {
			t.Fatalf("construct.go lacks %q:\n%s", want, construct)
		}
	}

	state := string(fileByPath(t, out, "internal/services/resources/servers/v7/http_server/state.go").Content)
	for _, want := range []string{
		"data.ID = convert.APIToFrameworkString(remote.GetId())",
		"data.Port = convert.APIToFrameworkInt32AsInt64(remote.GetPort())",
		"data.Kind = convert.APIToFrameworkEnum(remote.GetKind())",
		"data.Tags = convert.APIToFrameworkStringList(remote.GetTags())",
	} {
		if !strings.Contains(state, want) {
			t.Fatalf("state.go lacks %q:\n%s", want, state)
		}
	}

	crud := string(fileByPath(t, out, "internal/services/resources/servers/v7/http_server/crud.go").Content)
	for _, want := range []string{
		"const eventualConsistency = 30 * time.Second",
		"opts.ConsistencyPredicate = consistencyPredicate",
		"crud.DeleteWithRetry",
		"errors.HandleCreateError",
	} {
		if !strings.Contains(crud, want) {
			t.Fatalf("crud.go lacks %q:\n%s", want, crud)
		}
	}

	alertCrud := string(fileByPath(t, out, "internal/services/resources/alerts/v7/alert_rule/crud.go").Content)
	if !strings.Contains(alertCrud, "Update is not supported") {
		t.Fatalf("a missing-update resource must refuse Update:\n%s", alertCrud)
	}

	validators := string(fileByPath(t, out, "internal/services/resources/servers/v7/http_server/conditional_validators.go").Content)
	for _, want := range []string{
		// The edges are wired through the resource's ConfigValidators method.
		"var _ resource.ResourceWithConfigValidators = (*HTTPServerResource)(nil)",
		"func (r *HTTPServerResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {",
		// mutually-exclusive: the stock Conflicting, not a hand-rolled check.
		`resourcevalidator.Conflicting(path.MatchRoot("protocols"), path.MatchRoot("tags")),`,
		// The value-conditional edges are named custom validators, each keyed
		// on its gate and wired into the same slice.
		"kindRequiredWhenValidator{},",
		"kindValidWhenValidator{},",
		"kindValidConfigurationValidator{},",
		"type kindRequiredWhenValidator struct{}",
		"func (v kindRequiredWhenValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {",
		// required-when: settings required when kind == advanced
		`data.Kind.ValueString() == "advanced"`,
		"data.Settings == nil",
		// valid-when: rules valid only under kind == advanced
		`data.Kind.ValueString() != "advanced"`,
		"if data.Rules != nil {",
		// valid-configuration: port only under basic, settings only under advanced
		`if data.Settings != nil && data.Kind.ValueString() != "advanced" {`,
		`if !data.Port.IsNull() && data.Kind.ValueString() != "basic" {`,
	} {
		if !strings.Contains(validators, want) {
			t.Fatalf("conditional_validators.go misses %q:\n%s", want, validators)
		}
	}
	// depends-on is realized as an attribute-level AlsoRequires on the subject
	// attribute in the schema, not as a check in the validators file.
	resourceGo := string(fileByPath(t, out, "internal/services/resources/servers/v7/http_server/resource.go").Content)
	if want := `[]validator.Float64{float64validator.AlsoRequires(path.MatchRoot("port"))}`; !strings.Contains(resourceGo, want) {
		t.Fatalf("resource.go misses the AlsoRequires dependency validator %q:\n%s", want, resourceGo)
	}
	if strings.Contains(validators, "mutually exclusive") || strings.Contains(validators, "may be set only when port is also set") {
		t.Fatalf("the hand-rolled dependency/exclusion prose must be gone:\n%s", validators)
	}

	license := string(fileByPath(t, out, "internal/services/datasources/licenses/v7/license/datasource.go").Content)
	if !strings.Contains(license, `"license_key": schema.StringAttribute{`) {
		t.Fatalf("the lookup datasource must carry its key argument:\n%s", license)
	}

	maximalTf := string(fileByPath(t, out, "internal/services/resources/servers/v7/http_server/tests/terraform/unit/resource_maximal.tf").Content)
	if !strings.Contains(maximalTf, "# blob skipped: free-form object declares no properties") {
		t.Fatalf("the maximal fixture must state its skips:\n%s", maximalTf)
	}
	if !strings.Contains(maximalTf, `= "tfpfgen-test-name"`) {
		t.Fatalf("the maximal fixture must carry derived values:\n%s", maximalTf)
	}

	responses := string(fileByPath(t, out, "internal/services/resources/servers/v7/http_server/tests/responses/resource_maximal.json").Content)
	responders := string(fileByPath(t, out, "internal/services/resources/servers/v7/http_server/mocks/responders.go").Content)
	if !strings.Contains(responders, `"name": "tfpfgen-test-name"`) || !strings.Contains(responses, `"name": "tfpfgen-test-name"`) {
		t.Fatal("the responder constants and the response fixtures must carry the same derived values")
	}

	regs := out.Registrations
	if len(regs.Resources.Imports) != 2 || len(regs.Datasources.Imports) != 2 ||
		len(regs.ListResources.Imports) != 1 || len(regs.Actions.Imports) != 1 {
		t.Fatalf("registration counts are wrong: %+v", regs)
	}
	if regs.Resources.Registrations[0] != "serversV7HttpServer.NewHTTPServerResource," {
		t.Fatalf("registration line = %q", regs.Resources.Registrations[0])
	}
	if !strings.Contains(regs.Resources.Imports[0], `serversV7HttpServer "example.test/provider/internal/services/resources/servers/v7/http_server"`) {
		t.Fatalf("import line = %q", regs.Resources.Imports[0])
	}
}

// TestUnit_RenderServices_ListEnvelopeIsDataDriven proves the ListWrap defect
// is closed: the generated list mock keys its envelope on the entity's
// observed list envelope key, not a hardcoded "value" — and unwraps a bare
// array when the response carries no envelope.
func TestUnit_RenderServices_ListEnvelopeIsDataDriven(t *testing.T) {
	pc := fictionalProviderCore()

	// A vendor that wraps its collection under its own key.
	m := fictionalModel()
	m.Resources[0].ListEnvelopeKey = "http_servers"
	m.Datasources[0].ListEnvelopeKey = "http_servers"
	m.ListResources[0].ListEnvelopeKey = "records"
	out, err := RenderServices(pc, m, fictionalBindings())
	if err != nil {
		t.Fatalf("RenderServices: %v", err)
	}
	resResp := string(fileByPath(t, out, "internal/services/resources/servers/v7/http_server/mocks/responders.go").Content)
	if !strings.Contains(resResp, `providermocks.SuccessResponse(200, map[string]any{"http_servers": items})`) {
		t.Fatalf("resource list mock ignores the envelope key:\n%s", resResp)
	}
	dsResp := string(fileByPath(t, out, "internal/services/datasources/servers/v7/http_server/mocks/responders.go").Content)
	if !strings.Contains(dsResp, `map[string]any{"http_servers": []map[string]any{object()}}`) {
		t.Fatalf("datasource list mock ignores the envelope key:\n%s", dsResp)
	}
	listJSON := string(fileByPath(t, out, "internal/services/list-resources/audit/v7/audit_event/tests/responses/list.json").Content)
	if !strings.Contains(listJSON, `"records": [`) {
		t.Fatalf("list-resource fixture ignores the envelope key:\n%s", listJSON)
	}

	// A vendor that returns a bare array: no wrapper object at all.
	m = fictionalModel()
	m.Resources[0].ListEnvelopeKey = ""
	out, err = RenderServices(pc, m, fictionalBindings())
	if err != nil {
		t.Fatalf("RenderServices: %v", err)
	}
	bare := string(fileByPath(t, out, "internal/services/resources/servers/v7/http_server/mocks/responders.go").Content)
	if !strings.Contains(bare, "providermocks.SuccessResponse(200, items)") {
		t.Fatalf("a bare-array list must not be wrapped:\n%s", bare)
	}
	if strings.Contains(bare, `map[string]any{"`) {
		t.Fatalf("a bare-array list must emit no envelope object:\n%s", bare)
	}
}
