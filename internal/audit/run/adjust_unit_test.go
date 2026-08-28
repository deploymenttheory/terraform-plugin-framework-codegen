package run

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/testapiserver"
)

// TestUnit_Borrow_BorrowsCachesAndReportsEmpty covers the borrower's three
// outcomes: a real id from a served collection, the cache hit that costs no
// request, and the false the caller reads as inconclusive when the collection
// does not exist.
func TestUnit_Borrow_BorrowsCachesAndReportsEmpty(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})
	r, err := newRunner(testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "assignment", Budget: plan.Budget{Requests: 20}}}

	id, ok := r.borrow(context.Background(), entity, "agent")
	if !ok || id == "" {
		t.Fatalf("borrow(agent) = %q, %v; want a real id", id, ok)
	}
	before := r.reqTotal
	if id2, ok := r.borrow(context.Background(), entity, "agent"); !ok || id2 != id {
		t.Errorf("cached borrow = %q, %v; want the same id", id2, ok)
	}
	if r.reqTotal != before {
		t.Errorf("a cached borrow spent %d requests, want none", r.reqTotal-before)
	}
	if _, ok := r.borrow(context.Background(), entity, "widget"); ok {
		t.Error("borrow of a collection the server does not serve must report false")
	}
}

// TestUnit_Adjust_ClassifyRefusalGrammar pins the whole refusal-classification
// table: each sentence the test API server's stable grammar emits, plus the
// envelope variants a real API might wrap it in, and the unintelligible case.
func TestUnit_Adjust_ClassifyRefusalGrammar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		body  string
		kind  adjustmentKind
		field string
		extra string // collection / trigger / condVal, per kind
	}{
		{"required", `{"detail":"field interval is required"}`, adjustmentAdd, "interval", ""},
		{"bare-member", `{"title":"400 Bad Request\nrepeat type must be specified"}`, adjustmentAdd, "type", ""},
		{"required-when", `{"detail":"field target_host is required when kind=ping"}`, adjustmentAdd, "target_host", "ping"},
		{"not-valid", `{"detail":"field domain is not valid when kind=ping"}`, adjustmentRemove, "domain", "ping"},
		{"requires", `{"detail":"field dnssec requires field domain to be set"}`, adjustmentRequires, "domain", "dnssec"},
		{"reference", `{"detail":"field agent_id must reference an existing agent"}`, adjustmentBorrow, "agent_id", "agent"},
		{"enum-list", `{"detail":"field kind must be one of ping, web, dns"}`, adjustmentNone, "", ""},
		{"bare-field", `{"title":"missing required field","detail":"serial"}`, adjustmentNone, "", ""},
		{"oauth-envelope", `{"error":"invalid_token","error_description":"bad: field interval is required"}`, adjustmentAdd, "interval", ""},
		{"legacy-envelope", `{"errorMessage":"nope: field interval is required"}`, adjustmentAdd, "interval", ""},
		{"plain-text", `field interval is required`, adjustmentAdd, "interval", ""},
		{"empty", ``, adjustmentNone, "", ""},
		{"unparseable", `{"weird":true}`, adjustmentNone, "", ""},

		// An envelope that lists its complaints rather than stating one, and
		// spells the rejected property as a path into its own request object.
		{"listed-strings",
			`{"errors":["endpoint.streamEndpointUrl: Endpoint URL cannot be blank"],"httpStatus":"400 BAD_REQUEST"}`,
			adjustmentAdd, "streamEndpointUrl", ""},
		{"listed-objects",
			`{"errors":[{"field":"interval","message":"must not be null"}]}`,
			adjustmentAdd, "interval", ""},
		{"listed-under-messages",
			`{"messages":["interval: is required"]}`,
			adjustmentAdd, "interval", ""},
		{"listed-empty", `{"errors":[]}`, adjustmentNone, "", ""},
		// A field-prefixed complaint about the value that was sent, rather
		// than about its absence: adding a value cannot correct it.
		{"field-said-not-absence",
			`{"errors":["interval: must be one of 60, 120, 300"]}`,
			adjustmentNone, "", ""},
		// A sentence that merely contains a colon is not a field complaint.
		{"prose-with-colon",
			`{"detail":"Validation failed: the request was rejected"}`,
			adjustmentNone, "", ""},
		// Both shapes at once: the sentence only summarises, the list names
		// the field, so the list is what the loop must act on.
		{"summary-beside-listed",
			`{"detail":"There are invalid or missing fields","errors":[{"field":"testName","message":"must not be null"}],"title":"Request validation failed"}`,
			adjustmentAdd, "testName", ""},
		// A validation framework's own error object, which spells the field
		// as a code and the complaint as a default message.
		{"listed-code-and-default-message",
			`{"errors":[{"code":"name","defaultMessage":"must not be blank"}]}`,
			adjustmentAdd, "name", ""},
		// A refusal that names its field mid-sentence, wrapped in prose.
		{"field-named-in-prose",
			`{"title":"There were some errors in your request, please correct them before trying again. Error in field roleName : must not be null."}`,
			adjustmentAdd, "roleName", ""},
		// The same shape, but complaining about the value rather than its
		// absence: adding one cannot correct it.
		{"field-named-in-prose-not-absence",
			`{"title":"Error in field roleName : must be one of a, b"}`,
			adjustmentNone, "", ""},
		// Bare English naming only the field it wanted.
		{"the-field-is-required",
			`{"title":"The loginAccountGroup is required"}`,
			adjustmentAdd, "loginAccountGroup", ""},
		// "field X is required" still wins over the bare-English reading, so
		// the field is X and not the word "field".
		{"field-keyword-beats-bare-english",
			`{"detail":"the field interval is required"}`,
			adjustmentAdd, "interval", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			act := classifyRefusal(&httpResult{body: []byte(testCase.body)})
			if act.kind != testCase.kind {
				t.Fatalf("kind = %v, want %v", act.kind, testCase.kind)
			}
			if act.field != testCase.field {
				t.Errorf("field = %q, want %q", act.field, testCase.field)
			}
			switch testCase.kind {
			case adjustmentBorrow:
				if act.collection != testCase.extra {
					t.Errorf("collection = %q, want %q", act.collection, testCase.extra)
				}
			case adjustmentRequires:
				if act.trigger != testCase.extra {
					t.Errorf("trigger = %q, want %q", act.trigger, testCase.extra)
				}
			case adjustmentAdd, adjustmentRemove:
				if act.condVal != testCase.extra {
					t.Errorf("condVal = %q, want %q", act.condVal, testCase.extra)
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
	h := func(hh strategy.SyntheticValueRules) strategy.SyntheticValueRules { return hh }

	// synthValue priority and type fallbacks.
	if v := synthesiseValue(h(strategy.SyntheticValueRules{Field: "x", Example: "ex"}), "e", "p"); v != "ex" {
		t.Errorf("example not preferred: %v", v)
	}
	if v := synthesiseValue(h(strategy.SyntheticValueRules{Field: "x", Default: "df"}), "e", "p"); v != "df" {
		t.Errorf("default not used: %v", v)
	}
	if v := synthesiseValue(h(strategy.SyntheticValueRules{Field: "x", Enum: []any{"a", "b"}}), "e", "p"); v != "a" {
		t.Errorf("enum not used: %v", v)
	}
	if v := synthesiseValue(h(strategy.SyntheticValueRules{Field: "x", Format: "email"}), "e", "p"); v == nil {
		t.Error("format not used")
	}
	for _, testCase := range []struct {
		typ  string
		want any
	}{
		{"boolean", true}, {"integer", 1}, {"number", 1.5},
	} {
		if v := synthesiseValue(strategy.SyntheticValueRules{Field: "x", Type: testCase.typ}, "e", "p"); v != testCase.want {
			t.Errorf("type %s = %v, want %v", testCase.typ, v, testCase.want)
		}
	}
	if v := synthesiseValue(strategy.SyntheticValueRules{Field: "label", Type: "string"}, "ent", "tfpfgen"); v != "tfpfgen-<runid>-ent-label" {
		t.Errorf("name-bearing string = %v", v)
	}
	// A name-bearing field takes the invented token over a declared example:
	// this is the path a live run synthesises from, and an API that requires a
	// unique name refuses the example every run after the first.
	if v := synthesiseValue(strategy.SyntheticValueRules{Field: "name", Type: "string", Example: "My thing"},
		"ent", "tfpfgen"); v != "tfpfgen-<runid>-ent-name" {
		t.Errorf("a name-bearing example = %#v, want the invented token", v)
	}
	// A field an enum, a format or a pattern constrains is not one to invent a
	// name for, so the ordinary priority stands.
	if v := synthesiseValue(strategy.SyntheticValueRules{Field: "name", Type: "string", Example: "My thing",
		Pattern: "^[A-Za-z ]+$"}, "ent", "tfpfgen"); v != "My thing" {
		t.Errorf("a constrained name = %#v, want the declared example", v)
	}
	if v := synthesiseValue(strategy.SyntheticValueRules{Field: "name", Type: "string", Format: "email"},
		"ent", "tfpfgen"); v != "tfpfgen-<runid>@example.invalid" {
		t.Errorf("a formatted name = %#v, want the format-driven value", v)
	}
	if v := synthesiseValue(strategy.SyntheticValueRules{Field: "color", Type: "string"}, "ent", "p"); v != "sample-color" {
		t.Errorf("plain string = %v", v)
	}
	if _, ok := synthesiseValue(strategy.SyntheticValueRules{Field: "x", Type: "array"}, "e", "p").([]any); !ok {
		t.Error("array type did not synth a slice")
	}
	if _, ok := synthesiseValue(strategy.SyntheticValueRules{Field: "x", Type: "object"}, "e", "p").(map[string]any); !ok {
		t.Error("object type did not synth a map")
	}

	// typedGate across declared types.
	if v := typedGate(strategy.SyntheticValueRules{Type: "boolean"}, "true"); v != true {
		t.Errorf("bool gate = %v", v)
	}
	if v := typedGate(strategy.SyntheticValueRules{Type: "integer"}, "7"); v != 7 {
		t.Errorf("int gate = %v", v)
	}
	if v := typedGate(strategy.SyntheticValueRules{Type: "number"}, "2.5"); v != 2.5 {
		t.Errorf("number gate = %v", v)
	}
	if v := typedGate(strategy.SyntheticValueRules{Type: "string"}, "dns"); v != "dns" {
		t.Errorf("string gate = %v", v)
	}
	if v := typedGate(strategy.SyntheticValueRules{Type: "integer"}, "notanint"); v != "notanint" {
		t.Errorf("bad int gate should fall back to the string: %v", v)
	}

	// variantValue produces a distinct second value per type.
	if v, ok := variantValue(strategy.SyntheticValueRules{Enum: []any{"a", "b"}}, "a"); !ok || v != "b" {
		t.Errorf("enum variant = %v", v)
	}
	if _, ok := variantValue(strategy.SyntheticValueRules{Enum: []any{"only"}}, "only"); ok {
		t.Error("single-value enum should have no variant")
	}
	if v, ok := variantValue(strategy.SyntheticValueRules{Type: "boolean"}, true); !ok || v != false {
		t.Errorf("bool variant = %v", v)
	}
	if v, ok := variantValue(strategy.SyntheticValueRules{Type: "string", Field: "n"}, "base"); !ok || v != "base-2" {
		t.Errorf("string variant = %v", v)
	}
	if v, ok := variantValue(strategy.SyntheticValueRules{Type: "integer"}, 1); !ok || v != int64(2) {
		t.Errorf("int variant = %v", v)
	}
	if _, ok := variantValue(strategy.SyntheticValueRules{Type: "object"}, nil); ok {
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
// between correcting and recording. A parent re-created so a child has something
// to address must still be corrected — otherwise every child of an understated
// parent blocks — but the fields it needed are facts about the parent, and
// recording them here would attribute them to the child that asked.
func TestUnit_Adjust_ParentRecreationHealsWithoutRecording(t *testing.T) {
	t.Parallel()

	refusal := &httpResult{body: []byte(
		`{"detail":"There are invalid or missing fields","errors":[{"field":"testName","message":"must not be null"}]}`)}

	r := &runner{opts: Options{NamePrefix: "tfpfgen"}}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "scheduled_test"}}
	body := map[string]any{}

	added, ok := r.applyAdjustment(context.Background(), entity, body, refusal, map[string]bool{}, false)
	if !ok {
		t.Fatal("a listed field complaint did not correct the body")
	}
	if _, in := body["testName"]; !in {
		t.Errorf("the named field was not added: %#v", body)
	}
	// The add is answered, not asserted: only an accepted create makes it a
	// fact about the entity.
	if len(added) != 1 || added[0].field != "testName" {
		t.Errorf("the add was not answered for the caller to confirm: %+v", added)
	}
	if len(r.adjustments) != 0 {
		t.Errorf("a silent correct recorded %d adjustment(s)", len(r.adjustments))
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
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := identifyingProperty(testCase.got, testCase.id); got != testCase.want {
				t.Errorf("identifyingProperty = %q, want %q", got, testCase.want)
			}
		})
	}
}

// TestUnit_Search_CandidatesAreOrderedCheapestSignalFirst pins the order the
// additive search adds fields in. The order decides how many live creates a
// blocked entity costs, and it must be the same on a re-run.
func TestUnit_Search_CandidatesAreOrderedCheapestSignalFirst(t *testing.T) {
	t.Parallel()

	r := &runner{hints: map[string]map[string]strategy.SyntheticValueRules{
		"widget": {
			"alreadySent": {Field: "alreadySent", Type: "string"},
			"zNamed":      {Field: "zNamed", Type: "string"},
			"aPlain":      {Field: "aPlain", Type: "string"},
			"bPlain":      {Field: "bPlain", Type: "string"},
			"withEnum":    {Field: "withEnum", Type: "string", Enum: []any{"x"}},
			"nested":      {Field: "nested", Type: "object"},
		},
	}}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "widget"}}
	body := map[string]any{"alreadySent": "v"}
	refusal := &httpResult{body: []byte(`{"detail":"zNamed is wrong somehow"}`)}

	got := r.fieldsToTry(entity, body, refusal)
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
	if got := fieldAdditionAttemptLimit(3); got != 3 {
		t.Errorf("searchAllowance(3) = %d, want 3", got)
	}
	if got := fieldAdditionAttemptLimit(500); got != 24 {
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
	if got := r.refusedOptionalFields(body, minimal, named); !reflect.DeepEqual(got, []string{"colour"}) {
		t.Errorf("refusedFields = %v, want the field the refusal named", got)
	}

	// Several named at once are all dropped in one attempt.
	several := &httpResult{body: []byte(`{"detail":"Invalid value for fields: colour, shape"}`)}
	if got := r.refusedOptionalFields(body, minimal, several); !reflect.DeepEqual(got, []string{"colour", "shape"}) {
		t.Errorf("refusedFields = %v, want every field the refusal named", got)
	}

	// Nothing named: the last optional field in order, never a field the
	// minimal create needs.
	silent := &httpResult{body: []byte(`{"detail":"bad request"}`)}
	if got := r.refusedOptionalFields(body, minimal, silent); !reflect.DeepEqual(got, []string{"shape"}) {
		t.Errorf("refusedFields = %v, want the last optional field", got)
	}

	// Only the minimal body left: there is nothing safe to drop.
	if got := r.refusedOptionalFields(minimal, minimal, silent); got != nil {
		t.Errorf("refusedFields = %v, want none", got)
	}
}

