package render

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

func TestUnit_Render_ArgExpr(t *testing.T) {
	t.Parallel()

	r := blueprint.Resource{Key: "tag"}

	tests := []struct {
		name    string
		arg     blueprint.Argument
		want    string
		wantErr bool
	}{
		{"context", blueprint.Argument{Kind: blueprint.ArgContext}, "ctx", false},
		{"body", blueprint.Argument{Kind: blueprint.ArgBody}, "body", false},
		{
			"state field",
			blueprint.Argument{Kind: blueprint.ArgStateField, Field: "ID"},
			"state.ID.ValueString()", false,
		},
		{
			"plan field",
			blueprint.Argument{Kind: blueprint.ArgPlanField, Field: "Name"},
			"plan.Name.ValueString()", false,
		},
		{
			// An explicit expression overrides the derived one, which is the
			// escape hatch for an argument the convention does not cover.
			"explicit expression wins",
			blueprint.Argument{Kind: blueprint.ArgStateField, Field: "ID", Expr: "custom()"},
			"custom()", false,
		},
		{"literal with no expression", blueprint.Argument{Kind: blueprint.ArgLiteral}, "", true},
		{"unknown kind", blueprint.Argument{Kind: "telepathy"}, "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := argExpr(r, tc.arg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("argExpr: %v", err)
			}
			if got != tc.want {
				t.Errorf("argExpr = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnit_Render_AttrTypeExprCollections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      blueprint.AttrType
		want    string
		wantErr bool
	}{
		{
			"map of strings",
			blueprint.AttrType{Kind: blueprint.KindMap, Elem: &blueprint.AttrType{Kind: blueprint.KindString}},
			"types.MapType{ElemType: types.StringType}", false,
		},
		{
			"set of sets",
			blueprint.AttrType{
				Kind: blueprint.KindSet,
				Elem: &blueprint.AttrType{Kind: blueprint.KindSet, Elem: &blueprint.AttrType{Kind: blueprint.KindString}},
			},
			"types.SetType{ElemType: types.SetType{ElemType: types.StringType}}", false,
		},
		{"set with no element", blueprint.AttrType{Kind: blueprint.KindSet}, "", true},
		{"map with no element", blueprint.AttrType{Kind: blueprint.KindMap}, "", true},
		{
			"collection of an unmappable element",
			blueprint.AttrType{Kind: blueprint.KindList, Elem: &blueprint.AttrType{Kind: "octopus"}},
			"", true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := attrTypeExpr(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("attrTypeExpr: %v", err)
			}
			if got != tc.want {
				t.Errorf("attrTypeExpr = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnit_Render_NestedDepthAllowsOneLevel is the companion to the refusal test:
// the supported depth must actually be supported.
func TestUnit_Render_NestedDepthAllowsOneLevel(t *testing.T) {
	t.Parallel()

	r := blueprint.Resource{
		Key: "tag",
		Attributes: []blueprint.Attribute{
			nestedAttr(blueprint.KindSetNested, scalarChild("id", "ID")),
		},
	}

	shapes, err := nestedShapes(r)
	if err != nil {
		t.Fatalf("one level of nesting must be supported: %v", err)
	}
	if len(shapes) != 1 {
		t.Errorf("got %d shapes, want 1", len(shapes))
	}

	// A dropped nested attribute is not a shape to generate.
	dropped := r
	dropped.Attributes[0].Drop = true
	if got, err := nestedShapes(dropped); err != nil || len(got) != 0 {
		t.Errorf("a dropped attribute should yield no shape: %v, %v", got, err)
	}
}

func TestUnit_Render_NestedShapeWithoutAnObjectFails(t *testing.T) {
	t.Parallel()

	r := blueprint.Resource{
		Key: "tag",
		Attributes: []blueprint.Attribute{{
			Name: "items", GoField: "Items",
			Type: blueprint.AttrType{Kind: blueprint.KindSetNested},
		}},
	}

	if _, err := nestedShapes(r); err == nil {
		t.Error("a nested kind with no object shape must fail")
	}
}

func TestUnit_Render_NestedFlattenView(t *testing.T) {
	t.Parallel()

	shapes, err := nestedShapes(blueprint.Resource{
		Key:        "tag",
		Attributes: []blueprint.Attribute{nestedAttr(blueprint.KindSetNested, scalarChild("id", "ID"))},
	})
	if err != nil {
		t.Fatalf("nestedShapes: %v", err)
	}

	v := nestedFlattenView(shapes[0])

	if v.FuncName != "flattenTagAssignments" || !v.IsCollection {
		t.Errorf("view = %+v", v)
	}
	if len(v.Assignments) != 1 || !strings.Contains(v.Assignments[0], "m.ID") {
		t.Errorf("assignments = %v", v.Assignments)
	}
	// An infallible child leaves the helper infallible, so the simpler signature
	// is generated.
	if v.NeedsDiagnostics {
		t.Error("an infallible child should not make the helper fallible")
	}
}

// TestUnit_Render_NestedSkipDirectionsAreHonoured: a shape excluded in one
// direction must not get a helper for that direction.
func TestUnit_Render_NestedSkipDirectionsAreHonoured(t *testing.T) {
	t.Parallel()

	bp := pilot(t)

	// The pilot has two nested collections; suppress expansion on one.
	for i := range bp.Resources[0].Attributes {
		if bp.Resources[0].Attributes[i].Type.Kind.IsNested() {
			bp.Resources[0].Attributes[i].Wire.SkipExpand = true
			bp.Resources[0].Attributes[i].Wire.Expand = nil
			bp.Resources[0].Attributes[i].Presence = blueprint.Computed
			break
		}
	}

	v, err := Resource(bp, bp.Resources[0], Options{})
	if err != nil {
		t.Fatalf("Resource: %v", err)
	}

	// Two nested shapes, but only one expand helper now.
	if len(v.State.Nested) <= len(v.Construct.Nested) {
		t.Errorf("suppressing an expand should leave fewer expand helpers than flatten helpers: %d vs %d",
			len(v.Construct.Nested), len(v.State.Nested))
	}
}

func TestUnit_Render_DataSourcePackagePath(t *testing.T) {
	t.Parallel()

	bp := pilot(t)
	bp.DataSources = []blueprint.DataSource{{
		Key: "agent", TerraformType: "thousandeyes_agent", GoPackage: "agent",
		GoPackageAlias: "v7Agent", GoTypeName: "AgentDataSource", ModelTypeName: "AgentDataSourceModel",
		ServiceGroup: "agents", APIVersionDir: "v7",
	}}

	v := Registration(bp, KindDataSources, Options{})

	if len(v.Entries) != 1 || !strings.HasPrefix(v.Entries[0], "v7Agent.") {
		t.Fatalf("entries = %v", v.Entries)
	}
	if !strings.Contains(v.Imports, "internal/services/datasources/agents/v7/agent") {
		t.Errorf("import path is wrong:\n%s", v.Imports)
	}
}
