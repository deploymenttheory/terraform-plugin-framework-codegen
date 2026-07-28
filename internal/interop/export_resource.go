package interop

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-codegen-spec/resource"
	"github.com/hashicorp/terraform-plugin-codegen-spec/schema"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// resourceAttribute assembles one upstream resource attribute from a prepared
// value.
//
// This file is deliberately mechanical. Every decision -- coarsening, presence,
// default parsing, recursion -- already happened in prepared.go, so what remains is
// choosing which of the fat struct's pointer fields to set and wrapping the shared
// schema values in the kind-specific named slice types upstream declares. The
// verbosity is the feature: a reviewer checking that a set-nested attribute carries
// its validators can read one case and be sure.
//
// The kind-specific wrapping cannot be factored out with generics. schema.StringValidators
// and schema.SetValidators are distinct named slice types over distinct element
// structs, and expressing "any of these, all of which have a Custom field" needs a
// constraint Go does not have. Adapter interfaces would cost more code than the
// repetition does, and would hide the mapping a reviewer specifically needs to see.
func resourceAttribute(p prepared) (resource.Attribute, error) {
	out := resource.Attribute{Name: p.name}

	switch p.kind {
	case blueprint.KindBool:
		out.Bool = &resource.BoolAttribute{
			ComputedOptionalRequired: p.presence,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               boolValidators(p.validators),
			PlanModifiers:            boolPlanModifiers(p.modifiers),
			Default:                  boolDefault(p),
		}

	case blueprint.KindString:
		out.String = &resource.StringAttribute{
			ComputedOptionalRequired: p.presence,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               stringValidators(p.validators),
			PlanModifiers:            stringPlanModifiers(p.modifiers),
			Default:                  stringDefault(p),
		}

	case blueprint.KindInt64:
		out.Int64 = &resource.Int64Attribute{
			ComputedOptionalRequired: p.presence,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               int64Validators(p.validators),
			PlanModifiers:            int64PlanModifiers(p.modifiers),
			Default:                  int64Default(p),
		}

	case blueprint.KindFloat64:
		out.Float64 = &resource.Float64Attribute{
			ComputedOptionalRequired: p.presence,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               float64Validators(p.validators),
			PlanModifiers:            float64PlanModifiers(p.modifiers),
			Default:                  float64Default(p),
		}

	case blueprint.KindNumber:
		out.Number = &resource.NumberAttribute{
			ComputedOptionalRequired: p.presence,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               numberValidators(p.validators),
			PlanModifiers:            numberPlanModifiers(p.modifiers),
			Default: customOnly(p.defCustom, func(c *schema.CustomDefault) *schema.NumberDefault {
				return &schema.NumberDefault{Custom: c}
			}),
		}

	case blueprint.KindList:
		out.List = &resource.ListAttribute{
			ComputedOptionalRequired: p.presence,
			ElementType:              *p.elem,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               listValidators(p.validators),
			PlanModifiers:            listPlanModifiers(p.modifiers),
			Default: customOnly(p.defCustom, func(c *schema.CustomDefault) *schema.ListDefault {
				return &schema.ListDefault{Custom: c}
			}),
		}

	case blueprint.KindSet:
		out.Set = &resource.SetAttribute{
			ComputedOptionalRequired: p.presence,
			ElementType:              *p.elem,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               setValidators(p.validators),
			PlanModifiers:            setPlanModifiers(p.modifiers),
			Default: customOnly(p.defCustom, func(c *schema.CustomDefault) *schema.SetDefault {
				return &schema.SetDefault{Custom: c}
			}),
		}

	case blueprint.KindMap:
		out.Map = &resource.MapAttribute{
			ComputedOptionalRequired: p.presence,
			ElementType:              *p.elem,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               mapValidators(p.validators),
			PlanModifiers:            mapPlanModifiers(p.modifiers),
			Default: customOnly(p.defCustom, func(c *schema.CustomDefault) *schema.MapDefault {
				return &schema.MapDefault{Custom: c}
			}),
		}

	case blueprint.KindListNested:
		obj, err := resourceNestedObject(p.nested)
		if err != nil {
			return resource.Attribute{}, err
		}
		out.ListNested = &resource.ListNestedAttribute{
			ComputedOptionalRequired: p.presence,
			NestedObject:             *obj,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               listValidators(p.validators),
			PlanModifiers:            listPlanModifiers(p.modifiers),
			Default: customOnly(p.defCustom, func(c *schema.CustomDefault) *schema.ListDefault {
				return &schema.ListDefault{Custom: c}
			}),
		}

	case blueprint.KindSetNested:
		obj, err := resourceNestedObject(p.nested)
		if err != nil {
			return resource.Attribute{}, err
		}
		out.SetNested = &resource.SetNestedAttribute{
			ComputedOptionalRequired: p.presence,
			NestedObject:             *obj,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               setValidators(p.validators),
			PlanModifiers:            setPlanModifiers(p.modifiers),
			Default: customOnly(p.defCustom, func(c *schema.CustomDefault) *schema.SetDefault {
				return &schema.SetDefault{Custom: c}
			}),
		}

	case blueprint.KindSingleNested:
		obj, err := resourceNestedObject(p.nested)
		if err != nil {
			return resource.Attribute{}, err
		}
		out.SingleNested = &resource.SingleNestedAttribute{
			ComputedOptionalRequired: p.presence,
			Attributes:               obj.Attributes,
			AssociatedExternalType:   obj.AssociatedExternalType,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               objectValidators(p.validators),
			PlanModifiers:            objectPlanModifiers(p.modifiers),
			Default: customOnly(p.defCustom, func(c *schema.CustomDefault) *schema.ObjectDefault {
				return &schema.ObjectDefault{Custom: c}
			}),
		}

	default:
		return resource.Attribute{}, fmt.Errorf("%w: attribute kind %q", ErrUnrepresentable, p.kind)
	}

	return out, nil
}

