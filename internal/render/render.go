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
package render

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
	pkgSchema    = "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	pkgTypes     = "github.com/hashicorp/terraform-plugin-framework/types"
	pkgTimeouts  = "github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	pkgTflog     = "github.com/hashicorp/terraform-plugin-log/tflog"
	pkgPlanModif = "github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	pkgStringPM  = "github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	pkgAttr      = "github.com/hashicorp/terraform-plugin-framework/attr"
	pkgDiag      = "github.com/hashicorp/terraform-plugin-framework/diag"
	pkgBaseTypes = "github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

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
	// ModelFields are finished struct field declarations.
	ModelFields []string
	// NestedModels are the sibling model structs a nested attribute needs. They
	// are siblings rather than inner types because the framework decodes elements
	// into a named type.
	NestedModels []NestedModelView

	// ImportState renders the ImportState method body, or is empty when the
	// resource does not support import.
	ImportState string

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
	// Nested are the per-shape expand helpers this resource needs.
	Nested []NestedFuncView
	// NeedsDiagnostics is true when any assignment can fail, which decides
	// whether the generated function returns diagnostics at all.
	NeedsDiagnostics bool
}

// StateView is the flatten function.
type StateView struct {
	ResponseType string
	// Assignments are finished statements assigning to the model.
	Assignments []string
	// Nested are the per-shape flatten helpers this resource needs.
	Nested           []NestedFuncView
	NeedsDiagnostics bool
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

	// IDAssign is the finished line that writes the created object's identifier
	// into the model, e.g. "plan.ID = convert.PtrStringToFramework(created.ID)".
	IDAssign string

	// ReadBack requests a re-read after create and update.
	ReadBack       bool
	ReadBackReason string

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
		"// Code generated by tfprovidergen from %s\n// (sha256:%s). DO NOT EDIT.",
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
		ResourceName:        r.TerraformType,
		GoTypeName:          r.GoTypeName,
		ModelTypeName:       r.ModelTypeName,
		ConstructorFn:       "New" + r.GoTypeName,
		SDKClientType:       bp.Provider.SDK.ClientType,
		ServiceAccessor:     r.Binding.Service.Accessor,
		IDField:             r.Binding.ID.GoField,
		MarkdownDescription: r.MarkdownDescription,
		Timeouts:            timeoutsView(r, bp.Provider.Conventions.DefaultTimeouts),
	}

	if r.DocRefURL != "" {
		v.DocRefComment = "// REF: " + r.DocRefURL
	}

	sdk := bp.Provider.SDK
	sup := bp.Provider.Support

	// resource.go: schema, metadata, configure, import.
	impResource.add(pkgContext, "")
	impResource.add(pkgResource, "")
	impResource.add(pkgSchema, "")
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

	v.Interfaces = interfaces(r)

	if r.Import.Style == blueprint.ImportPassthroughID {
		impResource.add(pkgPath, "")
		v.ImportState = fmt.Sprintf("resource.ImportStatePassthroughID(ctx, path.Root(%q), req, resp)",
			r.Import.Attribute)
	}

	attrs, fields, err := attributes(r, impResource)
	if err != nil {
		return ResourceView{}, err
	}
	v.SchemaAttributes = attrs
	v.ModelFields = fields

	// A collection attribute's ElementType expression needs the types package,
	// and a scalar-only resource must not import it.
	if usesElementTypes(r) {
		impResource.add(pkgTypes, "")
	}

	shapes, err := nestedShapes(r)
	if err != nil {
		return ResourceView{}, err
	}

	for _, sh := range shapes {
		nm, err := nestedModelView(sh)
		if err != nil {
			return ResourceView{}, err
		}
		v.NestedModels = append(v.NestedModels, nm)
	}

	// A nested shape puts attr.Type maps in model.go, whose imports the template
	// declares itself, and gives the conversion helpers framework values and
	// diagnostics to carry. It adds nothing to crud.go, which only ever appends to
	// resp.Diagnostics.
	if len(shapes) > 0 {
		impResource.add(pkgTypes, "")
		for _, s := range []*importSet{impConstruct, impState} {
			s.add(pkgTypes, "")
			s.add(pkgDiag, "")
		}
		// A single nested object is decoded with basetypes.ObjectAsOptions, which
		// a collection does not need.
		for _, sh := range shapes {
			if sh.attr.Type.Kind == blueprint.KindSingleNested {
				impConstruct.add(pkgBaseTypes, "")
			}
		}
	}

	v.Construct = constructView(r, shapes)
	v.State = stateView(r, shapes)

	// A fallible conversion anywhere means construct or state returns diagnostics,
	// which changes the shape of the generated CRUD call sites. crud.go needs no
	// extra import for that: it appends to resp.Diagnostics.

	crud, err := crudView(bp, r)
	if err != nil {
		return ResourceView{}, err
	}
	v.CRUD = crud

	org := bp.Provider.GoModule
	v.Imports = FileImports{
		Resource:  impResource.render(org),
		Construct: impConstruct.render(org),
		State:     impState.render(org),
		CRUD:      impCRUD.render(org),
	}

	return v, nil
}

