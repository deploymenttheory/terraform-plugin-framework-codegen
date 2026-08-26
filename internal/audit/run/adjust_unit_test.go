package run

import (
	"context"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/quirkserver"
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

// TestUnit_Adjust_ClassifyRefusalGrammar pins the whole refusal-classification
// table: each sentence the quirk server's stable grammar emits, plus the
// envelope variants a real API might wrap it in, and the unintelligible case.
func TestUnit_Adjust_ClassifyRefusalGrammar(t *testing.T) {
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

		// An envelope that lists its complaints rather than stating one, and
		// spells the rejected property as a path into its own request object.
		{"listed-strings",
			`{"errors":["endpoint.streamEndpointUrl: Endpoint URL cannot be blank"],"httpStatus":"400 BAD_REQUEST"}`,
			actAdd, "streamEndpointUrl", ""},
		{"listed-objects",
			`{"errors":[{"field":"interval","message":"must not be null"}]}`,
			actAdd, "interval", ""},
		{"listed-under-messages",
			`{"messages":["interval: is required"]}`,
			actAdd, "interval", ""},
		{"listed-empty", `{"errors":[]}`, actStop, "", ""},
		// A field-prefixed complaint about the value that was sent, rather
		// than about its absence: adding a value cannot heal it.
		{"field-said-not-absence",
			`{"errors":["interval: must be one of 60, 120, 300"]}`,
			actStop, "", ""},
		// A sentence that merely contains a colon is not a field complaint.
		{"prose-with-colon",
			`{"detail":"Validation failed: the request was rejected"}`,
			actStop, "", ""},
		// Both shapes at once: the sentence only summarises, the list names
		// the field, so the list is what the loop must act on.
		{"summary-beside-listed",
			`{"detail":"There are invalid or missing fields","errors":[{"field":"testName","message":"must not be null"}],"title":"Request validation failed"}`,
			actAdd, "testName", ""},
		// A validation framework's own error object, which spells the field
		// as a code and the complaint as a default message.
		{"listed-code-and-default-message",
			`{"errors":[{"code":"name","defaultMessage":"must not be blank"}]}`,
			actAdd, "name", ""},
		// A refusal that names its field mid-sentence, wrapped in prose.
		{"field-named-in-prose",
			`{"title":"There were some errors in your request, please correct them before trying again. Error in field roleName : must not be null."}`,
			actAdd, "roleName", ""},
		// The same shape, but complaining about the value rather than its
		// absence: adding one cannot heal it.
		{"field-named-in-prose-not-absence",
			`{"title":"Error in field roleName : must be one of a, b"}`,
			actStop, "", ""},
		// Bare English naming only the field it wanted.
		{"the-field-is-required",
			`{"title":"The loginAccountGroup is required"}`,
			actAdd, "loginAccountGroup", ""},
		// "field X is required" still wins over the bare-English reading, so
		// the field is X and not the word "field".
		{"field-keyword-beats-bare-english",
			`{"detail":"the field interval is required"}`,
			actAdd, "interval", ""},
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

// TestUnit_Adjust_SortedAdjustmentsDedupsAndOrders checks the summary signal
// is stable and duplicate-free however the variants ran.
func TestUnit_Adjust_SortedAdjustmentsDedupsAndOrders(t *testing.T) {
	t.Parallel()
	in := []infer.RequestAdjustment{
		{Entity: "monitor", Action: infer.AdjustAdd, Field: "interval"},
		{Entity: "monitor", Action: infer.AdjustAdd, Field: "interval"},
		{Entity: "assignment", Action: infer.AdjustBorrow, Field: "agent_id"},
		{Entity: "monitor", Action: infer.AdjustRemove, Field: "domain", GateField: "kind", GateValue: "ping"},
	}
	out := sortedAdjustments(in)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3 (one duplicate dropped): %+v", len(out), out)
	}
	if out[0].Entity != "assignment" {
		t.Errorf("not sorted by entity first: %+v", out)
	}
	if sortedAdjustments(nil) != nil {
		t.Error("empty input must yield nil")
	}
}

// TestUnit_Strategize_SynthesisHelpers exercises the value synthesis the
// adjustment loop and the translator both draw on, across every branch.
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
	if v := synthValue(h(strategy.SynthHint{Field: "x", Enum: []any{"a", "b"}}), "e", "p"); v != "a" {
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
	if v, ok := variantValue(strategy.SynthHint{Enum: []any{"a", "b"}}, "a"); !ok || v != "b" {
		t.Errorf("enum variant = %v", v)
	}
	if _, ok := variantValue(strategy.SynthHint{Enum: []any{"only"}}, "only"); ok {
		t.Error("single-value enum should have no variant")
	}
	if v, ok := variantValue(strategy.SynthHint{Type: "boolean"}, true); !ok || v != false {
		t.Errorf("bool variant = %v", v)
	}
	if v, ok := variantValue(strategy.SynthHint{Type: "string", Field: "n"}, "base"); !ok || v != "base-2" {
		t.Errorf("string variant = %v", v)
	}
	if v, ok := variantValue(strategy.SynthHint{Type: "integer"}, 1); !ok || v != int64(2) {
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

// TestUnit_Adjust_ParentRecreationHealsWithoutRecording pins the split
// between healing and recording. A parent re-created so a child has something
// to address must still be healed — otherwise every child of an understated
// parent blocks — but the fields it needed are facts about the parent, and
// recording them here would attribute them to the child that asked.
func TestUnit_Adjust_ParentRecreationHealsWithoutRecording(t *testing.T) {
	t.Parallel()

	refusal := &httpResult{body: []byte(
		`{"detail":"There are invalid or missing fields","errors":[{"field":"testName","message":"must not be null"}]}`)}

	r := &runner{opts: Options{NamePrefix: "tfpfgen"}}
	ent := &entityState{plan: &plan.EntityPlan{Entity: "scheduled_test"}}
	body := map[string]any{}

	if !r.applyAdjustment(context.Background(), ent, body, refusal, map[string]bool{}, false) {
		t.Fatal("a listed field complaint did not heal the body")
	}
	if _, added := body["testName"]; !added {
		t.Errorf("the named field was not added: %#v", body)
	}
	if len(r.adjustments) != 0 {
		t.Errorf("a silent heal recorded %d adjustment(s)", len(r.adjustments))
	}
}

// TestUnit_Evidence_TheIdentifyingPropertyIsFoundByValue pins how an entity
// whose response spells its id differently from its path is identified: by
// matching the id the run already learned against the body, never by name.
func TestUnit_Evidence_TheIdentifyingPropertyIsFoundByValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  map[string]any
		id   string
		want string
	}{
		{"a plain id key wins outright",
			map[string]any{"id": "7", "aid": "9"}, "9", "id"},
		{"the property carrying the learned id",
			map[string]any{"aid": "281474976717041", "accountGroupName": "x"}, "281474976717041", "aid"},
		{"a number renders without a decimal point",
			map[string]any{"roleId": float64(42)}, "42", "roleId"},
		{"sorted, so one response names one property",
			map[string]any{"bid": "5", "aid": "5"}, "5", "aid"},
		{"no id learned names nothing",
			map[string]any{"aid": "9"}, "", ""},
		{"no property carries it",
			map[string]any{"name": "x"}, "9", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := identifyingProperty(tc.got, tc.id); got != tc.want {
				t.Errorf("identifyingProperty = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnit_Search_CandidatesAreOrderedCheapestSignalFirst pins the order the
// additive search adds fields in. The order decides how many live creates a
// blocked entity costs, and it must be the same on a re-run.
func TestUnit_Search_CandidatesAreOrderedCheapestSignalFirst(t *testing.T) {
	t.Parallel()

	r := &runner{hints: map[string]map[string]strategy.SynthHint{
		"widget": {
			"alreadySent": {Field: "alreadySent", Type: "string"},
			"zNamed":      {Field: "zNamed", Type: "string"},
			"aPlain":      {Field: "aPlain", Type: "string"},
			"bPlain":      {Field: "bPlain", Type: "string"},
			"withEnum":    {Field: "withEnum", Type: "string", Enum: []any{"x"}},
			"nested":      {Field: "nested", Type: "object"},
		},
	}}
	ent := &entityState{plan: &plan.EntityPlan{Entity: "widget"}}
	body := map[string]any{"alreadySent": "v"}
	refusal := &httpResult{body: []byte(`{"detail":"zNamed is wrong somehow"}`)}

	got := r.searchCandidates(ent, body, refusal)
	want := []string{"zNamed", "withEnum", "aPlain", "bPlain", "nested"}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
	}
	// A field the body already carries is never a candidate.
	for _, f := range got {
		if f == "alreadySent" {
			t.Error("a field already in the body was offered as a candidate")
		}
	}
}

// TestUnit_Search_AllowanceIsBounded pins the ceiling on how many live creates
// one entity's search may spend.
func TestUnit_Search_AllowanceIsBounded(t *testing.T) {
	t.Parallel()
	if got := searchAllowance(3); got != 3 {
		t.Errorf("searchAllowance(3) = %d, want 3", got)
	}
	if got := searchAllowance(500); got != 24 {
		t.Errorf("searchAllowance(500) = %d, want the cap", got)
	}
}

// TestUnit_Search_MaximalCulpritPrefersTheNamedField pins which field the
// reduction drops next. A refusal that names one is believed; otherwise the
// choice is the last in order, so a re-run reduces the same way.
func TestUnit_Search_MaximalCulpritPrefersTheNamedField(t *testing.T) {
	t.Parallel()

	r := &runner{}
	body := map[string]any{"name": "n", "colour": "c", "shape": "s"}
	minimal := map[string]any{"name": "n"}

	named := &httpResult{body: []byte(`{"detail":"colour is not valid here"}`)}
	if got := r.maximalCulprit(body, minimal, named); got != "colour" {
		t.Errorf("culprit = %q, want the field the refusal named", got)
	}

	// Nothing named: the last optional field in order, never a field the
	// minimal create needs.
	silent := &httpResult{body: []byte(`{"detail":"bad request"}`)}
	if got := r.maximalCulprit(body, minimal, silent); got != "shape" {
		t.Errorf("culprit = %q, want the last optional field", got)
	}

	// Only the minimal body left: there is nothing safe to drop.
	if got := r.maximalCulprit(minimal, minimal, silent); got != "" {
		t.Errorf("culprit = %q, want none", got)
	}
}
