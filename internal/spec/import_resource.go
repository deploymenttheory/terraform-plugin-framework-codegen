package spec

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-codegen-spec/code"
	"github.com/hashicorp/terraform-plugin-codegen-spec/resource"
	"github.com/hashicorp/terraform-plugin-codegen-spec/schema"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// importAttributes converts a schema's attributes and blocks into blueprint
// attributes.
//
// Blocks land in the same list as attributes, converted to their nested-attribute
// equivalent. The blueprint has no notion of a block -- see TypeKind's comment on
// why that choice is deliberate -- so this is the only representation available, and
// importResource has already reported it.
func importAttributes(
	attrs resource.Attributes,
	blocks resource.Blocks,
	path string,
	r *Report,
	acc *importLosses,
) ([]blueprint.Attribute, error) {
	out := make([]blueprint.Attribute, 0, len(attrs)+len(blocks))

	for _, a := range attrs {
		converted, err := importAttribute(a, path, r, acc)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
		r.Attributes++
	}

	for _, b := range blocks {
		converted, err := importBlock(b, path, r, acc)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
		r.Attributes++
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%s: %w: a schema with no attributes", path, ErrInvalidSpec)
	}

	return out, nil
}

// the mapping in a helper a reader has to jump to; the whole value of this function
// is that the fourteen cases are readable side by side.
//
//nolint:gocognit,cyclop // One case per upstream kind. Splitting it would put half
func importAttribute(
	a resource.Attribute,
	parent string,
	r *Report,
	acc *importLosses,
) (blueprint.Attribute, error) {
	path := fmt.Sprintf("%s.attributes[%s]", parent, a.Name)

	if a.Name == "" {
		return blueprint.Attribute{}, fmt.Errorf(
			"%s: %w: an attribute with no name",
			parent,
			ErrInvalidSpec,
		)
	}

	out := blueprint.Attribute{
		Name: a.Name,
		// The model field name is not in the document, so it is derived. Reported
		// per resource by importResource rather than per attribute.
		GoField: namingOpts.GoFieldName(a.Name),
	}

	switch {
	case a.Bool != nil:
		out.Type = blueprint.AttrType{Kind: blueprint.KindBool}
		out.ComputedOptionalRequired = presenceFrom(a.Bool.ComputedOptionalRequired)
		out.MarkdownDescription = describe(nil, a.Bool.Description, acc)
		out.DeprecationMessage = derefStr(a.Bool.DeprecationMessage)
		out.Sensitive = derefBool(a.Bool.Sensitive)
		out.Validators = customCodes(a.Bool.Validators.CustomValidators())
		out.PlanModifiers = planModifierCodes(a.Bool.PlanModifiers.CustomPlanModifiers())
		out.Default = boolDefaultFrom(a.Bool.Default)
		noteCustomType(a.Bool.CustomType, path, r)

	case a.String != nil:
		out.Type = blueprint.AttrType{Kind: blueprint.KindString}
		out.ComputedOptionalRequired = presenceFrom(a.String.ComputedOptionalRequired)
		out.MarkdownDescription = describe(nil, a.String.Description, acc)
		out.DeprecationMessage = derefStr(a.String.DeprecationMessage)
		out.Sensitive = derefBool(a.String.Sensitive)
		out.Validators = customCodes(a.String.Validators.CustomValidators())
		out.PlanModifiers = planModifierCodes(a.String.PlanModifiers.CustomPlanModifiers())
		out.Default = stringDefaultFrom(a.String.Default)
		noteCustomType(a.String.CustomType, path, r)

	case a.Int64 != nil:
		out.Type = blueprint.AttrType{Kind: blueprint.KindInt64}
		out.ComputedOptionalRequired = presenceFrom(a.Int64.ComputedOptionalRequired)
		out.MarkdownDescription = describe(nil, a.Int64.Description, acc)
		out.DeprecationMessage = derefStr(a.Int64.DeprecationMessage)
		out.Sensitive = derefBool(a.Int64.Sensitive)
		out.Validators = customCodes(a.Int64.Validators.CustomValidators())
		out.PlanModifiers = planModifierCodes(a.Int64.PlanModifiers.CustomPlanModifiers())
		out.Default = int64DefaultFrom(a.Int64.Default)
		noteCustomType(a.Int64.CustomType, path, r)

	case a.Float64 != nil:
		out.Type = blueprint.AttrType{Kind: blueprint.KindFloat64}
		out.ComputedOptionalRequired = presenceFrom(a.Float64.ComputedOptionalRequired)
		out.MarkdownDescription = describe(nil, a.Float64.Description, acc)
		out.DeprecationMessage = derefStr(a.Float64.DeprecationMessage)
		out.Sensitive = derefBool(a.Float64.Sensitive)
		out.Validators = customCodes(a.Float64.Validators.CustomValidators())
		out.PlanModifiers = planModifierCodes(a.Float64.PlanModifiers.CustomPlanModifiers())
		out.Default = float64DefaultFrom(a.Float64.Default)
		noteCustomType(a.Float64.CustomType, path, r)

	case a.Number != nil:
		out.Type = blueprint.AttrType{Kind: blueprint.KindNumber}
		out.ComputedOptionalRequired = presenceFrom(a.Number.ComputedOptionalRequired)
		out.MarkdownDescription = describe(nil, a.Number.Description, acc)
		out.DeprecationMessage = derefStr(a.Number.DeprecationMessage)
		out.Sensitive = derefBool(a.Number.Sensitive)
		out.Validators = customCodes(a.Number.Validators.CustomValidators())
		out.PlanModifiers = planModifierCodes(a.Number.PlanModifiers.CustomPlanModifiers())
		out.Default = customDefaultOnly(a.Number.Default.CustomDefault())
		noteCustomType(a.Number.CustomType, path, r)

	case a.List != nil:
		elem, err := elemFrom(a.List.ElementType, path, r)
		if err != nil {
			return blueprint.Attribute{}, err
		}
		out.Type = blueprint.AttrType{Kind: blueprint.KindList, ElementType: elem}
		out.ComputedOptionalRequired = presenceFrom(a.List.ComputedOptionalRequired)
		out.MarkdownDescription = describe(nil, a.List.Description, acc)
		out.DeprecationMessage = derefStr(a.List.DeprecationMessage)
		out.Sensitive = derefBool(a.List.Sensitive)
		out.Validators = customCodes(a.List.Validators.CustomValidators())
		out.PlanModifiers = planModifierCodes(a.List.PlanModifiers.CustomPlanModifiers())
		out.Default = customDefaultOnly(a.List.Default.CustomDefault())
		noteCustomType(a.List.CustomType, path, r)

	case a.Set != nil:
		elem, err := elemFrom(a.Set.ElementType, path, r)
		if err != nil {
			return blueprint.Attribute{}, err
		}
		out.Type = blueprint.AttrType{Kind: blueprint.KindSet, ElementType: elem}
		out.ComputedOptionalRequired = presenceFrom(a.Set.ComputedOptionalRequired)
		out.MarkdownDescription = describe(nil, a.Set.Description, acc)
		out.DeprecationMessage = derefStr(a.Set.DeprecationMessage)
		out.Sensitive = derefBool(a.Set.Sensitive)
		out.Validators = customCodes(a.Set.Validators.CustomValidators())
		out.PlanModifiers = planModifierCodes(a.Set.PlanModifiers.CustomPlanModifiers())
		out.Default = customDefaultOnly(a.Set.Default.CustomDefault())
		noteCustomType(a.Set.CustomType, path, r)

	case a.Map != nil:
		elem, err := elemFrom(a.Map.ElementType, path, r)
		if err != nil {
			return blueprint.Attribute{}, err
		}
		out.Type = blueprint.AttrType{Kind: blueprint.KindMap, ElementType: elem}
		out.ComputedOptionalRequired = presenceFrom(a.Map.ComputedOptionalRequired)
		out.MarkdownDescription = describe(nil, a.Map.Description, acc)
		out.DeprecationMessage = derefStr(a.Map.DeprecationMessage)
		out.Sensitive = derefBool(a.Map.Sensitive)
		out.Validators = customCodes(a.Map.Validators.CustomValidators())
		out.PlanModifiers = planModifierCodes(a.Map.PlanModifiers.CustomPlanModifiers())
		out.Default = customDefaultOnly(a.Map.Default.CustomDefault())
		noteCustomType(a.Map.CustomType, path, r)

	case a.ListNested != nil:
		nested, err := nestedFrom(a.Name, a.ListNested.NestedObject, path, r, acc)
		if err != nil {
			return blueprint.Attribute{}, err
		}
		out.Type = blueprint.AttrType{Kind: blueprint.KindListNested, NestedObject: nested}
		out.ComputedOptionalRequired = presenceFrom(a.ListNested.ComputedOptionalRequired)
		out.MarkdownDescription = describe(nil, a.ListNested.Description, acc)
		out.DeprecationMessage = derefStr(a.ListNested.DeprecationMessage)
		out.Sensitive = derefBool(a.ListNested.Sensitive)
		out.Validators = customCodes(a.ListNested.Validators.CustomValidators())
		out.PlanModifiers = planModifierCodes(a.ListNested.PlanModifiers.CustomPlanModifiers())
		out.Default = customDefaultOnly(a.ListNested.Default.CustomDefault())

	case a.SetNested != nil:
		nested, err := nestedFrom(a.Name, a.SetNested.NestedObject, path, r, acc)
		if err != nil {
			return blueprint.Attribute{}, err
		}
		out.Type = blueprint.AttrType{Kind: blueprint.KindSetNested, NestedObject: nested}
		out.ComputedOptionalRequired = presenceFrom(a.SetNested.ComputedOptionalRequired)
		out.MarkdownDescription = describe(nil, a.SetNested.Description, acc)
		out.DeprecationMessage = derefStr(a.SetNested.DeprecationMessage)
		out.Sensitive = derefBool(a.SetNested.Sensitive)
		out.Validators = customCodes(a.SetNested.Validators.CustomValidators())
		out.PlanModifiers = planModifierCodes(a.SetNested.PlanModifiers.CustomPlanModifiers())
		out.Default = customDefaultOnly(a.SetNested.Default.CustomDefault())

	case a.SingleNested != nil:
		// SingleNested carries its attributes directly rather than a NestedObject,
		// so it is rewrapped to share one conversion path.
		obj := resource.NestedAttributeObject{
			Attributes:             a.SingleNested.Attributes,
			AssociatedExternalType: a.SingleNested.AssociatedExternalType,
		}
		nested, err := nestedFrom(a.Name, obj, path, r, acc)
		if err != nil {
			return blueprint.Attribute{}, err
		}
		out.Type = blueprint.AttrType{Kind: blueprint.KindSingleNested, NestedObject: nested}
		out.ComputedOptionalRequired = presenceFrom(a.SingleNested.ComputedOptionalRequired)
		out.MarkdownDescription = describe(nil, a.SingleNested.Description, acc)
		out.DeprecationMessage = derefStr(a.SingleNested.DeprecationMessage)
		out.Sensitive = derefBool(a.SingleNested.Sensitive)
		out.Validators = customCodes(a.SingleNested.Validators.CustomValidators())
		out.PlanModifiers = planModifierCodes(a.SingleNested.PlanModifiers.CustomPlanModifiers())
		out.Default = customDefaultOnly(a.SingleNested.Default.CustomDefault())

	case a.MapNested != nil:
		// The blueprint has no map-nested kind. Converting to a list would change
		// the configuration a practitioner writes, which is not something an import
		// gets to decide.
		return blueprint.Attribute{}, fmt.Errorf(
			"%s: %w: map_nested has no blueprint counterpart",
			path,
			ErrUnrepresentable,
		)

	case a.Object != nil:
		// An object attribute is a single value with typed fields and no per-field
		// presence, documentation or validators. The blueprint's single_nested is
		// close but not equivalent, and silently upgrading would invent a schema the
		// document did not describe.
		return blueprint.Attribute{}, fmt.Errorf(
			"%s: %w: object attributes have no blueprint counterpart; use single_nested",
			path,
			ErrUnrepresentable,
		)

	case a.Dynamic != nil:
		return blueprint.Attribute{}, fmt.Errorf(
			"%s: %w: dynamic has no blueprint counterpart (tfplugingen-framework v0.4.1 never implemented it either)",
			path,
			ErrUnrepresentable,
		)

	default:
		return blueprint.Attribute{}, fmt.Errorf(
			"%s: %w: the attribute declares no type",
			path,
			ErrInvalidSpec,
		)
	}

	return out, nil
}

