package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
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

// expectRenderExclusion renders and requires that the run survived, and that
// exactly the named entity was excluded with a reason carrying every other
// fragment. The first fragment is the entity key.
func expectRenderExclusion(t *testing.T, pc ProviderCore, m *ir.Model, b *sdkbind.Bindings, key string, fragments ...string) {
	t.Helper()
	out, err := RenderServices(pc, m, b)
	if err != nil {
		t.Fatalf("an entity emission cannot serve must not fail the run (%s): %v", key, err)
	}
	for _, e := range out.Excluded {
		if e.Key != key {
			continue
		}
		for _, fragment := range fragments {
			if !strings.Contains(e.Reason, fragment) {
				t.Fatalf("the reason %q does not carry %q", e.Reason, fragment)
			}
		}
		return
	}
	t.Fatalf("%s must be excluded, got %+v", key, out.Excluded)
}

// assertQualifiedModelNames renders and requires that two nested objects
// claiming one short model name each got the ancestor-qualified spelling,
// consistently in the struct declarations and in the state assignments.
func assertQualifiedModelNames(t *testing.T, pc ProviderCore, m *ir.Model, b *sdkbind.Bindings) {
	t.Helper()
	out, err := RenderServices(pc, m, b)
	if err != nil {
		t.Fatalf("a model-name collision must be resolved, not refused: %v", err)
	}
	var model, state string
	for _, f := range out.Files {
		switch {
		case !strings.Contains(f.Path, "/resources/"):
			// The datasource of the same key declares its own item model.
		case strings.HasSuffix(f.Path, "http_server/model.go"):
			model = string(f.Content)
		case strings.HasSuffix(f.Path, "http_server/state.go"):
			state = string(f.Content)
		}
	}
	if model == "" || state == "" {
		t.Fatal("the resource must emit both model.go and state.go")
	}
	for _, want := range []string{"HTTPServerOneInnerModel", "HTTPServerTwoInnerModel"} {
		if !strings.Contains(model, "type "+want+" struct") {
			t.Fatalf("model.go must declare %s:\n%s", want, model)
		}
		if !strings.Contains(state, want) {
			t.Fatalf("state.go must name %s, or the two halves disagree:\n%s", want, state)
		}
	}
	if strings.Contains(model, "type HTTPServerInnerModel struct") {
		t.Fatal("the contested short spelling must not survive")
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

	// A conversion the catalog cannot bridge.
	m, b := fictionalModel(), fictionalBindings()
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

	// A valid-when gated on a missing attribute.
	m, b = fictionalModel(), fictionalBindings()
	m.Resources[0].Schema.ConditionalValidities = []ir.ConditionalValidity{
		{Property: "ghost", Equals: "x", Valid: []string{"name"}}}
	expectRenderError(t, pc, m, b, "http_server", "ghost")

	// A valid-when allowing a missing attribute.
	m, b = fictionalModel(), fictionalBindings()
	m.Resources[0].Schema.ConditionalValidities = []ir.ConditionalValidity{
		{Property: "kind", Equals: "advanced", Valid: []string{"ghost"}}}
	expectRenderError(t, pc, m, b, "http_server", "ghost")

	// A dependency whose subject is a missing attribute.
	m, b = fictionalModel(), fictionalBindings()
	m.Resources[0].Schema.Dependencies = []ir.Dependency{{Attribute: "ghost", Requires: []string{"name"}}}
	expectRenderError(t, pc, m, b, "http_server", "ghost")

	// A dependency requiring a missing attribute.
	m, b = fictionalModel(), fictionalBindings()
	m.Resources[0].Schema.Dependencies = []ir.Dependency{{Attribute: "ratio", Requires: []string{"ghost"}}}
	expectRenderError(t, pc, m, b, "http_server", "ghost")

	// A valid configuration on a missing discriminator.
	m, b = fictionalModel(), fictionalBindings()
	m.Resources[0].Schema.ValidConfigurations = []ir.ValidConfiguration{
		{Discriminator: "ghost", Variants: []ir.ConfigVariant{{Value: "x", Valid: []string{"name"}}}}}
	expectRenderError(t, pc, m, b, "http_server", "ghost")

	// A valid configuration admitting a missing attribute.
	m, b = fictionalModel(), fictionalBindings()
	m.Resources[0].Schema.ValidConfigurations = []ir.ValidConfiguration{
		{Discriminator: "kind", Variants: []ir.ConfigVariant{{Value: "basic", Valid: []string{"ghost"}}}}}
	expectRenderError(t, pc, m, b, "http_server", "ghost")

	// A path parameter no attribute can feed.
	m, b = fictionalModel(), fictionalBindings()
	b.Resources["http_server"].Read.Params = []sdkbind.CallParam{
		{Local: "a", GoType: "string", Wire: "aId"}, {Local: "b", GoType: "string", Wire: "bId"}}
	expectRenderExclusion(t, pc, m, b, "http_server", "aId")

	// Two nested objects claiming one model struct name.
	m, b = fictionalModel(), fictionalBindings()
	inner := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "inner", WireName: "inner", Kind: ir.TypeObject, ComputedOptionalRequired: ir.Optional,
			Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
				{Name: "x", WireName: "x", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional}}}},
	}}
	m.Resources[0].Schema.Attributes = append(m.Resources[0].Schema.Attributes,
		ir.Attribute{Name: "one", WireName: "one", Kind: ir.TypeObject, ComputedOptionalRequired: ir.Optional, Nested: inner},
		ir.Attribute{Name: "two", WireName: "two", Kind: ir.TypeObject, ComputedOptionalRequired: ir.Optional, Nested: inner},
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
	assertQualifiedModelNames(t, pc, m, b)

	// A list resource whose element carries no id.
	m, b = fictionalModel(), fictionalBindings()
	m.ListResources[0].Schema.Attributes = m.ListResources[0].Schema.Attributes[1:]
	b.ListResources["audit_event"].Fields = b.ListResources["audit_event"].Fields[1:]
	expectRenderExclusion(t, pc, m, b, "audit_event", "id")

	// A list resource whose call demands a path parameter no addressing
	// attribute answers.
	m, b = fictionalModel(), fictionalBindings()
	b.ListResources["audit_event"].List.Params = []sdkbind.CallParam{
		{Local: "parentId", GoType: "string", Wire: "parentId"}}
	expectRenderExclusion(t, pc, m, b, "audit_event", "parentId")

	// A lookup datasource without a read call.
	m, b = fictionalModel(), fictionalBindings()
	b.Datasources["license"].Read = nil
	expectRenderExclusion(t, pc, m, b, "license", "read")

	// A companion datasource without a list call.
	m, b = fictionalModel(), fictionalBindings()
	b.Datasources["http_server"].List = nil
	expectRenderExclusion(t, pc, m, b, "http_server", "list")

	// An action without an invoke call.
	m, b = fictionalModel(), fictionalBindings()
	b.Actions["http_server_restart"].Invoke = nil
	expectRenderExclusion(t, pc, m, b, "http_server_restart", "invoke")
}

