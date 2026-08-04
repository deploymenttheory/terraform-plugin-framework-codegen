package generate

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// seededBlueprint returns a blueprint holding a seedable resource and a data source
// reading it, mirroring the pilot's tag pair.
func seededBlueprint(args []blueprint.SeedArg) (blueprint.Blueprint, blueprint.DataSource) {
	seed := blueprint.Resource{
		Key: "tag", Name: "tag", GoPackage: "tag", GoTypeName: "TagResource",
		Binding: blueprint.ResourceBinding{
			Service: blueprint.ServiceRef{
				ImportPath: "example.com/sdk/tags", TypeName: "Tags",
				Accessor: "r.client.Tags",
			},
			Read: &blueprint.Operation{
				Style: blueprint.CallStyleMethod, Method: "GetTag",
				Return: blueprint.ReturnResultTransportError, ResultType: "tags.Tag",
				Args: []blueprint.Argument{
					{Kind: blueprint.ArgContext},
					{Kind: blueprint.ArgStateField, Field: "ID"},
				},
			},
			ID: blueprint.IDBinding{Attribute: "id", GoField: "ID"},
		},
	}
	seed.Schema.Attributes = []blueprint.Attribute{
		func() blueprint.Attribute {
			a := attr("id", blueprint.KindString, blueprint.Computed)
			a.GoField = "ID"
			return a
		}(),
		attr("key", blueprint.KindString, blueprint.Required),
		func() blueprint.Attribute {
			a := attr("color", blueprint.KindString, blueprint.Computed)
			a.Behaviour.Normalises = "lowercases it"
			return a
		}(),
	}

	ds := blueprint.DataSource{
		Key: "tag", Name: "tag", GoPackage: "tag", GoTypeName: "TagDataSource",
		AccTest: &blueprint.AccSeed{SeedResourceKey: "tag", Args: args},
	}
	ds.Schema.Attributes = []blueprint.Attribute{
		attr("id", blueprint.KindString, blueprint.Required),
		attr("key", blueprint.KindString, blueprint.Computed),
		attr("color", blueprint.KindString, blueprint.Computed),
		attr("assignments", blueprint.KindListNested, blueprint.Computed),
	}

	bp := blueprint.Blueprint{
		Provider:  blueprint.Provider{Name: "te", TypePrefix: "te", GoModule: "example.com/prov"},
		Resources: []blueprint.Resource{seed},
	}

	return bp, ds
}

// TestUnit_Render_ADataSourceTestReadsBackWhatTheSeedCreated.
//
// The cross-key checks are the point: the data source's state and the seed's state
// arrive by different code paths, and a flatten that drops or mangles a field on either
// shows up as a mismatch no compile can see.
func TestUnit_Render_ADataSourceTestReadsBackWhatTheSeedCreated(t *testing.T) {
	t.Parallel()

	bp, ds := seededBlueprint([]blueprint.SeedArg{{Attr: "id", FromSeedAttr: "id"}})

	v, fixture, err := DataSourceAccTest(bp, ds, Options{})
	if err != nil {
		t.Fatalf("DataSourceAccTest: %v", err)
	}

	if v.Address != "data.te_tag.test" || v.SeedAddress != "te_tag.test" {
		t.Errorf("addresses = %q, %q", v.Address, v.SeedAddress)
	}

	joined := strings.Join(v.Checks, "\n")

	if !strings.Contains(joined,
		`check.That(address).Key("key").MatchesOtherKey(check.That(seedAddress).Key("key"))`) {
		t.Errorf("a shared assertable attribute should cross-check:\n%s", joined)
	}
	// The seed's colour is normalised, so its state is by definition not comparable.
	if strings.Contains(joined, `Key("color")`) {
		t.Errorf("a normalised seed attribute must not be asserted:\n%s", joined)
	}
	// A singular data source's collections belong to the object, and a freshly seeded
	// object legitimately has empty ones.
	if strings.Contains(joined, "assignments") {
		t.Errorf("a collection on a singular data source must not be asserted:\n%s", joined)
	}

	// The fixture wires the data block into the seed by reference.
	var ref bool
	for _, arg := range fixture.Args {
		if arg.Name == "id" && arg.HCL == "te_tag.test.id" {
			ref = true
		}
	}
	if !ref {
		t.Errorf("the data block should reference the seed: %+v", fixture.Args)
	}
	if fixture.Seed.Header != "" {
		t.Error("the embedded seed fixture must not carry its own header")
	}
}

// TestUnit_Render_AListingDataSourceAssertsItsCollectionHoldsTheSeed.
func TestUnit_Render_AListingDataSourceAssertsItsCollectionHoldsTheSeed(t *testing.T) {
	t.Parallel()

	bp, ds := seededBlueprint(nil)
	ds.Schema.Attributes = []blueprint.Attribute{
		attr("tags", blueprint.KindListNested, blueprint.Computed),
	}

	v, _, err := DataSourceAccTest(bp, ds, Options{})
	if err != nil {
		t.Fatalf("DataSourceAccTest: %v", err)
	}

	if len(v.Checks) != 1 ||
		!strings.Contains(v.Checks[0], `Key("tags.#").CountAtLeast(1)`) {
		t.Errorf("a listing read after seeding must hold at least one element: %v", v.Checks)
	}
}

