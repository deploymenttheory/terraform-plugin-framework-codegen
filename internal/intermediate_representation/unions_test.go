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
		union := &specmodel.Schema{}
		for _, keyword := range kinds {
			union.OneOf = append(union.OneOf, &specmodel.Schema{Type: keyword})
		}
		return union
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
		}}, "", "writable position"},
	} {
		tree := buildAttributeTree(&specmodel.Schema{Type: "object", Properties: []specmodel.Property{
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

// unionSpec declares a read-only object whose owner is a union of two named
// components, and a payload whose second branch names nothing.
const unionSpec = `openapi: 3.0.3
info: {title: U, version: "1"}
paths: {}
components:
  schemas:
    simple-user:
      type: object
      properties:
        login: {type: string}
    Enterprise:
      type: object
      properties:
        slug: {type: string}
    deployment:
      type: object
      properties:
        task: {type: string}
    thing:
      type: object
      properties:
        owner:
          oneOf:
            - $ref: '#/components/schemas/simple-user'
            - $ref: '#/components/schemas/Enterprise'
        payload:
          oneOf:
            - $ref: '#/components/schemas/deployment'
            - type: object
              properties:
                loose: {type: string}
`

// A union with an object branch is served where nothing writes it: one
// attribute per branch, named for the component the branch references, which
// is also what the SDK names its composed-type accessor after.
func TestUnit_DeriveUnion_ReadOnlyYieldsAnAttributePerVariant(t *testing.T) {
	// Read side only: nothing writes the union, which is the case the
	// generated datasources and list elements are made of.
	tree := buildAttributeTree(nil, mustLoad(t, unionSpec).Schemas["thing"], nil, false)

	owner := attribute(t, tree, "owner")
	if owner.Unsupported {
		t.Fatalf("a read-only union refused: %s", owner.UnsupportedReason)
	}
	if owner.Kind != TypeObject || owner.Nested == nil {
		t.Fatalf("owner = %+v, want an object carrying its variants", owner)
	}

	// The component names the variant, and the wire name keeps the
	// component's own spelling so the drafted accessor lands on
	// GetSimpleUser rather than on a name nothing in the SDK answers to.
	for _, want := range []struct{ name, wire, field string }{
		{"simple_user", "simple-user", "login"},
		{"enterprise", "Enterprise", "slug"},
	} {
		variant := attribute(t, owner.Nested, want.name)
		if variant.WireName != want.wire {
			t.Errorf("%s wire name = %q, want %q", want.name, variant.WireName, want.wire)
		}
		if variant.Kind != TypeObject || variant.ComputedOptionalRequired != Computed {
			t.Errorf("%s = %+v, want a computed object", want.name, variant)
		}
		if a := attribute(t, variant.Nested, want.field); a.Kind != TypeString {
			t.Errorf("%s does not carry its branch's own %s: %+v", want.name, want.field, a)
		}
	}
}

// A variant the document does not name has no attribute to become, and half
// a union is a schema that cannot hold what the API returns. The refusal says
// how many branches are anonymous so the document can be corrected.
func TestUnit_DeriveUnion_AnAnonymousBranchRefusesTheWholeUnion(t *testing.T) {
	tree := buildAttributeTree(nil, mustLoad(t, unionSpec).Schemas["thing"], nil, false)

	payload := attribute(t, tree, "payload")
	if !payload.Unsupported {
		t.Fatalf("an anonymous branch derived: %+v", payload)
	}
	for _, want := range []string{"2 branches", "1 name no component"} {
		if !strings.Contains(payload.UnsupportedReason, want) {
			t.Errorf("reason does not state %q: %s", want, payload.UnsupportedReason)
		}
	}
}
