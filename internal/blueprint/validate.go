package blueprint

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors. The house lint configuration enables err113, so every error
// originates from one of these and callers can branch on the class rather than
// on message text.
var (
	ErrInvalid           = errors.New("invalid blueprint")
	ErrUnsupportedFormat = errors.New("unsupported blueprint format version")
	ErrNoBlueprint       = errors.New("no blueprint found")
)

// problem is one validation failure, addressed by a path into the document so a
// message names the offending node rather than describing it.
type problem struct {
	path string
	msg  string
}

type problems []problem

func (p *problems) add(path, format string, args ...any) {
	*p = append(*p, problem{path: path, msg: fmt.Sprintf(format, args...)})
}

// err folds the accumulated problems into a single error.
//
// Every problem is reported, not just the first. A generator run that surfaces
// one error per invocation turns fixing a blueprint into a guessing game, and the
// cost of collecting them is nothing.
func (p problems) err() error {
	if len(p) == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d problem(s):", len(p))
	for _, pr := range p {
		fmt.Fprintf(&b, "\n  %s: %s", pr.path, pr.msg)
	}

	return fmt.Errorf("%w: %s", ErrInvalid, b.String())
}

// Validate reports every structural problem in the blueprint.
//
// It checks what would otherwise fail as a Go compile error in emitted code or,
// worse, as a runtime surprise: a schema attribute with no model field, a call
// binding with no method, an attribute whose type needs an element type and has
// none. The emitter is entitled to assume a validated blueprint.
func (b Blueprint) Validate() error {
	var p problems

	if b.FormatVersion != FormatVersion {
		p.add("formatVersion", "is %q, want %q", b.FormatVersion, FormatVersion)
	}

	b.Provider.validate(&p)

	seenKeys := map[string]bool{}
	seenTypes := map[string]bool{}
	seenAliases := map[string]bool{}

	for i, r := range b.Resources {
		at := fmt.Sprintf("resources[%d]", i)
		if r.Key != "" {
			at = fmt.Sprintf("resources[%s]", r.Key)
		}

		if r.Drop {
			continue
		}

		r.validate(at, &p)

		// Duplicates here are not cosmetic. Two resources sharing a Terraform
		// type make the provider fail to start; two sharing an import alias make
		// the registration file fail to compile; two sharing a key make probe
		// facts and overrides land on the wrong one.
		dup(&p, seenKeys, r.Key, at+".key", "resource key")
		dup(&p, seenTypes, r.Name, at+".name", "resource name")
		dup(&p, seenAliases, r.GoPackageAlias, at+".goPackageAlias", "import alias")
	}

	// Data sources were not validated at all before format 2, which mattered less while none were
	// emitted. Their keys and type names live in their own namespaces -- a `tag` resource and a
	// `tag` data source are a normal pair -- but the import alias is shared, because both register
	// into the same generated provider package.
	seenDataKeys := map[string]bool{}
	seenDataTypes := map[string]bool{}

	for i, d := range b.DataSources {
		at := fmt.Sprintf("dataSources[%d]", i)
		if d.Key != "" {
			at = fmt.Sprintf("dataSources[%s]", d.Key)
		}

		if d.Drop {
			continue
		}

		d.validate(at, &p)

		dup(&p, seenDataKeys, d.Key, at+".key", "data source key")
		dup(&p, seenDataTypes, d.Name, at+".name", "data source name")
		dup(&p, seenAliases, d.GoPackageAlias, at+".goPackageAlias", "import alias")
	}

	return p.err()
}

