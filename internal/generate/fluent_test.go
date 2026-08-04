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
