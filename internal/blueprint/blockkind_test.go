package blueprint

import (
	"strings"
	"testing"
)

// TestUnit_BlockKind_SchemaPackage pins the framework import path suffix per kind.
//
// These strings become import paths in generated code, so a wrong one is a build
// failure in the emitted provider rather than here. The unknown kind returns empty
// rather than guessing, so a caller that forgets to handle it produces an obviously
// broken import instead of a plausible wrong one.
func TestUnit_BlockKind_SchemaPackage(t *testing.T) {
	t.Parallel()

	want := map[BlockKind]string{
		BlockResource:         "resource/schema",
		BlockDataSource:       "datasource/schema",
		BlockEphemeral:        "ephemeral/schema",
		BlockAction:           "action/schema",
		BlockList:             "list/schema",
		BlockKind("nonesuch"): "",
	}

	for kind, w := range want {
		if got := kind.SchemaPackage(); got != w {
			t.Errorf("%s.SchemaPackage() = %q, want %q", kind, got, w)
		}
	}
}

// TestUnit_BlockKind_FieldSupport is the table from the framework's own schema
// packages, asserted directly.
//
// Each row is a field that exists on some kinds' attribute structs and not others. If
// one of these predicates is wrong the generator emits a struct literal setting a
// field the type does not have, and the failure surfaces as a compiler error in
// somebody else's generated provider.
func TestUnit_BlockKind_FieldSupport(t *testing.T) {
	t.Parallel()

	kinds := []BlockKind{BlockResource, BlockDataSource, BlockEphemeral, BlockAction, BlockList}

	tests := []struct {
		field string
		got   func(BlockKind) bool
		// want is indexed the same as kinds.
		want []bool
	}{
		// Action and list attributes have no Computed. For a list resource that is the
		// structural reason its config schema is filter-only.
		{"Computed", BlockKind.SupportsComputed, []bool{true, true, true, false, false}},
		{"Sensitive", BlockKind.SupportsSensitive, []bool{true, true, true, false, false}},
		// Only a managed resource has a plan to modify.
		{"PlanModifiers", BlockKind.SupportsPlanModifiers, []bool{true, false, false, false, false}},
		{"Default", BlockKind.SupportsDefault, []bool{true, false, false, false, false}},
		{"WriteOnly", BlockKind.SupportsWriteOnly, []bool{true, false, false, true, false}},
	}

	for _, tc := range tests {
		for i, kind := range kinds {
			if got := tc.got(kind); got != tc.want[i] {
				t.Errorf("%s on a %s attribute: got %v, want %v", tc.field, kind, got, tc.want[i])
			}
		}
	}
}

// TestUnit_BlockKind_ValidateRefusesFieldsTheKindHasNoHomeFor is the refusal this
// phase exists to add.
//
// Before it, a blueprint could declare a default on a data source attribute and the
// generator would emit datasourceschema.StringAttribute{Default: ...}, which does not
// compile. The point of refusing here is that the message names the attribute and the
// kind; the compiler error in generated output names neither.
func TestUnit_BlockKind_ValidateRefusesFieldsTheKindHasNoHomeFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		kind     BlockKind
		attr     Attribute
		wantPath string
		wantMsg  string
	}{
		{
			name:     "default on a data source attribute",
			kind:     BlockDataSource,
			attr:     Attribute{Name: "f", Default: &Default{Static: &Literal{Kind: KindString, Raw: `"x"`}}},
			wantPath: "attributes[f].default",
			wantMsg:  "resources only",
		},
		{
			name:     "plan modifier on a data source attribute",
			kind:     BlockDataSource,
			attr:     Attribute{Name: "f", PlanModifiers: []CustomCode{{}}},
			wantPath: "attributes[f].planModifiers",
			wantMsg:  "no plan to modify",
		},
		{
			name:     "write-only on a data source attribute",
			kind:     BlockDataSource,
			attr:     Attribute{Name: "f", WriteOnly: true},
			wantPath: "attributes[f].writeOnly",
			wantMsg:  "write-only",
		},
		{
			name:     "computed on a list attribute",
			kind:     BlockList,
			attr:     Attribute{Name: "f", ComputedOptionalRequired: Computed},
			wantPath: "attributes[f].computedOptionalRequired",
			wantMsg:  "required and optional",
		},
		{
			// computed_optional also sets Computed, so it must be refused too --
			// checking only for the exact value "computed" would let it through.
			name:     "computed_optional on an action attribute",
			kind:     BlockAction,
			attr:     Attribute{Name: "f", ComputedOptionalRequired: ComputedOptional},
			wantPath: "attributes[f].computedOptionalRequired",
			wantMsg:  "required and optional",
		},
		{
			name:     "sensitive on an action attribute",
			kind:     BlockAction,
			attr:     Attribute{Name: "f", Sensitive: true},
			wantPath: "attributes[f].sensitive",
			wantMsg:  "sensitive",
		},
		{
			// The refusal has to reach through nesting, because that is where a
			// hand-authored blueprint is least likely to be read carefully.
			name: "default on an attribute nested inside a data source attribute",
			kind: BlockDataSource,
			attr: Attribute{
				Name: "outer",
				Type: AttrType{
					Kind: KindSingleNested,
					NestedObject: &NestedAttributeObject{
						Attributes: []Attribute{{
							Name:    "inner",
							Default: &Default{Static: &Literal{Kind: KindBool, Raw: "false"}},
						}},
					},
				},
			},
			wantPath: "attributes[outer].type.nestedObject.attributes[inner].default",
			wantMsg:  "resources only",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var p problems
			tc.attr.validateForKind(tc.kind, "attributes["+tc.attr.Name+"]", &p)

			if len(p) == 0 {
				t.Fatalf("a %s attribute should have been refused", tc.kind)
			}

			var paths, msgs []string
			for _, prob := range p {
				paths = append(paths, prob.path)
				msgs = append(msgs, prob.msg)
			}
			joinedPaths := strings.Join(paths, "; ")
			joinedMsgs := strings.Join(msgs, "; ")

			if !strings.Contains(joinedPaths, tc.wantPath) {
				t.Errorf("path %q not among %q", tc.wantPath, joinedPaths)
			}
			// The kind is named, so the reader knows which schema package refused and
			// does not have to guess why the same attribute is legal elsewhere.
			if !strings.Contains(joinedMsgs, string(tc.kind)) {
				t.Errorf("message should name the kind %q: %q", tc.kind, joinedMsgs)
			}
			if !strings.Contains(joinedMsgs, tc.wantMsg) {
				t.Errorf("message should explain %q: %q", tc.wantMsg, joinedMsgs)
			}
		})
	}
}

// TestUnit_BlockKind_ValidateAcceptsEveryFieldOnAResource is the other half of the
// refusal: a resource attribute setting all of them is fine.
//
// Without this, the refusal could be made to pass by rejecting everything.
func TestUnit_BlockKind_ValidateAcceptsEveryFieldOnAResource(t *testing.T) {
	t.Parallel()

	a := Attribute{
		Name:                     "f",
		ComputedOptionalRequired: ComputedOptional,
		Sensitive:                true,
		WriteOnly:                true,
		PlanModifiers:            []CustomCode{{}},
		Default:                  &Default{Static: &Literal{Kind: KindString, Raw: `"x"`}},
	}

	var p problems
	a.validateForKind(BlockResource, "attributes[f]", &p)

	if len(p) != 0 {
		t.Errorf("a resource attribute may set all of these; got %v", p)
	}
}
