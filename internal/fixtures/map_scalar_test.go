package fixtures

import (
	"encoding/json"
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// mapScalarTree is one map attribute of each scalar element kind. A map
// carries its value type the way a list carries its element type, so the
// derived value has to be of that type and not of the map's own.
func mapScalarTree() *ir.AttributeTree {
	return &ir.AttributeTree{
		Attributes: []ir.Attribute{
			{Name: "labels", WireName: "labels", Type: ir.TypeMap, ElementType: ir.TypeString, ComputedOptionalRequired: ir.Optional},
			{Name: "limits", WireName: "limits", Type: ir.TypeMap, ElementType: ir.TypeInt64, ComputedOptionalRequired: ir.Optional},
			{Name: "weights", WireName: "weights", Type: ir.TypeMap, ElementType: ir.TypeFloat64, ComputedOptionalRequired: ir.Optional},
			{Name: "flags", WireName: "flags", Type: ir.TypeMap, ElementType: ir.TypeBool, ComputedOptionalRequired: ir.Optional},
		},
	}
}

func TestUnit_Fixtures_AMapValueTakesTheElementKindNotTheMapKind(t *testing.T) {
	s := Derive(mapScalarTree())

	for _, c := range []struct {
		name string
		want any
	}{
		{"labels", NamePrefix + "labels"},
		{"limits", int64(7)},
		{"weights", 1.5},
		{"flags", true},
	} {
		if got := valueByName(t, s, c.name).Scalar; got != c.want {
			t.Errorf("%s = %#v, want %#v: a map of %s must synthesise a %[4]s",
				c.name, got, c.want, valueByName(t, s, c.name).ElementType)
		}
	}
}

func TestUnit_Fixtures_AMapOfNonStringsRendersItsOwnTypeInHCL(t *testing.T) {
	got := Derive(mapScalarTree()).HCL(ConfigMaximal)
	// Unquoted, because terraform rejects a quoted number or boolean
	// against an Int64Type, Float64Type or BoolType element.
	for _, want := range []string{
		`labels  = { "labels" = "tfpfgen-test-labels" }`,
		`limits  = { "limits" = 7 }`,
		`weights = { "weights" = 1.5 }`,
		`flags   = { "flags" = true }`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("HCL is missing %s\ngot:\n%s", want, got)
		}
	}
}

func TestUnit_Fixtures_AMapOfNonStringsRendersItsOwnTypeOnTheWire(t *testing.T) {
	body := Derive(mapScalarTree()).WireJSON(ResponseMaximal)

	var parsed map[string]map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("the wire rendering is not JSON: %v\n%s", err, body)
	}
	// encoding/json decodes every number as float64, so a number that
	// arrived as a JSON string would read back as a string here.
	for _, c := range []struct {
		attribute string
		want      any
	}{
		{"labels", NamePrefix + "labels"},
		{"limits", float64(7)},
		{"weights", 1.5},
		{"flags", true},
	} {
		if got := parsed[c.attribute][c.attribute]; got != c.want {
			t.Errorf("wire %s = %#v, want %#v", c.attribute, got, c.want)
		}
	}
}
