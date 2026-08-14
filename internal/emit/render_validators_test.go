package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// TestUnit_ConflictingExpr_DropsPrunedAttributes proves a mutually-exclusive
// group survives an attribute the binder removed: a validator cannot address
// what is not in the schema, and refusing the whole entity over it would
// lose every other attribute too.
func TestUnit_ConflictingExpr_DropsPrunedAttributes(t *testing.T) {
	byName := map[string]node{
		"alpha": {attr: ir.Attribute{Name: "alpha"}},
		"beta":  {attr: ir.Attribute{Name: "beta"}},
	}

	expr := conflictingExpr(byName, []string{"alpha", "gone", "beta"})
	for _, want := range []string{`path.MatchRoot("alpha")`, `path.MatchRoot("beta")`} {
		if !strings.Contains(expr, want) {
			t.Errorf("expression %q does not carry %s", expr, want)
		}
	}
	if strings.Contains(expr, "gone") {
		t.Errorf("expression %q addresses a pruned attribute", expr)
	}

	// One survivor constrains nothing, so no validator is emitted at all.
	empty := conflictingExpr(byName, []string{"alpha", "gone"})
	if empty != "" {
		t.Errorf("a group with one survivor produced %q, want no validator", empty)
	}
}
