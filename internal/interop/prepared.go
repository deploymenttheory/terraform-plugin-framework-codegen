package interop

import (
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-codegen-spec/code"
	"github.com/hashicorp/terraform-plugin-codegen-spec/schema"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// prepared is one blueprint attribute with every package-neutral upstream
// sub-value already converted.
//
// The official format declares its attributes three times over -- once each in the
// resource, datasource and provider packages -- as a fat struct with one pointer
// field per type, exactly one of which is non-nil. Those three fat structs are
// distinct Go types, but everything *inside* them comes from the shared schema
// package: presence, descriptions, element types, validators, defaults. So this
// type holds all of that, converted once, and the per-package assembler does
// nothing but choose which pointer field to set.
//
// It is the same division the house draws between internal/render and
// internal/templates: every decision happens here, and reading export_resource.go
// should tell you the shape of the output without containing any logic to reason
// about. It carries no JSON tags because it is never serialized, which is also why
// tagliatelle has nothing to say about it.
type prepared struct {
	name string

	// kind is the upstream kind, already coarsened: an int32 blueprint attribute
	// arrives here as KindInt64, with the loss already reported.
	kind     blueprint.TypeKind
	presence schema.ComputedOptionalRequired

	// description carries the blueprint's markdown text. The official format has no
	// attribute-level markdown field -- only `description` -- so the markdown lands
	// there verbatim and the mismatch in declared contract is reported once per
	// resource rather than once per attribute.
	description *string
	deprecation *string
	sensitive   *bool

	validators []schema.CustomValidator
	modifiers  []schema.CustomPlanModifier

	defCustom *schema.CustomDefault
	defStatic *staticValue

	// elem is set for list, set and map.
	elem *schema.ElementType
	// nested is set for the three nested kinds.
	nested *preparedObject
}

// preparedObject is the object shape a nested attribute holds.
type preparedObject struct {
	attrs []prepared
	// external is the SDK struct this object maps to. It is the one piece of the
	// blueprint's binding the official format can carry, via
	// associated_external_type, so it survives a round trip.
	external *schema.AssociatedExternalType
}

// staticValue is a parsed static default, typed by the kind it belongs to.
//
// Exactly one field is non-nil. The blueprint stores a default as a Go literal
// string, because that is what the emitter writes into generated source; the
// official format wants a JSON value of the matching type. So export has to parse,
// and this is what it parses into.
type staticValue struct {
	boolVal  *bool
	strVal   *string
	intVal   *int64
	floatVal *float64
}

// attrLosses accumulates the losses that fall on every attribute alike.
//
// A model field name and a wire binding are present on all of them, so reporting
// each one individually produces a page of identical lines: the pilot alone yields
// twenty-two "goField has no counterpart" notes and twenty-four for wire. That
// buries the losses a reader actually has to think about -- a coarsened int32, a
// populated Behavior, an unrepresentable default -- which are selective and stay
// addressed per attribute. So the uniform ones are counted here and reported once
// per resource with the count.
type attrLosses struct {
	goField int
	wire    int
}

// prepare converts one blueprint attribute, recording every loss against path.
//
// forResource controls whether plan modifiers and defaults are carried: the
// datasource package has neither field, because a data source is read-only and
// neither concept applies to it.
func prepare(
	a blueprint.Attribute,
	path string,
	forResource bool,
	r *Report,
	acc *attrLosses,
) (prepared, error) {
	p := prepared{
		name:        a.Name,
		description: strPtr(a.MarkdownDescription),
		deprecation: strPtr(a.DeprecationMessage),
		sensitive:   boolPtr(a.Sensitive),
	}

	presence, err := presenceOf(a.ComputedOptionalRequired)
	if err != nil {
		return prepared{}, fmt.Errorf("%s.presence: %w", path, err)
	}
	p.presence = presence

	p.kind = coarsen(a.Type.Kind, path, r)

	// The uniform losses are counted for one aggregate note per resource; the
	// selective ones are addressed here. Behavior in particular must be reported
	// only when populated: an unprobed blueprint would otherwise produce a note per
	// attribute saying nothing was observed.
	if a.GoField != "" {
		acc.goField++
	}
	if a.Wire != (blueprint.WireBinding{}) {
		acc.wire++
	}
	if a.Behavior != (blueprint.Behavior{}) {
		r.note("behavior", path+".behavior")
	}

	for _, v := range a.Validators {
		p.validators = append(p.validators, schema.CustomValidator{
			SchemaDefinition: v.SchemaDefinition,
			Imports:          importsOf(v.Imports),
		})
	}

	if forResource {
		for _, m := range a.PlanModifiers {
			p.modifiers = append(p.modifiers, schema.CustomPlanModifier{
				SchemaDefinition: m.SchemaDefinition,
				Imports:          importsOf(m.Imports),
			})
		}
	} else if len(a.PlanModifiers) > 0 {
		r.add(
			SeverityLossy,
			path+".planModifiers",
			"a data source attribute has no plan modifiers in this format; %d dropped",
			len(a.PlanModifiers),
		)
	}

	if a.Default != nil {
		if err := p.applyDefault(a, path, forResource, r); err != nil {
			return prepared{}, err
		}
	}

	switch {
	case p.kind.IsCollection():
		elem, err := elementTypeOf(a.Type.ElementType, path+".elem", r)
		if err != nil {
			return prepared{}, err
		}
		p.elem = elem

	case p.kind.IsNested():
		obj, err := prepareObject(a.Type.NestedObject, path, forResource, r, acc)
		if err != nil {
			return prepared{}, err
		}
		p.nested = obj
	}

	return p, nil
}

// applyDefault converts a default, refusing the combinations the format cannot
// express rather than approximating them.
func (p *prepared) applyDefault(
	a blueprint.Attribute,
	path string,
	forResource bool,
	r *Report,
) error {
	if !forResource {
		r.add(SeverityLossy, path+".default",
			"a data source attribute has no default in this format; the default was dropped")
		return nil
	}

	if a.Default.Custom != nil {
		p.defCustom = &schema.CustomDefault{
			SchemaDefinition: a.Default.Custom.SchemaDefinition,
			Imports:          importsOf(a.Default.Custom.Imports),
		}
	}

	if a.Default.Static == nil {
		return nil
	}

	static, err := parseStatic(*a.Default.Static, p.kind)
	if err != nil {
		return fmt.Errorf("%s.default.static: %w", path, err)
	}
	p.defStatic = static

	return nil
}

// coarsen widens a blueprint kind to the nearest kind the official format has,
// reporting the widening.
//
// int32 and float32 are the only two cases, and widening is the right answer for
// both: an int64 schema accepts everything an int32 schema does, so the exported
// document is weaker but not wrong. Refusing instead would make a perfectly legal
// blueprint inexportable, which would defeat the purpose of having an export.
func coarsen(k blueprint.TypeKind, path string, r *Report) blueprint.TypeKind {
	switch k {
	case blueprint.KindInt32:
		r.note("type.kind/int32", path+".type.kind")
		return blueprint.KindInt64
	case blueprint.KindFloat32:
		r.note("type.kind/float32", path+".type.kind")
		return blueprint.KindFloat64
	default:
		return k
	}
}

// presenceOf maps blueprint presence onto the upstream enum.
//
// The four values are spelled identically in both formats, which invites a cast.
// A cast would be wrong: it would launder an unrecognized blueprint value straight
// into the JSON, where the only thing standing between it and a committed document
// is upstream's schema validation, reported as an opaque enum violation a long way
// from the attribute that caused it. A switch with an explicit default names the
// offender instead.
func presenceOf(p blueprint.ComputedOptionalRequired) (schema.ComputedOptionalRequired, error) {
	switch p {
	case blueprint.Required:
		return schema.Required, nil
	case blueprint.Optional:
		return schema.Optional, nil
	case blueprint.Computed:
		return schema.Computed, nil
	case blueprint.ComputedOptional:
		return schema.ComputedOptional, nil
	default:
		return "", fmt.Errorf("%w: unknown presence %q", ErrUnrepresentable, p)
	}
}

// parseStatic turns a blueprint literal into a typed upstream default.
//
// Two distinct refusals live here. The first is a kind whose upstream default type
// has no Static field at all: number, the collections and the nested kinds carry
// only a custom default, so a static default on one of them cannot cross. The
// second is a Raw string that is not a Go literal -- a bare identifier or a
// qualified constant. Neither is silently rerouted into Custom, because Custom
// expects a full expression such as stringdefault.StaticString("x") and would
// generate code that does not compile.
func parseStatic(lit blueprint.Literal, kind blueprint.TypeKind) (*staticValue, error) {
	// The literal's own kind is advisory; the attribute's kind decides which
	// upstream default type is in play. They disagree only in a malformed
	// blueprint, and blueprint.Validate is what catches that.
	switch kind {
	case blueprint.KindBool:
		v, err := strconv.ParseBool(lit.Raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %q is not a bool literal", ErrUnrepresentable, lit.Raw)
		}
		return &staticValue{boolVal: &v}, nil

	case blueprint.KindString:
		// Unquote accepts both interpreted and raw string literals, and rejects
		// anything that is not a literal at all, which is exactly the line we want.
		v, err := strconv.Unquote(lit.Raw)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: %q is not a quoted string literal",
				ErrUnrepresentable,
				lit.Raw,
			)
		}
		return &staticValue{strVal: &v}, nil

	case blueprint.KindInt32, blueprint.KindInt64:
		v, err := strconv.ParseInt(lit.Raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: %q is not an integer literal", ErrUnrepresentable, lit.Raw)
		}
		return &staticValue{intVal: &v}, nil

	case blueprint.KindFloat32, blueprint.KindFloat64:
		v, err := strconv.ParseFloat(lit.Raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: %q is not a float literal", ErrUnrepresentable, lit.Raw)
		}
		return &staticValue{floatVal: &v}, nil

	default:
		return nil, fmt.Errorf(
			"%w: a %s attribute has no static default in this format",
			ErrUnrepresentable,
			kind,
		)
	}
}

