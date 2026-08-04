package generate

import (
	"fmt"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// The example views feed the tfplugindocs layout: examples/provider, examples/resources,
// examples/data-sources and examples/ephemeral-resources, each copied verbatim into the
// registry documentation.
//
// Every example is a scaffold -- written once when absent, then owned by whoever edits
// it. Documentation is prose with code in it, and a human's richer example (the pilot's
// hand-written tag example shows both nested collections with realistic values) must
// never be flattened back to a generated minimum by the next emit.

// ProviderExampleView drives examples/provider/provider.tf.
type ProviderExampleView struct {
	Name string
	// Source is the registry address, e.g. "deploymenttheory/thousandeyes".
	Source string
}

// ProviderExample builds the provider block example.
func ProviderExample(bp blueprint.Blueprint) ProviderExampleView {
	return ProviderExampleView{
		Name:   bp.Provider.Name,
		Source: registrySource(bp),
	}
}

// registrySource derives the registry address from the module path's owner, which is
// the convention the pilot follows; a provider published elsewhere edits the scaffold.
func registrySource(bp blueprint.Blueprint) string {
	owner := "example"
	if parts := splitModule(bp.Provider.GoModule); parts != "" {
		owner = parts
	}

	return owner + "/" + bp.Provider.Name
}

func splitModule(module string) string {
	// github.com/OWNER/... -> OWNER
	var parts []string
	start := 0
	for i := 0; i <= len(module); i++ {
		if i == len(module) || module[i] == '/' {
			parts = append(parts, module[start:i])
			start = i + 1
		}
	}
	if len(parts) >= 2 {
		return parts[1]
	}

	return ""
}

// ResourceExampleView drives examples/resources/<type>/resource.tf.
type ResourceExampleView struct {
	TerraformType string
	Values        []fixtureValue
}

// ResourceExample builds a resource example from the same value derivation the minimal
// fixture uses -- observed-accepted values first -- with the example's own label.
func ResourceExample(bp blueprint.Blueprint, r blueprint.Resource) (ResourceExampleView, error) {
	fixture, err := Fixture(bp, r, Options{}, true)
	if err != nil {
		return ResourceExampleView{}, err
	}

	return ResourceExampleView{
		TerraformType: bp.Provider.TerraformType(r.Name),
		Values:        fixture.Values,
	}, nil
}

// BlockExampleView drives the data source and ephemeral examples: a block whose
// arguments are placeholders a reader fills in.
type BlockExampleView struct {
	TerraformType string
	Args          []fixtureValue
}

// DataSourceExample builds a data source example from its configurable attributes.
func DataSourceExample(bp blueprint.Blueprint, d blueprint.DataSource) BlockExampleView {
	return BlockExampleView{
		TerraformType: bp.Provider.TerraformType(d.Name),
		Args:          placeholderArgs(d.Schema.Attributes),
	}
}

// EphemeralExample builds an ephemeral example from its configurable attributes.
func EphemeralExample(bp blueprint.Blueprint, e blueprint.Ephemeral) BlockExampleView {
	return BlockExampleView{
		TerraformType: bp.Provider.TerraformType(e.Name),
		Args:          placeholderArgs(e.Schema.Attributes),
	}
}

// placeholderArgs renders each required attribute as a quoted hint naming itself.
func placeholderArgs(attrs []blueprint.Attribute) []fixtureValue {
	var out []fixtureValue
	for _, a := range attrs {
		if a.Drop || !a.ComputedOptionalRequired.IsRequired() {
			continue
		}
		out = append(out, fixtureValue{
			Name: a.Name,
			HCL:  fmt.Sprintf("%q", "<"+a.Name+">"),
		})
	}
	alignNames(out)

	return out
}

// ImportExampleView drives examples/resources/<type>/import.sh.
type ImportExampleView struct {
	Example string
}

// ImportExample builds the import script when the blueprint records an example.
func ImportExample(r blueprint.Resource) (ImportExampleView, bool) {
	if r.Import.Example == "" {
		return ImportExampleView{}, false
	}

	return ImportExampleView{Example: r.Import.Example}, true
}
