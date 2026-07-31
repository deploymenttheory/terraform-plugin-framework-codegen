package probe

import (
	"fmt"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// Scope is what a probe is allowed to touch, and it is what every probe-facing signature takes.
//
// # Why not Subject and Plan separately
//
// Cost and Exercise must agree about which fields a probe will act on. If Cost multiplied by
// len(WritableFields) while Exercise iterated the plan's narrowed candidates, the declared budget
// would be wrong by an order of magnitude and nothing would notice -- a run would be refused for
// a budget it does not spend, or granted one it was never meant to have.
//
// So the narrowing happens once, here, and both sides read the same precomputed sets. Passing the
// two inputs separately would put the narrowing rules in every probe, which is the same bug with
// more places to fix it.
//
// # Why not compute it in the runner
//
// Because then the rules exist twice: once where the budget is checked and once where the
// requests are issued. The day they disagree is the day a run either spends past its cap or
// refuses work it was authorised to do.
type Scope struct {
	// Subject is the resource, flattened to paths and JSON keys.
	Subject Subject

	// Plan is the operator-supplied knowledge a probe cannot discover.
	Plan Plan

	// Planned is false when no plan was supplied.
	//
	// It is what lets `probe -list` report the *unnarrowed* worst case honestly. Without it, a
	// catalogue listing with no plan would report a small number -- not because the run is
	// cheap but because nobody said which fields to exercise -- and an operator would budget
	// against a figure that means nothing.
	Planned bool

	// The precomputed sets. Unexported with accessors, so a probe cannot append to one and
	// thereby exercise a field the cost model never counted.
	sendable     []Field
	omitted      []Field
	immutable    []Field
	enums        []Field
	normalisable []Field
	influencers  []Field
	denied       map[string]bool
}

// NewScope narrows a subject by a plan.
//
// Validates the plan, because every set below is derived from it and a plan naming a field that
// does not exist would silently produce an empty set -- which reads exactly like "this API has no
// writable fields".
func NewScope(subj Subject, plan Plan) (Scope, error) {
	if err := plan.Validate(subj); err != nil {
		return Scope{}, err
	}

	// Planned turns on the presence of a fixture, not on a plan having been passed. A zero plan
	// -- or one carrying only a budget -- narrows nothing and cannot support a mutating probe, so
	// treating it as a plan would report the narrowed cost of an empty narrowing: zero, for every
	// mutating probe, which is the most misleading number this command could print.
	sc := Scope{Subject: subj, Plan: plan, Planned: len(plan.Fixtures) > 0}
	sc.compute()

	return sc, nil
}

// UnplannedScope is a scope with no plan.
//
// For `probe -list` and for the read-only tier, neither of which needs a fixture. Mutating probes
// are refused by the gate without a plan, so this cannot become a way to write without one.
func UnplannedScope(subj Subject) Scope {
	sc := Scope{Subject: subj}
	sc.compute()

	return sc
}

// compute derives every set once.
func (sc *Scope) compute() {
	sc.denied = make(map[string]bool, len(sc.Plan.Deny))
	for _, path := range sc.Plan.Deny {
		sc.denied[path] = true
	}

	setByFixture := sc.fixtureKeys()

	for _, f := range sc.Subject.Fields {
		if !f.Writable || sc.Denied(f.JSONPath) {
			continue
		}

		sc.sendable = append(sc.sendable, f)

		// A field the fixture does not set is what the server-default protocol observes. Only
		// meaningful with a fixture: with none, every field is "omitted" and the set says
		// nothing.
		if sc.Planned && !setByFixture[f.JSONPath] {
			sc.omitted = append(sc.omitted, f)
		}

		// Immutability needs two or more candidates, not one.
		//
		// Immutable=true requires Corroborated, which the catalogue defines as two fixtures and
		// two *distinct* values -- so a field with one alternative cannot reach the confidence
		// the fact requires, and probing it would only produce a note. Making the candidate
		// count the gate means the plan controls this field set precisely, with no new schema.
		if len(sc.Plan.Candidates[f.JSONPath]) >= 2 {
			sc.immutable = append(sc.immutable, f)
		}

		if len(f.AllowedValues) > 0 {
			sc.enums = append(sc.enums, f)
		}

		if f.Kind == blueprint.KindString && !strings.Contains(f.JSONPath, ".") {
			sc.normalisable = append(sc.normalisable, f)
		}
	}

	for _, path := range sc.Plan.DefaultInfluencers {
		if f, ok := sc.Subject.Field(path); ok {
			sc.influencers = append(sc.influencers, f)
		}
	}

	for _, set := range [][]Field{
		sc.sendable, sc.omitted, sc.immutable, sc.enums, sc.normalisable, sc.influencers,
	} {
		sortFields(set)
	}
}

// fixtureKeys is every JSON path any fixture sets.
//
// Across all fixtures rather than per fixture, deliberately: a field one fixture sets is a field
// the operator has told us a valid value for, so it is not a candidate for "what does the server
// do when this is omitted" even if another fixture happens to omit it.
func (sc Scope) fixtureKeys() map[string]bool {
	out := map[string]bool{}

	for _, f := range sc.Plan.Fixtures {
		for key := range f.Body {
			out[key] = true
		}
	}

	return out
}

func sortFields(fields []Field) {
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].JSONPath < fields[j].JSONPath })
}

// Denied reports whether a path is on the plan's deny list.
//
// Every probe that would have sent a denied field must emit a note rather than skipping it in
// silence. Silence reads as agreement with whatever the blueprint already claims, and the reason
// a field is denied -- it is writable and has consequences -- is exactly the reason its existing
// guess deserves scrutiny.
func (sc Scope) Denied(jsonPath string) bool { return sc.denied[jsonPath] }