// validate checks one data source.
//
// The same skeleton as a resource minus everything a data source has no operation for: no binding
// beyond a read, no policy, no import. Its attributes are validated against BlockDataSource, which
// refuses a default or a plan modifier -- fields the framework's datasource/schema package does not
// have.
func (d DataSource) validate(at string, p *problems) {
	required(p, at+".key", d.Key)
	required(p, at+".name", d.Name)
	required(p, at+".goPackage", d.GoPackage)
	required(p, at+".goPackageAlias", d.GoPackageAlias)
	required(p, at+".goTypeName", d.GoTypeName)
	required(p, at+".modelTypeName", d.ModelTypeName)

	if len(d.Schema.Attributes) == 0 {
		p.add(at+".schema.attributes", "a data source with no attributes cannot be emitted")
	}

	d.Binding.validate(at+".binding", p)

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}

	for i, a := range d.Schema.Attributes {
		aat := fmt.Sprintf("%s.schema.attributes[%d]", at, i)
		if a.Name != "" {
			aat = fmt.Sprintf("%s.schema.attributes[%s]", at, a.Name)
		}

		if a.Drop {
			continue
		}

		a.validate(aat, p)
		a.validateForKind(BlockDataSource, aat, p)

		dup(p, seenNames, a.Name, aat+".name", "attribute name")
		dup(p, seenFields, a.GoField, aat+".goField", "model field")
	}
}

func dup(p *problems, seen map[string]bool, value, path, what string) {
	if value == "" {
		return
	}
	if seen[value] {
		p.add(path, "%s %q is used more than once", what, value)
	}
	seen[value] = true
}

func (pr Provider) validate(p *problems) {
	required(p, "provider.name", pr.Name)
	required(p, "provider.goModule", pr.GoModule)
	required(p, "provider.typePrefix", pr.TypePrefix)
	required(p, "provider.sdk.modulePath", pr.SDK.ModulePath)
	required(p, "provider.sdk.clientType", pr.SDK.ClientType)

	switch pr.SDK.Dialect {
	case DialectRestyService:
	case DialectKiotaFluent:
		p.add(
			"provider.sdk.dialect",
			"%q is reserved but not yet implemented by the emitter",
			pr.SDK.Dialect,
		)
	default:
		p.add("provider.sdk.dialect", "%q is not a known dialect", pr.SDK.Dialect)
	}
}

func (r Resource) validate(at string, p *problems) {
	required(p, at+".key", r.Key)
	required(p, at+".name", r.Name)
	required(p, at+".goPackage", r.GoPackage)
	required(p, at+".goPackageAlias", r.GoPackageAlias)
	required(p, at+".goTypeName", r.GoTypeName)
	required(p, at+".modelTypeName", r.ModelTypeName)

	if len(r.Schema.Attributes) == 0 {
		p.add(at+".schema.attributes", "a resource with no attributes cannot be emitted")
	}

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}
	hasWritable := false

	for i, a := range r.Schema.Attributes {
		aat := fmt.Sprintf("%s.schema.attributes[%d]", at, i)
		if a.Name != "" {
			aat = fmt.Sprintf("%s.schema.attributes[%s]", at, a.Name)
		}
		if a.Drop {
			continue
		}

		a.validate(aat, p)
		a.validateForKind(BlockResource, aat, p)

		dup(p, seenNames, a.Name, aat+".name", "attribute name")
		// A duplicated Go field is the subtler failure: the schema is fine and
		// the model silently loses an attribute.
		dup(p, seenFields, a.GoField, aat+".goField", "model field")

		if !a.ComputedOptionalRequired.IsComputed() || a.ComputedOptionalRequired.IsOptional() {
			hasWritable = true
		}
	}

	r.Binding.validate(at+".binding", p)

	if r.Identity != nil {
		r.Identity.validate(r, at+".identity", p)
	}

	if r.List != nil {
		r.List.validate(r, at+".list", p)
	}
	r.validateIDBinding(at, seenNames, seenFields, p)
	r.validatePolicy(at, hasWritable, p)
	r.validateImport(at, seenNames, p)
}