// TestUnit_Strategize_AnOperatorValueOutranksEverySynthesis: the value an
// operator supplies is what the skeleton sends, over an example, an enum and
// the variant's own gate.
func TestUnit_Strategize_AnOperatorValueOutranksEverySynthesis(t *testing.T) {
	t.Parallel()
	sk := strategy.RequestFields{
		Fields: []string{"endpoint", "kind", "name"},
		Rules: []strategy.SyntheticValueRules{
			{Field: "endpoint", Type: "string", Example: "https://unreachable.invalid"},
			{Field: "kind", Type: "string", Enum: []any{"ping", "web"}},
			{Field: "name", Type: "string"},
		},
	}
	values := map[string]any{"endpoint": "https://reachable.example", "kind": "web"}

	body := bodySynthesis{entity: "monitor", prefix: "tfpfgen", values: values}.requestBody(sk, "kind", "ping")

	// The operator supplies a value precisely for the field no synthesis can
	// guess: an example the API cannot reach is still an example.
	if body["endpoint"] != "https://reachable.example" {
		t.Errorf("endpoint = %#v, want the operator's value", body["endpoint"])
	}
	// Even the gate yields: a discriminator is one of the things an operator
	// has to supply when the document does not say which shape is valid.
	if body["kind"] != "web" {
		t.Errorf("kind = %#v, want the operator's value over the variant gate", body["kind"])
	}
	// A field the operator said nothing about is synthesised as before.
	if body["name"] != "tfpfgen-<runid>-monitor-name" {
		t.Errorf("name = %#v, want the invented name", body["name"])
	}
}

