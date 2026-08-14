package intermediate_representation

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// TestUnit_ResolveUnion_CollapsesScalarBranches proves a union whose
// branches are all simple values becomes one attribute type, string
// whenever the branches disagree, and that a union with an object branch is
// still refused by name.
func TestUnit_ResolveUnion_CollapsesScalarBranches(t *testing.T) {
	scalar := func(kinds ...string) *specmodel.Schema {
		u := &specmodel.Schema{}
		for _, k := range kinds {
			u.OneOf = append(u.OneOf, &specmodel.Schema{Type: k})
		}
		return u
	}

	for _, testCase := range []struct {
		name       string
		schema     *specmodel.Schema
		wantKind   AttributeType
		wantReason string
	}{
		{"integer or string", scalar("integer", "string"), TypeString, ""},
		{"string or integer, order reversed", scalar("string", "integer"), TypeString, ""},
		{"integer, number or string", scalar("integer", "number", "string"), TypeString, ""},
		{"one branch only", scalar("string"), TypeString, ""},
		{"branches that agree", scalar("integer", "integer"), TypeInt64, ""},
		{"booleans", scalar("boolean", "boolean"), TypeBool, ""},
		{"anyOf collapses too", &specmodel.Schema{AnyOf: []*specmodel.Schema{
			{Type: "integer"}, {Type: "string"}}}, TypeString, ""},
		{"an object branch", &specmodel.Schema{OneOf: []*specmodel.Schema{
			{Type: "string"},
			{Type: "object", Properties: []specmodel.Property{{Name: "x", Schema: &specmodel.Schema{Type: "string"}}}},
		}}, "", "one attribute per variant"},
	} {
		tree := buildTree(&specmodel.Schema{Type: "object", Properties: []specmodel.Property{
			{Name: "field", Schema: testCase.schema},
		}}, nil, nil, false)
		got := attribute(t, tree, "field")

		if testCase.wantReason != "" {
			if !got.Unsupported {
				t.Errorf("%s: derived %q, want a refusal", testCase.name, got.Kind)
			} else if !strings.Contains(got.UnsupportedReason, testCase.wantReason) {
				t.Errorf("%s: reason = %q", testCase.name, got.UnsupportedReason)
			}
			continue
		}
		if got.Unsupported {
			t.Errorf("%s: refused with %q", testCase.name, got.UnsupportedReason)
			continue
		}
		if got.Kind != testCase.wantKind {
			t.Errorf("%s: kind = %q, want %q", testCase.name, got.Kind, testCase.wantKind)
		}
	}
}
