package fixtures

import (
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

func TestUnit_HCL_AReplayedMapCarriesTheKeysTheAPITook(t *testing.T) {
	entry := Entry{
		Name: "custom_headers", Wire: "customHeaders", Kind: ir.TypeMap,
		ComputedOptionalRequired: ir.Optional,
		Scalar:                   map[string]any{"Content-Type": "application/json", "Authorization": "token"},
	}

	got := scalarHCL(entry)

	// The keys are the API's, in key order so a regenerated fixture is
	// byte-identical.
	want := `{ "Authorization" = "token", "Content-Type" = "application/json" }`
	if got != want {
		t.Errorf("map = %s, want %s", got, want)
	}
	// A derived fixture has no keys to take, and falls back to one entry
	// named for the attribute itself.
	derived := Entry{Name: "labels", Kind: ir.TypeMap, Scalar: "tfpfgen-test-labels"}
	if got := scalarHCL(derived); got != `{ "labels" = "tfpfgen-test-labels" }` {
		t.Errorf("derived map = %s, want the attribute-named entry", got)
	}
}
