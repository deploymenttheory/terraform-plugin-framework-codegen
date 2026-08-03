// Package fixturespec derives test values for acceptance fixtures from a blueprint.
//
// It exists because two consumers need the same derivation and must never disagree:
// the render package formats these values as HCL for the generated fixtures, and the
// probe's rehearsal sends them as wire JSON to the live API before emit ever runs.
// When the two derive independently, the acceptance run becomes the first time the
// real body meets the API -- the discovery-at-acceptance failure mode this package
// removes.
//
// The package therefore produces typed values, not HCL: formatting is the render
// package's business, wire encoding is the probe's, and the choice of value is
// neither's.
package fixturespec

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// Source says where a derived value came from, in decreasing order of trust.
type Source string

const (
	// ObservedAccepted is a value the probe watched the API accept.
	ObservedAccepted Source = "observed-accepted"
	// ServerDefault is the value the API itself applied when the field was omitted.
	ServerDefault Source = "server-default"
	// Documented is a value the specification declares but nothing has exercised.
	Documented Source = "documented"
	// Synthesised is a value invented to be recognisable test debris.
	Synthesised Source = "synthesised"
	// Forced is the value the API substitutes whatever is sent. A fixture must carry
	// it: any other value plans a change the apply cannot deliver.
	Forced Source = "forced"
)

// Entry is one attribute's derived test value in wire-typed form, or the recorded
// reason there is none.
type Entry struct {
	// Value is the JSON-shaped native value: string, bool, int64, float64, []any or
	// map[string]any. Nil when Skipped.
	Value any
	// Verbatim is a literal recorded exactly as the evidence stated it -- a server
	// default's Raw. When set, an HCL renderer must emit it unchanged rather than
	// re-format Value, so the emitted fixture stays byte-identical to the evidence;
	// Value still carries the parsed wire form for a consumer that sends JSON.
	Verbatim string
	// Source says where the value came from. Unset when Skipped.
	Source Source
	// Note explains a value that is not obvious -- which is mostly "the probe proved
	// this one works".
	Note string
	// Skipped means no value could be derived. Reason says why.
	Skipped bool
	Reason  string
}

// Wants reports whether an attribute belongs in this fixture.
//
// A purely computed attribute never does: it cannot be set in configuration, so writing it
// would be invalid HCL rather than merely redundant.
//
// "Minimal" means the smallest configuration that *works*, not the smallest the schema permits.
// Those differ whenever the API enforces a field the specification does not declare, and the
// first live acceptance run is what proved it. The pilot's minimal fixture set only `key` and
// `object_type` -- the two the specification marks required -- and POST /tags answered:
//
//	value: value for the label cannot be null
//	accessType: You need to provide the accessType for the tag. It cannot be null or empty
//
// Both were already recorded as `requiredByApi: true` in the committed blueprint, corroborated,
// from a probe run months earlier. The evidence was gathered, stored, and not consulted.
//
// Note this reads Behaviour rather than promoting the attribute to Required in the schema.
// Promoting changes what a practitioner may write with no workaround, so merge deliberately
// refuses to do it automatically and records the observation in the description instead. A
// fixture has no such constraint: it is an input, and it either works or wastes a live run.
func Wants(a blueprint.Attribute, minimal bool) bool {
	if a.ComputedOptionalRequired == blueprint.Computed {
		return false
	}
	if minimal {
		return a.ComputedOptionalRequired == blueprint.Required || RequiredByAPI(a)
	}

	return true
}

// RequiredByAPI reports whether the prober watched the API reject a request omitting this field.
func RequiredByAPI(a blueprint.Attribute) bool {
	return a.Behaviour.RequiredByAPI != nil && *a.Behaviour.RequiredByAPI
}

// Immutable reports whether the prober corroborated that the API refuses in-place
// changes to this field.
func Immutable(a blueprint.Attribute) bool {
	return a.Behaviour.Immutable != nil && *a.Behaviour.Immutable
}

