package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/sdkbind"
)

// expectRenderError renders and requires a failure whose message carries
// every given fragment.
func expectRenderError(t *testing.T, pc ProviderCore, m *ir.Model, b *sdkbind.Bindings, fragments ...string) {
	t.Helper()
	_, err := RenderServices(pc, m, b)
	if err == nil {
		t.Fatalf("rendering must fail (%s)", strings.Join(fragments, ", "))
	}
	for _, fragment := range fragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("the error %q does not carry %q", err, fragment)
		}
	}
}

func TestUnit_RenderServices_RefusesAnUnrenderableContext(t *testing.T) {
	if _, err := RenderServices(ProviderCore{}, fictionalModel(), fictionalBindings()); err == nil {
		t.Fatal("an empty provider-core context must be refused")
	}
	if _, err := RenderServices(fictionalProviderCore(), nil, fictionalBindings()); err == nil {
		t.Fatal("a nil model must be refused")
	}
	if _, err := RenderServices(fictionalProviderCore(), fictionalModel(), nil); err == nil {
		t.Fatal("nil bindings must be refused")
	}
}

func TestUnit_RenderServices_SkipsEntitiesTheBindingsLack(t *testing.T) {
	b := fictionalBindings()
	delete(b.Resources, "alert_rule")
	delete(b.Datasources, "license")
	delete(b.ListResources, "audit_event")
	delete(b.Actions, "http_server_restart")

	out, err := RenderServices(fictionalProviderCore(), fictionalModel(), b)
	if err != nil {
		t.Fatalf("RenderServices: %v", err)
	}
	for _, f := range out.Files {
		if strings.Contains(f.Path, "alert_rule") || strings.Contains(f.Path, "license") ||
			strings.Contains(f.Path, "audit_event") || strings.Contains(f.Path, "http_server_restart") {
			t.Fatalf("a pruned entity must render nothing, got %s", f.Path)
		}
	}
	if len(out.Registrations.ListResources.Imports) != 0 || len(out.Registrations.Actions.Imports) != 0 {
		t.Fatalf("a pruned entity must register nothing: %+v", out.Registrations)
	}
}

func TestUnit_RenderServices_NamesTheEntityAndAttributeAtFault(t *testing.T) {
	pc := fictionalProviderCore()

	// A resource without a bound delete call.
	m, b := fictionalModel(), fictionalBindings()
	b.Resources["http_server"].Delete = nil
	expectRenderError(t, pc, m, b, "http_server", "delete")

	// A conversion the catalog cannot bridge.
	m, b = fictionalModel(), fictionalBindings()
	b.Resources["http_server"].Fields[1].Access.ConvertSet = "ToWeird"
	expectRenderError(t, pc, m, b, "http_server", "name", "ToWeird")

	// A read-direction conversion the catalog cannot bridge.
	m, b = fictionalModel(), fictionalBindings()
	b.Resources["http_server"].Fields[0].Access.ConvertGet = "FromWeird"
	expectRenderError(t, pc, m, b, "http_server", "id", "FromWeird")

	// A conditional requirement naming a missing attribute.
	m, b = fictionalModel(), fictionalBindings()
	m.Resources[0].Schema.ConditionalRequirements = []ir.ConditionalRequirement{
		{Property: "ghost", Equals: "x", Required: []string{"name"}}}
	expectRenderError(t, pc, m, b, "http_server", "ghost")

	// A conditional requirement on a non-string attribute.
	m, b = fictionalModel(), fictionalBindings()
	m.Resources[0].Schema.ConditionalRequirements = []ir.ConditionalRequirement{
		{Property: "enabled", Equals: "true", Required: []string{"name"}}}
	expectRenderError(t, pc, m, b, "http_server", "enabled")

	// A conditional requirement requiring a missing attribute.
	m, b = fictionalModel(), fictionalBindings()
	m.Resources[0].Schema.ConditionalRequirements = []ir.ConditionalRequirement{
		{Property: "kind", Equals: "advanced", Required: []string{"ghost"}}}
	expectRenderError(t, pc, m, b, "http_server", "ghost")

	// A path parameter no attribute can feed.
	m, b = fictionalModel(), fictionalBindings()
	b.Resources["http_server"].Read.Params = []sdkbind.CallParam{
		{Local: "a", GoType: "string", Wire: "aId"}, {Local: "b", GoType: "string", Wire: "bId"}}
	expectRenderError(t, pc, m, b, "http_server", "aId")

	// Two nested objects claiming one model struct name.
	m, b = fictionalModel(), fictionalBindings()
	inner := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "inner", WireName: "inner", Kind: ir.TypeObject, Presence: ir.PresenceOptional,
			Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
				{Name: "x", WireName: "x", Kind: ir.TypeString, Presence: ir.PresenceOptional}}}},
	}}
	m.Resources[0].Schema.Attributes = append(m.Resources[0].Schema.Attributes,
		ir.Attribute{Name: "one", WireName: "one", Kind: ir.TypeObject, Presence: ir.PresenceOptional, Nested: inner},
		ir.Attribute{Name: "two", WireName: "two", Kind: ir.TypeObject, Presence: ir.PresenceOptional, Nested: inner},
	)
	innerBinding := func() []sdkbind.FieldBinding {
		return []sdkbind.FieldBinding{{Attr: "inner", Wire: "inner", Kind: ir.TypeObject,
			Access:            sdkbind.FieldAccess{Get: "GetInner", Set: "SetInner", SDKType: "models.Innerable"},
			NestedModel:       "models.Inner",
			NestedWriteModel:  "models.Inner",
			NestedConstructor: "models.NewInner()",
			Nested: []sdkbind.FieldBinding{{Attr: "x", Wire: "x", Kind: ir.TypeString,
				Access: kAccess("X", "*string", "FromPtrString", "ToPtrString", "")}},
		}}
	}
	b.Resources["http_server"].Fields = append(b.Resources["http_server"].Fields,
		sdkbind.FieldBinding{Attr: "one", Wire: "one", Kind: ir.TypeObject,
			Access:      sdkbind.FieldAccess{Get: "GetOne", Set: "SetOne", SDKType: "models.Oneable"},
			NestedModel: "models.One", NestedWriteModel: "models.One", NestedConstructor: "models.NewOne()",
			Nested: innerBinding()},
		sdkbind.FieldBinding{Attr: "two", Wire: "two", Kind: ir.TypeObject,
			Access:      sdkbind.FieldAccess{Get: "GetTwo", Set: "SetTwo", SDKType: "models.Twoable"},
			NestedModel: "models.Two", NestedWriteModel: "models.Two", NestedConstructor: "models.NewTwo()",
			Nested: innerBinding()},
	)
	expectRenderError(t, pc, m, b, "HTTPServerInnerModel")

	// A list resource whose element carries no id.
	m, b = fictionalModel(), fictionalBindings()
	m.ListResources[0].Schema.Attributes = m.ListResources[0].Schema.Attributes[1:]
	b.ListResources["audit_event"].Fields = b.ListResources["audit_event"].Fields[1:]
	expectRenderError(t, pc, m, b, "audit_event", "id")

	// A list resource whose call demands a path parameter.
	m, b = fictionalModel(), fictionalBindings()
	b.ListResources["audit_event"].List.Params = []sdkbind.CallParam{
		{Local: "parentId", GoType: "string", Wire: "parentId"}}
	expectRenderError(t, pc, m, b, "audit_event", "parentId")

	// A lookup datasource without a read call.
	m, b = fictionalModel(), fictionalBindings()
	b.Datasources["license"].Read = nil
	expectRenderError(t, pc, m, b, "license", "read")

	// A companion datasource without a list call.
	m, b = fictionalModel(), fictionalBindings()
	b.Datasources["http_server"].List = nil
	expectRenderError(t, pc, m, b, "http_server", "list")

	// An action without an invoke call.
	m, b = fictionalModel(), fictionalBindings()
	b.Actions["http_server_restart"].Invoke = nil
	expectRenderError(t, pc, m, b, "http_server_restart", "invoke")
}

