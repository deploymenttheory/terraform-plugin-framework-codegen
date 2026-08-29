package providergen

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkgen"
)

// TestUnit_Providergen_EveryRewriteSaysWhyAndWhatItCost holds the account a
// reader gets of the changes made to their specification. Seeing that
// something was changed and not why, or not what it cost, is what makes a
// list of two hundred edits read as damage.
func TestUnit_Providergen_EveryRewriteSaysWhyAndWhatItCost(t *testing.T) {
	t.Parallel()
	lines := rewriteLines(sdkgen.Rewrites{})
	if len(lines) != 5 {
		t.Fatalf("got %d rewrites, want the five that run", len(lines))
	}
	for _, r := range lines {
		if r.Name == "" || r.Why == "" || r.Cost == "" {
			t.Errorf("%q does not say both why and what it cost: %+v", r.Name, r)
		}
		// The report is HTML, so markdown arrives on the page as itself.
		if strings.ContainsAny(r.Name+r.Why+r.Cost, "`*_") {
			t.Errorf("%q carries markdown that HTML will show as itself", r.Name)
		}
		if strings.ContainsRune(r.Name+r.Why+r.Cost, '—') {
			t.Errorf("%q uses an em dash", r.Name)
		}
	}
}