// validateIDBinding checks the ID wiring points at an attribute that exists. A
// blueprint naming a nonexistent field here produces a generated create that
// assigns to nothing, which compiles only by coincidence.
func (r Resource) validateIDBinding(at string, names, fields map[string]bool, p *problems) {
	id := r.Binding.ID

	if id.Attribute == "" {
		p.add(at+".binding.id.attribute", "is required")
	} else if !names[id.Attribute] {
		p.add(
			at+".binding.id.attribute",
			"names attribute %q, which the resource does not declare",
			id.Attribute,
		)
	}

	if id.GoField == "" {
		p.add(at+".binding.id.goField", "is required")
	} else if !fields[id.GoField] {
		p.add(
			at+".binding.id.goField",
			"names model field %q, which the resource does not declare",
			id.GoField,
		)
	}

	// Only meaningful when there is a create to take an ID from.
	if r.Binding.Create != nil && id.FromCreate == "" {
		p.add(at+".binding.id.fromCreate", "is required when the resource has a create operation")
	}
}

func (r Resource) validatePolicy(at string, hasWritable bool, p *problems) {
	switch r.Policy.UpdateStyle {
	case UpdateMergePatch, UpdatePutFull:
		if r.Binding.Update == nil {
			p.add(
				at+".policy.updateStyle",
				"is %q but the resource has no update operation",
				r.Policy.UpdateStyle,
			)
		}
	case UpdateReplaceOnly:
		if r.Binding.Update != nil {
			p.add(
				at+".policy.updateStyle",
				"is %q but the resource declares an update operation",
				r.Policy.UpdateStyle,
			)
		}
	case "":
		// An update binding with no declared style is the dangerous gap: PUT and
		// PATCH differ in whether an omitted field is cleared, and guessing
		// wrong silently erases attributes the practitioner did not mention.
		if r.Binding.Update != nil {
			p.add(
				at+".policy.updateStyle",
				"is required when the resource has an update operation, "+
					"because whether an omitted field is preserved or cleared cannot be guessed",
			)
		}
	default:
		p.add(at+".policy.updateStyle", "%q is not a known update style", r.Policy.UpdateStyle)
	}

	// A resource with no update path and no writable attribute can never be
	// changed, which is almost always a modelling mistake rather than intent.
	if r.Binding.Update == nil && r.Policy.UpdateStyle != UpdateReplaceOnly && hasWritable {
		p.add(
			at+".policy.updateStyle",
			"should be %q: the resource has writable attributes but no update operation",
			UpdateReplaceOnly,
		)
	}
}

func (r Resource) validateImport(at string, names map[string]bool, p *problems) {
	switch r.Import.Style {
	case "", ImportUnsupported:
	case ImportPassthroughID:
		switch {
		case r.Import.Attribute == "":
			p.add(at+".import.attribute", "is required for a passthrough import")
		case !names[r.Import.Attribute]:
			p.add(
				at+".import.attribute",
				"names attribute %q, which the resource does not declare",
				r.Import.Attribute,
			)
		}
	default:
		p.add(at+".import.style", "%q is not a known import style", r.Import.Style)
	}
}

func (a Attribute) validate(at string, p *problems) {
	required(p, at+".name", a.Name)
	required(p, at+".goField", a.GoField)

	switch a.ComputedOptionalRequired {
	case Required, Optional, Computed, ComputedOptional:
	case "":
		p.add(at+".presence", "is required")
	default:
		p.add(at+".presence", "%q is not a known presence", a.ComputedOptionalRequired)
	}

	a.Type.validate(at+".type", p)

	// A default only takes effect on an attribute Terraform may fill in, so one
	// on a purely required or optional attribute is silently dead configuration.
	if a.Default != nil {
		if !a.ComputedOptionalRequired.IsComputed() {
			p.add(
				at+".default",
				"is set but presence is %q; a default only applies to a computed attribute",
				a.ComputedOptionalRequired,
			)
		}
		if a.Default.Static == nil && a.Default.Custom == nil {
			p.add(at+".default", "must set either static or custom")
		}
		if a.Default.Static != nil && a.Default.Custom != nil {
			p.add(at+".default", "sets both static and custom, which are mutually exclusive")
		}
	}

	// The wire directions are checked by validateForKind, because which of them an
	// attribute needs depends on whether its block kind sends anything to the API.
}

