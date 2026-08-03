package main

import (
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
)

// TestUnit_CLI_RehearsalBodiesMatchWhatTheFixturesWouldApply derives the wire bodies
// for a committed pilot resource and checks the properties the whole design rests on:
// the minimal body is applyable (required + API-enforced fields, with real values from
// hints and the plan), and the maximal body carries the optional surface.
func TestUnit_CLI_RehearsalBodiesMatchWhatTheFixturesWouldApply(t *testing.T) {
	t.Parallel()

	bp, err := blueprint.LoadDir(blueprintDir())
	if err != nil {
		t.Fatalf("loading the committed blueprint: %v", err)
	}

	plan, err := loadPlan(blueprintDir() + "/tests_http_server.probe.plan.json")
	if err != nil {
		t.Fatalf("loading the committed plan: %v", err)
	}

	round, err := rehearsalBodies(bp, "tests_http_server", plan, nil)
	if err != nil {
		t.Fatalf("rehearsalBodies: %v", err)
	}

	// The fields the live API refuses a create without, whose values only curation
	// knows: a curated hint (interval 3600, a real URL) or the plan's fixture (the
	// cloud agent reference). Synthesised placeholders here are exactly what made
	// the first live acceptance run fail.
	if v, ok := round.Minimal["interval"]; !ok || v == nil {
		t.Errorf("minimal must carry the API-enforced interval, got %v", round.Minimal)
	}
	if v, ok := round.Minimal["url"].(string); !ok || v == "" || v[0:4] != "http" {
		t.Errorf("minimal must carry a real URL from the hint, got %v", round.Minimal["url"])
	}
	if _, ok := round.Minimal["agents"]; !ok {
		t.Errorf("minimal must carry the agents reference from the plan fixture, got %v",
			round.Minimal)
	}

	// The maximal body includes the minimal one and exercises more.
	for k := range round.Minimal {
		if _, ok := round.Maximal[k]; !ok {
			t.Errorf("maximal should include minimal's %q", k)
		}
	}
	if len(round.Maximal) <= len(round.Minimal) {
		t.Errorf("maximal (%d keys) should exercise more than minimal (%d)",
			len(round.Maximal), len(round.Minimal))
	}

	// Facts change the derivation: a corroborated forced value pins the fixture...
	// which is the fixpoint the record loop iterates on. Proven here at one remove:
	// folding a fact in must not error and must keep the bodies derivable.
	forced := probe.Fact{
		Resource: "tests_http_server", JSONPath: "networkMeasurements",
		Field: probe.FactServerForced,
		Value: probe.LiteralValue(blueprint.Literal{
			Kind: blueprint.KindBool, Raw: "true",
		}),
		Confidence: probe.Corroborated, Probe: "write.rehearsal",
		Evidence: []string{"005-put-tests"}, Rationale: "test",
	}
	if _, err := rehearsalBodies(bp, "tests_http_server", plan, []probe.Fact{forced}); err != nil {
		t.Fatalf("deriving with facts folded in: %v", err)
	}
}