// Derive derives one attribute's test value.
//
// The value comes from what the API was *observed to accept* where that is known, and only
// otherwise from what the specification documents. That is the opposite of how a validator is
// built, deliberately, and the asymmetry is the point:
//
//   - A generated validator uses the documented set, so it errs toward *permitting*. Blocking a
//     configuration the API would have taken is a harm the practitioner cannot work around.
//   - A generated fixture uses the observed set, so it errs toward *working*. A fixture is not a
//     contract with anybody -- it is an input that either applies or wastes a test run.
//
// The pilot proves both halves matter: `access_type` documents "system" and the probe watched the
// API refuse it, and `object_type` documents "endpoint-agent" and the probe watched that refused
// too. A fixture built from the documented set would have picked a rejected value for one of them.
func Derive(a blueprint.Attribute, salt string) Entry {
	// A forced value beats everything: the API substitutes it whatever is sent, so a
	// fixture carrying any other value plans a change the apply cannot deliver -- the
	// exact inconsistent-result failure the classic-tests wave iterated on live.
	if fv := a.Behaviour.ForcedValue; fv != nil && fv.Raw != "" {
		return Entry{
			Value:    parseLiteral(fv.Raw),
			Verbatim: fv.Raw,
			Source:   Forced,
			Note:     "the API forces this value, so no configured value can take effect",
		}
	}

	if e, ok := enumEntry(a); ok {
		return e
	}

	if e, ok := serverDefaultEntry(a); ok {
		return e
	}

	// Refused rather than guessed, and this is the one refusal worth explaining at length,
	// because it looks like something a generator ought to manage.
	//
	// A nested object's members are frequently identifiers of objects that must already exist --
	// the pilot's `assignments` names a test or a dashboard by id. Nothing in the IR distinguishes
	// those from a self-contained object like `filters`, whose members are plain strings, because
	// the specification does not say. Synthesising an id would produce HCL that is valid, matches
	// the schema, applies cleanly, and then fails against the API complaining about an object that
	// does not exist -- which is worse than an omission somebody can see and fill in.
	//
	// IsNested rather than a list of kinds, so a nested kind added later is refused by default
	// instead of falling through to the "no fixture value" branch with a vaguer reason.
	if a.Type.Kind.IsNested() {
		return Entry{
			Skipped: true,
			Reason: "a nested object's members may be identifiers of objects that must " +
				"already exist, and nothing in the specification says whether they are",
		}
	}

	switch a.Type.Kind {
	case blueprint.KindString:
		return stringEntry(a, salt)

	case blueprint.KindBool:
		return Entry{Value: true, Source: Synthesised}

	case blueprint.KindInt32, blueprint.KindInt64:
		return Entry{Value: intWithinBounds(a.Type.Constraints), Source: Synthesised}

	case blueprint.KindFloat32, blueprint.KindFloat64, blueprint.KindNumber:
		return Entry{Value: floatWithinBounds(a.Type.Constraints), Source: Synthesised}

	case blueprint.KindList, blueprint.KindSet:
		return collectionEntry(a)

	case blueprint.KindMap:
		return mapEntry(a)

	default:
		return Entry{
			Skipped: true,
			Reason:  fmt.Sprintf("type kind %q has no fixture value", a.Type.Kind),
		}
	}
}

// enumEntry picks a value from a constrained set, preferring what was observed to work.
func enumEntry(a blueprint.Attribute) (Entry, bool) {
	if accepted := a.Behaviour.AcceptedValues; len(accepted) > 0 {
		note := "observed accepted"
		if rejected := a.Behaviour.RejectedValues; len(rejected) > 0 {
			// Worth saying at the call site: it explains why the fixture does not simply
			// use the first documented value, which is the obvious thing to expect.
			note = fmt.Sprintf(
				"observed accepted; the API refused %s", strings.Join(rejected, ", "),
			)
		}

		return Entry{Value: accepted[0], Source: ObservedAccepted, Note: note}, true
	}

	if allowed := a.Type.AllowedValues; len(allowed) > 0 {
		return Entry{Value: allowed[0], Source: Documented, Note: "documented; unprobed"}, true
	}

	return Entry{}, false
}

