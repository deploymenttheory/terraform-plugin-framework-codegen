package render

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }

// bounded returns a writable attribute of the given kind carrying the given constraints.
func bounded(kind blueprint.TypeKind, c blueprint.Constraints) blueprint.Attribute {
	return blueprint.Attribute{
		Name:                     "field",
		GoField:                  "Field",
		ComputedOptionalRequired: blueprint.Optional,
		Type:                     blueprint.AttrType{Kind: kind, Constraints: c},
	}
}

// rendered returns the validator expressions one attribute produces.
func rendered(a blueprint.Attribute) []string {
	got := validatorsFor(a, newImportSet(), newPatternVars())

	out := make([]string, 0, len(got))
	for _, v := range got {
		out = append(out, v.SchemaDefinition)
	}

	return out
}

// TestUnit_Render_ConstraintsBecomeTheirTypesValidators is the phase's central table.
//
// The committed pilot exercises none of these -- the ThousandEyes tag schemas declare no
// constraints at all, and across the whole specification only int64 minimum/maximum lands on a
// writable attribute. So without this table every path but one would ship unexercised, which is
// the reason this work was deferred until it could be verified rather than merely written.
func TestUnit_Render_ConstraintsBecomeTheirTypesValidators(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		attr blueprint.Attribute
		want []string
	}{
		{
			name: "a string pattern",
			attr: bounded(blueprint.KindString, blueprint.Constraints{Pattern: "^a+$"}),
			want: []string{`stringvalidator.RegexMatches(fieldPattern, "must match ^a+$")`},
		},
		{
			// Between rather than two validators, so one mistake reports one diagnostic.
			name: "both string length bounds",
			attr: bounded(blueprint.KindString, blueprint.Constraints{
				MinLength: i64(1), MaxLength: i64(63),
			}),
			want: []string{"stringvalidator.LengthBetween(1, 63)"},
		},
		{
			name: "a minimum length alone",
			attr: bounded(blueprint.KindString, blueprint.Constraints{MinLength: i64(1)}),
			want: []string{"stringvalidator.LengthAtLeast(1)"},
		},
		{
			name: "a maximum length alone",
			attr: bounded(blueprint.KindString, blueprint.Constraints{MaxLength: i64(2048)}),
			want: []string{"stringvalidator.LengthAtMost(2048)"},
		},
		{
			// JSON Schema states every bound as a number, so an integer attribute's bound
			// arrives as a float64. Emitting it as one would not compile against
			// int64validator.Between.
			name: "an integer range narrows the declared float",
			attr: bounded(blueprint.KindInt64, blueprint.Constraints{
				Minimum: f64(0), Maximum: f64(100),
			}),
			want: []string{"int64validator.Between(0, 100)"},
		},
		{
			name: "an int32 range",
			attr: bounded(blueprint.KindInt32, blueprint.Constraints{Minimum: f64(1)}),
			want: []string{"int32validator.AtLeast(1)"},
		},
		{
			// A whole number keeps its decimal point, so the literal's type is unambiguous
			// where it is passed to a float validator.
			name: "a float range keeps a float literal",
			attr: bounded(blueprint.KindFloat64, blueprint.Constraints{
				Minimum: f64(0), Maximum: f64(1e9),
			}),
			want: []string{"float64validator.Between(0.0, 1000000000.0)"},
		},
		{
			name: "a float32 upper bound",
			attr: bounded(blueprint.KindFloat32, blueprint.Constraints{Maximum: f64(1.5)}),
			want: []string{"float32validator.AtMost(1.5)"},
		},
		{
			name: "a set size",
			attr: bounded(blueprint.KindSet, blueprint.Constraints{
				MinItems: i64(1), MaxItems: i64(8),
			}),
			want: []string{"setvalidator.SizeBetween(1, 8)"},
		},
		{
			name: "a list size",
			attr: bounded(blueprint.KindList, blueprint.Constraints{MinItems: i64(2)}),
			want: []string{"listvalidator.SizeAtLeast(2)"},
		},
		{
			name: "a map size",
			attr: bounded(blueprint.KindMap, blueprint.Constraints{MaxItems: i64(4)}),
			want: []string{"mapvalidator.SizeAtMost(4)"},
		},
		{
			// A nested collection is a collection: its size bound is the container's.
			name: "a nested collection's size",
			attr: bounded(blueprint.KindSetNested, blueprint.Constraints{MaxItems: i64(3)}),
			want: []string{"setvalidator.SizeAtMost(3)"},
		},
		{
			name: "no constraints at all",
			attr: bounded(blueprint.KindString, blueprint.Constraints{}),
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := rendered(tc.attr)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("validator %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestUnit_Render_ElementConstraintsAreLiftedOntoTheCollection.
//
// The element type carries its own bounds, which is why constraints live on AttrType rather
// than on Attribute: a bound on a set's elements is not a bound on the set.
func TestUnit_Render_ElementConstraintsAreLiftedOntoTheCollection(t *testing.T) {
	t.Parallel()

	a := bounded(blueprint.KindSet, blueprint.Constraints{MinItems: i64(1)})
	a.Type.ElementType = &blueprint.AttrType{
		Kind: blueprint.KindString,
		Constraints: blueprint.Constraints{
			Pattern:   "^x$",
			MaxLength: i64(128),
		},
	}

	got := rendered(a)
	if len(got) != 2 {
		t.Fatalf("got %v, want the set's own bound plus one element wrapper", got)
	}

	if got[0] != "setvalidator.SizeAtLeast(1)" {
		t.Errorf("the collection's own bound = %q", got[0])
	}

	// One wrapper carrying both element bounds, not one wrapper each.
	want := `setvalidator.ValueStringsAre(stringvalidator.RegexMatches(fieldElementPattern, ` +
		`"must match ^x$"), stringvalidator.LengthAtMost(128))`
	if got[1] != want {
		t.Errorf("element wrapper = %q, want %q", got[1], want)
	}

	// A collection whose elements are unconstrained gets no wrapper at all.
	plain := bounded(blueprint.KindSet, blueprint.Constraints{MinItems: i64(1)})
	plain.Type.ElementType = &blueprint.AttrType{Kind: blueprint.KindString}
	if got := rendered(plain); len(got) != 1 {
		t.Errorf("an unconstrained element type needs no wrapper: %v", got)
	}
}

// TestUnit_Render_PatternVarsAreDeclaredAndUnique.
//
// RegexMatches takes a compiled *regexp.Regexp, so each pattern needs a package-level var --
// and two attributes named the same at different nesting depths must not claim the same name,
// or the generated package declares one identifier twice.
func TestUnit_Render_PatternVarsAreDeclaredAndUnique(t *testing.T) {
	t.Parallel()

	patterns := newPatternVars()
	imports := newImportSet()

	first := bounded(blueprint.KindString, blueprint.Constraints{Pattern: "^a$"})
	second := bounded(blueprint.KindString, blueprint.Constraints{Pattern: "^b$"})

	v1 := validatorsFor(first, imports, patterns)
	v2 := validatorsFor(second, imports, patterns)

	// Same attribute name, so the second must take a suffixed var.
	if strings.Contains(v2[0].SchemaDefinition, "(fieldPattern,") {
		t.Errorf("two patterns claimed the same var: %q", v2[0].SchemaDefinition)
	}
	if !strings.Contains(v1[0].SchemaDefinition, "(fieldPattern,") {
		t.Errorf("the first pattern should take the unsuffixed var: %q", v1[0].SchemaDefinition)
	}

	decls := patterns.Decls()
	if len(decls) != 2 {
		t.Fatalf("got %d declarations, want one per pattern: %v", len(decls), decls)
	}
	for _, want := range []string{
		`var fieldPattern = regexp.MustCompile("^a$")`,
		`var fieldPattern2 = regexp.MustCompile("^b$")`,
	} {
		var found bool
		for _, d := range decls {
			if d == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing declaration %q, got %v", want, decls)
		}
	}

	// And the regexp package is registered, or the declaration would not compile.
	if !strings.Contains(imports.render("irrelevant"), `"regexp"`) {
		t.Error("the regexp package should be registered when a pattern is generated")
	}
}

// TestUnit_Render_AComputedAttributeGetsNoConstraintValidator.
//
// A validator runs against configuration, so one on a purely computed attribute could never
// run. This is not hypothetical: the only two patterns the whole ThousandEyes specification
// declares on an ingestable resource sit on computed attributes.
func TestUnit_Render_AComputedAttributeGetsNoConstraintValidator(t *testing.T) {
	t.Parallel()

	a := bounded(blueprint.KindString, blueprint.Constraints{
		Pattern:   "^http-server$",
		MaxLength: i64(32),
	})
	a.ComputedOptionalRequired = blueprint.Computed

	if got := rendered(a); len(got) != 0 {
		t.Errorf("a computed attribute needs no constraint validator: %v", got)
	}

	// Optional-and-computed can be set, so it does get one.
	a.ComputedOptionalRequired = blueprint.ComputedOptional
	if got := rendered(a); len(got) != 2 {
		t.Errorf("an optional-and-computed attribute should get its validators: %v", got)
	}
}
