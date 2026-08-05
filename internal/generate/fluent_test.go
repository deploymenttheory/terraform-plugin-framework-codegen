package generate

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// fluentPilot rebinds the committed pilot's tag resource onto a kiota-shaped
// SDK: fluent chains, method access, an interface response. The schema, wire
// join keys and convert calls are untouched -- which is the claim under test:
// switching dialect is a binding change, not a schema change.
func fluentPilot(t *testing.T) blueprint.Blueprint {
	t.Helper()

	bp := pilot(t)
	bp.Provider.SDK.Dialect = blueprint.DialectKiotaFluent

	ctx := blueprint.Argument{Kind: blueprint.ArgContext}
	nilCfg := blueprint.Argument{Kind: blueprint.ArgLiteral, Expr: "nil"}
	id := blueprint.Argument{Kind: blueprint.ArgStateField, Field: "ID"}

	r := &bp.Resources[0]
	r.Binding.Service.TypeName = ""
	r.Binding.Service.Accessor = "r.client"
	r.Binding.Body.AccessStyle = blueprint.AccessMethod
	r.Binding.ID.FromCreate = "created.GetId()"

	fluent := func(segs ...blueprint.ChainSegment) *blueprint.Operation {
		return &blueprint.Operation{
			Style:      blueprint.CallStyleFluent,
			Chain:      segs,
			Return:     blueprint.ReturnResultError,
			ResultType: r.Binding.Body.ResponseType,
		}
	}
	seg := func(m string, args ...blueprint.Argument) blueprint.ChainSegment {
		return blueprint.ChainSegment{Method: m, Args: args}
	}

	r.Binding.Create = fluent(seg("Tags"), seg("Post", ctx, blueprint.Argument{Kind: blueprint.ArgBody}, nilCfg))
	r.Binding.Read = fluent(seg("Tags"), seg("ByTagId", id), seg("Get", ctx, nilCfg))
	r.Binding.Update = fluent(seg("Tags"), seg("ByTagId", id), seg("Patch", ctx, blueprint.Argument{Kind: blueprint.ArgBody}, nilCfg))
	r.Binding.Delete = &blueprint.Operation{
		Style:  blueprint.CallStyleFluent,
		Chain:  []blueprint.ChainSegment{seg("Tags"), seg("ByTagId", id), seg("Delete", ctx, nilCfg)},
		Return: blueprint.ReturnError,
	}
	// The list facet stays method-style in the committed pilot; a mixed
	// document would be refused, so drop it for this fixture.
	r.List = nil
	bp.DataSources = nil
	bp.Ephemerals = nil
	bp.Actions = nil
	bp.Resources = bp.Resources[:1]

	return bp
}

func TestUnit_Generate_FluentDialectRenders(t *testing.T) {
	t.Parallel()

	bp := fluentPilot(t)

	if err := bp.Validate(); err != nil {
		t.Fatalf("the fluent fixture must validate now the dialect is implemented: %v", err)
	}

	v, err := Resource(bp, bp.Resources[0], Options{})
	if err != nil {
		t.Fatalf("Resource: %v", err)
	}

	if want := "r.client.Tags().ByTagId(state.ID.ValueString()).Get(ctx, nil)"; v.CRUD.Read.Call != want {
		t.Errorf("read call = %q, want %q", v.CRUD.Read.Call, want)
	}
	if want := "r.client.Tags().Post(ctx, body, nil)"; v.CRUD.Create.Call != want {
		t.Errorf("create call = %q, want %q", v.CRUD.Create.Call, want)
	}
	if !v.CRUD.Read.NilResultGuard || !v.CRUD.Create.NilResultGuard {
		t.Error("fluent reads and creates must carry the nil-result guard")
	}
	if v.CRUD.Delete.NilResultGuard {
		t.Error("a delete discards its result and must not carry the guard")
	}

	construct := strings.Join(v.Construct.Assignments, "\n")
	if !strings.Contains(construct, "body.Set") {
		t.Errorf("construct must write through setters:\n%s", construct)
	}
	if strings.Contains(construct, "body.Name =") {
		t.Errorf("construct must not assign struct fields under method access:\n%s", construct)
	}

	state := strings.Join(v.State.Assignments, "\n")
	if !strings.Contains(state, "remote.Get") {
		t.Errorf("state mapping must read through getters:\n%s", state)
	}

	if !strings.Contains(v.CRUD.IDAssign, "created.GetId()") {
		t.Errorf("the identifier must come off the create result's getter: %s", v.CRUD.IDAssign)
	}
}

