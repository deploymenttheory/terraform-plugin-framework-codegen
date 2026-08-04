package render

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"
)

// The packages a generated acceptance test needs.
const (
	pkgTFTestResource = "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	pkgTFPlancheck    = "github.com/hashicorp/terraform-plugin-testing/plancheck"
	pkgTesting        = "testing"
)

// AccTestView is what the per-resource acceptance test template needs.
type AccTestView struct {
	Header  string
	Package string
	Imports string

	// TestName is the Go test function, e.g. "TestAccResourceTag_01_Lifecycle".
	TestName string
	// Address is the resource address in HCL, e.g. "thousandeyes_tag.test".
	Address string
	// TerraformType is the type alone, for CheckDestroy.
	TerraformType string
	// TestResourceType is the generated existence helper, e.g. "TagTestResource".
	TestResourceType string

	// DestroyTimeout is the Go duration literal the destroy check waits for.
	DestroyTimeout string
	// DestroyReason says where DestroyTimeout came from, measured or default.
	DestroyReason string

	// MinimalChecks and MaximalChecks are finished check expressions per step.
	MinimalChecks []string
	MaximalChecks []string
	// HasMaximal is false when no maximal fixture could be produced, in which case the
	// update step is omitted rather than repeating the create.
	HasMaximal bool

	// HasReplace is true when an immutable attribute in the minimal fixture has a second
	// value the API was observed or documented to take, so a step can flip it and assert
	// the plan forces replacement -- the generated RequiresReplace working, proven live.
	HasReplace bool
	// ReplaceAttr names the flipped attribute, for the step's comment.
	ReplaceAttr string
	// ReplaceChecks are the assertions after the replacement applied.
	ReplaceChecks []string

	// ImportIgnore lists attributes import verification must skip, as Go string literals.
	ImportIgnore []string
	// ImportIgnoreReasons explains each, as wrapped comment lines.
	ImportIgnoreReasons []string

	// PatternVars are package-level regexp declarations the checks reference.
	PatternVars []string

	// SkipUnlessEnv names an environment variable the test requires; unset, the
	// test skips naming it before PreCheck runs. Empty means no gate.
	SkipUnlessEnv string
}

// skipUnlessEnvFor resolves the env gate for a seeded block's generated test: its own
// declared variable wins, else the seed resource's. The inheritance is the point -- a
// test whose seed cannot be created has already failed for the seed's reason, so a
// gated resource gates every test that seeds from it without anybody declaring it twice.
func skipUnlessEnvFor(seed blueprint.Resource, own string) string {
	if own != "" {
		return own
	}
	if seed.AccTest != nil {
		return seed.AccTest.SkipUnlessEnv
	}
	return ""
}

// notReturnedSomewhere reports whether any reading of the behaviour says the API may not
// return this attribute: the unconditional field, or any branch variant.
//
// Deliberately conservative. A variant saying "not returned when objectType is test" is a
// fact about one branch, but the generated fixtures are single-branch configurations and
// nothing here resolves which branch they exercise -- so an attribute one branch discards
// is not asserted and not import-verified on any, the exact stance the old unconditional
// fact produced, now derived without the half-truth stored in the schema.
func notReturnedSomewhere(b blueprint.Behaviour) bool {
	if b.ReturnedOnRead != nil && !*b.ReturnedOnRead {
		return true
	}
	for _, v := range b.Conditional {
		if v.Behaviour.ReturnedOnRead != nil && !*v.Behaviour.ReturnedOnRead {
			return true
		}
	}

	return false
}

// isUnsupported reports whether an error is a stated refusal rather than a fault.
func isUnsupported(err error) bool {
	var u *ErrUnsupported

	return errors.As(err, &u)
}

