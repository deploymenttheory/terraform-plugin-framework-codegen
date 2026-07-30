package blueprint

import (
	"reflect"
	"strings"
	"testing"
)

// withConfigValidator returns the valid fixture carrying one cross-attribute rule, letting a
// test deviate from it in exactly one way.
//
// The rule is over two attributes added here rather than over the fixture's own `id` and `key`.
// Neither of those would do: `id` is computed and `key` is required, and a rule over either
// degenerates -- which is a refusal this file tests for, so using them as the baseline would
// make every case pass for the wrong reason.
func withConfigValidator(mutate func(*ConfigValidator)) Blueprint {
	b := validBlueprint()

	// Cloned from `key` so the wire binding is a real one, then made settable-and-computed,
	// which is what the pilot's own two members are.
	for _, name := range []string{"assignments", "filters"} {
		a := b.Resources[0].Schema.Attributes[1]
		a.Name = name
		a.GoField = strings.ToUpper(name[:1]) + name[1:]
		a.ComputedOptionalRequired = ComputedOptional
		a.Wire.JSONPath = name
		a.Wire.SDKField = a.GoField

		b.Resources[0].Schema.Attributes = append(b.Resources[0].Schema.Attributes, a)
	}

	cv := &ConfigValidator{
		Kind:       ConfigConflicting,
		Attributes: []string{"assignments", "filters"},
	}
	if mutate != nil {
		mutate(cv)
	}

	b.Resources[0].ConfigValidators = []ConfigValidator{*cv}

	return b
}

// TestUnit_Blueprint_AConfigValidatorThatCouldNeverFireIsRefused.
//
// Every case here compiles. That is the whole reason to check them: resourcevalidator takes
// path expressions, and path.MatchRoot accepts any string, so a rule naming an attribute the
// resource does not have builds cleanly and then does nothing for the lifetime of the
// provider. A silent no-op validator is worse than none, because the blueprint claims a
// constraint that is not being enforced.
func TestUnit_Blueprint_AConfigValidatorThatCouldNeverFireIsRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*ConfigValidator)
		wantPath string
		wantMsg  string
	}{
		{
			name:     "no kind",
			mutate:   func(cv *ConfigValidator) { cv.Kind = "" },
			wantPath: "configValidators[0].kind",
			wantMsg:  "required",
		},
		{
			// resourcevalidator has four rules. A fifth would render a call to a function
			// that does not exist.
			name:     "an invented rule",
			mutate:   func(cv *ConfigValidator) { cv.Kind = "mutuallyAgreeable" },
			wantPath: "configValidators[0].kind",
			wantMsg:  "not a known cross-attribute rule",
		},
		{
			name:     "no attributes",
			mutate:   func(cv *ConfigValidator) { cv.Attributes = nil },
			wantPath: "configValidators[0].attributes",
			wantMsg:  "at least two attributes",
		},
		{
			// One attribute is a per-attribute validator declared in the wrong place, and
			// the rules behave surprisingly rather than obviously: ExactlyOneOf over a
			// single path is satisfied by that one path, i.e. it is Required spelled at
			// length.
			name:     "a single attribute",
			mutate:   func(cv *ConfigValidator) { cv.Attributes = []string{"assignments"} },
			wantPath: "configValidators[0].attributes",
			wantMsg:  "at least two attributes",
		},
		{
			name:     "an empty attribute name",
			mutate:   func(cv *ConfigValidator) { cv.Attributes = []string{"assignments", ""} },
			wantPath: "configValidators[0].attributes[1]",
			wantMsg:  "is empty",
		},
		{
			name:     "an attribute the resource does not declare",
			mutate:   func(cv *ConfigValidator) { cv.Attributes = []string{"assignments", "nonesuch"} },
			wantPath: "configValidators[0].attributes[1]",
			wantMsg:  "would never fire",
		},
		{
			// The same path twice: Conflicting would compare an attribute with itself,
			// which no configuration can fail.
			name:     "the same attribute twice",
			mutate:   func(cv *ConfigValidator) { cv.Attributes = []string{"assignments", "assignments"} },
			wantPath: "configValidators[0].attributes[1]",
			wantMsg:  "used more than once",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := withConfigValidator(tc.mutate).Validate()
			if err == nil {
				t.Fatal("expected the rule to be refused")
			}
			if !strings.Contains(err.Error(), tc.wantPath) {
				t.Errorf("error should name %q: %v", tc.wantPath, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error should explain %q: %v", tc.wantMsg, err)
			}
		})
	}

	// And every rule the framework does have is accepted, or the table above could be
	// passing because config validators are refused outright.
	for _, k := range []ConfigValidatorKind{
		ConfigConflicting, ConfigAtLeastOneOf, ConfigExactlyOneOf, ConfigRequiredTogether,
	} {
		b := withConfigValidator(func(cv *ConfigValidator) { cv.Kind = k })
		if err := b.Validate(); err != nil {
			t.Errorf("%s should be valid over two declared attributes: %v", k, err)
		}
	}
}

// TestUnit_Blueprint_HooksIsZeroTracksItsOwnFields.
//
// IsZero decides whether the hook files are scaffolded at all, and it is a struct comparison
// -- so a field added without a thought here would be invisible to it, and the scaffold would
// silently never appear. Reflection rather than a hand-listed set, so adding a field to Hooks
// cannot leave this passing.
func TestUnit_Blueprint_HooksIsZeroTracksItsOwnFields(t *testing.T) {
	t.Parallel()

	if !(Hooks{}).IsZero() {
		t.Error("a Hooks with nothing set is zero")
	}

	for _, tc := range []struct {
		name string
		h    Hooks
	}{
		{"modifyPlan", Hooks{ModifyPlan: true}},
		{"readBackPredicate", Hooks{ReadBackPredicate: true}},
		{"stateUpgrade", Hooks{StateUpgrade: true}},
	} {
		if tc.h.IsZero() {
			t.Errorf("a Hooks with %s set is not zero", tc.name)
		}
	}

	if got := reflect.TypeOf(Hooks{}).NumField(); got != 3 {
		t.Errorf("Hooks has %d fields but only 3 are covered above; cover the new one", got)
	}
}

