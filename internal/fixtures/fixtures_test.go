package fixtures

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// testTree is one tree exercising every value shape: scalars of each
// kind, an enum, an advisory enum, a list of scalars, a nested object, a
// list of objects, and an unsupported attribute.
func testTree() *ir.AttributeTree {
	return &ir.AttributeTree{
		Attributes: []ir.Attribute{
			{Name: "id", WireName: "id", Kind: ir.TypeString, ComputedOptionalRequired: ir.Computed},
			{Name: "name", WireName: "name", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required},
			{Name: "enabled", WireName: "enabled", Kind: ir.TypeBool, ComputedOptionalRequired: ir.Optional},
			{Name: "port", WireName: "port", Kind: ir.TypeInt64, ComputedOptionalRequired: ir.Optional},
			{Name: "ratio", WireName: "ratio", Kind: ir.TypeFloat64, ComputedOptionalRequired: ir.Optional},
			{Name: "kind", WireName: "kind", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional,
				OneOf: []string{"basic", "advanced"}},
			{Name: "mode", WireName: "mode", Kind: ir.TypeString, ComputedOptionalRequired: ir.ComputedOptional,
				AdvisoryValues: []string{"auto", "manual"}},
			{Name: "tags", WireName: "tags", Kind: ir.TypeList, ElementType: ir.TypeString, ComputedOptionalRequired: ir.Optional},
			{Name: "settings", WireName: "settings", Kind: ir.TypeObject, ComputedOptionalRequired: ir.Optional,
				Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "retries", WireName: "retries", Kind: ir.TypeInt64, ComputedOptionalRequired: ir.Required},
					{Name: "trace", WireName: "trace", Kind: ir.TypeBool, ComputedOptionalRequired: ir.Optional},
				}}},
			{Name: "rules", WireName: "rules", Kind: ir.TypeList, ElementType: ir.TypeObject, ComputedOptionalRequired: ir.Optional,
				Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "pattern", WireName: "pattern", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required},
				}}},
			{Name: "blob", WireName: "blob", ComputedOptionalRequired: ir.Optional,
				Unsupported: true, UnsupportedReason: "free-form object declares no properties"},
		},
	}
}

func valueByName(t *testing.T, s Fixture, name string) Entry {
	t.Helper()
	for _, v := range s.Entries {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no derived value for %s", name)
	return Entry{}
}

func TestUnit_Fixturespec_DerivationIsDeterministicAndTypeDriven(t *testing.T) {
	s := Derive(testTree())
	again := Derive(testTree())
	if !reflect.DeepEqual(s, again) {
		t.Fatal("two derivations of the same tree disagree")
	}

	if got := valueByName(t, s, "name").Scalar; got != NamePrefix+"name" {
		t.Fatalf("name = %v", got)
	}
	if got := valueByName(t, s, "enabled").Scalar; got != true {
		t.Fatalf("enabled = %v", got)
	}
	if got := valueByName(t, s, "port").Scalar; got != int64(7) {
		t.Fatalf("port = %v", got)
	}
	if got := valueByName(t, s, "ratio").Scalar; got != 1.5 {
		t.Fatalf("ratio = %v", got)
	}
	if got := valueByName(t, s, "kind").Scalar; got != "basic" {
		t.Fatalf("a closed enum must take its first declared value, got %v", got)
	}
	if got := valueByName(t, s, "mode").Scalar; got != "auto" {
		t.Fatalf("an open enum must take its first advisory value, got %v", got)
	}
	if got := valueByName(t, s, "tags").Scalar; got != NamePrefix+"tags" {
		t.Fatalf("tags element = %v", got)
	}

	settings := valueByName(t, s, "settings")
	if len(settings.Nested) != 2 || settings.Nested[0].Scalar != int64(7) {
		t.Fatalf("settings nested = %+v", settings.Nested)
	}
	rules := valueByName(t, s, "rules")
	if len(rules.Nested) != 1 || rules.Nested[0].Scalar != NamePrefix+"rules-pattern" {
		t.Fatalf("a nested string value must carry the full dashed path, got %+v", rules.Nested)
	}
}

func TestUnit_Fixturespec_SkipsCarryTheirReason(t *testing.T) {
	s := Derive(testTree())
	if len(s.Omissions) != 1 {
		t.Fatalf("skips = %+v", s.Omissions)
	}
	if s.Omissions[0].Name != "blob" || !strings.Contains(s.Omissions[0].Reason, "free-form") {
		t.Fatalf("skip = %+v", s.Omissions[0])
	}

	nested := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "outer", WireName: "outer", Kind: ir.TypeObject, ComputedOptionalRequired: ir.Optional,
			Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
				{Name: "inner", WireName: "inner", ComputedOptionalRequired: ir.Optional, Unsupported: true},
			}}},
	}}
	deep := Derive(nested)
	if len(deep.Omissions) != 1 || deep.Omissions[0].Name != "outer.inner" {
		t.Fatalf("a nested skip must carry its dotted path, got %+v", deep.Omissions)
	}
	if deep.Omissions[0].Reason == "" {
		t.Fatal("a skip without a stated reason must still carry a fallback reason")
	}

	if empty := Derive(nil); len(empty.Entries) != 0 || len(empty.Omissions) != 0 {
		t.Fatalf("a nil tree must derive an empty spec, got %+v", empty)
	}
}

