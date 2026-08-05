// Package render turns a blueprint into the finished strings the templates
// consume.
//
// All the logic lives here. A template contains no conditionals over meaning,
// only over presence: it either writes a precomputed string or it does not. That
// division is inherited from the sibling SDK generator, and it is what keeps the
// emitted shape reviewable as ordinary text in internal/templates without having
// to read the generator to know what it produces.
//
// Every exported view field is therefore a finished Go expression, a finished
// declaration, or a boolean.
package generate

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// Standard library and framework import paths the emitted files need.
const (
	pkgContext   = "context"
	pkgFmt       = "fmt"
	pkgTime      = "time"
	pkgPath      = "github.com/hashicorp/terraform-plugin-framework/path"
	pkgResource  = "github.com/hashicorp/terraform-plugin-framework/resource"
	pkgDataSrc   = "github.com/hashicorp/terraform-plugin-framework/datasource"
	pkgTypes     = "github.com/hashicorp/terraform-plugin-framework/types"
	pkgTimeouts  = "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	pkgTflog     = "github.com/hashicorp/terraform-plugin-log/tflog"
	pkgPlanModif = "github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	pkgStringPM  = "github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	pkgAttr      = "github.com/hashicorp/terraform-plugin-framework/attr"
	pkgDiag      = "github.com/hashicorp/terraform-plugin-framework/diag"
	pkgBaseTypes = "github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	// frameworkRoot is the prefix each kind's schema package hangs off. The suffix
	// comes from BlockKind.SchemaPackage, so the two never disagree.
	frameworkRoot = "github.com/hashicorp/terraform-plugin-framework/"
)

// schemaScope is what attribute rendering needs to know about the block it is
// rendering for.
//
// The attribute types in each kind's schema package are structurally identical, so one
// renderer serves every kind rather than one per kind. Two things differ, and this
// carries both: which import path the generated `schema` selector resolves to, and which
// fields the kind's attribute struct actually has.
//
// The selector itself stays `schema` for every kind. That is not a shortcut -- each block
// is emitted into its own package directory, so a resource's `schema` and a data source's
// `schema` never appear in the same file and there is nothing to disambiguate.
type schemaScope struct {
	kind blueprint.BlockKind
	// access is how the binding's SDK models expose their fields, threaded to
	// every nested shape built under this scope.
	access blueprint.AccessStyle
	// what names the block in an error message, e.g. `resource "tag"`.
	what string
	// patterns collects the package-level regexp vars this schema's RegexMatches validators
	// reference. Per-schema state, like the kind, rather than a parameter threaded through
	// every attribute call site.
	patterns *patternVars
	// idAttribute is the attribute holding the object's identifier, or "" for a kind that has
	// none. It is the one attribute UseStateForUnknown can be applied to safely -- see
	// planModifiersFor.
	idAttribute string
	// replaceOnly is true when the resource's API has no update operation, so every
	// writable attribute must force replacement -- see planModifiersFor.
	replaceOnly bool
}

// schemaImport is the framework import path this scope's `schema` selector resolves to.
func (sc schemaScope) schemaImport() string {
	return frameworkRoot + sc.kind.SchemaPackage()
}

// resourceScope and dataSourceScope name a block for the error messages attribute
// rendering produces.
func resourceScope(r blueprint.Resource) schemaScope {
	return schemaScope{
		kind:        blueprint.BlockKindResource,
		access:      r.Binding.Body.AccessStyle,
		what:        fmt.Sprintf("resource %q", r.Key),
		patterns:    newPatternVars(),
		idAttribute: r.Binding.ID.Attribute,
		replaceOnly: r.Policy.UpdateStyle == blueprint.UpdateReplaceOnly,
	}
}

func dataSourceScope(d blueprint.DataSource) schemaScope {
	return schemaScope{
		kind:     blueprint.BlockKindDataSource,
		access:   d.Binding.Response.AccessStyle,
		what:     fmt.Sprintf("data source %q", d.Key),
		patterns: newPatternVars(),
	}
}

// ResourceView is everything the per-resource templates need.
type ResourceView struct {
	// Header is the generated-file marker. Its wording is not cosmetic: the house
	// lint configuration only skips files matching Go's canonical form.
	Header string
	// Package is the Go package name for the resource's own directory.
	Package string
	// DocRefComment is the "// REF: <url>" line placed above the package clause,
	// as the archetype provider does, or empty.
	DocRefComment string

	// Imports holds one rendered import block per emitted file.
	//
	// They are separate because the files need different sets, and Go rejects an
	// unused import. A single shared block would fail to compile in most of them.
	Imports FileImports

	ResourceName  string // the Terraform type, e.g. "thousandeyes_tag"
	GoTypeName    string // e.g. "TagResource"
	ModelTypeName string // e.g. "TagResourceModel"
	ConstructorFn string // e.g. "NewTagResource"

	// SDKClientType is the client field's type, e.g. "*thousandeyes.Client".
	SDKClientType string
	// ServiceAccessor reaches the SDK service from the receiver.
	ServiceAccessor string
	// IDField is the model field holding the resource identifier, which update
	// carries over from prior state because the plan's copy is computed.
	IDField string

	MarkdownDescription string

	Timeouts TimeoutsView

	// Interfaces are the framework interfaces the resource asserts, rendered as
	// complete var lines.
	Interfaces []string

	// SchemaAttributes are finished `"name": schema.XAttribute{...}` entries.
	SchemaAttributes []string
	// PatternVars are finished package-level `var x = regexp.MustCompile(...)` declarations
	// that the schema's RegexMatches validators reference.
	PatternVars []string
	// ModelFields are finished struct field declarations.
	ModelFields []string
	// NestedModels are the sibling model structs a nested attribute needs. They
	// are siblings rather than inner types because the framework decodes elements
	// into a named type.
	NestedModels []NestedModelView

	// ImportState renders the ImportState method body, or is empty when the
	// resource does not support import.
	ImportState string

	// Identity is the resource identity schema, or nil when the resource declares none.
	Identity *IdentityView
	// List is the list-resource facet, or nil when the resource declares none.
	List *ListView

	// ConfigValidators are finished cross-attribute rule expressions.
	ConfigValidators []string
	// SchemaVersion is the resource schema's version, emitted only when past zero.
	//
	// Zero is the framework's default, and emitting "Version: 0" would be noise -- but a
	// nonzero value not reaching the schema is worse than noise. Terraform would keep reading
	// state as version 0, find it matching, pass it through untouched, and reinterpret old
	// fields under the new schema with no upgrade and no error.
	SchemaVersion int64
	// PriorVersions is every version an upgrader must handle: 0 up to SchemaVersion-1.
	//
	// All of them, not just the most recent. fwserver looks up the map by the version found in
	// state, and a practitioner who skipped a provider release is holding an older one -- a
	// missing key gives them "Terraform was expecting an implementation for version N upgrade"
	// and no way forward.
	PriorVersions []int64

	// Hooks are the hand-written seams this resource opts into. Generated code refers to a
	// hook only when its flag is set, so a scaffold deleted by hand takes the reference with
	// it rather than leaving a package that does not compile.
	Hooks blueprint.Hooks

	// Construct and State are the finished bodies of the expand and flatten
	// functions.
	Construct ConstructView
	State     StateView

	CRUD CRUDView
}

// FileImports holds the rendered import block for each emitted file.
type FileImports struct {
	Resource  string
	Construct string
	State     string
	CRUD      string
}

// TimeoutsView holds the four timeout constants in seconds.
type TimeoutsView struct {
	Create, Read, Update, Delete int
}

