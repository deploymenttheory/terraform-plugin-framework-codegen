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
		// Both wire directions, because a resource is a kind that expands: the
		// per-kind check covers wire as well as the schema fields, and an attribute
		// missing an expand would fail here for a reason this test is not about.
		Wire: WireBinding{
			Expand:  &ConvertCall{Func: "convert.FrameworkToPtrString"},
			Flatten: &ConvertCall{Func: "convert.PtrStringToFramework"},
		},
	}

	var p problems
	a.validateForKind(BlockResource, "attributes[f]", &p)

	if len(p) != 0 {
		t.Errorf("a resource attribute may set all of these; got %v", p)
	}
}

// TestUnit_BlockKind_Expands pins which kinds send attribute values to the API.
//
// A kind that expands needs an expand conversion on every settable attribute; one that
// does not must have none, because a settable attribute there is a lookup argument or a
// filter that reaches the API as a call argument rather than through a request body.
func TestUnit_BlockKind_Expands(t *testing.T) {
	t.Parallel()

	// Resource only. This said resource-and-action until the first action was generated:
	// an action does send values, which is why it looked like it belonged, but it sends them
	// as call arguments and the emitter generates no construct function for one -- so there
	// is no request body for an expand to build.
	want := map[BlockKind]bool{
		BlockResource:   true,
		BlockAction:     false,
		BlockDataSource: false,
		BlockEphemeral:  false,
		BlockList:       false,
	}

	for kind, w := range want {
		if got := kind.Expands(); got != w {
			t.Errorf("%s.Expands() = %v, want %v", kind, got, w)
		}
	}
}

// TestUnit_BlockKind_Flattens pins which kinds read values back.
//
// Everything except an action. InvokeResponse has no field to carry a result, so a flatten
// conversion on an action attribute would convert into somewhere that does not exist -- and
// requiring one, as the kind-agnostic rule did, made the first action unrepresentable.
func TestUnit_BlockKind_Flattens(t *testing.T) {
	t.Parallel()

	want := map[BlockKind]bool{
		BlockResource:   true,
		BlockDataSource: true,
		BlockEphemeral:  true,
		BlockList:       true,
		BlockAction:     false,
	}

	for kind, w := range want {
		if got := kind.Flattens(); got != w {
			t.Errorf("%s.Flattens() = %v, want %v", kind, got, w)
		}
	}
}

// TestUnit_BlockKind_WireDirectionsAreCheckedPerKind covers the rule that moved out of
// Attribute.validate in this phase.
//
// The flatten direction is universal, because every kind reads. The expand direction is
// required on a resource and refused on a data source, and putting that check in the
// kind-agnostic path made the first data source blueprint unrepresentable: its lookup
// argument is required, and a required attribute was assumed to need an expand.
func TestUnit_BlockKind_WireDirectionsAreCheckedPerKind(t *testing.T) {
	t.Parallel()

	expand := &ConvertCall{Func: "convert.FrameworkToPtrString"}
	flatten := &ConvertCall{Func: "convert.PtrStringToFramework"}

	tests := []struct {
		name     string
		kind     BlockKind
		attr     Attribute
		wantPath string
	}{
		{
			name: "a writable resource attribute needs an expand",
			kind: BlockResource,
			attr: Attribute{
				Name: "f", ComputedOptionalRequired: Required,
				Wire: WireBinding{Flatten: flatten},
			},
			wantPath: "attributes[f].wire.expand",
		},
		{
			name: "skipExpand on a writable resource attribute is refused",
			kind: BlockResource,
			attr: Attribute{
				Name: "f", ComputedOptionalRequired: Optional,
				Wire: WireBinding{Flatten: flatten, SkipExpand: true},
			},
			wantPath: "attributes[f].wire.skipExpand",
		},
		{
			name: "an expand on a data source attribute is refused",
			kind: BlockDataSource,
			attr: Attribute{
				Name: "f", ComputedOptionalRequired: Required,
				Wire: WireBinding{Expand: expand, Flatten: flatten},
			},
			wantPath: "attributes[f].wire.expand",
		},
		{
			name:     "every kind needs a flatten",
			kind:     BlockDataSource,
			attr:     Attribute{Name: "f", ComputedOptionalRequired: Computed},
			wantPath: "attributes[f].wire.flatten",
		},
		{
			name: "a resource's nested object needs an expand helper",
			kind: BlockResource,
			attr: Attribute{
				Name: "outer", ComputedOptionalRequired: Computed,
				Wire: WireBinding{Flatten: flatten},
				Type: AttrType{
					Kind: KindSetNested,
					NestedObject: &NestedAttributeObject{
						FlattenFunc: "flattenOuter",
						Attributes:  []Attribute{{Name: "id", Wire: WireBinding{Flatten: flatten}}},
					},
				},
			},
			wantPath: "attributes[outer].type.nested.expandFunc",
		},
		{
			name: "every kind's nested object needs a flatten helper",
			kind: BlockDataSource,
			attr: Attribute{
				Name: "outer", ComputedOptionalRequired: Computed,
				Wire: WireBinding{Flatten: flatten},
				Type: AttrType{
					Kind: KindSetNested,
					NestedObject: &NestedAttributeObject{
						Attributes: []Attribute{{Name: "id", Wire: WireBinding{Flatten: flatten}}},
					},
				},
			},
			wantPath: "attributes[outer].type.nested.flattenFunc",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var p problems
			tc.attr.validateForKind(tc.kind, "attributes["+tc.attr.Name+"]", &p)

			var paths []string
			for _, prob := range p {
				paths = append(paths, prob.path)
			}
			joined := strings.Join(paths, "; ")
			if !strings.Contains(joined, tc.wantPath) {
				t.Errorf("path %q not among %q", tc.wantPath, joined)
			}
		})
	}
}

// TestUnit_BlockKind_ReadOnlyAttributeNeedsNoExpand is the converse: the shape the pilot's
// data sources actually use must pass.
func TestUnit_BlockKind_ReadOnlyAttributeNeedsNoExpand(t *testing.T) {
	t.Parallel()

	flatten := &ConvertCall{Func: "convert.PtrStringToFramework"}

	// A required lookup argument with no expand, which is what a by-id data source is.
	lookup := Attribute{
		Name: "id", GoField: "ID", ComputedOptionalRequired: Required,
		Wire: WireBinding{Flatten: flatten},
	}

	var p problems
	lookup.validateForKind(BlockDataSource, "attributes[id]", &p)

	if len(p) != 0 {
		t.Errorf("a data source's required lookup argument needs no expand; got %v", p)
	}
}
