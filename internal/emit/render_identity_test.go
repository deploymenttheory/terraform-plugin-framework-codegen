package emit

import (
	"reflect"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// names of a resource's identity, in order, for comparison.
func identityNames(identity []identityAttribute) []string {
	out := make([]string, 0, len(identity))
	for _, a := range identity {
		out = append(out, a.Name)
	}
	return out
}

// An identity names one object: the parents that scope it, then its id. The
// parameter naming the object itself is the id and is not also addressing,
// which matters wherever the document declares that key as a property too —
// the identity would otherwise require the same value under two names, of
// every import and of every list result.
func TestUnit_ResourceIdentity_TheItemKeyIsTheIDAndNotAlsoAddressing(t *testing.T) {
	for _, testCase := range []struct {
		name string
		read *ir.Operation
		tree *ir.AttributeTree
		want []string
	}{
		{
			name: "the body declares the item key as a property",
			read: &ir.Operation{Kind: ir.OperationRead, Method: "GET",
				PathTemplate:   "/alerts/rules/{ruleId}",
				PathParameters: []ir.Parameter{{Name: "ruleId", Type: ir.TypeString}}},
			tree: &ir.AttributeTree{Attributes: []ir.Attribute{
				{Name: "id", WireName: "ruleId", Kind: ir.TypeString, ComputedOptionalRequired: ir.Computed},
				{Name: "rule_id", WireName: "ruleId", Kind: ir.TypeString, ComputedOptionalRequired: ir.Computed},
			}},
			want: []string{"id"},
		},
		{
			name: "a parent scopes the object",
			read: &ir.Operation{Kind: ir.OperationRead, Method: "GET",
				PathTemplate: "/repos/{owner}/hooks/{hookId}",
				PathParameters: []ir.Parameter{
					{Name: "owner", Type: ir.TypeString},
					{Name: "hookId", Type: ir.TypeString},
				}},
			tree: &ir.AttributeTree{Attributes: []ir.Attribute{
				{Name: "owner", WireName: "owner", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required},
				{Name: "id", WireName: "hookId", Kind: ir.TypeString, ComputedOptionalRequired: ir.Computed},
				{Name: "hook_id", WireName: "hookId", Kind: ir.TypeString, ComputedOptionalRequired: ir.Computed},
			}},
			want: []string{"owner", "id"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := identityNames(resourceIdentity(&ir.Resource{
				Schema:     testCase.tree,
				Operations: ir.Operations{Read: testCase.read},
			}))
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("identity = %v, want %v", got, testCase.want)
			}
		})
	}
}

// A collection path names no object, so every parameter on it is a parent
// and none of them is dropped as an item key.
func TestUnit_ResourceIdentity_ACollectionPathKeepsEveryParameter(t *testing.T) {
	got := identityAddressing(&ir.Operation{
		Kind: ir.OperationCreate, Method: "POST",
		PathTemplate: "/orgs/{org}/teams",
		PathParameters: []ir.Parameter{
			{Name: "org", Type: ir.TypeString},
		}})
	if !got["org"] {
		t.Errorf("a collection path's parameter is addressing, got %v", got)
	}
}
