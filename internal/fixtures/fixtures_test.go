package fixtures

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
)

// testTree is one tree exercising every value shape: scalars of each
// kind, an enum, an advisory enum, a list of scalars, a nested object, a
// list of objects, and an unsupported attribute.
func testTree() *ir.AttributeTree {
	return &ir.AttributeTree{
		Attributes: []ir.Attribute{
			{Name: "id", WireName: "id", Kind: ir.TypeString, Presence: ir.PresenceComputed},
			{Name: "name", WireName: "name", Kind: ir.TypeString, Presence: ir.PresenceRequired},
			{Name: "enabled", WireName: "enabled", Kind: ir.TypeBool, Presence: ir.PresenceOptional},
			{Name: "port", WireName: "port", Kind: ir.TypeInt64, Presence: ir.PresenceOptional},
			{Name: "ratio", WireName: "ratio", Kind: ir.TypeFloat64, Presence: ir.PresenceOptional},
			{Name: "kind", WireName: "kind", Kind: ir.TypeString, Presence: ir.PresenceOptional,
				OneOf: []string{"basic", "advanced"}},
			{Name: "mode", WireName: "mode", Kind: ir.TypeString, Presence: ir.PresenceOptionalComputed,
				AdvisoryValues: []string{"auto", "manual"}},
			{Name: "tags", WireName: "tags", Kind: ir.TypeList, ElemKind: ir.TypeString, Presence: ir.PresenceOptional},
			{Name: "settings", WireName: "settings", Kind: ir.TypeObject, Presence: ir.PresenceOptional,
				Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "retries", WireName: "retries", Kind: ir.TypeInt64, Presence: ir.PresenceRequired},
					{Name: "trace", WireName: "trace", Kind: ir.TypeBool, Presence: ir.PresenceOptional},
				}}},
			{Name: "rules", WireName: "rules", Kind: ir.TypeList, ElemKind: ir.TypeObject, Presence: ir.PresenceOptional,
				Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "pattern", WireName: "pattern", Kind: ir.TypeString, Presence: ir.PresenceRequired},
				}}},
			{Name: "blob", WireName: "blob", Presence: ir.PresenceOptional,
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
		{Name: "outer", WireName: "outer", Kind: ir.TypeObject, Presence: ir.PresenceOptional,
			Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
				{Name: "inner", WireName: "inner", Presence: ir.PresenceOptional, Unsupported: true},
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

func TestUnit_Fixturespec_AudiencesSelectByPresence(t *testing.T) {
	s := Derive(testTree())

	names := func(a Form) []string {
		var out []string
		for _, v := range selected(s.Entries, a) {
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

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(wire); err != nil {
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
			{Name: "kind", WireName: "kind", Kind: ir.TypeString, Presence: ir.PresenceRequired,
				OneOf: []string{"ping", "web", "dns"}},
			{Name: "name", WireName: "name", Kind: ir.TypeString, Presence: ir.PresenceOptional},
			{Name: "interval", WireName: "interval", Kind: ir.TypeInt64, Presence: ir.PresenceRequired},
			{Name: "target_host", WireName: "target_host", Kind: ir.TypeString, Presence: ir.PresenceOptional},
			{Name: "domain", WireName: "domain", Kind: ir.TypeString, Presence: ir.PresenceOptional},
			{Name: "dnssec", WireName: "dnssec", Kind: ir.TypeBool, Presence: ir.PresenceOptional},
			{Name: "web", WireName: "web", Kind: ir.TypeObject, Presence: ir.PresenceOptional,
				Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "url", WireName: "url", Kind: ir.TypeString, Presence: ir.PresenceRequired},
				}}},
			{Name: "id", WireName: "id", Kind: ir.TypeString, Presence: ir.PresenceComputed},
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