// elementTypeOf builds the element type of a list, set or map.
func elementTypeOf(t *blueprint.AttrType, path string, r *Report) (*schema.ElementType, error) {
	if t == nil {
		return nil, fmt.Errorf(
			"%s: %w: a collection with no element type",
			path,
			ErrUnrepresentable,
		)
	}

	kind := coarsen(t.Kind, path, r)

	switch kind {
	case blueprint.KindBool:
		return &schema.ElementType{Bool: &schema.BoolType{}}, nil
	case blueprint.KindString:
		return &schema.ElementType{String: &schema.StringType{}}, nil
	case blueprint.KindInt64:
		return &schema.ElementType{Int64: &schema.Int64Type{}}, nil
	case blueprint.KindFloat64:
		return &schema.ElementType{Float64: &schema.Float64Type{}}, nil
	case blueprint.KindNumber:
		return &schema.ElementType{Number: &schema.NumberType{}}, nil

	case blueprint.KindList, blueprint.KindSet, blueprint.KindMap:
		inner, err := elementTypeOf(t.ElementType, path+".elem", r)
		if err != nil {
			return nil, err
		}
		switch kind {
		case blueprint.KindList:
			return &schema.ElementType{List: &schema.ListType{ElementType: *inner}}, nil
		case blueprint.KindSet:
			return &schema.ElementType{Set: &schema.SetType{ElementType: *inner}}, nil
		default:
			return &schema.ElementType{Map: &schema.MapType{ElementType: *inner}}, nil
		}

	case blueprint.KindListNested, blueprint.KindSetNested, blueprint.KindSingleNested:
		// A nested shape inside a collection's element type flattens to an object
		// type, which is a real loss: an object type has no per-field presence,
		// description or validators. The blueprint does not produce this today --
		// nested kinds live on the attribute, not in Elem -- so this branch exists
		// to report rather than to panic if that changes.
		obj, err := objectTypeOf(t.NestedObject, path, r)
		if err != nil {
			return nil, err
		}
		r.add(
			SeverityLossy,
			path,
			"a nested object inside an element type becomes a plain object type, losing per-field presence and documentation",
		)
		return &schema.ElementType{Object: obj}, nil

	default:
		return nil, fmt.Errorf("%s: %w: element kind %q", path, ErrUnrepresentable, t.Kind)
	}
}