// AccTest builds the acceptance test view for one resource.
func AccTest(bp blueprint.Blueprint, r blueprint.Resource, opts Options) (AccTestView, error) {
	if r.Binding.Read == nil {
		return AccTestView{}, &ErrUnsupported{
			What: fmt.Sprintf("acceptance test for resource %q", r.Key),
			Why:  "without a read operation there is no way to check the object exists",
		}
	}

	minimal, err := Fixture(bp, r, opts, true)
	if err != nil {
		return AccTestView{}, err
	}

	tfType := bp.Provider.TerraformType(r.Name)
	patterns := newPatternVars()

	v := AccTestView{
		Header:           GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package:          r.GoPackage + "_test",
		TestName:         naming.AccTestName("Resource", trimResourceSuffix(r.GoTypeName), 1, "Lifecycle"),
		Address:          tfType + "." + fixtureLabel,
		TerraformType:    tfType,
		TestResourceType: r.GoPackage + "." + testResourceTypeName(r.GoTypeName),
	}

	if r.AccTest != nil {
		v.SkipUnlessEnv = r.AccTest.SkipUnlessEnv
	}

	v.DestroyTimeout, v.DestroyReason = destroyWait(r)
	// Wrapped into a finished comment block, because the prefix repeats on every line and
	// the lead-in must not.
	v.DestroyReason = wrapCommentPrefix("The wait is "+v.DestroyReason, "\t\t//")

	// The maximal fixture drives the update step. Its absence is not an error: a resource whose
	// every writable attribute is required has nothing to change, so the test creates, imports
	// and destroys without pretending to update.
	maximal, maxErr := Fixture(bp, r, opts, false)
	switch {
	case maxErr == nil && len(maximal.Values) > len(minimal.Values):
		v.HasMaximal = true
	case maxErr != nil && !isUnsupported(maxErr):
		return AccTestView{}, maxErr
	}

	v.MinimalChecks = checksFor(r, minimal, patterns)
	if v.HasMaximal {
		v.MaximalChecks = checksFor(r, maximal, patterns)
	}

	if replace, attr, ok := replaceFixture(r, minimal); ok {
		v.HasReplace = true
		v.ReplaceAttr = attr
		v.ReplaceChecks = checksFor(r, replace, patterns)
	}

	v.ImportIgnore, v.ImportIgnoreReasons = importIgnores(r)
	v.PatternVars = patterns.Decls()

	imports := newImportSet()
	imports.add(pkgTesting, "")
	imports.add(pkgTime, "")
	imports.add("os", "")
	imports.add("path/filepath", "")
	imports.add(pkgTFTestResource, "")
	if v.HasReplace {
		imports.add(pkgTFPlancheck, "")
	}
	if len(v.PatternVars) > 0 {
		imports.add(pkgRegexp, "")
	}

	org := bp.Provider.GoModule
	imports.add(org+"/"+accSubdir, "")
	imports.add(org+"/"+accSubdir+"/check", "")
	imports.add(org+"/"+accSubdir+"/destroy", "")
	imports.add(org+"/"+ResourceDir(bp, r), "")

	v.Imports = imports.render(org)

	return v, nil
}

// destroyWait is how long the destroy check waits for the object to disappear, and why.
//
// From the prober's measurement where there is one. An API that is read-your-writes consistent
// gets the built-in default and pays nothing for it, because the first check succeeds.
//
// The reason is emitted alongside, because a bare duration in a test is indistinguishable from
// superstition -- the reference provider's equivalent is a hardcoded `time.Sleep(30 * time.Second)`
// in every acceptance test, with nothing to say whether 30 was measured or guessed.
func destroyWait(r blueprint.Resource) (literal, reason string) {
	rb := r.Policy.ReadBack

	if rb.Enabled && rb.MaxRetries > 0 && rb.IntervalMS > 0 {
		// The full retry budget the provider itself would spend, so the test cannot be stricter
		// than the code under test.
		total := time.Duration(rb.MaxRetries*rb.IntervalMS) * time.Millisecond
		why := rb.Reason
		if why == "" {
			why = "the prober measured this API needing a re-read after a write"
		}

		return goDuration(total), "measured: " + why
	}

	total := time.Duration(defaultReadBackRetries*defaultReadBackIntervalMS) * time.Millisecond

	return goDuration(total),
		"the built-in default; this API's consistency after delete was never measured, so this " +
			"is a budget rather than an observation"
}

// goDuration renders a duration as a Go literal a reader can check.
func goDuration(d time.Duration) string {
	return fmt.Sprintf("%d * time.Second", int(d.Seconds()))
}

// checksFor builds the assertions for one step.
//
// Every assertion has to be one the API's observed behaviour permits, which is why this is
// generated from evidence rather than from the schema alone. Three things are deliberately not
// asserted, and each would produce a test that fails while the provider is correct:
//
//   - an attribute the API does not return on read, because state will not hold what the
//     configuration set;
//   - an attribute the API normalises, because the stored value is by definition not the sent one;
//   - a volatile attribute, because two reads disagree.
func checksFor(r blueprint.Resource, f FixtureView, patterns *patternVars) []string {
	out := []string{
		"check.That(address).ExistsInAPI(testResource)",
		`check.That(address).Key("id").Exists()`,
	}

	set := make(map[string]fixtureValue, len(f.Values))
	for _, v := range f.Values {
		set[v.Name] = v
	}

	for _, a := range r.Schema.Attributes {
		if a.Drop {
			continue
		}

		fv, configured := set[a.Name]

		switch {
		case configured && fv.Curated:
			// A curated value may be a reference -- a data-source attribute, an
			// expression -- whose value exists only at apply time, so equality is not
			// the generator's to assert. Presence is: the step configured it, and a
			// state that lost it is wrong whatever the value was.
			if why := unassertable(a); why != "" {
				continue
			}

			out = append(out, fmt.Sprintf(
				"check.That(address).Key(%s).Exists()", goStringLit(a.Name)))

		case configured:
			// Configured by this step, so its value is known and can be asserted exactly --
			// unless the API's own behaviour makes that assertion wrong.
			if why := unassertable(a); why != "" {
				continue
			}

			out = append(out, assertValue(a, f))

		case a.Behaviour.ServerDefault != nil && a.ComputedOptionalRequired != blueprint.Required:
			// Not configured, so what lands in state is the API's default. This is the check
			// that tests the probe's evidence against reality: if the default has changed since
			// the cassette was recorded, this is what says so.
			if why := unassertable(a); why != "" {
				continue
			}
			// An empty-string default is degenerate: the wire cannot distinguish it
			// from absence, the state mapper stores absence as null, and a live run
			// proved the assertion fails against an attribute that is not in state
			// at all. Nothing is claimed where nothing is distinguishable.
			if unquote(a.Behaviour.ServerDefault.Raw) == "" {
				continue
			}

			out = append(out, fmt.Sprintf(
				"check.That(address).Key(%s).HasValue(%s)",
				goStringLit(a.Name), goStringLit(unquote(a.Behaviour.ServerDefault.Raw)),
			))
		}
	}

	// A pattern is a claim about every value, not just the configured one, so it is checked
	// against whatever the API stored rather than against what was sent.
	for _, a := range r.Schema.Attributes {
		if a.Drop || a.Type.Constraints.Pattern == "" || unassertable(a) != "" {
			continue
		}

		out = append(out, fmt.Sprintf(
			"check.That(address).Key(%s).MatchesRegex(%s)",
			goStringLit(a.Name), patterns.claim(a.Name, a.Type.Constraints.Pattern),
		))
	}

	return out
}

