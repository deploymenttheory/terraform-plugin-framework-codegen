package run

import (
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/testapiserver"
)

func TestUnit_Evidence_NormalisedForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		sent, got  any
		want       string
		recognised bool
	}{
		{"case folded", "MiXeD", "mixed", "mixed", true},
		{"trimmed", "  padded  ", "padded", "padded", true},
		{"upper cased", "shouty", "SHOUTY", "SHOUTY", true},
		{"trimmed and folded", " Both ", "both", "both", true},
		{"sorted list", []any{"b", "a"}, []any{"a", "b"}, `["a","b"]`, true},
		{"unrelated string", "alpha", "omega", "", false},
		{"identical", "same", "same", "", false},
		{"unchanged list", []any{"a", "b"}, []any{"a", "b"}, "", false},
		{"non-string", 1, 2, "", false},
		{"string became number", "1", float64(1), "", false},
		{"timestamp respelt without zone", "2026-12-31T00:00:00Z", "2026-12-31 00:00:00", "2026-12-31 00:00:00", true},
		{"timestamp respelt with fraction", "2026-12-31T10:14:28Z", "2026-12-31T10:14:28.000Z", "2026-12-31T10:14:28.000Z", true},
		{"different instant", "2026-12-31T00:00:00Z", "2026-12-30 00:00:00", "", false},
		{"stored to the day", "2026-12-31T10:00:00Z", "2026-12-31", "2026-12-31", true},
	}
	for _, testCase := range cases {
		got, ok := normalisedForm(testCase.sent, testCase.got)
		if ok != testCase.recognised || got != testCase.want {
			t.Errorf("%s: normalisedForm(%v, %v) = %q, %v; want %q, %v",
				testCase.name, testCase.sent, testCase.got, got, ok, testCase.want, testCase.recognised)
		}
	}
}

func TestUnit_Evidence_MaskedEcho(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		sent, got any
		want      bool
	}{
		{"asterisks", "s3cret", "*****", true},
		{"bullets", "s3cret", "••••••", true},
		{"same", "*****", "*****", false},
		{"short", "s3cret", "**", false},
		{"plain change", "alpha", "omega", false},
		{"not a string", 5, "*****", false},
		{"masked object", map[string]any{"token": "abc", "type": "other-token"}, map[string]any{"token": "*****", "type": "other-token"}, false},
		{"wholly masked object", map[string]any{"token": "abc", "key": "def"}, map[string]any{"token": "*****", "key": "*****"}, true},
	}
	for _, testCase := range cases {
		if got := maskedEcho(testCase.sent, testCase.got); got != testCase.want {
			t.Errorf("%s: maskedEcho(%v, %v) = %v, want %v", testCase.name, testCase.sent, testCase.got, got, testCase.want)
		}
	}
}

func TestUnit_Evidence_SmallHelpers(t *testing.T) {
	t.Parallel()
	if !equalJSON(1, float64(1)) || equalJSON("a", "b") {
		t.Error("equalJSON does not compare via encodings")
	}
	for v, want := range map[any]bool{"s": true, true: true, float64(1): true, 1: true} {
		if scalarLike(v) != want {
			t.Errorf("scalarLike(%v) != %v", v, want)
		}
	}
	if scalarLike([]any{1}) || scalarLike(map[string]any{}) {
		t.Error("scalarLike admits composites")
	}
	if outcomeRank(observe.OutcomeConfirmed) <= outcomeRank(observe.OutcomeInconclusive) ||
		outcomeRank(observe.OutcomeInconclusive) <= outcomeRank(observe.OutcomeBlocked) ||
		outcomeRank(observe.OutcomeBlocked) <= outcomeRank(observe.OutcomeTimeoutExhausted) {
		t.Error("outcomeRank does not order confirmed > inconclusive > blocked > timeoutExhausted")
	}
}