// TestUnit_Render_ADataSourceTestNeedsADeclaredSeed: reading an empty tenant would pass
// or fail on what happens to be lying around, so no seed is a stated refusal.
func TestUnit_Render_ADataSourceTestNeedsADeclaredSeed(t *testing.T) {
	t.Parallel()

	bp, ds := seededBlueprint(nil)
	ds.AccTest = nil

	_, _, err := DataSourceAccTest(bp, ds, Options{})
	if !isUnsupported(err) {
		t.Fatalf("error = %v, want a stated refusal", err)
	}

	if _, err := SeedHelper(bp, ds, Options{}); !isUnsupported(err) {
		t.Errorf("the helper must refuse alongside the test: %v", err)
	}
}

// TestUnit_Render_TheSeedHelperIsReEmittedIntoTheDataSourcePackage.
//
// The seed's own helper lives in a _test.go file, which Go makes invisible to every
// other package -- so the data source's test build carries its own copy.
func TestUnit_Render_TheSeedHelperIsReEmittedIntoTheDataSourcePackage(t *testing.T) {
	t.Parallel()

	bp, ds := seededBlueprint(nil)
	ds.GoPackage = "tagds"

	helper, err := SeedHelper(bp, ds, Options{})
	if err != nil {
		t.Fatalf("SeedHelper: %v", err)
	}
	if helper.Package != "tagds" {
		t.Errorf("package = %q, want the data source's own", helper.Package)
	}
	if helper.GoTypeName != "TagTestResource" {
		t.Errorf("type = %q, want the seed's helper type", helper.GoTypeName)
	}
}

// TestUnit_Render_AnActionTestFillsItsArgsFromTheEnvironmentAndReversesItself.
func TestUnit_Render_AnActionTestFillsItsArgsFromTheEnvironmentAndReversesItself(t *testing.T) {
	t.Parallel()

	action := blueprint.Action{
		Key: "disable_agent", Name: "disable_agent", GoPackage: "disable_agent",
		GoTypeName: "DisableAgentAction",
		Binding: blueprint.ActionBinding{
			Service: blueprint.ServiceRef{
				ImportPath: "example.com/sdk/agents", TypeName: "Agents",
				Accessor: "a.client.API.Agents",
			},
		},
		AccTest: &blueprint.ActionAccTest{
			EnvArgs: []blueprint.EnvArg{{Attr: "agent_id", EnvVar: "TEST_AGENT_ID"}},
			Cleanup: &blueprint.Operation{
				Style: blueprint.CallStyleMethod, Method: "EnableAgent",
				Return: blueprint.ReturnResultTransportError, ResultType: "agents.Agent",
				Args: []blueprint.Argument{
					{Kind: blueprint.ArgContext},
					{Kind: blueprint.ArgConfigField, Field: "AgentID"},
				},
			},
		},
	}
	agentAttr := attr("agent_id", blueprint.KindString, blueprint.Required)
	agentAttr.GoField = "AgentID"
	action.Schema.Attributes = []blueprint.Attribute{agentAttr}

	bp := blueprint.Blueprint{
		Provider: blueprint.Provider{Name: "te", TypePrefix: "te", GoModule: "example.com/prov"},
	}

	v, fixture, err := ActionAccTest(bp, action, Options{})
	if err != nil {
		t.Fatalf("ActionAccTest: %v", err)
	}

	if len(v.EnvArgs) != 1 || v.EnvArgs[0].EnvVar != "TEST_AGENT_ID" ||
		v.EnvArgs[0].GoVar != "agentID" {
		t.Errorf("env args = %+v", v.EnvArgs)
	}

	if !v.HasCleanup {
		t.Fatal("a declared cleanup must be rendered")
	}
	if v.CleanupCall != "client.API.Agents.EnableAgent(ctx, agentID)" {
		t.Errorf("cleanup call = %q", v.CleanupCall)
	}
	// Plain assignment: the generated cleanup already declared err.
	if !strings.HasSuffix(v.CleanupAssign, "=") || strings.HasSuffix(v.CleanupAssign, ":=") {
		t.Errorf("cleanup assign = %q, want a plain assignment", v.CleanupAssign)
	}

	if len(fixture.Vars) != 1 || fixture.Vars[0] != "agent_id" {
		t.Errorf("fixture vars = %v", fixture.Vars)
	}
	if fixture.Args[0].HCL != "var.agent_id" {
		t.Errorf("the config should reference the variable: %+v", fixture.Args)
	}
	if !fixture.Reversed {
		t.Error("the fixture should say the action is reversed")
	}

	// A cleanup arg no envArg fills would generate a variable that does not exist.
	action.AccTest.Cleanup.Args[1].Field = "Missing"
	if _, _, err := ActionAccTest(bp, action, Options{}); !isUnsupported(err) {
		t.Errorf("an unfillable cleanup arg must be refused: %v", err)
	}

	// No accTest, no test -- an action's subject is not the generator's to invent.
	action.AccTest = nil
	if _, _, err := ActionAccTest(bp, action, Options{}); !isUnsupported(err) {
		t.Errorf("a missing accTest must be a stated refusal: %v", err)
	}
}