func TestUnit_Adjust_ClassifyRefusalWiderGrammar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		body       string
		field      string
		candidates []string
		declared   bool
	}{
		// A deserialiser naming the discriminator it could not find.
		{"missing-property",
			`{"message":"JSON parse error: Could not resolve subtype of [simple type, class Authentication]: missing type id property 'type' (for POJO property 'authentication')"}`,
			"type", nil, false},
		// A choice: any one of the listed fields satisfies the refusal.
		{"at-least-one",
			`{"errors":[{"defaultMessage":"At least one of the following is required: payload, query params or headers."}]}`,
			"payload", []string{"payload", "query params", "headers"}, true},
		// The word before an absence complaint, with nothing vouching for it
		// as a wire property.
		{"bare-absent", `{"title":"400 Bad Request\nendRepeat must be specified"}`, "endRepeat", nil, true},
		{"bare-absent-qualified", `{"title":"400 Bad Request\nalertSuppressionWindow name must be specified"}`, "name", nil, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := classifyRefusal(&httpResult{status: 400, body: []byte(testCase.body)})
			if got.kind != adjustmentAdd || got.field != testCase.field {
				t.Fatalf("classify = %+v, want add %s", got, testCase.field)
			}
			if got.mustBeDeclared != testCase.declared {
				t.Errorf("mustBeDeclared = %v, want %v", got.mustBeDeclared, testCase.declared)
			}
			if testCase.candidates != nil && !reflect.DeepEqual(got.candidates, testCase.candidates) {
				t.Errorf("candidates = %v, want %v", got.candidates, testCase.candidates)
			}
		})
	}
}

