package intermediate_representation

import (
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// TestUnit_DeriveMapType_AMapOfObjectsCarriesItsNestedTree holds the shape a
// map of objects derives to. terraform-plugin-framework has
// MapNestedAttribute for exactly this, and a specification that keys objects
// by a name the practitioner chooses has no other shape to become; refusing
// it left the attribute out of the provider altogether.
func TestUnit_DeriveMapType_AMapOfObjectsCarriesItsNestedTree(t *testing.T) {
	t.Parallel()
	value := &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "colour", Schema: &specmodel.Schema{Type: "string"}},
		{Name: "size", Schema: &specmodel.Schema{Type: "integer"}},
	}}
	tree := buildTree(&specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "labels", Schema: &specmodel.Schema{Type: "object", AdditionalProperties: value}},
	}}, nil, nil, false)

	got := attribute(t, tree, "labels")
	if got.Unsupported {
		t.Fatalf("refused with %q", got.UnsupportedReason)
	}
	if got.Kind != TypeMap || got.ElementType != TypeObject {
		t.Fatalf("kind/element = %q/%q, want a map of objects", got.Kind, got.ElementType)
	}
	if got.Nested == nil {
		t.Fatal("the map carries no nested tree, so nothing describes one value")
	}
	if len(got.Nested.Attributes) != 2 {
		t.Errorf("the nested tree carries %d attributes, want the value schema's two", len(got.Nested.Attributes))
	}
	for _, want := range []string{"colour", "size"} {
		attribute(t, got.Nested, want)
	}
}
