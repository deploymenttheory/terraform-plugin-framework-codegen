package generate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"
)

// DataSourceAccTestView is what the data source acceptance test template needs.
type DataSourceAccTestView struct {
	Header  string
	Package string
	Imports string

	TestName string
	// Address is the data source's address, e.g. `data.thousandeyes_tag.test`.
	Address string
	// SeedAddress is the seed resource's address, e.g. `thousandeyes_tag.test`.
	SeedAddress string
	// SeedTerraformType is the seed's type alone, for CheckDestroy.
	SeedTerraformType string
	// SeedTestResourceType is the seed's existence helper type qualified by *this* data
	// source's package, e.g. "tag.TagTestResource". The helper is re-emitted into the
	// data source's package -- the seed's own copy lives in a _test.go file no other
	// package can import -- and the external test package reaches it by importing the
	// package under test, whose test build is augmented with it.
	SeedTestResourceType string

	DestroyTimeout string
	DestroyReason  string

	// Checks are finished check expressions comparing the data source's state against
	// the seed's, plus collection emptiness assertions.
	Checks []string

	// SkipUnlessEnv names an environment variable the test requires: its own
	// declared gate, or the seed resource's -- a test whose seed cannot be created
	// has already failed for the seed's reason. Empty means no gate.
	SkipUnlessEnv string
}

// DataSourceFixtureView drives testdata/datasource.tf: the seed resource's minimal
// fixture with a data block reading it back.
type DataSourceFixtureView struct {
	Header string
	// Seed is the seed resource's own minimal fixture, embedded whole.
	Seed FixtureView
	// TerraformType and Label name the data block.
	TerraformType string
	Label         string
	// Args are the data block's arguments, each an HCL reference into the seed.
	Args []fixtureValue
}

// DataSourceAccTest builds the acceptance test view for one data source.
//
// The refusals are stated, not silent, following AccTest: a data source reads an object
// it does not create, so without a declared seed there is nothing the generated test
// could read -- and generating a test that reads an empty tenant would pass or fail on
// what happens to be lying around.
func DataSourceAccTest(
	bp blueprint.Blueprint,
	d blueprint.DataSource,
	opts Options,
) (DataSourceAccTestView, DataSourceFixtureView, error) {
	if d.AccTest == nil {
		return DataSourceAccTestView{}, DataSourceFixtureView{}, &ErrUnsupported{
			What: fmt.Sprintf("acceptance test for data source %q", d.Key),
			Why: "no accTest seed is declared; a data source reads an object it does not " +
				"create, and which resource creates one is judgement the blueprint must state",
		}
	}

	seed, ok := seedResource(bp, d.AccTest.SeedResourceKey)
	if !ok {
		// Validate refuses this earlier; repeated here so render alone cannot emit
		// against a dangling key.
		return DataSourceAccTestView{}, DataSourceFixtureView{}, &ErrUnsupported{
			What: fmt.Sprintf("acceptance test for data source %q", d.Key),
			Why:  fmt.Sprintf("seed resource %q is not in the blueprint", d.AccTest.SeedResourceKey),
		}
	}

	// Salted with this data source's key: the seed must be this test's own object, or
	// concurrent packages seeding byte-identical values collide in the tenant -- the
	// first live run's 409s.
	seedFixture, err := SeedFixture(bp, seed, opts, "ds-"+d.Key)
	if err != nil {
		return DataSourceAccTestView{}, DataSourceFixtureView{}, err
	}

	dsType := bp.Provider.TerraformType(d.Name)
	seedType := bp.Provider.TerraformType(seed.Name)

	v := DataSourceAccTestView{
		Header:               GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package:              d.GoPackage + "_test",
		TestName:             naming.AccTestName("DataSource", trimDataSourceSuffix(d.GoTypeName), 1, "Read"),
		Address:              "data." + dsType + "." + fixtureLabel,
		SeedAddress:          seedType + "." + fixtureLabel,
		SeedTerraformType:    seedType,
		SeedTestResourceType: d.GoPackage + "." + testResourceTypeName(seed.GoTypeName),
	}

	v.DestroyTimeout, v.DestroyReason = destroyWait(seed)
	v.DestroyReason = wrapCommentPrefix("The wait is "+v.DestroyReason, "\t\t//")
	v.SkipUnlessEnv = skipUnlessEnvFor(seed, d.AccTest.SkipUnlessEnv)

	v.Checks = dataSourceChecks(d, seed)

	fixture := DataSourceFixtureView{
		Header:        GeneratedHeaderHCL(opts.BlueprintPath, opts.BlueprintSHA256),
		Seed:          seedFixture,
		TerraformType: dsType,
		Label:         fixtureLabel,
		Args:          seedArgValues(d, seedType),
	}
	// The seed fixture renders inside this file, which carries its own header.
	fixture.Seed.Header = ""

	imports := newImportSet()
	imports.add(pkgTesting, "")
	imports.add(pkgTime, "")
	imports.add("os", "")
	imports.add("path/filepath", "")
	imports.add(pkgTFTestResource, "")

	org := bp.Provider.GoModule
	imports.add(org+"/"+accSubdir, "")
	imports.add(org+"/"+accSubdir+"/check", "")
	imports.add(org+"/"+accSubdir+"/destroy", "")
	imports.add(org+"/"+DataSourceDir(bp, d), "")

	v.Imports = imports.render(org)

	return v, fixture, nil
}