func TestUnit_Emit_HelperSpellings(t *testing.T) {
	durations := map[int64]string{
		0:                 "0",
		1500:              "1500 * time.Nanosecond",
		2_000_000_000:     "2 * time.Second",
		120_000_000_000:   "2 * time.Minute",
		7_200_000_000_000: "2 * time.Hour",
	}
	for d, want := range durations {
		if got := goDuration(d); got != want {
			t.Fatalf("goDuration(%d) = %q, want %q", d, got, want)
		}
	}

	methods := map[string]string{
		"string": "ValueString", "bool": "ValueBool",
		"int64": "ValueInt64", "float64": "ValueFloat64",
	}
	for goType, want := range methods {
		if got := valueMethod(goType); got != want {
			t.Fatalf("valueMethod(%s) = %q", goType, got)
		}
	}

	if got := lowerCamel("tests_v7_http_server"); got != "testsV7HttpServer" {
		t.Fatalf("lowerCamel = %q", got)
	}

	checks := map[any]string{true: "true", int64(9): "9", 2.5: "2.5", "x": "x"}
	for in, want := range checks {
		if got := checkValue(in); got != want {
			t.Fatalf("checkValue(%v) = %q", in, got)
		}
	}

	// The auth-specific client.Config fragments the direct-call tests use.
	e := &serviceRenderer{pc: fictionalProviderCore()}
	if got := e.testClientConfig(); !strings.Contains(got, "APIToken") {
		t.Fatalf("bearer fragment = %q", got)
	}
	e.pc.AuthBearerToken, e.pc.AuthOAuth2ClientCredentials = false, true
	if got := e.testClientConfig(); !strings.Contains(got, "TokenURL") {
		t.Fatalf("oauth2 fragment = %q", got)
	}
	e.pc.AuthOAuth2ClientCredentials, e.pc.AuthGitHubApp = false, true
	if got := e.testClientConfig(); got != "" {
		t.Fatalf("github_app fragment = %q", got)
	}

	if got := successStatus(nil, 204); got != 204 {
		t.Fatalf("successStatus(nil) = %d", got)
	}
	if got := paramSegmentIndex("/plain/path"); got != -1 {
		t.Fatalf("paramSegmentIndex = %d", got)
	}
	if got := idWire(nil); got != "id" {
		t.Fatalf("idWire fallback = %q", got)
	}
	if companionItemTree(&ir.Datasource{}) != nil {
		t.Fatal("a schemaless companion has no item tree")
	}
}