// importBlock converts a block to its nested-attribute equivalent.
func importBlock(
	b resource.Block,
	parent string,
	r *Report,
	acc *importLosses,
) (blueprint.Attribute, error) {
	path := fmt.Sprintf("%s.blocks[%s]", parent, b.Name)

	// Rebuilt as the equivalent attribute so the whole conversion runs through one
	// path. A block carries the same data; only the configuration syntax differs.
	var a resource.Attribute

	switch {
	case b.ListNested != nil:
		a = resource.Attribute{Name: b.Name, ListNested: &resource.ListNestedAttribute{
			ComputedOptionalRequired: schema.Optional,
			NestedObject: resource.NestedAttributeObject{
				Attributes:             b.ListNested.NestedObject.Attributes,
				AssociatedExternalType: b.ListNested.NestedObject.AssociatedExternalType,
			},
			Description:        b.ListNested.Description,
			DeprecationMessage: b.ListNested.DeprecationMessage,
			Validators:         b.ListNested.Validators,
			PlanModifiers:      b.ListNested.PlanModifiers,
		}}

	case b.SetNested != nil:
		a = resource.Attribute{Name: b.Name, SetNested: &resource.SetNestedAttribute{
			ComputedOptionalRequired: schema.Optional,
			NestedObject: resource.NestedAttributeObject{
				Attributes:             b.SetNested.NestedObject.Attributes,
				AssociatedExternalType: b.SetNested.NestedObject.AssociatedExternalType,
			},
			Description:        b.SetNested.Description,
			DeprecationMessage: b.SetNested.DeprecationMessage,
			Validators:         b.SetNested.Validators,
			PlanModifiers:      b.SetNested.PlanModifiers,
		}}

	case b.SingleNested != nil:
		a = resource.Attribute{Name: b.Name, SingleNested: &resource.SingleNestedAttribute{
			ComputedOptionalRequired: schema.Optional,
			Attributes:               b.SingleNested.Attributes,
			AssociatedExternalType:   b.SingleNested.AssociatedExternalType,
			Description:              b.SingleNested.Description,
			DeprecationMessage:       b.SingleNested.DeprecationMessage,
			Validators:               b.SingleNested.Validators,
			PlanModifiers:            b.SingleNested.PlanModifiers,
		}}

	default:
		return blueprint.Attribute{}, fmt.Errorf(
			"%s: %w: the block declares no type",
			path,
			ErrInvalidSpec,
		)
	}

	// A block has no presence field upstream; nested attributes need one, and
	// optional is the only choice that neither forces the practitioner to write the
	// block nor stops them.
	return importAttribute(a, parent, r, acc)
}

