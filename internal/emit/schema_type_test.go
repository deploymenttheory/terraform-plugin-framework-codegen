package emit

import (
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// listNode and scalarNode build the two shapes schemaTypeOf branches on
// without dragging a whole entity in.
func scalarNode(kind ir.AttributeType) node {
	return node{attribute: ir.Attribute{Name: "x", WireName: "x", Type: kind}}
}

func listNode(element ir.AttributeType) node {
	return node{attribute: ir.Attribute{Name: "x", WireName: "x", Type: ir.TypeList, ElementType: element}}
}

func nestedNode(kind ir.AttributeType) node {
	return node{attribute: ir.Attribute{Name: "x", WireName: "x", Type: kind, NestedAttributes: &ir.AttributeTree{}}}
}

// TestUnit_SchemaTypeOf_EverySpelling pins the whole table. Generated code
// depends on every one of these names, so a change to any of them is a change
// to generated code and must be deliberate.
func TestUnit_SchemaTypeOf_EverySpelling(t *testing.T) {
	for _, testCase := range []struct {
		name                string
		node                node
		schemaAttribute     string
		valueType           string
		elementType         string
		planModifier        string
		planModifierPackage string
		validatorPackage    string
	}{
		{"string", scalarNode(ir.TypeString), "StringAttribute", "types.String", "", "String", "stringplanmodifier", "stringvalidator"},
		{"bool", scalarNode(ir.TypeBool), "BoolAttribute", "types.Bool", "", "Bool", "boolplanmodifier", "boolvalidator"},
		{"int64", scalarNode(ir.TypeInt64), "Int64Attribute", "types.Int64", "", "Int64", "int64planmodifier", "int64validator"},
		{"float64", scalarNode(ir.TypeFloat64), "Float64Attribute", "types.Float64", "", "Float64", "float64planmodifier", "float64validator"},
		{"list of strings", listNode(ir.TypeString), "ListAttribute", "types.List", "types.StringType", "List", "listplanmodifier", "listvalidator"},
		{"list of bools", listNode(ir.TypeBool), "ListAttribute", "types.List", "types.BoolType", "List", "listplanmodifier", "listvalidator"},
		{"list of int64s", listNode(ir.TypeInt64), "ListAttribute", "types.List", "types.Int64Type", "List", "listplanmodifier", "listvalidator"},
		{"list of float64s", listNode(ir.TypeFloat64), "ListAttribute", "types.List", "types.Float64Type", "List", "listplanmodifier", "listvalidator"},
		{"nested object", nestedNode(ir.TypeObject), "SingleNestedAttribute", "", "", "Object", "objectplanmodifier", "objectvalidator"},
		{"nested list", nestedNode(ir.TypeList), "ListNestedAttribute", "", "", "List", "listplanmodifier", "listvalidator"},
	} {
		resolved := schemaTypeOf(testCase.node)
		if resolved.SchemaAttribute != testCase.schemaAttribute {
			t.Errorf("%s: SchemaAttribute = %q, want %q", testCase.name, resolved.SchemaAttribute, testCase.schemaAttribute)
		}
		if resolved.ValueType != testCase.valueType {
			t.Errorf("%s: ValueType = %q, want %q", testCase.name, resolved.ValueType, testCase.valueType)
		}
		if resolved.ElementType != testCase.elementType {
			t.Errorf("%s: ElementType = %q, want %q", testCase.name, resolved.ElementType, testCase.elementType)
		}
		if resolved.PlanModifier != testCase.planModifier {
			t.Errorf("%s: PlanModifier = %q, want %q", testCase.name, resolved.PlanModifier, testCase.planModifier)
		}
		if got := resolved.PlanModifierPackage(); got != testCase.planModifierPackage {
			t.Errorf("%s: PlanModifierPackage() = %q, want %q", testCase.name, got, testCase.planModifierPackage)
		}
		if got := resolved.ValidatorPackage(); got != testCase.validatorPackage {
			t.Errorf("%s: ValidatorPackage() = %q, want %q", testCase.name, got, testCase.validatorPackage)
		}
	}
}

// TestUnit_SchemaTypeOf_NestingOutranksKind pins the precedence the old
// switches encoded in their case order: a nested attribute is a nested
// attribute whatever its kind says, and only an unnested list is a plain
// ListAttribute.
func TestUnit_SchemaTypeOf_NestingOutranksKind(t *testing.T) {
	nestedList := nestedNode(ir.TypeList)
	if got := schemaTypeOf(nestedList).SchemaAttribute; got != "ListNestedAttribute" {
		t.Errorf("a nested list is %q, want ListNestedAttribute", got)
	}

	// A nested attribute of any other kind is a single nested object.
	for _, kind := range []ir.AttributeType{ir.TypeObject, ir.TypeString, ir.TypeBool} {
		if got := schemaTypeOf(nestedNode(kind)).SchemaAttribute; got != "SingleNestedAttribute" {
			t.Errorf("a nested %s is %q, want SingleNestedAttribute", kind, got)
		}
	}
}

// TestUnit_SchemaTypeOf_UnknownKindFallsBackToString pins the behaviour the
// switches had in their default arm: an attribute that reaches emission
// with no kind renders as a string rather than as nothing at all.
func TestUnit_SchemaTypeOf_UnknownKindFallsBackToString(t *testing.T) {
	resolved := schemaTypeOf(scalarNode(ir.AttributeType("")))
	if resolved.SchemaAttribute != "StringAttribute" || resolved.ValueType != "types.String" {
		t.Errorf("an unknown kind resolved to %q/%q, want StringAttribute/types.String",
			resolved.SchemaAttribute, resolved.ValueType)
	}
	if got := frameworkElementType(ir.AttributeType("")); got != "types.StringType" {
		t.Errorf("an unknown element kind resolved to %q, want types.StringType", got)
	}
	if got := scalarSchemaType(ir.AttributeType("")).ValueType; got != "types.String" {
		t.Errorf("scalarSchemaType of an unknown kind = %q, want types.String", got)
	}
}

// TestUnit_SchemaType_ImportPathsAreWellFormed pins that the per-type
// imports are real framework package paths, not just a package name
// concatenated onto the wrong root.
func TestUnit_SchemaType_ImportPathsAreWellFormed(t *testing.T) {
	resolved := schemaTypeOf(scalarNode(ir.TypeInt64))

	wantPlanModifier := "github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	if resolved.PlanModifierImport.Path != wantPlanModifier {
		t.Errorf("PlanModifierImport.Path = %q, want %q", resolved.PlanModifierImport.Path, wantPlanModifier)
	}

	wantValidator := "github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	if resolved.ValidatorImport.Path != wantValidator {
		t.Errorf("ValidatorImport.Path = %q, want %q", resolved.ValidatorImport.Path, wantValidator)
	}

	if resolved.PlanModifierImport.HasAlias() || resolved.ValidatorImport.HasAlias() {
		t.Error("the per-type imports are imported under their own names, never aliased")
	}
}

// TestUnit_SchemaTypeOf_Map proves a map attribute renders as a
// MapAttribute with its element type, and picks up the map plan-modifier
// and validator packages from the same base word as every other type.
func TestUnit_SchemaTypeOf_Map(t *testing.T) {
	n := node{attribute: ir.Attribute{Name: "x", WireName: "x", Type: ir.TypeMap, ElementType: ir.TypeString}}
	resolved := schemaTypeOf(n)

	for _, check := range []struct{ got, want, what string }{
		{resolved.SchemaAttribute, "MapAttribute", "SchemaAttribute"},
		{resolved.ValueType, "types.Map", "ValueType"},
		{resolved.ElementType, "types.StringType", "ElementType"},
		{resolved.PlanModifier, "Map", "PlanModifier"},
		{resolved.PlanModifierPackage(), "mapplanmodifier", "PlanModifierPackage"},
		{resolved.ValidatorPackage(), "mapvalidator", "ValidatorPackage"},
	} {
		if check.got != check.want {
			t.Errorf("%s = %q, want %q", check.what, check.got, check.want)
		}
	}
}
