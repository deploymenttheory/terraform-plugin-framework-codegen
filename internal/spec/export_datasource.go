package spec

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-codegen-spec/datasource"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// datasourceAttribute assembles one upstream data source attribute.
//
// It is the resource assembler minus two fields. The datasource package declares no
// PlanModifiers and no Default on any attribute, because a data source is read-only
// and neither concept applies -- so prepare() was told forResource=false and has
// already reported anything the blueprint carried in those positions as a loss.
// Nothing here needs to check again.
func datasourceAttribute(p prepared) (datasource.Attribute, error) {
	out := datasource.Attribute{Name: p.name}

	switch p.kind {
	case blueprint.KindBool:
		out.Bool = &datasource.BoolAttribute{
			ComputedOptionalRequired: p.presence,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               boolValidators(p.validators),
		}

	case blueprint.KindString:
		out.String = &datasource.StringAttribute{
			ComputedOptionalRequired: p.presence,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               stringValidators(p.validators),
		}

	case blueprint.KindInt64:
		out.Int64 = &datasource.Int64Attribute{
			ComputedOptionalRequired: p.presence,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               int64Validators(p.validators),
		}

	case blueprint.KindFloat64:
		out.Float64 = &datasource.Float64Attribute{
			ComputedOptionalRequired: p.presence,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               float64Validators(p.validators),
		}

	case blueprint.KindNumber:
		out.Number = &datasource.NumberAttribute{
			ComputedOptionalRequired: p.presence,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               numberValidators(p.validators),
		}

	case blueprint.KindList:
		out.List = &datasource.ListAttribute{
			ComputedOptionalRequired: p.presence,
			ElementType:              *p.elem,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               listValidators(p.validators),
		}

	case blueprint.KindSet:
		out.Set = &datasource.SetAttribute{
			ComputedOptionalRequired: p.presence,
			ElementType:              *p.elem,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               setValidators(p.validators),
		}

	case blueprint.KindMap:
		out.Map = &datasource.MapAttribute{
			ComputedOptionalRequired: p.presence,
			ElementType:              *p.elem,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               mapValidators(p.validators),
		}

	case blueprint.KindListNested:
		obj, err := datasourceNestedObject(p.nested)
		if err != nil {
			return datasource.Attribute{}, err
		}
		out.ListNested = &datasource.ListNestedAttribute{
			ComputedOptionalRequired: p.presence,
			NestedObject:             *obj,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               listValidators(p.validators),
		}

	case blueprint.KindSetNested:
		obj, err := datasourceNestedObject(p.nested)
		if err != nil {
			return datasource.Attribute{}, err
		}
		out.SetNested = &datasource.SetNestedAttribute{
			ComputedOptionalRequired: p.presence,
			NestedObject:             *obj,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               setValidators(p.validators),
		}

	case blueprint.KindSingleNested:
		obj, err := datasourceNestedObject(p.nested)
		if err != nil {
			return datasource.Attribute{}, err
		}
		out.SingleNested = &datasource.SingleNestedAttribute{
			ComputedOptionalRequired: p.presence,
			Attributes:               obj.Attributes,
			AssociatedExternalType:   obj.AssociatedExternalType,
			Description:              p.description,
			DeprecationMessage:       p.deprecation,
			Sensitive:                p.sensitive,
			Validators:               objectValidators(p.validators),
		}

	default:
		return datasource.Attribute{}, fmt.Errorf("%w: attribute kind %q", ErrUnrepresentable, p.kind)
	}

	return out, nil
}

func datasourceNestedObject(o *preparedObject) (*datasource.NestedAttributeObject, error) {
	if o == nil {
		return nil, fmt.Errorf("%w: a nested attribute with no object shape", ErrUnrepresentable)
	}

	out := &datasource.NestedAttributeObject{AssociatedExternalType: o.external}

	for _, child := range o.attrs {
		a, err := datasourceAttribute(child)
		if err != nil {
			return nil, err
		}
		out.Attributes = append(out.Attributes, a)
	}

	return out, nil
}
