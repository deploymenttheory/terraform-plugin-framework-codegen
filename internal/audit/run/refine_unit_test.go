package run

import (
	"context"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/strategy"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/quirkserver"
)

// TestUnit_Borrow_BorrowsCachesAndReportsEmpty covers the borrower's three
// outcomes: a real id from a served collection, the cache hit that costs no
// request, and the false the caller reads as inconclusive when the collection
// does not exist.
func TestUnit_Borrow_BorrowsCachesAndReportsEmpty(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	r, err := newRunner(testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	ent := &entityState{plan: &plan.EntityPlan{Entity: "assignment", Budget: plan.Budget{Requests: 20}}}

	id, ok := r.borrow(context.Background(), ent, "agent")
	if !ok || id == "" {
		t.Fatalf("borrow(agent) = %q, %v; want a real id", id, ok)
	}
	before := r.reqTotal
	if id2, ok := r.borrow(context.Background(), ent, "agent"); !ok || id2 != id {
		t.Errorf("cached borrow = %q, %v; want the same id", id2, ok)
	}
	if r.reqTotal != before {
		t.Errorf("a cached borrow spent %d requests, want none", r.reqTotal-before)
	}
	if _, ok := r.borrow(context.Background(), ent, "widget"); ok {
		t.Error("borrow of a collection the server does not serve must report false")
	}
}

// TestUnit_Refine_ClassifyRefusalGrammar pins the whole refusal-classification
// table: each sentence the quirk server's stable grammar emits, plus the
// envelope variants a real API might wrap it in, and the unintelligible case.
func TestUnit_Refine_ClassifyRefusalGrammar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		body  string
		kind  actKind
		field string
		extra string // collection / trigger / condVal, per kind
	}{
		{"required", `{"detail":"field interval is required"}`, actAdd, "interval", ""},
		{"required-when", `{"detail":"field target_host is required when kind=ping"}`, actAdd, "target_host", "ping"},
		{"not-valid", `{"detail":"field domain is not valid when kind=ping"}`, actRemove, "domain", "ping"},
		{"requires", `{"detail":"field dnssec requires field domain to be set"}`, actRequires, "domain", "dnssec"},
		{"reference", `{"detail":"field agent_id must reference an existing agent"}`, actBorrow, "agent_id", "agent"},
		{"enum-list", `{"detail":"field kind must be one of ping, web, dns"}`, actStop, "", ""},
		{"bare-field", `{"title":"missing required field","detail":"serial"}`, actStop, "", ""},
		{"oauth-envelope", `{"error":"invalid_token","error_description":"bad: field interval is required"}`, actAdd, "interval", ""},
		{"legacy-envelope", `{"errorMessage":"nope: field interval is required"}`, actAdd, "interval", ""},
		{"plain-text", `field interval is required`, actAdd, "interval", ""},
		{"empty", ``, actStop, "", ""},
		{"unparseable", `{"weird":true}`, actStop, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			act := classifyRefusal(&httpResult{body: []byte(tc.body)})
			if act.kind != tc.kind {
				t.Fatalf("kind = %v, want %v", act.kind, tc.kind)
			}
			if act.field != tc.field {
				t.Errorf("field = %q, want %q", act.field, tc.field)
			}
			switch tc.kind {
			case actBorrow:
				if act.collection != tc.extra {
					t.Errorf("collection = %q, want %q", act.collection, tc.extra)
				}
			case actRequires:
				if act.trigger != tc.extra {
					t.Errorf("trigger = %q, want %q", act.trigger, tc.extra)
				}
			case actAdd, actRemove:
				if act.condVal != tc.extra {
					t.Errorf("condVal = %q, want %q", act.condVal, tc.extra)
				}
			}
		})
	}
}

// TestUnit_Refine_SortedRefinementsDedupsAndOrders checks the summary signal
// is stable and duplicate-free however the variants ran.
func TestUnit_Refine_SortedRefinementsDedupsAndOrders(t *testing.T) {
	t.Parallel()
	in := []Refinement{
		{Entity: "monitor", Action: RefineAdd, Field: "interval"},
		{Entity: "monitor", Action: RefineAdd, Field: "interval"},
		{Entity: "assignment", Action: RefineBorrow, Field: "agent_id"},
		{Entity: "monitor", Action: RefineRemove, Field: "domain", GateField: "kind", GateValue: "ping"},
	}
	out := sortedRefinements(in)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3 (one duplicate dropped): %+v", len(out), out)
	}
	if out[0].Entity != "assignment" {
		t.Errorf("not sorted by entity first: %+v", out)
	}
	if sortedRefinements(nil) != nil {
		t.Error("empty input must yield nil")
	}
}

