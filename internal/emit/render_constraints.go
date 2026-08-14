// The plan-time enforcement of the bounds a document declares. An API that
// silently clamps or truncates an out-of-range value leaves state and config
// disagreeing with no error to explain it; a validator turns that into a plan
// failure naming the attribute.

package emit

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/code"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// validatorRoot is where the stock validator packages live.
const validatorPackageRoot = "github.com/hashicorp/terraform-plugin-framework-validators/"

// constraintValidators is every validator that follows from what the document
// declares about one attribute's value: its length, its size, its numeric
// range, and the pattern it must match.
//
// Nothing here registers an import; each expression carries the packages it
// references, and validatorLines is the single place those are honoured.
func constraintValidators(n node) []code.CustomValidator {
	kind := n.attr.Kind
	switch {
	case kind == ir.TypeList || kind == ir.TypeMap:
		// A size bound applies to the collection whether its elements are
		// scalars or objects, so this is deliberately not gated on Nested.
		return sizeValidators(n)
	case n.attr.Nested != nil:
		// An object declaring a length or a range is declaring it about
		// something this attribute does not hold.
		return nil
	case kind == ir.TypeString:
		return stringValidators(n)
	case kind == ir.TypeInt64 || kind == ir.TypeFloat64:
		return numericValidators(n)
	}
	return nil
}

// stringValidators are the length bounds and the pattern.
//
// Length is measured in characters rather than bytes: JSON Schema counts
// maxLength in characters, and the framework's LengthBetween counts bytes,
// which would refuse a valid value the moment it stopped being ASCII.
func stringValidators(n node) []code.CustomValidator {
	var out []code.CustomValidator

	if call, ok := boundCall("UTF8Length", "", n.attr.MinLength, n.attr.MaxLength); ok {
		out = append(out, stockValidator("stringvalidator", call))
	}
	if pattern := n.attr.Pattern; pattern != "" {
		if expression, ok := regexLiteral(pattern); ok {
			out = append(out, code.CustomValidator{
				Imports: []code.Import{
					{Path: validatorPackageRoot + "stringvalidator"},
					{Path: "regexp"},
				},
				SchemaDefinition: fmt.Sprintf("stringvalidator.RegexMatches(regexp.MustCompile(%s), %s)",
					expression, strconv.Quote("must match "+pattern)),
			})
		}
	}
	return out
}

// numericValidators is the declared range, rendered against the attribute's
// own type so the literal the validator takes is the one it compares with.
func numericValidators(n node) []code.CustomValidator {
	pkg := "float64validator"
	format := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	minimum, maximum := n.attr.Minimum, n.attr.Maximum

	if n.attr.Kind == ir.TypeInt64 {
		pkg = "int64validator"
		format = func(v float64) string { return strconv.FormatInt(int64(v), 10) }
		// A fractional bound on an integer attribute describes a value the
		// attribute cannot hold. Truncating it would silently move the
		// boundary, so the bound is dropped and the other one still stands.
		minimum, maximum = integralOnly(minimum), integralOnly(maximum)
	}

	var out []code.CustomValidator
	if call, ok := boundCall("", "", formatted(minimum, format), formatted(maximum, format)); ok {
		out = append(out, stockValidator(pkg, call))
	}
	return out
}

// sizeValidators is the declared member-count range of a list or a map.
func sizeValidators(n node) []code.CustomValidator {
	pkg := "listvalidator"
	if n.attr.Kind == ir.TypeMap {
		pkg = "mapvalidator"
	}
	if call, ok := boundCall("Size", "Size", n.attr.MinItems, n.attr.MaxItems); ok {
		return []code.CustomValidator{stockValidator(pkg, call)}
	}
	return nil
}

// boundCall renders the one call that states a pair of bounds: Between when
// both are declared, AtLeast or AtMost when one is. prefix names the family
// ("UTF8Length", "Size", or none for a plain range); betweenPrefix is the
// same for the two-sided spelling, because a size range is SizeBetween while
// a length range is UTF8LengthBetween and a numeric range is plain Between.
//
// The generic parameter is the literal's own type, so an int64 bound renders
// without a decimal point and a float64 bound keeps one.
func boundCall[T int64 | string](prefix, betweenPrefix string, minimum, maximum *T) (string, bool) {
	if betweenPrefix == "" {
		betweenPrefix = prefix
	}
	switch {
	case minimum != nil && maximum != nil:
		return fmt.Sprintf("%sBetween(%v, %v)", betweenPrefix, *minimum, *maximum), true
	case minimum != nil:
		return fmt.Sprintf("%sAtLeast(%v)", prefix, *minimum), true
	case maximum != nil:
		return fmt.Sprintf("%sAtMost(%v)", prefix, *maximum), true
	}
	return "", false
}

// stockValidator is one validator from a stock package, carrying that
// package as its only import.
func stockValidator(pkg, call string) code.CustomValidator {
	return code.CustomValidator{
		Imports:          []code.Import{{Path: validatorPackageRoot + pkg}},
		SchemaDefinition: pkg + "." + call,
	}
}

// integralOnly passes a bound through only when it names a whole number.
func integralOnly(bound *float64) *float64 {
	if bound == nil || *bound != float64(int64(*bound)) {
		return nil
	}
	return bound
}

// formatted renders a numeric bound as the literal its validator takes.
func formatted(bound *float64, format func(float64) string) *string {
	if bound == nil {
		return nil
	}
	literal := format(*bound)
	return &literal
}

// regexLiteral renders a declared pattern as a Go regexp literal, and reports
// whether Go can compile it at all.
//
// OpenAPI patterns are ECMA-262 and Go's regexp is RE2, which has no
// lookahead and no backreferences. An expression RE2 rejects would panic the
// generated provider inside MustCompile at package initialisation — before
// any test or plan runs, and where `go build` cannot see it — so one that
// does not compile here yields no validator at all.
func regexLiteral(pattern string) (string, bool) {
	if _, err := regexp.Compile(pattern); err != nil {
		return "", false
	}
	// A raw literal keeps a pattern's backslashes as written. It cannot hold
	// a backtick or a carriage return, and an interpreted literal can hold
	// either.
	if !strings.ContainsAny(pattern, "`\r") {
		return "`" + pattern + "`", true
	}
	return strconv.Quote(pattern), true
}
