package render

import (
	"fmt"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// The framework packages a generated list resource needs.
const (
	pkgList       = "github.com/hashicorp/terraform-plugin-framework/list"
	pkgListSchema = "github.com/hashicorp/terraform-plugin-framework/list/schema"
)

// ListView is everything the list resource template needs.
//
// A facet of ResourceView rather than a view of its own, because a list resource is not an
// independent thing: its type name is the resource's, its identity schema is the resource's,
// and it is registered by the provider only because the resource declares it.
type ListView struct {
	GoTypeName    string
	ConstructorFn string

	// Imports is the rendered import block for list_resource.go.
	Imports string

	// Interfaces are the framework interfaces the list resource asserts, as complete var
	// lines.
	Interfaces []string

	// Read is the SDK call returning many objects.
	Read *OpView

	// Elements is the finished expression yielding the slice to iterate, e.g.
	// "remote.Tags", or just the result variable when the response is itself the slice.
	Elements string

	// IdentityAssignments are finished statements filling the identity model from one
	// element, e.g. "identity.ID = convert.Deref(item.ID)".
	IdentityAssignments []string
	// IdentityGoTypeName is the identity model both this and the resource use.
	IdentityGoTypeName string

	// DisplayName is the finished expression for ListResult.DisplayName, or empty.
	DisplayName string

	// HasFilters, ConfigAttributes, FilterModelType and FilterModelFields render the
	// facet's filter schema: the query block's attributes, and the model List decodes
	// them into. Empty when the facet declares no filters, in which case a query lists
	// everything visible to the configured credentials.
	HasFilters        bool
	ConfigAttributes  []string
	FilterModelType   string
	FilterModelFields []string

	// IncludeResource is true when the collection element is the same SDK type the
	// resource's read returns, so `terraform query --include-resource` can be served
	// from the element already fetched -- never a request per element, which is the
	// template's own stated non-goal.
	IncludeResource bool
	// TimeoutsNull is the finished expression for a null timeouts value matching the
	// resource schema's block, needed because the state mapper does not touch the
	// timeouts field and a zero value carries no attribute types to serialise with.
	TimeoutsNull string
}

// listView builds the view for a resource's list facet.
//
// The facet is a parameter for the same reason identityView takes the identity: absence
// belongs to the caller, and a nil view with a nil error says nothing about which of the two
// it means.
func listView(
	bp blueprint.Blueprint,
	r blueprint.Resource,
	lf blueprint.ListFacet,
) (*ListView, error) {
	// Guaranteed by validation, and re-checked because the identity is dereferenced below: a
	// list result with no identity is an error diagnostic at query time.
	if r.Identity == nil {
		return nil, &ErrUnsupported{
			What: fmt.Sprintf("list facet of resource %q", r.Key),
			Why:  "it requires the resource to declare an identity, and this one does not",
		}
	}
	if lf.Read == nil {
		return nil, &ErrUnsupported{
			What: fmt.Sprintf("list facet of resource %q", r.Key),
			Why:  "it has no read operation, which is its whole requirement of the API",
		}
	}

	imports := newImportSet()

	v := &ListView{
		GoTypeName:         lf.GoTypeName,
		ConstructorFn:      "New" + lf.GoTypeName,
		IdentityGoTypeName: r.Identity.GoTypeName,
		Interfaces: []string{
			fmt.Sprintf("_ list.ListResource = (*%s)(nil)", lf.GoTypeName),
			// Configure is how the SDK client reaches it, so every generated list resource
			// implements it. Note the request types are resource.* and not list.*: the
			// framework says so explicitly, and templating this from the ephemeral or action
			// signatures is the trap.
			fmt.Sprintf("_ list.ListResourceWithConfigure = (*%s)(nil)", lf.GoTypeName),
		},
	}

	read, err := opView(
		fmt.Sprintf("list facet of resource %q", r.Key), lf.Service.Accessor,
		*lf.Read, "crud.PhaseRead", "errors.OpRead", "ReadTimeout", bindsResult,
	)
	if err != nil {
		return nil, err
	}
	v.Read = read

	v.Elements = read.ResultVar
	if lf.CollectionField != "" {
		v.Elements = read.ResultVar + "." + lf.CollectionField
	}

	for _, m := range lf.IdentityFrom {
		src := "item." + m.FromSDKField
		if m.IsPointer {
			// Dereferenced through the provider's convert package rather than inline, so a
			// nil field yields the zero value instead of panicking during a query.
			src = "convert.Deref(" + src + ")"
		}
		v.IdentityAssignments = append(
			v.IdentityAssignments,
			fmt.Sprintf("identity.%s = %s", m.GoField, src),
		)
	}

	if lf.DisplayNameFrom != "" {
		expr := "item." + lf.DisplayNameFrom
		if lf.DisplayNameIsPointer {
			expr = "convert.Deref(" + expr + ")"
		}
		v.DisplayName = expr
	}

	if lf.Schema != nil {
		lsc := schemaScope{
			kind:     blueprint.BlockList,
			what:     fmt.Sprintf("list facet of resource %q", r.Key),
			patterns: newPatternVars(),
		}

		attrs, fields, err := attributes(lsc, *lf.Schema, imports)
		if err != nil {
			return nil, err
		}

		v.HasFilters = true
		v.ConfigAttributes = attrs
		v.FilterModelType = lf.GoTypeName + "Filters"
		v.FilterModelFields = fields
		imports.add(pkgTypes, "")
	}

	// Structural, decided here rather than at query time: when the collection element is
	// the resource's own read type, the state mapper can serve --include-resource from
	// the element in hand. A mismatched element type means the mapper would not compile
	// against it, so the branch is simply not generated and the template's doc comment
	// keeps stating why.
	if lf.ElementType != "" && lf.ElementType == r.Binding.Body.ResponseType {
		v.IncludeResource = true
		// Mirrors commonschema.ResourceTimeouts: all four operations, string attributes.
		// The mapper never touches the timeouts field, and a zero timeouts.Value carries
		// no attribute types to serialise with.
		v.TimeoutsNull = `timeouts.Value{Object: types.ObjectNull(map[string]attr.Type{` +
			`"create": types.StringType, "read": types.StringType, ` +
			`"update": types.StringType, "delete": types.StringType})}`
		imports.add(pkgTypes, "")
		imports.add(frameworkRoot+"attr", "")
		imports.add("github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts", "")
	}

	sup := bp.Provider.Support

	imports.add(pkgContext, "")
	imports.add(pkgList, "")
	// Unaliased: the framework package is named schema, and attribute declarations are
	// rendered with the plain selector, exactly as every other block kind's are.
	imports.add(pkgListSchema, "")
	imports.add(pkgResource, "")
	imports.add(pkgTflog, "")
	// diag for the failure path: a list resource reports errors by streaming a result that
	// carries diagnostics, since List has no response object to append to.
	imports.add(pkgDiag, "")
	imports.add(bp.Provider.SDK.ClientImport.Path, bp.Provider.SDK.ClientImport.Alias)
	imports.add(sup.Client.Path, sup.Client.Alias)
	imports.add(sup.Errors.Path, sup.Errors.Alias)
	// Deliberately not the SDK service package: nothing in the generated file names a type
	// from it. The call is reached through the accessor and the loop variable's type is
	// inferred from the slice, so importing it would not compile.

	if needsConvert(lf) {
		imports.add(sup.Convert.Path, sup.Convert.Alias)
	}

	v.Imports = imports.render(bp.Provider.GoModule)

	return v, nil
}

// needsConvert reports whether any generated expression dereferences a pointer, which is the
// only reason a list resource touches the convert package.
func needsConvert(lf blueprint.ListFacet) bool {
	if lf.DisplayNameIsPointer {
		return true
	}
	for _, m := range lf.IdentityFrom {
		if m.IsPointer {
			return true
		}
	}

	return false
}