// resourceNestedObject assembles the object a nested attribute holds.
//
// SingleNestedAttribute is the odd one out upstream: it carries Attributes and
// AssociatedExternalType directly rather than a NestedObject, so its caller reads
// the fields off the value this returns instead of assigning it whole.
func resourceNestedObject(o *preparedObject) (*resource.NestedAttributeObject, error) {
	if o == nil {
		return nil, fmt.Errorf("%w: a nested attribute with no object shape", ErrUnrepresentable)
	}

	out := &resource.NestedAttributeObject{AssociatedExternalType: o.external}

	for _, child := range o.attrs {
		a, err := resourceAttribute(child)
		if err != nil {
			return nil, err
		}
		out.Attributes = append(out.Attributes, a)
	}

	return out, nil
}

// customOnly builds a default for a kind whose upstream default type carries only
// a custom definition, returning nil when there is nothing to carry.
//
// Number, the collections and the nested kinds have no Static field, which is why
// prepared.go refuses a static default on them rather than arriving here with one.
func customOnly[T any](c *schema.CustomDefault, build func(*schema.CustomDefault) *T) *T {
	if c == nil {
		return nil
	}
	return build(c)
}

// The wrappers below turn the shared []schema.CustomValidator and
// []schema.CustomPlanModifier into the kind-specific named slice types upstream
// declares. Each is one line of real work; they are grouped here rather than
// inlined so the switch above stays readable as a mapping table.

