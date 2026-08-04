package blueprint

import (
	"strings"
	"testing"
)

// withIdentity returns the valid pilot-shaped blueprint plus an identity, and lets a test
// deviate from it in exactly one way.
func withIdentity(mutate func(*ResourceIdentity)) Blueprint {
	b := validBlueprint()

	ri := &ResourceIdentity{
		GoTypeName: "TagResourceIdentity",
		Attributes: []IdentityAttribute{{
			Name:              "id",
			GoField:           "ID",
			Kind:              KindString,
			RequiredForImport: true,
			FromAttribute:     "id",
		}},
	}
	if mutate != nil {
		mutate(ri)
	}

	b.Resources[0].Identity = ri

	return b
}

// TestUnit_Blueprint_IdentityRefusesWhatIdentityschemaCannotExpress.
//
// The framework's identityschema package has scalars and a list of them, and two flags
// instead of a presence. Every refusal here is a shape it has no way to hold, so the
// alternative to refusing is emitting a type that does not exist or flags the framework
// rejects at runtime -- which is a worse place to find out.
func TestUnit_Blueprint_IdentityRefusesWhatIdentityschemaCannotExpress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*ResourceIdentity)
		wantPath string
		wantMsg  string
	}{
		{
			name:     "no attributes",
			mutate:   func(ri *ResourceIdentity) { ri.Attributes = nil },
			wantPath: "identity.attributes",
			wantMsg:  "cannot be emitted",
		},
		{
			name:     "no goTypeName",
			mutate:   func(ri *ResourceIdentity) { ri.GoTypeName = "" },
			wantPath: "identity.goTypeName",
			wantMsg:  "required",
		},
		{
			// identityschema has no nested attribute at all.
			name:     "a nested kind",
			mutate:   func(ri *ResourceIdentity) { ri.Attributes[0].Kind = KindSingleNested },
			wantPath: "identity.attributes[id].kind",
			wantMsg:  "no identityschema counterpart",
		},
		{
			name:     "a map kind",
			mutate:   func(ri *ResourceIdentity) { ri.Attributes[0].Kind = KindMap },
			wantPath: "identity.attributes[id].kind",
			wantMsg:  "no identityschema counterpart",
		},
		{
			// Neither flag means the attribute can never be supplied on import.
			name: "neither import flag",
			mutate: func(ri *ResourceIdentity) {
				ri.Attributes[0].RequiredForImport = false
			},
			wantPath: "identity.attributes[id]",
			wantMsg:  "either requiredForImport or optionalForImport",
		},
		{
			// Both is a contradiction the framework rejects at runtime.
			name: "both import flags",
			mutate: func(ri *ResourceIdentity) {
				ri.Attributes[0].OptionalForImport = true
			},
			wantPath: "identity.attributes[id]",
			wantMsg:  "exclusive",
		},
		{
			name:     "no source attribute",
			mutate:   func(ri *ResourceIdentity) { ri.Attributes[0].FromAttribute = "" },
			wantPath: "identity.attributes[id].fromAttribute",
			wantMsg:  "copies the identity value out of the resource model",
		},
		{
			// Generated code reads this field off the model, so a name that does not
			// resolve is a compile error in somebody else's provider.
			name:     "a source attribute the resource does not declare",
			mutate:   func(ri *ResourceIdentity) { ri.Attributes[0].FromAttribute = "nonesuch" },
			wantPath: "identity.attributes[id].fromAttribute",
			wantMsg:  "does not declare",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := withIdentity(tc.mutate).Validate()
			if err == nil {
				t.Fatal("expected the identity to be refused")
			}
			if !strings.Contains(err.Error(), tc.wantPath) {
				t.Errorf("error should name %q: %v", tc.wantPath, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error should explain %q: %v", tc.wantMsg, err)
			}
		})
	}

	// And the shape the pilot uses passes, or every case above could be passing for the
	// wrong reason.
	if err := withIdentity(nil).Validate(); err != nil {
		t.Errorf("a required-for-import string identity should be valid: %v", err)
	}
}

// TestUnit_Blueprint_ADroppedAttributeCannotSourceAnIdentity.
//
// A dropped attribute is not in the generated model, so reading the identity from it would
// not compile. Separate from the unknown-name case because the attribute *is* declared --
// only a check that ignores Drop would let it through.
func TestUnit_Blueprint_ADroppedAttributeCannotSourceAnIdentity(t *testing.T) {
	t.Parallel()

	b := withIdentity(nil)
	for i := range b.Resources[0].Schema.Attributes {
		if b.Resources[0].Schema.Attributes[i].Name == "id" {
			b.Resources[0].Schema.Attributes[i].Drop = true
		}
	}

	err := b.Validate()
	if err == nil {
		t.Fatal("an identity reading from a dropped attribute must be refused")
	}
	if !strings.Contains(err.Error(), "does not declare") {
		t.Errorf("error should say the attribute is not available: %v", err)
	}
}

