// Package specmodel is the typed reader for the revised OpenAPI document.
//
// The revised spec (spec/revised.yaml) is the single source of truth for
// everything downstream: the audit planner reads it to decide what to
// exercise, and IR derivation reads it to decide what to generate. Both need
// the same three things — the document loaded into queryable Go values, the
// x-tfpfgen-* extensions surfaced through typed accessors instead of raw
// interface soup, and the document's operations grouped into entities and
// classified. This package owns all three so the two consumers cannot
// disagree about what the document says.
//
// It is deliberately not a general OpenAPI library. It reads the subset the
// toolkit consumes, resolves references local to the document, and refuses
// everything else loudly: a remote reference, an unknown x-tfpfgen-* key, a
// dangling pointer. The revised document is committed and machine-produced,
// so anything surprising in it is a defect to surface, not an input to
// tolerate.
package specmodel

import "sort"

// Document is a loaded OpenAPI 3.x document.
type Document struct {
	// OpenAPI is the declared specification version, e.g. "3.0.3".
	OpenAPI string
	// Info carries the document's identity.
	Info Info
	// Servers lists the declared servers in document order.
	Servers []Server
	// Paths holds every path item, sorted by path so iteration is
	// deterministic regardless of document order.
	Paths []Path
	// Schemas is components.schemas by name.
	Schemas map[string]*Schema
}

// Info is the info object's fields the toolkit consumes.
type Info struct {
	Title   string
	Version string
}

// Server is one entry of the servers array.
type Server struct {
	URL string
}

// Path is one path item and its operations.
type Path struct {
	// Path is the template as written, e.g. "/tags/{tagId}".
	Path string
	// Operations is in the fixed method order get, put, post, delete,
	// patch, head, options, trace, so iteration is deterministic.
	Operations []Operation
}

// Operation is one HTTP operation.
type Operation struct {
	// Method is the upper-case HTTP method.
	Method string
	// Path is the path template the operation lives under.
	Path string
	// OperationID is the declared operationId, possibly empty.
	OperationID string
	// Parameters combines path-item and operation parameters, the
	// operation's own winning on a (name, in) collision, sorted by
	// location then name.
	Parameters []Parameter
	// RequestBody is the request schema, nil when the operation takes
	// none. JSON content is preferred when several media types are
	// declared.
	RequestBody *Schema
	// Responses is sorted by status code.
	Responses []Response
	// Extensions holds the operation's x-tfpfgen-* keys.
	Extensions Extensions
}

// Parameter is one operation parameter.
type Parameter struct {
	Name     string
	In       string
	Required bool
	Schema   *Schema
}

// Response is one declared response.
type Response struct {
	// Status is the response key as written: "200", "404", "default".
	Status string
	// Schema is the response body schema, nil when none is declared.
	Schema *Schema
}

// Schema is one schema object, either named under components.schemas or
// inline. A schema that was written as a $ref carries the target's name in
// Ref and resolves through Resolved; its own remaining fields describe only
// what sat beside the reference (extensions).
type Schema struct {
	// Name is the components.schemas key for named schemas, empty inline.
	Name string
	// Ref is the referenced schema's name when this node was a $ref.
	Ref string
	// Type is the declared type. A 3.1 type array collapses to its first
	// non-"null" entry.
	Type string
	// Format is the declared format.
	Format string
	// Pattern is the declared regular-expression constraint on a string.
	Pattern string
	// Description is the declared human description. The audit's strategy
	// compiler mines it for weak conditional hints; nothing else reads it.
	Description string
	// ReadOnly is the declared readOnly flag.
	ReadOnly bool
	// WriteOnly is the declared writeOnly flag: the property is accepted on
	// write and never returned.
	WriteOnly bool
	// Deprecated is the declared deprecated flag.
	Deprecated bool
	// UniqueItems is the declared uniqueItems flag on an array: its members
	// are a set, so the order they come back in means nothing.
	UniqueItems bool
	// MinLength and MaxLength are the declared length bounds on a string;
	// nil when absent.
	MinLength *int64
	MaxLength *int64
	// MinItems and MaxItems are the declared size bounds on an array; nil
	// when absent.
	MinItems *int64
	MaxItems *int64
	// Enum lists the declared enum values as decoded scalars.
	Enum []any
	// Default is the declared default value, decoded; nil when absent.
	Default any
	// Example is the declared example value, decoded; nil when absent.
	Example any
	// Minimum and Maximum are the declared numeric bounds; nil when absent.
	// The audit reads them to keep a probe value inside what the document
	// says is acceptable, so that a refusal means something about the field
	// rather than about the probe.
	Minimum *float64
	Maximum *float64
	// Required lists the property names the schema requires.
	Required []string
	// Properties preserves document order, which is load-bearing
	// downstream: SDK backends resolve naming collisions by encounter
	// order, so the model must not reorder what the document says.
	Properties []Property
	// Items is the array element schema.
	Items *Schema
	// AdditionalProperties is the value schema of a map-shaped object, nil
	// when the document declares none. Only a schema names the value type,
	// which is what typing the map needs.
	AdditionalProperties *Schema
	// AdditionalPropertiesDeclared records an additionalProperties the
	// document spelled as a bare boolean rather than a schema, so a refusal
	// can say which of the two it saw.
	AdditionalPropertiesDeclared bool
	// AllOf, OneOf and AnyOf hold the composition branches.
	AllOf []*Schema
	OneOf []*Schema
	AnyOf []*Schema
	// Discriminator is the OpenAPI discriminator object when the schema
	// declares one: the property whose value selects a oneOf/anyOf branch,
	// plus any explicit value→branch mapping. Nil when absent.
	Discriminator *Discriminator
	// DependentRequired holds JSON-Schema dependentRequired entries, sorted
	// by the triggering property: when the property is present, the listed
	// siblings become required.
	DependentRequired []DependentRequired
	// DependentSchemas holds JSON-Schema dependentSchemas entries, sorted by
	// the triggering property: when the property is present, the subschema
	// additionally applies.
	DependentSchemas []DependentSchema
	// Extensions holds the schema's x-tfpfgen-* keys.
	Extensions Extensions

	// resolved is the target schema when Ref is set, wired by the
	// resolution pass after every named schema exists.
	resolved *Schema
}