func TestUnit_Adjust_AddFieldPrefersWhatTheEntityDeclares(t *testing.T) {
	t.Parallel()
	known := map[string]strategy.SyntheticValueRules{
		"payload": {Field: "payload"}, "queryParams": {Field: "queryParams"}, "repeat": {Field: "repeat"},
	}
	r := &runner{hints: map[string]map[string]strategy.SyntheticValueRules{"webhook": known}}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "webhook"}}

	// The first offered field the body lacks, in the entity's own spelling.
	choice := parsedRefusal{kind: adjustmentAdd, field: "payload", candidates: []string{"payload", "query params", "headers"}, mustBeDeclared: true}
	if got := r.addField(entity, map[string]any{"payload": "{}"}, choice, ""); got != "queryParams" {
		t.Errorf("addField = %q, want queryParams", got)
	}
	// A loose sentence whose word is not a declared field falls back on a
	// declared field the sentence names, and otherwise adds nothing.
	loose := parsedRefusal{kind: adjustmentAdd, field: "conditions", mustBeDeclared: true}
	if got := r.addField(entity, map[string]any{}, loose, "repeat conditions must be specified"); got != "repeat" {
		t.Errorf("addField = %q, want the declared field the sentence names", got)
	}
	if got := r.addField(entity, map[string]any{}, loose, "conditions must be specified"); got != "" {
		t.Errorf("addField = %q, want nothing for an undeclared word", got)
	}
	// A marked field is taken at its word even when undeclared: an API
	// routinely requires a property the document omits.
	marked := parsedRefusal{kind: adjustmentAdd, field: "loginAccountGroupId"}
	if got := r.addField(entity, map[string]any{}, marked, ""); got != "loginAccountGroupId" {
		t.Errorf("addField = %q, want the marked field", got)
	}
	// Without hints — a run with no strategy — the first absent candidate.
	bare := &runner{}
	if got := bare.addField(entity, map[string]any{"payload": 1}, choice, ""); got != "headers" {
		t.Errorf("addField without hints = %q, want headers (query params carries a space)", got)
	}
}

