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
		dup(&p, seenTypes, r.TerraformType, at+".terraformType", "Terraform type")
		dup(&p, seenAliases, r.GoPackageAlias, at+".goPackageAlias", "import alias")
	}

	return p.err()
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
		p.add("provider.sdk.dialect", "%q is reserved but not yet implemented by the emitter", pr.SDK.Dialect)
	default:
		p.add("provider.sdk.dialect", "%q is not a known dialect", pr.SDK.Dialect)
	}
}

func (r Resource) validate(at string, p *problems) {
	required(p, at+".key", r.Key)
	required(p, at+".terraformType", r.TerraformType)
	required(p, at+".goPackage", r.GoPackage)
	required(p, at+".goPackageAlias", r.GoPackageAlias)
	required(p, at+".goTypeName", r.GoTypeName)
	required(p, at+".modelTypeName", r.ModelTypeName)

	if len(r.Attributes) == 0 {
		p.add(at+".attributes", "a resource with no attributes cannot be emitted")
	}

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}
	hasWritable := false

	for i, a := range r.Attributes {
		aat := fmt.Sprintf("%s.attributes[%d]", at, i)
		if a.Name != "" {
			aat = fmt.Sprintf("%s.attributes[%s]", at, a.Name)
		}
		if a.Drop {
			continue
		}

		a.validate(aat, p)

		dup(p, seenNames, a.Name, aat+".name", "attribute name")
		// A duplicated Go field is the subtler failure: the schema is fine and
		// the model silently loses an attribute.
		dup(p, seenFields, a.GoField, aat+".goField", "model field")

		if !a.Presence.IsComputed() || a.Presence.IsOptional() {
			hasWritable = true
		}
	}

	r.Binding.validate(at+".binding", p)
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
		p.add(at+".binding.id.attribute", "names attribute %q, which the resource does not declare", id.Attribute)
	}

	if id.GoField == "" {
		p.add(at+".binding.id.goField", "is required")
	} else if !fields[id.GoField] {
		p.add(at+".binding.id.goField", "names model field %q, which the resource does not declare", id.GoField)
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
			p.add(at+".policy.updateStyle", "is %q but the resource has no update operation", r.Policy.UpdateStyle)
		}
	case UpdateReplaceOnly:
		if r.Binding.Update != nil {
			p.add(at+".policy.updateStyle", "is %q but the resource declares an update operation", r.Policy.UpdateStyle)
		}
	case "":
		// An update binding with no declared style is the dangerous gap: PUT and
		// PATCH differ in whether an omitted field is cleared, and guessing
		// wrong silently erases attributes the practitioner did not mention.
		if r.Binding.Update != nil {
			p.add(at+".policy.updateStyle", "is required when the resource has an update operation, "+
				"because whether an omitted field is preserved or cleared cannot be guessed")
		}
	default:
		p.add(at+".policy.updateStyle", "%q is not a known update style", r.Policy.UpdateStyle)
	}

	// A resource with no update path and no writable attribute can never be
	// changed, which is almost always a modelling mistake rather than intent.
	if r.Binding.Update == nil && r.Policy.UpdateStyle != UpdateReplaceOnly && hasWritable {
		p.add(at+".policy.updateStyle", "should be %q: the resource has writable attributes but no update operation",
			UpdateReplaceOnly)
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
			p.add(at+".import.attribute", "names attribute %q, which the resource does not declare", r.Import.Attribute)
		}
	default:
		p.add(at+".import.style", "%q is not a known import style", r.Import.Style)
	}
}