// ConstructView is the expand function.
type ConstructView struct {
	RequestType     string
	ConstructorExpr string
	// Assignments are finished statements assigning to the request body.
	Assignments []string
	// NestedAttributeObject are the per-shape expand helpers this resource needs.
	NestedObject []NestedFuncView
	// NeedsDiagnostics is true when any assignment can fail, which decides
	// whether the generated function returns diagnostics at all.
	NeedsDiagnostics bool

	// Update is set when the SDK splits the update request type from create's. The
	// template then emits a second construct function over the *same* assignment
	// list -- the split types are field-name clones, and sdkbind has proven every
	// assigned field exists on both -- and the generated update calls it instead.
	Update *ConstructTarget
}

// ConstructTarget is the type one construct function builds.
type ConstructTarget struct {
	RequestType     string
	ConstructorExpr string
	// Assignments are the update body's own statements. They differ from the
	// create list exactly where an attribute declares UpdateExpand -- an SDK whose
	// update type spells a field differently from create's.
	Assignments []string
}

// StateView is the flatten function.
type StateView struct {
	ResponseType string
	// Assignments are finished statements assigning to the model.
	Assignments []string
	// NestedAttributeObject are the per-shape flatten helpers this resource needs.
	NestedObject     []NestedFuncView
	NeedsDiagnostics bool
	// NeedsTypes is set when an assignment references the types package, which happens for a
	// null constructor even in a resource that has no nested shapes at all.
	NeedsTypes bool
}

// NestedModelView is one generated nested model, its attr.Type map and its
// object type.
type NestedModelView struct {
	GoTypeName string
	// Fields are finished struct field declarations.
	Fields []string
	// AttrTypesVar and AttrTypeEntries render the attr.Type map that both the
	// expand and flatten helpers reference, so the shape is declared once.
	AttrTypesVar     string
	AttrTypeEntries  []string
	ObjectTypeVar    string
	CollectionGoType string
}

// NestedFuncView is one generated expand or flatten helper.
type NestedFuncView struct {
	FuncName string
	// FrameworkType is the framework value the helper converts to or from.
	FrameworkType string
	SDKType       string
	ObjectTypeVar string
	ModelType     string
	// IsCollection distinguishes a helper over many objects from one over a
	// single object.
	IsCollection bool
	// Container is the framework container type a collection helper builds: "Set" or
	// "List". The flatten side needs it because types.SetNull and types.ListNull are
	// different functions returning different types, and the model field's type comes
	// from the attribute kind -- so hardcoding one of them emits a list_nested
	// attribute whose helper does not compile against its own model.
	Container string
	// ConstructorExpr builds one element under a setter-based SDK; empty means
	// a zero-value declaration of SDKType, the struct-field dialect.
	ConstructorExpr string
	// SDKSingleType is the single-object form handed around: a pointer to the
	// model for a struct-field SDK, the bare interface for a method-access one
	// -- pointering an interface would break every call site.
	SDKSingleType string
	// ItemRef is how a finished single element is returned: "&item" when item
	// is a value, "item" when the constructor already yielded the interface.
	ItemRef string
	// InRef is how a single-object flatten reads its parameter: "*in" when the
	// helper receives *Model, "in" when it receives the bare interface.
	InRef string
	// SharedDiag is true when an assignment uses the enclosing shared d, which
	// is what makes declaring `var d` outside a loop body load-bearing; the
	// temp forms declare their own.
	SharedDiag bool
	// Assignments are finished per-field statements inside the helper.
	Assignments []string
	// NeedsDiagnostics is true when a field conversion inside the helper can fail.
	NeedsDiagnostics bool
}

// CRUDView holds one entry per operation the resource supports.
type CRUDView struct {
	Create *OpView
	Read   *OpView
	Update *OpView
	Delete *OpView

	// ReplaceOnly is true when the API has no update operation. Update above is nil, and
	// the template emits a stub instead: the framework's interface still demands the
	// method, but every writable attribute carries RequiresReplace, so Terraform plans a
	// replacement and never calls it. Reaching the stub is a bug, and it says so.
	ReplaceOnly bool

	// IDAssign is the finished line that writes the created object's identifier
	// into the model, e.g. "plan.ID = convert.PtrStringToFramework(created.ID)".
	IDAssign string

	// ReadBack fields configure the re-read that Create and Update delegate to.
	//
	// The delegation itself is unconditional: it is what gives state mapping a single
	// call site, which is the point. These only decide the retry budget and what the
	// generated comment says about it.
	//
	// ReadBackMeasured distinguishes a budget the prober established from the built-in
	// fallback, because a retry loop with no stated reason is indistinguishable from
	// cargo cult -- so when it is false the comment says the consistency was never
	// measured rather than inventing a justification.
	ReadBackMeasured   bool
	ReadBackReason     string
	ReadBackMaxRetries int
	ReadBackIntervalMS int

	// DeleteToleratesNotFound makes a 404 on delete a success.
	DeleteToleratesNotFound bool
}

// OpView is one generated SDK call.
type OpView struct {
	// Call is the finished call expression, without the assignment.
	Call string
	// Assign is the left-hand side plus ":=", e.g. "created, _, err :=".
	Assign string
	// HasResult reports whether Assign binds a result variable.
	HasResult bool
	// ResultVar is the bound result variable name, when there is one.
	ResultVar string
	// TimeoutConst is the constant naming this operation's timeout.
	TimeoutConst string
	// Phase is the crud package's phase constant for this operation.
	Phase string
	// ErrorOp is the errors package's operation constant.
	ErrorOp string
	// NilResultGuard asks the template to treat a nil result as meaningful: a
	// fluent SDK returns (nil, nil) for an empty success body, so a read maps
	// it as gone and a create refuses to record an object it cannot identify.
	// Always false for a struct-field SDK, whose calls never return nil, nil.
	NilResultGuard bool
}

// GeneratedHeader returns the canonical generated-file marker.
//
// The wording matters twice over: the house golangci configuration sets
// generated exclusions to strict, which only recognises Go's canonical form, and
// gofmt's own tooling looks for the same. It carries the source blueprint and its
// digest, and deliberately carries neither a timestamp nor a tool version --
// either would make every regeneration a diff, which would destroy the drift check.
func GeneratedHeader(blueprintPath, sha256 string) string {
	return fmt.Sprintf(
		"// Code generated by tfpfgen from %s\n// (sha256:%s). DO NOT EDIT.",
		blueprintPath, sha256)
}

// GeneratedHeaderHCL is GeneratedHeader in HCL's comment syntax.
//
// The wording is identical, deliberately. Both the overwrite refusal and the drift check key on
// "DO NOT EDIT." appearing in the file's bytes, so a header that said the same thing in different
// words would leave an emitted .tf unprotected by both.
func GeneratedHeaderHCL(blueprintPath, sha256 string) string {
	return fmt.Sprintf(
		"# Code generated by tfpfgen from %s\n# (sha256:%s). DO NOT EDIT.",
		blueprintPath, sha256)
}

// Options configures rendering.
type Options struct {
	// BlueprintPath and BlueprintSHA256 go into the generated header.
	BlueprintPath   string
	BlueprintSHA256 string
}

