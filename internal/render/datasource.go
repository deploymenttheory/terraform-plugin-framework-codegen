package render

import (
	"fmt"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// DataSourceView is everything the per-data-source templates need.
//
// It is a sibling of ResourceView rather than a superset or a subset of it. A data source
// has one operation, no request body and no plan, so the fields a resource needs for the
// other three operations are not absent here -- they were never applicable. Sharing one
// view would mean every data source template guarding against fields that are always
// empty, which is the sort of conditional the templates are meant not to contain.
type DataSourceView struct {
	Header        string
	Package       string
	DocRefComment string

	Imports DataSourceImports

	// DataSourceName is the Terraform type, e.g. "thousandeyes_tag".
	DataSourceName string
	GoTypeName     string
	ModelTypeName  string
	ConstructorFn  string

	SDKClientType string

	MarkdownDescription string

	// ReadTimeout is the read deadline in seconds. There is no other operation to give
	// a data source a deadline for.
	ReadTimeout int

	Interfaces []string

	SchemaAttributes []string
	ModelFields      []string
	NestedModels     []NestedModelView

	// Read is the SDK call, and State is the flatten function it feeds.
	Read  *OpView
	State StateView
}

// DataSourceImports holds the rendered import block for each emitted file.
//
// There is no Construct: a data source sends no request body, so there is nothing to
// expand into one. model.go declares its own imports in the template, as the resource's
// does.
type DataSourceImports struct {
	DataSource string
	Read       string
	State      string
}

// DataSource builds the view for one data source.
func DataSource(
	bp blueprint.Blueprint,
	d blueprint.DataSource,
	opts Options,
) (DataSourceView, error) {
	var (
		impDataSource = newImportSet()
		impRead       = newImportSet()
		impState      = newImportSet()
	)

	sc := dataSourceScope(d)

	v := DataSourceView{
		Header:              GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package:             d.GoPackage,
		DataSourceName:      bp.Provider.TerraformType(d.Name),
		GoTypeName:          d.GoTypeName,
		ModelTypeName:       d.ModelTypeName,
		ConstructorFn:       "New" + d.GoTypeName,
		SDKClientType:       bp.Provider.SDK.ClientType,
		MarkdownDescription: d.Schema.MarkdownDescription,
		ReadTimeout: pickTimeout(
			d.Timeouts.ReadSeconds,
			bp.Provider.Conventions.DefaultTimeouts.ReadSeconds,
		),
	}

	if d.DocRefURL != "" {
		v.DocRefComment = "// REF: " + d.DocRefURL
	}

	sdk := bp.Provider.SDK
	sup := bp.Provider.Support

	// datasource.go: schema, metadata, configure.
	impDataSource.add(pkgContext, "")
	impDataSource.add(pkgDataSrc, "")
	impDataSource.add(sc.schemaImport(), "")
	impDataSource.add(sdk.ClientImport.Path, sdk.ClientImport.Alias)
	impDataSource.add(sup.Client.Path, sup.Client.Alias)
	impDataSource.add(sup.CommonSchema.Path, sup.CommonSchema.Alias)

	// state.go: the conversions, plus the SDK package whose types appear in generic
	// conversion arguments.
	impState.add(pkgContext, "")
	impState.add(pkgTflog, "")
	impState.add(sup.Convert.Path, sup.Convert.Alias)
	impState.add(d.Binding.Service.ImportPath, d.Binding.Service.Alias)

	// read.go: the one operation.
	impRead.add(pkgContext, "")
	impRead.add(pkgTime, "")
	impRead.add(pkgDataSrc, "")
	impRead.add(pkgTflog, "")
	impRead.add(sup.CRUD.Path, sup.CRUD.Alias)
	impRead.add(sup.Errors.Path, sup.Errors.Alias)

	v.Interfaces = dataSourceInterfaces(d)

	attrs, fields, err := attributes(sc, d.Schema, impDataSource)
	if err != nil {
		return DataSourceView{}, err
	}
	v.SchemaAttributes = attrs

	// The timeouts value is last in the model, as it is for a resource. The type comes
	// from the framework's datasource timeouts package, not the resource one: they are
	// distinct types and mixing them does not compile.
	fields = append(fields, "Timeouts timeouts.Value `tfsdk:\"timeouts\"`")
	v.ModelFields = fields

	if usesElementTypes(d.Schema) {
		impDataSource.add(pkgTypes, "")
	}

	shapes, err := nestedShapes(sc, d.Schema)
	if err != nil {
		return DataSourceView{}, err
	}

	for _, sh := range shapes {
		nm, nmErr := nestedModelView(sh)
		if nmErr != nil {
			return DataSourceView{}, nmErr
		}
		v.NestedModels = append(v.NestedModels, nm)
	}

	if len(shapes) > 0 {
		impState.add(pkgTypes, "")
		impState.add(pkgDiag, "")
	}

	v.State = stateView(d.Schema, d.Binding.Response.Type, shapes)

	if d.Binding.Read == nil {
		return DataSourceView{}, &ErrUnsupported{
			What: sc.what,
			Why:  "a data source with no read operation has nothing to generate",
		}
	}

	read, err := opView(
		sc.what, d.Binding.Service.Accessor,
		*d.Binding.Read, "crud.PhaseRead", "errors.OpRead", "ReadTimeout",
	)
	if err != nil {
		return DataSourceView{}, err
	}
	v.Read = read

	org := bp.Provider.GoModule
	v.Imports = DataSourceImports{
		DataSource: impDataSource.render(org),
		Read:       impRead.render(org),
		State:      impState.render(org),
	}

	return v, nil
}

// dataSourceInterfaces are the framework interfaces a generated data source asserts.
//
// Every data source implements both, so unlike the resource equivalent there is nothing
// conditional here: DataSource is the required method set and DataSourceWithConfigure is
// how the SDK client reaches it, which every generated data source needs.
func dataSourceInterfaces(d blueprint.DataSource) []string {
	return []string{
		fmt.Sprintf("_ datasource.DataSource = &%s{}", d.GoTypeName),
		fmt.Sprintf("_ datasource.DataSourceWithConfigure = &%s{}", d.GoTypeName),
	}
}