func TestUnit_Emit_ValidatorHelperSpellings(t *testing.T) {
	orLists := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"a"}, `"a"`},
		{[]string{"a", "b"}, `"a" or "b"`},
		{[]string{"a", "b", "c"}, `"a", "b" or "c"`},
	}
	for _, tc := range orLists {
		if got := orList(tc.in); got != tc.want {
			t.Fatalf("orList(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}

	if got := listPayloadExpr("", "items"); got != "items" {
		t.Fatalf("bare payload = %q", got)
	}
	if got := listPayloadExpr("things", "items"); got != `map[string]any{"things": items}` {
		t.Fatalf("wrapped payload = %q", got)
	}
	if got := listResponseJSON("", "  {}"); !strings.HasPrefix(got, "[\n") || strings.Contains(got, "{") == false {
		t.Fatalf("bare list json = %q", got)
	}
	if got := listResponseJSON("data", "{}"); !strings.Contains(got, `"data": [`) {
		t.Fatalf("wrapped list json = %q", got)
	}

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

	methods := map[ir.AttributeType]string{
		ir.TypeString: "ValueString", ir.TypeBool: "ValueBool",
		ir.TypeInt64: "ValueInt64", ir.TypeFloat64: "ValueFloat64",
	}
	for kind, want := range methods {
		if got := valueMethod(kind); got != want {
			t.Fatalf("valueMethod(%s) = %q", kind, got)
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

// TestUnit_RenderServices_ExcludesTheEntityWhoseShapeItCannotServe covers the
// other half of the failure contract: an entity emission cannot serve is
// reported as excluded and the entities beside it still render, where it used
// to abort the whole provider.
func TestUnit_RenderServices_ExcludesTheEntityWhoseShapeItCannotServe(t *testing.T) {
	pc := fictionalProviderCore()
	m, b := fictionalModel(), fictionalBindings()
	b.Resources["http_server"].Delete = nil

	out, err := RenderServices(pc, m, b)
	if err != nil {
		t.Fatalf("one unservable entity must not fail the run: %v", err)
	}
	if len(out.Excluded) != 1 {
		t.Fatalf("want exactly one exclusion, got %d: %+v", len(out.Excluded), out.Excluded)
	}
	if out.Excluded[0].Key != "http_server" {
		t.Fatalf("the exclusion must name the entity, got %q", out.Excluded[0].Key)
	}
	if !strings.Contains(out.Excluded[0].Reason, "delete") {
		t.Fatalf("the reason must say what was missing, got %q", out.Excluded[0].Reason)
	}
	for _, line := range out.Registrations.Resources.Registrations {
		if strings.Contains(line, "HTTPServer") {
			t.Fatal("an excluded entity must not be registered")
		}
	}
}

// TestUnit_RenderServices_ExcludesRatherThanAbortsTheWholeRun proves the
// exclusion is local: a second, well-formed resource beside the refused one
// still renders and registers.
func TestUnit_RenderServices_ExcludesRatherThanAbortsTheWholeRun(t *testing.T) {
	pc := fictionalProviderCore()
	m, b := fictionalModel(), fictionalBindings()

	before, err := RenderServices(pc, m, b)
	if err != nil {
		t.Fatalf("the fictional model must render: %v", err)
	}
	if len(before.Excluded) != 0 {
		t.Fatalf("nothing should be excluded from a sound model: %+v", before.Excluded)
	}

	b.Resources["http_server"].Delete = nil
	after, err := RenderServices(pc, m, b)
	if err != nil {
		t.Fatalf("rendering must survive one refusal: %v", err)
	}
	if len(after.Files) == 0 {
		t.Fatal("the entities beside the refused one must still render")
	}
	if len(after.Files) >= len(before.Files) {
		t.Fatalf("the refused entity's files must be absent: %d then %d", len(before.Files), len(after.Files))
	}
}

func TestUnit_JoinTree_KeepsAddressingAttributesTheSDKCannotCarry(t *testing.T) {
	// A path parameter addresses the object rather than describing it, so no
	// request or response body declares it and no SDK model carries it. It
	// must survive the join all the same, or nothing can fill the call.
	tree := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "owner", WireName: "owner", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required},
		{Name: "id", WireName: "id", Kind: ir.TypeString, ComputedOptionalRequired: ir.Computed},
		{Name: "name", WireName: "name", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional},
	}}
	bound := []sdkbind.FieldBinding{{Attr: "name", Wire: "name", Kind: ir.TypeString,
		Access: kAccess("Name", "*string", "FromPtrString", "ToPtrString", "")}}

	nodes := joinTree(tree, bound, map[string]bool{"owner": true})

	got := map[string]bool{}
	for _, n := range nodes {
		got[n.attr.Name] = true
	}
	for _, want := range []string{"owner", "id", "name"} {
		if !got[want] {
			t.Fatalf("%s must survive the join, got %v", want, got)
		}
	}
}

func TestUnit_JoinTree_DropsAnUnboundAttributeThatAddressesNothing(t *testing.T) {
	tree := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "ghost", WireName: "ghost", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional},
	}}
	if nodes := joinTree(tree, nil); len(nodes) != 0 {
		t.Fatalf("an ordinary unbound attribute must be dropped, got %d node(s)", len(nodes))
	}
}