// Resource builds the view for one resource.
func Resource(bp blueprint.Blueprint, r blueprint.Resource, opts Options) (ResourceView, error) {
	// One import set per emitted file, since Go rejects an unused import and the
	// five files need genuinely different sets.
	var (
		impResource  = newImportSet()
		impConstruct = newImportSet()
		impState     = newImportSet()
		impCRUD      = newImportSet()
	)

	v := ResourceView{
		Header:              GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package:             r.GoPackage,
		ResourceName:        bp.Provider.TerraformType(r.Name),
		GoTypeName:          r.GoTypeName,
		ModelTypeName:       r.ModelTypeName,
		ConstructorFn:       "New" + r.GoTypeName,
		SDKClientType:       bp.Provider.SDK.ClientType,
		ServiceAccessor:     r.Binding.Service.Accessor,
		IDField:             r.Binding.ID.GoField,
		MarkdownDescription: r.Schema.MarkdownDescription,
		Timeouts:            timeoutsView(r, bp.Provider.Conventions.DefaultTimeouts),
	}

	if r.DocRefURL != "" {
		v.DocRefComment = "// REF: " + r.DocRefURL
	}

	sc := resourceScope(r)
	sdk := bp.Provider.SDK
	sup := bp.Provider.Support

	// resource.go: schema, metadata, configure, import.
	impResource.add(pkgContext, "")
	impResource.add(pkgResource, "")
	impResource.add(sc.schemaImport(), "")
	impResource.add(sdk.ClientImport.Path, sdk.ClientImport.Alias)
	impResource.add(sup.Client.Path, sup.Client.Alias)
	impResource.add(sup.CommonSchema.Path, sup.CommonSchema.Alias)

	// construct.go and state.go: the conversions, plus the SDK package whose
	// types appear in generic conversion arguments.
	for _, s := range []*importSet{impConstruct, impState} {
		s.add(pkgContext, "")
		s.add(pkgTflog, "")
		s.add(sup.Convert.Path, sup.Convert.Alias)
		s.add(r.Binding.Service.ImportPath, r.Binding.Service.Alias)
	}

	// crud.go: the four operations.
	impCRUD.add(pkgContext, "")
	impCRUD.add(pkgTime, "")
	impCRUD.add(pkgResource, "")
	impCRUD.add(pkgTflog, "")
	impCRUD.add(sup.CRUD.Path, sup.CRUD.Alias)
	impCRUD.add(sup.Errors.Path, sup.Errors.Alias)
	impCRUD.add(sup.Convert.Path, sup.Convert.Alias)
	// readState and readAfterWrite return diagnostics rather than appending to a response,
	// because three callers with different response types share them.
	impCRUD.add(pkgDiag, "")

	if r.Identity != nil {
		identity, err := identityView(r, *r.Identity, impResource)
		if err != nil {
			return ResourceView{}, err
		}
		v.Identity = identity
	}

	if r.List != nil {
		lv, err := listView(bp, r, *r.List)
		if err != nil {
			return ResourceView{}, err
		}
		v.List = lv
	}

	v.Hooks = r.Hooks
	v.SchemaVersion = r.Schema.Version
	for prior := int64(0); prior < r.Schema.Version; prior++ {
		v.PriorVersions = append(v.PriorVersions, prior)
	}

	cvs, err := configValidators(r, impResource)
	if err != nil {
		return ResourceView{}, err
	}

	// Evidence-derived rules render beside the hand-declared ones: to the framework they
	// are the same kind of thing, and a reader of ConfigValidators should meet both.
	conditional, _ := conditionalValidators(r, impResource)
	v.ConfigValidators = append(cvs, conditional...)

	v.Interfaces = interfaces(r)

	if r.Import.Style == blueprint.ImportPassthroughID {
		impResource.add(pkgPath, "")
		v.ImportState = fmt.Sprintf(
			"resource.ImportStatePassthroughID(ctx, path.Root(%q), req, resp)",
			r.Import.Attribute,
		)
	}

	attrs, fields, err := attributes(sc, r.Schema, impResource)
	if err != nil {
		return ResourceView{}, err
	}
	v.SchemaAttributes = attrs
	v.PatternVars = sc.patterns.Decls()

	// The timeouts value is last in the model, matching the archetype, and is what the
	// generated CRUD reads its per-operation deadlines from.
	fields = append(fields, "Timeouts timeouts.Value `tfsdk:\"timeouts\"`")
	v.ModelFields = fields

	// A collection attribute's ElementType expression needs the types package,
	// and a scalar-only resource must not import it.
	if usesElementTypes(r.Schema) {
		impResource.add(pkgTypes, "")
	}

	shapes, err := nestedShapes(sc, r.Schema)
	if err != nil {
		return ResourceView{}, err
	}

	for _, sh := range shapes {
		nm, nmErr := nestedModelView(sh)
		if nmErr != nil {
			return ResourceView{}, nmErr
		}
		v.NestedModels = append(v.NestedModels, nm)
	}

	// A nested shape puts attr.Type maps in model.go, whose imports the template
	// declares itself, and gives the conversion helpers framework values and
	// diagnostics to carry. It adds nothing to crud.go, which only ever appends to
	// resp.Diagnostics. Counted per direction: a write-only shape -- the classic
	// tests' agents, sent on create and never flattened -- gives state.go nothing
	// to use either import for, and an unused import does not compile.
	for _, sh := range shapes {
		if !sh.attr.Wire.SkipExpand {
			impConstruct.add(pkgTypes, "")
			impConstruct.add(pkgDiag, "")
			// A single nested object is decoded with basetypes.ObjectAsOptions,
			// which a collection does not need.
			if sh.attr.Type.Kind == blueprint.KindSingleNested {
				impConstruct.add(pkgBaseTypes, "")
			}
		}
		if !sh.attr.Wire.SkipFlatten {
			impState.add(pkgTypes, "")
			impState.add(pkgDiag, "")
		}
	}

	v.Construct = constructView(r, shapes)
	state, err := stateView(r.Schema, r.Binding.Body.ResponseType, r.Binding.Body.AccessStyle, shapes)
	if err != nil {
		return ResourceView{}, err
	}
	v.State = state

	if v.State.NeedsTypes {
		impState.add(pkgTypes, "")
	}

	// diag arrives with fallible conversions, which is what the templates key the
	// diagnostics-returning signatures on. It used to arrive only via nested
	// shapes, so the first resource whose fallible conversions were all flat --
	// alerts_rule, every nested attribute dropped, sets everywhere -- did not
	// compile.
	if v.Construct.NeedsDiagnostics {
		impConstruct.add(pkgDiag, "")
	}
	if v.State.NeedsDiagnostics {
		impState.add(pkgDiag, "")
	}

	// A fallible conversion anywhere means construct or state returns diagnostics,
	// which changes the shape of the generated CRUD call sites. crud.go needs no
	// extra import for that: it appends to resp.Diagnostics.

	crud, err := crudView(bp, r)
	if err != nil {
		return ResourceView{}, err
	}
	v.CRUD = crud

	// An argument's verbatim expression can name packages crud.go never otherwise
	// imports -- a request option for an expansion-gated read is the live case.
	for _, op := range []*blueprint.Operation{
		r.Binding.Create, r.Binding.Read, r.Binding.Update, r.Binding.Delete,
	} {
		if op == nil {
			continue
		}
		for _, arg := range op.Args {
			for _, imp := range arg.Imports {
				impCRUD.add(imp.Path, imp.Alias)
			}
		}
		// A fluent call carries its arguments on the chain's segments instead.
		for _, seg := range op.Chain {
			for _, arg := range seg.Args {
				for _, imp := range arg.Imports {
					impCRUD.add(imp.Path, imp.Alias)
				}
			}
		}
	}

	// The identifier assignment can name an SDK type in a generic argument --
	// alerts_rule's id flattens through EnumToFramework[alert_rules.RuleId] -- and
	// crud.go otherwise never imports the service package. Gated on actual use:
	// an unused import does not compile.
	if svc := serviceSelector(r.Binding.Service); svc != "" &&
		strings.Contains(crud.IDAssign, svc+".") {
		impCRUD.add(r.Binding.Service.ImportPath, r.Binding.Service.Alias)
	}

	org := bp.Provider.GoModule
	v.Imports = FileImports{
		Resource:  impResource.render(org),
		Construct: impConstruct.render(org),
		State:     impState.render(org),
		CRUD:      impCRUD.render(org),
	}

	return v, nil
}