// Sendable is every writable field a probe may send a value for.
func (sc Scope) Sendable() []Field { return sc.sendable }

// Omitted is every sendable field no fixture sets, which is what a server default is observed on.
func (sc Scope) Omitted() []Field { return sc.omitted }

// Immutable is every field with two or more candidate values, which is the immutability
// protocol's domain.
func (sc Scope) Immutable() []Field { return sc.immutable }

// Enums is every sendable field whose specification documents a value set.
func (sc Scope) Enums() []Field { return sc.enums }

// Normalisable is every sendable top-level string field.
func (sc Scope) Normalisable() []Field { return sc.normalisable }

// Influencers is the fields whose value might determine another field's default.
func (sc Scope) Influencers() []Field { return sc.influencers }

// Fixtures is the plan's fixtures.
func (sc Scope) Fixtures() []Fixture { return sc.Plan.Fixtures }

// Fixture returns one fixture by index, with its body copied.
//
// Copied because a probe mutates the body it sends -- omitting a key, substituting a value -- and
// a probe that mutated the plan's own map would silently change what every later probe sends. The
// resulting facts would be about a body nobody declared.
//
// The copy is deep. A fixture body may nest -- a dynamic tag carries a `filters` list of maps --
// and a top-level copy would share every nested map with the plan, so a probe reaching one level
// down would corrupt the plan just as surely as one mutating the top.
func (sc Scope) Fixture(i int) (Fixture, bool) {
	if i < 0 || i >= len(sc.Plan.Fixtures) {
		return Fixture{}, false
	}

	src := sc.Plan.Fixtures[i]

	body, _ := deepCopyValue(src.Body).(map[string]any)

	return Fixture{Name: src.Name, Body: body}, true
}

// deepCopyValue copies the JSON-shaped value graph a fixture body decodes to: maps, slices and
// scalars. Scalars are immutable and returned as-is; anything else JSON cannot produce is not in
// a fixture body, because fixtures arrive by json.Unmarshal.
func deepCopyValue(v any) any {
	switch tv := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(tv))
		for k, elem := range tv {
			out[k] = deepCopyValue(elem)
		}
		return out
	case []any:
		out := make([]any, len(tv))
		for i, elem := range tv {
			out[i] = deepCopyValue(elem)
		}
		return out
	default:
		return v
	}
}

// HostFixture returns the first fixture whose body sets the given path, falling back to
// fixture 0, with the body deep-copied exactly as Fixture's is.
//
// This is how a per-field probe reaches the branch its field lives on. Every such probe
// used to inject into fixture 0 unconditionally, so a field only the dynamic fixture sets
// -- matchType, filters -- was probed inside a static body: the API's refusal was then
// recorded as a fact about the field when it was a fact about the combination. First
// match rather than best match, deterministically: a cassette records exact bodies.
func (sc Scope) HostFixture(jsonPath string) (Fixture, bool) {
	// A nested path is carried by its top-level parent, so that is the key a fixture
	// body declares.
	head, _, _ := strings.Cut(jsonPath, ".")

	for i, f := range sc.Plan.Fixtures {
		if _, ok := f.Body[head]; ok {
			return sc.Fixture(i)
		}
	}

	return sc.Fixture(0)
}

// Omittable is the fixture keys the requiredness protocol may leave out.
//
// Every key the fixture sets except the name field. Omitting the name field is not an experiment
// this tool can run: an object created without the stamped prefix could not be found by the
// sweeper, so a create that omitted it would be refused before it was sent. A probe that reaches
// this must say so in a note rather than leave the field looking unexamined.
func (sc Scope) Omittable(fixture Fixture) []string {
	out := make([]string, 0, len(fixture.Body))

	for key := range fixture.Body {
		if key == sc.Subject.NameField {
			continue
		}

		out = append(out, key)
	}

	// Sorted, because map iteration order is randomised and a cassette is an ordered transcript:
	// an unsorted domain would make the same plan record a different request sequence every run.
	sort.Strings(out)

	return out
}

// Candidates returns the alternative values declared for a path.
func (sc Scope) Candidates(jsonPath string) []any {
	src := sc.Plan.Candidates[jsonPath]
	if len(src) == 0 {
		return nil
	}

	return append([]any(nil), src...)
}

// CanMutate reports whether mutating probes may run, and why not when they may not.
//
// Both halves matter: the subject has to be shaped so cleanup is possible, and a plan has to
// exist so there is a valid body to build on.
func (sc Scope) CanMutate() (bool, string) {
	if can, why := sc.Subject.CanMutate(); !can {
		return false, why
	}

	if !sc.Planned || len(sc.Plan.Fixtures) == 0 {
		return false, "no probe plan with a fixture was supplied, so there is no valid request " +
			"body for a mutating probe to build on"
	}

	return true, ""
}

// String describes the narrowing, for -list and for a report.
func (sc Scope) String() string {
	if !sc.Planned {
		return fmt.Sprintf("no plan, so costs are the unnarrowed worst case over %d writable "+
			"field(s)", len(sc.sendable))
	}

	return fmt.Sprintf("%d fixture(s), %d sendable, %d omitted by every fixture, "+
		"%d with candidates, %d documented enum(s), %d denied",
		len(sc.Plan.Fixtures), len(sc.sendable), len(sc.omitted),
		len(sc.immutable), len(sc.enums), len(sc.denied))
}