func TestUnit_AddressingNames_TakesEveryPathParameterInTerraformSpelling(t *testing.T) {
	read := &ir.Operation{PathParameters: []ir.Parameter{
		{Name: "owner", Type: ir.TypeString},
		{Name: "repo", Type: ir.TypeString},
		{Name: "ruleset_id", Type: ir.TypeInt64},
	}}
	list := &ir.Operation{PathParameters: []ir.Parameter{{Name: "org", Type: ir.TypeString}}}

	names := addressingNames(read, nil, list)
	for _, want := range []string{"owner", "repo", "ruleset_id", "org"} {
		if !names[want] {
			t.Fatalf("%s must be addressing, got %v", want, names)
		}
	}
	if len(names) != 4 {
		t.Fatalf("want exactly the four parameters, got %v", names)
	}
}

func TestUnit_ParamNode_RefusesAnObjectOfTheSameName(t *testing.T) {
	// A path parameter is a scalar in the URL. An attribute of the same name
	// that is an object is a different thing the document spells the same
	// way, and reading a value out of it does not compile.
	nodes := []node{
		{attr: ir.Attribute{Name: "owner", WireName: "owner", Kind: ir.TypeObject,
			Nested: &ir.AttributeTree{}}},
	}
	if _, err := paramNode(sdkbind.CallParam{Local: "owner", Wire: "owner", GoType: "string"}, nodes, false); err == nil {
		t.Fatal("an object must not answer a path parameter")
	}
}