// usesElementTypes reports whether any attribute renders an ElementType expression, at
// any depth.
//
// The depth matters: those expressions are the only reason a schema file imports the
// framework's types package, and a collection nested inside an object is just as much a
// use as one at the top level. Checking only the top level made the resource compile by
// luck -- its collection happens to be nested, and a separate "has nested shapes" rule
// pulled the import in -- while a data source whose nested objects are all scalars got
// an unused import and did not compile.
func usesElementTypes(s blueprint.Schema) bool {
	for _, a := range s.Attributes {
		if a.Drop {
			continue
		}
		if a.Type.Kind.IsCollection() {
			return true
		}
		if n := a.Type.NestedObject; n != nil &&
			usesElementTypes(blueprint.Schema{Attributes: n.Attributes}) {
			return true
		}
	}
	return false
}

// The retry budget used when the prober has not measured the API's consistency.
//
// Deliberately modest. An API that is read-your-writes consistent -- which the pilot's is
// -- succeeds on the first attempt and pays nothing, so the cost of having the loop is a
// single extra branch. The cost of *not* having it is a create that reports success and
// leaves state that the next read contradicts.
const (
	defaultReadBackRetries    = 10
	defaultReadBackIntervalMS = 2000
)

// pickPositive returns the first positive value, or zero.
func pickPositive(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

// defaultTimeoutSeconds is the deadline used when neither the block nor the provider
// conventions state one. A generated operation always has a bounded context: an
// unbounded one hangs a terraform apply with no diagnostic a practitioner can act on.
const defaultTimeoutSeconds = 180

// pickTimeout returns the first positive value, which is how a block-level timeout falls
// back to the provider default and then to the built-in.
func pickTimeout(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return defaultTimeoutSeconds
}

func timeoutsView(r blueprint.Resource, def blueprint.Timeouts) TimeoutsView {
	return TimeoutsView{
		Create: pickTimeout(r.Timeouts.CreateSeconds, def.CreateSeconds),
		Read:   pickTimeout(r.Timeouts.ReadSeconds, def.ReadSeconds),
		Update: pickTimeout(r.Timeouts.UpdateSeconds, def.UpdateSeconds),
		Delete: pickTimeout(r.Timeouts.DeleteSeconds, def.DeleteSeconds),
	}
}

// interfaces renders the compile-time interface assertions.
//
// They are not decoration: an assertion is what turns "this resource forgot to
// implement ImportState" from a runtime surprise into a build failure.
func interfaces(r blueprint.Resource) []string {
	out := []string{
		fmt.Sprintf("_ resource.Resource = (*%s)(nil)", r.GoTypeName),
		fmt.Sprintf("_ resource.ResourceWithConfigure = (*%s)(nil)", r.GoTypeName),
	}
	if r.Import.Style == blueprint.ImportPassthroughID {
		out = append(
			out,
			fmt.Sprintf("_ resource.ResourceWithImportState = (*%s)(nil)", r.GoTypeName),
		)
	}
	if r.Identity != nil {
		out = append(
			out,
			fmt.Sprintf("_ resource.ResourceWithIdentity = (*%s)(nil)", r.GoTypeName),
		)
	}
	if len(r.ConfigValidators) > 0 || len(requiredWhenRules(r)) > 0 {
		out = append(
			out,
			fmt.Sprintf("_ resource.ResourceWithConfigValidators = (*%s)(nil)", r.GoTypeName),
		)
	}
	// Asserted here even though the method is hand-written, which is the point: the assertion
	// and the scaffold are both driven by the same flag, so a deleted scaffold fails the build
	// with a message naming the interface rather than silently ceasing to modify plans.
	if r.Hooks.ModifyPlan {
		out = append(
			out,
			fmt.Sprintf("_ resource.ResourceWithModifyPlan = (*%s)(nil)", r.GoTypeName),
		)
	}
	if r.Hooks.StateUpgrade {
		out = append(
			out,
			fmt.Sprintf("_ resource.ResourceWithUpgradeState = (*%s)(nil)", r.GoTypeName),
		)
	}
	return out
}

// frameworkSchemaType maps a type kind to its schema attribute type.
var frameworkSchemaType = map[blueprint.TypeKind]string{
	blueprint.KindBool:    "BoolAttribute",
	blueprint.KindString:  "StringAttribute",
	blueprint.KindInt32:   "Int32Attribute",
	blueprint.KindInt64:   "Int64Attribute",
	blueprint.KindFloat32: "Float32Attribute",
	blueprint.KindFloat64: "Float64Attribute",
	blueprint.KindNumber:  "NumberAttribute",
	blueprint.KindList:    "ListAttribute",
	blueprint.KindSet:     "SetAttribute",
	blueprint.KindMap:     "MapAttribute",

	blueprint.KindListNested:   "ListNestedAttribute",
	blueprint.KindSetNested:    "SetNestedAttribute",
	blueprint.KindSingleNested: "SingleNestedAttribute",
}

// frameworkModelType maps a type kind to the model field's Go type.
var frameworkModelType = map[blueprint.TypeKind]string{
	blueprint.KindBool:    "types.Bool",
	blueprint.KindString:  "types.String",
	blueprint.KindInt32:   "types.Int32",
	blueprint.KindInt64:   "types.Int64",
	blueprint.KindFloat32: "types.Float32",
	blueprint.KindFloat64: "types.Float64",
	blueprint.KindNumber:  "types.Number",
	blueprint.KindList:    "types.List",
	blueprint.KindSet:     "types.Set",
	blueprint.KindMap:     "types.Map",

	blueprint.KindListNested:   "types.List",
	blueprint.KindSetNested:    "types.Set",
	blueprint.KindSingleNested: "types.Object",
}

// frameworkElemType maps a scalar kind to its attr.Type expression.
var frameworkElemType = map[blueprint.TypeKind]string{
	blueprint.KindBool:    "types.BoolType",
	blueprint.KindString:  "types.StringType",
	blueprint.KindInt32:   "types.Int32Type",
	blueprint.KindInt64:   "types.Int64Type",
	blueprint.KindFloat32: "types.Float32Type",
	blueprint.KindFloat64: "types.Float64Type",
	blueprint.KindNumber:  "types.NumberType",
}

// ErrUnsupported reports a blueprint construct the emitter cannot render.
//
// It is a hard error rather than a skipped attribute. Silently omitting an
// attribute produces a provider that looks complete and cannot express part of
// the API, which is far worse than refusing to generate.
type ErrUnsupported struct {
	What string
	Why  string
}

func (e *ErrUnsupported) Error() string {
	return fmt.Sprintf("cannot render %s: %s", e.What, e.Why)
}

// attributes renders one schema's attributes and the matching model fields.
//
// It deliberately does not append the timeouts model field. That field's type differs by
// kind -- timeouts.Value for a resource, the datasource/timeouts package's Value for a
// data source -- and appending it here would make a function named "attributes" quietly
// responsible for something that is not an attribute.
func attributes(
	sc schemaScope,
	s blueprint.Schema,
	imports *importSet,
) (attrs, fields []string, err error) {
	for _, a := range s.Attributes {
		if a.Drop {
			continue
		}

		schemaType, ok := frameworkSchemaType[a.Type.Kind]
		if !ok {
			return nil, nil, &ErrUnsupported{
				What: fmt.Sprintf("attribute %q of %s", a.Name, sc.what),
				Why:  fmt.Sprintf("type kind %q has no framework mapping", a.Type.Kind),
			}
		}

		decl, err := attributeDecl(sc, a, schemaType, imports)
		if err != nil {
			return nil, nil, err
		}
		attrs = append(attrs, decl)

		modelType := frameworkModelType[a.Type.Kind]
		fields = append(fields, fmt.Sprintf("%s %s `tfsdk:%q`", a.GoField, modelType, a.Name))
	}

	return attrs, fields, nil
}

func attributeDecl(
	sc schemaScope,
	a blueprint.Attribute,
	schemaType string,
	imports *importSet,
) (string, error) {
	if a.Type.Kind.IsNested() {
		return nestedAttributeDecl(sc, a, imports)
	}

	var b strings.Builder

	fmt.Fprintf(&b, "%q: schema.%s{\n", a.Name, schemaType)

	if a.Type.Kind.IsCollection() {
		if a.Type.ElementType == nil {
			return "", &ErrUnsupported{
				What: fmt.Sprintf("attribute %q", a.Name),
				Why:  "a collection needs an element type",
			}
		}
		elem, ok := frameworkElemType[a.Type.ElementType.Kind]
		if !ok {
			return "", &ErrUnsupported{
				What: fmt.Sprintf("attribute %q", a.Name),
				Why: fmt.Sprintf(
					"element kind %q is not a scalar; nested elements are not yet supported",
					a.Type.ElementType.Kind,
				),
			}
		}
		fmt.Fprintf(&b, "ElementType: %s,\n", elem)
	}

	writeAttributeFlags(&b, a)

	if a.MarkdownDescription != "" {
		fmt.Fprintf(&b, "MarkdownDescription: %s,\n", goStringLit(a.MarkdownDescription))
	}
	if a.DeprecationMessage != "" {
		fmt.Fprintf(&b, "DeprecationMessage: %s,\n", goStringLit(a.DeprecationMessage))
	}

	writeValidators(&b, sc, a, imports)
	writeCustomCodeBlock(
		&b,
		"PlanModifiers",
		"planmodifier."+validatorKind(a.Type.Kind),
		planModifiersFor(sc, a, imports),
		imports,
	)

	if a.Default != nil {
		def, err := defaultExpr(a, imports)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "Default: %s,\n", def)
	}

	b.WriteString("}")

	return b.String(), nil
}

