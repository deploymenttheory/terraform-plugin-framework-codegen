package strategy

import (
	"reflect"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

func TestStringifyScalar(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hello", "hello"},
		{true, "true"},
		{false, "false"},
		{7, "7"},
		{int64(9), "9"},
		{1.0, "1"},
		{1.5, "1.5"},
		{map[string]any{"x": 1}, ""}, // non-scalar renders empty
		{nil, ""},
	}
	for _, c := range cases {
		if got := stringifyScalar(c.in); got != c.want {
			t.Errorf("stringifyScalar(%v)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestStringifyValuesDedupsAndSorts(t *testing.T) {
	got := stringifyValues([]any{"b", "a", "b", 1.0, 1})
	want := []string{"1", "a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stringifyValues=%v, want %v", got, want)
	}
}

func TestGateKindRank(t *testing.T) {
	if GateRequiredEnum.rank() >= GateOptionalEnum.rank() ||
		GateOptionalEnum.rank() >= GateBool.rank() {
		t.Fatal("gate ranks are not required>optional>bool")
	}
	if GateKind("nonsense").rank() != 3 {
		t.Fatalf("unknown gate rank=%d, want 3", GateKind("nonsense").rank())
	}
}

func TestTokenIndex(t *testing.T) {
	if tokenIndex("anything", "") != -1 {
		t.Fatal("empty needle should not match")
	}
	// "type" must not match inside "prototype".
	if tokenIndex("the prototype value", "type") != -1 {
		t.Fatal("token match ignored word boundaries")
	}
	if got := tokenIndex("set the type now", "type"); got != 8 {
		t.Fatalf("tokenIndex=%d, want 8", got)
	}
	// A match at the very start, bounded by the string edge.
	if got := tokenIndex("type here", "type"); got != 0 {
		t.Fatalf("tokenIndex at start=%d, want 0", got)
	}
}

func TestExtractProseNamingSiblingButNoValue(t *testing.T) {
	// A conditional phrase naming a sibling with no matchable value becomes a
	// requiresField co-requirement, prose provenance.
	got := extractProse(
		"deep",
		"Must be set when advanced is present.",
		[]string{"deep", "advanced"},
		map[string][]string{}, // advanced has no enum values
	)
	if len(got) != 1 || got[0].Kind != HypothesisRequiresField {
		t.Fatalf("got %+v, want one requiresField", got)
	}
	if got[0].Provenance != ProvenanceProse {
		t.Fatalf("provenance=%q, want prose", got[0].Provenance)
	}
	if !contains(got[0].Subjects, "advanced") || !contains(got[0].Subjects, "deep") {
		t.Fatalf("subjects=%v, want deep and advanced", got[0].Subjects)
	}
}

func TestExtractProseNamingNothingDiscarded(t *testing.T) {
	got := extractProse(
		"x",
		"Required when something unrelated happens.",
		[]string{"x", "y"},
		map[string][]string{},
	)
	if len(got) != 0 {
		t.Fatalf("got %+v, want nothing (no sibling named)", got)
	}
}

// anyOfSchema exercises the anyOf branch path of gatherBranches: a gate whose
// value selects an anyOf branch's fields.
const anyOfSchema = `openapi: 3.0.3
info: {title: T, version: "1"}
paths: {}
components:
  schemas:
    Body:
      type: object
      required: [kind]
      properties:
        kind: {type: string, enum: [a, b]}
      anyOf:
        - type: object
          properties:
            kind: {type: string, enum: [a]}
            aOnly: {type: string}
        - type: object
          properties:
            kind: {type: string, enum: [b]}
            bOnly: {type: string}
`

func TestDeriveVariantsFromAnyOf(t *testing.T) {
	doc, err := specmodel.Load([]byte(anyOfSchema))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	body := doc.Schemas["Body"]
	gates := detectGates(body)
	if len(gates) != 1 || gates[0].Field != "kind" {
		t.Fatalf("gates=%+v, want one on kind", gates)
	}
	variants := deriveVariants(body, gates)
	if len(variants) != 3 {
		t.Fatalf("variants=%d, want 3", len(variants))
	}
	var va *Variant
	for i := range variants {
		if variants[i].GateValue == "a" {
			va = &variants[i]
		}
	}
	if va == nil || va.Provenance != ProvenanceStructural {
		t.Fatalf("anyOf variant a not structural: %+v", variants)
	}
	if !contains(va.Maximal.Fields, "aOnly") || contains(va.Maximal.Fields, "bOnly") {
		t.Fatalf("a maximal=%v, want aOnly without bOnly", va.Maximal.Fields)
	}
}

func TestDetectGatesSkipsSingleValueEnum(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths: {}
components:
  schemas:
    Body:
      type: object
      properties:
        only: {type: string, enum: [solo]}
        name: {type: string}
`
	doc, err := specmodel.Load([]byte(spec))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if g := detectGates(doc.Schemas["Body"]); len(g) != 0 {
		t.Fatalf("gates=%+v, want none (single-value enum is not a gate)", g)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
