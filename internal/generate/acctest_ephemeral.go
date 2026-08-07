package generate

import (
	"fmt"
	"sort"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"
)

// EphemeralAccTestView is what the ephemeral acceptance test template needs.
type EphemeralAccTestView struct {
	Header  string
	Package string
	Imports string

	TestName string
	// EchoAddress is where the opened value lands, e.g. `echo.test`.
	EchoAddress string
	// SeedAddress, SeedTerraformType and SeedTestResourceType are the seed resource's,
	// exactly as a data source test carries them; the helper is re-emitted into this
	// package for the same _test.go visibility reason.
	SeedAddress          string
	SeedTerraformType    string
	SeedTestResourceType string

	DestroyTimeout string
	DestroyReason  string

	// Checks assert the echoed object against the seed's configured values. An
	// ephemeral value never reaches state, so the echo provider is the one place a
	// check can read it -- which is exactly what it exists for.
	Checks []string

	// SkipUnlessEnv names an environment variable the test requires: its own
	// declared gate, or the seed resource's. Empty means no gate.
	SkipUnlessEnv string
}

// EphemeralFixtureView drives testdata/ephemeral.tf.
type EphemeralFixtureView struct {
	Header string
	// Seed is the seed resource's minimal fixture, embedded whole.
	Seed FixtureView
	// TerraformType and Label name the ephemeral block.
	TerraformType string
	Label         string
	// Args wire the ephemeral's configuration into the seed by HCL reference.
	Args []fixtureValue
}

// EphemeralAccTest builds the acceptance test view for one ephemeral resource.
//
// The shape is the data source test's with one addition: the opened value cannot be
// asserted from state, because not reaching state is the kind's whole contract. So the
// fixture hands the ephemeral object to the echo provider, whose resource stores it for
// exactly this purpose, and the checks read the echo.
func EphemeralAccTest(
	bp blueprint.Blueprint,
	e blueprint.Ephemeral,
	opts Options,
) (EphemeralAccTestView, EphemeralFixtureView, error) {
	if e.AccTest == nil {
		return EphemeralAccTestView{}, EphemeralFixtureView{}, &ErrUnsupported{
			What: fmt.Sprintf("acceptance test for ephemeral %q", e.Key),
			Why: "no accTest seed is declared; an ephemeral opens an object it does not " +
				"create, and which resource creates one is judgement the blueprint must state",
		}
	}

	seed, ok := seedResource(bp, e.AccTest.SeedResourceKey)
	if !ok {
		return EphemeralAccTestView{}, EphemeralFixtureView{}, &ErrUnsupported{
			What: fmt.Sprintf("acceptance test for ephemeral %q", e.Key),
			Why:  fmt.Sprintf("seed resource %q is not in the blueprint", e.AccTest.SeedResourceKey),
		}
	}

	// Salted with this ephemeral's key, for the reason the data source test's seed is:
	// each test's seed must be its own object in the shared tenant.
	seedFixture, err := SeedFixture(bp, seed, opts, "eph-"+e.Key)
	if err != nil {
		return EphemeralAccTestView{}, EphemeralFixtureView{}, err
	}

	ephType := bp.Provider.TerraformType(e.Name)
	seedType := bp.Provider.TerraformType(seed.Name)

	v := EphemeralAccTestView{
		Header:               GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package:              e.GoPackage + "_test",
		TestName:             naming.AccTestName("Ephemeral", trimEphemeralSuffix(e.GoTypeName), 1, "Open"),
		EchoAddress:          "echo.test",
		SeedAddress:          seedType + "." + fixtureLabel,
		SeedTerraformType:    seedType,
		SeedTestResourceType: e.GoPackage + "." + testResourceTypeName(seed.GoTypeName),
	}

	v.DestroyTimeout, v.DestroyReason = destroyWait(seed)
	v.DestroyReason = wrapCommentPrefix("The wait is "+v.DestroyReason, "\t\t//")
	v.SkipUnlessEnv = skipUnlessEnvFor(seed, e.AccTest.SkipUnlessEnv)

	v.Checks = ephemeralChecks(e, seed, seedFixture)

	fixture := EphemeralFixtureView{
		Header:        GeneratedHeaderHCL(opts.BlueprintPath, opts.BlueprintSHA256),
		Seed:          seedFixture,
		TerraformType: ephType,
		Label:         fixtureLabel,
	}
	fixture.Seed.Header = ""
	for _, arg := range e.AccTest.Args {
		fixture.Args = append(fixture.Args, fixtureValue{
			Name: arg.Attr,
			HCL:  seedType + "." + fixtureLabel + "." + arg.FromSeedAttr,
		})
	}
	alignNames(fixture.Args)

	imports := newImportSet()
	imports.add(pkgTesting, "")
	imports.add(pkgTime, "")
	imports.add("os", "")
	imports.add("path/filepath", "")
	imports.add(pkgTFTestResource, "")
	imports.add("github.com/hashicorp/terraform-plugin-testing/tfversion", "")

	org := bp.Provider.GoModule
	imports.add(org+"/"+accSubdir, "")
	if len(v.Checks) > 0 {
		// Conditional because an ephemeral sharing no assertable attribute with its
		// seed produces no cross-checks, and an import nothing references is a test
		// file that does not compile.
		imports.add(org+"/"+accSubdir+"/check", "")
	}
	imports.add(org+"/"+accSubdir+"/destroy", "")
	imports.add(org+"/"+EphemeralDir(bp, e), "")

	v.Imports = imports.render(org)

	return v, fixture, nil
}

