package specmodel

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// methods is the fixed order operations are read in, so two loads of the
// same document present them identically whatever order the author wrote.
var methods = []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}

// Load reads an OpenAPI 3.x document from YAML or JSON bytes.
//
// It works on the YAML node tree rather than a decoded map for two reasons
// the correction package already established: mapping order is load-bearing
// downstream, and node keys are always their literal text — a `200:`
// response key arrives as the string "200" instead of an integer that a
// map-based decode would have to special-case.
func Load(data []byte) (*Document, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parsing the document: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, errors.New("the document is empty")
	}
	top := deref(root.Content[0])
	if top.Kind != yaml.MappingNode {
		return nil, errors.New("the document root is not a mapping")
	}

	l := &loader{
		doc:        &Document{Schemas: map[string]*Schema{}},
		parameters: map[string]Parameter{},
		bodies:     map[string]*Schema{},
		responses:  map[string]*Schema{},
	}
	if err := l.document(top); err != nil {
		return nil, err
	}
	if err := l.resolve(); err != nil {
		return nil, err
	}
	return l.doc, nil
}

// loader carries the partially built document plus the component objects
// that exist only to be referenced, and the reference fixups the resolution
// pass completes once every named schema exists.
type loader struct {
	doc        *Document
	parameters map[string]Parameter
	bodies     map[string]*Schema
	responses  map[string]*Schema
	refs       []pendingRef
}

func (l *loader) document(top *yaml.Node) error {
	version := lookup(top, "openapi")
	switch {
	case version == nil && lookup(top, "swagger") != nil:
		return errors.New("this is a Swagger 2.0 document; only OpenAPI 3.x is supported")
	case version == nil:
		return errors.New("not an OpenAPI document: no openapi version field")
	case len(version.Value) < 2 || version.Value[:2] != "3.":
		return fmt.Errorf("openapi version %q is not supported; only 3.x is", version.Value)
	}
	l.doc.OpenAPI = version.Value

	if _, err := parseExtensions(top, "document"); err != nil {
		return err
	}

	info := lookup(top, "info")
	if info == nil || lookup(info, "version") == nil {
		return errors.New("the document declares no info.version")
	}
	l.doc.Info.Version = lookup(info, "version").Value
	if title := lookup(info, "title"); title != nil {
		l.doc.Info.Title = title.Value
	}

	if servers := lookup(top, "servers"); servers != nil {
		for _, s := range servers.Content {
			if u := lookup(deref(s), "url"); u != nil {
				l.doc.Servers = append(l.doc.Servers, Server{URL: u.Value})
			}
		}
	}

	// Components before paths, so a path referencing a component parameter
	// or body finds it already parsed. Schema references need no such
	// ordering: they resolve in a dedicated pass.
	if err := l.components(lookup(top, "components")); err != nil {
		return err
	}
	return l.paths(lookup(top, "paths"))
}

func (l *loader) components(node *yaml.Node) error {
	if node = deref(node); node == nil {
		return nil
	}
	if schemas := lookup(node, "schemas"); schemas != nil {
		for name, sn := range pairs(schemas) {
			s, err := l.schema(sn, "components.schemas."+name)
			if err != nil {
				return err
			}
			s.Name = name
			l.doc.Schemas[name] = s
		}
	}
	if params := lookup(node, "parameters"); params != nil {
		for name, pn := range pairs(params) {
			at := "components.parameters." + name
			if lookup(deref(pn), "$ref") != nil {
				return fmt.Errorf("%s: a component parameter must be declared inline, not by reference", at)
			}
			p, err := l.parameter(pn, at)
			if err != nil {
				return err
			}
			l.parameters[name] = p
		}
	}
	if bodies := lookup(node, "requestBodies"); bodies != nil {
		for name, bn := range pairs(bodies) {
			s, err := l.contentSchema(deref(bn), "components.requestBodies."+name)
			if err != nil {
				return err
			}
			l.bodies[name] = s
		}
	}
	if responses := lookup(node, "responses"); responses != nil {
		for name, rn := range pairs(responses) {
			s, err := l.contentSchema(deref(rn), "components.responses."+name)
			if err != nil {
				return err
			}
			l.responses[name] = s
		}
	}
	return nil
}