func TestUnit_Cycle_QuotedEnumFieldAndSegments(t *testing.T) {
	t.Parallel()
	hints := map[string]strategy.SyntheticValueRules{
		"type": {Field: "type", Enum: []any{"generic"}},
		"name": {Field: "name"},
	}
	body := map[string]any{"type": "generic", "name": "generic"}
	field, value := quotedEnumField("Could not resolve type id 'generic' as a subtype of X", body, hints)
	if field != "type" || value != "generic" {
		t.Errorf("quotedEnumField = %q %q, want type generic", field, value)
	}
	if field, _ := quotedEnumField(`object "generic" not found`, map[string]any{"name": "generic"}, hints); field != "" {
		t.Errorf("a quoted value under a non-enum field matched: %q", field)
	}
	if got := staticSegments("/connectors/{kind}/panorama"); !reflect.DeepEqual(got, []string{"connectors", "panorama"}) {
		t.Errorf("staticSegments = %v", got)
	}
}

func TestUnit_Adjust_ANestedMemberTheAPIRefusesIsRemovedWhereItSits(t *testing.T) {
	t.Parallel()
	got := classifyRefusal(&httpResult{status: 400, body: []byte(`{"errors":[{"field":"secrets[0].id","message":"must be null"}]}`)})
	if got.kind != adjustmentRemove || got.field != "secrets[0].id" {
		t.Fatalf("classify = %+v, want remove secrets[0].id", got)
	}
	body := map[string]any{
		"name":    "x",
		"secrets": []any{map[string]any{"id": "stale", "name": "s"}, map[string]any{"id": "keep"}},
		"auth":    map[string]any{"username": "u", "type": "basic"},
	}
	if !removePath(body, "secrets[0].id") {
		t.Fatal("secrets[0].id was not removed")
	}
	if _, still := body["secrets"].([]any)[0].(map[string]any)["id"]; still {
		t.Error("secrets[0].id survived")
	}
	if _, kept := body["secrets"].([]any)[1].(map[string]any)["id"]; !kept {
		t.Error("secrets[1].id was removed instead")
	}
	if !removePath(body, "auth.username") || len(body["auth"].(map[string]any)) != 1 {
		t.Errorf("auth.username was not removed: %v", body["auth"])
	}
	if !removePath(body, "secrets[1]") || len(body["secrets"].([]any)) != 1 {
		t.Errorf("secrets[1] was not removed: %v", body["secrets"])
	}
	if removePath(body, "secrets[7].id") || removePath(body, "nothing.here") || removePath(body, "name.deeper") {
		t.Error("a path that names nothing reported a removal")
	}
	r := &runner{}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "vault"}}
	applied := map[string]bool{}
	if _, ok := r.applyAdjustment(context.Background(), entity, body, &httpResult{status: 400,
		body: []byte(`{"errors":[{"field":"auth.type","message":"must be null"}]}`)}, applied, true); !ok {
		t.Fatal("the nested removal was not applied")
	}
	if _, still := body["auth"].(map[string]any)["type"]; still {
		t.Error("auth.type survived the adjustment")
	}
}

