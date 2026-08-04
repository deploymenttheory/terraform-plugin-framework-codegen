package render

import (
	"fmt"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"
)

// ListAccTestView is what the list acceptance test template needs.
//
// The file lands in the resource's own test package, beside the resource acceptance
// test, and deliberately leans on it: address, testResource and config() are the
// sibling file's declarations, which is why emit only produces this when the resource
// test itself was produced.
type ListAccTestView struct {
	Header  string
	Package string
	Imports string

	TestName string
	// TerraformType is the resource type, which a list facet shares by construction.
	TerraformType string
	// QueryAddress is the list block's address in the query file, e.g.
	// "thousandeyes_tag.test".
	QueryAddress string

	DestroyTimeout string
	DestroyReason  string

	// SkipUnlessEnv is inherited from the resource's own gate: the list test seeds
	// the same minimal fixture, so it needs the same privilege. Empty means no gate.
	SkipUnlessEnv string
}

// ListQueryFixtureView drives testdata/query.tf: a provider block and the list block.
type ListQueryFixtureView struct {
	Header string
	// ProviderName is the provider block's label, which the list block references.
	ProviderName  string
	TerraformType string
	Label         string
}

// ListAccTest builds the acceptance test view for a resource's list facet.
func ListAccTest(
	bp blueprint.Blueprint,
	r blueprint.Resource,
	opts Options,
) (ListAccTestView, ListQueryFixtureView, error) {
	if r.List == nil {
		return ListAccTestView{}, ListQueryFixtureView{}, &ErrUnsupported{
			What: fmt.Sprintf("list acceptance test for resource %q", r.Key),
			Why:  "the resource declares no list facet",
		}
	}

	tfType := bp.Provider.TerraformType(r.Name)

	v := ListAccTestView{
		Header:        GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package:       r.GoPackage + "_test",
		TestName:      naming.AccTestName("List", trimResourceSuffix(r.GoTypeName), 1, "Query"),
		TerraformType: tfType,
		QueryAddress:  tfType + "." + fixtureLabel,
	}

	v.DestroyTimeout, v.DestroyReason = destroyWait(r)
	v.DestroyReason = wrapCommentPrefix("The wait is "+v.DestroyReason, "\t\t//")
	v.SkipUnlessEnv = skipUnlessEnvFor(r, "")

	fixture := ListQueryFixtureView{
		Header:        GeneratedHeaderHCL(opts.BlueprintPath, opts.BlueprintSHA256),
		ProviderName:  bp.Provider.Name,
		TerraformType: tfType,
		Label:         fixtureLabel,
	}

	imports := newImportSet()
	imports.add(pkgTesting, "")
	imports.add(pkgTime, "")
	imports.add(pkgTFTestResource, "")
	imports.add("github.com/hashicorp/terraform-plugin-testing/querycheck", "")
	imports.add("github.com/hashicorp/terraform-plugin-testing/tfversion", "")
	if v.SkipUnlessEnv != "" {
		imports.add("os", "")
	}

	org := bp.Provider.GoModule
	imports.add(org+"/"+accSubdir, "")
	imports.add(org+"/"+accSubdir+"/destroy", "")

	v.Imports = imports.render(org)

	return v, fixture, nil
}
