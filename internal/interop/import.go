package interop

import (
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-codegen-spec/resource"
	"github.com/hashicorp/terraform-plugin-codegen-spec/spec"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"
)

// DraftExt is the extension an imported blueprint is written under.
//
// blueprint.findBlueprints matches names ending in blueprint.Ext, and
// ".blueprint.draft.json" does not have that suffix -- so LoadDir, emit and verify
// cannot see a draft at all. That is the whole mechanism: an incomplete blueprint is
// not something the pipeline tolerates, it is something the pipeline cannot open.
// Promoting a draft is a rename, which is a git-visible, reviewable act.
//
// The alternative designs were all worse. A Draft flag on the blueprint creates a
// first-class "incomplete" state that emit will be made to tolerate within two
// releases. Writing a normal .blueprint.json produces a file that fails validation
// with forty unrelated messages. Setting Drop is the worst of the three: Validate
// skips dropped resources, so the blueprint would pass CI cleanly while emitting
// nothing at all.
//
// If blueprint.Ext's matching is ever loosened -- to strings.Contains, say -- every
// draft becomes loadable by emit in one line, with no test failing anywhere near the
// change. TestUnit_Interop_Drafts is the guard.
const DraftExt = ".blueprint.draft.json"

// Options is what ToBlueprint needs in order to name things.
//
// A specification carries a schema and a resource name. Everything else a blueprint
// needs in order to be emitted -- module paths, package layout, service grouping --
// is not in the document and cannot be guessed, so it is supplied here or reported
// as unauthored. Deliberately no SDK fields: asking an operator for an SDK module
// path in order to write a draft whose bindings are empty anyway would be several
// flags buying nothing.
type Options struct {
	// Provider is the registry name, e.g. "thousandeyes". Required.
	Provider string
	// TypePrefix prefixes every Terraform type. Defaults to Provider.
	TypePrefix string
	// GoModule is the generated provider's module path.
	GoModule string
	// APIVersionDir is the version directory generated packages live under, e.g. "v7".
	APIVersionDir string
	// ServiceGroup groups resources on disk. A specification carries no grouping, so
	// this is operator-supplied.
	ServiceGroup string
}

func (o Options) typePrefix() string {
	if o.TypePrefix != "" {
		return o.TypePrefix
	}
	return o.Provider
}

// ToBlueprint reads a specification into a draft blueprint.
//
// Resources only. Data sources and the provider configuration schema are reported
// and skipped: there are no data-source blueprints to compare an implementation
// against, and an untested import path is worse than an absent one.
//
// The result is a draft. It has a schema and no bindings, so it cannot be emitted,
// and Unauthored lists what a human has to write before it can be.
func ToBlueprint(s spec.Specification, opts Options) (blueprint.Blueprint, Report, error) {
	var r Report

	if opts.Provider == "" {
		return blueprint.Blueprint{}, r, fmt.Errorf(
			"%w: no provider name was given",
			ErrInvalidSpec,
		)
	}

	out := blueprint.Blueprint{
		FormatVersion: blueprint.FormatVersion,
		Provider: blueprint.Provider{
			Name:       opts.Provider,
			TypePrefix: opts.typePrefix(),
			GoModule:   opts.GoModule,
		},
	}

	if s.Provider != nil && s.Provider.Schema != nil {
		r.add(
			SeverityDropped,
			"provider.schema",
			"the blueprint has no provider configuration attributes, so the imported provider schema was skipped",
		)
	}

	if len(s.DataSources) > 0 {
		r.add(SeverityDropped, "datasources",
			"importing data sources is not implemented; %d skipped", len(s.DataSources))
	}

	for _, res := range s.Resources {
		converted, err := importResource(res, opts, &r)
		if err != nil {
			return blueprint.Blueprint{}, r, err
		}

		out.Resources = append(out.Resources, converted)
		r.Resources++
	}

	if len(out.Resources) == 0 {
		return blueprint.Blueprint{}, r, fmt.Errorf(
			"%w: the document declares no resources",
			ErrInvalidSpec,
		)
	}

	sort.Slice(
		out.Resources,
		func(i, j int) bool { return out.Resources[i].Key < out.Resources[j].Key },
	)

	return out, r, nil
}

// namingOpts matches internal/ingest/openapi's, so a blueprint imported from a
// specification and one inferred from OpenAPI name things the same way.
var namingOpts = naming.Options{StripPrefix: naming.DefaultStripPrefix}

