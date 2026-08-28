package run

import (
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