// TestUnit_Blueprint_AListFacetRequiresAnIdentity is the refusal the phase turns on.
//
// The framework raises an error diagnostic for a ListResult with no identity, so a list
// resource on a resource that declares none cannot produce a usable result. Refused here
// because at query time it surfaces as a failure with nothing to point at.
func TestUnit_Blueprint_AListFacetRequiresAnIdentity(t *testing.T) {
	t.Parallel()

	b := validBlueprint()
	b.Resources[0].Identity = nil
	b.Resources[0].List = &ListFacet{
		GoTypeName: "TagListResource",
		Service:    ServiceRef{ImportPath: "example.com/sdk/p", TypeName: "Tags", Accessor: "l.client.Tags"},
		Read: &Operation{
			Style: CallStyleMethod, Method: "GetTags",
			Return: ReturnResultTransportError, ResultType: "tags.ResourceTags",
		},
		Response:    ResponseModel{Type: "tags.ResourceTags", AccessStyle: AccessStructField},
		ElementType: "tags.Tag",
		IdentityFrom: []ListIdentityMapping{
			{GoField: "ID", FromSDKField: "ID", IsPointer: true},
		},
	}

	err := b.Validate()
	if err == nil {
		t.Fatal("a list facet without an identity must be refused")
	}
	for _, want := range []string{"identity", "ListResult.Identity is mandatory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

// TestUnit_Blueprint_AListFacetMustFillEveryIdentityField.
//
// A partly-filled identity is worse than none: Terraform records an address that does not
// resolve, and the practitioner gets a query result they cannot import.
func TestUnit_Blueprint_AListFacetMustFillEveryIdentityField(t *testing.T) {
	t.Parallel()

	base := func() Blueprint {
		b := withIdentity(func(ri *ResourceIdentity) {
			// A composite identity, so there is a second field to leave unfilled.
			ri.Attributes = append(ri.Attributes, IdentityAttribute{
				Name:              "key",
				GoField:           "Key",
				Kind:              KindString,
				RequiredForImport: true,
				FromAttribute:     "key",
			})
		})
		b.Resources[0].List = &ListFacet{
			GoTypeName: "TagListResource",
			Service:    ServiceRef{ImportPath: "example.com/sdk/p", TypeName: "Tags", Accessor: "l.client.Tags"},
			Read: &Operation{
				Style: CallStyleMethod, Method: "GetTags",
				Return: ReturnResultTransportError, ResultType: "tags.ResourceTags",
			},
			Response:    ResponseModel{Type: "tags.ResourceTags", AccessStyle: AccessStructField},
			ElementType: "tags.Tag",
			IdentityFrom: []ListIdentityMapping{
				{GoField: "ID", FromSDKField: "ID", IsPointer: true},
			},
		}

		return b
	}

	err := base().Validate()
	if err == nil {
		t.Fatal("a list facet leaving an identity field unfilled must be refused")
	}
	if !strings.Contains(err.Error(), `"Key"`) {
		t.Errorf("error should name the unfilled field: %v", err)
	}
	if !strings.Contains(err.Error(), "does not resolve") {
		t.Errorf("error should say why it matters: %v", err)
	}

	// Filling it makes the facet valid.
	b := base()
	b.Resources[0].List.IdentityFrom = append(
		b.Resources[0].List.IdentityFrom,
		ListIdentityMapping{GoField: "Key", FromSDKField: "Key", IsPointer: true},
	)
	if err := b.Validate(); err != nil {
		t.Errorf("a fully-filled composite identity should be valid: %v", err)
	}

	// A mapping naming a field the identity does not declare is refused too.
	b = base()
	b.Resources[0].List.IdentityFrom = []ListIdentityMapping{
		{GoField: "ID", FromSDKField: "ID"},
		{GoField: "Key", FromSDKField: "Key"},
		{GoField: "Nonesuch", FromSDKField: "X"},
	}
	err = b.Validate()
	if err == nil || !strings.Contains(err.Error(), "Nonesuch") {
		t.Errorf("a mapping to an undeclared identity field must be refused: %v", err)
	}
}