func TestUnit_ParamNode_TakesTheLastParameterAsTheID(t *testing.T) {
	// An item path with a parent takes two parameters, and its key is the
	// last one — whatever the response happens to call its own id.
	nodes := []node{
		{attr: ir.Attribute{Name: "id", WireName: "id", Kind: ir.TypeString}},
	}
	got, err := paramNode(sdkbind.CallParam{Local: "cfg", Wire: "configuration_id", GoType: "string"}, nodes, true)
	if err != nil {
		t.Fatalf("the last path parameter must fall back to the id: %v", err)
	}
	if got.attr.Name != "id" {
		t.Fatalf("want the id attribute, got %q", got.attr.Name)
	}
	if _, err := paramNode(sdkbind.CallParam{Local: "o", Wire: "owner", GoType: "string"}, nodes, false); err == nil {
		t.Fatal("a parent parameter must not fall back to the id")
	}
}

func TestUnit_Invocable_DropsWhatAnActionCannotTake(t *testing.T) {
	// An action schema has no Computed: the framework's action package does
	// not declare the field, so a computed attribute does not compile.
	nodes := []node{
		{attr: ir.Attribute{Name: "kept", ComputedOptionalRequired: ir.Required}},
		{attr: ir.Attribute{Name: "dropped", ComputedOptionalRequired: ir.Computed}},
		{attr: ir.Attribute{Name: "block", ComputedOptionalRequired: ir.Optional, Nested: &ir.AttributeTree{}},
			children: []node{
				{attr: ir.Attribute{Name: "inner_kept", ComputedOptionalRequired: ir.Optional}},
				{attr: ir.Attribute{Name: "inner_dropped", ComputedOptionalRequired: ir.Computed}},
			}},
	}
	got := invocable(nodes)
	if len(got) != 2 || got[0].attr.Name != "kept" || got[1].attr.Name != "block" {
		t.Fatalf("want kept and block, got %+v", got)
	}
	if len(got[1].children) != 1 || got[1].children[0].attr.Name != "inner_kept" {
		t.Fatalf("a nested computed argument must go too, got %+v", got[1].children)
	}
}

func TestUnit_PresenceLines_NeverRendersComputedInAnActionSchema(t *testing.T) {
	for _, presence := range []ir.ComputedOptionalRequired{ir.Computed, ir.ComputedOptional} {
		sb := &schemaBuilder{kind: schemaAction, imports: newImportSet("example.com/mod")}
		got := sb.computedOptionalRequiredLines(node{attr: ir.Attribute{Name: "x", ComputedOptionalRequired: presence}}, "")
		if strings.Contains(got, "Computed") {
			t.Fatalf("presence %s rendered %q in an action schema", presence, got)
		}
	}
	// A datasource still computes.
	sb := &schemaBuilder{kind: schemaDatasource, imports: newImportSet("example.com/mod")}
	if got := sb.computedOptionalRequiredLines(node{attr: ir.Attribute{Name: "x", ComputedOptionalRequired: ir.Computed}}, ""); !strings.Contains(got, "Computed") {
		t.Fatalf("a datasource must still render Computed, got %q", got)
	}
}