func TestUnit_Fixtures_FormsSelectTheirEntries(t *testing.T) {
	s := Derive(testTree())

	names := func(form Form) []string {
		var out []string
		for _, v := range selected(s.Entries, form) {
			out = append(out, v.Name)
		}
		return out
	}

	if got := names(ConfigMinimal); !reflect.DeepEqual(got, []string{"name"}) {
		t.Fatalf("minimal config = %v", got)
	}
	maximal := names(ConfigMaximal)
	for _, name := range maximal {
		if name == "id" {
			t.Fatal("a computed attribute may never appear in a configuration")
		}
	}
	if len(maximal) != 9 {
		t.Fatalf("maximal config must carry every writable attribute, got %v", maximal)
	}
	if got := names(ResponseMinimal); !reflect.DeepEqual(got, []string{"id", "name", "mode"}) {
		t.Fatalf("minimal response must carry required plus server-filled, got %v", got)
	}
	if got := names(ResponseMaximal); len(got) != 10 {
		t.Fatalf("maximal response = %v", got)
	}
}

func TestUnit_Fixturespec_HCLAlignsRunsAndNestsByAttributeSyntax(t *testing.T) {
	s := Derive(testTree())
	got := s.HCL(ConfigMaximal)
	want := strings.Join([]string{
		`  name    = "tfpfgen-test-name"`,
		`  enabled = true`,
		`  port    = 7`,
		`  ratio   = 1.5`,
		`  kind    = "basic"`,
		`  mode    = "auto"`,
		`  tags    = ["tfpfgen-test-tags"]`,
		`  settings = {`,
		`    retries = 7`,
		`    trace   = true`,
		`  }`,
		`  rules = [`,
		`    {`,
		`      pattern = "tfpfgen-test-rules-pattern"`,
		`    },`,
		`  ]`,
		``,
	}, "\n")
	if got != want {
		t.Fatalf("maximal HCL:\n%s\nwant:\n%s", got, want)
	}

	if minimal := s.HCL(ConfigMinimal); minimal != "  name = \"tfpfgen-test-name\"\n" {
		t.Fatalf("minimal HCL:\n%s", minimal)
	}
}

func TestUnit_Fixturespec_WireJSONKeepsTreeOrderAndParses(t *testing.T) {
	s := Derive(testTree())
	got := s.WireJSON(ResponseMaximal)

	var parsed map[string]any
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("the wire rendering is not JSON: %v\n%s", err, got)
	}
	if parsed["id"] != NamePrefix+"id" || parsed["name"] != NamePrefix+"name" {
		t.Fatalf("wire values = %v", parsed)
	}

	idAt := bytes.Index(got, []byte(`"id"`))
	nameAt := bytes.Index(got, []byte(`"name"`))
	rulesAt := bytes.Index(got, []byte(`"rules"`))
	if idAt >= nameAt || nameAt >= rulesAt {
		t.Fatalf("wire keys must keep attribute-tree order:\n%s", got)
	}
	if !bytes.HasSuffix(got, []byte("}\n")) {
		t.Fatalf("the wire rendering must end with one newline:\n%q", got)
	}

	wire := s.WireValue(ResponseMaximal)
	if !reflect.DeepEqual(wire["tags"], []any{NamePrefix + "tags"}) {
		t.Fatalf("tags wire = %#v", wire["tags"])
	}
	settings, ok := wire["settings"].(map[string]any)
	if !ok || settings["retries"] != int64(7) {
		t.Fatalf("settings wire = %#v", wire["settings"])
	}
	rules, ok := wire["rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("rules wire = %#v", wire["rules"])
	}

	var buffer bytes.Buffer
	if err := json.NewEncoder(&buffer).Encode(wire); err != nil {
		t.Fatalf("the wire shape must encode: %v", err)
	}

	minimal := s.WireValue(ResponseMinimal)
	if _, there := minimal["enabled"]; there {
		t.Fatal("a minimal response must not carry an optional attribute the create never sent")
	}

	if empty := Derive(nil).WireJSON(ResponseMaximal); string(empty) != "{}\n" {
		t.Fatalf("an empty spec must render an empty object, got %q", empty)
	}
}