// SeedHelper re-renders the seed resource's existence helper into the data source's own
// test package. The seed's copy is a _test.go file, which Go makes invisible to every
// other package, so each read-only block's test carries its own -- generated, so the
// duplication costs nothing and cannot drift.
func SeedHelper(
	bp blueprint.Blueprint,
	d blueprint.DataSource,
	opts Options,
) (TestHelperView, error) {
	if d.AccTest == nil {
		return TestHelperView{}, &ErrUnsupported{
			What: fmt.Sprintf("seed helper for data source %q", d.Key),
			Why:  "no accTest seed is declared",
		}
	}

	seed, ok := seedResource(bp, d.AccTest.SeedResourceKey)
	if !ok {
		return TestHelperView{}, &ErrUnsupported{
			What: fmt.Sprintf("seed helper for data source %q", d.Key),
			Why:  fmt.Sprintf("seed resource %q is not in the blueprint", d.AccTest.SeedResourceKey),
		}
	}

	helper, err := TestHelper(bp, seed, opts)
	if err != nil {
		return TestHelperView{}, err
	}

	helper.Package = d.GoPackage

	return helper, nil
}

// seedResource resolves the acceptance seed's resource by key.
func seedResource(bp blueprint.Blueprint, key string) (blueprint.Resource, bool) {
	for _, r := range bp.Resources {
		if !r.Drop && r.Key == key {
			return r, true
		}
	}
	return blueprint.Resource{}, false
}

// seedArgValues renders the data block's arguments as HCL references into the seed.
func seedArgValues(d blueprint.DataSource, seedType string) []fixtureValue {
	out := make([]fixtureValue, 0, len(d.AccTest.Args))
	for _, arg := range d.AccTest.Args {
		out = append(out, fixtureValue{
			Name: arg.Attr,
			HCL:  seedType + "." + fixtureLabel + "." + arg.FromSeedAttr,
		})
	}
	alignNames(out)

	return out
}

// dataSourceChecks builds the assertions: every data source attribute that name-matches a
// seed attribute and is assertable must equal what the seed's apply put in state -- which
// is what catches a flatten that drops or mangles a field on the read path.
//
// Collections assert element count instead of equality; per-element matching is refused
// for the same reason the resource test refuses it -- element order is the API's, not the
// configuration's.
func dataSourceChecks(d blueprint.DataSource, seed blueprint.Resource) []string {
	seedAttrs := map[string]blueprint.Attribute{}
	for _, a := range seed.Schema.Attributes {
		if !a.Drop {
			seedAttrs[a.Name] = a
		}
	}

	argAttrs := map[string]bool{}
	for _, arg := range d.AccTest.Args {
		argAttrs[arg.Attr] = true
	}

	var out []string

	for _, a := range d.Schema.Attributes {
		if a.Drop {
			continue
		}

		if a.Type.Kind.IsCollection() || a.Type.Kind.IsNested() {
			// A collection is only assertable on a listing data source -- one with no
			// arguments, whose root collection must hold at least the object this test
			// just seeded. On a singular data source the collections belong to the
			// object, and a freshly seeded object legitimately has empty ones.
			if len(d.AccTest.Args) == 0 {
				out = append(out, fmt.Sprintf(
					"check.That(address).Key(%s).CountAtLeast(1)",
					goStringLit(a.Name+".#")))
			}

			continue
		}

		// An argument is what the configuration set; asserting it round-trips is
		// asserting the read used it.
		if argAttrs[a.Name] {
			out = append(out, fmt.Sprintf(
				"check.That(address).Key(%s).MatchesOtherKey(check.That(seedAddress).Key(%s))",
				goStringLit(a.Name), goStringLit(a.Name)))

			continue
		}

		seedAttr, shared := seedAttrs[a.Name]
		if !shared {
			continue
		}
		// Assertability is the *seed's*: the value originates from the seed's apply, so
		// the reasons a value cannot be asserted -- never returned, normalised, volatile
		// -- are recorded on the seed's attribute.
		if why := unassertable(seedAttr); why != "" {
			continue
		}

		out = append(out, fmt.Sprintf(
			"check.That(address).Key(%s).MatchesOtherKey(check.That(seedAddress).Key(%s))",
			goStringLit(a.Name), goStringLit(a.Name)))
	}

	sort.Strings(out)

	return out
}

// trimDataSourceSuffix strips the conventional type suffix for a test name.
func trimDataSourceSuffix(goTypeName string) string {
	if trimmed, ok := strings.CutSuffix(goTypeName, "DataSource"); ok && trimmed != "" {
		return trimmed
	}
	return goTypeName
}
