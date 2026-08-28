package emit

import (
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/fixtures"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }
func constraintDeclaration(attribute ir.Attribute) string {
	return declarationOf(schemaResource, attribute)
}

// TestUnit_ConstraintValidators_RenderTheDeclaredBounds proves each declared
// bound becomes the validator that enforces it at plan time, in the spelling
// its own package uses.
func TestUnit_ConstraintValidators_RenderTheDeclaredBounds(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		attribute ir.Attribute
		want      string
	}{
		{
			"a string with both length bounds",
			ir.Attribute{Name: "n", Kind: ir.TypeString, MinLength: i64(8), MaxLength: i64(64)},
			"stringvalidator.UTF8LengthBetween(8, 64)",
		},
		{
			"a string with only a minimum length",
			ir.Attribute{Name: "n", Kind: ir.TypeString, MinLength: i64(8)},
			"stringvalidator.UTF8LengthAtLeast(8)",
		},
		{
			"a string with only a maximum length",
			ir.Attribute{Name: "n", Kind: ir.TypeString, MaxLength: i64(64)},
			"stringvalidator.UTF8LengthAtMost(64)",
		},
		{
			"an integer range",
			ir.Attribute{Name: "n", Kind: ir.TypeInt64, Minimum: f64(1), Maximum: f64(10)},
			"int64validator.Between(1, 10)",
		},
		{
			"an integer with only a maximum",
			ir.Attribute{Name: "n", Kind: ir.TypeInt64, Maximum: f64(10)},
			"int64validator.AtMost(10)",
		},
		{
			"a float range keeps its fraction",
			ir.Attribute{Name: "n", Kind: ir.TypeFloat64, Minimum: f64(0.5), Maximum: f64(99.5)},
			"float64validator.Between(0.5, 99.5)",
		},
		{
			"a list size range",
			ir.Attribute{Name: "n", Kind: ir.TypeList, ElementType: ir.TypeString, MinItems: i64(1), MaxItems: i64(5)},
			"listvalidator.SizeBetween(1, 5)",
		},
		{
			"a map size floor",
			ir.Attribute{Name: "n", Kind: ir.TypeMap, ElementType: ir.TypeString, MinItems: i64(1)},
			"mapvalidator.SizeAtLeast(1)",
		},
		{
			"a pattern becomes a compiled match",
			ir.Attribute{Name: "n", Kind: ir.TypeString, Pattern: `^[a-z]+$`},
			"stringvalidator.RegexMatches(regexp.MustCompile(`^[a-z]+$`), \"must match ^[a-z]+$\")",
		},
	} {
		if declaration := constraintDeclaration(testCase.attribute); !strings.Contains(declaration, testCase.want) {
			t.Errorf("%s: does not carry %q:\n%s", testCase.name, testCase.want, declaration)
		}
	}
}

// TestUnit_ConstraintValidators_SkipAPatternRE2CannotCompile proves a
// lookahead — legal in ECMA-262, rejected by RE2 — yields no validator at
// all. Emitting it would panic the generated provider inside MustCompile at
// package initialisation, where go build cannot see it.
func TestUnit_ConstraintValidators_SkipAPatternRE2CannotCompile(t *testing.T) {
	declaration := constraintDeclaration(ir.Attribute{
		Name: "n", Kind: ir.TypeString, Pattern: `^(?=.*[A-Z]).{8,}$`,
	})
	if strings.Contains(declaration, "RegexMatches") {
		t.Errorf("a pattern RE2 cannot compile was emitted anyway:\n%s", declaration)
	}

	// The rest of the attribute still stands: one unrenderable pattern is a
	// fact about the pattern, not about the length bound beside it.
	both := constraintDeclaration(ir.Attribute{
		Name: "n", Kind: ir.TypeString, Pattern: `^(?=.*[A-Z]).{8,}$`, MaxLength: i64(64),
	})
	if !strings.Contains(both, "stringvalidator.UTF8LengthAtMost(64)") {
		t.Errorf("an unrenderable pattern took the length bound with it:\n%s", both)
	}
}

// TestUnit_ConstraintValidators_DropAFractionalBoundOnAnInteger proves a
// bound the attribute's type cannot hold is dropped rather than truncated,
// which would silently move the boundary.
func TestUnit_ConstraintValidators_DropAFractionalBoundOnAnInteger(t *testing.T) {
	declaration := constraintDeclaration(ir.Attribute{
		Name: "n", Kind: ir.TypeInt64, Minimum: f64(1.5), Maximum: f64(10),
	})
	if strings.Contains(declaration, "1.5") || strings.Contains(declaration, "Between") {
		t.Errorf("a fractional minimum survived onto an integer attribute:\n%s", declaration)
	}
	if !strings.Contains(declaration, "int64validator.AtMost(10)") {
		t.Errorf("the integral bound beside it was dropped too:\n%s", declaration)
	}
}

