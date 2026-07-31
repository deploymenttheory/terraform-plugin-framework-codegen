package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// fixtureValue is one HCL assignment, or a recorded reason there is none.
type fixtureValue struct {
	// Name is the attribute as it appears in HCL.
	Name string
	// HCL is the rendered value, e.g. `"test"` or `true`. Empty when Skipped.
	HCL string
	// Note explains a value that is not obvious -- which is mostly "the probe proved
	// this one works" -- and is rendered as a trailing comment.
	Note string
	// Padding right-pads Name so the "=" signs align, as terraform fmt would.
	Padding string
	// Comment is the wrapped, "#"-prefixed explanation of this entry, ready to emit.
	//
	// Wrapped here rather than in the template because terraform fmt does not reflow comments,
	// so an over-long line would sit in the output at whatever length the prose happened to be.
	Comment string
	// Skipped means no value could be derived. Reason says why.
	Skipped bool
	Reason  string
	// Curated means the value came from the blueprint's accFixture, verbatim. It is
	// what routes the acceptance assertion to "is set" rather than an equality the
	// generator cannot know: a curated expression may be a reference whose value
	// exists only at apply time.
	Curated bool
}

// FixtureView is what an HCL fixture template needs.
type FixtureView struct {
	Header string
	// TerraformType is the full type, e.g. "thousandeyes_tag".
	TerraformType string
	// Label is the resource label in HCL, always "test" so a generated acceptance test
	// can reference the address without being told it.
	Label string
	// Values are the assignments to emit, in schema order.
	Values []fixtureValue
	// Notes are the values whose provenance is worth stating, rendered as a comment block
	// rather than as trailing comments.
	//
	// Trailing comments were the obvious shape and are the wrong one: `terraform fmt` aligns
	// them across runs of consecutive lines, with its own rules about what breaks a run, so
	// emitting them would mean reimplementing that algorithm exactly or emitting output the
	// canonical formatter rewrites. Neither is worth it for a comment.
	Notes []fixtureValue
	// Skipped are the attributes that could not be derived, named so a practitioner
	// knows what to add rather than discovering it from an API error.
	Skipped []fixtureValue
	// DataBlocks are curated HCL blocks emitted verbatim above the resource block --
	// the declared dependencies of curated values that reference live tenant data.
	DataBlocks []string
}

// The label every fixture uses. Fixed rather than configurable: the generated acceptance
// test builds the resource address from it, and two places choosing independently is two
// places to disagree.
const fixtureLabel = "test"

// Fixture builds an HCL acceptance fixture for one resource.
//
// minimal selects the attributes: required only, or every writable one.
//
// Only the minimal fixture carries a generated header. The maximal one is a scaffold -- written
// once and then owned -- and the marker is what the drift check and the overwrite refusal key on,
// so a file meant to be edited must not carry it.
func Fixture(
	bp blueprint.Blueprint,
	r blueprint.Resource,
	opts Options,
	minimal bool,
) (FixtureView, error) {
	var header string
	if minimal {
		header = GeneratedHeaderHCL(opts.BlueprintPath, opts.BlueprintSHA256)
	}

	return fixtureView(bp, r, header, minimal, "")
}

// SeedFixture is a resource's minimal fixture with the consumer's key salted into every
// synthesised string value.
//
// The first live acceptance run is why this exists: four generated tests seeded the same
// resource with byte-identical synthesised values, the packages ran concurrently against
// one tenant, and three of the four creates answered 409 Duplicate. The salt is the
// consuming block's, deterministic, so each test's seed is its own object and the exact-
// value assertions still hold; values the API itself vouched for -- enums, server
// defaults -- are never salted, because uniqueness is not worth an invalid body.
func SeedFixture(
	bp blueprint.Blueprint,
	r blueprint.Resource,
	opts Options,
	salt string,
) (FixtureView, error) {
	return fixtureView(bp, r, "", true, salt)
}

