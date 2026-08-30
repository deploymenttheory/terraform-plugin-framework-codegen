package intermediate_representation

import (
	"reflect"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

func TestUnit_EnsureParentParameters_NamesAParentSpelledIdAfterItsEntity(t *testing.T) {
	tree := &AttributeTree{Attributes: []Attribute{
		{Name: "id", WireName: "id", Type: TypeString, ComputedOptionalRequired: Computed},
		{Name: "scope", WireName: "scope", Type: TypeString, ComputedOptionalRequired: Required},
	}}
	ensureParentParameters(tree, []URLPathParameter{{Name: "id", Type: TypeString}}, "template")

	if len(tree.Attributes) != 3 || tree.Attributes[0].Name != "template_id" {
		t.Fatalf("want template_id prepended, got %+v", tree.Attributes)
	}
	if got := tree.Attributes[0]; got.WireName != "id" || got.ComputedOptionalRequired != Required {
		t.Fatalf("the parent keeps the parameter's spelling as its wire name and is required, got %+v", got)
	}

	// With no parent entity to name it after, the parameter is not added:
	// the resource's own id already holds the name.
	bare := &AttributeTree{Attributes: []Attribute{{Name: "id", WireName: "id", Type: TypeString, ComputedOptionalRequired: Computed}}}
	ensureParentParameters(bare, []URLPathParameter{{Name: "id", Type: TypeString}}, "")
	if len(bare.Attributes) != 1 {
		t.Fatalf("an unnameable parent was added: %+v", bare.Attributes)
	}
}

func TestUnit_BuildTree_MembersOfAServerFilledAttributeAreComputed(t *testing.T) {
	attributes := []Attribute{
		{Name: "name", ComputedOptionalRequired: Required},
		{Name: "groupings", ComputedOptionalRequired: ComputedOptional, NestedAttributes: &AttributeTree{Attributes: []Attribute{
			{Name: "title", ComputedOptionalRequired: Required},
			{Name: "type", ComputedOptionalRequired: Optional},
			{Name: "count", ComputedOptionalRequired: Computed},
			{Name: "inner", ComputedOptionalRequired: Optional, NestedAttributes: &AttributeTree{Attributes: []Attribute{
				{Name: "leaf", ComputedOptionalRequired: Required},
			}}},
		}}},
		{Name: "settings", ComputedOptionalRequired: Optional, NestedAttributes: &AttributeTree{Attributes: []Attribute{
			{Name: "mode", ComputedOptionalRequired: Required},
		}}},
	}
	serverFilledMembersComputed(attributes, false)

	if attributes[0].ComputedOptionalRequired != Required {
		t.Errorf("a top-level required attribute changed: %s", attributes[0].ComputedOptionalRequired)
	}
	members := attributes[1].NestedAttributes.Attributes
	for _, want := range []struct {
		name string
		cor  ComputedOptionalRequired
	}{{"title", ComputedOptional}, {"type", ComputedOptional}, {"count", Computed}, {"inner", ComputedOptional}} {
		for _, member := range members {
			if member.Name == want.name && member.ComputedOptionalRequired != want.cor {
				t.Errorf("%s under a server-filled attribute = %s, want %s", member.Name, member.ComputedOptionalRequired, want.cor)
			}
		}
	}
	if leaf := members[3].NestedAttributes.Attributes[0]; leaf.ComputedOptionalRequired != ComputedOptional {
		t.Errorf("a member two levels down = %s, want computed-optional", leaf.ComputedOptionalRequired)
	}
	// A plain optional parent is the practitioner's whole object: its
	// members keep what the document declares.
	if mode := attributes[2].NestedAttributes.Attributes[0]; mode.ComputedOptionalRequired != Required {
		t.Errorf("a member of an optional attribute = %s, want required", mode.ComputedOptionalRequired)
	}
}

func TestUnit_DeriveListType_CarriesTheElementEnum(t *testing.T) {
	attribute := Attribute{Name: "modules"}
	items := &specmodel.Schema{Type: "string", Enum: []any{"default", "extended"}}
	deriveListType(&attribute, &specmodel.Schema{Type: "array", Items: items}, nil, nil)
	if attribute.Type != TypeList || attribute.ElementType != TypeString {
		t.Fatalf("attribute = %+v, want a list of strings", attribute)
	}
	if !reflect.DeepEqual(attribute.OneOf, []string{"default", "extended"}) {
		t.Errorf("OneOf = %v, want the element enum", attribute.OneOf)
	}
	plain := Attribute{Name: "labels"}
	deriveListType(&plain, &specmodel.Schema{Type: "array", Items: &specmodel.Schema{Type: "string"}}, nil, nil)
	if len(plain.OneOf) != 0 {
		t.Errorf("a list of free strings carries an enum: %v", plain.OneOf)
	}
}
