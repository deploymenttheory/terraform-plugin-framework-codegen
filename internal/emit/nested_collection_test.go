package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// nestedCollectionNode is a node for a collection of collections with a
// scalar at the bottom, bound as the given carrier's conversion pair.
func nestedCollectionNode(name string, collection ir.AttributeType, levels []ir.AttributeType, get, set string) node {
	attribute := ir.Attribute{Name: name, WireName: name, Type: collection, ElementType: levels[len(levels)-1],
		NestedCollectionElementTypes: levels, ComputedOptionalRequired: ir.Optional}
	return node{attribute: attribute, fb: &sdkbind.FieldBinding{
		Attr: name, Wire: name, Type: collection, ElementType: attribute.ElementType,
		NestedCollectionElementTypes: levels,
		Access:                       sdkbind.FieldAccess{Get: "Get" + ir.GoName(name), Set: "Set" + ir.GoName(name), ConvertGet: get, ConvertSet: set},
	}}
}

// TestUnit_ElementTypeExpression_ComposesEveryLevel holds the attr.Type
// expression of a collection's element to its levels, innermost first.
func TestUnit_ElementTypeExpression_ComposesEveryLevel(t *testing.T) {
	for name, testCase := range map[string]struct {
		levels []ir.AttributeType
		want   string
	}{
		"leaf alone":      {nil, "types.StringType"},
		"list of strings": {[]ir.AttributeType{ir.TypeList}, "types.ListType{ElemType: types.StringType}"},
		"map of lists":    {[]ir.AttributeType{ir.TypeMap, ir.TypeList}, "types.MapType{ElemType: types.ListType{ElemType: types.StringType}}"},
		"list of maps of lists": {[]ir.AttributeType{ir.TypeList, ir.TypeMap, ir.TypeList},
			"types.ListType{ElemType: types.MapType{ElemType: types.ListType{ElemType: types.StringType}}}"},
	} {
		if got := elementTypeExpression(testCase.levels, "types.StringType"); got != testCase.want {
			t.Errorf("%s: composed %s, want %s", name, got, testCase.want)
		}
	}
}

// TestUnit_SchemaTypeOf_ANestedCollectionIsAPlainCollectionAttribute holds
// each of the four shapes to a ListAttribute or MapAttribute whose element
// type is composed to depth, so the attr.Type expression and the schema's
// ElementType line follow from one record.
func TestUnit_SchemaTypeOf_ANestedCollectionIsAPlainCollectionAttribute(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		collection    ir.AttributeType
		levels        []ir.AttributeType
		wantAttribute string
		wantElement   string
	}{
		{"list of lists", ir.TypeList, []ir.AttributeType{ir.TypeList, ir.TypeString}, "ListAttribute", "types.ListType{ElemType: types.StringType}"},
		{"list of maps", ir.TypeList, []ir.AttributeType{ir.TypeMap, ir.TypeBool}, "ListAttribute", "types.MapType{ElemType: types.BoolType}"},
		{"map of lists", ir.TypeMap, []ir.AttributeType{ir.TypeList, ir.TypeInt64}, "MapAttribute", "types.ListType{ElemType: types.Int64Type}"},
		{"map of maps", ir.TypeMap, []ir.AttributeType{ir.TypeMap, ir.TypeFloat64}, "MapAttribute", "types.MapType{ElemType: types.Float64Type}"},
	} {
		n := nestedCollectionNode("grid", testCase.collection, testCase.levels, "FromNestedCollectionSlice", "ToNestedCollectionSlice")
		resolved := schemaTypeOf(n)
		if resolved.SchemaAttribute != testCase.wantAttribute || resolved.ElementType != testCase.wantElement {
			t.Errorf("%s: resolved %s with element %s, want %s with %s", testCase.name, resolved.SchemaAttribute, resolved.ElementType, testCase.wantAttribute, testCase.wantElement)
		}
		wantAttrType := strings.Replace(resolved.ValueType, "types.", "types.", 1) + "Type{ElemType: " + testCase.wantElement + "}"
		if got := attrTypeExpr(newModelNamer("Thing", []node{n}), "", n); got != wantAttrType {
			t.Errorf("%s: attr.Type = %s, want %s", testCase.name, got, wantAttrType)
		}
		if got := fieldType(n); got != resolved.ValueType {
			t.Errorf("%s: model field %s, want %s", testCase.name, got, resolved.ValueType)
		}
	}
}

// TestUnit_FrameworkElementType_ACollectionKindIsNeverSpelledAsAString
// holds the leaf spelling to scalars: a collection kind reaching it is a
// missed depth check, and an empty spelling fails to compile rather than
// generating a schema that holds the wrong thing.
func TestUnit_FrameworkElementType_ACollectionKindIsNeverSpelledAsAString(t *testing.T) {
	for _, kind := range []ir.AttributeType{ir.TypeList, ir.TypeMap, ir.TypeObject} {
		if got := frameworkElementType(kind); got != "" {
			t.Errorf("%s spelled as %q", kind, got)
		}
	}
	if got := frameworkElementType(ir.TypeBool); got != "types.BoolType" {
		t.Errorf("bool spelled as %q", got)
	}
}

