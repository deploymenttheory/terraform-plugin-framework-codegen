package openapi

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"
)

// Two reasons a candidate cannot become a resource, as sentinels so a caller can tell them apart
// without matching on message text.
//
// The distinction matters to `ingest`: a candidate that is not a resource is a classification
// outcome and belongs in the skipped count, while one whose schemas yielded no attributes is a
// gap in the specification and belongs in the notes a human reads.
var (
	// ErrNotAResource marks a candidate whose operation set cannot support a Terraform resource.
	ErrNotAResource = errors.New("not a resource")
	// ErrNoAttributes marks a candidate whose schemas produced no usable attributes.
	ErrNoAttributes = errors.New("no attributes could be inferred")
)

// InferOptions configures inference.
type InferOptions struct {
	// Provider is the registry name, which prefixes every resource type.
	Provider string
	// SDKServiceRoot is the import prefix under which the SDK's service packages
	// live, e.g. ".../thousandeyes/thousandeyes_api".
	SDKServiceRoot string
	// SDKAccessorPrefix reaches a service from the resource receiver, e.g.
	// "r.client.API". The service name is appended.
	SDKAccessorPrefix string
	// APIVersionDir places generated packages under a version directory.
	APIVersionDir string

	// SDKDialect selects the binding shape: restyService (the zero value) or
	// kiotaFluent, which binds fluent chains against a kiota-generated SDK.
	SDKDialect blueprint.SDKDialect
	// SDKModelsImport is the kiota SDK's models package import path, required
	// under kiotaFluent and unused otherwise.
	SDKModelsImport string
}

// Caveat records something inference could not do, or did by assumption.
//
// Notes are the point of the exercise as much as the blueprint is. A generator
// that silently drops what it cannot express produces a provider that looks
// complete and is not, so everything skipped is reported with the attribute it
// belongs to.
type Caveat struct {
	Resource string
	Field    string
	Message  string
}

func (n Caveat) String() string {
	if n.Field == "" {
		return fmt.Sprintf("%s: %s", n.Resource, n.Message)
	}
	return fmt.Sprintf("%s.%s: %s", n.Resource, n.Field, n.Message)
}

// namingOpts strips the specification's component namespace, which the
// ThousandEyes unified document prefixes every schema with.
var namingOpts = naming.Options{StripPrefix: naming.DefaultStripPrefix}

