package render

import (
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// testResourceScope is the scope the resource-oriented rendering tests pass. It is named
// rather than inlined so a test that means to render as a data source has to say so.
var testResourceScope = schemaScope{
	kind: blueprint.BlockResource,
	what: `resource "tag"`,
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

// TestUnit_Render_NestedDepthIsRefusedRatherThanEmittedWrongly is the important
// one. Each nesting level needs its own model, attr.Type map and helper; emitting
// a partially-correct mapping produces a diff a practitioner cannot resolve, so
// exceeding the supported depth must fail loudly.
func TestUnit_Render_NestedDepthIsRefusedRatherThanEmittedWrongly(t *testing.T) {
	t.Parallel()

	inner := nestedAttr(blueprint.KindSetNested, scalarChild("id", "ID"))
	inner.Name = "inner"
	inner.GoField = "Inner"

	outer := nestedAttr(blueprint.KindSetNested, inner)

	r := blueprint.Resource{Key: "tag", Schema: blueprint.Schema{
		Attributes: []blueprint.Attribute{outer},
	}}

	_, err := nestedShapes(testResourceScope, r.Schema)
	if err == nil {
		t.Fatal("expected nesting beyond the supported depth to be refused")
	}

	var unsupported *ErrUnsupported
	if !errors.As(err, &unsupported) {
		t.Fatalf("error should be an ErrUnsupported: %v", err)
	}
	// The message has to name the offending attribute, not merely the limit.
	if !strings.Contains(err.Error(), "inner") {
		t.Errorf("error should name the attribute at fault: %v", err)
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
func TestUnit_Render_AttrTypeExprRefusesNesting(t *testing.T) {
	t.Parallel()

	_, err := attrTypeExpr(blueprint.AttrType{Kind: blueprint.KindSetNested})
	if err == nil {
		t.Fatal("expected a nested kind to have no attr.Type expression")
	}
}