// TestUnit_Generate_FluentSingleNestedConstructs proves the single-object
// helpers under method access: the element is built by constructor, handed
// around as the bare interface, and returned without an address-of.
func TestUnit_Generate_FluentSingleNestedConstructs(t *testing.T) {
	t.Parallel()

	bp := fluentPilot(t)
	r := &bp.Resources[0]

	r.Schema.Attributes = append(r.Schema.Attributes, blueprint.Attribute{
		Name: "details", GoField: "Details",
		ComputedOptionalRequired: blueprint.Optional,
		Type: blueprint.AttrType{
			Kind: blueprint.KindSingleNested,
			NestedObject: &blueprint.NestedAttributeObject{
				GoTypeName:      "TagDetailsModel",
				SDKType:         "models.Tags_API_Detailsable",
				ConstructorExpr: "models.NewTags_API_Details()",
				AttrTypesVar:    "tagDetailsAttrTypes",
				ObjectTypeVar:   "tagDetailsObjectType",
				ExpandFunc:      "expandTagDetails",
				FlattenFunc:     "flattenTagDetails",
				Attributes: []blueprint.Attribute{{
					Name: "note", GoField: "Note",
					ComputedOptionalRequired: blueprint.Optional,
					Type:                     blueprint.AttrType{Kind: blueprint.KindString},
					Wire: blueprint.WireBinding{
						JSONPath: "note", SDKField: "Note", SDKGoType: "*string",
						Flatten: &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
						Expand:  &blueprint.ConvertCall{Func: "convert.FrameworkToPtrString"},
					},
				}},
			},
		},
		Wire: blueprint.WireBinding{
			JSONPath: "details", SDKField: "Details", SDKGoType: "models.Tags_API_Detailsable",
			Flatten: &blueprint.ConvertCall{Func: "flattenTagDetails", NeedsCtx: true, ReturnsError: true},
			Expand:  &blueprint.ConvertCall{Func: "expandTagDetails", NeedsCtx: true, ReturnsError: true},
		},
	})

	if err := bp.Validate(); err != nil {
		t.Fatalf("a fluent single nested attribute must validate now: %v", err)
	}

	v, err := Resource(bp, bp.Resources[0], Options{})
	if err != nil {
		t.Fatalf("Resource: %v", err)
	}

	var expand *NestedFuncView
	for i := range v.Construct.NestedObject {
		if v.Construct.NestedObject[i].FuncName == "expandTagDetails" {
			expand = &v.Construct.NestedObject[i]
		}
	}
	if expand == nil {
		t.Fatal("the single-object expand helper was not built")
	}
	if expand.SDKSingleType != "models.Tags_API_Detailsable" {
		t.Errorf("SDKSingleType = %q, want the bare interface", expand.SDKSingleType)
	}
	if expand.ItemRef != "item" {
		t.Errorf("ItemRef = %q, want item (no address-of on an interface)", expand.ItemRef)
	}
	if expand.ConstructorExpr != "models.NewTags_API_Details()" {
		t.Errorf("ConstructorExpr = %q", expand.ConstructorExpr)
	}
	joined := strings.Join(expand.Assignments, "NEWLINE")
	if !strings.Contains(joined, "item.SetNote(") {
		t.Errorf("single-object assignments must go through setters:\n%s", joined)
	}

	var flatten *NestedFuncView
	for i := range v.State.NestedObject {
		if v.State.NestedObject[i].FuncName == "flattenTagDetails" {
			flatten = &v.State.NestedObject[i]
		}
	}
	if flatten == nil {
		t.Fatal("the single-object flatten helper was not built")
	}
	if flatten.InRef != "in" {
		t.Errorf("InRef = %q, want in (an interface cannot be dereferenced)", flatten.InRef)
	}
}

func TestUnit_Generate_FluentTestHelperRebasesTheChain(t *testing.T) {
	t.Parallel()

	bp := fluentPilot(t)

	v, err := TestHelper(bp, bp.Resources[0], Options{})
	if err != nil {
		t.Fatalf("TestHelper: %v", err)
	}

	if want := "client.Tags().ByTagId(state.ID).Get(ctx, nil)"; v.Call != want {
		t.Errorf("helper call = %q, want %q", v.Call, want)
	}
}

// TestUnit_Generate_ImmutableIsCreateAuthoritative proves an immutable
// attribute reaches the create body and never a split update body: the
// endpoint tests' update models declare a fraction of their create fields,
// and RequiresReplace owns changes to the rest.
func TestUnit_Generate_ImmutableIsCreateAuthoritative(t *testing.T) {
	t.Parallel()

	bp := fluentPilot(t)
	r := &bp.Resources[0]
	r.Binding.Body.UpdateRequestType = "models.Tags_API_TagUpdate"
	r.Binding.Body.UpdateConstructorExpr = "models.NewTags_API_TagUpdate()"

	yes := true
	for i := range r.Schema.Attributes {
		if r.Schema.Attributes[i].Name == "key" {
			r.Schema.Attributes[i].Behaviour.Immutable = &yes
		}
	}

	v, err := Resource(bp, bp.Resources[0], Options{})
	if err != nil {
		t.Fatalf("Resource: %v", err)
	}
	if v.Construct.Update == nil {
		t.Fatal("a split update body must produce an update target")
	}

	create := strings.Join(v.Construct.Assignments, "\n")
	update := strings.Join(v.Construct.Update.Assignments, "\n")
	if !strings.Contains(create, "SetKey(") {
		t.Errorf("the create body must keep the immutable field:\n%s", create)
	}
	if strings.Contains(update, "SetKey(") {
		t.Errorf("a split update body must omit the immutable field:\n%s", update)
	}
}
