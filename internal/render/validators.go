package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"
)

// The framework's validator sub-packages, one per type.
const (
	pkgValidator     = "github.com/hashicorp/terraform-plugin-framework/schema/validator"
	pkgRegexp        = "regexp"
	validatorPkgRoot = "github.com/hashicorp/terraform-plugin-framework-validators/"
)

// pkgStringValidator is named because oneOfValidator reaches for it directly.
const pkgStringValidator = validatorPkgRoot + "stringvalidator"

// validatorPackage is the sub-package a kind's validators live in.
//
// The absence of an entry is a refusal, and one entry is deliberately missing: numbervalidator
// exists but carries only AtLeastOneOf, so an arbitrary-precision number has no range
// validator to generate. blueprint.Validate says so; this map agreeing keeps the two from
// drifting.
var validatorPackage = map[blueprint.TypeKind]string{
	blueprint.KindBool:    "boolvalidator",
	blueprint.KindString:  "stringvalidator",
	blueprint.KindInt32:   "int32validator",
	blueprint.KindInt64:   "int64validator",
	blueprint.KindFloat32: "float32validator",
	blueprint.KindFloat64: "float64validator",
	blueprint.KindList:    "listvalidator",
	blueprint.KindSet:     "setvalidator",
	blueprint.KindMap:     "mapvalidator",

	blueprint.KindListNested:   "listvalidator",
	blueprint.KindSetNested:    "setvalidator",
	blueprint.KindSingleNested: "objectvalidator",
}

// elementValidatorWrapper is the constructor that lifts an element validator onto a
// collection, e.g. listvalidator.ValueStringsAre.
var elementValidatorWrapper = map[blueprint.TypeKind]string{
	blueprint.KindString:  "ValueStringsAre",
	blueprint.KindInt32:   "ValueInt32sAre",
	blueprint.KindInt64:   "ValueInt64sAre",
	blueprint.KindFloat32: "ValueFloat32sAre",
	blueprint.KindFloat64: "ValueFloat64sAre",
}

// validatorsFor returns every validator an attribute carries: the ones the blueprint
// declares by hand, then the ones derived from the type's own constraints.
//
// Hand-authored first, so a person reading generated output meets the deliberate
// constraint before the mechanical one.
func validatorsFor(
	a blueprint.Attribute,
	imports *importSet,
	patterns *patternVars,
) []blueprint.CustomCode {
	out := append([]blueprint.CustomCode(nil), a.Validators...)

	if v, ok := oneOfValidator(a, imports); ok {
		out = append(out, v)
	}

	out = append(out, constraintValidators(a, imports, patterns)...)

	return out
}

// renderNaming spells generated identifiers. No StripPrefix: by render time the names come
// from the blueprint's own attribute names, which carry no specification namespace.
var renderNaming = naming.Options{}

// patternVars collects the package-level regexp vars a schema needs.
//
// RegexMatches takes a compiled *regexp.Regexp, so the pattern has to be compiled somewhere.
// A package-level var rather than regexp.MustCompile inline at the call site: Schema runs once
// per provider process, so the cost is not the point -- a named var is what the reference
// provider does, and it puts the expression somewhere a reader can find it rather than buried
// mid-literal.
type patternVars struct {
	taken map[string]bool
	decls []string
}

func newPatternVars() *patternVars {
	return &patternVars{taken: map[string]bool{}}
}

// Decls returns the finished var declarations, in the order they were claimed.
func (pv *patternVars) Decls() []string { return pv.decls }

// claim registers a pattern and returns the var name to reference it by.
func (pv *patternVars) claim(attrName, pattern string) string {
	name := naming.Unique(pv.taken, renderNaming.GoVarName(attrName, "pattern"))

	pv.decls = append(
		pv.decls,
		fmt.Sprintf("var %s = regexp.MustCompile(%s)", name, goStringLit(pattern)),
	)

	return name
}