func (a Attribute) validate(at string, p *problems) {
	required(p, at+".name", a.Name)
	required(p, at+".goField", a.GoField)

	switch a.Presence {
	case Required, Optional, Computed, ComputedOptional:
	case "":
		p.add(at+".presence", "is required")
	default:
		p.add(at+".presence", "%q is not a known presence", a.Presence)
	}

	a.Type.validate(at+".type", p)

	// A default only takes effect on an attribute Terraform may fill in, so one
	// on a purely required or optional attribute is silently dead configuration.
	if a.Default != nil {
		if !a.Presence.IsComputed() {
			p.add(at+".default", "is set but presence is %q; a default only applies to a computed attribute", a.Presence)
		}
		if a.Default.Static == nil && a.Default.Custom == nil {
			p.add(at+".default", "must set either static or custom")
		}
		if a.Default.Static != nil && a.Default.Custom != nil {
			p.add(at+".default", "sets both static and custom, which are mutually exclusive")
		}
	}

	// An attribute the practitioner can set must have a way to reach the API,
	// and one that is read must have a way back. Catching this here is the
	// difference between a clear message and a silently inert attribute.
	if a.Presence.IsRequired() || a.Presence.IsOptional() {
		if a.Wire.SkipExpand {
			p.add(at+".wire.skipExpand", "is set on a writable attribute, so its value would never reach the API")
		} else if a.Wire.Expand == nil {
			p.add(at+".wire.expand", "is required on a writable attribute")
		}
	}
	if !a.Wire.SkipFlatten && a.Wire.Flatten == nil {
		p.add(at+".wire.flatten", "is required unless skipFlatten is set")
	}
}

func (t AttrType) validate(at string, p *problems) {
	if t.Nested != nil && !t.Kind.IsNested() {
		p.add(at+".nested", "is set on non-nested kind %q", t.Kind)
	}

	switch t.Kind {
	case KindBool, KindString, KindInt32, KindInt64, KindFloat32, KindFloat64, KindNumber:
		if t.Elem != nil {
			p.add(at+".elem", "is set on scalar kind %q", t.Kind)
		}
	case KindList, KindSet, KindMap:
		if t.Elem == nil {
			p.add(at+".elem", "is required for collection kind %q", t.Kind)
			return
		}
		t.Elem.validate(at+".elem", p)
	case KindListNested, KindSetNested, KindSingleNested:
		if t.Elem != nil {
			p.add(at+".elem", "is set on nested kind %q, which uses nested instead", t.Kind)
		}
		if t.Nested == nil {
			p.add(at+".nested", "is required for nested kind %q", t.Kind)
			return
		}
		t.Nested.validate(at+".nested", p)
	case "":
		p.add(at+".kind", "is required")
	default:
		p.add(at+".kind", "%q is not a known type kind", t.Kind)
	}
}

func (n Nested) validate(at string, p *problems) {
	required(p, at+".goTypeName", n.GoTypeName)
	required(p, at+".sdkType", n.SDKType)
	required(p, at+".attrTypesVar", n.AttrTypesVar)
	required(p, at+".objectTypeVar", n.ObjectTypeVar)
	required(p, at+".expandFunc", n.ExpandFunc)
	required(p, at+".flattenFunc", n.FlattenFunc)

	if len(n.Attributes) == 0 {
		p.add(at+".attributes", "a nested object with no attributes cannot be emitted")
		return
	}

	seenNames := map[string]bool{}
	seenFields := map[string]bool{}

	for i, a := range n.Attributes {
		aat := fmt.Sprintf("%s.attributes[%d]", at, i)
		if a.Name != "" {
			aat = fmt.Sprintf("%s.attributes[%s]", at, a.Name)
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
		p.add(at+".body.accessStyle", "%q is reserved but not yet implemented by the emitter", b.Body.AccessStyle)
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
		aat := fmt.Sprintf("%s.args[%d]", at, i)
		switch a.Kind {
		case ArgContext, ArgBody:
		case ArgStateField, ArgPlanField:
			if a.Field == "" && a.Expr == "" {
				p.add(aat, "kind %q needs either field or expr", a.Kind)
			}
		case ArgLiteral:
			if a.Expr == "" {
				p.add(aat+".expr", "is required for a literal argument")
			}
		case "":
			p.add(aat+".kind", "is required")
		default:
			p.add(aat+".kind", "%q is not a known argument kind", a.Kind)
		}
	}
}

func required(p *problems, path, value string) {
	if strings.TrimSpace(value) == "" {
		p.add(path, "is required")
	}
}