// TestUnit_ConstraintValidators_LeaveANestedObjectAlone proves a length or a
// range declared on an object is not applied to the object: it describes
// something the attribute does not hold. A size bound on a nested list still
// applies, because the list is the thing being sized.
func TestUnit_ConstraintValidators_LeaveANestedObjectAlone(t *testing.T) {
	nested := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "inner", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional},
	}}

	object := constraintDeclaration(ir.Attribute{
		Name: "n", Kind: ir.TypeObject, Nested: nested, MaxLength: i64(64), Maximum: f64(10),
	})
	if strings.Contains(object, "Validators:") {
		t.Errorf("a bound was applied to an object:\n%s", object)
	}

	list := constraintDeclaration(ir.Attribute{
		Name: "n", Kind: ir.TypeList, Nested: nested, MaxItems: i64(5),
	})
	if !strings.Contains(list, "listvalidator.SizeAtMost(5)") {
		t.Errorf("a nested list was not sized:\n%s", list)
	}
}

// TestUnit_ConstraintValidators_DeclareTheirOwnImports proves every rendered
// expression registered the package it names, so a validator can never reach
// a file whose import block forgot it.
func TestUnit_ConstraintValidators_DeclareTheirOwnImports(t *testing.T) {
	sb := &schemaBuilder{kind: schemaResource, imports: newImportSet("example.com/m")}
	sb.attributeDeclaration(node{attribute: ir.Attribute{
		Name: "n", Kind: ir.TypeString, MaxLength: i64(64), Pattern: `^[a-z]+$`,
	}}, 0)

	rendered := sb.imports.render()
	for _, want := range []string{
		"terraform-plugin-framework-validators/stringvalidator",
		`"regexp"`,
		"terraform-plugin-framework/schema/validator",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the import block does not declare %q:\n%s", want, rendered)
		}
	}
}

func TestUnit_Validators_AListOfEnumeratedStringsValidatesEachMember(t *testing.T) {
	sb := &schemaBuilder{kind: schemaResource}
	n := node{attribute: ir.Attribute{Name: "modules", Kind: ir.TypeList, ElementType: ir.TypeString, OneOf: []string{"default", "extended"}}}
	got := sb.validators(n, 1)
	if len(got) != 1 || got[0].SchemaDefinition != `listvalidator.ValueStringsAre(stringvalidator.OneOf("default", "extended"))` {
		t.Fatalf("validators = %+v, want the member validator", got)
	}
	if len(got[0].Imports) != 2 {
		t.Errorf("imports = %+v, want listvalidator and stringvalidator", got[0].Imports)
	}
}

func TestUnit_CheckLines_AddressMapsByKeyAndReferencesByPresence(t *testing.T) {
	spec := fixtures.Fixture{Entries: []fixtures.Entry{
		{Name: "template_id", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required, Expression: "petstore_template.template.id"},
		{Name: "labels", Kind: ir.TypeMap, ElementType: ir.TypeString, ComputedOptionalRequired: ir.Optional, Scalar: "one"},
		{Name: "tags", Kind: ir.TypeMap, ElementType: ir.TypeString, ComputedOptionalRequired: ir.Optional, Scalar: map[string]any{"b": "2", "a": "1"}},
	}}
	got := checkLines("petstore_thing.test", spec, fixtures.ConfigMaximal)
	for _, want := range []string{
		`resource.TestCheckResourceAttrSet("petstore_thing.test", "template_id")`,
		`resource.TestCheckResourceAttr("petstore_thing.test", "labels.labels", "one")`,
		`resource.TestCheckResourceAttr("petstore_thing.test", "tags.a", "1")`,
		`resource.TestCheckResourceAttr("petstore_thing.test", "tags.b", "2")`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("checks = %q, want %s", got, want)
		}
	}
	if strings.Contains(got, `"tags", `) || strings.Contains(got, `"labels", `) {
		t.Errorf("a map was checked whole: %q", got)
	}
}

func TestUnit_StateLines_ANormalisedStringKeepsTheConfiguredSpelling(t *testing.T) {
	nodes := []node{
		{attribute: ir.Attribute{Name: "server", Kind: ir.TypeString, Normalisation: "extended"},
			fb: &sdkbind.FieldBinding{Attr: "server", Wire: "server", Kind: ir.TypeString, Access: kiotaAccess("Server", "*string", "FromPtrString", "ToPtrString", "")}},
		{attribute: ir.Attribute{Name: "name", Kind: ir.TypeString},
			fb: &sdkbind.FieldBinding{Attr: "name", Wire: "name", Kind: ir.TypeString, Access: kiotaAccess("Name", "*string", "FromPtrString", "ToPtrString", "")}},
	}
	got, err := stateLines(nodes, "Thing", "remote", "data", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `data.Server = convert.Normalised(data.Server, convert.APIToFrameworkString(remote.GetServer()), "extended")`) {
		t.Errorf("state lines = %q, want the normalised read", got)
	}
	if !strings.Contains(got, "data.Name = convert.APIToFrameworkString(remote.GetName())") {
		t.Errorf("state lines = %q, want the plain read for the other attribute", got)
	}
}
