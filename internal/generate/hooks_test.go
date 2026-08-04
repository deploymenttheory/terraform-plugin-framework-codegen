package generate

import (
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// ruled returns a resource carrying the given cross-attribute rules, and the rendered result.
func ruled(t *testing.T, cvs ...blueprint.ConfigValidator) ([]string, *importSet) {
	t.Helper()

	imports := newImportSet()

	got, err := configValidators(
		blueprint.Resource{Key: "tag", ConfigValidators: cvs},
		imports,
	)
	if err != nil {
		t.Fatalf("configValidators: %v", err)
	}

	return got, imports
}

// TestUnit_Render_ConfigValidatorsBecomeResourcevalidatorCalls.
//
// One call per rule over path.MatchRoot expressions. MatchRoot rather than path.Root, which is
// the distinction worth pinning: resourcevalidator takes path *expressions*, and path.Root
// returns a path -- passing one does not compile, and the two names are a letter apart.
func TestUnit_Render_ConfigValidatorsBecomeResourcevalidatorCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind blueprint.ConfigValidatorKind
		want string
	}{
		{
			blueprint.ConfigConflicting,
			`resourcevalidator.Conflicting(path.MatchRoot("assignments"), path.MatchRoot("filters"))`,
		},
		{
			blueprint.ConfigAtLeastOneOf,
			`resourcevalidator.AtLeastOneOf(path.MatchRoot("assignments"), path.MatchRoot("filters"))`,
		},
		{
			blueprint.ConfigExactlyOneOf,
			`resourcevalidator.ExactlyOneOf(path.MatchRoot("assignments"), path.MatchRoot("filters"))`,
		},
		{
			blueprint.ConfigRequiredTogether,
			`resourcevalidator.RequiredTogether(path.MatchRoot("assignments"), ` +
				`path.MatchRoot("filters"))`,
		},
	}

	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()

			got, imports := ruled(t, blueprint.ConfigValidator{
				Kind:       tc.kind,
				Attributes: []string{"assignments", "filters"},
			})

			if len(got) != 1 {
				t.Fatalf("one rule should render one call, got %v", got)
			}
			if got[0] != tc.want {
				t.Errorf("got  %s\nwant %s", got[0], tc.want)
			}

			// Both packages, or the generated file does not compile.
			block := imports.render("example.com/provider")
			for _, want := range []string{
				`"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"`,
				`"github.com/hashicorp/terraform-plugin-framework/path"`,
			} {
				if !strings.Contains(block, want) {
					t.Errorf("missing import %s in:\n%s", want, block)
				}
			}
		})
	}
}

// TestUnit_Render_NoRulesRegistersNoImports.
//
// The imports are added once the rules are known to render, not up front. A resource declaring
// none would otherwise carry two unused imports, which in Go is a compile error rather than
// untidiness -- and every resource in the pilot but one declares none.
func TestUnit_Render_NoRulesRegistersNoImports(t *testing.T) {
	t.Parallel()

	got, imports := ruled(t)
	if got != nil {
		t.Errorf("no rules should render nothing, got %v", got)
	}

	if block := imports.render("example.com/provider"); strings.Contains(block, "resourcevalidator") {
		t.Errorf("resourcevalidator should not be imported by a resource with no rules:\n%s", block)
	}
}

// TestUnit_Render_MoreThanTwoAttributesAllGetPaths.
//
// AtLeastOneOf over three is real practice, and a rule that rendered only the first two would
// enforce something narrower than the blueprint declares while still compiling.
func TestUnit_Render_MoreThanTwoAttributesAllGetPaths(t *testing.T) {
	t.Parallel()

	got, _ := ruled(t, blueprint.ConfigValidator{
		Kind:       blueprint.ConfigAtLeastOneOf,
		Attributes: []string{"a", "b", "c"},
	})

	want := `resourcevalidator.AtLeastOneOf(path.MatchRoot("a"), path.MatchRoot("b"), ` +
		`path.MatchRoot("c"))`
	if got[0] != want {
		t.Errorf("got  %s\nwant %s", got[0], want)
	}
}

// TestUnit_Render_AnUnknownRuleIsRefusedRatherThanRendered.
//
// blueprint.Validate refuses this first, so reaching it means something bypassed validation --
// a hand-built view, or a rule kind added to the IR without a constructor to render it. The
// refusal is what keeps that from emitting a call to a resourcevalidator function that does
// not exist, which fails in the practitioner's build rather than in ours.
func TestUnit_Render_AnUnknownRuleIsRefusedRatherThanRendered(t *testing.T) {
	t.Parallel()

	_, err := configValidators(
		blueprint.Resource{Key: "tag", ConfigValidators: []blueprint.ConfigValidator{{
			Kind:       "mutuallyAgreeable",
			Attributes: []string{"a", "b"},
		}}},
		newImportSet(),
	)
	if err == nil {
		t.Fatal("an unknown rule should be refused")
	}

	var unsupported *ErrUnsupported
	if !errors.As(err, &unsupported) {
		t.Fatalf("want an ErrUnsupported, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "mutuallyAgreeable") {
		t.Errorf("the refusal should name the rule: %v", err)
	}
	if !strings.Contains(err.Error(), "tag") {
		t.Errorf("the refusal should name the resource: %v", err)
	}
}

// TestUnit_Render_EveryRuleKindTheIRAcceptsCanBeRendered.
//
// The two halves of one statement: blueprint.Validate lists the legal kinds, configValidatorFunc
// maps them to constructors. A kind accepted by one and missing from the other is a blueprint
// that validates and then fails to generate, which is the worst place for the two to disagree.
func TestUnit_Render_EveryRuleKindTheIRAcceptsCanBeRendered(t *testing.T) {
	t.Parallel()

	for _, k := range []blueprint.ConfigValidatorKind{
		blueprint.ConfigConflicting,
		blueprint.ConfigAtLeastOneOf,
		blueprint.ConfigExactlyOneOf,
		blueprint.ConfigRequiredTogether,
	} {
		if _, ok := configValidatorFunc[k]; !ok {
			t.Errorf("kind %q validates but has no constructor to render it", k)
		}
	}

	if len(configValidatorFunc) != 4 {
		t.Errorf(
			"configValidatorFunc has %d entries; if resourcevalidator gained a rule, accept it "+
				"in blueprint.Validate and cover it above",
			len(configValidatorFunc),
		)
	}
}