// nestedFrom converts a nested object, deriving the identifiers the blueprint needs
// and the document does not carry.
//
// Deliberately not singularised. The pilot's hand-authored name for the object
// inside "assignments" is TagAssignmentModel; this derivation produces
// AssignmentsModel, and says so in a note for a human to shorten. English
// pluralisation in a code generator is a bug factory -- "statuses", "analyses",
// "children" -- and the note costs one line where a wrong guess costs a rename
// nobody notices is needed.
func nestedFrom(
	attrName string,
	obj resource.NestedAttributeObject,
	path string,
	r *Report,
	acc *importLosses,
) (*blueprint.NestedAttributeObject, error) {
	if len(obj.Attributes) == 0 {
		return nil, fmt.Errorf("%s: %w: a nested object with no attributes", path, ErrInvalidSpec)
	}

	base := namingOpts.GoTypeName(attrName)

	out := &blueprint.NestedAttributeObject{
		GoTypeName:    base + "Model",
		AttrTypesVar:  lowerFirstRune(base) + "AttrTypes",
		ObjectTypeVar: lowerFirstRune(base) + "ObjectType",
		ExpandFunc:    "expand" + base,
		FlattenFunc:   "flatten" + base,
	}

	// associated_external_type is the one upstream field carrying part of the
	// binding, so the SDK struct survives a round trip rather than needing to be
	// retyped.
	if obj.AssociatedExternalType != nil {
		out.SDKType = obj.AssociatedExternalType.Type
	} else {
		r.add(
			SeverityDropped,
			path+".type.nested.sdkType",
			"the document names no external type, so the SDK struct this object maps to must be authored",
		)
	}

	r.add(
		SeverityInfo,
		path+".type.nested",
		"the nested model and helper names were derived from the attribute name and are not singularised",
	)

	for _, child := range obj.Attributes {
		converted, err := importAttribute(child, path+".nested", r, acc)
		if err != nil {
			return nil, err
		}
		out.Attributes = append(out.Attributes, converted)
	}

	return out, nil
}

