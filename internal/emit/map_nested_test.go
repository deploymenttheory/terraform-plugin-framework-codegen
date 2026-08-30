package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// portsNode is one map of objects, bound the way a generated client carries
// one: the value type is the map's, not the field's.
func portsNode() node {
	child := node{
		attribute: ir.Attribute{Name: "bore", WireName: "bore", Type: ir.TypeInt64},
		fb: &sdkbind.FieldBinding{Attr: "bore", Wire: "bore",
			Access: sdkbind.FieldAccess{Get: "GetBore", Set: "SetBore",
				SDKType: "*int64", ConvertGet: "FromPtrInt64", ConvertSet: "ToPtrInt64"}},
	}
	return node{
		attribute: ir.Attribute{Name: "ports", WireName: "ports", Type: ir.TypeMap,
			ElementType: ir.TypeObject, NestedAttributes: &ir.AttributeTree{}},
		fb: &sdkbind.FieldBinding{Attr: "ports", Wire: "ports",
			NestedModel: "models.Portable", NestedWriteModel: "models.Port",
			NestedConstructor: "models.NewPort()",
			Access: sdkbind.FieldAccess{Get: "GetPorts", Set: "SetPorts",
				SDKType: "map[string]models.Portable", SDKWriteType: "map[string]models.Port"}},
		children: []node{child},
	}
}

// TestUnit_ConstructNested_BuildsAMapKeyedByItsOwnKeys holds the write side
// of a map of objects. The value type has to come from the map, not from the
// field, and the entry has to be copied to a local: a map value is not
// addressable in Go, so a value-typed entry cannot take a pointer method
// where a slice element could.
func TestUnit_ConstructNested_BuildsAMapKeyedByItsOwnKeys(t *testing.T) {
	t.Parallel()
	n := portsNode()

	lines, _, err := constructNested(newModelNamer("Entity", []node{n}), "ports", n, "data", "body", "ports", 1)
	if err != nil {
		t.Fatalf("constructNested: %v", err)
	}
	for _, want := range []string{
		"make(map[string]models.Port",
		"for key := range",
		"entry := ",
		"models.NewPort()",
		"SetPorts(",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("the write does not carry %q:\n%s", want, lines)
		}
	}
	if strings.Contains(lines, "make(map[string]models.Portable") {
		t.Errorf("the map is typed for the getter, which does not compile:\n%s", lines)
	}
}

// TestUnit_StateNested_ReadsAMapIntoAKeyedValue holds the read side. A map
// reads back through types.MapValueFrom and answers types.MapNull where the
// client carried nothing, the way a list does with its own two.
func TestUnit_StateNested_ReadsAMapIntoAKeyedValue(t *testing.T) {
	t.Parallel()
	n := portsNode()

	lines, err := stateNested(newModelNamer("Entity", []node{n}), "ports", n, "object", "state", 1)
	if err != nil {
		t.Fatalf("stateNested: %v", err)
	}
	for _, want := range []string{
		"GetPorts()",
		"make(map[string]",
		"for key := range",
		"entry := ",
		"types.MapValueFrom(ctx,",
		"types.MapNull(",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("the read does not carry %q:\n%s", want, lines)
		}
	}
}

// TestUnit_SchemaTypeOf_AMapOfObjectsIsAMapNestedAttribute pins the
// framework attribute a map of objects becomes. terraform-plugin-framework
// has one for exactly this shape, and rendering it as a single nested object
// would describe one object where the API carries many.
func TestUnit_SchemaTypeOf_AMapOfObjectsIsAMapNestedAttribute(t *testing.T) {
	t.Parallel()
	resolved := schemaTypeOf(node{attribute: ir.Attribute{
		Type: ir.TypeMap, ElementType: ir.TypeObject, NestedAttributes: &ir.AttributeTree{},
	}})
	if resolved.SchemaAttribute != "MapNestedAttribute" {
		t.Errorf("schema attribute = %q, want MapNestedAttribute", resolved.SchemaAttribute)
	}
	if resolved.Validator != "Map" || resolved.PlanModifier != "Map" {
		t.Errorf("validator/plan modifier = %q/%q, want Map for both", resolved.Validator, resolved.PlanModifier)
	}
}

// TestUnit_FieldType_AMapOfObjectsIsHeldAsAMap keeps the model field able to
// carry unknown, which a Go map of structs cannot.
func TestUnit_FieldType_AMapOfObjectsIsHeldAsAMap(t *testing.T) {
	t.Parallel()
	n := node{attribute: ir.Attribute{Type: ir.TypeMap, ElementType: ir.TypeObject, NestedAttributes: &ir.AttributeTree{}}}
	if got := fieldType(n); got != "types.Map" {
		t.Errorf("field type = %q, want types.Map", got)
	}
}