// objectTypeOf flattens a nested shape into an object type.
func objectTypeOf(
	n *blueprint.NestedAttributeObject,
	path string,
	r *Report,
) (*schema.ObjectType, error) {
	if n == nil {
		return nil, fmt.Errorf(
			"%s: %w: a nested kind with no object shape",
			path,
			ErrUnrepresentable,
		)
	}

	out := &schema.ObjectType{}

	for _, a := range n.Attributes {
		if a.Drop {
			r.Omitted++
			continue
		}

		at := schema.ObjectAttributeType{Name: a.Name}
		kind := coarsen(a.Type.Kind, path+"."+a.Name, r)

		switch kind {
		case blueprint.KindBool:
			at.Bool = &schema.BoolType{}
		case blueprint.KindString:
			at.String = &schema.StringType{}
		case blueprint.KindInt64:
			at.Int64 = &schema.Int64Type{}
		case blueprint.KindFloat64:
			at.Float64 = &schema.Float64Type{}
		case blueprint.KindNumber:
			at.Number = &schema.NumberType{}
		default:
			return nil, fmt.Errorf("%s.%s: %w: object attribute kind %q",
				path, a.Name, ErrUnrepresentable, a.Type.Kind)
		}

		out.AttributeTypes = append(out.AttributeTypes, at)
	}

	return out, nil
}

