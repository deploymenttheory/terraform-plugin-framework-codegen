package render

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// testResourceScope is the scope the resource-oriented rendering tests pass. It is named
// rather than inlined so a test that means to render as a data source has to say so.
var testResourceScope = schemaScope{
	kind: blueprint.BlockKindResource,
	what: `resource "tag"`,
	// The identifier, which is the only attribute UseStateForUnknown is applied to.
	idAttribute: "id",
}

func nestedAttr(kind blueprint.TypeKind, children ...blueprint.Attribute) blueprint.Attribute {
	return blueprint.Attribute{
		Name:                     "assignments",
		GoField:                  "Assignments",
		ComputedOptionalRequired: blueprint.ComputedOptional,
		Type: blueprint.AttrType{
			Kind: kind,
			NestedObject: &blueprint.NestedAttributeObject{
				GoTypeName:    "TagAssignmentModel",
				SDKType:       "tags.Assignment",
				AttrTypesVar:  "tagAssignmentAttrTypes",
				ObjectTypeVar: "tagAssignmentObjectType",
				ExpandFunc:    "expandTagAssignments",
				FlattenFunc:   "flattenTagAssignments",
				Attributes:    children,
			},
		},
		Wire: blueprint.WireBinding{
			SDKField: "Assignments",
			Expand:   &blueprint.ConvertCall{Func: "expandTagAssignments", NeedsCtx: true, ReturnsError: true},
			Flatten:  &blueprint.ConvertCall{Func: "flattenTagAssignments", NeedsCtx: true, ReturnsError: true},
		},
	}
}

func scalarChild(name, goField string) blueprint.Attribute {
	return blueprint.Attribute{
		Name:                     name,
		GoField:                  goField,
		ComputedOptionalRequired: blueprint.Required,
		Type:                     blueprint.AttrType{Kind: blueprint.KindString},
		Wire: blueprint.WireBinding{
			SDKField: goField,
			Expand:   &blueprint.ConvertCall{Func: "convert.FrameworkToPtrString"},
			Flatten:  &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
		},
	}
}

func TestUnit_Render_NestedSchemaUsesNestedObject(t *testing.T) {
	t.Parallel()

	imports := newImportSet()

	decl, err := nestedAttributeDecl(testResourceScope, nestedAttr(blueprint.KindSetNested, scalarChild("id", "ID")), imports)
	if err != nil {
		t.Fatalf("nestedAttributeDecl: %v", err)
	}

	// A collection of nested objects wraps its children in a nested-object
	// descriptor; getting this wrong does not compile.
	for _, want := range []string{"SetNestedAttribute", "NestedObject", "schema.NestedAttributeObject", `"id"`} {
		if !strings.Contains(decl, want) {
			t.Errorf("declaration omits %q:\n%s", want, decl)
		}
	}
}

// TestUnit_Render_SingleNestedHoldsAttributesDirectly pins the asymmetry in the
// framework's API: a single nested attribute has no nested-object wrapper.
func TestUnit_Render_SingleNestedHoldsAttributesDirectly(t *testing.T) {
	t.Parallel()

	decl, err := nestedAttributeDecl(testResourceScope, nestedAttr(blueprint.KindSingleNested, scalarChild("id", "ID")), newImportSet())
	if err != nil {
		t.Fatalf("nestedAttributeDecl: %v", err)
	}

	if strings.Contains(decl, "NestedObject") {
		t.Errorf("a single nested attribute must not use a nested-object wrapper:\n%s", decl)
	}
	if !strings.Contains(decl, "SingleNestedAttribute") {
		t.Errorf("declaration should use SingleNestedAttribute:\n%s", decl)
	}
}

// TestUnit_Render_NestedDepthTwoLevelsGenerateTwoShapes replaces a test that asserted two
// levels were refused.
//
// Deleting rather than adapting it: the old fixture kept passing after the cap was
// removed, but against the identifier-collision check, because nestedAttr gives every
// level the same generated names. A test that still passes for a reason its name disowns
// is worse than one that fails.
func TestUnit_Render_NestedDepthTwoLevelsGenerateTwoShapes(t *testing.T) {
	t.Parallel()

	inner := nestedAttr(blueprint.KindSetNested, scalarChild("id", "ID"))
	inner.Name = "inner"
	inner.GoField = "Inner"
	// Distinct generated identifiers, or the collision check fires and the depth path is
	// never exercised.
	inner.Type.NestedObject.GoTypeName = "InnerModel"
	inner.Type.NestedObject.AttrTypesVar = "innerAttrTypes"
	inner.Type.NestedObject.ObjectTypeVar = "innerObjectType"
	inner.Type.NestedObject.ExpandFunc = "expandInner"
	inner.Type.NestedObject.FlattenFunc = "flattenInner"

	outer := nestedAttr(blueprint.KindSetNested, inner)

	shapes, err := nestedShapes(testResourceScope, blueprint.Schema{
		Attributes: []blueprint.Attribute{outer},
	})
	if err != nil {
		t.Fatalf("two levels of nesting must be supported: %v", err)
	}
	if len(shapes) != 2 {
		t.Fatalf("got %d shapes, want one per level", len(shapes))
	}
	if shapes[0].path != "assignments" || shapes[1].path != "assignments.inner" {
		t.Errorf("paths = %q, %q; want the outermost first", shapes[0].path, shapes[1].path)
	}
}