func (l *loader) paths(node *yaml.Node) error {
	if node = deref(node); node == nil {
		return nil
	}
	for path, itemNode := range pairs(node) {
		at := "paths." + path
		item := deref(itemNode)
		if item.Kind != yaml.MappingNode {
			return fmt.Errorf("%s: a path item must be a mapping", at)
		}
		if _, err := parseExtensions(item, at); err != nil {
			return err
		}

		shared, err := l.parameterList(lookup(item, "parameters"), at)
		if err != nil {
			return err
		}

		p := Path{Path: path}
		for _, method := range methods {
			opNode := lookup(item, method)
			if opNode == nil {
				continue
			}
			op, err := l.operation(method, path, deref(opNode), shared)
			if err != nil {
				return err
			}
			p.Operations = append(p.Operations, op)
		}
		l.doc.Paths = append(l.doc.Paths, p)
	}
	sort.Slice(l.doc.Paths, func(i, j int) bool { return l.doc.Paths[i].Path < l.doc.Paths[j].Path })
	return nil
}

func (l *loader) operation(method, path string, node *yaml.Node, shared []Parameter) (Operation, error) {
	at := "paths." + path + "." + method
	op := Operation{Method: httpMethod(method), Path: path}

	ext, err := parseExtensions(node, at)
	if err != nil {
		return Operation{}, err
	}
	op.Extensions = ext

	if id := lookup(node, "operationId"); id != nil {
		op.OperationID = id.Value
	}

	own, err := l.parameterList(lookup(node, "parameters"), at)
	if err != nil {
		return Operation{}, err
	}
	op.Parameters = combineParameters(shared, own)

	if rb := deref(lookup(node, "requestBody")); rb != nil {
		schema, err := l.bodySchema(rb, at+".requestBody")
		if err != nil {
			return Operation{}, err
		}
		op.RequestBody = schema
	}

	if responses := deref(lookup(node, "responses")); responses != nil {
		for status, rn := range pairs(responses) {
			schema, err := l.responseSchema(deref(rn), at+".responses."+status)
			if err != nil {
				return Operation{}, err
			}
			op.Responses = append(op.Responses, Response{Status: status, Schema: schema})
		}
		sort.Slice(op.Responses, func(i, j int) bool {
			return op.Responses[i].Status < op.Responses[j].Status
		})
	}

	return op, nil
}

// bodySchema reads a request body object, following a reference to a
// component request body when that is what the document wrote.
func (l *loader) bodySchema(node *yaml.Node, at string) (*Schema, error) {
	if ref := lookup(node, "$ref"); ref != nil {
		name, err := componentName(ref.Value, "#/components/requestBodies/", at)
		if err != nil {
			return nil, err
		}
		s, ok := l.bodies[name]
		if !ok {
			return nil, fmt.Errorf("%s: references #/components/requestBodies/%s, which the document does not declare", at, name)
		}
		return s, nil
	}
	return l.contentSchema(node, at)
}

// responseSchema reads a response object, following a reference to a
// component response when that is what the document wrote.
func (l *loader) responseSchema(node *yaml.Node, at string) (*Schema, error) {
	if ref := lookup(node, "$ref"); ref != nil {
		name, err := componentName(ref.Value, "#/components/responses/", at)
		if err != nil {
			return nil, err
		}
		s, ok := l.responses[name]
		if !ok {
			return nil, fmt.Errorf("%s: references #/components/responses/%s, which the document does not declare", at, name)
		}
		return s, nil
	}
	return l.contentSchema(node, at)
}

// contentSchema picks a media type's schema out of a content mapping,
// preferring JSON and falling back to the alphabetically first media type
// that declares a schema — a fixed rule, so the choice never depends on
// document order.
func (l *loader) contentSchema(node *yaml.Node, at string) (*Schema, error) {
	content := deref(lookup(node, "content"))
	if content == nil {
		return nil, nil
	}

	var mediaTypes []string
	for mt := range pairs(content) {
		mediaTypes = append(mediaTypes, mt)
	}
	sort.Strings(mediaTypes)
	sort.SliceStable(mediaTypes, func(i, j int) bool {
		return isJSON(mediaTypes[i]) && !isJSON(mediaTypes[j])
	})

	for _, mt := range mediaTypes {
		schemaNode := lookup(deref(lookup(content, mt)), "schema")
		if schemaNode == nil {
			continue
		}
		return l.schema(schemaNode, at+".content."+mt+".schema")
	}
	return nil, nil
}

