package fixtures

import (
	"encoding/json"
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// portsTree is one map of objects: the shape a specification declares when
// it keys objects by a name the practitioner chooses.
func portsTree() *ir.AttributeTree {
	return &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "ports", WireName: "ports", Kind: ir.TypeMap, ElementType: ir.TypeObject,
			ComputedOptionalRequired: ir.Optional,
			Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
				{Name: "bore", WireName: "bore", Kind: ir.TypeInt64, ComputedOptionalRequired: ir.Required},
				{Name: "sealed", WireName: "sealed", Kind: ir.TypeBool, ComputedOptionalRequired: ir.Optional},
			}}},
	}}
}

// TestUnit_Fixturespec_AMapOfObjectsRendersOneKeyedEntry holds the shape of
// the fixture a map of objects gets. The keys belong to the practitioner and
// the specification names none, so one entry keyed by the attribute's own
// name is the only key that can be derived, the same answer a map of scalars
// already takes.
func TestUnit_Fixturespec_AMapOfObjectsRendersOneKeyedEntry(t *testing.T) {
	t.Parallel()
	got := Derive(portsTree())

	entry := valueByName(t, got, "ports")
	if entry.Nested == nil {
		t.Fatal("the map carries no nested values, so nothing describes one entry")
	}

	hcl := got.HCL(ConfigMaximal)
	for _, want := range []string{"ports = {", `"ports" = {`, "bore", "sealed"} {
		if !strings.Contains(hcl, want) {
			t.Errorf("the HCL does not carry %q:\n%s", want, hcl)
		}
	}

	body := string(got.WireJSON(ResponseMaximal))
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("the wire fixture is not valid JSON: %v\n%s", err, body)
	}
	ports, ok := decoded["ports"].(map[string]any)
	if !ok {
		t.Fatalf("ports is not an object on the wire: %s", body)
	}
	if len(ports) != 1 {
		t.Fatalf("ports carries %d entries, want the one derived key: %s", len(ports), body)
	}
	for _, value := range ports {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("the map's value is not an object: %s", body)
		}
		if _, ok := object["bore"]; !ok {
			t.Errorf("the map's value carries no bore: %s", body)
		}
	}
}