func TestUnit_RenderServices_RefusesALookupWhoseReadAnswersACollection(t *testing.T) {
	// A read that answers with a collection is not a lookup: the state
	// mapper reads fields off one object, and which element it should map is
	// not something the document says.
	pc := fictionalProviderCore()
	m, b := fictionalModel(), fictionalBindings()
	b.Datasources["license"].ReadModel = "[]models.Licenseable"

	expectRenderExclusion(t, pc, m, b, "license", "collection")
}

func TestUnit_FindIdentityNode_AcceptsAnIdentityTheAPIDidNotSpellAsAString(t *testing.T) {
	// A list identity is a string, but an API is not obliged to key its
	// objects with one. Requiring the string spelling excluded every entity
	// with a numeric id.
	for _, kind := range []ir.AttributeType{ir.TypeString, ir.TypeInt64, ir.TypeFloat64, ir.TypeBool} {
		nodes := []node{{
			attr: ir.Attribute{Name: "id", WireName: "id", Kind: kind},
			fb:   &sdkbind.FieldBinding{Attr: "id", Wire: "id", Kind: kind, Access: sdkbind.FieldAccess{Get: "GetId"}},
		}}
		if _, ok := findIdentityNode(nodes); !ok {
			t.Fatalf("a %s id must serve as the list identity", kind)
		}
	}

	// An object of that name is not an identity, and neither is an unbound one.
	nested := []node{{
		attr: ir.Attribute{Name: "id", Kind: ir.TypeObject, Nested: &ir.AttributeTree{}},
		fb:   &sdkbind.FieldBinding{Attr: "id", Access: sdkbind.FieldAccess{Get: "GetId"}},
	}}
	if _, ok := findIdentityNode(nested); ok {
		t.Fatal("an object must not serve as the list identity")
	}
	if _, ok := findIdentityNode([]node{{attr: ir.Attribute{Name: "id", Kind: ir.TypeString}}}); ok {
		t.Fatal("an unbound attribute cannot be read for the identity")
	}
}

func TestUnit_ReadStringLocal_RendersANonStringIdentityThroughFmt(t *testing.T) {
	numeric := node{
		attr: ir.Attribute{Name: "id", Kind: ir.TypeInt64},
		fb:   &sdkbind.FieldBinding{Access: sdkbind.FieldAccess{Get: "GetId", SDKType: "*int64"}},
	}
	got := readStringLocal("id", numeric)
	if !strings.Contains(got, "fmt.Sprintf") {
		t.Fatalf("a numeric identity must be rendered as a string:\n%s", got)
	}

	text := node{
		attr: ir.Attribute{Name: "id", Kind: ir.TypeString},
		fb:   &sdkbind.FieldBinding{Access: sdkbind.FieldAccess{Get: "GetId", SDKType: "*string"}},
	}
	if got := readStringLocal("id", text); strings.Contains(got, "fmt.Sprintf") {
		t.Fatalf("a string identity needs no conversion:\n%s", got)
	}
}

func TestUnit_RenderServices_EmitsASingletonWithoutCreateOrDelete(t *testing.T) {
	// A singleton writes on create through its update, and destroys by
	// forgetting. It needs neither a create call nor a delete one.
	pc := fictionalProviderCore()
	m, b := fictionalModel(), fictionalBindings()

	r := &m.Resources[0]
	r.Singleton = true
	r.Operations.Create = nil
	r.Operations.Delete = nil
	rb := b.Resources[r.Names.Key]
	rb.Create = nil
	rb.Delete = nil

	out, err := RenderServices(pc, m, b)
	if err != nil {
		t.Fatalf("a singleton must render: %v", err)
	}
	if len(out.Excluded) != 0 {
		t.Fatalf("a singleton must not be excluded: %+v", out.Excluded)
	}

	var crud string
	for _, f := range out.Files {
		if strings.Contains(f.Path, "/resources/") && strings.HasSuffix(f.Path, "http_server/crud.go") {
			crud = string(f.Content)
		}
		if strings.HasSuffix(f.Path, "http_server/mocks/responders.go") && strings.Contains(f.Path, "/resources/") {
			t.Fatal("a singleton has no collection to mock, so no responders are emitted")
		}
	}
	if crud == "" {
		t.Fatal("the singleton must emit crud.go")
	}
	if strings.Contains(crud, "crud.DeleteWithRetry") {
		t.Fatalf("destroy must forget rather than call the API:\n%s", crud)
	}
	if !strings.Contains(crud, "resp.State.RemoveResource(ctx)") {
		t.Fatalf("destroy must still drop the object from state:\n%s", crud)
	}
	if !strings.Contains(crud, `data.ID = types.StringValue("petstore_http_server")`) {
		t.Fatalf("a singleton publishes a constant id:\n%s", crud)
	}
}