// fixtureView builds a fixture for one resource.
func fixtureView(
	bp blueprint.Blueprint,
	r blueprint.Resource,
	header string,
	minimal bool,
	salt string,
) (FixtureView, error) {
	v := FixtureView{
		Header:        header,
		TerraformType: bp.Provider.TerraformType(r.Name),
		Label:         fixtureLabel,
	}

	if r.AccFixture != nil {
		v.DataBlocks = append(v.DataBlocks, r.AccFixture.DataBlocks...)
	}

	for _, a := range r.Schema.Attributes {
		if a.Drop || !fixtureWants(a, minimal) {
			continue
		}

		// An optional immutable attribute stays out of the maximal fixture. The maximal
		// configuration drives the update step, and introducing an immutable value there
		// would force a replacement in the middle of a test asserting in-place update --
		// the step would pass while silently testing something else. Skipped with the
		// reason stated, so the scaffold's owner can move it into the create configuration
		// deliberately.
		if !minimal && attrImmutable(a) && !fixtureWants(a, true) {
			v.Skipped = append(v.Skipped, fixtureValue{
				Name: a.Name,
				Comment: wrapCommentPrefix(a.Name+": immutable; setting it at update would "+
					"force replacement, so it belongs in the create configuration or nowhere",
					"  #"),
			})

			continue
		}

		// A curated value beats derivation and is emitted exactly as written -- never
		// salted, because salting exists to de-collide synthesised strings and this one
		// was stated by a person. It is what carries an attribute the generator refuses
		// to synthesise (a nested object of live identifiers) past the required-attribute
		// refusal below.
		if hcl, ok := r.AccFixture.Hint(a.Name); ok {
			fv := fixtureValue{
				Name:    a.Name,
				HCL:     hcl,
				Note:    "curated in the blueprint's accFixture; the generator cannot derive it",
				Curated: true,
			}
			fv.Comment = wrapCommentPrefix(fv.Name+": "+fv.Note, "  #")
			v.Values = append(v.Values, fv)
			v.Notes = append(v.Notes, fv)

			continue
		}

		fv := fixtureValueFor(a, salt)

		switch {
		case !fv.Skipped:
			v.Values = append(v.Values, fv)
			if fv.Note != "" {
				fv.Comment = wrapCommentPrefix(fv.Name+": "+fv.Note, "  #")
				v.Notes = append(v.Notes, fv)
			}
		case a.ComputedOptionalRequired == blueprint.Required:
			// A required attribute with no derivable value makes the whole fixture
			// unusable, so it is refused by name rather than emitted incomplete. A
			// generated file that cannot apply is worse than no generated file: it
			// fails in somebody's acceptance run instead of here.
			return FixtureView{}, &ErrUnsupported{
				What: fmt.Sprintf("acceptance fixture for resource %q", r.Key),
				Why: fmt.Sprintf(
					"required attribute %q has no derivable test value: %s", a.Name, fv.Reason,
				),
			}
		default:
			fv.Comment = wrapCommentPrefix(fv.Name+": "+fv.Reason, "  #")
			v.Skipped = append(v.Skipped, fv)
		}
	}

	if len(v.Values) == 0 {
		return FixtureView{}, &ErrUnsupported{
			What: fmt.Sprintf("acceptance fixture for resource %q", r.Key),
			Why:  "no attribute has a derivable test value, so the fixture would be empty",
		}
	}

	alignNames(v.Values)

	return v, nil
}

// alignNames pads each name so the "=" signs line up.
//
// Not cosmetic. `terraform fmt` aligns consecutive assignments within a block, so unaligned output
// is output that `terraform fmt` rewrites -- and a generated file a formatter rewrites is a file
// that shows up as drift with no source change, which is the thing the whole drift check exists to
// make meaningful. The generator produces what the canonical formatter would, for the same reason
// emitted Go goes through gofumpt rather than gofmt.
func alignNames(values []fixtureValue) {
	var widest int
	for _, v := range values {
		if len(v.Name) > widest {
			widest = len(v.Name)
		}
	}

	for i := range values {
		values[i].Padding = strings.Repeat(" ", widest-len(values[i].Name))
	}
}

// fixtureWants reports whether an attribute belongs in this fixture.
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
// from a probe run months earlier. The evidence was gathered, stored, and not consulted -- the
// same shape as ReadBack in 5.5, the never-returned flatten in 6.3 and Schema.Version in 6.4.
//
// Note this reads Behaviour rather than promoting the attribute to Required in the schema.
// Promoting changes what a practitioner may write with no workaround, so merge deliberately
// refuses to do it automatically and records the observation in the description instead. A
// fixture has no such constraint: it is an input, and it either works or wastes a live run.
func fixtureWants(a blueprint.Attribute, minimal bool) bool {
	if a.ComputedOptionalRequired == blueprint.Computed {
		return false
	}
	if minimal {
		return a.ComputedOptionalRequired == blueprint.Required || requiredByAPI(a)
	}

	return true
}

// requiredByAPI reports whether the prober watched the API reject a request omitting this field.
func requiredByAPI(a blueprint.Attribute) bool {
	return a.Behaviour.RequiredByAPI != nil && *a.Behaviour.RequiredByAPI
}

// attrImmutable reports whether the prober corroborated that the API refuses in-place
// changes to this field.
func attrImmutable(a blueprint.Attribute) bool {
	return a.Behaviour.Immutable != nil && *a.Behaviour.Immutable
}