func usesElementTypes(r blueprint.Resource) bool {
	for _, a := range r.Attributes {
		if !a.Drop && a.Type.Kind.IsCollection() {
			return true
		}
	}
	return false
}

func timeoutsView(r blueprint.Resource, def blueprint.Timeouts) TimeoutsView {
	pick := func(a, b int) int {
		if a > 0 {
			return a
		}
		if b > 0 {
			return b
		}
		return 180
	}
	return TimeoutsView{
		Create: pick(r.Timeouts.CreateSeconds, def.CreateSeconds),
		Read:   pick(r.Timeouts.ReadSeconds, def.ReadSeconds),
		Update: pick(r.Timeouts.UpdateSeconds, def.UpdateSeconds),
		Delete: pick(r.Timeouts.DeleteSeconds, def.DeleteSeconds),
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
		out = append(out, fmt.Sprintf("_ resource.ResourceWithImportState = (*%s)(nil)", r.GoTypeName))
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

func attributes(r blueprint.Resource, imports *importSet) (attrs, fields []string, err error) {
	for _, a := range r.Attributes {
		if a.Drop {
			continue
		}

		schemaType, ok := frameworkSchemaType[a.Type.Kind]
		if !ok {
			return nil, nil, &ErrUnsupported{
				What: fmt.Sprintf("attribute %q of resource %q", a.Name, r.Key),
				Why:  fmt.Sprintf("type kind %q has no framework mapping", a.Type.Kind),
			}
		}

		decl, err := attributeDecl(a, schemaType, imports)
		if err != nil {
			return nil, nil, err
		}
		attrs = append(attrs, decl)

		modelType := frameworkModelType[a.Type.Kind]
		fields = append(fields, fmt.Sprintf("%s %s `tfsdk:%q`", a.GoField, modelType, a.Name))
	}

	// The timeouts block is last in the model, matching the archetype, and is
	// what the generated CRUD reads its per-operation deadlines from.
	fields = append(fields, "Timeouts timeouts.Value `tfsdk:\"timeouts\"`")

	return attrs, fields, nil
}

func attributeDecl(a blueprint.Attribute, schemaType string, imports *importSet) (string, error) {
	if a.Type.Kind.IsNested() {
		return nestedAttributeDecl(a, imports)
	}

	var b strings.Builder

	fmt.Fprintf(&b, "%q: schema.%s{\n", a.Name, schemaType)

	if a.Type.Kind.IsCollection() {
		if a.Type.Elem == nil {
			return "", &ErrUnsupported{
				What: fmt.Sprintf("attribute %q", a.Name),
				Why:  "a collection needs an element type",
			}
		}
		elem, ok := frameworkElemType[a.Type.Elem.Kind]
		if !ok {
			return "", &ErrUnsupported{
				What: fmt.Sprintf("attribute %q", a.Name),
				Why:  fmt.Sprintf("element kind %q is not a scalar; nested elements are not yet supported", a.Type.Elem.Kind),
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

	if len(a.Validators) > 0 {
		b.WriteString("Validators: []validator." + validatorKind(a.Type.Kind) + "{\n")
		for _, v := range a.Validators {
			fmt.Fprintf(&b, "%s,\n", v.SchemaDefinition)
			for _, im := range v.Imports {
				imports.add(im.Path, im.Alias)
			}
		}
		b.WriteString("},\n")
	}

	pms := a.PlanModifiers
	// A computed attribute with no plan modifier shows as "(known after apply)"
	// on every plan, even when nothing about it changed. UseStateForUnknown is
	// the standard remedy, and applying it by default is what stops a generated
	// provider from producing noisy plans.
	if a.Presence == blueprint.Computed && len(pms) == 0 && a.Type.Kind == blueprint.KindString {
		imports.add(pkgStringPM, "")
		pms = []blueprint.CustomCode{{SchemaDefinition: "stringplanmodifier.UseStateForUnknown()"}}
	}

	if len(pms) > 0 {
		imports.add(pkgPlanModif, "")
		b.WriteString("PlanModifiers: []planmodifier." + validatorKind(a.Type.Kind) + "{\n")
		for _, pm := range pms {
			fmt.Fprintf(&b, "%s,\n", pm.SchemaDefinition)
			for _, im := range pm.Imports {
				imports.add(im.Path, im.Alias)
			}
		}
		b.WriteString("},\n")
	}

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

// writeAttributeFlags writes the presence and sensitivity flags shared by every
// attribute kind, nested or not.
func writeAttributeFlags(b *strings.Builder, a blueprint.Attribute) {
	if a.Presence.IsRequired() {
		b.WriteString("Required: true,\n")
	}
	if a.Presence.IsOptional() {
		b.WriteString("Optional: true,\n")
	}
	if a.Presence.IsComputed() {
		b.WriteString("Computed: true,\n")
	}
	if a.Sensitive {
		b.WriteString("Sensitive: true,\n")
	}
}

// validatorKind is the framework's per-type sub-package suffix, which both the
// validator and planmodifier packages share.
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
		return fmt.Sprintf("%s.Static%s(%s)", pkg, validatorKind(a.Type.Kind), a.Default.Static.Raw), nil

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

	for _, sh := range shapes {
		if sh.attr.Wire.SkipExpand {
			continue
		}
		v.Nested = append(v.Nested, nestedExpandView(sh))
	}

	for _, a := range r.Attributes {
		if a.Drop || a.Wire.SkipExpand || a.Wire.Expand == nil {
			continue
		}

		// A fallible conversion becomes two statements plus a diagnostics append,
		// which is why the enclosing function has to return diagnostics at all.
		if a.Wire.Expand.ReturnsError {
			v.NeedsDiagnostics = true
			v.Assignments = append(v.Assignments, fmt.Sprintf(
				"body.%s, d = %s\ndiags.Append(d...)",
				a.Wire.SDKField, convertExpr(*a.Wire.Expand, "data."+a.GoField)))
			continue
		}

		v.Assignments = append(v.Assignments, fmt.Sprintf("body.%s = %s",
			a.Wire.SDKField, convertExpr(*a.Wire.Expand, "data."+a.GoField)))
	}

	return v
}

func stateView(r blueprint.Resource, shapes []nestedShape) StateView {
	v := StateView{ResponseType: r.Binding.Body.ResponseType}

	for _, sh := range shapes {
		if sh.attr.Wire.SkipFlatten {
			continue
		}
		v.Nested = append(v.Nested, nestedFlattenView(sh))
	}

	for _, a := range r.Attributes {
		if a.Drop || a.Wire.SkipFlatten || a.Wire.Flatten == nil {
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

	return v
}

// convertExpr renders a call into the provider's convert package.
func convertExpr(c blueprint.ConvertCall, arg string) string {
	var b strings.Builder

	b.WriteString(c.Func)
	if len(c.TypeArgs) > 0 {
		b.WriteString("[" + strings.Join(c.TypeArgs, ", ") + "]")
	}
	b.WriteString("(")
	if c.NeedsCtx {
		b.WriteString("ctx, ")
	}
	b.WriteString(arg)
	b.WriteString(")")

	return b.String()
}

func crudView(bp blueprint.Blueprint, r blueprint.Resource) (CRUDView, error) {
	v := CRUDView{
		ReadBack:                r.Policy.ReadBack.Enabled,
		ReadBackReason:          r.Policy.ReadBack.Reason,
		DeleteToleratesNotFound: r.Policy.Delete.NotFoundIsSuccess,
	}

	ops := []struct {
		op      *blueprint.Operation
		dst     **OpView
		phase   string
		errOp   string
		timeout string
	}{
		{r.Binding.Create, &v.Create, "crud.PhaseCreate", "errors.OpCreate", "CreateTimeout"},
		{r.Binding.Read, &v.Read, "crud.PhaseRead", "errors.OpRead", "ReadTimeout"},
		{r.Binding.Update, &v.Update, "crud.PhaseUpdate", "errors.OpUpdate", "UpdateTimeout"},
		{r.Binding.Delete, &v.Delete, "crud.PhaseDelete", "errors.OpDelete", "DeleteTimeout"},
	}

	for _, o := range ops {
		if o.op == nil {
			continue
		}
		view, err := opView(r, *o.op, o.phase, o.errOp, o.timeout)
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
				Why:  fmt.Sprintf("the ID binding names attribute %q, which does not exist", r.Binding.ID.Attribute),
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

func opView(r blueprint.Resource, op blueprint.Operation, phase, errOp, timeout string) (*OpView, error) {
	if op.Style != blueprint.CallStyleMethod {
		return nil, &ErrUnsupported{
			What: fmt.Sprintf("operation %q of resource %q", op.Method, r.Key),
			Why:  fmt.Sprintf("call style %q is not implemented", op.Style),
		}
	}

	args := make([]string, 0, len(op.Args))
	for _, a := range op.Args {
		expr, err := argExpr(r, a)
		if err != nil {
			return nil, err
		}
		args = append(args, expr)
	}

	v := &OpView{
		Call:         fmt.Sprintf("%s.%s(%s)", r.Binding.Service.Accessor, op.Method, strings.Join(args, ", ")),
		TimeoutConst: timeout,
		Phase:        phase,
		ErrorOp:      errOp,
	}

	// The assignment must match the call's arity exactly, which is why the
	// blueprint records it rather than the emitter guessing from a method name.
	switch op.Return {
	case blueprint.ReturnResultTransportError:
		v.HasResult, v.ResultVar = true, resultVarFor(phase)
		v.Assign = fmt.Sprintf("%s, _, err :=", v.ResultVar)
	case blueprint.ReturnResultError:
		v.HasResult, v.ResultVar = true, resultVarFor(phase)
		v.Assign = fmt.Sprintf("%s, err :=", v.ResultVar)
	case blueprint.ReturnTransportError:
		v.Assign = "_, err :="
	case blueprint.ReturnError:
		v.Assign = "err :="
	default:
		return nil, &ErrUnsupported{
			What: fmt.Sprintf("operation %q of resource %q", op.Method, r.Key),
			Why:  fmt.Sprintf("return arity %q is not implemented", op.Return),
		}
	}

	return v, nil
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

func argExpr(r blueprint.Resource, a blueprint.Argument) (string, error) {
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
	case blueprint.ArgLiteral:
		return "", &ErrUnsupported{
			What: fmt.Sprintf("argument of resource %q", r.Key),
			Why:  "a literal argument needs an expression",
		}
	default:
		return "", &ErrUnsupported{
			What: fmt.Sprintf("argument of resource %q", r.Key),
			Why:  fmt.Sprintf("argument kind %q is not implemented", a.Kind),
		}
	}
}

func findAttribute(r blueprint.Resource, name string) (blueprint.Attribute, bool) {
	for _, a := range r.Attributes {
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