// assertValue asserts one configured attribute against the value the fixture set.
func assertValue(a blueprint.Attribute, f FixtureView) string {
	for _, v := range f.Values {
		if v.Name == a.Name {
			return fmt.Sprintf(
				"check.That(address).Key(%s).HasValue(%s)",
				goStringLit(a.Name), goStringLit(unquote(v.HCL)),
			)
		}
	}

	// Unreachable: the caller only calls this for a name it found in the fixture. Asserting
	// presence rather than panicking, because a wrong assertion is recoverable and a panic in
	// the generator is not.
	return fmt.Sprintf("check.That(address).Key(%s).Exists()", goStringLit(a.Name))
}

// unassertable reports why an attribute's value cannot be asserted, or "" when it can.
func unassertable(a blueprint.Attribute) string {
	b := a.Behaviour

	switch {
	case notReturnedSomewhere(b):
		return "the API does not return it on read, so state cannot hold the configured value"
	case b.Normalises != "":
		return "the API " + b.Normalises + ", so the stored value is not the one sent"
	case b.Volatile != nil && *b.Volatile:
		return "it differs between two identical reads"
	case a.Type.Kind.IsNested() || a.Type.Kind == blueprint.KindList ||
		a.Type.Kind == blueprint.KindSet || a.Type.Kind == blueprint.KindMap:
		// A collection's state is indexed keys rather than one value, so an equality assertion
		// needs a shape this does not attempt.
		return "a collection is asserted per element, which is not generated"
	default:
		return ""
	}
}

// importIgnores are the attributes ImportStateVerify has to skip, with the reason for each.
//
// ImportStateVerify re-imports the object and compares every attribute against the state the
// apply produced. An attribute the API never returns is absent after import and present before
// it, so the comparison fails for a reason that has nothing to do with import being broken --
// which is exactly the false failure that makes people delete the check.
func importIgnores(r blueprint.Resource) (names, reasons []string) {
	for _, a := range r.Schema.Attributes {
		if a.Drop {
			continue
		}

		why := ""
		switch b := a.Behaviour; {
		case a.Wire.SkipFlatten:
			// Structural, not evidential: a write-only attribute's state is carried
			// from configuration, and an import has no configuration to carry from.
			// The classic tests' agents is the live case -- present before import,
			// absent after, and the difference says nothing about import working.
			why = "it is write-only, carried from configuration, which an import does not have"
		case notReturnedSomewhere(b):
			why = "the API never returns it, so it is absent after an import"
		case b.Volatile != nil && *b.Volatile:
			why = "it differs between two identical reads"
		default:
			continue
		}

		names = append(names, goStringLit(a.Name))
		reasons = append(reasons, wrapComment(a.Name+": "+why))
	}

	return names, reasons
}

// trimResourceSuffix drops a trailing "Resource" so a test is named for the thing, not the type.
func trimResourceSuffix(goTypeName string) string {
	if len(goTypeName) > len("Resource") &&
		goTypeName[len(goTypeName)-len("Resource"):] == "Resource" {
		return goTypeName[:len(goTypeName)-len("Resource")]
	}

	return goTypeName
}

// unquote strips one layer of Go string quoting from a literal.
//
// Fixture values and probed defaults are both stored already quoted, and the check helpers take
// an unquoted Go string, so the quotes have to come off before goStringLit puts them back. Doing
// it this way rather than trimming characters keeps an embedded quote or backslash correct.
func unquote(lit string) string {
	if len(lit) >= 2 && lit[0] == '"' && lit[len(lit)-1] == '"' {
		if s, err := strconv.Unquote(lit); err == nil {
			return s
		}
	}

	return lit
}