// ephemeralChecks asserts the echoed object's attributes against the values the seed's
// configuration set, wherever both sides carry the attribute and the seed's behaviour
// permits an exact assertion.
func ephemeralChecks(
	e blueprint.Ephemeral,
	seed blueprint.Resource,
	seedFixture FixtureView,
) []string {
	seedAttrs := map[string]blueprint.Attribute{}
	for _, a := range seed.Schema.Attributes {
		if !a.Drop {
			seedAttrs[a.Name] = a
		}
	}
	configured := map[string]string{}
	for _, fv := range seedFixture.Values {
		configured[fv.Name] = fv.HCL
	}

	var out []string

	for _, a := range e.Schema.Attributes {
		if a.Drop || !a.ComputedOptionalRequired.IsComputed() {
			continue
		}
		seedAttr, shared := seedAttrs[a.Name]
		if !shared {
			continue
		}
		hcl, set := configured[a.Name]
		if !set {
			continue
		}
		if why := unassertable(seedAttr); why != "" {
			continue
		}

		out = append(out, fmt.Sprintf(
			`check.That(echoAddress).Key(%s).HasValue(%s)`,
			goStringLit("data."+a.Name), goStringLit(unquote(hcl))))
	}

	sort.Strings(out)

	return out
}

// EphemeralSeedHelper re-renders the seed's existence helper into the ephemeral's own
// package; see SeedHelper for why the seed's copy cannot be imported.
func EphemeralSeedHelper(
	bp blueprint.Blueprint,
	e blueprint.Ephemeral,
	opts Options,
) (TestHelperView, error) {
	if e.AccTest == nil {
		return TestHelperView{}, &ErrUnsupported{
			What: fmt.Sprintf("seed helper for ephemeral %q", e.Key),
			Why:  "no accTest seed is declared",
		}
	}

	seed, ok := seedResource(bp, e.AccTest.SeedResourceKey)
	if !ok {
		return TestHelperView{}, &ErrUnsupported{
			What: fmt.Sprintf("seed helper for ephemeral %q", e.Key),
			Why:  fmt.Sprintf("seed resource %q is not in the blueprint", e.AccTest.SeedResourceKey),
		}
	}

	helper, err := TestHelper(bp, seed, opts)
	if err != nil {
		return TestHelperView{}, err
	}

	helper.Package = e.GoPackage

	return helper, nil
}

// trimEphemeralSuffix strips the conventional type suffix for a test name.
func trimEphemeralSuffix(goTypeName string) string {
	for _, suffix := range []string{"Ephemeral", "EphemeralResource"} {
		if trimmed, ok := cutSuffixNonEmpty(goTypeName, suffix); ok {
			return trimmed
		}
	}
	return goTypeName
}

func cutSuffixNonEmpty(s, suffix string) (string, bool) {
	if len(s) > len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)], true
	}
	return s, false
}