func TestUnit_Adjust_ARefusedChoiceMovesOnToTheNextOffered(t *testing.T) {
	t.Parallel()
	known := map[string]strategy.SyntheticValueRules{
		"payload": {Field: "payload"}, "queryParams": {Field: "queryParams"}, "headers": {Field: "headers"},
	}
	r := &runner{hints: map[string]map[string]strategy.SyntheticValueRules{"webhook": known}}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "webhook"}}
	choice := &pendingAdd{field: "payload", candidates: []string{"payload", "query params", "headers"}}
	if next := r.nextChoice(entity, map[string]any{"payload": "{}"}, choice); next != "queryParams" {
		t.Errorf("nextChoice = %q, want queryParams", next)
	}
	choice.field = "queryParams"
	if next := r.nextChoice(entity, map[string]any{"queryParams": "{}", "headers": []any{}}, choice); next != "" {
		t.Errorf("nextChoice = %q, want nothing once every candidate is present or spent", next)
	}
	adds := withoutAdd([]pendingAdd{{field: "type"}, {field: "payload"}}, "payload")
	if len(adds) != 1 || adds[0].field != "type" {
		t.Errorf("withoutAdd = %+v", adds)
	}
}

func TestUnit_Adjust_ExactlyOneOfAPairRemovesTheSecond(t *testing.T) {
	t.Parallel()
	got := classifyRefusal(&httpResult{status: 400, body: []byte(`{"title":"minimumSources: Exactly 1 field must be defined between minimumSources and minimumSourcesPct"}`)})
	if got.kind != adjustmentRemove || got.field != "minimumSourcesPct" || got.mutuallyExclusiveWith != "minimumSources" {
		t.Fatalf("classify = %+v, want remove minimumSourcesPct beside minimumSources", got)
	}
	r := &runner{}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "rule"}}
	body := map[string]any{"minimumSources": 10, "minimumSourcesPct": 99}
	if _, ok := r.applyAdjustment(context.Background(), entity, body, &httpResult{status: 400,
		body: []byte(`{"title":"Exactly 1 field must be defined between minimumSources and minimumSourcesPct"}`)}, map[string]bool{}, false); !ok {
		t.Fatal("the pair was not reduced")
	}
	if _, still := body["minimumSourcesPct"]; still || body["minimumSources"] != 10 {
		t.Errorf("body = %v, want the first of the pair alone", body)
	}
	// With only one of the pair present the sentence is about something
	// else, and removing the one present would leave neither.
	lone := map[string]any{"minimumSourcesPct": 99}
	if _, ok := r.applyAdjustment(context.Background(), entity, lone, &httpResult{status: 400,
		body: []byte(`{"title":"Exactly 1 field must be defined between minimumSources and minimumSourcesPct"}`)}, map[string]bool{}, false); ok {
		t.Error("a lone field of the pair was removed")
	}
}

func TestUnit_Adjust_ANumberRefusedAsNotPositiveTakesTheSmallestPositiveValue(t *testing.T) {
	t.Parallel()
	got := classifyRefusal(&httpResult{status: 400, body: []byte(`{"title":"400 Bad Request\nduration must be a positive value"}`)})
	if got.kind != adjustmentRevalue || got.field != "duration" || got.revalue != positiveValue {
		t.Fatalf("classify = %+v, want a positive revalue of duration", got)
	}
	minimum := 30.0
	hints := map[string]strategy.SyntheticValueRules{
		"duration": {Field: "duration", Type: "integer"},
		"ratio":    {Field: "ratio", Type: "number", Minimum: &minimum},
	}
	r := &runner{hints: map[string]map[string]strategy.SyntheticValueRules{"window": hints}}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "window"}}
	body := map[string]any{"duration": 0, "ratio": -1.5}
	refusal := &httpResult{status: 400, body: []byte(`{"title":"duration must be a positive value"}`)}
	if _, ok := r.applyAdjustment(context.Background(), entity, body, refusal, map[string]bool{}, false); !ok || body["duration"] != int64(1) {
		t.Errorf("duration = %#v, want 1", body["duration"])
	}
	if v, ok := revalued(positiveValue, -1.5, hints["ratio"]); !ok || v != 30.0 {
		t.Errorf("a bounded number = %#v, want the declared minimum", v)
	}
	if _, ok := revalued(positiveValue, 5, hints["duration"]); ok {
		t.Error("a positive value was replaced")
	}
}

