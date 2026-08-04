package generate

import (
	"fmt"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// The framework packages a generated ephemeral resource needs.
const (
	pkgEphemeral       = frameworkRoot + "ephemeral"
	pkgEphemeralSchema = frameworkRoot + "ephemeral/schema"
)

// EphemeralView is everything the per-ephemeral templates need.
//
// A sibling of DataSourceView, for the same reason that is a sibling of ResourceView: an
// ephemeral is structurally a data source whose result lives outside state -- one
// operation, no request body, no plan -- and the fields the other kinds need were never
// applicable rather than being absent.
type EphemeralView struct {
	Header        string
	Package       string
	DocRefComment string

	Imports EphemeralImports

	// EphemeralName is the Terraform type, e.g. "thousandeyes_credential".
	EphemeralName string
	GoTypeName    string
	ModelTypeName string
	ConstructorFn string

	SDKClientType string

	MarkdownDescription string

	// OpenTimeout is the open deadline in seconds. Open is a read, and the one
	// operation this toolkit renders; there is no timeouts block on an ephemeral
	// schema, so the deadline is the generated constant applied with
	// context.WithTimeout, as an action's is.
	OpenTimeout int

	Interfaces []string

	SchemaAttributes []string
	PatternVars      []string
	ModelFields      []string
	NestedModels     []NestedModelView

	// Open is the SDK call, and State is the flatten function it feeds.
	Open  *OpView
	State StateView
}

// EphemeralImports holds the rendered import block for each emitted file.
type EphemeralImports struct {
	Ephemeral string
	Open      string
	State     string
}

// Ephemeral builds the view for one ephemeral resource.
func Ephemeral(
	bp blueprint.Blueprint,
	e blueprint.Ephemeral,
	opts Options,
) (EphemeralView, error) {
	var (
		impEphemeral = newImportSet()
		impOpen      = newImportSet()
		impState     = newImportSet()
	)

	sc := ephemeralScope(e)

	v := EphemeralView{
		Header:              GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package:             e.GoPackage,
		EphemeralName:       bp.Provider.TerraformType(e.Name),
		GoTypeName:          e.GoTypeName,
		ModelTypeName:       e.ModelTypeName,
		ConstructorFn:       "New" + e.GoTypeName,
		SDKClientType:       bp.Provider.SDK.ClientType,
		MarkdownDescription: e.Schema.MarkdownDescription,
		OpenTimeout: pickTimeout(
			e.Timeouts.ReadSeconds,
			bp.Provider.Conventions.DefaultTimeouts.ReadSeconds,
		),
		Interfaces: []string{
			fmt.Sprintf("_ ephemeral.EphemeralResource = (*%s)(nil)", e.GoTypeName),
			fmt.Sprintf("_ ephemeral.EphemeralResourceWithConfigure = (*%s)(nil)", e.GoTypeName),
		},
	}

	if e.DocRefURL != "" {
		v.DocRefComment = "// REF: " + e.DocRefURL
	}

	// Renew and Close are modelled in the IR and refused by validation until a pilot
	// needs them; re-checked here so render alone cannot emit an ephemeral that claims a
	// lifecycle nothing generates.
	if e.Binding.Renew != nil || e.Binding.Close != nil {
		return EphemeralView{}, &ErrUnsupported{
			What: sc.what,
			Why:  "renew and close are modelled but not yet rendered",
		}
	}
	if e.Binding.Open == nil {
		return EphemeralView{}, &ErrUnsupported{
			What: sc.what,
			Why:  "it has no open operation, and opening the value is the one thing it does",
		}
	}

	sdk := bp.Provider.SDK
	sup := bp.Provider.Support

	impEphemeral.add(pkgContext, "")
	impEphemeral.add(pkgEphemeral, "")
	impEphemeral.add(sc.schemaImport(), "")
	impEphemeral.add(sdk.ClientImport.Path, sdk.ClientImport.Alias)
	impEphemeral.add(sup.Client.Path, sup.Client.Alias)

	impOpen.add(pkgContext, "")
	impOpen.add(pkgTime, "")
	impOpen.add(pkgEphemeral, "")
	impOpen.add(pkgTflog, "")
	impOpen.add(sup.Errors.Path, sup.Errors.Alias)

	attrs, fields, err := attributes(sc, e.Schema, impEphemeral)
	if err != nil {
		return EphemeralView{}, err
	}
	v.SchemaAttributes = attrs
	v.PatternVars = sc.patterns.Decls()
	v.ModelFields = fields

	shapes, err := nestedShapes(sc, e.Schema)
	if err != nil {
		return EphemeralView{}, err
	}
	for _, sh := range shapes {
		nm, nmErr := nestedModelView(sh)
		if nmErr != nil {
			return EphemeralView{}, nmErr
		}
		v.NestedModels = append(v.NestedModels, nm)
	}
	if len(shapes) > 0 {
		impEphemeral.add(pkgTypes, "")
	}

	open, err := opView(
		sc.what, e.Binding.Service.Accessor,
		*e.Binding.Open, "crud.PhaseRead", "errors.OpRead", "OpenTimeout", bindsResult,
	)
	if err != nil {
		return EphemeralView{}, err
	}
	v.Open = open

	state, err := ephemeralStateView(bp, e, impState)
	if err != nil {
		return EphemeralView{}, err
	}
	v.State = state

	org := bp.Provider.GoModule
	v.Imports = EphemeralImports{
		Ephemeral: impEphemeral.render(org),
		Open:      impOpen.render(org),
		State:     impState.render(org),
	}

	return v, nil
}

// ephemeralScope names an ephemeral for the error messages attribute rendering produces.
func ephemeralScope(e blueprint.Ephemeral) schemaScope {
	return schemaScope{
		kind:     blueprint.BlockKindEphemeral,
		what:     fmt.Sprintf("ephemeral %q", e.Key),
		patterns: newPatternVars(),
	}
}

// ephemeralStateView builds the flatten function's view, exactly as a data source's is
// built: every attribute with a flatten conversion is mapped from the open response.
func ephemeralStateView(
	bp blueprint.Blueprint,
	e blueprint.Ephemeral,
	imports *importSet,
) (StateView, error) {
	v := StateView{ResponseType: e.Binding.Response.Type}

	sup := bp.Provider.Support
	imports.add(pkgContext, "")
	imports.add(pkgTflog, "")
	imports.add(sup.Convert.Path, sup.Convert.Alias)
	imports.add(e.Binding.Service.ImportPath, e.Binding.Service.Alias)

	for _, a := range e.Schema.Attributes {
		if a.Drop || a.Wire.SkipFlatten {
			continue
		}
		if a.Wire.Flatten == nil {
			continue
		}

		if a.Wire.Flatten.ReturnsError {
			v.NeedsDiagnostics = true
			v.Assignments = append(v.Assignments, fmt.Sprintf(
				"data.%s, d = %s\ndiags.Append(d...)",
				a.GoField, convertExpr(*a.Wire.Flatten, "remote."+a.Wire.SDKField)))
			continue
		}

		v.Assignments = append(v.Assignments, fmt.Sprintf("data.%s = %s",
			a.GoField, convertExpr(*a.Wire.Flatten, "remote."+a.Wire.SDKField)))
	}

	if v.NeedsDiagnostics {
		imports.add(pkgDiag, "")
	}

	return v, nil
}