// variantTree is a multi-variant entity in the shape the audit discovers for a
// discriminated resource: kind gates which sibling attributes are valid, one
// per variant, with a value-conditional requirement each. name and interval are
// common to every variant; target_host belongs to ping, domain and dnssec to
// dns, web to its own variant. The tree-level edges are what a revised spec
// carries after the inference confirms them.
func variantTree() *ir.AttributeTree {
	return &ir.AttributeTree{
		Attributes: []ir.Attribute{
			{Name: "kind", WireName: "kind", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required,
				OneOf: []string{"ping", "web", "dns"}},
			{Name: "name", WireName: "name", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional},
			{Name: "interval", WireName: "interval", Kind: ir.TypeInt64, ComputedOptionalRequired: ir.Required},
			{Name: "target_host", WireName: "target_host", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional},
			{Name: "domain", WireName: "domain", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional},
			{Name: "dnssec", WireName: "dnssec", Kind: ir.TypeBool, ComputedOptionalRequired: ir.Optional},
			{Name: "web", WireName: "web", Kind: ir.TypeObject, ComputedOptionalRequired: ir.Optional,
				Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "url", WireName: "url", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required},
				}}},
			{Name: "id", WireName: "id", Kind: ir.TypeString, ComputedOptionalRequired: ir.Computed},
		},
		ConditionalValidities: []ir.ConditionalValidity{
			{Property: "kind", Equals: "dns", Valid: []string{"dnssec", "domain"}},
			{Property: "kind", Equals: "ping", Valid: []string{"target_host"}},
		},
		ConditionalRequirements: []ir.ConditionalRequirement{
			{Property: "kind", Equals: "dns", Required: []string{"domain"}},
			{Property: "kind", Equals: "ping", Required: []string{"target_host"}},
			{Property: "kind", Equals: "web", Required: []string{"web"}},
		},
		ValidConfigurations: []ir.ValidConfiguration{{
			Discriminator: "kind",
			Variants: []ir.ConfigVariant{
				{Value: "dns", Valid: []string{"dnssec", "domain"}},
				{Value: "ping", Valid: []string{"target_host"}},
			},
		}},
	}
}

func TestUnit_Fixturespec_VariantGatesToOneDiscriminatorValue(t *testing.T) {
	s := Derive(variantTree())

	// The discriminator is pinned to the first enum value that names a known
	// variant — "ping" — and its value drives which siblings appear.
	if got := valueByName(t, s, "kind").Scalar; got != "ping" {
		t.Fatalf("discriminator kind should be pinned to ping, got %v", got)
	}

	minHCL := s.HCL(ConfigMinimal)
	maxHCL := s.HCL(ConfigMaximal)

	// The chosen variant's value-conditional requirement (target_host when
	// kind=ping) is forced into the minimal config even though the attribute is
	// optional at the document level, or the minimal create would be refused.
	for _, want := range []string{`kind`, `interval`, `target_host`} {
		if !strings.Contains(minHCL, want) {
			t.Fatalf("minimal config must carry %q, got:\n%s", want, minHCL)
		}
	}
	// Attributes owned by another variant never appear — a config that set them
	// would fail the generated value-conditional validator.
	for _, banned := range []string{"domain", "dnssec", "web"} {
		if strings.Contains(minHCL, banned) {
			t.Fatalf("minimal config must not carry other-variant attr %q, got:\n%s", banned, minHCL)
		}
		if strings.Contains(maxHCL, banned) {
			t.Fatalf("maximal config must not carry other-variant attr %q, got:\n%s", banned, maxHCL)
		}
	}
	// The maximal config still carries the common writable attributes and the
	// chosen variant's own field.
	for _, want := range []string{"kind", "name", "interval", "target_host"} {
		if !strings.Contains(maxHCL, want) {
			t.Fatalf("maximal config must carry %q, got:\n%s", want, maxHCL)
		}
	}

	// The wire renderings gate identically, so a create built from the HCL meets
	// a mock built from the same variant.
	max := s.WireValue(ResponseMaximal)
	for _, banned := range []string{"domain", "dnssec", "web"} {
		if _, ok := max[banned]; ok {
			t.Fatalf("maximal wire must not carry other-variant key %q, got %v", banned, max)
		}
	}
	if _, ok := max["target_host"]; !ok {
		t.Fatalf("maximal wire must carry target_host, got %v", max)
	}
	minWire := s.WireValue(ResponseMinimal)
	if _, ok := minWire["target_host"]; !ok {
		t.Fatalf("minimal wire must carry the forced target_host, got %v", minWire)
	}
}

