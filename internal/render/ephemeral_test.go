package render

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// ephemeralBlueprint returns a blueprint holding a seedable credential resource and the
// ephemeral that opens it, mirroring the pilot pair.
func ephemeralBlueprint() (blueprint.Blueprint, blueprint.Ephemeral) {
	seed := blueprint.Resource{
		Key: "credential", Name: "credential", GoPackage: "credential",
		GoTypeName: "CredentialResource",
		Binding: blueprint.ResourceBinding{
			Service: blueprint.ServiceRef{
				ImportPath: "example.com/sdk/credentials", TypeName: "Credentials",
				Accessor: "r.client.Credentials",
			},
			Read: &blueprint.Operation{
				Style: blueprint.CallStyleMethod, Method: "GetCredential",
				Return: blueprint.ReturnResultTransportError, ResultType: "credentials.Credential",
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
		attr("name", blueprint.KindString, blueprint.Required),
		func() blueprint.Attribute {
			a := attr("value", blueprint.KindString, blueprint.Required)
			a.Sensitive = true
			return a
		}(),
	}

	e := blueprint.Ephemeral{
		Key: "credential", Name: "credential", GoPackage: "credential",
		GoTypeName: "CredentialEphemeral", ModelTypeName: "CredentialEphemeralModel",
		Binding: blueprint.EphemeralBinding{
			Service: blueprint.ServiceRef{
				ImportPath: "example.com/sdk/credentials", TypeName: "Credentials",
				Accessor: "e.client.Credentials",
			},
			Open: &blueprint.Operation{
				Style: blueprint.CallStyleMethod, Method: "GetCredential",
				Return: blueprint.ReturnResultTransportError, ResultType: "credentials.Credential",
				Args: []blueprint.Argument{
					{Kind: blueprint.ArgContext},
					{Kind: blueprint.ArgConfigField, Field: "ID"},
				},
			},
			Response: blueprint.ResponseModel{
				Type: "credentials.Credential", AccessStyle: blueprint.AccessStructField,
			},
		},
		AccTest: &blueprint.AccSeed{
			SeedResourceKey: "credential",
			Args:            []blueprint.SeedArg{{Attr: "id", FromSeedAttr: "id"}},
		},
	}
	e.Schema.Attributes = []blueprint.Attribute{
		{
			Name: "id", GoField: "ID", Type: blueprint.AttrType{Kind: blueprint.KindString},
			ComputedOptionalRequired: blueprint.Required,
			Wire: blueprint.WireBinding{
				JSONPath: "id", SDKField: "ID", SDKGoType: "*string", SkipFlatten: true,
			},
		},
		{
			Name: "value", GoField: "Value", Type: blueprint.AttrType{Kind: blueprint.KindString},
			ComputedOptionalRequired: blueprint.Computed, Sensitive: true,
			Wire: blueprint.WireBinding{
				JSONPath: "value", SDKField: "Value", SDKGoType: "*string",
				Flatten: &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
			},
		},
	}

	bp := blueprint.Blueprint{
		Provider: blueprint.Provider{
			Name: "te", TypePrefix: "te", GoModule: "example.com/prov",
			SDK: blueprint.SDKModule{
				Dialect: blueprint.DialectRestyService, ModulePath: "example.com/sdk",
				ClientType:   "*te.Client",
				ClientImport: blueprint.Import{Path: "example.com/sdk/te"},
			},
		},
		Resources:  []blueprint.Resource{seed},
		Ephemerals: []blueprint.Ephemeral{e},
	}

	return bp, e
}

// TestUnit_Render_AnEphemeralRendersLikeADataSourceWithoutState.
func TestUnit_Render_AnEphemeralRendersLikeADataSourceWithoutState(t *testing.T) {
	t.Parallel()

	bp, e := ephemeralBlueprint()

	v, err := Ephemeral(bp, e, Options{})
	if err != nil {
		t.Fatalf("Ephemeral: %v", err)
	}

	if v.EphemeralName != "te_credential" {
		t.Errorf("name = %q", v.EphemeralName)
	}
	joined := strings.Join(v.Interfaces, "\n")
	for _, want := range []string{
		"ephemeral.EphemeralResource ", "ephemeral.EphemeralResourceWithConfigure",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("interfaces missing %q:\n%s", want, joined)
		}
	}
	if v.Open == nil || !strings.Contains(v.Open.Call, "GetCredential(ctx, data.ID.ValueString())") {
		t.Errorf("open call = %+v", v.Open)
	}
	// The config-only attribute is not flattened; the result attribute is.
	if len(v.State.Assignments) != 1 ||
		!strings.Contains(v.State.Assignments[0], "data.Value = convert.PtrStringToFramework(remote.Value)") {
		t.Errorf("state assignments = %v", v.State.Assignments)
	}

	// Renew and close are modelled and refused: nothing may claim a lifecycle nothing
	// generates.
	e.Binding.Renew = &blueprint.Operation{
		Style: blueprint.CallStyleMethod, Method: "RenewCredential",
		Return: blueprint.ReturnResultTransportError, ResultType: "credentials.Credential",
	}
	if _, err := Ephemeral(bp, e, Options{}); !isUnsupported(err) {
		t.Errorf("renew must be refused until it renders: %v", err)
	}
}

// TestUnit_Render_TheEphemeralTestEchoesTheOpenedValue.
//
// An ephemeral value never reaches state -- that is the kind's contract -- so the echo
// provider is the one place a check can read what Open produced, and the assertion is
// the full round trip: stored by the seed's create, decrypted by the open.
func TestUnit_Render_TheEphemeralTestEchoesTheOpenedValue(t *testing.T) {
	t.Parallel()

	bp, e := ephemeralBlueprint()

	v, fixture, err := EphemeralAccTest(bp, e, Options{})
	if err != nil {
		t.Fatalf("EphemeralAccTest: %v", err)
	}

	if len(v.Checks) != 1 ||
		!strings.Contains(v.Checks[0], `Key("data.value").HasValue("tfacc-value")`) {
		t.Errorf("checks = %v", v.Checks)
	}

	if len(fixture.Args) != 1 || fixture.Args[0].HCL != "te_credential.test.id" {
		t.Errorf("the ephemeral should reference the seed: %+v", fixture.Args)
	}
	if fixture.Seed.Header != "" {
		t.Error("the embedded seed fixture must not carry its own header")
	}

	// No seed, no test: an ephemeral opens an object it does not create.
	e.AccTest = nil
	if _, _, err := EphemeralAccTest(bp, e, Options{}); !isUnsupported(err) {
		t.Errorf("a missing seed must be a stated refusal: %v", err)
	}
	if _, err := EphemeralSeedHelper(bp, e, Options{}); !isUnsupported(err) {
		t.Errorf("the helper must refuse alongside the test: %v", err)
	}
}
