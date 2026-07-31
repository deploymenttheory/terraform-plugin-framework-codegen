package probe

import (
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// TestUnit_Probe_DraftPlanScaffoldsAWorksheet: the draft fills what a specification
// can know and marks every gap CURATE_ME, and the result must be a plan the subject
// itself validates -- a worksheet the loader would refuse teaches nobody anything.
func TestUnit_Probe_DraftPlanScaffoldsAWorksheet(t *testing.T) {
	t.Parallel()

	subj := Subject{
		Resource:  "test_http",
		NameField: "testName",
		Fields: []Field{
			{JSONPath: "testName", Kind: blueprint.KindString, Writable: true,
				ComputedOptionalRequired: blueprint.Required},
			{JSONPath: "url", Kind: blueprint.KindString, Writable: true,
				ComputedOptionalRequired: blueprint.Required},
			{JSONPath: "interval", Kind: blueprint.KindInt64, Writable: true,
				ComputedOptionalRequired: blueprint.Required},
			// The classic-test shape the scaffold exists for: a required nested
			// collection whose members are live identifiers nothing can invent.
			{JSONPath: "agents", Kind: blueprint.KindListNested, Writable: true,
				ComputedOptionalRequired: blueprint.Required},
			{JSONPath: "agents.agentId", Kind: blueprint.KindString, Writable: true,
				ComputedOptionalRequired: blueprint.Required},
			// Optional with documented values: not in the fixture, but its
			// alternatives fuel the enum and immutability protocols.
			{JSONPath: "protocol", Kind: blueprint.KindString, Writable: true,
				ComputedOptionalRequired: blueprint.Optional,
				AllowedValues:            []string{"TCP", "ICMP", "UDP"}},
			// Computed never reaches a request body.
			{JSONPath: "createdDate", Kind: blueprint.KindString, Writable: false,
				ComputedOptionalRequired: blueprint.Computed},
		},
	}

	plan := DraftPlan(subj)

	if err := plan.Validate(subj); err != nil {
		t.Fatalf("a draft the plan loader would refuse is a broken worksheet: %v", err)
	}

	if len(plan.Fixtures) != 1 {
		t.Fatalf("want one minimal fixture, got %d", len(plan.Fixtures))
	}
	body := plan.Fixtures[0].Body

	if body["testName"] != CurateMe {
		t.Errorf("the name field should be a conspicuous placeholder, got %v", body["testName"])
	}
	if body["url"] != CurateMe {
		t.Errorf("an underivable required string should be CURATE_ME, got %v", body["url"])
	}
	if body["interval"] != 1 {
		t.Errorf("a required integer gets a plausible value, got %v", body["interval"])
	}
	if _, ok := body["createdDate"]; ok {
		t.Error("a computed field must not reach the fixture")
	}
	if _, ok := body["protocol"]; ok {
		t.Error("an optional field is curation's call, not the scaffold's")
	}

	agents, ok := body["agents"].([]any)
	if !ok || len(agents) != 1 {
		t.Fatalf("a required nested collection should carry one placeholder member, got %v",
			body["agents"])
	}
	if member, _ := agents[0].(map[string]any); member[CurateMe] != CurateMe {
		t.Errorf("the nested member is unknowable and should say so, got %v", agents[0])
	}

	// The documented alternatives -- everything after the first value -- become
	// candidates, which is exactly what the immutability protocol needs: a second
	// value it can prove acceptable.
	got := plan.Candidates["protocol"]
	if len(got) != 2 || got[0] != "ICMP" || got[1] != "UDP" {
		t.Errorf("candidates should be the documented alternatives, got %v", got)
	}
}
