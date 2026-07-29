package render

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// The framework's validator sub-packages, one per type.
const (
	pkgValidator       = "github.com/hashicorp/terraform-plugin-framework/schema/validator"
	pkgStringValidator = "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
)

// validatorsFor returns every validator an attribute carries: the ones the blueprint
// declares by hand, then the ones derived from the type's own constraints.
//
// Hand-authored first, so a person reading generated output meets the deliberate
// constraint before the mechanical one.
func validatorsFor(
	a blueprint.Attribute,
	imports *importSet,
) []blueprint.CustomCode {
	out := append([]blueprint.CustomCode(nil), a.Validators...)

	if v, ok := oneOfValidator(a, imports); ok {
		out = append(out, v)
	}

	return out
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
	var (
		lines []string
		line  = "//"
	)

	for _, word := range strings.Fields(text) {
		if len(line)+1+len(word) > commentWidth {
			lines = append(lines, line)
			line = "//"
		}
		line += " " + word
	}

	return strings.Join(append(lines, line), "\n")
}
