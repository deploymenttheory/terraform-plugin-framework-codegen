package render

import (
	"fmt"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// The framework packages a generated action needs.
const (
	pkgAction       = "github.com/hashicorp/terraform-plugin-framework/action"
	pkgActionSchema = "github.com/hashicorp/terraform-plugin-framework/action/schema"
)

// ActionView is everything the per-action templates need.
//
// Smaller than ResourceView and DataSourceView, and it is the block kind that has the least
// to say: an action reads configuration, calls the API once and reports what happened.
// InvokeRequest carries only a config, and InvokeResponse has no field to return a value in,
// so there is no state to map, nothing to flatten and nothing to reconcile.
type ActionView struct {
	Header        string
	Package       string
	DocRefComment string

	Imports ActionImports

	// ActionName is the Terraform type, e.g. "thousandeyes_disable_endpoint_agent".
	ActionName    string
	GoTypeName    string
	ModelTypeName string
	ConstructorFn string

	SDKClientType string

	MarkdownDescription string

	// InvokeTimeout is the deadline in seconds. One operation, one deadline.
	InvokeTimeout int

	Interfaces []string

	SchemaAttributes []string
	// PatternVars are the package-level regexp declarations the schema references.
	PatternVars  []string
	ModelFields  []string
	NestedModels []NestedModelView

	// Invoke is the SDK call.
	Invoke *OpView
}

// ActionImports holds the rendered import block for each emitted file.
//
// Two files: action.go and invoke.go. There is no construct.go or state.go -- an action sends
// its arguments as call parameters rather than a body, and has no state to map back.
type ActionImports struct {
	Action string
	Invoke string
}

// Action builds the view for one action.
func Action(bp blueprint.Blueprint, a blueprint.Action, opts Options) (ActionView, error) {
	var (
		impAction = newImportSet()
		impInvoke = newImportSet()
	)

	sc := actionScope(a)

	v := ActionView{
		Header:              GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package:             a.GoPackage,
		ActionName:          bp.Provider.TerraformType(a.Name),
		GoTypeName:          a.GoTypeName,
		ModelTypeName:       a.ModelTypeName,
		ConstructorFn:       "New" + a.GoTypeName,
		SDKClientType:       bp.Provider.SDK.ClientType,
		MarkdownDescription: a.Schema.MarkdownDescription,
		// An invoke is a write, so it takes the create deadline rather than the read one.
		InvokeTimeout: pickTimeout(
			a.Timeouts.CreateSeconds,
			bp.Provider.Conventions.DefaultTimeouts.CreateSeconds,
		),
		Interfaces: []string{
			fmt.Sprintf("_ action.Action = (*%s)(nil)", a.GoTypeName),
			fmt.Sprintf("_ action.ActionWithConfigure = (*%s)(nil)", a.GoTypeName),
		},
	}

	if a.DocRefURL != "" {
		v.DocRefComment = "// REF: " + a.DocRefURL
	}

	sdk := bp.Provider.SDK
	sup := bp.Provider.Support

	// action.go: schema, metadata, configure.
	impAction.add(pkgContext, "")
	impAction.add(pkgAction, "")
	impAction.add(sc.schemaImport(), "")
	impAction.add(sdk.ClientImport.Path, sdk.ClientImport.Alias)
	impAction.add(sup.Client.Path, sup.Client.Alias)

	// invoke.go: the one operation.
	impInvoke.add(pkgContext, "")
	impInvoke.add(pkgTime, "")
	impInvoke.add(pkgAction, "")
	impInvoke.add(pkgTflog, "")
	// Deliberately not the crud package: an action has no timeouts block for crud.Timeout to
	// resolve, so the deadline is the generated constant applied with context.WithTimeout.
	impInvoke.add(sup.Errors.Path, sup.Errors.Alias)

	attrs, fields, err := attributes(sc, a.Schema, impAction)
	if err != nil {
		return ActionView{}, err
	}
	v.SchemaAttributes = attrs
	v.PatternVars = sc.patterns.Decls()

	// No timeouts field appended to the model, unlike a resource or a data source. An action
	// has no timeouts block: the framework's action schema has no home for one, so the
	// deadline is the generated constant and nothing overrides it per invocation.
	v.ModelFields = fields

	if usesElementTypes(a.Schema) {
		impAction.add(pkgTypes, "")
	}

	shapes, err := nestedShapes(sc, a.Schema)
	if err != nil {
		return ActionView{}, err
	}

	for _, sh := range shapes {
		nm, nmErr := nestedModelView(sh)
		if nmErr != nil {
			return ActionView{}, nmErr
		}
		v.NestedModels = append(v.NestedModels, nm)
	}

	if len(shapes) > 0 {
		impAction.add(pkgTypes, "")
	}

	if a.Binding.Invoke == nil {
		return ActionView{}, &ErrUnsupported{
			What: sc.what,
			Why:  "it has no invoke operation, so there is nothing for it to do",
		}
	}

	// discardResult, because InvokeResponse has no field to put a result in. Whatever the
	// SDK hands back is deliberately dropped, and binding it would leave a
	// declared-and-not-used variable.
	invoke, err := opView(
		sc.what, a.Binding.Service.Accessor,
		*a.Binding.Invoke, "crud.PhaseCreate", "errors.OpCreate", "InvokeTimeout", discardResult,
	)
	if err != nil {
		return ActionView{}, err
	}
	v.Invoke = invoke

	org := bp.Provider.GoModule
	v.Imports = ActionImports{
		Action: impAction.render(org),
		Invoke: impInvoke.render(org),
	}

	return v, nil
}

// actionScope names an action for the error messages attribute rendering produces.
func actionScope(a blueprint.Action) schemaScope {
	return schemaScope{
		kind:     blueprint.BlockKindAction,
		what:     fmt.Sprintf("action %q", a.Key),
		patterns: newPatternVars(),
	}
}
