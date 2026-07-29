package render

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// dataSourceByKey returns one committed pilot data source's view.
func dataSourceByKey(t *testing.T, key string) (blueprint.Blueprint, DataSourceView) {
	t.Helper()

	bp := pilot(t)

	for _, d := range bp.DataSources {
		if d.Key != key {
			continue
		}
		v, err := DataSource(bp, d, Options{BlueprintPath: "blueprints/x", BlueprintSHA256: "abc"})
		if err != nil {
			t.Fatalf("DataSource(%q): %v", key, err)
		}
		return bp, v
	}

	t.Fatalf("the committed pilot blueprint has no data source keyed %q", key)
	return blueprint.Blueprint{}, DataSourceView{}
}

// TestUnit_Render_DataSourceUsesTheDataSourceSchemaPackage is the whole point of
// parameterising schema rendering by kind.
//
// The generated selector stays `schema` for every kind -- each block is emitted into its
// own package, so there is nothing to disambiguate -- which means the only observable
// difference is the import path. If that resolves to resource/schema, the generated data
// source assigns a resource schema to a datasource.SchemaResponse and does not compile.
func TestUnit_Render_DataSourceUsesTheDataSourceSchemaPackage(t *testing.T) {
	t.Parallel()

	_, v := dataSourceByKey(t, "tag")

	const want = "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	if !strings.Contains(v.Imports.DataSource, want) {
		t.Errorf("datasource.go should import %q:\n%s", want, v.Imports.DataSource)
	}
	if strings.Contains(v.Imports.DataSource, "terraform-plugin-framework/resource/schema") {
		t.Errorf("datasource.go must not import the resource schema package:\n%s", v.Imports.DataSource)
	}

	// The same schema rendered for a resource must reach the other package, or the
	// parameterisation is not doing anything.
	rv := pilotView(t)
	if !strings.Contains(rv.Imports.Resource, "terraform-plugin-framework/resource/schema") {
		t.Errorf("resource.go should still import the resource schema package:\n%s", rv.Imports.Resource)
	}
}

// TestUnit_Render_DataSourceEmitsNoPlanModifiers guards the one place the generator can
// put a plan modifier somewhere blueprint.Validate cannot see it.
//
// UseStateForUnknown is synthesised by planModifiersFor for computed strings rather than
// declared in the blueprint, so nothing upstream would refuse it on a data source. The
// pilot's tag data source has fourteen computed string attributes, so this would fire
// fourteen times if the guard were dropped -- and datasource/schema.StringAttribute has
// no PlanModifiers field, so the generated provider would not compile.
func TestUnit_Render_DataSourceEmitsNoPlanModifiers(t *testing.T) {
	t.Parallel()

	_, v := dataSourceByKey(t, "tag")

	joined := strings.Join(v.SchemaAttributes, "\n")
	for _, forbidden := range []string{"PlanModifiers", "planmodifier", "Default:"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("a data source attribute must not carry %s", forbidden)
		}
	}
	if strings.Contains(v.Imports.DataSource, "planmodifier") {
		t.Errorf("datasource.go must not import a planmodifier package:\n%s", v.Imports.DataSource)
	}

	// The resource, rendered from the same attribute shapes, still gets them. Without
	// this the test would pass if the synthesis were removed altogether.
	rv := pilotView(t)
	if !strings.Contains(strings.Join(rv.SchemaAttributes, "\n"), "stringplanmodifier.UseStateForUnknown()") {
		t.Error("the resource should still get a synthesised UseStateForUnknown")
	}
}

// TestUnit_Render_DataSourceModelUsesTheDataSourceTimeouts pins the timeouts type.
//
// The resource and data source timeouts packages both export a Value, they are distinct
// types, and the data source's Read method is what crud.HandleTimeout is handed. Getting
// this wrong compiles nowhere near the mistake.
func TestUnit_Render_DataSourceModelUsesTheDataSourceTimeouts(t *testing.T) {
	t.Parallel()

	_, v := dataSourceByKey(t, "tag")

	last := v.ModelFields[len(v.ModelFields)-1]
	if !strings.Contains(last, `Timeouts timeouts.Value `) {
		t.Errorf("the last model field should be the timeouts value, got %q", last)
	}

	if v.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout = %d, want the provider default", v.ReadTimeout)
	}
}

// TestUnit_Render_DataSourceReadsFromConfig checks the read call and its argument source.
//
// A data source has no prior state and no plan, so an argument that renders as state.X
// would reference a variable the generated Read never declares.
func TestUnit_Render_DataSourceReadsFromConfig(t *testing.T) {
	t.Parallel()

	_, v := dataSourceByKey(t, "tag")

	if v.Read == nil {
		t.Fatal("a data source view must carry a read")
	}
	if want := "d.client.API.Tags.GetTag(ctx, data.ID.ValueString())"; v.Read.Call != want {
		t.Errorf("Call = %q, want %q", v.Read.Call, want)
	}
	for _, forbidden := range []string{"state.", "plan.", "body"} {
		if strings.Contains(v.Read.Call, forbidden) {
			t.Errorf("a data source read must not reference %q: %s", forbidden, v.Read.Call)
		}
	}
	if v.Read.ResultVar != "remote" {
		t.Errorf("ResultVar = %q, want remote", v.Read.ResultVar)
	}
}

