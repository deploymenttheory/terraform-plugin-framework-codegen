package plan

import (
	"strings"
	"testing"
)

func threeEntityPlan() *Plan {
	return &Plan{Entities: []EntityPlan{
		{Entity: "account"},
		{Entity: "thing", Parents: []string{"account"}},
		{Entity: "tag"},
	}}
}

func TestUnit_Plan_OnlyEntitiesKeepsTheNamedOnesAndTheirParents(t *testing.T) {
	p := threeEntityPlan()
	if err := p.OnlyEntities([]string{"thing"}); err != nil {
		t.Fatal(err)
	}
	if len(p.Entities) != 2 || p.Entities[0].Entity != "account" || p.Entities[1].Entity != "thing" {
		t.Fatalf("kept %v, want account then thing", p.Entities)
	}
	if len(p.Skipped) != 1 || p.Skipped[0].Entity != "tag" || !strings.Contains(p.Skipped[0].Reason, "--entity") {
		t.Fatalf("skipped %v, want tag with the flag named", p.Skipped)
	}
}

func TestUnit_Plan_OnlyEntitiesRefusesAnUnknownNameWithASuggestion(t *testing.T) {
	p := threeEntityPlan()
	err := p.OnlyEntities([]string{"tags"})
	if err == nil {
		t.Fatal("an unknown entity was accepted")
	}
	if !strings.Contains(err.Error(), `"tags"`) || !strings.Contains(err.Error(), `did you mean "tag"`) {
		t.Fatalf("error = %v, want the name and a suggestion", err)
	}
	if len(p.Entities) != 3 {
		t.Fatalf("a refused narrowing changed the plan: %v", p.Entities)
	}
}

func TestUnit_Plan_OnlyEntitiesWithNoNamesLeavesThePlanAlone(t *testing.T) {
	p := threeEntityPlan()
	if err := p.OnlyEntities(nil); err != nil {
		t.Fatal(err)
	}
	if len(p.Entities) != 3 || len(p.Skipped) != 0 {
		t.Fatalf("plan changed: %v / %v", p.Entities, p.Skipped)
	}
}

func TestUnit_Plan_DurationFollowsTheRequestsAtTheRate(t *testing.T) {
	if got := DurationFor(8000, 2); got != "2h13m20s" {
		t.Errorf("DurationFor(8000, 2) = %s, want twice the time 8000 requests take at 2/s", got)
	}
	if got := DurationFor(10, 2); got != "1m0s" {
		t.Errorf("DurationFor(10, 2) = %s, want the one-minute floor", got)
	}
	if got := DurationFor(120, 0); got != "2m0s" {
		t.Errorf("DurationFor(120, 0) = %s, want the default rate of 2", got)
	}
}