// ReplaceFixture builds the configuration for the acceptance step that proves the
// generated RequiresReplace live. Ok is false, with no error, when nothing can be
// flipped -- see replaceFixture.
func ReplaceFixture(
	bp blueprint.Blueprint,
	r blueprint.Resource,
	opts Options,
) (FixtureView, bool, error) {
	minimal, err := Fixture(bp, r, opts, true)
	if err != nil {
		return FixtureView{}, false, err
	}

	v, _, ok := replaceFixture(r, minimal)
	if !ok {
		return FixtureView{}, false, nil
	}

	// Generated and policed like the minimal fixture: the flipped value is derived from
	// evidence, so a human editing it should edit the blueprint instead.
	v.Header = GeneratedHeaderHCL(opts.BlueprintPath, opts.BlueprintSHA256)

	return v, true, nil
}

// replaceFixture is the minimal fixture with one immutable attribute flipped to a second
// value the API takes, for the acceptance step that proves the generated RequiresReplace
// live: the plan must say "forces replacement" and the apply must succeed by replacing.
//
// Not ok when no immutable attribute in the fixture has a second usable value -- a set of
// one can be created but never changed, so there is nothing to flip and no step to run.
// Strings only: the candidate pools are value sets, which the IR records for strings.
func replaceFixture(
	r blueprint.Resource,
	minimal FixtureView,
) (FixtureView, string, bool) {
	inFixture := map[string]int{}
	for i, fv := range minimal.Values {
		inFixture[fv.Name] = i
	}

	for _, a := range r.Schema.Attributes {
		idx, present := inFixture[a.Name]
		if a.Drop || !present || !attrImmutable(a) || a.Type.Kind != blueprint.KindString {
			continue
		}

		current := minimal.Values[idx].HCL
		next, ok := secondValue(a, current)
		if !ok {
			continue
		}

		replaced := minimal
		replaced.Header = "" // a distinct file, headered by its own emit entry
		replaced.Values = append([]fixtureValue(nil), minimal.Values...)
		replaced.Values[idx].HCL = next
		replaced.Values[idx].Note = "flipped from the create configuration; the API refuses " +
			"in-place changes here, so this must plan as a replacement"
		replaced.Values[idx].Comment = wrapCommentPrefix(
			replaced.Values[idx].Name+": "+replaced.Values[idx].Note, "  #")

		// The notes block is copied, not shared: the minimal fixture's own view must not
		// see the flip, and the flip must be stated where the other provenance notes are.
		replaced.Notes = append([]fixtureValue(nil), minimal.Notes...)
		replaced.Notes = replaceNote(replaced.Notes, replaced.Values[idx])

		return replaced, a.Name, true
	}

	return FixtureView{}, "", false
}

// replaceNote swaps the flipped attribute's provenance note in, appending when the
// minimal fixture carried none for it.
func replaceNote(notes []fixtureValue, flipped fixtureValue) []fixtureValue {
	for i, n := range notes {
		if n.Name == flipped.Name {
			notes[i] = flipped
			return notes
		}
	}
	return append(notes, flipped)
}

// secondValue picks a different value the API takes for an attribute, preferring what was
// observed to work over what is merely documented -- the same asymmetry the fixture's own
// value derivation uses, for the same reason: this value has one job, to apply.
func secondValue(a blueprint.Attribute, current string) (string, bool) {
	rejected := map[string]bool{}
	for _, v := range a.Behaviour.RejectedValues {
		rejected[v] = true
	}

	pools := [][]string{a.Behaviour.AcceptedValues, a.Type.AllowedValues}
	for _, pool := range pools {
		for _, candidate := range pool {
			quoted := strconv.Quote(candidate)
			if quoted != current && !rejected[candidate] {
				return quoted, true
			}
		}
	}

	return "", false
}

// fixtureValueFor derives one attribute's test value.
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
func fixtureValueFor(a blueprint.Attribute, salt string) fixtureValue {
	fv := fixtureValue{Name: a.Name}

	if v, note, ok := enumValue(a); ok {
		fv.HCL, fv.Note = v, note

		return fv
	}

	if v, note, ok := serverDefaultValue(a); ok {
		fv.HCL, fv.Note = v, note

		return fv
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
		fv.Skipped = true
		fv.Reason = "a nested object's members may be identifiers of objects that must " +
			"already exist, and nothing in the specification says whether they are"

		return fv
	}

	switch a.Type.Kind {
	case blueprint.KindString:
		return stringValue(a, salt)

	case blueprint.KindBool:
		fv.HCL = "true"

		return fv

	case blueprint.KindInt32, blueprint.KindInt64:
		fv.HCL = strconv.FormatInt(intWithinBounds(a.Type.Constraints), 10)

		return fv

	case blueprint.KindFloat32, blueprint.KindFloat64, blueprint.KindNumber:
		fv.HCL = strconv.FormatFloat(floatWithinBounds(a.Type.Constraints), 'f', -1, 64)

		return fv

	case blueprint.KindList, blueprint.KindSet:
		return collectionValue(a, "[", "]")

	case blueprint.KindMap:
		return elementMapValue(a)

	default:
		fv.Skipped = true
		fv.Reason = fmt.Sprintf("type kind %q has no fixture value", a.Type.Kind)

		return fv
	}
}