func (t AttrType) validate(at string, p *problems) {
	if t.NestedObject != nil && !t.Kind.IsNested() {
		p.add(at+".nested", "is set on non-nested kind %q", t.Kind)
	}

	switch t.Kind {
	case KindBool, KindString, KindInt32, KindInt64, KindFloat32, KindFloat64, KindNumber:
		if t.ElementType != nil {
			p.add(at+".elem", "is set on scalar kind %q", t.Kind)
		}
	case KindList, KindSet, KindMap:
		if t.ElementType == nil {
			p.add(at+".elem", "is required for collection kind %q", t.Kind)
			return
		}
		t.ElementType.validate(at+".elem", p)
	case KindListNested, KindSetNested, KindSingleNested:
		if t.ElementType != nil {
			p.add(at+".elem", "is set on nested kind %q, which uses nested instead", t.Kind)
		}
		if t.NestedObject == nil {
			p.add(at+".nested", "is required for nested kind %q", t.Kind)
			return
		}
		t.NestedObject.validate(at+".nested", p)
	case "":
		p.add(at+".kind", "is required")
	default:
		p.add(at+".kind", "%q is not a known type kind", t.Kind)
	}
}

func (n NestedAttributeObject) validate(at string, p *problems) {
	required(p, at+".goTypeName", n.GoTypeName)
	required(p, at+".sdkType", n.SDKType)
	required(p, at+".attrTypesVar", n.AttrTypesVar)
	required(p, at+".objectTypeVar", n.ObjectTypeVar)
	// expandFunc and flattenFunc are checked by validateWireForKind: a data source needs
	// only the flatten direction, so requiring both here would refuse a valid one.

	if len(n.Attributes) == 0 {
		p.add(at+".attributes", "a nested object with no attributes cannot be emitted")
		return
	}

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}

	for i, a := range n.Attributes {
		aat := fmt.Sprintf("%s.schema.attributes[%d]", at, i)
		if a.Name != "" {
			aat = fmt.Sprintf("%s.schema.attributes[%s]", at, a.Name)
		}
		if a.Drop {
			continue
		}

		a.validate(aat, p)

		dup(p, seenNames, a.Name, aat+".name", "attribute name")
		dup(p, seenFields, a.GoField, aat+".goField", "model field")
	}
}

func (b ResourceBinding) validate(at string, p *problems) {
	required(p, at+".service.importPath", b.Service.ImportPath)
	required(p, at+".service.typeName", b.Service.TypeName)
	required(p, at+".service.accessor", b.Service.Accessor)

	required(p, at+".body.requestType", b.Body.RequestType)
	required(p, at+".body.responseType", b.Body.ResponseType)
	required(p, at+".body.constructorExpr", b.Body.ConstructorExpr)

	switch b.Body.AccessStyle {
	case AccessStructField:
	case AccessMethod:
		p.add(
			at+".body.accessStyle",
			"%q is reserved but not yet implemented by the emitter",
			b.Body.AccessStyle,
		)
	default:
		p.add(at+".body.accessStyle", "%q is not a known access style", b.Body.AccessStyle)
	}

	// Read is what refreshes state, so a resource without one cannot participate
	// in a plan at all.
	if b.Read == nil {
		p.add(at+".read", "is required: a resource with no read operation cannot refresh state")
	}

	for name, op := range map[string]*Operation{
		"create": b.Create, "read": b.Read, "update": b.Update, "delete": b.Delete,
	} {
		if op != nil {
			op.validate(at+"."+name, p)
		}
	}
}