// lowerFirstRune lowercases the first character, for a package-level variable name.
func lowerFirstRune(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]|0x20) + s[1:]
}

// presenceFrom maps the upstream enum onto blueprint presence.
//
// An unrecognised value is carried through rather than rejected: blueprint.Validate
// will refuse it with a message that names the attribute, which is a better report
// than one from here, and the document was already schema-validated so this is
// unreachable in practice.
func presenceFrom(p schema.ComputedOptionalRequired) blueprint.ComputedOptionalRequired {
	switch p {
	case schema.Required:
		return blueprint.Required
	case schema.Optional:
		return blueprint.Optional
	case schema.Computed:
		return blueprint.Computed
	case schema.ComputedOptional:
		return blueprint.ComputedOptional
	default:
		return blueprint.ComputedOptionalRequired(p)
	}
}

func elemFrom(e schema.ElementType, path string, r *Report) (*blueprint.AttrType, error) {
	switch {
	case e.Bool != nil:
		return &blueprint.AttrType{Kind: blueprint.KindBool}, nil
	case e.String != nil:
		return &blueprint.AttrType{Kind: blueprint.KindString}, nil
	case e.Int64 != nil:
		return &blueprint.AttrType{Kind: blueprint.KindInt64}, nil
	case e.Float64 != nil:
		return &blueprint.AttrType{Kind: blueprint.KindFloat64}, nil
	case e.Number != nil:
		return &blueprint.AttrType{Kind: blueprint.KindNumber}, nil

	case e.List != nil:
		inner, err := elemFrom(e.List.ElementType, path, r)
		if err != nil {
			return nil, err
		}
		return &blueprint.AttrType{Kind: blueprint.KindList, ElementType: inner}, nil
	case e.Set != nil:
		inner, err := elemFrom(e.Set.ElementType, path, r)
		if err != nil {
			return nil, err
		}
		return &blueprint.AttrType{Kind: blueprint.KindSet, ElementType: inner}, nil
	case e.Map != nil:
		inner, err := elemFrom(e.Map.ElementType, path, r)
		if err != nil {
			return nil, err
		}
		return &blueprint.AttrType{Kind: blueprint.KindMap, ElementType: inner}, nil

	case e.Object != nil:
		// An object element type has no blueprint counterpart: the blueprint's
		// nested kinds live on the attribute, not in an element type. Note there is
		// no dynamic case to handle -- schema.ElementType has no Dynamic field, so a
		// collection of dynamic values is not expressible upstream either.
		return nil, fmt.Errorf(
			"%s: %w: an object element type has no counterpart",
			path,
			ErrUnrepresentable,
		)

	default:
		return nil, fmt.Errorf("%s: %w: the element type declares no type", path, ErrInvalidSpec)
	}
}