func TestUnit_Fixturespec_SingleVariantEntityIsUngated(t *testing.T) {
	// testTree carries no conditional edges: the variant gate must be a no-op,
	// so no entry is pruned, no requirement is forced, and every enum keeps its
	// first value.
	s := Derive(testTree())
	if s.requiredForVariant != nil {
		t.Fatalf("a single-variant entity must force no requirement, got %v", s.requiredForVariant)
	}
	// No entry is pruned: every optional sibling the gate might have dropped is
	// still present.
	for _, name := range []string{"enabled", "port", "ratio", "tags", "settings"} {
		_ = valueByName(t, s, name)
	}
	if got := valueByName(t, s, "kind").Scalar; got != "basic" {
		t.Fatalf("ungated enum keeps its first value basic, got %v", got)
	}
}

// TestUnit_Fixturespec_AFormatDecidesTheValueShape proves a string the
// document says is more than a string is synthesised as that thing. A
// generated SDK parses these on the way in, so a value of the wrong shape is
// refused before any assertion runs.
func TestUnit_Fixturespec_AFormatDecidesTheValueShape(t *testing.T) {
	tree := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "created_at", WireName: "createdAt", Kind: ir.TypeString, Format: "date-time"},
		{Name: "born_on", WireName: "bornOn", Kind: ir.TypeString, Format: "date"},
		{Name: "agent_id", WireName: "agentId", Kind: ir.TypeString, Format: "uuid"},
		{Name: "owner_email", WireName: "ownerEmail", Kind: ir.TypeString, Format: "email"},
		{Name: "home", WireName: "home", Kind: ir.TypeString, Format: "uri"},
		{Name: "address", WireName: "address", Kind: ir.TypeString, Format: "ipv4"},
		{Name: "label", WireName: "label", Kind: ir.TypeString},
	}}

	got := map[string]any{}
	for _, e := range Derive(tree).Entries {
		got[e.Name] = e.Scalar
	}

	if _, err := time.Parse(time.RFC3339, got["created_at"].(string)); err != nil {
		t.Errorf("created_at = %v, which no SDK will parse as a timestamp: %v", got["created_at"], err)
	}
	if _, err := time.Parse(time.DateOnly, got["born_on"].(string)); err != nil {
		t.Errorf("born_on = %v: %v", got["born_on"], err)
	}
	// Matched by shape rather than parsed: the toolkit takes no uuid
	// dependency, and the shape is what an SDK's parser accepts.
	uuidShape := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if value, _ := got["agent_id"].(string); !uuidShape.MatchString(value) {
		t.Errorf("agent_id = %q, which no SDK will parse as a uuid", value)
	}
	if _, err := netip.ParseAddr(got["address"].(string)); err != nil {
		t.Errorf("address = %v: %v", got["address"], err)
	}

	// A format with room for the prefix keeps it, so a value left behind on a
	// live API is still recognisable as toolkit debris.
	for _, name := range []string{"owner_email", "home", "label"} {
		if value, _ := got[name].(string); !strings.Contains(value, NamePrefix) {
			t.Errorf("%s = %q, which carries no %q", name, value, NamePrefix)
		}
	}
	if value, _ := got["owner_email"].(string); !strings.HasSuffix(value, "@example.invalid") {
		t.Errorf("owner_email = %q, which is not an address", value)
	}
	if value, _ := got["home"].(string); !strings.HasPrefix(value, "https://") {
		t.Errorf("home = %q, which is not a url", value)
	}

	// Determinism is the whole scheme: a regenerated fixture is byte-identical
	// or the document changed.
	for _, e := range Derive(tree).Entries {
		if e.Scalar != got[e.Name] {
			t.Errorf("%s derived %v then %v", e.Name, got[e.Name], e.Scalar)
		}
	}
}