// validate checks a data source's binding.
//
// Read is not merely required, it is the whole binding: a data source that cannot read is
// not a data source. The resource equivalent has three more operations and a request
// body, none of which exist on this type, so there is nothing here to refuse.
// validate checks an identity schema.
//
// Every attribute must name a resource attribute it mirrors, because generated code copies
// the value out of the resource model rather than fetching it again -- and a name that does
// not resolve would be a compile error in someone else's provider.
func (ri ResourceIdentity) validate(r Resource, at string, p *problems) {
	required(p, at+".goTypeName", ri.GoTypeName)

	if len(ri.Attributes) == 0 {
		p.add(at+".attributes", "an identity schema with no attributes cannot be emitted")
		return
	}

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}

	for i, a := range ri.Attributes {
		aat := fmt.Sprintf("%s.attributes[%d]", at, i)
		if a.Name != "" {
			aat = fmt.Sprintf("%s.attributes[%s]", at, a.Name)
		}

		required(p, aat+".name", a.Name)
		required(p, aat+".goField", a.GoField)

		dup(p, seenNames, a.Name, aat+".name", "identity attribute name")
		dup(p, seenFields, a.GoField, aat+".goField", "identity model field")

		// The framework's identityschema has scalars and a list of scalars, and nothing
		// else. A nested identity is not expressible there, so refusing it here names the
		// attribute instead of producing a type that does not exist.
		switch {
		case a.Kind == "":
			p.add(aat+".kind", "is required")
		case a.Kind.IsNested(), a.Kind == KindMap:
			p.add(aat+".kind",
				"%q has no identityschema counterpart; an identity is scalars, or a list of them",
				a.Kind)
		}

		// Exactly one of the two flags. Neither means the attribute can never be supplied;
		// both is a contradiction the framework rejects at runtime, which is a worse place
		// to find out.
		switch {
		case !a.RequiredForImport && !a.OptionalForImport:
			p.add(aat, "must set either requiredForImport or optionalForImport")
		case a.RequiredForImport && a.OptionalForImport:
			p.add(aat, "sets both requiredForImport and optionalForImport, which are exclusive")
		}

		if a.FromAttribute == "" {
			p.add(aat+".fromAttribute",
				"is required: generated code copies the identity value out of the resource model")
			continue
		}
		if !hasAttributeNamed(r.Schema.Attributes, a.FromAttribute) {
			p.add(aat+".fromAttribute",
				"names attribute %q, which this resource does not declare", a.FromAttribute)
		}
	}
}

// validate checks a list facet.
//
// The first refusal is the one that matters: the framework raises an error diagnostic for a
// ListResult with no identity, so a list resource on a resource that declares none cannot
// work. Catching it here names both halves; at runtime it surfaces as a query that fails
// with nothing to point at.
func (lf ListFacet) validate(r Resource, at string, p *problems) {
	if r.Identity == nil {
		p.add(at,
			"requires the resource to declare an identity: ListResult.Identity is mandatory, "+
				"so a list resource without one cannot produce a usable result")
		return
	}

	required(p, at+".goTypeName", lf.GoTypeName)
	required(p, at+".service.importPath", lf.Service.ImportPath)
	required(p, at+".service.typeName", lf.Service.TypeName)
	required(p, at+".service.accessor", lf.Service.Accessor)
	required(p, at+".elementType", lf.ElementType)
	required(p, at+".response.type", lf.Response.Type)

	if lf.Read == nil {
		p.add(at+".read",
			"is required: a list resource's whole requirement of the API is a collection read")
	} else {
		lf.Read.validate(at+".read", p)
	}

	if len(lf.IdentityFrom) == 0 {
		p.add(at+".identityFrom",
			"is required: every result must carry an identity, so each identity field needs a "+
				"source on the element")
		return
	}

	// Every identity field must be filled. A partially-populated identity is worse than
	// none: Terraform would record an address that does not resolve.
	want := map[string]bool{}
	for _, ia := range r.Identity.Attributes {
		want[ia.GoField] = true
	}

	seen := map[string]bool{}

	for i, m := range lf.IdentityFrom {
		mat := fmt.Sprintf("%s.identityFrom[%d]", at, i)

		required(p, mat+".goField", m.GoField)
		required(p, mat+".fromSdkField", m.FromSDKField)

		if m.GoField == "" {
			continue
		}
		if !want[m.GoField] {
			p.add(mat+".goField",
				"names %q, which the resource's identity does not declare", m.GoField)
			continue
		}
		dup(p, seen, m.GoField, mat+".goField", "identity mapping")
	}

	for _, ia := range r.Identity.Attributes {
		if !seen[ia.GoField] {
			p.add(at+".identityFrom",
				"does not fill identity field %q; a partly-filled identity records an address "+
					"that does not resolve", ia.GoField)
		}
	}
}

