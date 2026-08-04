package generate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/fixturespec"
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
// Both fixtures carry the generated marker and are policed by the drift check. The
// maximal one was a hand-owned scaffold once, and that ownership is what desynced its
// values from the generated assertions -- the checks derive from the blueprint, so the
// blueprint is the only place a value can be corrected without the two disagreeing.
func Fixture(
	bp blueprint.Blueprint,
	r blueprint.Resource,
	opts Options,
	minimal bool,
) (FixtureView, error) {
	header := GeneratedHeaderHCL(opts.BlueprintPath, opts.BlueprintSHA256)

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

		// A curated omission keeps a jointly-refused combination out of the fixture:
		// the value would be valid alone and the body is refused with it present.
		if r.AccFixture.Omitted(a.Name) {
			v.Skipped = append(v.Skipped, fixtureValue{
				Name: a.Name,
				Comment: wrapCommentPrefix(a.Name+": curated omission; individually valid, "+
					"refused in combination -- see the attribute's documentation", "  #"),
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

// The derivation itself lives in fixturespec, shared with the probe's rehearsal so the
// body the probe sends and the fixture the generator emits can never disagree. What
// remains here is formatting: an Entry in, HCL out.

// fixtureWants reports whether an attribute belongs in this fixture. See fixturespec.Wants.
func fixtureWants(a blueprint.Attribute, minimal bool) bool {
	return fixturespec.Wants(a, minimal)
}

// attrImmutable reports whether the prober corroborated that the API refuses in-place
// changes to this field.
func attrImmutable(a blueprint.Attribute) bool {
	return fixturespec.Immutable(a)
}

// fixtureValueFor derives one attribute's test value and formats it as HCL.
func fixtureValueFor(a blueprint.Attribute, salt string) fixtureValue {
	e := fixturespec.Derive(a, salt)

	fv := fixtureValue{
		Name:    a.Name,
		Note:    e.Note,
		Skipped: e.Skipped,
		Reason:  e.Reason,
	}
	if e.Skipped {
		return fv
	}

	fv.HCL = entryHCL(e)

	return fv
}

// entryHCL formats a derived entry as HCL.
//
// A Verbatim literal is emitted unchanged: it is the evidence's own spelling of the
// value, and re-formatting a parsed copy is how a generated file starts drifting with
// no source change.
func entryHCL(e fixturespec.Entry) string {
	if e.Verbatim != "" {
		return e.Verbatim
	}

	switch v := e.Value.(type) {
	case []any:
		// The derivation produces one scalar element -- see fixturespec.Derive.
		return "[" + scalarHCL(v[0]) + "]"
	case map[string]any:
		return "{\n    tfacc = " + scalarHCL(v["tfacc"]) + "\n  }"
	default:
		return scalarHCL(e.Value)
	}
}

// scalarHCL formats one wire-typed scalar as HCL.
func scalarHCL(v any) string {
	switch v := v.(type) {
	case string:
		return goStringLit(v)
	case bool:
		return strconv.FormatBool(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		// Unreachable by construction: fixturespec produces only the types above.
		return fmt.Sprintf("%v", v)
	}
}