// planModifiersFor returns the plan modifiers an attribute should carry.
//
// UseStateForUnknown is applied to the identifier and to nothing else, and the narrowness is the
// whole point. It tells Terraform to plan the prior state value instead of unknown, which is a
// correctness claim -- "this value cannot change while the resource exists" -- dressed up as a
// tidiness optimisation.
//
// It used to be applied to every computed string, for exactly that tidiness: a computed attribute
// without it shows as "(known after apply)" on every plan. The first live acceptance run showed
// what that costs. `modified_date` is a computed string the server rewrites on every update, so
// the plan carried the prior null, the update returned a timestamp, and Terraform refused the
// result:
//
//	unexpected new value: .modified_date: was null, but now cty.StringVal(...)
//
// No fact in the evidence set distinguishes a computed value the server keeps from one it rewrites
// -- `volatile` means "differs between two identical reads", which a modification timestamp does
// not. So the generator has no basis for the claim, except for the identifier, where it holds
// structurally: an id that changed under Terraform would break far more than a plan.
//
// The trade is deliberate. Without the modifier a plan is noisier; with it wrongly applied, every
// update fails. Noise is recoverable. A blueprint can still declare plan modifiers per attribute
// where somebody knows the value is stable.
// RequiresReplace is synthesised from two sources, with different justifications:
//
// Structurally, when the resource's update style is replaceOnly: the API has no update
// operation, so an in-place change has nowhere to go, and a provider without the modifier
// is simply broken on any change. No evidence is involved -- "there is no update" is a
// property of the binding.
//
// From evidence, when the blueprint says Behaviour.Immutable. The value is triple-gated
// before it gets here: the prober asserts Immutable=true only at Corroborated confidence
// under a protocol with a control update, merge surfaces it as a recommendation rather
// than writing a modifier, and a human commits the blueprint carrying the field -- that
// committed diff is the opt-in this consumes. The failure asymmetry favours emitting:
// a wrong RequiresReplace is visible in `plan` output as "forces replacement" before
// anything is destroyed, while the current alternative -- an in-place update the API
// refuses -- fails after apply has started, or silently diverges.
//
// Hand-declared planModifiers still win outright, in both directions: declaring any
// modifier set replaces the synthesis entirely, which is the per-attribute escape hatch.
func planModifiersFor(
	sc schemaScope,
	a blueprint.Attribute,
	imports *importSet,
) []blueprint.CustomCode {
	// Only a managed resource has a plan to modify, and this is the one place the
	// generator can put a plan modifier somewhere blueprint.Validate cannot see it:
	// the UseStateForUnknown below is synthesised here rather than declared in the
	// blueprint, so nothing upstream would refuse it on a data source.
	if !sc.kind.SupportsPlanModifiers() {
		return nil
	}

	if len(a.PlanModifiers) > 0 {
		return a.PlanModifiers
	}

	var out []blueprint.CustomCode

	if a.ComputedOptionalRequired == blueprint.Computed &&
		a.Type.Kind == blueprint.KindString &&
		a.Name != "" && a.Name == sc.idAttribute {
		imports.add(pkgStringPM, "")
		out = append(out,
			blueprint.CustomCode{SchemaDefinition: "stringplanmodifier.UseStateForUnknown()"})
	}

	writable := a.ComputedOptionalRequired.IsRequired() || a.ComputedOptionalRequired.IsOptional()

	if writable {
		switch {
		case sc.replaceOnly:
			out = append(out, requiresReplace(a, imports,
				"// RequiresReplace: the API has no update operation, so every change to a\n"+
					"// writable attribute must be applied by replacement."))
		case a.Behaviour.Immutable != nil && *a.Behaviour.Immutable:
			out = append(out, requiresReplace(a, imports,
				"// RequiresReplace: the prober corroborated that the API refuses in-place\n"+
					"// changes to this field."))
		}
	}

	return out
}

// requiresReplace renders the modifier for the attribute's kind, with its provenance
// stated where a reader of the schema meets it.
func requiresReplace(a blueprint.Attribute, imports *importSet, comment string) blueprint.CustomCode {
	kind := strings.ToLower(validatorKind(a.Type.Kind))
	imports.add(frameworkRoot+"resource/schema/"+kind+"planmodifier", "")

	return blueprint.CustomCode{
		SchemaDefinition: comment + "\n" + kind + "planmodifier.RequiresReplace()",
	}
}

// writeValidators renders the Validators field, preceded by the note about a documented
// value the API refused when there is one.
//
// Separate from writeCustomCodeBlock because the note belongs to the block rather than to
// any one entry, and threading a comment through a type named CustomCode -- which models
// code a person wrote -- would put a render concern in the IR.
func writeValidators(
	b *strings.Builder,
	sc schemaScope,
	a blueprint.Attribute,
	imports *importSet,
) {
	items := validatorsFor(a, imports, sc.patterns)
	if len(items) == 0 {
		return
	}

	if note := validatorNote(a); note != "" {
		b.WriteString(note + "\n")
	}

	imports.add(pkgValidator, "")

	writeCustomCodeBlock(b, "Validators", "validator."+validatorKind(a.Type.Kind), items, imports)
}

// writeCustomCodeBlock renders a slice of rendered Go expressions as a named
// schema field, registering whatever imports they need.
func writeCustomCodeBlock(
	b *strings.Builder,
	field, elemType string,
	items []blueprint.CustomCode,
	imports *importSet,
) {
	if len(items) == 0 {
		return
	}

	if field == "PlanModifiers" {
		imports.add(pkgPlanModif, "")
	}

	fmt.Fprintf(b, "%s: []%s{\n", field, elemType)
	for _, it := range items {
		fmt.Fprintf(b, "%s,\n", it.SchemaDefinition)
		for _, im := range it.Imports {
			imports.add(im.Path, im.Alias)
		}
	}
	b.WriteString("},\n")
}