func TestUnit_RenderServices_RefusesASingletonWithoutAnUpdate(t *testing.T) {
	pc := fictionalProviderCore()
	m, b := fictionalModel(), fictionalBindings()

	r := &m.Resources[0]
	r.Singleton = true
	r.Operations.Create, r.Operations.Delete, r.Operations.Update = nil, nil, nil
	rb := b.Resources[r.Names.Key]
	rb.Create, rb.Delete, rb.Update = nil, nil, nil

	expectRenderExclusion(t, pc, m, b, r.Names.Key, "singleton", "read and update")
}

func TestUnit_AttributeDescription_LeadsWithTheDocumentsOwnProse(t *testing.T) {
	a := ir.Attribute{
		Name: "direction", WireName: "direction",
		Description:     "Direction for applicable alert types",
		AdvisoryValues:  []string{"to-target", "from-target"},
		RequiresReplace: true,
	}
	got := attributeDescription(a)
	if !strings.HasPrefix(got, "Direction for applicable alert types.") {
		t.Fatalf("the document's sentence must lead, and be terminated: %q", got)
	}
	for _, want := range []string{"Known values: to-target, from-target.", "forces replacement"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the derived facts must follow the prose, missing %q: %q", want, got)
		}
	}
}

func TestUnit_AttributeDescription_FallsBackToTheWireName(t *testing.T) {
	// Real documents leave most properties bare — one pilot annotates 12%.
	got := attributeDescription(ir.Attribute{Name: "path", WireName: "path"})
	if got != "The path property." {
		t.Fatalf("an undescribed attribute keeps the derived sentence, got %q", got)
	}
}

func TestUnit_Terminated_DoesNotDoubleUpPunctuation(t *testing.T) {
	for in, want := range map[string]string{
		"A description of the alert rule":  "A description of the alert rule.",
		"A description of the alert rule.": "A description of the alert rule.",
		"Is it set?":                       "Is it set?",
		"One of:":                          "One of:",
	} {
		if got := terminated(in); got != want {
			t.Fatalf("terminated(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnit_EntityDescription_KeepsBothSentences(t *testing.T) {
	// The derived sentence is the only one that says what the terraform
	// surface is; the document's is the only human-written text there is.
	tree := &ir.AttributeTree{Description: "A rule that raises alerts"}
	got := entityDescription(tree, "Manages the alerts_rule entity.")
	if got != "Manages the alerts_rule entity. A rule that raises alerts." {
		t.Fatalf("both sentences must survive, got %q", got)
	}
	if got := entityDescription(&ir.AttributeTree{}, "Manages the x entity."); got != "Manages the x entity." {
		t.Fatalf("an undescribed object keeps the derived sentence, got %q", got)
	}
	if got := entityDescription(nil, "Manages the x entity."); got != "Manages the x entity." {
		t.Fatalf("a nil tree keeps the derived sentence, got %q", got)
	}
}

func TestUnit_Schema_RendersMarkdownDescription(t *testing.T) {
	// Every emitted description is a markdown one, so the registry renders
	// whatever formatting a vendor's prose carries.
	out, err := RenderServices(fictionalProviderCore(), fictionalModel(), fictionalBindings())
	if err != nil {
		t.Fatalf("RenderServices: %v", err)
	}
	for _, f := range out.Files {
		if !strings.HasSuffix(f.Path, ".go") {
			continue
		}
		body := string(f.Content)
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Description:") {
				t.Fatalf("%s renders a plain description: %s", f.Path, trimmed)
			}
		}
	}
}