// constraintValidators generates the validators a type's declared bounds imply.
//
// Built from the declared bounds and nothing else. Unlike allowed values there is no observed
// counterpart to weigh them against: the prober has no protocol for discovering a length limit
// or a numeric range, so there is no evidence that could suppress one.
func constraintValidators(
	a blueprint.Attribute,
	imports *importSet,
	patterns *patternVars,
) []blueprint.CustomCode {
	// A validator runs against configuration, so a purely computed attribute would never
	// exercise one -- the same reason oneOfValidator skips it.
	if a.ComputedOptionalRequired == blueprint.Computed {
		return nil
	}

	out := boundValidators(a.Name, a.Type, imports, patterns)

	// A collection's element bounds become a per-element wrapper. The element type carries its
	// own constraints, which is why they are modelled on the type rather than the attribute.
	if elem := a.Type.ElementType; elem != nil && !elem.Constraints.IsZero() {
		out = append(out, elementValidators(a.Name, a.Type.Kind, *elem, imports, patterns)...)
	}

	return out
}

// boundValidators renders the validators for one type's own bounds.
func boundValidators(
	attrName string,
	t blueprint.AttrType,
	imports *importSet,
	patterns *patternVars,
) []blueprint.CustomCode {
	pkg, ok := validatorPackage[t.Kind]
	if !ok || t.Constraints.IsZero() {
		return nil
	}

	c := t.Constraints

	var out []blueprint.CustomCode

	add := func(expr string) {
		imports.add(validatorPkgRoot+pkg, "")
		out = append(out, blueprint.CustomCode{SchemaDefinition: pkg + "." + expr})
	}

	if c.Pattern != "" {
		imports.add(pkgRegexp, "")
		// The message is the pattern itself. A generated message cannot explain intent, and
		// showing the expression at least tells the practitioner what was checked.
		add(fmt.Sprintf("RegexMatches(%s, %s)",
			patterns.claim(attrName, c.Pattern),
			goStringLit("must match "+c.Pattern)))
	}

	if e, ok := boundPair("Length", c.MinLength, c.MaxLength, formatInt); ok {
		add(e)
	}
	if e, ok := boundPair("Size", c.MinItems, c.MaxItems, formatInt); ok {
		add(e)
	}
	if e, ok := numericBound(t.Kind, c.Minimum, c.Maximum); ok {
		add(e)
	}

	return out
}

// elementValidators lifts an element type's bounds onto its collection.
func elementValidators(
	attrName string,
	container blueprint.TypeKind,
	elem blueprint.AttrType,
	imports *importSet,
	patterns *patternVars,
) []blueprint.CustomCode {
	pkg, ok := validatorPackage[container]
	if !ok {
		return nil
	}

	wrapper, ok := elementValidatorWrapper[elem.Kind]
	if !ok {
		return nil
	}

	inner := boundValidators(attrName+"_element", elem, imports, patterns)
	if len(inner) == 0 {
		return nil
	}

	exprs := make([]string, 0, len(inner))
	for _, v := range inner {
		exprs = append(exprs, v.SchemaDefinition)
	}

	imports.add(validatorPkgRoot+pkg, "")

	return []blueprint.CustomCode{{
		SchemaDefinition: fmt.Sprintf("%s.%s(%s)", pkg, wrapper, strings.Join(exprs, ", ")),
	}}
}

// boundPair renders the AtLeast/AtMost/Between form for one pair of bounds.
//
// Between when both are present, because the framework provides it and two separate validators
// would report two diagnostics for one mistake.
func boundPair[T any](prefix string, lo, hi *T, format func(T) string) (string, bool) {
	switch {
	case lo != nil && hi != nil:
		return fmt.Sprintf("%sBetween(%s, %s)", prefix, format(*lo), format(*hi)), true
	case lo != nil:
		return fmt.Sprintf("%sAtLeast(%s)", prefix, format(*lo)), true
	case hi != nil:
		return fmt.Sprintf("%sAtMost(%s)", prefix, format(*hi)), true
	default:
		return "", false
	}
}

// numericBound renders a range validator, narrowing the declared float64 to the kind's own
// literal form.
//
// JSON Schema expresses every bound as a number, so an int64 attribute's minimum arrives as a
// float64. Emitting it as one would not compile against int64validator.AtLeast.
func numericBound(kind blueprint.TypeKind, lo, hi *float64) (string, bool) {
	switch kind {
	case blueprint.KindInt32, blueprint.KindInt64:
		return boundPair("", asInt(lo), asInt(hi), formatInt)
	case blueprint.KindFloat32, blueprint.KindFloat64:
		return boundPair("", lo, hi, formatFloat)
	default:
		return "", false
	}
}

// asInt narrows a declared bound to an integer, for an integer-kinded attribute.
func asInt(v *float64) *int64 {
	if v == nil {
		return nil
	}
	n := int64(*v)

	return &n
}