// TestUnit_Render_DataSourceFlattensAListAsAList is the regression test for a latent bug
// this phase surfaced.
//
// A list_nested attribute's model field is a types.List, but the flatten helper used to
// hardcode types.SetNull and types.SetValueFrom -- so any list_nested attribute generated
// a helper whose return type did not match its own signature. It went unnoticed because
// the only nested attributes in the pilot were sets.
func TestUnit_Render_DataSourceFlattensAListAsAList(t *testing.T) {
	t.Parallel()

	_, v := dataSourceByKey(t, "tags")

	// Three helpers now, because the element carries two nested objects of its own. The
	// list one is the outermost.
	var list *NestedFuncView
	for i := range v.State.NestedObject {
		if v.State.NestedObject[i].FuncName == "flattenTagSummaries" {
			list = &v.State.NestedObject[i]
		}
	}
	if list == nil {
		t.Fatalf("no flattenTagSummaries helper among %d", len(v.State.NestedObject))
	}

	if list.FrameworkType != "types.List" {
		t.Errorf("FrameworkType = %q, want types.List", list.FrameworkType)
	}
	if list.Container != "List" {
		t.Errorf("Container = %q, want List; a types.List built with types.SetNull does not compile",
			list.Container)
	}

	// Every helper's container must match its own framework type, at any depth.
	for _, h := range v.State.NestedObject {
		want := strings.TrimPrefix(h.FrameworkType, "types.")
		if h.Container != want {
			t.Errorf("%s: Container = %q but FrameworkType = %q", h.FuncName, h.Container, h.FrameworkType)
		}
	}

	// And a set still says Set, or the parameterisation has simply flipped the bug.
	rv := pilotView(t)
	for _, rh := range rv.State.NestedObject {
		if rh.FrameworkType == "types.Set" && rh.Container != "Set" {
			t.Errorf("%s: Container = %q, want Set", rh.FuncName, rh.Container)
		}
	}
}

// TestUnit_Render_DataSourceAssertsItsInterfaces keeps the compile-time assertions honest.
func TestUnit_Render_DataSourceAssertsItsInterfaces(t *testing.T) {
	t.Parallel()

	_, v := dataSourceByKey(t, "tag")

	want := []string{
		"_ datasource.DataSource = &TagDataSource{}",
		"_ datasource.DataSourceWithConfigure = &TagDataSource{}",
	}
	if len(v.Interfaces) != len(want) {
		t.Fatalf("got %d assertions, want %d: %v", len(v.Interfaces), len(want), v.Interfaces)
	}
	for i, w := range want {
		if v.Interfaces[i] != w {
			t.Errorf("assertion %d = %q, want %q", i, v.Interfaces[i], w)
		}
	}
}

// TestUnit_Render_DataSourceWithNoReadIsRefused covers the guard that would otherwise
// dereference a nil operation.
func TestUnit_Render_DataSourceWithNoReadIsRefused(t *testing.T) {
	t.Parallel()

	bp, _ := dataSourceByKey(t, "tag")

	d := bp.DataSources[0]
	d.Binding.Read = nil

	if _, err := DataSource(bp, d, Options{}); err == nil {
		t.Error("a data source with no read operation must be refused")
	}
}

// TestUnit_Render_UsesElementTypesDescendsIntoNestedObjects pins the condition that
// decides whether a schema file imports the framework's types package.
//
// Checking only the top level made the resource compile by luck: its one collection is
// nested, and a separate "has nested shapes" rule pulled the import in anyway. A data
// source whose nested objects are all scalars got the import with nothing to use it, and
// an unused import is a compile error.
func TestUnit_Render_UsesElementTypesDescendsIntoNestedObjects(t *testing.T) {
	t.Parallel()

	nestedCollection := blueprint.Schema{Attributes: []blueprint.Attribute{{
		Name: "outer", GoField: "Outer",
		Type: blueprint.AttrType{
			Kind: blueprint.KindSetNested,
			NestedObject: &blueprint.NestedAttributeObject{
				Attributes: []blueprint.Attribute{{
					Name: "values", GoField: "Values",
					Type: blueprint.AttrType{
						Kind:        blueprint.KindSet,
						ElementType: &blueprint.AttrType{Kind: blueprint.KindString},
					},
				}},
			},
		},
	}}}
	if !usesElementTypes(nestedCollection) {
		t.Error("a collection nested inside an object is still a use of an element type")
	}

	nestedScalars := blueprint.Schema{Attributes: []blueprint.Attribute{{
		Name: "outer", GoField: "Outer",
		Type: blueprint.AttrType{
			Kind: blueprint.KindSetNested,
			NestedObject: &blueprint.NestedAttributeObject{
				Attributes: []blueprint.Attribute{{
					Name: "id", GoField: "ID",
					Type: blueprint.AttrType{Kind: blueprint.KindString},
				}},
			},
		},
	}}}
	if usesElementTypes(nestedScalars) {
		t.Error("nested scalars render no element type, so the import would be unused")
	}

	// A dropped attribute is not rendered, so it cannot justify an import.
	dropped := nestedCollection
	dropped.Attributes[0].Drop = true
	if usesElementTypes(dropped) {
		t.Error("a dropped attribute must not pull in an import")
	}
}
