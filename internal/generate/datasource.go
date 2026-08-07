package generate

import (
	"fmt"
	"strings"

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
	// PatternVars are the package-level regexp declarations the schema references.
	PatternVars  []string
	ModelFields  []string
	NestedModels []NestedModelView

	// Read is the SDK call, and State is the flatten function it feeds.
	Read  *OpView
	State StateView

	// Resolve is the selector machinery: the list call that narrows a lookup to
	// exactly one element. Nil for a plain single-operation data source.
	Resolve *ResolveView
}

// ResolveView renders the list-then-match resolver.
//
// The contract it renders: exactly one selector must be set; anything other
// than the direct identifier fetches the list and filters it, and zero or
// several matches are both errors -- a lookup must be predictable, never a
// guess. With a direct read present the matched element only supplies the
// identifier; without one the matched element is the state source itself.
type ResolveView struct {
	// IDGoField is the identifier attribute's model field, e.g. "ID". Empty in
	// MapsElement mode, which has no direct read to feed.
	IDGoField string

	// AllSelectorGoFields drive the exactly-one count; SelectorList names
	// the attributes in the diagnostic, comma-joined.
	AllSelectorGoFields []string
	SelectorList        string

	// Matchers are the non-identifier selectors: each compares a configured
	// attribute against a getter on the list element.
	Matchers []MatcherView

	// List is the list call, bound to the "listing" variable.
	List *OpView
	// CollectionField reaches the elements, e.g. "GetTags()".
	CollectionField string
	// ElementType is the element's Go type, for the matches slice.
	ElementType string

	// ElementIDExpr converts the matched element's identifier into the id
	// attribute, e.g. `convert.PtrStringToFramework(match.GetId())`.
	ElementIDExpr string
	// MapsElement is the no-direct-read mode: the matched element maps to
	// state directly and the function returns inside the resolver.
	MapsElement bool
}

// MatcherView is one selector comparison.
type MatcherView struct {
	// GoField is the configured attribute's model field.
	GoField string
	// Getter is the element access expression, e.g. `el.GetTestName()`, and the
	// thing the presence guard nil-checks.
	Getter string
	// Mismatch is the finished "this element is not the one" expression, e.g.
	// `*el.GetTestName() != data.TestName.ValueString()`. It is computed here
	// rather than assembled in the template because what reads correctly
	// depends on both sides' types, and a template that branched on those would
	// be the type switch this toolkit keeps in Go.
	Mismatch string
	// AttrName names the attribute in diagnostics.
	AttrName string
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
	v.PatternVars = sc.patterns.Decls()

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

	state, err := stateView(d.Schema, d.Binding.Response.Type, d.Binding.Response.AccessStyle, shapes)
	if err != nil {
		return DataSourceView{}, err
	}
	v.State = state

	if v.State.NeedsTypes {
		impState.add(pkgTypes, "")
	}
	// The fallible mapping declares diag.Diagnostics whatever nested shapes
	// exist; without this, a flat data source whose conversions can fail
	// imported everything but the package its own signature names.
	if v.State.NeedsDiagnostics {
		impState.add(pkgDiag, "")
	}

	if d.Binding.Read == nil && d.Binding.List == nil {
		return DataSourceView{}, &ErrUnsupported{
			What: sc.what,
			Why:  "a data source with no read operation has nothing to generate",
		}
	}

	// Every call a data source renders lands in read.go, so one scope serves the
	// direct read and the resolver's list alike.
	as := newArgScope(d.Schema.Attributes, impRead)

	if d.Binding.Read != nil {
		read, err := opView(
			sc.what, d.Binding.Service.Accessor,
			*d.Binding.Read, "crud.PhaseRead", "errors.OpRead", "ReadTimeout", bindsResult, as,
		)
		if err != nil {
			return DataSourceView{}, err
		}
		v.Read = read
	}

	if d.Binding.List != nil {
		resolve, err := resolveView(sc.what, d, as)
		if err != nil {
			return DataSourceView{}, err
		}
		v.Resolve = resolve
		// The resolver's diagnostics count matches and quote selectors.
		impRead.add("fmt", "")
		if resolve.ElementIDExpr != "" {
			impRead.add(sup.Convert.Path, sup.Convert.Alias)
		}
		impRead.add(d.Binding.Service.ImportPath, d.Binding.Service.Alias)
	}

	org := bp.Provider.GoModule
	v.Imports = DataSourceImports{
		DataSource: impDataSource.render(org),
		Read:       impRead.render(org),
		State:      impState.render(org),
	}

	return v, nil
}