func TestUnit_Render_NestedModelDeclaresTheShapeOnce(t *testing.T) {
	t.Parallel()

	shapes, err := nestedShapes(testResourceScope, blueprint.Schema{
		Attributes: []blueprint.Attribute{
			nestedAttr(blueprint.KindSetNested, scalarChild("id", "ID"), scalarChild("type", "Type")),
		},
	})
	if err != nil {
		t.Fatalf("nestedShapes: %v", err)
	}
	if len(shapes) != 1 {
		t.Fatalf("got %d shapes, want 1", len(shapes))
	}

	v, err := nestedModelView(shapes[0])
	if err != nil {
		t.Fatalf("nestedModelView: %v", err)
	}

	if len(v.Fields) != 2 || len(v.AttrTypeEntries) != 2 {
		t.Errorf("got %d fields and %d attr type entries, want 2 of each", len(v.Fields), len(v.AttrTypeEntries))
	}
	// The model and the attr.Type map must describe the same field set, or the
	// framework fails to decode elements at apply time rather than at build time.
	for _, want := range []string{`"id"`, `"type"`} {
		found := false
		for _, e := range v.AttrTypeEntries {
			if strings.Contains(e, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("attr type map omits %s: %v", want, v.AttrTypeEntries)
		}
	}
}

// TestUnit_Render_FallibleChildMakesTheHelperFallible checks the propagation that
// decides the generated signatures. A child conversion that can fail forces the
// enclosing helper, and then construct or state, to carry diagnostics.
func TestUnit_Render_FallibleChildMakesTheHelperFallible(t *testing.T) {
	t.Parallel()

	fallible := scalarChild("values", "Values")
	fallible.Type = blueprint.AttrType{Kind: blueprint.KindSet, ElementType: &blueprint.AttrType{Kind: blueprint.KindString}}
	fallible.Wire.Expand = &blueprint.ConvertCall{
		Func: "convert.FrameworkSetToStringSlice", NeedsCtx: true, ReturnsError: true,
	}

	shapes, err := nestedShapes(testResourceScope, blueprint.Schema{
		Attributes: []blueprint.Attribute{nestedAttr(blueprint.KindSetNested, fallible)},
	})
	if err != nil {
		t.Fatalf("nestedShapes: %v", err)
	}

	expand := nestedExpandView(shapes[0])
	if !expand.NeedsDiagnostics {
		t.Error("a fallible child conversion must make the expand helper fallible")
	}
	if !strings.Contains(strings.Join(expand.Assignments, "\n"), "diags.Append(d...)") {
		t.Errorf("a fallible conversion must append its diagnostics:\n%v", expand.Assignments)
	}
}

func TestUnit_Render_AttrTypeExpr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   blueprint.AttrType
		want string
	}{
		{"string", blueprint.AttrType{Kind: blueprint.KindString}, "types.StringType"},
		{"bool", blueprint.AttrType{Kind: blueprint.KindBool}, "types.BoolType"},
		{
			"set of strings",
			blueprint.AttrType{Kind: blueprint.KindSet, ElementType: &blueprint.AttrType{Kind: blueprint.KindString}},
			"types.SetType{ElemType: types.StringType}",
		},
		{
			"list of int64",
			blueprint.AttrType{Kind: blueprint.KindList, ElementType: &blueprint.AttrType{Kind: blueprint.KindInt64}},
			"types.ListType{ElemType: types.Int64Type}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := attrTypeExpr(tc.in)
			if err != nil {
				t.Fatalf("attrTypeExpr: %v", err)
			}
			if got != tc.want {
				t.Errorf("attrTypeExpr = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnit_Render_AttrTypeExprRefusesNesting confirms an unsupported shape errors
// rather than silently emitting something that does not compile.
func TestUnit_Render_AttrTypeExprNeedsAnObjectToNameIt(t *testing.T) {
	t.Parallel()

	// A nested kind now has an attr.Type expression -- the object type var its own model
	// declares -- so what is refused is a nested kind with nothing to name.
	if _, err := attrTypeExpr(blueprint.AttrType{Kind: blueprint.KindSetNested}); err == nil {
		t.Error("a nested kind with no object shape has nothing to name")
	}

	noVar := blueprint.AttrType{
		Kind:         blueprint.KindSetNested,
		NestedObject: &blueprint.NestedAttributeObject{GoTypeName: "ItemModel"},
	}
	if _, err := attrTypeExpr(noVar); err == nil {
		t.Error("a nested object with no objectTypeVar cannot be referred to")
	}

	// And the expressions an enclosing attr.Type map actually gets.
	set := blueprint.AttrType{
		Kind:         blueprint.KindSetNested,
		NestedObject: &blueprint.NestedAttributeObject{ObjectTypeVar: "itemObjectType"},
	}
	got, err := attrTypeExpr(set)
	if err != nil {
		t.Fatalf("attrTypeExpr: %v", err)
	}
	if want := "types.SetType{ElemType: itemObjectType}"; got != want {
		t.Errorf("attrTypeExpr = %q, want %q", got, want)
	}

	single := blueprint.AttrType{
		Kind:         blueprint.KindSingleNested,
		NestedObject: &blueprint.NestedAttributeObject{ObjectTypeVar: "itemObjectType"},
	}
	got, err = attrTypeExpr(single)
	if err != nil {
		t.Fatalf("attrTypeExpr: %v", err)
	}
	if want := "itemObjectType"; got != want {
		t.Errorf("a single nested object is its own object type: got %q, want %q", got, want)
	}
}
