package openapi

import (
	"regexp"
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

	// Constraints are the bounds the schema declares, and BadPattern is set when it declares
	// a regular expression Go's regexp cannot compile.
	//
	// Refused rather than passed on: the generated code compiles the pattern with
	// regexp.MustCompile, so an expression Go rejects is a panic when the provider starts.
	// JSON Schema permits ECMA constructs -- lookahead in particular -- that RE2 does not.
	Constraints blueprint.Constraints
	BadPattern  string

	// ElemConstraints are the bounds declared on a scalar collection's *item* schema, which
	// are not the collection's own: maxLength on the items bounds each string, and becomes a
	// per-element validator rather than one applied to the whole collection.
	ElemConstraints blueprint.Constraints

	// Object holds a nested object's own fields, resolved recursively, so a shape
	// several levels down arrives whole rather than as a name to look up later.
	Object []Field
	// SelfReferential marks a nested object that reaches itself. Its depth is decided by
	// the data rather than by the schema, so it is not expressible as a fixed set of
	// generated types -- inference reports it by name instead of recursing forever.
	SelfReferential bool
}

// IsEnum reports whether the field resolved to a named enumeration.
func (f Field) IsEnum() bool { return f.EnumTypeName != "" }

// CyclicSchema returns the name of the schema that re-enters itself, anywhere beneath this
// field, or empty.
//
// It looks all the way down rather than only at this field, and the caller refuses the
// whole attribute on the strength of it. Refusing only the cycle point would leave the
// enclosing object in place minus its recursive dimension -- a tree attribute offering a
// label and no children, which looks usable and cannot express the shape it is named for.
// That is the failure mode this package exists to avoid.
func (f Field) CyclicSchema() string {
	if f.SelfReferential {
		return f.ObjectTypeName
	}
	for _, c := range f.Object {
		if n := c.CyclicSchema(); n != "" {
			return n
		}
	}
	return ""
}

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
		if strings.Contains(pair.Key(), "json") && pair.Value() != nil &&
			pair.Value().Schema != nil {
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
		if strings.Contains(pair.Key(), "json") && pair.Value() != nil &&
			pair.Value().Schema != nil {
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
	return fieldsWithin(s, nil)
}

// fieldsWithin is Fields, carrying the chain of object schema names already entered.
//
// The chain is what stops a self-referential schema recursing forever. It is a path
// rather than a set of everything seen: the same object appearing twice in different
// branches is ordinary reuse, and only re-entering one still on the current path is a
// cycle.
func fieldsWithin(s *base.Schema, path []string) []Field {
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
		out = append(out, fieldsWithin(m, path)...)
	}

	if s.Properties != nil {
		for pair := s.Properties.First(); pair != nil; pair = pair.Next() {
			f, ok := fieldOf(pair.Key(), pair.Value(), path)
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
func fieldOf(name string, proxy *base.SchemaProxy, path []string) (Field, bool) {
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

	if kind.IsNested() {
		f.Object, f.SelfReferential = nestedFields(s, f.ObjectTypeName, path)
	}

	f.Constraints, f.BadPattern = constraintsOf(s, kind)

	if kind.IsCollection() && f.ElemKind != "" {
		if item := itemSchema(s); item != nil {
			// A bad pattern on an element is reported against the attribute, so the second
			// return is folded in rather than tracked separately -- there is one note either
			// way and it names the same field.
			ec, bad := constraintsOf(item, f.ElemKind)
			f.ElemConstraints = ec

			if bad != "" && f.BadPattern == "" {
				f.BadPattern = bad
			}
		}
	}

	return f, true
}

// constraintsOf reads the bounds a schema declares.
//
// Only those that belong to the kind: a maxLength on an array is something the document says
// and the framework has no validator for, so carrying it into the IR would only give
// blueprint.Validate something to refuse. What a document declares in the wrong place is not
// worth propagating.
func constraintsOf(s *base.Schema, kind blueprint.TypeKind) (blueprint.Constraints, string) {
	var (
		c   blueprint.Constraints
		bad string
	)

	switch {
	case kind == blueprint.KindString:
		c.MinLength = s.MinLength
		c.MaxLength = s.MaxLength
		// The declared format travels so value synthesis can honour it -- a
		// date-time field given "tfacc-start-date" is refused before anything else
		// in the body is even read.
		c.Format = s.Format

		if s.Pattern != "" {
			// Compiled here, not merely copied: the generated code will call
			// regexp.MustCompile on it, and a pattern RE2 cannot parse would panic at
			// provider start rather than fail here where it can be reported.
			if _, err := regexp.Compile(s.Pattern); err != nil {
				bad = s.Pattern
			} else {
				c.Pattern = s.Pattern
			}
		}

	case kind.IsCollection(), kind.IsNestedCollection():
		c.MinItems = s.MinItems
		c.MaxItems = s.MaxItems

	case kind == blueprint.KindInt32, kind == blueprint.KindInt64,
		kind == blueprint.KindFloat32, kind == blueprint.KindFloat64:
		c.Minimum = s.Minimum
		c.Maximum = s.Maximum
	}

	return c, bad
}

// nestedFields resolves a nested object's own fields, one level of recursion at a time.
//
// The object schema for a collection is its item schema; for a single nested object it is
// the schema itself. Re-entering a name already on the path is a cycle, reported rather
// than followed.
func nestedFields(s *base.Schema, typeName string, path []string) (fields []Field, cyclic bool) {
	obj := s
	if primaryType(s) == "array" {
		obj = itemSchema(s)
	}
	if obj == nil {
		return nil, false
	}

	// An inline object has no name to key the cycle check on. That is not a gap: without
	// a $ref it cannot refer back to anything, so it cannot be part of a cycle.
	if typeName != "" {
		for _, entered := range path {
			if entered == typeName {
				return nil, true
			}
		}
		path = append(append([]string(nil), path...), typeName)
	}

	return fieldsWithin(obj, path), false
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