// hasAttributeNamed reports whether a schema declares an attribute by that name, ignoring
// dropped ones -- a dropped attribute is not in the generated model, so an identity cannot
// read from it.
func hasAttributeNamed(attrs []Attribute, name string) bool {
	for _, a := range attrs {
		if a.Name == name && !a.Drop {
			return true
		}
	}
	return false
}

func (b DataSourceBinding) validate(at string, p *problems) {
	required(p, at+".service.importPath", b.Service.ImportPath)
	required(p, at+".service.typeName", b.Service.TypeName)
	required(p, at+".service.accessor", b.Service.Accessor)

	required(p, at+".response.type", b.Response.Type)

	switch b.Response.AccessStyle {
	case AccessStructField:
	case AccessMethod:
		p.add(
			at+".response.accessStyle",
			"%q is reserved but not yet implemented by the emitter",
			b.Response.AccessStyle,
		)
	default:
		p.add(at+".response.accessStyle", "%q is not a known access style", b.Response.AccessStyle)
	}

	if b.Read == nil {
		p.add(at+".read", "is required: a data source with no read operation has nothing to do")
		return
	}

	b.Read.validate(at+".read", p)
}

func (o Operation) validate(at string, p *problems) {
	switch o.Style {
	case CallStyleMethod:
		required(p, at+".method", o.Method)
	case CallStyleFluent:
		p.add(at+".style", "%q is reserved but not yet implemented by the emitter", o.Style)
	default:
		p.add(at+".style", "%q is not a known call style", o.Style)
	}

	switch o.Return {
	case ReturnResultTransportError, ReturnTransportError, ReturnResultError, ReturnError:
	case "":
		p.add(at+".return", "is required")
	default:
		p.add(at+".return", "%q is not a known return arity", o.Return)
	}

	// The arity and the result type have to agree, or the generated assignment
	// has the wrong number of values on one side.
	if o.Return.HasResult() && o.ResultType == "" {
		p.add(at+".resultType", "is required for return arity %q", o.Return)
	}
	if !o.Return.HasResult() && o.ResultType != "" {
		p.add(at+".resultType", "is set but return arity %q yields no result", o.Return)
	}

	for i, a := range o.Args {
		a.validate(fmt.Sprintf("%s.args[%d]", at, i), p)
	}
}

func (a Argument) validate(at string, p *problems) {
	switch a.Kind {
	case ArgContext, ArgBody:
	case ArgStateField, ArgPlanField, ArgConfigField:
		if a.Field == "" && a.Expr == "" {
			p.add(at, "kind %q needs either field or expr", a.Kind)
		}
	case ArgLiteral:
		if a.Expr == "" {
			p.add(at+".expr", "is required for a literal argument")
		}
	case "":
		p.add(at+".kind", "is required")
	default:
		p.add(at+".kind", "%q is not a known argument kind", a.Kind)
	}
}

func required(p *problems, path, value string) {
	if strings.TrimSpace(value) == "" {
		p.add(path, "is required")
	}
}