// noteCustomType reports a custom type, which the blueprint has no field for.
func noteCustomType(ct *schema.CustomType, path string, r *Report) {
	if ct == nil {
		return
	}
	r.add(SeverityDropped, path+".customType",
		"a custom type and value has no blueprint counterpart and was dropped")
}

func customCodes(in schema.CustomValidators) []blueprint.CustomCode {
	out := make([]blueprint.CustomCode, 0, len(in))
	for _, v := range in {
		if v == nil {
			continue
		}
		out = append(out, blueprint.CustomCode{
			SchemaDefinition: v.SchemaDefinition,
			Imports:          blueprintImports(v.Imports),
		})
	}
	return nilIfEmpty(out)
}

func planModifierCodes(in schema.CustomPlanModifiers) []blueprint.CustomCode {
	out := make([]blueprint.CustomCode, 0, len(in))
	for _, m := range in {
		if m == nil {
			continue
		}
		out = append(out, blueprint.CustomCode{
			SchemaDefinition: m.SchemaDefinition,
			Imports:          blueprintImports(m.Imports),
		})
	}
	return nilIfEmpty(out)
}

func blueprintImports(in []code.Import) []blueprint.Import {
	if len(in) == 0 {
		return nil
	}
	out := make([]blueprint.Import, 0, len(in))
	for _, i := range in {
		out = append(out, blueprint.Import{Path: i.Path, Alias: derefStr(i.Alias)})
	}
	return out
}