// TestUnit_Blueprint_ARuleOverAnUnsettableOrAlwaysSetMemberIsRefused.
//
// These rules read *configuration*, not state, so what a member can hold in configuration
// decides whether the rule means anything. Both shapes here compile and both produce a rule
// that is not the one the blueprint states -- silently, which is why they are worth refusing
// rather than documenting.
func TestUnit_Blueprint_ARuleOverAnUnsettableOrAlwaysSetMemberIsRefused(t *testing.T) {
	t.Parallel()

	// setKind returns the fixture with one rule member at the given configurability, and the
	// rule itself of the given kind.
	setKind := func(k ConfigValidatorKind, cor ComputedOptionalRequired) Blueprint {
		b := withConfigValidator(func(cv *ConfigValidator) { cv.Kind = k })
		for i := range b.Resources[0].Schema.Attributes {
			if b.Resources[0].Schema.Attributes[i].Name == "assignments" {
				b.Resources[0].Schema.Attributes[i].ComputedOptionalRequired = cor
			}
		}

		return b
	}

	tests := []struct {
		name    string
		kind    ConfigValidatorKind
		cor     ComputedOptionalRequired
		wantMsg string
	}{
		{
			// Computed-only cannot be written at all, so its config value is always null and
			// the rule relates one fewer attribute than it names.
			name:    "a computed member of a conflicting rule",
			kind:    ConfigConflicting,
			cor:     Computed,
			wantMsg: "can never participate",
		},
		{
			name:    "a computed member of a required-together rule",
			kind:    ConfigRequiredTogether,
			cor:     Computed,
			wantMsg: "can never participate",
		},
		{
			// Always set, so the answer is decided before anybody writes a configuration.
			name:    "a required member of an at-least-one-of rule",
			kind:    ConfigAtLeastOneOf,
			cor:     Required,
			wantMsg: "would never fire",
		},
		{
			name:    "a required member of an exactly-one-of rule",
			kind:    ConfigExactlyOneOf,
			cor:     Required,
			wantMsg: "forbids all the others",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := setKind(tc.kind, tc.cor).Validate()
			if err == nil {
				t.Fatal("expected the rule to be refused")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error should explain %q: %v", tc.wantMsg, err)
			}
		})
	}

	// The two that stay meaningful over a required member, or the table above would be
	// refusing required members wholesale rather than the two rules it degenerates.
	for _, k := range []ConfigValidatorKind{ConfigConflicting, ConfigRequiredTogether} {
		if err := setKind(k, Required).Validate(); err != nil {
			t.Errorf(
				"%s over a required member is meaningful -- it forbids or requires the others: %v",
				k, err,
			)
		}
	}

	// And optional-and-computed, which is what the pilot uses, is accepted by all four.
	for _, k := range []ConfigValidatorKind{
		ConfigConflicting, ConfigAtLeastOneOf, ConfigExactlyOneOf, ConfigRequiredTogether,
	} {
		if err := setKind(k, ComputedOptional).Validate(); err != nil {
			t.Errorf("%s over an optional-and-computed member should be valid: %v", k, err)
		}
	}
}

// TestUnit_Blueprint_ASchemaVersionBumpNeedsSomethingToMigrateWith.
//
// The framework decides this, and the failure mode is why it is worth refusing rather than
// documenting. fwserver's UpgradeResourceState passes state through untouched when the stored
// version equals the schema's, and demands a ResourceWithUpgradeState when it does not. So a
// bumped version with no upgrader works perfectly for anyone creating the resource fresh and
// fails for everyone holding older state -- which is exactly who the bump is for, and nobody a
// green test suite covers.
func TestUnit_Blueprint_ASchemaVersionBumpNeedsSomethingToMigrateWith(t *testing.T) {
	t.Parallel()

	// versioned returns the fixture at the given schema version and hook setting.
	versioned := func(version int64, upgrade bool) Blueprint {
		b := validBlueprint()
		b.Resources[0].Schema.Version = version
		b.Resources[0].Hooks.StateUpgrade = upgrade

		return b
	}

	tests := []struct {
		name    string
		version int64
		upgrade bool
		wantMsg string
	}{
		{
			name:    "bumped with no upgrader",
			version: 2,
			upgrade: false,
			wantMsg: "nothing migrates state written by an earlier version",
		},
		{
			// The converse, and just as wrong: fwserver never calls UpgradeState when the
			// stored version already matches, so the scaffolded file is dead code that reads
			// as a migration somebody can rely on.
			name:    "an upgrader with no bump",
			version: 0,
			upgrade: true,
			wantMsg: "can never be called",
		},
		{
			name:    "a negative version",
			version: -1,
			upgrade: true,
			wantMsg: "cannot be negative",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := versioned(tc.version, tc.upgrade).Validate()
			if err == nil {
				t.Fatal("expected this combination to be refused")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error should explain %q: %v", tc.wantMsg, err)
			}
		})
	}

	// The two coherent combinations pass, or the table above could be refusing versions
	// outright rather than refusing the incoherent pairs.
	for _, ok := range []struct {
		name    string
		version int64
		upgrade bool
	}{
		{"version 0 and no upgrader, the default", 0, false},
		{"a bump with an upgrader", 3, true},
	} {
		if err := versioned(ok.version, ok.upgrade).Validate(); err != nil {
			t.Errorf("%s should be valid: %v", ok.name, err)
		}
	}
}