// exampleTree is one tree exercising the declared-example precedence: a
// string the document describes only by example, one it also gives a format,
// one it constrains to an enum, and numbers the document bounds.
func exampleTree() *ir.AttributeTree {
	minimum, maximum := 10.0, 1.0
	return &ir.AttributeTree{
		Attributes: []ir.Attribute{
			{Name: "name", WireName: "name", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required,
				Example: "Production metrics stream"},
			{Name: "endpoint_url", WireName: "endpointUrl", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required,
				Example: "https://api.example.otel-collector"},
			{Name: "created", WireName: "created", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional,
				Format: "date-time", Example: "whenever"},
			{Name: "kind", WireName: "kind", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional,
				OneOf: []string{"basic", "advanced"}, Example: "advanced"},
			{Name: "retries", WireName: "retries", Kind: ir.TypeInt64, ComputedOptionalRequired: ir.Optional,
				Minimum: &minimum},
			{Name: "ratio", WireName: "ratio", Kind: ir.TypeFloat64, ComputedOptionalRequired: ir.Optional,
				Maximum: &maximum},
			{Name: "interval", WireName: "interval", Kind: ir.TypeInt64, ComputedOptionalRequired: ir.Optional,
				Example: 300},
		},
	}
}

func TestUnit_Fixturespec_DeclaredExampleDisplacesTheInventedName(t *testing.T) {
	s := Derive(exampleTree())

	// A string the document describes only by example takes the example: an
	// invented name is a string, and the API wanted a URL.
	if got := valueByName(t, s, "endpoint_url").Scalar; got != "https://api.example.otel-collector" {
		t.Errorf("endpoint_url = %#v, want the declared example", got)
	}
	// A declared format still wins: it synthesises a value of the right shape
	// that keeps the prefix, which an example cannot.
	if got := valueByName(t, s, "created").Scalar; got != "2026-01-02T03:04:05Z" {
		t.Errorf("created = %#v, want the format-driven value", got)
	}
	// An enum still wins, being the stricter statement of the two.
	if got := valueByName(t, s, "kind").Scalar; got != "basic" {
		t.Errorf("kind = %#v, want the first enum value", got)
	}
}

func TestUnit_Fixturespec_DeclaredBoundsMoveTheNumericConstant(t *testing.T) {
	s := Derive(exampleTree())

	// The constant sits below the declared minimum, so the value moves up to
	// it; a value the document forbids is refused by the API, not asserted on.
	if got := valueByName(t, s, "retries").Scalar; got != int64(10) {
		t.Errorf("retries = %#v, want the declared minimum", got)
	}
	if got := valueByName(t, s, "ratio").Scalar; got != 1.0 {
		t.Errorf("ratio = %#v, want the declared maximum", got)
	}
	if got := valueByName(t, s, "interval").Scalar; got != int64(300) {
		t.Errorf("interval = %#v, want the declared example", got)
	}
}

func TestUnit_Fixturespec_OneSynthesisedNameSurvivesForCleanup(t *testing.T) {
	s := Derive(exampleTree())

	// Every string in this tree could take an example, which would leave the
	// created object carrying no prefix for the cleanup pass to match on.
	if !anyPrefixed(s.Entries) {
		t.Fatal("no derived string carries the name prefix")
	}
	// The first displaced name is the one restored, so the choice follows the
	// document's order rather than the shape of any one field.
	if got := valueByName(t, s, "name").Scalar; got != NamePrefix+"name" {
		t.Errorf("name = %#v, want the restored synthesised name", got)
	}
	// Restoring one name does not take back any other example.
	if got := valueByName(t, s, "endpoint_url").Scalar; got != "https://api.example.otel-collector" {
		t.Errorf("endpoint_url = %#v, want the declared example kept", got)
	}
}

func TestUnit_Fixturespec_APrefixedStringSuppressesTheRestore(t *testing.T) {
	tree := exampleTree()
	// A string the document says nothing about already carries the prefix, so
	// nothing needs restoring and every example stands.
	tree.Attributes = append(tree.Attributes, ir.Attribute{
		Name: "label", WireName: "label", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional,
	})
	s := Derive(tree)

	if got := valueByName(t, s, "label").Scalar; got != NamePrefix+"label" {
		t.Fatalf("label = %#v, want the synthesised name", got)
	}
	if got := valueByName(t, s, "name").Scalar; got != "Production metrics stream" {
		t.Errorf("name = %#v, want the declared example kept", got)
	}
}

// acceptedTree is one entity whose shape covers what a replayed body has to
// carry: scalars, a list of scalars, and a nested object.
func acceptedTree() *ir.AttributeTree {
	return &ir.AttributeTree{
		Attributes: []ir.Attribute{
			{Name: "name", WireName: "name", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required},
			{Name: "match_type", WireName: "matchType", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional},
			{Name: "colour", WireName: "colour", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional},
			{Name: "labels", WireName: "labels", Kind: ir.TypeList, ElementType: ir.TypeString, ComputedOptionalRequired: ir.Optional},
			{Name: "settings", WireName: "settings", Kind: ir.TypeObject, ComputedOptionalRequired: ir.Optional,
				Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "retries", WireName: "retries", Kind: ir.TypeInt64, ComputedOptionalRequired: ir.Optional},
				}}},
		},
	}
}