// serverDefaultValue reuses the value the API itself applied when the field was omitted.
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
func serverDefaultValue(a blueprint.Attribute) (value, note string, ok bool) {
	sd := a.Behaviour.ServerDefault
	if sd == nil || sd.Raw == "" {
		return "", "", false
	}

	// Only a scalar. A default recorded for a collection would need its own HCL shape, and no
	// probed default has ever been one.
	if a.Type.Kind.IsNested() || a.Type.Kind == blueprint.KindList ||
		a.Type.Kind == blueprint.KindSet || a.Type.Kind == blueprint.KindMap {
		return "", "", false
	}

	return sd.Raw, "the API's own default, so a value it is known to accept", true
}

// enumValue picks a value from a constrained set, preferring what was observed to work.
func enumValue(a blueprint.Attribute) (value, note string, ok bool) {
	if accepted := a.Behaviour.AcceptedValues; len(accepted) > 0 {
		note := "observed accepted"
		if rejected := a.Behaviour.RejectedValues; len(rejected) > 0 {
			// Worth saying at the call site: it explains why the fixture does not simply
			// use the first documented value, which is the obvious thing to expect.
			note = fmt.Sprintf(
				"observed accepted; the API refused %s", strings.Join(rejected, ", "),
			)
		}

		return goStringLit(accepted[0]), note, true
	}

	if allowed := a.Type.AllowedValues; len(allowed) > 0 {
		return goStringLit(allowed[0]), "documented; unprobed", true
	}

	return "", "", false
}

// stringValue derives a string, which is where the honest limits are.
func stringValue(a blueprint.Attribute, salt string) fixtureValue {
	fv := fixtureValue{Name: a.Name}
	c := a.Type.Constraints

	if c.Pattern != "" {
		// A value satisfying an arbitrary regular expression cannot be constructed by
		// working forwards from the pattern. Generating a plausible-looking string and
		// hoping is how a fixture starts failing for a reason nobody can see, so this is
		// reported instead.
		fv.Skipped = true
		fv.Reason = fmt.Sprintf(
			"its value must match %s, and a string satisfying an arbitrary pattern cannot be "+
				"derived from the pattern", c.Pattern,
		)

		return fv
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
			fv.Skipped = true
			fv.Reason = fmt.Sprintf("its maximum length is %d", *c.MaxLength)

			return fv
		}
		value = value[:*c.MaxLength]
	}
	if c.MinLength != nil && int64(len(value)) < *c.MinLength {
		value += strings.Repeat("x", int(*c.MinLength)-len(value))
	}

	fv.HCL = goStringLit(value)

	return fv
}

// collectionValue renders a one-element list or set of scalars.
//
// One element rather than none: an empty collection and an absent one are different states, and
// the interesting one to exercise is a collection that actually carries something.
func collectionValue(a blueprint.Attribute, open, close string) fixtureValue {
	fv := fixtureValue{Name: a.Name}

	if a.Type.ElementType == nil {
		fv.Skipped = true
		fv.Reason = "it declares no element type"

		return fv
	}

	elem := fixtureValueFor(blueprint.Attribute{
		Name:                     a.Name + "_element",
		ComputedOptionalRequired: blueprint.Optional,
		Type:                     *a.Type.ElementType,
	}, "")
	if elem.Skipped {
		fv.Skipped = true
		fv.Reason = "its elements have no derivable value: " + elem.Reason

		return fv
	}

	fv.HCL = open + elem.HCL + close

	return fv
}

// elementMapValue renders a one-entry map of scalars.
func elementMapValue(a blueprint.Attribute) fixtureValue {
	fv := fixtureValue{Name: a.Name}

	if a.Type.ElementType == nil {
		fv.Skipped = true
		fv.Reason = "it declares no element type"

		return fv
	}

	elem := fixtureValueFor(blueprint.Attribute{
		Name:                     a.Name + "_value",
		ComputedOptionalRequired: blueprint.Optional,
		Type:                     *a.Type.ElementType,
	}, "")
	if elem.Skipped {
		fv.Skipped = true
		fv.Reason = "its values have no derivable value: " + elem.Reason

		return fv
	}

	fv.HCL = "{\n    tfacc = " + elem.HCL + "\n  }"

	return fv
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