// The default readers below turn an upstream typed default back into the blueprint's
// Go-literal form. Rendering with %q and strconv is the inverse of the parse in
// prepared.go, which is what makes the round trip byte-stable.

func customDefaultOnly(c *schema.CustomDefault) *blueprint.Default {
	if c == nil {
		return nil
	}
	return &blueprint.Default{Custom: &blueprint.CustomCode{
		SchemaDefinition: c.SchemaDefinition,
		Imports:          blueprintImports(c.Imports),
	}}
}

func boolDefaultFrom(d *schema.BoolDefault) *blueprint.Default {
	if d == nil {
		return nil
	}
	out := customDefaultOnly(d.Custom)
	if d.Static != nil {
		if out == nil {
			out = &blueprint.Default{}
		}
		out.Static = &blueprint.Literal{
			Kind: blueprint.KindBool,
			Raw:  fmt.Sprintf("%t", *d.Static),
		}
	}
	return out
}

func stringDefaultFrom(d *schema.StringDefault) *blueprint.Default {
	if d == nil {
		return nil
	}
	out := customDefaultOnly(d.Custom)
	if d.Static != nil {
		if out == nil {
			out = &blueprint.Default{}
		}
		// %q, so the blueprint carries the Go literal the emitter will write.
		out.Static = &blueprint.Literal{
			Kind: blueprint.KindString,
			Raw:  fmt.Sprintf("%q", *d.Static),
		}
	}
	return out
}

func int64DefaultFrom(d *schema.Int64Default) *blueprint.Default {
	if d == nil {
		return nil
	}
	out := customDefaultOnly(d.Custom)
	if d.Static != nil {
		if out == nil {
			out = &blueprint.Default{}
		}
		out.Static = &blueprint.Literal{
			Kind: blueprint.KindInt64,
			Raw:  fmt.Sprintf("%d", *d.Static),
		}
	}
	return out
}

func float64DefaultFrom(d *schema.Float64Default) *blueprint.Default {
	if d == nil {
		return nil
	}
	out := customDefaultOnly(d.Custom)
	if d.Static != nil {
		if out == nil {
			out = &blueprint.Default{}
		}
		// %v rather than a fixed precision, so 1.5 round-trips as "1.5" rather than
		// "1.500000" and the export stays byte-stable across an import.
		out.Static = &blueprint.Literal{
			Kind: blueprint.KindFloat64,
			Raw:  fmt.Sprintf("%v", *d.Static),
		}
	}
	return out
}
