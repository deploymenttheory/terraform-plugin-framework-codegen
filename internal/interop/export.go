package interop

import (
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-codegen-spec/datasource"
	"github.com/hashicorp/terraform-plugin-codegen-spec/provider"
	"github.com/hashicorp/terraform-plugin-codegen-spec/resource"
	"github.com/hashicorp/terraform-plugin-codegen-spec/spec"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// FromBlueprint projects a blueprint onto the official specification, reporting
// everything the format cannot carry.
//
// The blueprint is a superset, so this is lossy by construction and the report is
// the interesting half of the return value. An error comes back only for a value
// that cannot honestly be coarsened -- an unparsable static default, an unknown
// presence, a nested object with no attributes. Everything else becomes a note, and
// a heavily downgraded export is a success: the format has no way to express a CRUD
// binding, and a tool that failed for that reason would never export anything.
func FromBlueprint(bp blueprint.Blueprint) (spec.Specification, Report, error) {
	var r Report

	out := spec.Specification{
		Version:  SpecVersion,
		Provider: exportProvider(bp.Provider, &r),
	}

	if bp.Source != (blueprint.SourceInfo{}) {
		r.note("source", "source")
	}

	// Reported once for the kind rather than once per action: the loss is that the format has
	// no such concept, which is one fact however many actions a blueprint carries.
	if len(bp.Actions) > 0 {
		r.note("actionKind", "actions")
	}
	if len(bp.Ephemerals) > 0 {
		r.note("ephemeralKind", "ephemerals")
	}

	for _, res := range bp.Resources {
		if res.Drop {
			r.Omitted++
			continue
		}

		converted, err := exportResource(res, &r)
		if err != nil {
			return spec.Specification{}, r, err
		}

		out.Resources = append(out.Resources, converted)
		r.Resources++
	}

	for _, ds := range bp.DataSources {
		if ds.Drop {
			r.Omitted++
			continue
		}

		converted, err := exportDataSource(ds, &r)
		if err != nil {
			return spec.Specification{}, r, err
		}

		out.DataSources = append(out.DataSources, converted)
		r.DataSources++
	}

	// Sorted by the name the official document carries, not by blueprint key.
	// LoadDir sorts by Key, and Key need not sort the same way as Name --
	// a resource keyed "tag" and typed "thousandeyes_tag" is the common case, but
	// nothing enforces the correspondence. Sorting on the field that actually
	// appears in the output is what makes the export independent of how the
	// blueprint happens to be split across files.
	sort.Slice(
		out.Resources,
		func(i, j int) bool { return out.Resources[i].Name < out.Resources[j].Name },
	)
	sort.Slice(
		out.DataSources,
		func(i, j int) bool { return out.DataSources[i].Name < out.DataSources[j].Name },
	)

	return out, r, nil
}

// exportProvider projects the provider block.
//
// The blueprint has no provider configuration attributes -- there is no field for
// them -- so the exported block carries a name and nothing else. That is not a
// shortcut: the JSON schema lists "provider" and "version" as the two required
// top-level keys while requiring only "name" within the provider block, so a
// name-only projection is both the most this blueprint can say and a valid
// document.
func exportProvider(p blueprint.Provider, r *Report) *provider.Provider {
	r.note("provider.schema", "provider.schema")

	if p.SDK != (blueprint.SDKModule{}) {
		r.note("provider.sdk", "provider.sdk")
	}
	if p.GoModule != "" {
		r.note("provider.goModule", "provider.goModule")
	}
	if p.TypePrefix != "" {
		r.note("provider.typePrefix", "provider.typePrefix")
	}
	if p.Conventions != (blueprint.Conventions{}) {
		r.note("provider.conventions", "provider.conventions")
	}
	if p.Support != (blueprint.SupportPkgs{}) {
		r.note("provider.support", "provider.support")
	}

	return &provider.Provider{Name: p.Name}
}

func exportResource(res blueprint.Resource, r *Report) (resource.Resource, error) {
	path := fmt.Sprintf("resources[%s]", res.Key)

	out := resource.Resource{
		Name: res.Name,
		Schema: &resource.Schema{
			// Only MarkdownDescription is set. The schema carries both it and a
			// plain Description, and writing the same text into both would double
			// the size of the block for no reader: every consumer that renders one
			// falls back to the other.
			MarkdownDescription: strPtr(res.Schema.MarkdownDescription),
			DeprecationMessage:  strPtr(res.Schema.DeprecationMessage),
		},
	}

	reportResourceLosses(res, path, r)

	var (
		acc       attrLosses
		described int
	)

	for _, a := range res.Schema.Attributes {
		if a.Drop {
			r.Omitted++
			continue
		}

		p, err := prepare(a, path+".attributes["+a.Name+"]", true, r, &acc)
		if err != nil {
			return resource.Resource{}, err
		}

		converted, err := resourceAttribute(p)
		if err != nil {
			return resource.Resource{}, fmt.Errorf("%s.attributes[%s]: %w", path, a.Name, err)
		}

		out.Schema.Attributes = append(out.Schema.Attributes, converted)
		r.Attributes++

		if a.MarkdownDescription != "" {
			described++
		}
	}

	if len(out.Schema.Attributes) == 0 {
		return resource.Resource{}, fmt.Errorf(
			"%s: %w: a resource with no attributes",
			path,
			ErrUnrepresentable,
		)
	}

	// The aggregate notes, each with its count so the reader can see the scale of
	// the loss without reading a line per attribute. Counts include attributes
	// nested inside objects, which is why they can exceed the top-level total.
	if acc.wire > 0 {
		r.noteCount("wire", path+".attributes[*].wire", acc.wire)
	}
	if acc.goField > 0 {
		r.noteCount("goField", path+".attributes[*].goField", acc.goField)
	}

	// Descriptions get the same treatment. The text crosses verbatim into
	// `description`, so nothing is lost and a round trip recovers it exactly -- but
	// the field a reader lands on is documented as plain text, and that is worth
	// saying once.
	if described > 0 {
		r.noteCount("markdownDescription", path+".attributes[*].markdownDescription", described)
	}

	return out, nil
}

// reportResourceLosses records the resource-level sections with no counterpart.
//
// Binding is reported once for the whole subtree rather than per operation. It has
// a service reference, four operations, argument lists and body models; naming each
// would bury the one thing the reader needs to know, which is that the exported
// document cannot be emitted from.
func reportResourceLosses(res blueprint.Resource, path string, r *Report) {
	r.note("naming", path+".naming")
	r.note("binding", path+".binding")

	if res.Policy.UpdateStyle != "" {
		r.note("policy.updateStyle", path+".policy.updateStyle")
	}
	if res.Policy.ReadBack != (blueprint.ReadBack{}) {
		r.note("policy.readBack", path+".policy.readBack")
	}
	if res.Policy.Delete != (blueprint.Delete{}) {
		r.note("policy.delete", path+".policy.delete")
	}
	if res.Import != (blueprint.ImportPolicy{}) {
		r.note("import", path+".import")
	}
	if res.Timeouts != (blueprint.Timeouts{}) {
		r.note("timeouts", path+".timeouts")
	}
	if res.DocRefURL != "" {
		r.note("docRefUrl", path+".docRefUrl")
	}

	// Both are facets the format has no representation for, and both are structural rather
	// than cosmetic: a document exported from this blueprint describes a resource that cannot
	// be addressed by identity and cannot be listed. Silence here would let a reader believe
	// otherwise.
	if res.Identity != nil {
		r.note("identity", path+".identity")
	}
	if res.List != nil {
		r.note("list", path+".list")
	}

	// The escape hatches. codegen-spec has no path-typed validator at all, so a cross-attribute
	// rule has nowhere to go -- and unlike the per-attribute validators above, there is not even
	// an approximation to degrade to. The hooks are worse than unexpressible: they are files a
	// practitioner owns, so a document exported from here and generated from elsewhere would
	// quietly lose hand-written plan modification.
	if len(res.ConfigValidators) > 0 {
		r.note("configValidators", path+".configValidators")
	}
	if !res.Hooks.IsZero() {
		r.note("hooks", path+".hooks")
	}
}

// reportDataSourceLosses is the data source counterpart of reportResourceLosses.
//
// A data source's binding is smaller than a resource's -- one operation, a service
// reference and a response model -- but the format has no counterpart for any of it, so
// dropping it silently would leave a reader believing the exported document could be
// emitted from. It cannot.
func reportDataSourceLosses(ds blueprint.DataSource, path string, r *Report) {
	r.note("naming", path+".naming")
	r.note("binding", path+".binding")

	if ds.Timeouts != (blueprint.Timeouts{}) {
		r.note("timeouts", path+".timeouts")
	}
	if ds.DocRefURL != "" {
		r.note("docRefUrl", path+".docRefUrl")
	}
}

func exportDataSource(ds blueprint.DataSource, r *Report) (datasource.DataSource, error) {
	path := fmt.Sprintf("dataSources[%s]", ds.Key)

	out := datasource.DataSource{
		Name: ds.Name,
		Schema: &datasource.Schema{
			// Only MarkdownDescription is set, for the same reason as a resource: the
			// schema carries both it and a plain Description, and every consumer that
			// renders one falls back to the other.
			MarkdownDescription: strPtr(ds.Schema.MarkdownDescription),
			DeprecationMessage:  strPtr(ds.Schema.DeprecationMessage),
		},
	}

	reportDataSourceLosses(ds, path, r)

	var acc attrLosses

	for _, a := range ds.Schema.Attributes {
		if a.Drop {
			r.Omitted++
			continue
		}

		p, err := prepare(a, path+".attributes["+a.Name+"]", false, r, &acc)
		if err != nil {
			return datasource.DataSource{}, err
		}

		converted, err := datasourceAttribute(p)
		if err != nil {
			return datasource.DataSource{}, fmt.Errorf("%s.attributes[%s]: %w", path, a.Name, err)
		}

		out.Schema.Attributes = append(out.Schema.Attributes, converted)
		r.Attributes++
	}

	if len(out.Schema.Attributes) == 0 {
		return datasource.DataSource{}, fmt.Errorf(
			"%s: %w: a data source with no attributes",
			path,
			ErrUnrepresentable,
		)
	}

	if acc.wire > 0 {
		r.noteCount("wire", path+".attributes[*].wire", acc.wire)
	}
	if acc.goField > 0 {
		r.noteCount("goField", path+".attributes[*].goField", acc.goField)
	}

	return out, nil
}