// writeAttributeFlags writes the presence and sensitivity flags shared by every
// attribute kind, nested or not.
func writeAttributeFlags(b *strings.Builder, a blueprint.Attribute) {
	if a.ComputedOptionalRequired.IsRequired() {
		b.WriteString("Required: true,\n")
	}
	if a.ComputedOptionalRequired.IsOptional() {
		b.WriteString("Optional: true,\n")
	}
	if a.ComputedOptionalRequired.IsComputed() {
		b.WriteString("Computed: true,\n")
	}
	if a.Sensitive {
		b.WriteString("Sensitive: true,\n")
	}
}

// validatorKind is the framework's per-type sub-package suffix, which both the
// validator and planmodifier packages share.
//
// The nested kinds matter as much as the scalars: a ListNestedAttribute takes
// []validator.List, and the fallthrough to String below would have emitted
// []validator.String against it. That was unreachable while nested attributes silently
// dropped their validators, and became reachable the moment they stopped.
func validatorKind(k blueprint.TypeKind) string {
	switch k {
	case blueprint.KindBool:
		return "Bool"
	case blueprint.KindInt32:
		return "Int32"
	case blueprint.KindInt64:
		return "Int64"
	case blueprint.KindFloat32:
		return "Float32"
	case blueprint.KindFloat64:
		return "Float64"
	case blueprint.KindNumber:
		return "Number"
	case blueprint.KindList:
		return "List"
	case blueprint.KindSet:
		return "Set"
	case blueprint.KindMap:
		return "Map"
	case blueprint.KindListNested:
		return "List"
	case blueprint.KindSetNested:
		return "Set"
	case blueprint.KindSingleNested:
		return "Object"
	default:
		return "String"
	}
}

func defaultExpr(a blueprint.Attribute, imports *importSet) (string, error) {
	switch {
	case a.Default.Custom != nil:
		for _, im := range a.Default.Custom.Imports {
			imports.add(im.Path, im.Alias)
		}
		return a.Default.Custom.SchemaDefinition, nil

	case a.Default.Static != nil:
		pkg := strings.ToLower(validatorKind(a.Type.Kind)) + "default"
		imports.add("github.com/hashicorp/terraform-plugin-framework/resource/schema/"+pkg, "")
		return fmt.Sprintf(
			"%s.Static%s(%s)",
			pkg,
			validatorKind(a.Type.Kind),
			a.Default.Static.Raw,
		), nil

	default:
		return "", &ErrUnsupported{
			What: fmt.Sprintf("default for attribute %q", a.Name),
			Why:  "neither static nor custom is set",
		}
	}
}

