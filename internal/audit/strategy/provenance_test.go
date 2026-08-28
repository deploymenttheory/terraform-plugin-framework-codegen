package strategy_test

import (
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
)

// TestUnit_Provenance_MirrorsObserve pins the deliberate duplication. strategy
// declares its own Provenance rather than importing observe's, so the compiler
// that reads documents carries no dependency on the package that writes
// observations. The two value sets must stay identical, because an observation
// inherits the provenance of the claim it confirms.
func TestUnit_Provenance_MirrorsObserve(t *testing.T) {
	t.Parallel()
	pairs := []struct {
		compiled strategy.Provenance
		observed observe.Provenance
	}{
		{strategy.ProvenanceStructural, observe.ProvenanceStructural},
		{strategy.ProvenanceProse, observe.ProvenanceProse},
		{strategy.ProvenanceDerived, observe.ProvenanceDerived},
	}
	for _, p := range pairs {
		if string(p.compiled) != string(p.observed) {
			t.Errorf("strategy %q and observe %q have drifted apart", p.compiled, p.observed)
		}
	}
}