// Infer turns a candidate into a blueprint resource.
//
// It infers the mechanical parts: names, types, presence, wire bindings and the
// SDK call shape. It does not invent editorial content -- documentation links,
// deliberate presence overrides and curated descriptions belong to a person, and
// the difference between this output and a curated blueprint is exactly the work
// that required judgement.
func (d *Document) Infer(c Candidate, opts InferOptions) (blueprint.Resource, []Caveat, error) {
	kind, why := c.Classify()
	if kind != CandidateKindResource {
		return blueprint.Resource{}, nil, fmt.Errorf("%w: %s: %s", ErrNotAResource, c.Key, why)
	}

	var notes []Caveat

	service := namingOpts.GoTypeName(c.Tag)
	pkgDir := naming.SnakeDirName(c.Key)
	goType := namingOpts.GoTypeName(c.Key) + "Resource"

	r := blueprint.Resource{
		Key:            c.Key,
		Name:           naming.TerraformName(c.Key),
		GoPackage:      pkgDir,
		GoPackageAlias: namingOpts.PackageAlias(opts.APIVersionDir, c.Key),
		GoTypeName:     goType,
		ModelTypeName:  goType + "Model",
		ServiceGroup:   naming.SnakeDirName(c.Tag),
		APIVersionDir:  opts.APIVersionDir,
		Import: blueprint.ImportPolicy{
			Style:     blueprint.ImportPassthroughID,
			Attribute: "id",
		},
		Policy: blueprint.ResourcePolicy{
			UpdateStyle: updateStyleOf(c.Update),
			Delete:      blueprint.Delete{NotFoundIsSuccess: true},
		},
	}

	if s := summaryOf(c); s != "" {
		r.Schema.MarkdownDescription = s
	}

	requestType, responseType := d.bodyTypeNames(c)
	sdkPkg := naming.SnakeDirName(c.Tag)

	if opts.SDKDialect == blueprint.DialectKiotaFluent {
		// The kiota shape: every model lives in one models package imported
		// under our own alias (never kiota's hash aliases), builders return
		// <Type>able interfaces, bodies are built by constructor and setter,
		// and the chain starts at the bare client.
		sdkPkg = "models"
		reqT := kiotaModelName(requestType)
		respT := kiotaModelName(responseType)

		body := blueprint.BodyModels{
			RequestType:     "models." + reqT,
			ResponseType:    "models." + respT + "able",
			ConstructorExpr: "models.New" + reqT + "()",
			AccessStyle:     blueprint.AccessMethod,
		}
		if updateType := d.updateBodyTypeName(c); updateType != "" && updateType != requestType {
			upT := kiotaModelName(updateType)
			body.UpdateRequestType = "models." + upT
			body.UpdateConstructorExpr = "models.New" + upT + "()"
		}

		r.Binding = blueprint.ResourceBinding{
			Service: blueprint.ServiceRef{
				ImportPath: strings.TrimSuffix(opts.SDKModelsImport, "/"),
				Alias:      "models",
				Accessor:   "r.client",
			},
			Body: body,
			ID: blueprint.IDBinding{
				Attribute: "id", GoField: "ID",
				FromCreate: "created.GetId()", FromCreateIsPointer: true,
			},
		}

		// Each operation names its own result: an API routinely gives create a
		// model of its own -- ThousandEyes' user create returns CreatedUser where
		// read returns UserDetail -- and declaring read's model on create fails
		// the bindings check against the SDK's real signature.
		results := opResultTypes{
			create: kiotaOpResult(d, c.Create, body.ResponseType),
			read:   kiotaOpResult(d, c.Read, body.ResponseType),
			update: kiotaOpResult(d, c.Update, body.ResponseType),
		}
		bindOperationsKiota(&r, c, results)
	} else {
		reqT := namingOpts.GoTypeName(requestType)
		respT := namingOpts.GoTypeName(responseType)

		body := blueprint.BodyModels{
			RequestType:     sdkPkg + "." + reqT,
			ResponseType:    sdkPkg + "." + respT,
			ConstructorExpr: "&" + sdkPkg + "." + reqT + "{}",
			AccessStyle:     blueprint.AccessStructField,
		}

		// An update whose request schema is its own named type is a split body, and
		// pretending update sends the create type would fail the bindings check on the
		// first divergent field. Inferred here so curation starts from the truth; the
		// emitter reuses one assignment list against both types, which sdkbind proves
		// safe field by field.
		if updateType := d.updateBodyTypeName(c); updateType != "" && updateType != requestType {
			upT := namingOpts.GoTypeName(updateType)
			body.UpdateRequestType = sdkPkg + "." + upT
			body.UpdateConstructorExpr = "&" + sdkPkg + "." + upT + "{}"
		}

		r.Binding = blueprint.ResourceBinding{
			Service: blueprint.ServiceRef{
				ImportPath: strings.TrimRight(opts.SDKServiceRoot, "/") + "/" + sdkPkg,
				TypeName:   service,
				Accessor:   strings.TrimRight(opts.SDKAccessorPrefix, ".") + "." + service,
			},
			Body: body,
			ID: blueprint.IDBinding{
				Attribute: "id", GoField: "ID",
				FromCreate: "created.ID", FromCreateIsPointer: true,
			},
		}

		bindOperations(&r, c, sdkPkg+"."+responseType)
	}

	attrs, attrNotes := d.attributes(c, sdkPkg, opts.SDKDialect)
	r.Schema.Attributes = attrs
	notes = append(notes, attrNotes...)

	if len(r.Schema.Attributes) == 0 {
		return blueprint.Resource{}, notes, fmt.Errorf(
			"%w: %s: nothing usable in its schemas", ErrNoAttributes,
			c.Key,
		)
	}

	// Without an identifier there is nothing to read, import or delete by.
	if why := adoptIdentifier(&r, c, opts.SDKDialect); why != "" {
		notes = append(notes, Caveat{Resource: c.Key, Message: why})
	}

	return r, notes, nil
}