func TestUnit_Cycle_OnlyTheNamedEnumFieldsAreCycled(t *testing.T) {
	t.Parallel()
	hints := map[string]strategy.SyntheticValueRules{
		"kind":     {Field: "kind", Enum: []any{"a", "b"}},
		"protocol": {Field: "protocol", Enum: []any{"tcp", "udp"}},
		"cert":     {Field: "cert"},
	}
	body := map[string]any{"kind": "a", "protocol": "tcp", "cert": "x"}
	got := retryableSiblings(body, hints, "", []string{"cert", "protocol"})
	if len(got) != 1 || got[0].Field != "protocol" {
		t.Errorf("siblings = %+v, want protocol alone: the named enum field", got)
	}
	if got := retryableSiblings(body, hints, "protocol", []string{"protocol"}); len(got) != 0 {
		t.Errorf("the held field was cycled: %+v", got)
	}
}

func TestUnit_Adjust_AWordBeforeTheComplaintFallsBackOnTheDeclaredFieldNamed(t *testing.T) {
	t.Parallel()
	known := map[string]strategy.SyntheticValueRules{
		"repeat": {Field: "repeat", Type: "object"}, "duration": {Field: "duration", Type: "integer"},
	}
	r := &runner{hints: map[string]map[string]strategy.SyntheticValueRules{"window": known}}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "window"}}
	body := map[string]any{"duration": 1, "name": "n"}
	res := &httpResult{status: 400, body: []byte(`{"status":400,"title":"400 Bad Request\nrepeat conditions must be specified"}`)}
	act := classifyRefusal(res)
	if act.kind != adjustmentAdd {
		t.Fatalf("classify = %+v, want an add", act)
	}
	if got := r.addField(entity, body, act, refusalMessage(res.body)); got != "repeat" {
		t.Fatalf("addField = %q, want repeat", got)
	}
	if _, ok := r.applyAdjustment(context.Background(), entity, body, res, map[string]bool{}, false); !ok {
		t.Fatal("the adjustment was not applied")
	}
	if _, has := body["repeat"]; !has {
		t.Errorf("repeat was not added: %v", body)
	}
}

func TestUnit_Adjust_ADeclaredNameTheRefusalQualifiesIsStillNamed(t *testing.T) {
	t.Parallel()
	known := map[string]strategy.SyntheticValueRules{
		"startDate": {Field: "startDate"}, "id": {Field: "id"}, "name": {Field: "name"},
	}
	if got := declaredSpelling("startDateTime", known); got != "startDate" {
		t.Errorf("declaredSpelling(startDateTime) = %q, want startDate", got)
	}
	// Too short a declared name to claim a prefix.
	if got := declaredSpelling("identifier", known); got != "" {
		t.Errorf("declaredSpelling(identifier) = %q, want nothing", got)
	}
}

func TestUnit_Steps_AProbeUnderARefusedGateValueIsNotSent(t *testing.T) {
	t.Parallel()
	r := &runner{}
	entity := &entityState{
		plan:                &plan.EntityPlan{Entity: "rule"},
		recipe:              &entityLifecycle{minimalBody: map[string]any{"alertType": "http-server"}},
		unreachedGateValues: map[string]map[string]bool{"alertType": {"bgp": true}},
	}
	step := &plan.Step{
		Kind: plan.StepCreatePerEnumValue, Attribute: "direction",
		Body:      map[string]any{"alertType": "bgp", "direction": "to-target"},
		Condition: &observe.Condition{Attribute: "alertType", Equals: "bgp"},
	}
	// A runner with no client would fail the moment a request was built;
	// returning cleanly proves nothing was sent.
	if err := r.runCreatePerEnumValue(context.Background(), entity, step); err != nil {
		t.Fatalf("a probe under a refused gate value was sent: %v", err)
	}
}

func TestUnit_Adjust_ABareAbsenceNamingAnObjectsMemberAddsTheMember(t *testing.T) {
	t.Parallel()
	act := classifyRefusal(&httpResult{status: 400, body: []byte(`{"title":"400 Bad Request\nrepeat type must be specified"}`)})
	if act.kind != adjustmentAdd || act.field != "type" || act.container != "repeat" {
		t.Fatalf("classifyRefusal = %+v, want an add of type under repeat", act)
	}
	r := &runner{
		hints: map[string]map[string]strategy.SyntheticValueRules{"window": {"repeat": {Field: "repeat"}, "name": {Field: "name"}}},
		syntheses: map[string]bodySynthesis{"window": {
			composites:         map[string]any{"repeat": map[string]any{}},
			attestedComposites: map[string]any{"repeat": map[string]any{"type": "week", "intervalType": "day"}},
		}},
	}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "window"}}
	body := map[string]any{"name": "n", "repeat": map[string]any{}}
	applied := map[string]bool{}
	path, ok := r.addMember(entity, body, "repeat", "type", applied)
	if !ok || path != "repeat.type" {
		t.Fatalf("addMember = %q, %v; want repeat.type set", path, ok)
	}
	if got := body["repeat"].(map[string]any)["type"]; got != "week" {
		t.Errorf("repeat.type = %#v, want the attested value", got)
	}
	if _, again := r.addMember(entity, body, "repeat", "type", applied); again {
		t.Error("a member already set was added twice")
	}
	if _, ok := r.addMember(entity, body, "Request", "duration", applied); ok {
		t.Error("a word that is not a declared field was taken for a container")
	}
	if _, ok := r.addMember(entity, body, "name", "type", applied); ok {
		t.Error("a scalar field was taken for a container")
	}
}