func boolValidators(in []schema.CustomValidator) schema.BoolValidators {
	out := make(schema.BoolValidators, 0, len(in))
	for i := range in {
		out = append(out, schema.BoolValidator{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func stringValidators(in []schema.CustomValidator) schema.StringValidators {
	out := make(schema.StringValidators, 0, len(in))
	for i := range in {
		out = append(out, schema.StringValidator{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func int64Validators(in []schema.CustomValidator) schema.Int64Validators {
	out := make(schema.Int64Validators, 0, len(in))
	for i := range in {
		out = append(out, schema.Int64Validator{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func float64Validators(in []schema.CustomValidator) schema.Float64Validators {
	out := make(schema.Float64Validators, 0, len(in))
	for i := range in {
		out = append(out, schema.Float64Validator{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func numberValidators(in []schema.CustomValidator) schema.NumberValidators {
	out := make(schema.NumberValidators, 0, len(in))
	for i := range in {
		out = append(out, schema.NumberValidator{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func listValidators(in []schema.CustomValidator) schema.ListValidators {
	out := make(schema.ListValidators, 0, len(in))
	for i := range in {
		out = append(out, schema.ListValidator{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func setValidators(in []schema.CustomValidator) schema.SetValidators {
	out := make(schema.SetValidators, 0, len(in))
	for i := range in {
		out = append(out, schema.SetValidator{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func mapValidators(in []schema.CustomValidator) schema.MapValidators {
	out := make(schema.MapValidators, 0, len(in))
	for i := range in {
		out = append(out, schema.MapValidator{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func objectValidators(in []schema.CustomValidator) schema.ObjectValidators {
	out := make(schema.ObjectValidators, 0, len(in))
	for i := range in {
		out = append(out, schema.ObjectValidator{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func boolPlanModifiers(in []schema.CustomPlanModifier) schema.BoolPlanModifiers {
	out := make(schema.BoolPlanModifiers, 0, len(in))
	for i := range in {
		out = append(out, schema.BoolPlanModifier{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func stringPlanModifiers(in []schema.CustomPlanModifier) schema.StringPlanModifiers {
	out := make(schema.StringPlanModifiers, 0, len(in))
	for i := range in {
		out = append(out, schema.StringPlanModifier{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func int64PlanModifiers(in []schema.CustomPlanModifier) schema.Int64PlanModifiers {
	out := make(schema.Int64PlanModifiers, 0, len(in))
	for i := range in {
		out = append(out, schema.Int64PlanModifier{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func float64PlanModifiers(in []schema.CustomPlanModifier) schema.Float64PlanModifiers {
	out := make(schema.Float64PlanModifiers, 0, len(in))
	for i := range in {
		out = append(out, schema.Float64PlanModifier{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func numberPlanModifiers(in []schema.CustomPlanModifier) schema.NumberPlanModifiers {
	out := make(schema.NumberPlanModifiers, 0, len(in))
	for i := range in {
		out = append(out, schema.NumberPlanModifier{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func listPlanModifiers(in []schema.CustomPlanModifier) schema.ListPlanModifiers {
	out := make(schema.ListPlanModifiers, 0, len(in))
	for i := range in {
		out = append(out, schema.ListPlanModifier{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func setPlanModifiers(in []schema.CustomPlanModifier) schema.SetPlanModifiers {
	out := make(schema.SetPlanModifiers, 0, len(in))
	for i := range in {
		out = append(out, schema.SetPlanModifier{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func mapPlanModifiers(in []schema.CustomPlanModifier) schema.MapPlanModifiers {
	out := make(schema.MapPlanModifiers, 0, len(in))
	for i := range in {
		out = append(out, schema.MapPlanModifier{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

func objectPlanModifiers(in []schema.CustomPlanModifier) schema.ObjectPlanModifiers {
	out := make(schema.ObjectPlanModifiers, 0, len(in))
	for i := range in {
		out = append(out, schema.ObjectPlanModifier{Custom: &in[i]})
	}
	return nilIfEmpty(out)
}

// nilIfEmpty keeps an empty slice out of the JSON.
//
// make(T, 0, 0) is non-nil, and a non-nil empty slice with omitempty still encodes
// as absent -- but only for slices, and relying on that is the kind of detail that
// stops being true when a field's type changes. Returning nil is explicit.
func nilIfEmpty[S ~[]E, E any](s S) S {
	if len(s) == 0 {
		return nil
	}
	return s
}

func boolDefault(p prepared) *schema.BoolDefault {
	if p.defCustom == nil && (p.defStatic == nil || p.defStatic.boolVal == nil) {
		return nil
	}
	d := &schema.BoolDefault{Custom: p.defCustom}
	if p.defStatic != nil {
		d.Static = p.defStatic.boolVal
	}
	return d
}

func stringDefault(p prepared) *schema.StringDefault {
	if p.defCustom == nil && (p.defStatic == nil || p.defStatic.strVal == nil) {
		return nil
	}
	d := &schema.StringDefault{Custom: p.defCustom}
	if p.defStatic != nil {
		d.Static = p.defStatic.strVal
	}
	return d
}

func int64Default(p prepared) *schema.Int64Default {
	if p.defCustom == nil && (p.defStatic == nil || p.defStatic.intVal == nil) {
		return nil
	}
	d := &schema.Int64Default{Custom: p.defCustom}
	if p.defStatic != nil {
		d.Static = p.defStatic.intVal
	}
	return d
}

func float64Default(p prepared) *schema.Float64Default {
	if p.defCustom == nil && (p.defStatic == nil || p.defStatic.floatVal == nil) {
		return nil
	}
	d := &schema.Float64Default{Custom: p.defCustom}
	if p.defStatic != nil {
		d.Static = p.defStatic.floatVal
	}
	return d
}