// adoptIdentifier settles the resource's identifier on the import convention's name.
//
// The convention is that a resource is read, imported and deleted by an attribute
// named "id", but most families spell theirs after the object -- testId, roleId,
// uid -- and the hardcoded binding used to name an attribute that did not exist.
// The item path names the field the API is keyed by, so when the schemas carry no
// literal "id" the parameter's attribute is renamed to "id" while its wire binding
// keeps the API's spelling, and fromCreate reads the same field off the create
// response. This is exactly the shape curation used to produce by hand.
func adoptIdentifier(r *blueprint.Resource, c Candidate, dialect blueprint.SDKDialect) string {
	if hasAttribute(r.Schema.Attributes, "id") {
		return ""
	}

	missing := "no id attribute in the schemas, so the resource cannot be imported or refreshed"

	param := ""
	if i := strings.LastIndex(c.ItemPath, "{"); i >= 0 {
		param = strings.Trim(c.ItemPath[i:], "{}")
	}

	// The path parameter is the strongest signal. After it, the spellings APIs
	// use when the parameter is a generic {id} and the schema is not: the
	// resource's own name plus Id (roles/{id} returning roleId), then the
	// identifier idioms observed live -- /users/{id} returns uid, and an
	// account group's own identifier is aid.
	var want []string
	if param != "" {
		want = append(want, naming.TerraformName(param))
	}
	want = append(want, naming.TerraformName(c.Key)+"_id", "uid", "guid", "aid")

	idx := -1
	for _, name := range want {
		for i := range r.Schema.Attributes {
			if r.Schema.Attributes[i].Name == name {
				idx = i
				break
			}
		}
		if idx >= 0 {
			break
		}
	}
	if idx < 0 {
		return missing
	}

	a := &r.Schema.Attributes[idx]

	jsonName := a.Wire.JSONPath
	if jsonName == "" {
		jsonName = param
	}

	a.Name = "id"
	a.GoField = "ID"
	if a.MarkdownDescription != "" {
		a.MarkdownDescription += " "
	}
	a.MarkdownDescription += fmt.Sprintf(
		"Named `id` per the import convention; the wire field stays %s.", jsonName)

	if dialect == blueprint.DialectKiotaFluent {
		r.Binding.ID.FromCreate = "created.Get" + kiotaAccessorBase(jsonName) + "()"
	} else {
		r.Binding.ID.FromCreate = "created." + namingOpts.GoFieldName(jsonName)
	}

	// The rename moves the attribute out of alphabetical position, and drafting
	// promises deterministic, ordered output.
	sort.SliceStable(r.Schema.Attributes, func(x, y int) bool {
		return r.Schema.Attributes[x].Name < r.Schema.Attributes[y].Name
	})

	return ""
}

// updateStyleOf reads the update semantics off the HTTP verb.
//
// This is not cosmetic: PUT clears fields the request omits and PATCH leaves them
// alone, so getting it wrong silently erases attributes the practitioner never
// mentioned. The verb is the only evidence a specification offers, and probing
// later confirms it.
func updateStyleOf(op *Operation) blueprint.UpdateStyle {
	switch {
	case op == nil:
		return blueprint.UpdateReplaceOnly
	case op.Method == "PATCH":
		return blueprint.UpdatePatchMerge
	default:
		return blueprint.UpdatePutFull
	}
}