// goStringLit renders s as a Go string literal, splitting long values across
// concatenated lines.
//
// The split exists because the house formatter configuration runs golines at 120
// columns: a single long literal would be rewritten by a developer's editor and
// then show up as drift with no source change.
func goStringLit(s string) string {
	const maxChunk = 90

	if len(s) <= maxChunk {
		return strconv.Quote(s)
	}

	var (
		parts []string
		cur   strings.Builder
	)
	for _, word := range strings.Fields(s) {
		if cur.Len() > 0 && cur.Len()+len(word)+1 > maxChunk {
			parts = append(parts, strconv.Quote(cur.String()+" "))
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(word)
	}
	if cur.Len() > 0 {
		parts = append(parts, strconv.Quote(cur.String()))
	}

	return strings.Join(parts, " +\n")
}

func constructView(r blueprint.Resource, shapes []nestedShape) ConstructView {
	v := ConstructView{
		RequestType:     r.Binding.Body.RequestType,
		ConstructorExpr: r.Binding.Body.ConstructorExpr,
	}

	if r.Binding.Body.SplitsUpdateBody() {
		v.Update = &ConstructTarget{
			RequestType:     r.Binding.Body.UpdateRequestType,
			ConstructorExpr: r.Binding.Body.UpdateConstructorExpr,
		}
	}

	for _, sh := range shapes {
		if sh.attr.Wire.SkipExpand {
			continue
		}
		v.NestedObject = append(v.NestedObject, nestedExpandView(sh))
	}

	for _, a := range r.Schema.Attributes {
		if a.Drop || a.Wire.SkipExpand || a.Wire.Expand == nil {
			continue
		}

		// An immutable attribute is create-authoritative: a split update body
		// frequently cannot carry it at all -- the endpoint tests' update
		// models declare a fraction of their create fields -- and even where
		// it could, sending an unchangeable field on update invites the API to
		// refuse what the plan already forbids. RequiresReplace (driven by the
		// same Behaviour.Immutable) owns changes; the update body simply omits
		// the field. The create body below keeps it.
		immutable := a.Behaviour.Immutable != nil && *a.Behaviour.Immutable

		if v.Update != nil && !immutable {
			updateCall := a.Wire.Expand
			if a.Wire.UpdateExpand != nil {
				updateCall = a.Wire.UpdateExpand
			}
			v.Update.Assignments = append(v.Update.Assignments,
				expandAssignment(r.Binding.Body.AccessStyle, *updateCall, a, &v.NeedsDiagnostics))
		}

		v.Assignments = append(v.Assignments,
			expandAssignment(r.Binding.Body.AccessStyle, *a.Wire.Expand, a, &v.NeedsDiagnostics))
	}

	return v
}

// expandAssignment renders one construct statement for a call and attribute.
//
// A fallible conversion becomes two statements plus a diagnostics append, which is
// why the enclosing function has to return diagnostics at all -- the shapes live
// in expandStmt, the one seam that knows how an SDK field is written.
func expandAssignment(
	style blueprint.AccessStyle,
	call blueprint.ConvertCall,
	a blueprint.Attribute,
	needsDiags *bool,
) string {
	return expandStmt(style, "body", a.Wire.SDKField, call,
		"data."+a.GoField, lowerFirst(a.GoField), needsDiags, nil)
}

func stateView(
	s blueprint.Schema,
	responseType string,
	style blueprint.AccessStyle,
	shapes []nestedShape,
) (StateView, error) {
	// The mapper's parameter type: a struct-field SDK hands over a pointer to
	// its model, a method-access SDK hands over the interface its builders
	// return -- pointering an interface would break every call site.
	param := "*" + responseType
	if style == blueprint.AccessMethod {
		param = responseType
	}
	v := StateView{ResponseType: param}

	for _, sh := range shapes {
		if sh.attr.Wire.SkipFlatten {
			continue
		}
		v.NestedObject = append(v.NestedObject, nestedFlattenView(sh))
	}

	for _, a := range s.Attributes {
		if a.Drop || a.Wire.SkipFlatten || a.Wire.Flatten == nil {
			continue
		}

		// A field the API accepts and never returns must not be flattened, or every read
		// overwrites the configured value with the zero one and the next plan reports a diff
		// nobody caused. The IR has said so since the field was added -- "must not be flattened
		// or state blanks on every read" -- and nothing acted on it until a generated
		// acceptance test made the consequence visible.
		//
		// Suppressed from observed behaviour rather than by editing the blueprint, which is the
		// same shape as ValuesClosed suppressing a OneOf: evidence decides at render time, and
		// the curated document keeps saying what the API documents.
		//
		// Skipping the assignment is necessary and not sufficient, which the first live run
		// proved. An optional-and-computed attribute the practitioner left unset is *unknown*
		// during apply, and the framework rejects a provider that returns one:
		//
		//	After the apply operation, the provider still indicated an unknown value for
		//	thousandeyes_tag.test.match_type. All values must be known after apply.
		//
		// So the value is carried through when set and resolved to null when not. Null is the
		// honest answer: nothing configured it and the API will never say what it holds.
		if notReturnedSomewhere(a.Behaviour) {
			null, ok := nullExpr(a.Type)
			if !ok {
				return StateView{}, &ErrUnsupported{
					What: fmt.Sprintf("attribute %q", a.Name),
					Why: fmt.Sprintf(
						"the API never returns it and it is a %s, whose null constructor needs "+
							"an attribute type this does not derive; a nested object the API "+
							"never returns is refused rather than guessed at",
						a.Type.Kind,
					),
				}
			}

			v.NeedsTypes = true
			v.Assignments = append(v.Assignments, fmt.Sprintf(
				"// %[1]s is deliberately not read back: the API accepts it and never returns\n"+
					"// it, so flattening would blank the configured value on every read. An\n"+
					"// unset value still has to resolve, or the apply is rejected for leaving\n"+
					"// it unknown.\n"+
					"if data.%[2]s.IsUnknown() {\n"+
					"\tdata.%[2]s = %[3]s\n"+
					"}",
				a.Name, a.GoField, null))

			continue
		}

		if a.Wire.Flatten.ReturnsError {
			v.NeedsDiagnostics = true
			v.Assignments = append(v.Assignments, fmt.Sprintf(
				"data.%s, d = %s\ndiags.Append(d...)",
				a.GoField, convertExpr(*a.Wire.Flatten, readExpr(style, "remote", a.Wire.SDKField))))
			continue
		}

		v.Assignments = append(v.Assignments, fmt.Sprintf("data.%s = %s",
			a.GoField, convertExpr(*a.Wire.Flatten, readExpr(style, "remote", a.Wire.SDKField))))
	}

	return v, nil
}

// scalarNullExpr is the framework's null constructor for a scalar kind.
//
// Only scalars. types.ListNull and types.ObjectNull take the element or attribute types, which
// are not derivable here, so a collection is reported rather than approximated.
func scalarNullExpr(kind blueprint.TypeKind) (string, bool) {
	modelType, ok := frameworkModelType[kind]
	if !ok || kind.IsNested() || kind == blueprint.KindList ||
		kind == blueprint.KindSet || kind == blueprint.KindMap {
		return "", false
	}

	return modelType + "Null()", true
}

// nullExpr is scalarNullExpr plus the scalar-element collections, whose null
// constructors need the element type. A collection the API never returns was
// hypothetical until the rehearsal watched http_server's headers come back null,
// so the refusal's premise expired; nested kinds stay refused -- their null needs
// an attribute-type map this deliberately does not derive.
func nullExpr(t blueprint.AttrType) (string, bool) {
	if null, ok := scalarNullExpr(t.Kind); ok {
		return null, true
	}

	if (t.Kind == blueprint.KindList || t.Kind == blueprint.KindSet ||
		t.Kind == blueprint.KindMap) && t.ElementType != nil {
		elem, ok := frameworkModelType[t.ElementType.Kind]
		if !ok || t.ElementType.Kind.IsNested() {
			return "", false
		}

		return frameworkModelType[t.Kind] + "Null(" + elem + "Type)", true
	}

	return "", false
}

// convertExpr renders a call into the provider's convert package.
func convertExpr(c blueprint.ConvertCall, arg string) string {
	var b strings.Builder

	if c.TakesAddress {
		arg = "&" + arg
	}

	b.WriteString(c.Func)
	if len(c.TypeArgs) > 0 {
		b.WriteString("[" + strings.Join(c.TypeArgs, ", ") + "]")
	}
	b.WriteString("(")
	if c.NeedsCtx {
		b.WriteString("ctx, ")
	}
	b.WriteString(arg)
	for _, extra := range c.ExtraArgs {
		b.WriteString(", " + extra)
	}
	b.WriteString(")")

	return wrapConverted(c, b.String())
}

// wrapConverted applies a call's Deref and Cast wrappers to a finished expression.
// Split from convertExpr because the fallible forms wrap a temp variable instead.
func wrapConverted(c blueprint.ConvertCall, expr string) string {
	if c.Deref {
		expr = "convert.Deref(" + expr + ")"
	}
	if c.Cast != "" {
		expr = c.Cast + "(" + expr + ")"
	}
	return expr
}

// needsTemp reports whether a fallible call's result must land in a temp before
// its wrappers apply -- a wrapper cannot take a two-value call.
func needsTemp(c blueprint.ConvertCall) bool {
	return c.ReturnsError && (c.Deref || c.Cast != "")
}

func crudView(bp blueprint.Blueprint, r blueprint.Resource) (CRUDView, error) {
	v := CRUDView{
		ReadBackMeasured: r.Policy.ReadBack.Enabled,
		ReadBackReason:   r.Policy.ReadBack.Reason,
		ReadBackMaxRetries: pickPositive(
			r.Policy.ReadBack.MaxRetries, defaultReadBackRetries),
		ReadBackIntervalMS: pickPositive(
			r.Policy.ReadBack.IntervalMS, defaultReadBackIntervalMS),
		DeleteToleratesNotFound: r.Policy.Delete.NotFoundIsSuccess,
		ReplaceOnly:             r.Policy.UpdateStyle == blueprint.UpdateReplaceOnly,
	}

	// The two claims contradict: replaceOnly means "the API has no update", and a bound
	// update operation means it has one. Emitting either reading silently would make the
	// other a lie, so it is refused with both halves named.
	if v.ReplaceOnly && r.Binding.Update != nil {
		return CRUDView{}, &ErrUnsupported{
			What: fmt.Sprintf("resource %q", r.Key),
			Why: "policy.updateStyle is replaceOnly and binding.update is set; an API with " +
				"an update operation cannot be replace-only, so one of the two must go",
		}
	}

	ops := []struct {
		op      *blueprint.Operation
		dst     **OpView
		phase   string
		errOp   string
		timeout string
		bind    bindResult
	}{
		// Create binds its result because the identifier comes from it. Read binds because
		// readState maps it onto state. Update does not: state comes from the read that
		// follows, so its response is unused by design.
		{
			r.Binding.Create, &v.Create, "crud.PhaseCreate", "errors.OpCreate",
			"CreateTimeout", bindsResult,
		},
		{
			r.Binding.Read, &v.Read, "crud.PhaseRead", "errors.OpRead",
			"ReadTimeout", bindsResult,
		},
		{
			r.Binding.Update, &v.Update, "crud.PhaseUpdate", "errors.OpUpdate",
			"UpdateTimeout", discardResult,
		},
		{
			r.Binding.Delete, &v.Delete, "crud.PhaseDelete", "errors.OpDelete",
			"DeleteTimeout", discardResult,
		},
	}

	for _, o := range ops {
		if o.op == nil {
			continue
		}
		view, err := opView(
			fmt.Sprintf("resource %q", r.Key), r.Binding.Service.Accessor,
			*o.op, o.phase, o.errOp, o.timeout, o.bind,
		)
		if err != nil {
			return CRUDView{}, err
		}
		*o.dst = view
	}

	if r.Binding.Create != nil {
		idAttr, ok := findAttribute(r, r.Binding.ID.Attribute)
		if !ok {
			return CRUDView{}, &ErrUnsupported{
				What: fmt.Sprintf("resource %q", r.Key),
				Why: fmt.Sprintf(
					"the ID binding names attribute %q, which does not exist",
					r.Binding.ID.Attribute,
				),
			}
		}
		if idAttr.Wire.Flatten == nil {
			return CRUDView{}, &ErrUnsupported{
				What: fmt.Sprintf("resource %q", r.Key),
				Why:  "the identifier attribute has no flatten conversion, so the created object's ID cannot be stored",
			}
		}
		v.IDAssign = fmt.Sprintf("plan.%s = %s", r.Binding.ID.GoField,
			convertExpr(*idAttr.Wire.Flatten, r.Binding.ID.FromCreate))
	}

	_ = bp

	return v, nil
}

// opView renders one SDK call. what names the owning block for error messages and
// accessor reaches its service, so this serves a resource and a data source alike.
// bindResult says whether the generated call assigns its result to a variable.
//
// Update's response is deliberately unused: state comes from the read that follows, not
// from the write's own response, so binding it would leave a declared-and-not-used
// variable and the generated file would not compile.
type bindResult bool

const (
	bindsResult   bindResult = true
	discardResult bindResult = false
)

func opView(
	what, accessor string,
	op blueprint.Operation,
	phase, errOp, timeout string,
	bind bindResult,
) (*OpView, error) {
	var call string
	switch op.Style {
	case blueprint.CallStyleMethod:
		args := make([]string, 0, len(op.Args))
		for _, a := range op.Args {
			expr, err := argExpr(what, a)
			if err != nil {
				return nil, err
			}
			args = append(args, expr)
		}
		call = fmt.Sprintf("%s.%s(%s)", accessor, op.Method, strings.Join(args, ", "))

	case blueprint.CallStyleFluent:
		// Validation refuses an empty chain, but the emitter must not depend on
		// having been preceded by it: a chainless fluent op would render a bare
		// accessor, which is not a call.
		if len(op.Chain) == 0 {
			return nil, &ErrUnsupported{
				What: fmt.Sprintf("operation of %s", what),
				Why:  "style fluent declares no chain, so there is no call to render",
			}
		}
		var err error
		call, err = chainCall(accessor, op, func(a blueprint.Argument) (string, error) {
			return argExpr(what, a)
		})
		if err != nil {
			return nil, err
		}

	default:
		return nil, &ErrUnsupported{
			What: fmt.Sprintf("operation %q of %s", op.Method, what),
			Why:  fmt.Sprintf("call style %q is not implemented", op.Style),
		}
	}

	v := &OpView{
		Call:           call,
		TimeoutConst:   timeout,
		Phase:          phase,
		ErrorOp:        errOp,
		NilResultGuard: op.Style == blueprint.CallStyleFluent && op.Return.HasResult(),
	}

	// The assignment must match the call's arity exactly, which is why the
	// blueprint records it rather than the emitter guessing from a method name.
	//
	// A result the caller does not use is discarded here rather than bound, because Go
	// refuses a declared-and-unused variable.
	result := func() string {
		if bind {
			return resultVarFor(phase)
		}
		return "_"
	}()

	switch op.Return {
	case blueprint.ReturnResultTransportError:
		v.HasResult, v.ResultVar = bool(bind), result
		v.Assign = fmt.Sprintf("%s, _, err :=", result)
	case blueprint.ReturnResultError:
		v.HasResult, v.ResultVar = bool(bind), result
		v.Assign = fmt.Sprintf("%s, err :=", result)
	case blueprint.ReturnTransportError:
		v.Assign = "_, err :="
	case blueprint.ReturnError:
		v.Assign = "err :="
	default:
		return nil, &ErrUnsupported{
			What: fmt.Sprintf("operation %q of %s", op.Method, what),
			Why:  fmt.Sprintf("return arity %q is not implemented", op.Return),
		}
	}

	return v, nil
}

// chainCall renders a fluent chain: accessor.Seg1(args).Seg2(args)... The
// renderer knows nothing about which generator produced the SDK -- the chain
// is the call, as data, and argRender decides how each argument reads in the
// enclosing scope (a resource body, a test helper's state map).
func chainCall(
	accessor string,
	op blueprint.Operation,
	argRender func(blueprint.Argument) (string, error),
) (string, error) {
	parts := []string{accessor}
	for _, seg := range op.Chain {
		args := make([]string, 0, len(seg.Args))
		for _, a := range seg.Args {
			expr, err := argRender(a)
			if err != nil {
				return "", err
			}
			args = append(args, expr)
		}
		parts = append(parts, seg.Method+"("+strings.Join(args, ", ")+")")
	}
	return strings.Join(parts, "."), nil
}

// resultVarFor names the result variable after the operation, so a generated body
// reads as prose rather than using a bare "result" four times.
func resultVarFor(phase string) string {
	switch phase {
	case "crud.PhaseCreate":
		return "created"
	case "crud.PhaseUpdate":
		return "updated"
	default:
		return "remote"
	}
}

func argExpr(what string, a blueprint.Argument) (string, error) {
	if a.Expr != "" {
		return a.Expr, nil
	}

	switch a.Kind {
	case blueprint.ArgContext:
		return "ctx", nil
	case blueprint.ArgBody:
		return "body", nil
	case blueprint.ArgStateField:
		return fmt.Sprintf("state.%s.ValueString()", a.Field), nil
	case blueprint.ArgPlanField:
		return fmt.Sprintf("plan.%s.ValueString()", a.Field), nil
	case blueprint.ArgConfigField:
		// A data source reads its arguments from configuration: it has no prior state
		// and no plan. The variable is named for what it holds rather than reusing
		// "state", which would read as a lie in a generated data source body.
		return fmt.Sprintf("data.%s.ValueString()", a.Field), nil
	case blueprint.ArgLiteral:
		return "", &ErrUnsupported{
			What: fmt.Sprintf("argument of %s", what),
			Why:  "a literal argument needs an expression",
		}
	default:
		return "", &ErrUnsupported{
			What: fmt.Sprintf("argument of %s", what),
			Why:  fmt.Sprintf("argument kind %q is not implemented", a.Kind),
		}
	}
}

func findAttribute(r blueprint.Resource, name string) (blueprint.Attribute, bool) {
	for _, a := range r.Schema.Attributes {
		if a.Name == name {
			return a, true
		}
	}
	return blueprint.Attribute{}, false
}

// -----------------------------------------------------------------------------
// imports
// -----------------------------------------------------------------------------

// importSet collects imports and renders them in the three groups the house gci
// configuration expects: standard library, third party, then this organisation.
//
// Emitting them in that order matters more than it looks: the target repository's
// formatter regroups imports on save, so output that is merely valid rather than
// already-grouped shows up as drift the next time anyone opens the file.
type importSet struct {
	seen map[string]string
}

func newImportSet() *importSet {
	return &importSet{seen: map[string]string{}}
}

func (s *importSet) add(path, alias string) {
	if path == "" {
		return
	}
	// A non-empty alias wins, so a caller that knows the alias may register the
	// path before one that does not.
	if existing, ok := s.seen[path]; ok && existing != "" {
		return
	}
	s.seen[path] = alias
}

func (s *importSet) render(orgModule string) string {
	orgPrefix := organisationPrefix(orgModule)

	var std, third, org []string

	for path, alias := range s.seen {
		line := strconv.Quote(path)
		if alias != "" {
			line = alias + " " + line
		}

		switch {
		case !strings.Contains(strings.SplitN(path, "/", 2)[0], "."):
			std = append(std, line)
		case orgPrefix != "" && strings.HasPrefix(path, orgPrefix):
			org = append(org, line)
		default:
			third = append(third, line)
		}
	}

	for _, g := range [][]string{std, third, org} {
		sort.Strings(g)
	}

	groups := make([]string, 0, 3)
	for _, g := range [][]string{std, third, org} {
		if len(g) > 0 {
			groups = append(groups, strings.Join(g, "\n"))
		}
	}

	return strings.Join(groups, "\n\n")
}

// organisationPrefix reduces a module path to its organisation, so that the
// generated provider's own packages and its sibling SDK land in the same group,
// as the house gci configuration specifies.
func organisationPrefix(module string) string {
	parts := strings.Split(module, "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.Join(parts[:2], "/")
}

// serviceSelector is the identifier the service package is referenced by in
// generated code: the declared alias, or the import path's last element.
func serviceSelector(svc blueprint.ServiceRef) string {
	if svc.Alias != "" {
		return svc.Alias
	}
	if i := strings.LastIndex(svc.ImportPath, "/"); i >= 0 {
		return svc.ImportPath[i+1:]
	}
	return svc.ImportPath
}

// lowerFirst lowercases an identifier's first rune, for derived local names.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