func TestUnit_Adjust_AnAddedCompositeTakesItsAttestedFormWhenTheMinimalIsEmpty(t *testing.T) {
	t.Parallel()
	r := &runner{
		hints: map[string]map[string]strategy.SyntheticValueRules{"window": {"endRepeat": {Field: "endRepeat"}, "repeat": {Field: "repeat"}}},
		syntheses: map[string]bodySynthesis{"window": {
			composites:         map[string]any{"endRepeat": map[string]any{}, "repeat": map[string]any{"type": "day"}},
			attestedComposites: map[string]any{"endRepeat": map[string]any{"type": "never", "count": 3}, "repeat": map[string]any{"type": "week", "intervalType": "day"}},
		}},
	}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "window"}}
	added, _ := r.synthesiseField(entity, "endRepeat").(map[string]any)
	if added["type"] != "never" || added["count"] != 3 {
		t.Errorf("an empty composite was added as %#v, want its attested form", added)
	}
	minimal, _ := r.synthesiseField(entity, "repeat").(map[string]any)
	if len(minimal) != 1 || minimal["type"] != "day" {
		t.Errorf("a composite with a minimal form was widened: %#v", minimal)
	}
	// The same rule serves a body built from the plan.
	body := r.syntheses["window"].requestBody(strategy.RequestFields{Fields: []string{"endRepeat", "repeat"}, Rules: []strategy.SyntheticValueRules{{Field: "endRepeat"}, {Field: "repeat"}}}, "", "")
	if added, _ := body["endRepeat"].(map[string]any); added["type"] != "never" {
		t.Errorf("a planned empty composite = %#v, want its attested form", body["endRepeat"])
	}
}

func TestUnit_Adjust_ATimestampTheAPIWantsAheadMovesAheadOfNow(t *testing.T) {
	t.Parallel()
	for _, message := range []string{
		`{"title":"The suppression window must start at least 2 minutes from now."}`,
		`{"detail":"startDate must be in the future"}`,
		`{"detail":"the window cannot be in the past"}`,
	} {
		act := classifyRefusal(&httpResult{status: 400, body: []byte(message)})
		if act.kind != adjustmentRevalue || act.revalue != futureValue {
			t.Errorf("%s classified as %+v, want a future revalue", message, act)
		}
	}
	body := map[string]any{"name": "n", "startDate": "2017-07-01T05:00:00Z", "endDate": "2017-07-02T05:00:00Z", "duration": 1}
	if got := timestampField(body); got != "startDate" {
		t.Errorf("timestampField = %q, want the one that starts", got)
	}
	if got := timestampField(map[string]any{"when": "2017-07-01", "n": 1}); got != "when" {
		t.Errorf("timestampField with one timestamp = %q, want it", got)
	}
	if got := timestampField(map[string]any{"a": "2017-07-01", "b": "2017-07-02"}); got != "" {
		t.Errorf("timestampField with two unnamed timestamps = %q, want none", got)
	}
	moved, ok := revalued(futureValue, "2017-07-01T05:00:00Z", strategy.SyntheticValueRules{})
	if !ok {
		t.Fatal("a timestamp was not revalued")
	}
	if stamp, ok := observe.ParseTimestamp(moved.(string)); !ok || !stamp.After(time.Now()) {
		t.Errorf("revalued = %v, want an instant ahead of now", moved)
	}
	if day, ok := revalued(futureValue, "2017-07-01", strategy.SyntheticValueRules{}); !ok || len(day.(string)) != len("2006-01-02") {
		t.Errorf("a date revalued = %v, want a date", day)
	}
	if _, ok := revalued(futureValue, "not a time", strategy.SyntheticValueRules{}); ok {
		t.Error("a string that is not a timestamp was revalued")
	}

	r := &runner{hints: map[string]map[string]strategy.SyntheticValueRules{"window": {}}}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "window"}, ev: newEvidence()}
	res := &httpResult{status: 400, body: []byte(`{"title":"must start at least 2 minutes from now"}`)}
	if _, ok := r.applyAdjustment(context.Background(), entity, body, res, map[string]bool{}, true); !ok {
		t.Fatal("the future revalue was not applied")
	}
	if !entity.ev.futureFields["startDate"] {
		t.Errorf("futureFields = %v, want startDate recorded", entity.ev.futureFields)
	}
	if got := futureDatesIn(body, entity.ev.futureFields); len(got) != 1 || got[0] != "startDate" {
		t.Errorf("futureDatesIn = %v, want startDate", got)
	}
}