func bindOperations(r *blueprint.Resource, c Candidate, resultType string) {
	// Method names follow the SDK generator's own rule: the operation id, as a Go
	// identifier. bindings verifies that against the SDK rather than trusting it.
	method := func(op *Operation) string {
		if op == nil || op.OperationID == "" {
			return ""
		}
		return namingOpts.GoTypeName(op.OperationID)
	}

	ctxArg := blueprint.Argument{Kind: blueprint.ArgContext}
	idArg := blueprint.Argument{Kind: blueprint.ArgStateField, Field: "ID"}
	bodyArg := blueprint.Argument{Kind: blueprint.ArgBody}

	if c.Create != nil {
		r.Binding.Create = &blueprint.Operation{
			Style: blueprint.CallStyleMethod, Method: method(c.Create),
			Args:   []blueprint.Argument{ctxArg, bodyArg},
			Return: blueprint.ReturnResultTransportError, ResultType: resultType,
			HTTPMethod: c.Create.Method, PathTemplate: c.Create.Path,
		}
	}
	if c.Read != nil {
		r.Binding.Read = &blueprint.Operation{
			Style: blueprint.CallStyleMethod, Method: method(c.Read),
			Args:   []blueprint.Argument{ctxArg, idArg},
			Return: blueprint.ReturnResultTransportError, ResultType: resultType,
			HTTPMethod: c.Read.Method, PathTemplate: c.Read.Path,
		}
	}
	if c.Update != nil {
		r.Binding.Update = &blueprint.Operation{
			Style: blueprint.CallStyleMethod, Method: method(c.Update),
			Args:   []blueprint.Argument{ctxArg, idArg, bodyArg},
			Return: blueprint.ReturnResultTransportError, ResultType: resultType,
			HTTPMethod: c.Update.Method, PathTemplate: c.Update.Path,
		}
	}
	if c.Delete != nil {
		// Delete returns no body, so the arity differs -- and the arity decides
		// every error return in the generated function.
		r.Binding.Delete = &blueprint.Operation{
			Style: blueprint.CallStyleMethod, Method: method(c.Delete),
			Args:       []blueprint.Argument{ctxArg, idArg},
			Return:     blueprint.ReturnTransportError,
			HTTPMethod: c.Delete.Method, PathTemplate: c.Delete.Path,
		}
	}
}