// serverDefaultEntry reuses the value the API itself applied when the field was omitted.
//
// The strongest evidence of a valid value there is: the API produced it, so it cannot be the wrong
// shape. That matters for a field whose format is real but undocumented -- the pilot's `colour` is
// a hex triplet with no `pattern` recorded anywhere, so a synthesised "tfacc-colour" would be
// rejected by an API that checks, and rejected for a reason no reader of the blueprint could
// predict.
//
// The cost is stated in the emitted comment rather than hidden: configuring a value equal to the
// server's default means an assertion on it cannot distinguish "we set this" from "the server
// filled it in". That is a real loss, and it is the lesser one -- a fixture that applies and
// asserts weakly still runs, and a fixture the API rejects tests nothing at all.
func serverDefaultEntry(a blueprint.Attribute) (Entry, bool) {
	sd := a.Behaviour.ServerDefault
	if sd == nil || sd.Raw == "" {
		return Entry{}, false
	}

	// An empty-string default is degenerate: the wire cannot distinguish it from
	// absence, so configuring it explicitly stores a value the read maps back to
	// null -- an inconsistent-result failure a live run demonstrated. Nothing is
	// configured where nothing is distinguishable.
	if sd.Raw == `""` {
		return Entry{}, false
	}

	// Only a scalar. A default recorded for a collection would need its own HCL shape, and no
	// probed default has ever been one.
	if a.Type.Kind.IsNested() || a.Type.Kind == blueprint.KindList ||
		a.Type.Kind == blueprint.KindSet || a.Type.Kind == blueprint.KindMap {
		return Entry{}, false
	}

	return Entry{
		Value:    parseLiteral(sd.Raw),
		Verbatim: sd.Raw,
		Source:   ServerDefault,
		Note:     "the API's own default, so a value it is known to accept",
	}, true
}

// parseLiteral turns an evidence literal into its wire-typed value.
//
// A server default's Raw is stored the way the evidence stated it -- `true`, `3600`,
// `"0"`. The HCL renderer emits Raw verbatim, so this parse cannot change what a
// fixture says; it only decides what a wire body sends. An unparseable literal falls
// back to the raw string, which is at least honest about what the evidence held.
func parseLiteral(raw string) any {
	if unquoted, err := strconv.Unquote(raw); err == nil && strings.HasPrefix(raw, `"`) {
		return unquoted
	}
	if b, err := strconv.ParseBool(raw); err == nil {
		return b
	}
	if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}

	return raw
}

// stringEntry derives a string, which is where the honest limits are.
func stringEntry(a blueprint.Attribute, salt string) Entry {
	c := a.Type.Constraints

	// A credential-shaped attribute gets no synthesised value. Not because the
	// synthetic string is a secret -- it is not -- but because it is shaped like one:
	// secret scanners flag it in every committed fixture, and a generated file that
	// trains reviewers to wave through credential-looking literals is worse than a
	// stated omission. A curated accFixture hint carries one deliberately if a test
	// genuinely needs it.
	if CredentialShaped(a.Name) {
		return Entry{
			Skipped: true,
			Reason: "it is credential-shaped, and a generated fixture must not invent " +
				"values that read as secrets; supply one through accFixture if the test needs it",
		}
	}

	// An _id-suffixed string is a reference to an object that must already exist --
	// the top-level scalar cousin of the nested-member rule, and a live run proved it:
	// a synthesised overrideProxyId is a 400 naming nothing. Curate one through
	// accFixture when a test genuinely has the referenced object.
	if strings.HasSuffix(a.Name, "_id") {
		return Entry{
			Skipped: true,
			Reason: "it references an object that must already exist, which a synthesised " +
				"value cannot name; supply one through accFixture if the test has the object",
		}
	}

	if c.Pattern != "" {
		// A value satisfying an arbitrary regular expression cannot be constructed by
		// working forwards from the pattern. Generating a plausible-looking string and
		// hoping is how a fixture starts failing for a reason nobody can see, so this is
		// reported instead.
		return Entry{
			Skipped: true,
			Reason: fmt.Sprintf(
				"its value must match %s, and a string satisfying an arbitrary pattern cannot be "+
					"derived from the pattern", c.Pattern,
			),
		}
	}

	// Prefixed so anything this leaves behind in a tenant is identifiable as test debris,
	// and suffixed with the attribute name so two attributes of one resource differ -- an
	// API that silently deduplicates would otherwise be invisible.
	value := "tfacc-" + strings.ReplaceAll(a.Name, "_", "-")
	if salt != "" {
		value = "tfacc-" + salt + "-" + strings.ReplaceAll(a.Name, "_", "-")
	}

	if c.MaxLength != nil && int64(len(value)) > *c.MaxLength {
		if *c.MaxLength <= 0 {
			return Entry{
				Skipped: true,
				Reason:  fmt.Sprintf("its maximum length is %d", *c.MaxLength),
			}
		}
		value = value[:*c.MaxLength]
	}
	if c.MinLength != nil && int64(len(value)) < *c.MinLength {
		value += strings.Repeat("x", int(*c.MinLength)-len(value))
	}

	return Entry{Value: value, Source: Synthesised}
}