// evidenceRunner is a runner wired for offline evidence tests: record()
// needs the authenticator and the summary maps, nothing else.
func evidenceRunner(t *testing.T) *runner {
	t.Helper()
	s := testapiserver.New(t, testapiserver.Quirks{})
	r, err := newRunner(testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestUnit_Evidence_ServerForcedAndPresentNull(t *testing.T) {
	t.Parallel()
	r := evidenceRunner(t)
	entity := &entityState{plan: &r.opts.Plan.Entities[0], ev: newEvidence()}
	entity.ev.sent = map[string]any{"forced": "sent-value", "nulled": "sent-too"}
	entity.ev.got = map[string]any{"forced": "server-picked", "nulled": nil}

	r.finalizeEvidence(entity)
	obs := r.finishObservations()

	if o := wantConfirmed(t, obs, "thing", "forced", observe.KindServerForced); o.Value != true {
		t.Errorf("serverForced = %v", o.Value)
	}
	null := findObs(obs, "thing", "nulled", observe.KindWritable)
	if null == nil || null.Outcome != observe.OutcomeInconclusive {
		t.Errorf("present-and-null = %+v, want an inconclusive writable", null)
	}
}

func TestUnit_Evidence_DerivedDefaultVetoesTheConstant(t *testing.T) {
	t.Parallel()
	r := evidenceRunner(t)
	entity := &entityState{plan: &r.opts.Plan.Entities[0], ev: newEvidence()}
	entity.ev.sent = map[string]any{"name": "x"}
	entity.ev.got = map[string]any{"name": "x", "counter": "counter-1", "structured": map[string]any{"a": 1}}
	entity.ev.omitted["counter"] = []any{"counter-1", "counter-2"}

	r.finalizeEvidence(entity)
	obs := r.finishObservations()

	if o := wantConfirmed(t, obs, "thing", "counter", observe.KindDerivedDefault); o.Value != true {
		t.Errorf("derivedDefault = %v", o.Value)
	}
	if findObs(obs, "thing", "counter", observe.KindServerDefault) != nil {
		t.Error("the derived default was also written down as constant")
	}
	if findObs(obs, "thing", "structured", observe.KindServerDefault) != nil {
		t.Error("a composite value cannot be a serverDefault")
	}
}

func TestUnit_Evidence_UpdateStyleReduction(t *testing.T) {
	t.Parallel()
	r := evidenceRunner(t)

	ev := newEvidence()
	ev.updRefused = 3
	r.finalizeUpdateStyle("gadget", ev)

	ev2 := newEvidence()
	ev2.updSucceeded = 2
	r.finalizeUpdateStyle("thing", ev2)

	obs := r.finishObservations()
	if o := wantConfirmed(t, obs, "gadget", "", observe.KindUpdateStyle); o.Value != "replace-only" {
		t.Errorf("all-refused = %v, want replace-only", o.Value)
	}
	inconclusive := findObs(obs, "thing", "", observe.KindUpdateStyle)
	if inconclusive == nil || inconclusive.Outcome != observe.OutcomeInconclusive {
		t.Errorf("no omitted-field evidence = %+v, want inconclusive", inconclusive)
	}
}

func TestUnit_Evidence_StepClaims(t *testing.T) {
	t.Parallel()
	cond := &observe.Condition{Attribute: "mode", Equals: "advanced"}
	cases := []struct {
		step      plan.Step
		kind      observe.Kind
		attribute string
	}{
		{plan.Step{Kind: plan.StepReadWithRetry}, observe.KindReadAfterWrite, ""},
		{plan.Step{Kind: plan.StepUpdateField, Attribute: "a"}, observe.KindImmutable, "a"},
		{plan.Step{Kind: plan.StepDeleteWithConfirmation}, observe.KindDeleteNotFoundOK, ""},
		{plan.Step{Kind: plan.StepOmitRequired, Attribute: "a"}, observe.KindRequiredByAPI, "a"},
		{plan.Step{Kind: plan.StepUndocumentedEnumValue, Attribute: "a"}, observe.KindValues, "a"},
		{plan.Step{Kind: plan.StepCreatePerEnumValue, Attribute: "q", Condition: cond}, observe.KindRequiredWhen, "q"},
		{plan.Step{Kind: plan.StepCreatePerEnumValue, Attribute: "mode", Condition: cond}, observe.KindValues, "mode"},
	}
	for _, testCase := range cases {
		claims := stepPendingObservations(&testCase.step)
		if len(claims) != 1 || claims[0].kind != testCase.kind || claims[0].attribute != testCase.attribute {
			t.Errorf("stepClaims(%s %s) = %+v, want one %s claim on %q", testCase.step.Kind, testCase.step.Attribute, claims, testCase.kind, testCase.attribute)
		}
	}
	for _, silent := range []plan.StepKind{plan.StepCreateMinimal, plan.StepCreateMaximal, plan.StepRead, plan.StepReadConsecutive, plan.StepCleanupDelete, plan.StepUndeclaredSpecField} {
		if got := stepPendingObservations(&plan.Step{Kind: silent}); got != nil {
			t.Errorf("stepClaims(%s) = %+v, want none", silent, got)
		}
	}
}

func TestUnit_Run_NewRunIDShape(t *testing.T) {
	t.Parallel()
	id := newRunID()
	if len(id) != 8 || strings.ToLower(id) != id {
		t.Fatalf("newRunID() = %q, want 8 lowercase characters", id)
	}
	if id == newRunID() {
		t.Fatal("two run ids collided")
	}
}

func TestUnit_Evidence_ARefusalIsNotADeclinedRequest(t *testing.T) {
	t.Parallel()
	for status, want := range map[int]bool{400: true, 404: true, 409: true, 422: true, 429: false, 408: false, 500: false, 201: false} {
		if got := (&httpResult{status: status}).refused(); got != want {
			t.Errorf("refused(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestUnit_Evidence_ACompositeAnsweredDifferentlyIsNotServerForced(t *testing.T) {
	t.Parallel()
	r := evidenceRunner(t)
	entity := &entityState{plan: &r.opts.Plan.Entities[0], ev: newEvidence()}
	entity.ev.sent = map[string]any{
		"headers": []any{map[string]any{"name": "a", "value": "secret"}},
		"target":  "www.example.invalid",
	}
	entity.ev.got = map[string]any{
		"headers": []any{map[string]any{"name": "a", "value": "*****"}},
		"target":  "https://www.example.invalid/",
	}

	r.finalizeEvidence(entity)
	obs := r.finishObservations()

	forced := findObs(obs, "thing", "headers", observe.KindServerForced)
	if forced == nil || forced.Outcome != observe.OutcomeInconclusive {
		t.Errorf("a list answered with members changed = %+v, want an inconclusive serverForced", forced)
	}
	// The member answered masked is one the answer never carries as sent,
	// recorded on its own dotted path; the member answered as sent is not.
	if o := wantConfirmed(t, obs, "thing", "headers.value", observe.KindWritable); o.Value != false {
		t.Errorf("writable(headers.value) = %v, want false", o.Value)
	}
	if findObs(obs, "thing", "headers.name", observe.KindWritable) != nil {
		t.Error("a member answered as sent was recorded")
	}
	// A host answered inside a longer spelling is the API's own form of the
	// value sent, not another value.
	if o := wantConfirmed(t, obs, "thing", "target", observe.KindNormalisation); o.Value != "https://www.example.invalid/" {
		t.Errorf("normalisation = %v", o.Value)
	}
}

func TestUnit_Steps_AProbeBodyIsRebasedOnTheAcceptedMinimal(t *testing.T) {
	t.Parallel()
	r := &runner{}
	entity := &entityState{
		plannedMinimal: map[string]any{"name": "n", "filters": []any{map[string]any{}}, "kind": "a"},
		recipe: &entityLifecycle{minimalBody: map[string]any{
			"name": "n", "filters": []any{map[string]any{"key": "network"}}, "kind": "a", "type": "webhook",
		}},
	}
	// A per-value probe pins one field: the accepted body carries the
	// pinned value, the widened element and the field the loop added.
	got := r.rebased(entity, map[string]any{"name": "n", "filters": []any{map[string]any{}}, "kind": "b"})
	want := map[string]any{"name": "n", "filters": []any{map[string]any{"key": "network"}}, "kind": "b", "type": "webhook"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rebased = %#v, want %#v", got, want)
	}
	// An omission probe leaves out the field it omits, and an addition
	// probe carries what it adds.
	got = r.rebased(entity, map[string]any{"filters": []any{map[string]any{}}, "kind": "a", "extra": true})
	want = map[string]any{"filters": []any{map[string]any{"key": "network"}}, "kind": "a", "type": "webhook", "extra": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rebased omission = %#v, want %#v", got, want)
	}
	// Before a minimal was accepted there is nothing to rebase onto.
	bare := &entityState{recipe: &entityLifecycle{}}
	if got := r.rebased(bare, map[string]any{"a": 1}); !reflect.DeepEqual(got, map[string]any{"a": 1}) {
		t.Errorf("rebased without an accepted body = %#v", got)
	}
}

func TestUnit_Evidence_ARefusalMentionsAFieldSpeltInWords(t *testing.T) {
	t.Parallel()
	res := &httpResult{body: []byte(`{"title":"Cloud Agents are not supported for bandwidth measurements."}`)}
	if !res.mentions("bandwidthMeasurements") {
		t.Error("a field spelt in words was not recognised")
	}
	if res.mentions("mtuMeasurements") || res.mentions("") {
		t.Error("a field the refusal does not name was recognised")
	}
}

func TestUnit_Evidence_RejectedValueSkipsUnresolvedReference(t *testing.T) {
	t.Parallel()
	r := &runner{}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "group"}, ev: newEvidence()}
	res := &httpResult{status: 400, body: []byte(`{"message":"agents invalid"}`)}
	r.recordRejectedValue(entity, "agents", []any{BorrowToken + "/agents"}, res)
	r.recordRejectedValue(entity, "owner", "$created:user", res)
	r.recordRejectedValue(entity, "agents", []any{"7"}, res)
	got := entity.ev.valuesFor("agents").Rejected
	if !reflect.DeepEqual(got, []string{"[7]"}) {
		t.Fatalf("rejected = %v, want only the resolved value", got)
	}
	if v := entity.ev.valuesFor("owner").Rejected; len(v) != 0 {
		t.Fatalf("a created token was recorded as rejected: %v", v)
	}
}