func TestUnit_Fixturespec_AReplayedBodyCarriesWhatTheAPITook(t *testing.T) {
	spec := Derive(acceptedTree())
	request := map[string]any{
		"name":     "the-name-it-took",
		"colour":   "#FF0000",
		"labels":   []any{"first", "second"},
		"settings": map[string]any{"retries": int64(3)},
	}
	response := map[string]any{
		"name": "the-name-it-took", "colour": "#FF0000",
		"labels": []any{"first"}, "settings": map[string]any{"retries": int64(3)},
	}

	got := spec.FromAcceptedRequestBody(request, response, map[string]bool{"name": true})

	// The values are the ones the request carried, not the ones derived.
	if v := valueByName(t, got, "name").Scalar; v != "the-name-it-took" {
		t.Errorf("name = %#v, want the value the API took", v)
	}
	if v := valueByName(t, got, "colour").Scalar; v != "#FF0000" {
		t.Errorf("colour = %#v, want the value the API took", v)
	}
	// A list carries its first element, which is what one fixture renders.
	if v := valueByName(t, got, "labels").Scalar; v != "first" {
		t.Errorf("labels = %#v, want the first element sent", v)
	}
	// A nested object recurses.
	settings := valueByName(t, got, "settings")
	if len(settings.Nested) != 1 || settings.Nested[0].Scalar != int64(3) {
		t.Errorf("settings = %#v, want the nested value the API took", settings.Nested)
	}
	// An attribute the request never carried is not in a replay of it.
	for _, e := range got.Entries {
		if e.Name == "match_type" {
			t.Error("an attribute the accepted body did not carry was replayed")
		}
	}
}

func TestUnit_Fixturespec_AReplayDropsWhatTheAPINeverReturns(t *testing.T) {
	spec := Derive(acceptedTree())
	request := map[string]any{"name": "n", "match_type": "and", "matchType": "and", "colour": "#FF0000"}
	// The API took matchType and answered without it.
	response := map[string]any{"name": "n", "colour": "#FF0000"}

	got := spec.FromAcceptedRequestBody(request, response, map[string]bool{"name": true})

	for _, e := range got.Entries {
		if e.Name == "match_type" {
			t.Fatal("a property the API never returns was left in a configuration")
		}
	}
	var explained bool
	for _, o := range got.Omissions {
		if strings.Contains(o.Name, "match_type") && strings.Contains(o.Reason, "did not return it") {
			explained = true
		}
	}
	if !explained {
		t.Errorf("the dropped property was not explained: %#v", got.Omissions)
	}
	// A required property stays even unreturned: the create needs it.
	requiredUnreturned := spec.FromAcceptedRequestBody(
		map[string]any{"name": "n"}, map[string]any{}, map[string]bool{"name": true})
	if len(requiredUnreturned.Entries) != 1 {
		t.Errorf("a required property was dropped for not being echoed: %#v", requiredUnreturned.Entries)
	}
}

func TestUnit_Fixturespec_AReplayedRequiredPropertyTakesTheAPIsSpelling(t *testing.T) {
	spec := Derive(acceptedTree())
	// The API stores the required name in its own spelling and answers the
	// colour masked: the name takes the spelling, the masked colour stays
	// because the create needs it.
	got := spec.FromAcceptedRequestBody(
		map[string]any{"name": "host", "colour": "#FF0000"},
		map[string]any{"name": "https://host/", "colour": "*****"},
		map[string]bool{"name": true, "colour": true})
	if v := valueByName(t, got, "name").Scalar; v != "https://host/" {
		t.Errorf("name = %#v, want the spelling the API stored", v)
	}
	if v := valueByName(t, got, "colour").Scalar; v != "#FF0000" {
		t.Errorf("colour = %#v, want the value sent, since the create needs it", v)
	}
	if len(got.Omissions) != 0 {
		t.Errorf("a required property was dropped: %#v", got.Omissions)
	}
}

func TestUnit_Fixturespec_AnExpressionRendersUnquoted(t *testing.T) {
	spec := Derive(acceptedTree())
	got := spec.WithExpression("name", "petstore_owner.owner.id")
	if v := valueByName(t, got, "name").Expression; v != "petstore_owner.owner.id" {
		t.Fatalf("expression = %q, want it set on the named entry", v)
	}
	if spec.Entries[1].Expression != "" {
		t.Fatal("WithExpression changed the fixture it was called on")
	}
	hcl := got.HCL(ConfigMaximal)
	if !regexp.MustCompile(`(?m)^  name +?= petstore_owner\.owner\.id$`).MatchString(hcl) {
		t.Errorf("HCL = %q, want the expression rendered bare", hcl)
	}
	if strings.Contains(hcl, `"petstore_owner.owner.id"`) {
		t.Errorf("HCL = %q, want the expression unquoted", hcl)
	}
	// A name the fixture does not carry changes nothing.
	if same := spec.WithExpression("absent", "x"); len(same.Entries) != len(spec.Entries) {
		t.Errorf("an absent name changed the entries: %d", len(same.Entries))
	}
}