func formatInt(v int64) string { return strconv.FormatInt(v, 10) }

// formatFloat keeps a whole number looking like a float, so the literal's type is unambiguous
// where it is passed to a float validator.
func formatFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}

	return s
}

// oneOfValidator builds a OneOf over the documented value set, or reports that none
// should be generated.
//
// Generated from AttrType.AllowedValues -- the *documented* set -- and not from
// Behaviour.AcceptedValues, which reads backwards until you consider which way each errs.
// The documented set is a superset of what any single tenant accepts, so a validator built
// from it errs toward permitting: a stale specification then surfaces as a real API error
// carrying the API's own message, which a practitioner can act on. Built from the observed
// set it would err toward blocking, and the pilot is the case in point -- accessType
// documents "system", this sandbox refused it, and another licence may well allow it.
func oneOfValidator(a blueprint.Attribute, imports *importSet) (blueprint.CustomCode, bool) {
	if len(a.Type.AllowedValues) == 0 {
		return blueprint.CustomCode{}, false
	}

	// Only a plain string. A collection whose elements are constrained needs the
	// element-level wrapper, which is a separate shape rather than this one applied to a
	// list.
	if a.Type.Kind != blueprint.KindString {
		return blueprint.CustomCode{}, false
	}

	// A validator runs against configuration, and a purely computed attribute is never
	// configured, so one here would be dead code. Optional-and-computed still gets it:
	// that attribute can be set.
	if a.ComputedOptionalRequired == blueprint.Computed {
		return blueprint.CustomCode{}, false
	}

	// The one case with direct evidence that generating this would be harmful: the prober
	// sent values from outside the documented set and the API took them, so a OneOf would
	// reject configurations this API demonstrably accepts. Absence of the observation is
	// not the same as observing openness, hence the nil check -- an unprobed attribute
	// still gets its validator.
	if c := a.Behaviour.ValuesClosed; c != nil && !*c {
		return blueprint.CustomCode{}, false
	}

	quoted := make([]string, 0, len(a.Type.AllowedValues))
	for _, v := range a.Type.AllowedValues {
		quoted = append(quoted, goStringLit(v))
	}

	imports.add(pkgStringValidator, "")

	return blueprint.CustomCode{
		SchemaDefinition: fmt.Sprintf("stringvalidator.OneOf(%s)", strings.Join(quoted, ", ")),
	}, true
}

// validatorNote is the comment written above a generated validator, or empty.
//
// It exists for one case: the prober sent a documented value and the API refused it. The
// validator still permits that value, deliberately -- the refusal may be licence-gated
// rather than permanent -- but somebody reading the schema should meet the evidence that
// the specification is stale rather than have to go looking for it.
func validatorNote(a blueprint.Attribute) string {
	rejected := a.Behaviour.RejectedValues
	if len(rejected) == 0 || len(a.Type.AllowedValues) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(rejected))
	for _, v := range rejected {
		quoted = append(quoted, goStringLit(v))
	}

	return wrapComment(fmt.Sprintf(
		"The API refused %s from the documented set when probed. Still permitted here: a value "+
			"one tenant rejects may be licence-gated rather than nonexistent.",
		strings.Join(quoted, ", "),
	))
}

// commentWidth is where a generated comment wraps, including the "// ".
//
// gofumpt does not reflow comments, so an unwrapped one lands in the output at whatever
// length it happened to be -- and these embed a value set, so they get long.
const commentWidth = 96

// wrapComment turns prose into "// "-prefixed lines no wider than commentWidth.
func wrapComment(text string) string {
	return wrapCommentPrefix(text, "//")
}

// wrapCommentPrefix is wrapComment for a comment syntax other than Go's.
//
// The prefix carries the indent as well as the marker, so an HCL comment inside a block passes
// "  #" and the continuation lines line up under the first. Needed because `terraform fmt` does
// not reflow comments either, so the same unwrapped-prose problem applies to emitted HCL.
func wrapCommentPrefix(text, prefix string) string {
	var (
		lines []string
		line  = prefix
	)

	for _, word := range strings.Fields(text) {
		if len(line)+1+len(word) > commentWidth {
			lines = append(lines, line)
			line = prefix
		}
		line += " " + word
	}

	return strings.Join(append(lines, line), "\n")
}