// Discriminator is the OpenAPI discriminator object: the property whose value
// selects which composition branch applies, and an optional explicit mapping
// from a value to the named component schema it selects.
type Discriminator struct {
	// PropertyName is the wire name of the discriminating property.
	PropertyName string
	// Mapping maps a discriminator value to the referenced schema's name.
	// Nil or empty when the document declares no explicit mapping, in which
	// case branches are matched by the value their own property enum admits.
	Mapping map[string]string
}

// DependentRequired is one JSON-Schema dependentRequired entry: when Property
// is present, every name in Requires is required too.
type DependentRequired struct {
	Property string
	Requires []string
}

// DependentSchema is one JSON-Schema dependentSchemas entry: when Property is
// present, Schema additionally constrains the object.
type DependentSchema struct {
	Property string
	Schema   *Schema
}

// Property is one named property, in document order.
type Property struct {
	Name   string
	Schema *Schema
}

// Resolved follows the reference chain to the schema that actually carries
// fields. A schema that is not a reference resolves to itself. Reference
// cycles cannot occur in what Load accepts — a $ref node carries no
// properties, so a chain of them either reaches a concrete schema or would
// revisit a node, and the visit guard stops there rather than looping.
func (s *Schema) Resolved() *Schema {
	seen := map[*Schema]bool{}
	cur := s
	for cur.resolved != nil && !seen[cur] {
		seen[cur] = true
		cur = cur.resolved
	}
	return cur
}

// Property looks a property up by name on the resolved schema.
func (s *Schema) Property(name string) (*Schema, bool) {
	for _, p := range s.Resolved().Properties {
		if p.Name == name {
			return p.Schema, true
		}
	}
	return nil, false
}

// IsRequired reports whether the resolved schema requires the property.
func (s *Schema) IsRequired(name string) bool {
	for _, r := range s.Resolved().Required {
		if r == name {
			return true
		}
	}
	return false
}

// Schema returns the named component schema.
func (d *Document) Schema(name string) (*Schema, bool) {
	s, ok := d.Schemas[name]
	return s, ok
}

// Operations flattens every operation in the document, ordered by path then
// method.
func (d *Document) Operations() []Operation {
	var out []Operation
	for _, p := range d.Paths {
		out = append(out, p.Operations...)
	}
	return out
}

// SuccessSchema is the schema of the operation's first 2xx response, falling
// back to "default" when no 2xx declares one. Nil when the operation
// declares no readable success body, which classification treats as "no
// schemas".
func (o *Operation) SuccessSchema() *Schema {
	var fallback *Schema
	for _, r := range o.Responses {
		switch {
		case len(r.Status) > 0 && r.Status[0] == '2':
			if r.Schema != nil {
				return r.Schema
			}
		case r.Status == "default" && r.Schema != nil:
			fallback = r.Schema
		}
	}
	return fallback
}

// sortParameters orders parameters by location then name, so two loads of
// the same document present them identically.
func sortParameters(params []Parameter) {
	sort.SliceStable(params, func(i, j int) bool {
		if params[i].In != params[j].In {
			return params[i].In < params[j].In
		}
		return params[i].Name < params[j].Name
	})
}