// resolveView builds the selector resolver from the binding.
func resolveView(what string, d blueprint.DataSource, as argScope) (*ResolveView, error) {
	b := d.Binding

	list, err := opView(
		what, b.Service.Accessor,
		*b.List, "crud.PhaseRead", "errors.OpRead", "ReadTimeout", bindsResult, as,
	)
	if err != nil {
		return nil, err
	}
	// The direct read owns "remote"; the resolver's fetch is the listing.
	list.ResultVar = "listing"
	list.Assign = "listing, err :="

	v := &ResolveView{
		List:            list,
		CollectionField: b.CollectionField,
		ElementType:     b.ElementType,
		MapsElement:     b.Read == nil,
	}

	var names []string

	goFieldOf := func(attr string) string {
		for _, a := range d.Schema.Attributes {
			if a.Name == attr {
				return a.GoField
			}
		}
		return ""
	}

	for _, s := range b.Selectors {
		goField := s.GoField
		if goField == "" {
			goField = goFieldOf(s.Attribute)
		}
		v.AllSelectorGoFields = append(v.AllSelectorGoFields, goField)
		names = append(names, s.Attribute)

		if s.ViaRead {
			v.IDGoField = goField
			continue
		}
		getter := readExpr(b.Response.AccessStyle, "el", s.SDKField)
		mismatch, err := matcherMismatch(what, getter, goField, s, as)
		if err != nil {
			return nil, err
		}
		v.Matchers = append(v.Matchers, MatcherView{
			GoField:  goField,
			Getter:   getter,
			Mismatch: mismatch,
			AttrName: s.Attribute,
		})
	}

	v.SelectorList = strings.Join(names, ", ")

	if !v.MapsElement {
		if v.IDGoField == "" {
			return nil, &ErrUnsupported{
				What: what,
				Why:  "a list-resolved data source with a direct read declares no viaRead selector, so the resolver has nowhere to put the identifier",
			}
		}
		v.ElementIDExpr = convertExpr(*b.ElementIDFlatten,
			readExpr(b.Response.AccessStyle, "match", b.ElementIDField))
	}

	return v, nil
}

// matcherMismatch renders one selector's "this element is not the one" test.
//
// The configured value is read at the attribute's own framework type, and the
// element's value at whatever the SDK actually hands back. Those two agree only
// for a plain scalar field: a generated SDK holds a documented enumeration as a
// named type of its own, and comparing that against a string is not a wrong
// answer but a compile error.
//
// A kiota enumeration is int-backed and carries String(), so the comparison
// goes through it -- the same reading the attribute's own flatten already
// takes, convert.PtrStringerToFramework. The nil guard the template renders
// beside this expression short-circuits first, so reaching String() through the
// pointer is safe.
func matcherMismatch(
	what, getter, goField string,
	s blueprint.Selector,
	as argScope,
) (string, error) {
	configured, err := as.valueExpr(what, "data", goField)
	if err != nil {
		return "", err
	}
	if !s.SDKEnum {
		return fmt.Sprintf("*%s != %s", getter, configured), nil
	}
	if kind, known := as.kindOf(goField); known && kind != blueprint.KindString {
		return "", &ErrUnsupported{
			What: fmt.Sprintf("selector %q of %s", s.Attribute, what),
			Why: fmt.Sprintf(
				"the SDK holds it as an enumeration, whose String() yields a string, "+
					"but the attribute is %q", kind),
		}
	}
	return fmt.Sprintf("%s.String() != %s", getter, configured), nil
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