func (l *loader) parameterList(node *yaml.Node, at string) ([]Parameter, error) {
	node = deref(node)
	if node == nil {
		return nil, nil
	}
	var out []Parameter
	for i, pn := range node.Content {
		p, err := l.parameter(pn, fmt.Sprintf("%s.parameters[%d]", at, i))
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (l *loader) parameter(node *yaml.Node, at string) (Parameter, error) {
	node = deref(node)
	if ref := lookup(node, "$ref"); ref != nil {
		name, err := componentName(ref.Value, "#/components/parameters/", at)
		if err != nil {
			return Parameter{}, err
		}
		p, ok := l.parameters[name]
		if !ok {
			return Parameter{}, fmt.Errorf("%s: references #/components/parameters/%s, which the document does not declare", at, name)
		}
		return p, nil
	}

	if _, err := parseExtensions(node, at); err != nil {
		return Parameter{}, err
	}

	name, in := lookup(node, "name"), lookup(node, "in")
	if name == nil || in == nil {
		return Parameter{}, fmt.Errorf("%s: a parameter needs both name and in", at)
	}
	p := Parameter{Name: name.Value, In: in.Value}

	// Path parameters are required by definition; the spec obliges authors
	// to say so, but a document that forgot is not thereby describing an
	// optional path segment.
	p.Required = p.In == "path"
	if req := deref(lookup(node, "required")); req != nil {
		if err := req.Decode(&p.Required); err != nil {
			return Parameter{}, fmt.Errorf("%s.required: must be true or false, got %q", at, req.Value)
		}
	}

	if sn := lookup(node, "schema"); sn != nil {
		schema, err := l.schema(sn, at+".schema")
		if err != nil {
			return Parameter{}, err
		}
		p.Schema = schema
	}
	return p, nil
}

// combineParameters folds path-item parameters into an operation's own,
// the operation winning a (name, in) collision as the spec prescribes.
func combineParameters(shared, own []Parameter) []Parameter {
	if len(shared) == 0 && len(own) == 0 {
		return nil
	}
	claimed := map[[2]string]bool{}
	for _, p := range own {
		claimed[[2]string{p.Name, p.In}] = true
	}
	out := make([]Parameter, 0, len(shared)+len(own))
	out = append(out, own...)
	for _, p := range shared {
		if !claimed[[2]string{p.Name, p.In}] {
			out = append(out, p)
		}
	}
	sortParameters(out)
	return out
}

func (l *loader) schema(node *yaml.Node, at string) (*Schema, error) {
	node = deref(node)
	if node.Kind == yaml.ScalarNode {
		// A 3.1 boolean schema: `true` accepts anything, `false` nothing.
		// Both carry no structure, which an empty schema also says.
		return &Schema{}, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: a schema must be a mapping", at)
	}

	ext, err := parseExtensions(node, at)
	if err != nil {
		return nil, err
	}

	if ref := lookup(node, "$ref"); ref != nil {
		name, err := componentName(ref.Value, "#/components/schemas/", at)
		if err != nil {
			return nil, err
		}
		s := &Schema{Ref: name, Extensions: ext}
		l.refs = append(l.refs, pendingRef{schema: s, at: at})
		return s, nil
	}

	s := &Schema{Extensions: ext}

	if t := deref(lookup(node, "type")); t != nil {
		s.Type = schemaType(t)
	}
	if f := lookup(node, "format"); f != nil {
		s.Format = f.Value
	}
	if pat := lookup(node, "pattern"); pat != nil {
		s.Pattern = pat.Value
	}
	if desc := lookup(node, "description"); desc != nil {
		s.Description = desc.Value
	}
	// The declared flags. readOnly and writeOnly decide whether an attribute
	// is settable at all; deprecated and uniqueItems decide what the emitted
	// schema says about it.
	for _, field := range []struct {
		key string
		dst *bool
	}{
		{"readOnly", &s.ReadOnly},
		{"writeOnly", &s.WriteOnly},
		{"deprecated", &s.Deprecated},
		{"uniqueItems", &s.UniqueItems},
	} {
		n := deref(lookup(node, field.key))
		if n == nil {
			continue
		}
		if err := n.Decode(field.dst); err != nil {
			return nil, fmt.Errorf("%s.%s: must be true or false, got %q", at, field.key, n.Value)
		}
	}
	if enum := deref(lookup(node, "enum")); enum != nil {
		for _, en := range enum.Content {
			var v any
			if err := en.Decode(&v); err != nil {
				return nil, fmt.Errorf("%s.enum: %w", at, err)
			}
			s.Enum = append(s.Enum, v)
		}
	}
	// default and example are read as decoded values: the audit planner's
	// value synthesis prefers what the document demonstrates over what it
	// would otherwise invent.
	for _, field := range []struct {
		key string
		dst *any
	}{
		{"default", &s.Default},
		{"example", &s.Example},
	} {
		if n := deref(lookup(node, field.key)); n != nil {
			var v any
			if err := n.Decode(&v); err != nil {
				return nil, fmt.Errorf("%s.%s: %w", at, field.key, err)
			}
			*field.dst = v
		}
	}
	// Numeric bounds are read because the audit sends values: a probe outside
	// the declared range tests the API's validation, not the field, and a
	// clamp or refusal read as behaviour is a finding the document already
	// predicted. They also become plan-time validators, so a configuration
	// the API would silently clamp fails before it is sent.
	for _, field := range []struct {
		key string
		dst **float64
	}{
		{"minimum", &s.Minimum},
		{"maximum", &s.Maximum},
	} {
		if n := deref(lookup(node, field.key)); n != nil {
			var v float64
			if err := n.Decode(&v); err != nil {
				return nil, fmt.Errorf("%s.%s: %w", at, field.key, err)
			}
			*field.dst = &v
		}
	}
	// Length and size bounds, read for the same reason as the numeric ones.
	for _, field := range []struct {
		key string
		dst **int64
	}{
		{"minLength", &s.MinLength},
		{"maxLength", &s.MaxLength},
		{"minItems", &s.MinItems},
		{"maxItems", &s.MaxItems},
	} {
		if n := deref(lookup(node, field.key)); n != nil {
			var v int64
			if err := n.Decode(&v); err != nil {
				return nil, fmt.Errorf("%s.%s: %w", at, field.key, err)
			}
			*field.dst = &v
		}
	}
	if req := deref(lookup(node, "required")); req != nil {
		for _, rn := range req.Content {
			s.Required = append(s.Required, deref(rn).Value)
		}
	}
	if props := deref(lookup(node, "properties")); props != nil {
		for name, pn := range pairs(props) {
			child, err := l.schema(pn, at+".properties."+name)
			if err != nil {
				return nil, err
			}
			s.Properties = append(s.Properties, Property{Name: name, Schema: child})
		}
	}
	if items := lookup(node, "items"); items != nil {
		child, err := l.schema(items, at+".items")
		if err != nil {
			return nil, err
		}
		s.Items = child
	}
	// additionalProperties is a schema, a bare true, or absent. Only a
	// schema says what the values are, which is what a typed map needs; the
	// other two say the object has no declared shape.
	if additional := lookup(node, "additionalProperties"); additional != nil {
		if additional.Kind == yaml.MappingNode {
			child, err := l.schema(additional, at+".additionalProperties")
			if err != nil {
				return nil, err
			}
			s.AdditionalProperties = child
		} else {
			s.AdditionalPropertiesDeclared = true
		}
	}
	for _, comp := range []struct {
		key string
		dst *[]*Schema
	}{
		{"allOf", &s.AllOf},
		{"oneOf", &s.OneOf},
		{"anyOf", &s.AnyOf},
	} {
		branchesNode := deref(lookup(node, comp.key))
		if branchesNode == nil {
			continue
		}
		for i, bn := range branchesNode.Content {
			branch, err := l.schema(bn, fmt.Sprintf("%s.%s[%d]", at, comp.key, i))
			if err != nil {
				return nil, err
			}
			*comp.dst = append(*comp.dst, branch)
		}
	}

	if disc := deref(lookup(node, "discriminator")); disc != nil {
		d, err := discriminator(disc, at+".discriminator")
		if err != nil {
			return nil, err
		}
		s.Discriminator = d
	}
	if dr := deref(lookup(node, "dependentRequired")); dr != nil {
		for name, listNode := range pairs(dr) {
			var reqs []string
			for _, rn := range deref(listNode).Content {
				reqs = append(reqs, deref(rn).Value)
			}
			s.DependentRequired = append(s.DependentRequired, DependentRequired{Property: name, Requires: reqs})
		}
		sort.Slice(s.DependentRequired, func(i, j int) bool {
			return s.DependentRequired[i].Property < s.DependentRequired[j].Property
		})
	}
	if ds := deref(lookup(node, "dependentSchemas")); ds != nil {
		for name, sn := range pairs(ds) {
			child, err := l.schema(sn, at+".dependentSchemas."+name)
			if err != nil {
				return nil, err
			}
			s.DependentSchemas = append(s.DependentSchemas, DependentSchema{Property: name, Schema: child})
		}
		sort.Slice(s.DependentSchemas, func(i, j int) bool {
			return s.DependentSchemas[i].Property < s.DependentSchemas[j].Property
		})
	}

	return s, nil
}

// discriminator reads an OpenAPI discriminator object: a required propertyName
// and an optional value→schema mapping, whose reference values are reduced to
// the target component name so a consumer never re-parses a pointer.
func discriminator(node *yaml.Node, at string) (*Discriminator, error) {
	pn := lookup(node, "propertyName")
	if pn == nil || pn.Value == "" {
		return nil, fmt.Errorf("%s: a discriminator needs a non-empty propertyName", at)
	}
	d := &Discriminator{PropertyName: pn.Value}
	if m := deref(lookup(node, "mapping")); m != nil {
		d.Mapping = map[string]string{}
		for value, refNode := range pairs(m) {
			d.Mapping[value] = discriminatorTarget(deref(refNode).Value)
		}
	}
	return d, nil
}

// discriminatorTarget reduces a discriminator mapping value to a schema name:
// a "#/components/schemas/Name" reference becomes "Name", and a bare name is
// taken as written.
func discriminatorTarget(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

// schemaType reads a type declaration, collapsing a 3.1 type array to its
// first non-"null" entry: nullability is not a distinction any consumer of
// this model acts on, and the underlying type is.
func schemaType(node *yaml.Node) string {
	if node.Kind != yaml.SequenceNode {
		return node.Value
	}
	for _, tn := range node.Content {
		if v := deref(tn).Value; v != "null" {
			return v
		}
	}
	return "null"
}

// httpMethod is the upper-case spelling of a path-item method key.
func httpMethod(m string) string {
	upper := make([]byte, len(m))
	for i := 0; i < len(m); i++ {
		upper[i] = m[i] &^ 0x20
	}
	return string(upper)
}

// isJSON matches application/json and its suffixed variants such as
// application/hal+json.
func isJSON(mediaType string) bool {
	if mediaType == "application/json" {
		return true
	}
	for i := len(mediaType) - 1; i >= 0; i-- {
		if mediaType[i] == '+' {
			return mediaType[i+1:] == "json"
		}
	}
	return false
}

// deref follows YAML alias nodes so anchors read as their targets. Nil in,
// nil out, which lets lookups chain.
func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// lookup finds a mapping key's value node, nil when absent or when the node
// is not a mapping.
func lookup(n *yaml.Node, key string) *yaml.Node {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// pairs iterates a mapping's key/value pairs in document order.
func pairs(n *yaml.Node) func(yield func(string, *yaml.Node) bool) {
	n = deref(n)
	return func(yield func(string, *yaml.Node) bool) {
		if n == nil || n.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if !yield(n.Content[i].Value, n.Content[i+1]) {
				return
			}
		}
	}
}