// collectionEntry derives a one-element list or set of scalars.
//
// One element rather than none: an empty collection and an absent one are different states, and
// the interesting one to exercise is a collection that actually carries something.
func collectionEntry(a blueprint.Attribute) Entry {
	if a.Type.ElementType == nil {
		return Entry{Skipped: true, Reason: "it declares no element type"}
	}

	elem := Derive(blueprint.Attribute{
		Name:                     a.Name + "_element",
		ComputedOptionalRequired: blueprint.Optional,
		Type:                     *a.Type.ElementType,
	}, "")
	if elem.Skipped {
		return Entry{
			Skipped: true,
			Reason:  "its elements have no derivable value: " + elem.Reason,
		}
	}

	return Entry{Value: []any{elem.Value}, Source: Synthesised}
}

// mapEntry derives a one-entry map of scalars.
func mapEntry(a blueprint.Attribute) Entry {
	if a.Type.ElementType == nil {
		return Entry{Skipped: true, Reason: "it declares no element type"}
	}

	elem := Derive(blueprint.Attribute{
		Name:                     a.Name + "_value",
		ComputedOptionalRequired: blueprint.Optional,
		Type:                     *a.Type.ElementType,
	}, "")
	if elem.Skipped {
		return Entry{
			Skipped: true,
			Reason:  "its values have no derivable value: " + elem.Reason,
		}
	}

	return Entry{Value: map[string]any{"tfacc": elem.Value}, Source: Synthesised}
}

// intWithinBounds picks an integer the declared range actually permits.
func intWithinBounds(c blueprint.Constraints) int64 {
	const preferred = 1

	switch {
	case c.Minimum != nil && int64(*c.Minimum) > preferred:
		return int64(*c.Minimum)
	case c.Maximum != nil && int64(*c.Maximum) < preferred:
		return int64(*c.Maximum)
	default:
		return preferred
	}
}

// floatWithinBounds picks a float the declared range actually permits.
func floatWithinBounds(c blueprint.Constraints) float64 {
	const preferred = 1

	switch {
	case c.Minimum != nil && *c.Minimum > preferred:
		return *c.Minimum
	case c.Maximum != nil && *c.Maximum < preferred:
		return *c.Maximum
	default:
		return preferred
	}
}

// CredentialShaped reports whether an attribute name reads as a secret.
//
// Substring matching on the usual suspects. Over-matching is the safe direction: a
// skipped optional attribute is a stated line in the scaffold, while an invented
// "password" in a committed fixture is a secret-scanner finding on every wave.
func CredentialShaped(name string) bool {
	for _, marker := range []string{"password", "secret", "token", "credential", "api_key", "apikey", "certificate", "username"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}