// attributes merges the create request and read response schemas into one
// attribute set.
//
// The request says what may be written and the response says what can be read.
// A field in both is configurable; one only in the response is computed. That
// merge is the whole of presence inference, and it is why both schemas are read
// rather than just one.
func (d *Document) attributes(c Candidate, sdkPkg string, dialect blueprint.SDKDialect) ([]blueprint.Attribute, []Caveat) {
	writable := map[string]Field{}
	for _, f := range Fields(d.requestSchema(c)) {
		writable[f.Name] = f
	}

	readable := map[string]Field{}
	for _, f := range Fields(d.responseSchemaOf(c)) {
		readable[f.Name] = f
	}

	names := make([]string, 0, len(writable)+len(readable))
	seen := map[string]bool{}
	for _, m := range []map[string]Field{writable, readable} {
		for n := range m {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)

	ctx := newInferCtx(c.Key, sdkPkg, dialect)

	var out []blueprint.Attribute

	for _, name := range names {
		f, inRead := readable[name]
		w, inWrite := writable[name]
		if !inRead {
			f = w
		}

		// A request schema often documents a field its response schema does not,
		// and losing that would throw away most of the generated documentation.
		if f.Description == "" && w.Description != "" {
			f.Description = w.Description
		}
		// readOnly on either side means the field cannot be written.
		if w.ReadOnly {
			f.ReadOnly = true
		}

		if skip, why := skipField(f); skip {
			ctx.note(name, why)
			continue
		}

		attr, why := ctx.attributeOf(f, name, inWrite)
		if why != "" {
			ctx.note(name, why)
			continue
		}
		out = append(out, attr)
	}

	return out, ctx.notes
}

func (d *Document) requestSchema(c Candidate) *base.Schema {
	op := d.operation(c.Create)
	if op == nil {
		return nil
	}
	return bodySchema(op.RequestBody)
}

func (d *Document) responseSchemaOf(c Candidate) *base.Schema {
	// The read response is authoritative for what state can hold. Falling back to
	// create's response covers an API that returns the object only on create.
	for _, o := range []*Operation{c.Read, c.Create} {
		if s := responseSchema(d.operation(o)); s != nil {
			return s
		}
	}
	return nil
}

// operation finds the libopenapi operation behind a discovered one.
func (d *Document) operation(op *Operation) *v3.Operation {
	if op == nil || d.model == nil || d.model.Paths == nil {
		return nil
	}

	item, ok := d.model.Paths.PathItems.Get(op.Path)
	if !ok || item == nil {
		return nil
	}

	for pair := item.GetOperations().First(); pair != nil; pair = pair.Next() {
		if strings.EqualFold(pair.Key(), op.Method) {
			return pair.Value()
		}
	}

	return nil
}

// halEnvelope matches the hypermedia members a HAL response carries.
//
// They describe the transport, not the resource, and surfacing them as
// attributes would put self-referential URLs into Terraform state.
var halEnvelope = regexp.MustCompile(`^_(links|embedded)$`)

func skipField(f Field) (bool, string) {
	switch {
	case halEnvelope.MatchString(f.Name):
		return true, "hypermedia envelope member, not part of the resource"
	case f.Kind == "":
		return true, "no framework type maps to this schema"
	case f.Kind.IsNested() && f.ObjectTypeName == "":
		// Without a $ref there is no schema name, and a nested object's model, attr.Type
		// map and helper pair all take their names from it. Naming them after the
		// attribute instead would collide the moment two objects had a field in common.
		return true, "nested object with no named schema, which cannot be given a model type"
	case f.Kind.IsNested() && f.CyclicSchema() != "":
		return true, fmt.Sprintf(
			"the schema %s contains itself, so its depth is decided by the data rather than "+
				"by the schema; that is not expressible as a fixed set of generated types, so "+
				"write this resource by hand",
			f.CyclicSchema(),
		)
	default:
		return false, ""
	}
}

// attributeOf builds one attribute from a merged field.
//
// inWrite is whether the field appears in the request body, which is what
// decides presence and therefore whether the attribute is ever sent. path is the dotted
// attribute path, so a note about something inside a nested object says where it was.
func (ctx *inferCtx) attributeOf(
	f Field,
	path string,
	inWrite bool,
) (blueprint.Attribute, string) {
	// A free-form map -- an object schema that is only additionalProperties -- is
	// held by the generated SDKs as opaque additional data, and no conversion is
	// generated for that shape. Both pilots curated these fields as dropped, so
	// the draft omits them by name rather than emitting an attribute whose wire
	// binding could never round-trip.
	if f.Kind == blueprint.KindMap {
		return blueprint.Attribute{}, "a free-form map has no generated conversion; " +
			"the SDK holds it as opaque additional data, so the field is omitted"
	}

	goField := namingOpts.GoFieldName(f.Name)

	// The wire join key is the SDK's spelling of the field, which differs by
	// generator: our models follow Go initialism conventions, kiota's accessors
	// are plain word-capitalisation plus keyword mangling.
	sdkField := goField
	if ctx.dialect == blueprint.DialectKiotaFluent {
		sdkField = kiotaAccessorBase(f.Name)
	}

	a := blueprint.Attribute{
		Name:                     naming.TerraformName(f.Name),
		GoField:                  goField,
		Type:                     blueprint.AttrType{Kind: f.Kind},
		ComputedOptionalRequired: presenceOf(f, inWrite),
		MarkdownDescription:      f.Description,
	}

	if f.Deprecated {
		a.DeprecationMessage = "The API documents this field as deprecated."
	}

	if f.Kind.IsCollection() {
		a.Type.ElementType = &blueprint.AttrType{
			Kind:        f.ElemKind,
			Constraints: f.ElemConstraints,
		}
	}

	a.Type.Constraints = f.Constraints

	// Reported rather than dropped in silence. A pattern RE2 cannot parse is the document
	// making a claim this toolchain cannot enforce, and the practitioner should know the
	// constraint is unenforced rather than assume it is generated.
	if f.BadPattern != "" {
		ctx.note(path, fmt.Sprintf(
			"the pattern %q is not a valid Go regular expression, so no RegexMatches validator "+
				"is generated; JSON Schema permits constructs RE2 does not, lookahead most often",
			f.BadPattern,
		))
	}

	// Carried into the IR rather than dropped. Extracted here already and used only for a
	// description until now, which meant the one consumer that has a real use for them -- the
	// enum probe, whose whole claim is "the specification is stale" -- had nothing provably
	// spec-derived to work from.
	if len(f.EnumValues) > 0 {
		a.Type.AllowedValues = append([]string(nil), f.EnumValues...)
	}

	writable := a.ComputedOptionalRequired.IsRequired() || a.ComputedOptionalRequired.IsOptional()

	if f.Kind.IsNested() {
		n, why := ctx.nestedObject(f, path, writable)
		if why != "" {
			return blueprint.Attribute{}, why
		}
		a.Type.NestedObject = n

		// The SDK holds a collection of objects as a slice and a single object behind a
		// pointer -- except a setter-based SDK, whose builders traffic in bare
		// interfaces. The distinction decides the generated helper's signature,
		// so it is recorded rather than left for the emitter to guess.
		sdkGoType := "*" + n.SDKType
		if f.Kind.IsNestedCollection() {
			sdkGoType = "[]" + n.SDKType
		}
		if ctx.dialect == blueprint.DialectKiotaFluent {
			sdkGoType = n.SDKType
			if f.Kind.IsNestedCollection() {
				sdkGoType = "[]" + n.SDKType
			}
		}

		a.Wire = blueprint.WireBinding{
			JSONPath:  f.Name,
			SDKField:  sdkField,
			SDKGoType: sdkGoType,
			Flatten: &blueprint.ConvertCall{
				Func: n.FlattenFunc, NeedsCtx: true, ReturnsError: true,
			},
		}

		if writable {
			a.Wire.Expand = &blueprint.ConvertCall{
				Func: n.ExpandFunc, NeedsCtx: true, ReturnsError: true,
			}
		} else {
			a.Wire.SkipExpand = true
		}

		return a, ""
	}

	// kiota reads format: uuid literally and holds the field as *uuid.UUID, so a
	// string conversion against it does not compile. The attribute stays a
	// framework string; the flatten renders the UUID. No writable uuid field
	// exists in any curated set, and inventing an unproven parse direction here
	// would be guessing -- so a writable one is refused by name instead.
	// A collection of uuid elements is []uuid.UUID in the SDK. The read
	// direction renders each element through its Stringer; no expand helper
	// parses the other way, so a writable one is refused by name.
	if ctx.dialect == blueprint.DialectKiotaFluent &&
		f.Kind == blueprint.KindSet && f.ElemFormat == "uuid" {
		if writable {
			return blueprint.Attribute{}, "a writable set of uuid elements has no generated " +
				"conversion; the SDK holds it as []uuid.UUID and no expand direction is proven"
		}
		a.Wire = blueprint.WireBinding{
			JSONPath:  f.Name,
			SDKField:  sdkField,
			SDKGoType: "[]uuid.UUID",
			Flatten: &blueprint.ConvertCall{
				Func: "convert.KiotaEnumSliceToFrameworkSet", NeedsCtx: true, ReturnsError: true,
			},
			SkipExpand: true,
		}
		return a, ""
	}

	// kiota holds format: float as *float32, and no convert helper bridges that
	// width; the field is refused by name rather than wired to a function that
	// does not exist.
	if ctx.dialect == blueprint.DialectKiotaFluent &&
		(f.Kind == blueprint.KindFloat64 || f.Kind == blueprint.KindNumber) && f.Format == "float" {
		return blueprint.Attribute{}, "a float32 field has no generated conversion in the " +
			"kiota dialect, so the field is omitted"
	}

	if ctx.dialect == blueprint.DialectKiotaFluent &&
		f.Kind == blueprint.KindString && f.Format == "uuid" {
		if writable {
			return blueprint.Attribute{}, "a writable uuid field has no generated conversion; " +
				"the SDK holds it as *uuid.UUID and no expand direction is proven"
		}
		a.Wire = blueprint.WireBinding{
			JSONPath:   f.Name,
			SDKField:   sdkField,
			SDKGoType:  "*uuid.UUID",
			Flatten:    &blueprint.ConvertCall{Func: "convert.PtrStringerToFramework"},
			SkipExpand: true,
		}
		return a, ""
	}

	sdkType, convertFlatten, convertExpand := conversionsFor(f, ctx.sdkPkg, ctx.dialect)

	a.Wire = blueprint.WireBinding{
		JSONPath:  f.Name,
		SDKField:  sdkField,
		SDKGoType: sdkType,
		Flatten:   convertFlatten,
	}

	// A computed field is read and never sent. Marking it here is what stops the
	// generated construct function referring to a request field that may not
	// exist.
	if writable {
		a.Wire.Expand = convertExpand
	} else {
		a.Wire.SkipExpand = true
	}

	return a, ""
}

// presenceOf decides how Terraform treats a field.
//
// readOnly is the strongest signal a specification offers and is taken at face
// value. Everything else that can be written is optional unless the schema says
// required -- deliberately not computed_optional, because claiming the API
// supplies a default when it does not makes an attribute permanently
// "(known after apply)". Probing promotes those later, on evidence.
func presenceOf(f Field, inWrite bool) blueprint.ComputedOptionalRequired {
	switch {
	case f.ReadOnly, !inWrite:
		return blueprint.Computed
	case f.Required:
		return blueprint.Required
	default:
		return blueprint.Optional
	}
}

// conversionsFor picks the SDK type and the convert helpers for a field.
func conversionsFor(
	f Field,
	sdkPkg string,
	dialect blueprint.SDKDialect,
) (sdkType string, flatten, expand *blueprint.ConvertCall) {
	if f.IsEnum() {
		if dialect == blueprint.DialectKiotaFluent {
			// A kiota enum is an int-backed named type behind a pointer, paired
			// with a Parse function; the attribute stays a string driven by the
			// OpenAPI members, and the parse failure a validator missed becomes
			// a diagnostic rather than a silent nil.
			named := "models." + kiotaModelName(f.EnumTypeName)
			return "*" + named,
				&blueprint.ConvertCall{Func: "convert.PtrStringerToFramework"},
				&blueprint.ConvertCall{
					Func: "convert.FrameworkToKiotaEnum", TypeArgs: []string{named},
					ExtraArgs: []string{"models.Parse" + kiotaModelName(f.EnumTypeName)}, ReturnsError: true,
				}
		}
		// Generated SDKs hold enumerations by value as a named string type.
		named := sdkPkg + "." + namingOpts.GoTypeName(f.EnumTypeName)
		return named,
			&blueprint.ConvertCall{Func: "convert.EnumToFramework"},
			&blueprint.ConvertCall{Func: "convert.FrameworkToEnum", TypeArgs: []string{named}}
	}

	// Kiota reads integer formats literally: a plain integer is *int32 and only
	// format int64 widens, where the resty dialect holds every number as
	// *float64 or *int64. The attribute kind stays framework-facing; the
	// converters bridge the width.
	if dialect == blueprint.DialectKiotaFluent {
		switch f.Kind {
		case blueprint.KindString:
			// kiota reads temporal formats literally: date-time is *time.Time and
			// date is a serialization.DateOnly, where a string conversion against
			// either does not compile. A custom format string -- ThousandEyes
			// documents yyyy-MM-ddTHH:mm:ssZ on some fields -- is not a format
			// kiota recognises, and those stay strings.
			switch f.Format {
			case "date-time":
				return "*time.Time",
					&blueprint.ConvertCall{Func: "convert.PtrTimeToFramework"},
					&blueprint.ConvertCall{Func: "convert.FrameworkToPtrTime", ReturnsError: true}
			case "date":
				return "*serialization.DateOnly",
					&blueprint.ConvertCall{Func: "convert.PtrDateOnlyToFramework"},
					&blueprint.ConvertCall{Func: "convert.FrameworkToPtrDateOnly", ReturnsError: true}
			}
		case blueprint.KindInt64:
			if f.Format != "int64" {
				// The model field is types.Int64 -- the attribute kind decides that --
				// so the pair has to bridge the width, not assume a types.Int32 model
				// that generation never produces.
				return "*int32",
					&blueprint.ConvertCall{Func: "convert.PtrInt32ToFrameworkInt64"},
					&blueprint.ConvertCall{Func: "convert.FrameworkInt64ToPtrInt32"}
			}
		case blueprint.KindSet:
			// A set of enum members: the SDK holds a slice of the named enum type,
			// and the plain string-slice conversion against it does not compile.
			if f.ElemEnumTypeName != "" {
				named := "models." + kiotaModelName(f.ElemEnumTypeName)
				return "[]" + named,
					&blueprint.ConvertCall{
						Func: "convert.KiotaEnumSliceToFrameworkSet", NeedsCtx: true, ReturnsError: true,
					},
					&blueprint.ConvertCall{
						Func: "convert.FrameworkSetToKiotaEnumSlice", TypeArgs: []string{named},
						ExtraArgs: []string{"models.Parse" + kiotaModelName(f.ElemEnumTypeName)},
						NeedsCtx:  true, ReturnsError: true,
					}
			}
		}
	}

	switch f.Kind {
	case blueprint.KindString:
		return "*string",
			&blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
			&blueprint.ConvertCall{Func: "convert.FrameworkToPtrString"}

	case blueprint.KindBool:
		return "*bool",
			&blueprint.ConvertCall{Func: "convert.PtrBoolToFramework"},
			&blueprint.ConvertCall{Func: "convert.FrameworkToPtrBool"}

	case blueprint.KindInt64:
		return "*int64",
			&blueprint.ConvertCall{Func: "convert.PtrInt64ToFramework"},
			&blueprint.ConvertCall{Func: "convert.FrameworkToPtrInt64"}

	case blueprint.KindFloat64, blueprint.KindNumber:
		return "*float64",
			&blueprint.ConvertCall{Func: "convert.PtrFloat64ToFramework"},
			&blueprint.ConvertCall{Func: "convert.FrameworkToPtrFloat64"}

	case blueprint.KindSet:
		return "[]string",
			&blueprint.ConvertCall{
				Func:         "convert.StringSliceToFrameworkSet",
				NeedsCtx:     true,
				ReturnsError: true,
			},
			&blueprint.ConvertCall{
				Func:         "convert.FrameworkSetToStringSlice",
				NeedsCtx:     true,
				ReturnsError: true,
			}

	default:
		return "any", nil, nil
	}
}

// bodyTypeNames names the SDK request and response models, as the raw schema
// names the document declares.
//
// A generated SDK routinely splits them -- ThousandEyes takes a TagInfo and
// returns a Tag -- so modelling them as one type produces a construct function
// that does not compile. The names come back unmangled because each dialect
// spells its Go types differently: the resty SDK applies Go initialism
// conventions where kiota keeps the schema name verbatim, and mangling here
// would lose the underscores kiota preserves.
func (d *Document) bodyTypeNames(c Candidate) (request, response string) {
	request = schemaNameOf(d.requestBodyProxy(c))
	response = schemaNameOf(d.responseProxy(c))

	if request == "" {
		request = namingOpts.GoTypeName(c.Key)
	}
	if response == "" {
		response = request
	}

	return request, response
}

func (d *Document) requestBodyProxy(c Candidate) *base.SchemaProxy {
	return d.operationBodyProxy(c.Create)
}

// updateBodyTypeName names the update operation's own request schema, or "" when the
// update body is anonymous or absent. The caller compares it against create's: only a
// *different* named type is a split worth recording.
func (d *Document) updateBodyTypeName(c Candidate) string {
	return schemaNameOf(d.operationBodyProxy(c.Update))
}

func (d *Document) operationBodyProxy(o *Operation) *base.SchemaProxy {
	op := d.operation(o)
	if op == nil || op.RequestBody == nil || op.RequestBody.Content == nil {
		return nil
	}
	return proxyFromContent(op.RequestBody.Content)
}

func (d *Document) responseProxy(c Candidate) *base.SchemaProxy {
	for _, o := range []*Operation{c.Read, c.Create} {
		op := d.operation(o)
		if op == nil || op.Responses == nil || op.Responses.Codes == nil {
			continue
		}
		for pair := op.Responses.Codes.First(); pair != nil; pair = pair.Next() {
			if !strings.HasPrefix(pair.Key(), "2") || pair.Value() == nil ||
				pair.Value().Content == nil {
				continue
			}
			if p := proxyFromContent(pair.Value().Content); p != nil {
				return p
			}
		}
	}
	return nil
}

func schemaNameOf(p *base.SchemaProxy) string { return refTypeName(p) }

func hasAttribute(attrs []blueprint.Attribute, name string) bool {
	for _, a := range attrs {
		if a.Name == name {
			return true
		}
	}
	return false
}

// summaryOf builds a resource description from whatever the operations document.
func summaryOf(c Candidate) string {
	for _, op := range []*Operation{c.Read, c.Create, c.Update} {
		if op != nil && op.Summary != "" {
			s := strings.TrimSpace(op.Summary)
			return strings.ToUpper(s[:1]) + s[1:] + "."
		}
	}
	return ""
}
