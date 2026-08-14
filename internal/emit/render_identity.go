// The identity terraform stores beside a resource's state to name the remote
// object it stands for, and which a list resource's results are expressed in.

package emit

import (
	"fmt"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// identityAttribute is one attribute of a resource's identity.
type identityAttribute struct {
	// Name is the terraform attribute name, which the list resource reads
	// its value from the configuration or the element by.
	Name string
	// SchemaType is the identityschema attribute type, e.g. "StringAttribute".
	SchemaType string
	// Kind is the attribute's own kind, so a reader knows what the value is
	// before it is spelled as a string.
	Kind ir.AttributeType
}

// identitySchemaTypes is the identityschema spelling of each kind an identity
// may hold. The framework admits primitives and lists of primitives only, and
// an identity attribute is addressing or an id, so nothing else arises.
var identitySchemaTypes = map[ir.AttributeType]string{
	ir.TypeString:  "StringAttribute",
	ir.TypeInt64:   "Int64Attribute",
	ir.TypeFloat64: "Float64Attribute",
	ir.TypeBool:    "BoolAttribute",
}

// resourceIdentity is the identity of one resource: its addressing
// attributes, then its id, in that order.
//
// The framework requires an identity to name at most one remote object per
// provider. An id alone does not where a parent scopes it — two repositories'
// hooks may share one — so the path parameters that locate the object are part
// of it. They are already root attributes of the schema, in path order ahead
// of the id, so this reads them off in the order they are declared.
//
// Empty when the resource carries no id, which is the one attribute an
// identity cannot do without.
func resourceIdentity(r *ir.Resource) []identityAttribute {
	if r.Schema == nil {
		return nil
	}
	addressing := addressingNames(r.Operations.Read, r.Operations.Create, r.Operations.Delete)

	var out []identityAttribute
	var carriesID bool
	for _, attribute := range r.Schema.Attributes {
		if attribute.Nested != nil || attribute.Unsupported {
			continue
		}
		if attribute.Name != idAttributeName && !addressing[attribute.Name] {
			continue
		}
		schemaType, ok := identitySchemaTypes[attribute.Kind]
		if !ok {
			continue
		}
		kind := attribute.Kind
		if attribute.Name == idAttributeName {
			carriesID = true
			// The id is a string in the identity whatever the API keys its
			// objects with. An identity names an object and is compared for
			// equality; it is not the state value, and the list resource
			// reaches its id through an accessor that already renders every
			// scalar as a string.
			kind, schemaType = ir.TypeString, identitySchemaTypes[ir.TypeString]
		}
		out = append(out, identityAttribute{
			Name:       attribute.Name,
			SchemaType: schemaType,
			Kind:       kind,
		})
	}
	if !carriesID {
		return nil
	}
	return out
}

// identitySchemaDecls renders the identity schema's attribute declarations,
// ready to sit inside a map[string]identityschema.Attribute literal.
//
// Every attribute is required for import: an identity names one object, and a
// partial identity names none.
func identitySchemaDecls(identity []identityAttribute, depth int) string {
	indent := strings.Repeat("\t", depth)
	var b strings.Builder
	for _, attribute := range identity {
		fmt.Fprintf(&b, "%s%q: identityschema.%s{\n%s\tRequiredForImport: true,\n%s},\n",
			indent, attribute.Name, attribute.SchemaType, indent, indent)
	}
	return b.String()
}

// identityModelFields renders the struct fields the identity decodes into.
func identityModelFields(identity []identityAttribute) string {
	var b strings.Builder
	for _, attribute := range identity {
		fmt.Fprintf(&b, "\t%s %s `tfsdk:%q`\n",
			ir.GoName(attribute.Name), identityValueType(attribute.Kind), attribute.Name)
	}
	return b.String()
}

// identityValueType is the framework value type one identity attribute is
// held as.
func identityValueType(kind ir.AttributeType) string {
	return scalarSchemaType(kind).ValueType
}
