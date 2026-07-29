package render

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// pkgIdentitySchema is the framework package a resource identity schema comes from.
const pkgIdentitySchema = "github.com/hashicorp/terraform-plugin-framework/resource/identityschema"

// IdentityView is the resource identity schema, rendered.
type IdentityView struct {
	// GoTypeName is the struct both the resource and its list facet decode the identity
	// into, so the two cannot disagree about its shape.
	GoTypeName string
	// Attributes are finished `"name": identityschema.XAttribute{...}` entries.
	Attributes []string
	// Fields are finished struct field declarations for the identity model.
	Fields []string
	// Assignments are finished statements copying values out of the resource model, e.g.
	// `identity.ID = state.ID.ValueString()`.
	Assignments []string
}

// identityView builds the identity schema, model and the copy out of the resource model.
//
// The identity is populated from the resource model rather than fetched again: the values
// are by definition already in state, and a second request to learn what Terraform is
// holding would be a request that can fail for no reason.
// The identity is a parameter rather than something this looks for on the resource: absence
// is the caller's business, and taking it here means the function cannot return a nil view
// with a nil error -- which reads as "no error and no result" and says nothing about which.
func identityView(
	r blueprint.Resource,
	ri blueprint.ResourceIdentity,
	imports *importSet,
) (*IdentityView, error) {
	v := &IdentityView{GoTypeName: ri.GoTypeName}

	imports.add(pkgIdentitySchema, "")

	for _, a := range ri.Attributes {
		schemaType, ok := identitySchemaType[a.Kind]
		if !ok {
			return nil, &ErrUnsupported{
				What: fmt.Sprintf("identity attribute %q of resource %q", a.Name, r.Key),
				Why: fmt.Sprintf(
					"type kind %q has no identityschema counterpart; an identity is scalars, "+
						"or a list of them",
					a.Kind,
				),
			}
		}

		decl, err := identityAttributeDecl(a, schemaType)
		if err != nil {
			return nil, err
		}
		v.Attributes = append(v.Attributes, decl)

		goType, ok := identityGoType[a.Kind]
		if !ok {
			return nil, &ErrUnsupported{
				What: fmt.Sprintf("identity attribute %q of resource %q", a.Name, r.Key),
				Why:  fmt.Sprintf("type kind %q has no Go type for the identity model", a.Kind),
			}
		}
		v.Fields = append(v.Fields, fmt.Sprintf("%s %s `tfsdk:%q`", a.GoField, goType, a.Name))

		assign, err := identityAssignment(r, a)
		if err != nil {
			return nil, err
		}
		v.Assignments = append(v.Assignments, assign)
	}

	return v, nil
}

func identityAttributeDecl(a blueprint.IdentityAttribute, schemaType string) (string, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "%q: identityschema.%s{\n", a.Name, schemaType)

	// One of the two, and validation has already refused neither and both. Written here
	// rather than defaulted, because which one it is decides whether `terraform import`
	// accepts the identity with this member missing.
	if a.RequiredForImport {
		b.WriteString("RequiredForImport: true,\n")
	}
	if a.OptionalForImport {
		b.WriteString("OptionalForImport: true,\n")
	}

	if a.Description != "" {
		fmt.Fprintf(&b, "Description: %s,\n", goStringLit(a.Description))
	}

	b.WriteString("}")

	return b.String(), nil
}

// identityAssignment copies one identity value out of the resource model.
//
// The framework's identity attributes are plain Go scalars rather than framework values, so
// the conversion is off the model field's own accessor -- ValueString on a types.String, and
// so on. That is why the resource attribute's kind decides the accessor rather than the
// identity attribute's.
func identityAssignment(r blueprint.Resource, a blueprint.IdentityAttribute) (string, error) {
	src, ok := findAttribute(r, a.FromAttribute)
	if !ok {
		return "", &ErrUnsupported{
			What: fmt.Sprintf("identity attribute %q of resource %q", a.Name, r.Key),
			Why: fmt.Sprintf(
				"it reads from attribute %q, which this resource does not declare",
				a.FromAttribute,
			),
		}
	}

	accessor, ok := frameworkValueAccessor[src.Type.Kind]
	if !ok {
		return "", &ErrUnsupported{
			What: fmt.Sprintf("identity attribute %q of resource %q", a.Name, r.Key),
			Why: fmt.Sprintf(
				"it reads from a %q attribute, which has no scalar accessor for an identity",
				src.Type.Kind,
			),
		}
	}

	return fmt.Sprintf("identity.%s = state.%s.%s()", a.GoField, src.GoField, accessor), nil
}

// identitySchemaType maps a kind to its identityschema attribute type.
//
// Narrower than frameworkSchemaType on purpose: identityschema has no nested attribute and
// no map, so the absence of an entry is the refusal.
var identitySchemaType = map[blueprint.TypeKind]string{
	blueprint.KindBool:    "BoolAttribute",
	blueprint.KindString:  "StringAttribute",
	blueprint.KindInt32:   "Int32Attribute",
	blueprint.KindInt64:   "Int64Attribute",
	blueprint.KindFloat32: "Float32Attribute",
	blueprint.KindFloat64: "Float64Attribute",
	blueprint.KindNumber:  "NumberAttribute",
	blueprint.KindList:    "ListAttribute",
}

// identityGoType maps a kind to the Go type the identity model holds.
//
// Plain Go scalars, not framework values: the framework decodes an identity into ordinary
// types, which is also why generated code can copy into it without a conversion helper.
var identityGoType = map[blueprint.TypeKind]string{
	blueprint.KindBool:    "bool",
	blueprint.KindString:  "string",
	blueprint.KindInt32:   "int32",
	blueprint.KindInt64:   "int64",
	blueprint.KindFloat32: "float32",
	blueprint.KindFloat64: "float64",
	blueprint.KindNumber:  "float64",
	blueprint.KindList:    "[]string",
}

// frameworkValueAccessor is how a framework value in the resource model yields a Go scalar.
var frameworkValueAccessor = map[blueprint.TypeKind]string{
	blueprint.KindBool:    "ValueBool",
	blueprint.KindString:  "ValueString",
	blueprint.KindInt32:   "ValueInt32",
	blueprint.KindInt64:   "ValueInt64",
	blueprint.KindFloat32: "ValueFloat32",
	blueprint.KindFloat64: "ValueFloat64",
}