// TestUnit_Strategize_SynthesisHelpers exercises the value synthesis the
// refinement loop and the translator both draw on, across every branch.
func TestUnit_Strategize_SynthesisHelpers(t *testing.T) {
	t.Parallel()
	h := func(hh strategy.SynthHint) strategy.SynthHint { return hh }

	// synthValue priority and type fallbacks.
	if v := synthValue(h(strategy.SynthHint{Field: "x", Example: "ex"}), "e", "p"); v != "ex" {
		t.Errorf("example not preferred: %v", v)
	}
	if v := synthValue(h(strategy.SynthHint{Field: "x", Default: "df"}), "e", "p"); v != "df" {
		t.Errorf("default not used: %v", v)
	}
	if v := synthValue(h(strategy.SynthHint{Field: "x", Enum: []string{"a", "b"}}), "e", "p"); v != "a" {
		t.Errorf("enum not used: %v", v)
	}
	if v := synthValue(h(strategy.SynthHint{Field: "x", Format: "email"}), "e", "p"); v == nil {
		t.Error("format not used")
	}
	for _, tc := range []struct {
		typ  string
		want any
	}{
		{"boolean", true}, {"integer", 1}, {"number", 1.5},
	} {
		if v := synthValue(strategy.SynthHint{Field: "x", Type: tc.typ}, "e", "p"); v != tc.want {
			t.Errorf("type %s = %v, want %v", tc.typ, v, tc.want)
		}
	}
	if v := synthValue(strategy.SynthHint{Field: "label", Type: "string"}, "ent", "tfpfgen"); v != "tfpfgen-<runid>-ent-label" {
		t.Errorf("name-bearing string = %v", v)
	}
	if v := synthValue(strategy.SynthHint{Field: "color", Type: "string"}, "ent", "p"); v != "sample-color" {
		t.Errorf("plain string = %v", v)
	}
	if _, ok := synthValue(strategy.SynthHint{Field: "x", Type: "array"}, "e", "p").([]any); !ok {
		t.Error("array type did not synth a slice")
	}
	if _, ok := synthValue(strategy.SynthHint{Field: "x", Type: "object"}, "e", "p").(map[string]any); !ok {
		t.Error("object type did not synth a map")
	}

	// typedGate across declared types.
	if v := typedGate(strategy.SynthHint{Type: "boolean"}, "true"); v != true {
		t.Errorf("bool gate = %v", v)
	}
	if v := typedGate(strategy.SynthHint{Type: "integer"}, "7"); v != 7 {
		t.Errorf("int gate = %v", v)
	}
	if v := typedGate(strategy.SynthHint{Type: "number"}, "2.5"); v != 2.5 {
		t.Errorf("number gate = %v", v)
	}
	if v := typedGate(strategy.SynthHint{Type: "string"}, "dns"); v != "dns" {
		t.Errorf("string gate = %v", v)
	}
	if v := typedGate(strategy.SynthHint{Type: "integer"}, "notanint"); v != "notanint" {
		t.Errorf("bad int gate should fall back to the string: %v", v)
	}

	// variantValue produces a distinct second value per type.
	if v, ok := variantValue(strategy.SynthHint{Enum: []string{"a", "b"}}, "a"); !ok || v != "b" {
		t.Errorf("enum variant = %v", v)
	}
	if _, ok := variantValue(strategy.SynthHint{Enum: []string{"only"}}, "only"); ok {
		t.Error("single-value enum should have no variant")
	}
	if v, ok := variantValue(strategy.SynthHint{Type: "boolean"}, true); !ok || v != false {
		t.Errorf("bool variant = %v", v)
	}
	if v, ok := variantValue(strategy.SynthHint{Type: "string", Field: "n"}, "base"); !ok || v != "base-2" {
		t.Errorf("string variant = %v", v)
	}
	if v, ok := variantValue(strategy.SynthHint{Type: "integer"}, 1); !ok || v != 2 {
		t.Errorf("int variant = %v", v)
	}
	if _, ok := variantValue(strategy.SynthHint{Type: "object"}, nil); ok {
		t.Error("object has no scalar variant")
	}

	// formatValue covers every declared format.
	for _, f := range []string{"email", "uri", "url", "uuid", "date-time", "date", "hostname", "ipv4"} {
		if _, ok := formatValue(f, "e", "p"); !ok {
			t.Errorf("format %q not handled", f)
		}
	}
	if _, ok := formatValue("unknown-format", "e", "p"); ok {
		t.Error("unknown format should not synth")
	}
}

// TestUnit_Borrow_CollectionPaths pins the pluralisation the borrower tries.
func TestUnit_Borrow_CollectionPaths(t *testing.T) {
	t.Parallel()
	if got := collectionPaths("agent"); len(got) != 2 || got[0] != "/agent" || got[1] != "/agents" {
		t.Errorf("collectionPaths(agent) = %v", got)
	}
	if got := collectionPaths("agents"); len(got) != 1 || got[0] != "/agents" {
		t.Errorf("collectionPaths(agents) = %v", got)
	}
}