func importResource(res resource.Resource, opts Options, r *Report) (blueprint.Resource, error) {
	if res.Name == "" {
		return blueprint.Resource{}, fmt.Errorf("%w: a resource with no name", ErrInvalidSpec)
	}
	if res.Schema == nil {
		return blueprint.Resource{}, fmt.Errorf(
			"%w: resource %q has no schema",
			ErrInvalidSpec,
			res.Name,
		)
	}

	path := fmt.Sprintf("resources[%s]", res.Name)

	if len(res.Schema.Blocks) > 0 {
		// A block and a nested attribute are the same data with different HCL
		// syntax. For a new provider the choice is free, and refusing would make a
		// hand-authored specification un-ingestible -- but the choice is permanent
		// once published, so it is reported.
		r.add(
			SeverityLossy,
			path+".blocks",
			"%d block(s) were converted to nested attributes; the two are the same data with different configuration syntax",
			len(res.Schema.Blocks),
		)
	}

	// The resource's own description is one field, so it is reported directly rather
	// than counted.
	var resourceDesc importLosses

	out := blueprint.Resource{
		Key:           res.Name,
		Name:          res.Name,
		GoPackage:     naming.SnakeDirName(res.Name),
		GoTypeName:    namingOpts.GoTypeName(res.Name) + "Resource",
		ModelTypeName: namingOpts.GoTypeName(res.Name) + "ResourceModel",
		ServiceGroup:  opts.ServiceGroup,
		APIVersionDir: opts.APIVersionDir,

		Schema: blueprint.Schema{
			MarkdownDescription: describe(
				res.Schema.MarkdownDescription,
				res.Schema.Description,
				&resourceDesc,
			),
			DeprecationMessage: derefStr(res.Schema.DeprecationMessage),
		},
	}

	out.GoPackageAlias = namingOpts.PackageAlias(opts.APIVersionDir, res.Name)

	if resourceDesc.promoted > 0 {
		r.add(
			SeverityInfo,
			path+".description",
			"the document set description but not markdown_description, so the text is now treated as markdown",
		)
	}

	// Every generated identifier above was derived rather than read, which a reader
	// of the draft needs to know: the derivation is mechanical and occasionally
	// clumsy, and it is theirs to correct.
	r.add(
		SeverityInfo,
		path+".naming",
		"Go package, type and model names were derived from the resource name and may want shortening",
	)

	var acc importLosses

	attrs, err := importAttributes(res.Schema.Attributes, res.Schema.Blocks, path, r, &acc)
	if err != nil {
		return blueprint.Resource{}, err
	}
	out.Schema.Attributes = attrs

	if acc.promoted > 0 {
		r.noteCount("importedDescription", path+".attributes[*].description", acc.promoted)
	}

	return out, nil
}

// Unauthored lists the blueprint fields a human must fill in before a draft can be
// emitted from, collapsed so the list is a diagnosis rather than a wall.
//
// Running an imported blueprint through blueprint.Validate produces a correct
// message per missing field, which for a seventeen attribute resource is around forty
// lines that collectively say "this is broken" instead of "this came from a
// schema-only source and needs its bindings authored". The collapsing is the whole
// point: per-attribute wire fields become one line with a count, because they are
// missing uniformly and naming each one adds nothing.
func Unauthored(bp blueprint.Blueprint) []string {
	var out []string

	for _, res := range bp.Resources {
		path := fmt.Sprintf("resources[%s]", res.Key)

		if bp.Provider.SDK.ModulePath == "" {
			out = append(out, "provider.sdk.{dialect,modulePath,clientType}")
		}

		out = append(out,
			path+".binding.service.{importPath,typeName,accessor}",
			path+".binding.{create,read,update,delete}",
			path+".binding.body.{requestType,responseType}",
			path+".binding.id.fromCreate",
			path+".policy.updateStyle",
		)

		wire, sdkTypes := 0, 0

		for _, a := range res.Schema.Attributes {
			if a.Wire == (blueprint.WireBinding{}) {
				wire++
			}
			if a.Type.NestedObject != nil {
				if a.Type.NestedObject.SDKType == "" {
					sdkTypes++
				}
				for _, child := range a.Type.NestedObject.Attributes {
					if child.Wire == (blueprint.WireBinding{}) {
						wire++
					}
				}
			}
		}

		if wire > 0 {
			out = append(
				out,
				fmt.Sprintf(
					"%s.attributes[*].wire.{sdkField,sdkGoType,expand,flatten}   (%d)",
					path,
					wire,
				),
			)
		}
		if sdkTypes > 0 {
			out = append(
				out,
				fmt.Sprintf("%s.attributes[*].type.nested.sdkType   (%d)", path, sdkTypes),
			)
		}
	}

	return out
}

// importLosses accumulates the import-side losses that fall on every attribute
// alike, for the same reason attrLosses does on the export side.
//
// Description promotion is the case that matters: the official format has no
// attribute-level markdown field, so *every* document this toolkit exports sets only
// the plain description, and importing one produces a note per attribute. On the
// pilot that is seventeen identical lines burying the notes a reader has to act on.
type importLosses struct {
	promoted int
}

// describe picks the description to carry, preferring markdown.
//
// A specification that sets only the plain description has its text promoted, which
// reinterprets it as markdown. That is nearly always harmless and occasionally not --
// an underscore in plain text becomes emphasis - so it is counted and reported once
// per resource.
func describe(markdown, plain *string, acc *importLosses) string {
	if markdown != nil {
		return *markdown
	}
	if plain == nil {
		return ""
	}

	if acc != nil {
		acc.promoted++
	}

	return *plain
}