// TestUnit_Validators_TheLeafEnumIsWrappedPerLevel holds a closed set on
// the leaf string to one ValueXsAre per level, innermost first, and a map
// of enum strings to mapvalidator at one level.
func TestUnit_Validators_TheLeafEnumIsWrappedPerLevel(t *testing.T) {
	deep := ir.Attribute{Type: ir.TypeMap, ElementType: ir.TypeString,
		NestedCollectionElementTypes: []ir.AttributeType{ir.TypeList, ir.TypeString}}
	got := collectionMemberValidator(deep, `stringvalidator.OneOf("a")`)
	if got.SchemaDefinition != `mapvalidator.ValueListsAre(listvalidator.ValueStringsAre(stringvalidator.OneOf("a")))` {
		t.Errorf("a map of lists of enum strings validates as %s", got.SchemaDefinition)
	}
	paths := map[string]bool{}
	for _, imported := range got.Imports {
		paths[imported.Path] = true
	}
	for _, want := range []string{"mapvalidator", "listvalidator", "stringvalidator"} {
		if !paths[validatorRoot+want] {
			t.Errorf("the validator does not import %s", want)
		}
	}

	flat := ir.Attribute{Type: ir.TypeMap, ElementType: ir.TypeString}
	if got := collectionMemberValidator(flat, `stringvalidator.OneOf("a")`).SchemaDefinition; got != `mapvalidator.ValueStringsAre(stringvalidator.OneOf("a"))` {
		t.Errorf("a map of enum strings validates as %s", got)
	}
}

// TestUnit_StateLines_ANestedCollectionReadsThroughOneBridge holds the
// state mapping of a collection of collections to one call carrying ctx,
// the SDK value and the element type composed to depth, for each carrier's
// shorthand.
func TestUnit_StateLines_ANestedCollectionReadsThroughOneBridge(t *testing.T) {
	for shorthand, want := range map[string]string{
		"FromNestedCollectionSlice":               "data.Grid = convert.APIToFrameworkNestedCollectionList(ctx, remote.GetGrid(), types.ListType{ElemType: types.StringType})",
		"FromNestedCollectionSliceAdditionalData": "data.Grid = convert.APIToFrameworkNestedCollectionListAdditionalData(ctx, remote.GetGrid(), types.ListType{ElemType: types.StringType})",
		"FromNestedCollectionSliceUntypedNode":    "data.Grid = convert.APIToFrameworkNestedCollectionListUntypedNode(ctx, remote.GetGrid(), types.ListType{ElemType: types.StringType})",
	} {
		n := nestedCollectionNode("grid", ir.TypeList, []ir.AttributeType{ir.TypeList, ir.TypeString}, shorthand, "")
		lines, err := stateLines([]node{n}, "Thing", "data")
		if err != nil {
			t.Fatalf("%s: %v", shorthand, err)
		}
		if !strings.Contains(lines, want) {
			t.Errorf("%s rendered:\n%s\nwant %s", shorthand, lines, want)
		}
	}
}

// TestUnit_ConstructLines_ANestedCollectionWritesThroughOneBridge holds the
// write of a collection of collections to one call per carrier: the plain
// and untyped-node bridges take the setter, the bag map builds its model
// first, and the slice of bags is handed the model's constructor as a
// closure returning the model interface.
func TestUnit_ConstructLines_ANestedCollectionWritesThroughOneBridge(t *testing.T) {
	for shorthand, want := range map[string]string{
		"ToNestedCollectionMap":                 "if err := convert.FrameworkToAPINestedCollectionMap(ctx, data.Grid, body.SetGrid); err != nil {",
		"ToNestedCollectionSliceUntypedNode":    "if err := convert.FrameworkToAPINestedCollectionListUntypedNode(ctx, data.Grid, body.SetGrid); err != nil {",
		"ToNestedCollectionMapAdditionalData":   "if err := convert.FrameworkToAPINestedCollectionMapAdditionalData(ctx, data.Grid, gridMap.SetAdditionalData); err != nil {",
		"ToNestedCollectionSliceAdditionalData": "if err := convert.FrameworkToAPINestedCollectionListAdditionalData(ctx, data.Grid, func() models.Thing_gridable { return models.NewThing_grid() }, body.SetGrid); err != nil {",
	} {
		n := nestedCollectionNode("grid", ir.TypeMap, []ir.AttributeType{ir.TypeList, ir.TypeString}, "", shorthand)
		n.fb.NestedModel, n.fb.NestedWriteModel, n.fb.NestedConstructor = "models.Thing_gridable", "models.Thing_grid", "models.NewThing_grid()"
		lines, _, err := constructLines(newModelNamer("Thing", []node{n}), "", []node{n}, "data", "body", "", 1, false)
		if err != nil {
			t.Fatalf("%s: %v", shorthand, err)
		}
		if !strings.Contains(lines, want) {
			t.Errorf("%s rendered:\n%s\nwant %s", shorthand, lines, want)
		}
	}
}