func TestUnit_Fixturespec_TheRunSuffixOnlyTouchesInventedNames(t *testing.T) {
	spec := Derive(acceptedTree())
	spec.Entries[1].Scalar = NamePrefix + "invented"
	spec.Entries[2].Scalar = "#FF0000"

	got := spec.WithRunSuffix()

	if v := got.Entries[1].Scalar; v != NamePrefix+"invented-"+RunSuffixExpr {
		t.Errorf("an invented name = %#v, want the run suffix appended", v)
	}
	// A value the document supplied is one the API is known to accept;
	// appending to it could make it invalid.
	if v := got.Entries[2].Scalar; v != "#FF0000" {
		t.Errorf("a document value = %#v, want it left alone", v)
	}
}

func TestUnit_Fixturespec_TheRunSuffixSitsBeforeAnAddressesDomain(t *testing.T) {
	spec := Derive(acceptedTree())
	spec.Entries[1].Scalar = NamePrefix + "user@example.invalid"

	got := spec.WithRunSuffix()

	if v := got.Entries[1].Scalar; v != NamePrefix+"user-"+RunSuffixExpr+"@example.invalid" {
		t.Errorf("an invented address = %#v, want the suffix in its local part", v)
	}
}

func TestUnit_Fixturespec_AReplayedNameGoesBackToTheInventedOne(t *testing.T) {
	spec := Derive(exampleTree())
	// What the audit sent and the API took: the document's own example, which
	// an API that requires a unique name accepts once and refuses thereafter.
	body := map[string]any{
		"name":        "Production metrics stream",
		"endpointUrl": "https://api.example.otel-collector",
	}

	got := spec.FromAcceptedRequestBody(body, body, map[string]bool{"name": true})

	if v := valueByName(t, got, "name").Scalar; v != NamePrefix+"name" {
		t.Errorf("name = %#v, want the invented name back", v)
	}
	// The invented name is what the run suffix recognises, so a replayed
	// configuration is unique per run rather than a constant that collides.
	suffixed := got.WithRunSuffix()
	if v := valueByName(t, suffixed, "name").Scalar; v != NamePrefix+"name-"+RunSuffixExpr {
		t.Errorf("suffixed name = %#v, want the run suffix appended", v)
	}
}

func TestUnit_Fixturespec_AReplayKeepsEveryValueThatDoesNotNameTheObject(t *testing.T) {
	spec := Derive(exampleTree())
	body := map[string]any{
		"name":        "Production metrics stream",
		"endpointUrl": "https://api.example.otel-collector",
		"interval":    int64(300),
	}

	got := spec.FromAcceptedRequestBody(body, body, map[string]bool{"name": true})

	// A string the API took that names nothing keeps the value it took: an
	// invented name is a string, and this one had to be a URL.
	if v := valueByName(t, got, "endpoint_url").Scalar; v != "https://api.example.otel-collector" {
		t.Errorf("endpoint_url = %#v, want the value the API took", v)
	}
	if v := valueByName(t, got, "interval").Scalar; v != int64(300) {
		t.Errorf("interval = %#v, want the value the API took", v)
	}
}

func TestUnit_Fixturespec_AReplayCarriesTheAPIsOwnSpellingOfAValue(t *testing.T) {
	spec := Derive(acceptedTree())
	request := map[string]any{"name": "n", "colour": "www.example.invalid"}
	// Sent bare, answered with the scheme the API stores it under: the
	// answered spelling is the one the API will not rewrite again.
	response := map[string]any{"name": "n", "colour": "ftp://www.example.invalid/"}

	got := spec.FromAcceptedRequestBody(request, response, map[string]bool{"name": true})

	if v := valueByName(t, got, "colour").Scalar; v != "ftp://www.example.invalid/" {
		t.Errorf("colour = %#v, want the value the API answered", v)
	}
}