// prepareObject converts a nested object shape and its children.
func prepareObject(
	n *blueprint.NestedAttributeObject,
	path string,
	forResource bool,
	r *Report,
	acc *attrLosses,
) (*preparedObject, error) {
	if n == nil {
		return nil, fmt.Errorf(
			"%s: %w: a nested kind with no object shape",
			path,
			ErrUnrepresentable,
		)
	}

	out := &preparedObject{}

	// The generated identifiers -- model name, attr.Type variable, expand and
	// flatten helpers -- have no counterpart and are re-derived on import, so they
	// are one note rather than five.
	r.note("type.nested.names", path+".type.nested")

	if n.SDKType != "" {
		out.external = &schema.AssociatedExternalType{Type: n.SDKType}
		r.note("type.nested.sdkType", path+".type.nested.sdkType")
	}

	for _, child := range n.Attributes {
		if child.Drop {
			r.Omitted++
			continue
		}

		p, err := prepare(
			child,
			fmt.Sprintf("%s.nested[%s]", path, child.Name),
			forResource,
			r,
			acc,
		)
		if err != nil {
			return nil, err
		}
		out.attrs = append(out.attrs, p)
	}

	if len(out.attrs) == 0 {
		return nil, fmt.Errorf(
			"%s: %w: a nested object with no attributes",
			path,
			ErrUnrepresentable,
		)
	}

	return out, nil
}

// importsOf maps blueprint imports onto the upstream code.Import shape.
func importsOf(in []blueprint.Import) []code.Import {
	if len(in) == 0 {
		return nil
	}

	out := make([]code.Import, 0, len(in))
	for _, i := range in {
		out = append(out, code.Import{Path: i.Path, Alias: strPtr(i.Alias)})
	}

	return out
}
