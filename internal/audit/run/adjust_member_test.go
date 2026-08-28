package run

import (
	"context"
	"testing"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
)

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
