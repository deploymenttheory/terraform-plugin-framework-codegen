package openapi

import (
	"sort"
	"strings"

	"github.com/pb33f/libopenapi/datamodel/high/base"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// Field is one property of a schema, flattened out of the OpenAPI model.
//
// It exists so inference works against a small, explicit shape rather than
// against libopenapi's tree, which makes the mapping rules readable and testable
// without constructing documents.
type Field struct {
	// Name is the JSON property name, exactly as the API spells it.
	Name string
	// Kind is the framework type it maps to.
	Kind blueprint.TypeKind
	// ElemKind is the element type of a scalar collection.
	ElemKind blueprint.TypeKind
	// Description is the property's documentation, first sentence onward.
	Description string

	// ReadOnly is the specification's readOnly marker, which is the strongest
	// signal available that a field is computed rather than configurable.
	ReadOnly bool
	// Required is whether the enclosing schema lists it as required.
	Required bool
	// Nullable is the specification's nullable marker.
	Nullable bool
	// Deprecated marks a field the API documents as going away.
	Deprecated bool

	// EnumTypeName is the schema name a $ref pointed at, when that schema is an
	// enumeration. Generated SDKs turn those into named string types held by
	// value, which changes how the field is converted.
	EnumTypeName string
	// ObjectTypeName is the schema name of a nested object, or of an array's item
	// schema.
	ObjectTypeName string
	// EnumValues are the documented members, used for documentation only.
	EnumValues []string
}

// IsEnum reports whether the field resolved to a named enumeration.
func (f Field) IsEnum() bool { return f.EnumTypeName != "" }

// jsonContentTypes are the media types a JSON body may be declared as.
//
// The ThousandEyes API serves application/hal+json rather than
// application/json, and assuming the latter finds no schema at all -- silently,
// producing a resource with no attributes.
func jsonContentTypes() []string {
	return []string{"application/json", "application/hal+json", "application/problem+json"}
}

// bodySchema returns the JSON schema of a request body, if it has one.
func bodySchema(rb *v3.RequestBody) *base.Schema {
	if rb == nil || rb.Content == nil {
		return nil
	}
	return schemaFromContent(rb.Content)
}

// responseSchema returns the JSON schema of an operation's first success
// response.
func responseSchema(op *v3.Operation) *base.Schema {
	if op == nil || op.Responses == nil || op.Responses.Codes == nil {
		return nil
	}

	// 2xx codes in ascending order, so 200 wins over 201 when both are declared
	// and the choice is at least deterministic.
	var codes []string
	for pair := op.Responses.Codes.First(); pair != nil; pair = pair.Next() {
		if strings.HasPrefix(pair.Key(), "2") {
			codes = append(codes, pair.Key())
		}
	}
	sort.Strings(codes)

	for _, code := range codes {
		resp, ok := op.Responses.Codes.Get(code)
		if !ok || resp == nil || resp.Content == nil {
			continue
		}
		if s := schemaFromContent(resp.Content); s != nil {
			return s
		}
	}

	return nil
}

// schemaFromContent picks the JSON schema out of a media-type map.
func schemaFromContent(content *orderedmap.Map[string, *v3.MediaType]) *base.Schema {
	for _, ct := range jsonContentTypes() {
		if mt, ok := content.Get(ct); ok && mt != nil && mt.Schema != nil {
			return resolve(mt.Schema)
		}
	}

	// Fall back to any media type whose name looks like JSON, so an API using a
	// vendor content type is not silently skipped.
	for pair := content.First(); pair != nil; pair = pair.Next() {
		if strings.Contains(pair.Key(), "json") && pair.Value() != nil && pair.Value().Schema != nil {
			return resolve(pair.Value().Schema)
		}
	}

	return nil
}

// proxyFromContent picks the JSON schema proxy out of a media-type map.
//
// Distinct from schemaFromContent because resolving a proxy discards the name it
// referenced, and the name is what tells inference which SDK model to bind to.
func proxyFromContent(content *orderedmap.Map[string, *v3.MediaType]) *base.SchemaProxy {
	for _, ct := range jsonContentTypes() {
		if mt, ok := content.Get(ct); ok && mt != nil && mt.Schema != nil {
			return mt.Schema
		}
	}
	for pair := content.First(); pair != nil; pair = pair.Next() {
		if strings.Contains(pair.Key(), "json") && pair.Value() != nil && pair.Value().Schema != nil {
			return pair.Value().Schema
		}
	}
	return nil
}

// resolve follows a schema proxy to the schema it points at.
func resolve(p *base.SchemaProxy) *base.Schema {
	if p == nil {
		return nil
	}
	return p.Schema()
}

// Fields flattens a schema's properties into the shape inference works against.
//
// Composition is resolved one level: allOf members are merged in, which is how
// specifications express "this, plus the common envelope". Deeper composition and
// oneOf are not merged, because picking one branch of a genuine union silently
// produces a resource that can express only part of the API -- inference reports
// what it skipped instead.
func Fields(s *base.Schema) []Field {
	if s == nil {
		return nil
	}

	required := map[string]bool{}
	for _, r := range s.Required {
		required[r] = true
	}

	var out []Field

	// allOf first, so a property the schema restates overrides the composed one.
	for _, member := range s.AllOf {
		m := resolve(member)
		if m == nil {
			continue
		}
		for _, f := range Fields(m) {
			out = append(out, f)
		}
	}

	if s.Properties != nil {
		for pair := s.Properties.First(); pair != nil; pair = pair.Next() {
			f, ok := fieldOf(pair.Key(), pair.Value())
			if !ok {
				continue
			}
			f.Required = required[pair.Key()]
			out = replaceOrAppend(out, f)
		}
	}

	// Sorted by name so inference does not depend on the document's ordering.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

func replaceOrAppend(in []Field, f Field) []Field {
	for i := range in {
		if in[i].Name == f.Name {
			in[i] = f
			return in
		}
	}
	return append(in, f)
}

// fieldOf maps one property schema onto a Field.
//
// A shape with no framework mapping comes back with an empty Kind rather than
// being dropped here. That matters: dropping it at this level would make it
// invisible to the caller, and inference's promise is that everything it cannot
// express is reported by name. One place decides what to skip, and that place
// reports it.
//
// It returns false only when there is no schema at all to look at.
func fieldOf(name string, proxy *base.SchemaProxy) (Field, bool) {
	s := resolve(proxy)
	if s == nil {
		return Field{}, false
	}

	f := Field{
		Name:        name,
		Description: strings.TrimSpace(s.Description),
		ReadOnly:    boolValue(s.ReadOnly),
		Nullable:    boolValue(s.Nullable),
		Deprecated:  boolValue(s.Deprecated),
	}

	// A $ref to a schema that is an enumeration becomes a named string type in a
	// generated SDK, held by value rather than by pointer.
	if refName := refTypeName(proxy); refName != "" {
		if len(s.Enum) > 0 {
			f.Kind = blueprint.KindString
			f.EnumTypeName = refName
			f.EnumValues = enumValues(s)
			return f, true
		}
		f.ObjectTypeName = refName
	}

	// An unmapped shape keeps its empty Kind and travels on, to be reported.
	kind, elem, _ := kindOf(s)
	f.Kind = kind
	f.ElemKind = elem

	if kind.IsNested() && f.ObjectTypeName == "" {
		f.ObjectTypeName = itemTypeName(s)
	}

	return f, true
}

// kindOf maps an OpenAPI type onto a framework type.
//
// The mapping follows the rules HashiCorp's own OpenAPI generator documents, so
// that a blueprint inferred here and a specification produced by their tool agree
// about what a given schema means.
func kindOf(s *base.Schema) (kind, elem blueprint.TypeKind, ok bool) {
	switch primaryType(s) {
	case "boolean":
		return blueprint.KindBool, "", true

	case "integer":
		return blueprint.KindInt64, "", true

	case "number":
		// float64, including when no format is declared.
		//
		// types.Number is arbitrary-precision and would be the more careful
		// reading, but generated SDKs in the resty dialect represent every JSON
		// number as *float64. Inferring types.Number against a *float64 field
		// pairs a Number model field with a Float64 conversion, which does not
		// compile -- the mapping has to agree with the SDK, not only with the
		// specification. A genuinely arbitrary-precision field needs an override.
		return blueprint.KindFloat64, "", true

	case "string":
		return blueprint.KindString, "", true

	case "array":
		item := itemSchema(s)
		if item == nil {
			return "", "", false
		}
		if primaryType(item) == "object" || (item.Properties != nil && item.Properties.Len() > 0) {
			// A set rather than a list: an API that does not promise ordering
			// makes a list report a diff every time it comes back reordered.
			return blueprint.KindSetNested, "", true
		}
		elemKind, _, ok := kindOf(item)
		if !ok {
			return "", "", false
		}
		return blueprint.KindSet, elemKind, true

	case "object":
		// A free-form object with only additionalProperties is a map.
		if s.Properties == nil || s.Properties.Len() == 0 {
			if s.AdditionalProperties != nil {
				return blueprint.KindMap, blueprint.KindString, true
			}
			return "", "", false
		}
		return blueprint.KindSingleNested, "", true

	default:
		return "", "", false
	}
}

// primaryType returns a schema's type, ignoring a null member.
//
// OpenAPI 3.1 expresses nullability as a type union, and treating ["string",
// "null"] as an unmappable multi-type would drop every nullable field.
func primaryType(s *base.Schema) string {
	for _, t := range s.Type {
		if t != "null" && t != "" {
			return t
		}
	}
	return ""
}

func itemSchema(s *base.Schema) *base.Schema {
	if s.Items == nil || !s.Items.IsA() {
		return nil
	}
	return resolve(s.Items.A)
}

// refTypeName returns the schema name a proxy references, or empty when it is
// inline.
func refTypeName(p *base.SchemaProxy) string {
	if p == nil || !p.IsReference() {
		return ""
	}
	ref := p.GetReference()
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

func itemTypeName(s *base.Schema) string {
	if s.Items == nil || !s.Items.IsA() {
		return ""
	}
	return refTypeName(s.Items.A)
}

func enumValues(s *base.Schema) []string {
	out := make([]string, 0, len(s.Enum))
	for _, e := range s.Enum {
		if e == nil {
			continue
		}
		out = append(out, strings.Trim(e.Value, `"`))
	}
	return out
}

func boolValue(p *bool) bool { return p != nil && *p }