func TestUnit_Fixturespec_AReplayDropsWhatTheAPIReturnsMaskedOrRetyped(t *testing.T) {
	spec := Derive(acceptedTree())
	request := map[string]any{"name": "n", "colour": "s3cret", "match_type": "and", "matchType": "7"}
	// A mask is not a value; a value of another type is not one a
	// configuration of this attribute's type can carry.
	response := map[string]any{"name": "n", "colour": "******", "matchType": float64(7)}

	got := spec.FromAcceptedRequestBody(request, response, map[string]bool{"name": true})

	for _, e := range got.Entries {
		if e.Name == "colour" || e.Name == "match_type" {
			t.Fatalf("a property the API masks or retypes was left in a configuration: %s", e.Name)
		}
	}
	reasons := map[string]string{}
	for _, o := range got.Omissions {
		reasons[o.Name] = o.Reason
	}
	if !strings.Contains(reasons["colour"], "masked") {
		t.Errorf("the masked property was not explained: %#v", got.Omissions)
	}
	if !strings.Contains(reasons["match_type"], "different value") {
		t.Errorf("the retyped property was not explained: %#v", got.Omissions)
	}
}

func TestUnit_Fixturespec_AKeptRootKeepsItsMembersWhateverTheEchoSays(t *testing.T) {
	spec := Derive(acceptedTree())
	request := map[string]any{"name": "n", "settings": map[string]any{"retries": int64(3)}}
	// The member is answered masked; under a root the configuration keeps
	// whatever the response says, it stays as sent.
	response := map[string]any{"name": "n", "settings": map[string]any{"retries": "*****"}}

	got := spec.FromAcceptedRequestBody(request, response, map[string]bool{"name": true, "settings": true})

	settings := valueByName(t, got, "settings")
	if len(settings.Nested) != 1 || settings.Nested[0].Scalar != int64(3) {
		t.Errorf("settings = %#v, want the member as sent under a kept root", settings.Nested)
	}
	// Without the root kept, the masked member goes, and the object it
	// leaves empty goes with it.
	loose := spec.FromAcceptedRequestBody(request, response, map[string]bool{"name": true})
	for _, e := range loose.Entries {
		if e.Name == "settings" {
			t.Errorf("a masked member survived under an unkept root: %#v", e.Nested)
		}
	}
}

func TestUnit_Fixturespec_TheLiveSuiteInventsEveryName(t *testing.T) {
	s := Derive(exampleTree())
	// Derivation keeps a declared example for a name where another string
	// carries the prefix; the live suite takes the invented name back, so
	// the run suffix makes it unique and cleanup can match it.
	live := s.WithInventedNames().WithRunSuffix()
	if v := valueByName(t, live, "name").Scalar; v != NamePrefix+"name-"+RunSuffixExpr {
		t.Errorf("name = %#v, want the invented name with the run suffix", v)
	}
	if v := valueByName(t, live, "endpoint_url").Scalar; v != "https://api.example.otel-collector" {
		t.Errorf("endpoint_url = %#v, want the example untouched", v)
	}
}

func TestUnit_Fixturespec_AReplayDropsAnObjectTheBodyCarriedEmpty(t *testing.T) {
	spec := Derive(acceptedTree())
	body := map[string]any{"name": "n", "settings": map[string]any{}}

	got := spec.FromAcceptedRequestBody(body, body, map[string]bool{"name": true})

	// An empty object leaves nothing to render, and rendered anyway it spells
	// the absent value as a literal that does not plan.
	for _, e := range got.Entries {
		if e.Name == "settings" {
			t.Fatalf("an empty object was left in a configuration: %#v", e)
		}
	}
}

func TestUnit_Fixturespec_AListTakesItsEnumOrItsExamplesFirstMember(t *testing.T) {
	enumerated := ir.Attribute{Name: "modules", Kind: ir.TypeList, ElementType: ir.TypeString, OneOf: []string{"default", "extended"}}
	if v, _ := scalarFor(enumerated.ElementType, enumerated, []string{"modules"}); v != "default" {
		t.Errorf("an enumerated list element = %#v, want the first member", v)
	}
	exampled := ir.Attribute{Name: "modules", Kind: ir.TypeList, ElementType: ir.TypeString, Example: []any{"default"}}
	if v, invented := scalarFor(exampled.ElementType, exampled, []string{"modules"}); v != "default" || invented == "" {
		t.Errorf("an exampled list element = %#v (invented %q), want the example's first member with the name kept", v, invented)
	}
	bare := ir.Attribute{Name: "modules", Kind: ir.TypeList, ElementType: ir.TypeString, Example: []any{7}}
	if v, _ := scalarFor(bare.ElementType, bare, []string{"modules"}); v != NamePrefix+"modules" {
		t.Errorf("a list whose example is not strings = %#v, want the invented name", v)
	}
}
