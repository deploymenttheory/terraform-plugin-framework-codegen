package intermediate_representation

import (
	"fmt"
	"sort"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// flat is one schema with references resolved and allOf composition folded
// in, so tree derivation reads a single view of what the object declares.
type flat struct {
	empty    bool
	typ      string
	readOnly bool
	enum     []any
	required map[string]bool
	// props preserves encounter order — document order first, allOf
	// branches after — with the first declaration of a name winning.
	props    []specmodel.Property
	items    *specmodel.Schema
	hasUnion bool
	ext      specmodel.Extensions
}

// flatten folds a schema and its allOf branches flat. Extensions written
// beside a $ref win over the target's own, matching how a reference site
// annotates the use rather than the definition.
func flatten(s *specmodel.Schema) flat {
	f := flat{required: map[string]bool{}, ext: specmodel.Extensions{}}
	if s == nil {
		f.empty = true
		return f
	}
	seenSchema := map[*specmodel.Schema]bool{}
	seenProp := map[string]bool{}

	var walk func(s *specmodel.Schema)
	walk = func(s *specmodel.Schema) {
		if s == nil || seenSchema[s] {
			return
		}
		seenSchema[s] = true
		for k, v := range s.Extensions {
			if _, taken := f.ext[k]; !taken {
				f.ext[k] = v
			}
		}
		if r := s.Resolved(); r != s {
			walk(r)
			return
		}
		if f.typ == "" {
			f.typ = s.Type
		}
		if s.ReadOnly {
			f.readOnly = true
		}
		if f.enum == nil {
			f.enum = s.Enum
		}
		for _, name := range s.Required {
			f.required[name] = true
		}
		for _, p := range s.Properties {
			if !seenProp[p.Name] {
				seenProp[p.Name] = true
				f.props = append(f.props, p)
			}
		}
		if f.items == nil {
			f.items = s.Items
		}
		if len(s.OneOf)+len(s.AnyOf) > 0 {
			f.hasUnion = true
		}
		for _, branch := range s.AllOf {
			walk(branch)
		}
	}
	walk(s)
	return f
}

// prop finds a flattened schema's property by wire name.
func (f flat) prop(name string) *specmodel.Schema {
	for _, p := range f.props {
		if p.Name == name {
			return p.Schema
		}
	}
	return nil
}

// buildTree derives an object's attribute tree from its create request
// schema combined with its read response schema. Either side may be nil: a
// nil create side means everything is computed (a read-only view), a nil
// read side means the request stands alone. Response-only properties come
// out computed. replaceAll marks every writable attribute RequiresReplace,
// which is what a missing update operation amounts to.
func buildTree(create, read *specmodel.Schema, replaceAll bool) *AttributeTree {
	fc, fr := flatten(create), flatten(read)
	tree := &AttributeTree{}
	conditionals := map[[2]string][]string{}

	addAttr := func(name string, createSide, readSide *specmodel.Schema) {
		attr, rw := buildAttribute(name, site{
			create:         createSide,
			read:           readSide,
			requiredCreate: fc.required[name],
			requiredRead:   fr.required[name],
			replaceAll:     replaceAll,
		})
		tree.Attributes = append(tree.Attributes, attr)
		if rw != nil {
			gate := [2]string{snakeCase(rw.Property), rw.Equals}
			conditionals[gate] = append(conditionals[gate], attr.Name)
		}
	}

	for _, p := range fc.props {
		addAttr(p.Name, p.Schema, fr.prop(p.Name))
	}
	for _, p := range fr.props {
		if fc.prop(p.Name) == nil {
			addAttr(p.Name, nil, p.Schema)
		}
	}

	tree.ConditionalRequirements = sortedConditionals(conditionals)
	return tree
}

// sortedConditionals renders the gathered value-conditional rules in a
// fixed order, so the map above cannot leak iteration order into output.
func sortedConditionals(gates map[[2]string][]string) []ConditionalRequirement {
	if len(gates) == 0 {
		return nil
	}
	out := make([]ConditionalRequirement, 0, len(gates))
	for gate, names := range gates {
		sort.Strings(names)
		out = append(out, ConditionalRequirement{Property: gate[0], Equals: gate[1], Required: names})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Property != out[j].Property {
			return out[i].Property < out[j].Property
		}
		return out[i].Equals < out[j].Equals
	})
	return out
}

// site is one property seen from both sides of the create/read fold.
type site struct {
	create         *specmodel.Schema // nil when response-only
	read           *specmodel.Schema // nil when the response omits it
	requiredCreate bool
	requiredRead   bool
	replaceAll     bool
}

// buildAttribute decides one attribute, returning any value-conditional
// requirement declared on it for the tree to aggregate.
func buildAttribute(wire string, at site) (Attribute, *specmodel.RequiredWhen) {
	attr := Attribute{Name: snakeCase(wire), WireName: wire}

	writable := at.create != nil
	fc, fr := flatten(at.create), flatten(at.read)
	fp := fc
	if !writable {
		fp = fr
	}
	ext := mergeExtensions(fc.ext, fr.ext)

	serverForced, _ := ext.ServerForced()
	volatile, _ := ext.Volatile()
	createOnly, _ := ext.CreateOnly()
	attr.SilentlyIgnoredOnUpdate, _ = ext.SilentlyIgnoredOnUpdate()

	switch {
	case !writable || fp.readOnly || serverForced || volatile:
		attr.Presence = PresenceComputed
	case at.requiredCreate:
		attr.Presence = PresenceRequired
	case at.requiredRead:
		// Writable, optional, yet the response always carries it: the
		// server fills a default, so the practitioner may set it and
		// Terraform must tolerate the server's value when they do not.
		attr.Presence = PresenceOptionalComputed
	default:
		attr.Presence = PresenceOptional
	}
	if attr.Presence != PresenceComputed && (createOnly || at.replaceAll) {
		attr.RequiresReplace = true
	}

	// A computed attribute's children are computed too, whatever the
	// create schema declared for them.
	childCreate := at.create
	if attr.Presence == PresenceComputed {
		childCreate = nil
	}
	deriveType(&attr, fp, childCreate, at.read)

	if len(fp.enum) > 0 && attr.Kind != TypeList && attr.Kind != TypeObject && !attr.Unsupported {
		values := renderEnum(fp.enum)
		if open, _ := ext.ValuesOpen(); open {
			attr.AdvisoryValues = values
		} else {
			attr.OneOf = values
		}
	}

	if rw, ok := ext.RequiredWhen(); ok {
		return attr, &rw
	}
	return attr, nil
}

// deriveType maps the schema shape onto an attribute type, refusing the
// shapes the toolkit does not model rather than guessing: an Unsupported
// attribute names its reason and generates nothing.
func deriveType(attr *Attribute, fp flat, create, read *specmodel.Schema) {
	switch {
	case fp.typ == "string":
		attr.Kind = TypeString
	case fp.typ == "boolean":
		attr.Kind = TypeBool
	case fp.typ == "integer":
		attr.Kind = TypeInt64
	case fp.typ == "number":
		attr.Kind = TypeFloat64
	case fp.typ == "array":
		deriveListType(attr, create, read)
	case fp.typ == "object" || (fp.typ == "" && len(fp.props) > 0):
		if len(fp.props) == 0 {
			refuse(attr, "free-form object: map support is out of scope")
			return
		}
		attr.Kind = TypeObject
		attr.Nested = buildTree(create, read, false)
	case fp.typ == "" && fp.hasUnion:
		refuse(attr, "oneOf/anyOf union: no single attribute type describes it")
	case fp.typ == "":
		refuse(attr, "no type declared")
	default:
		refuse(attr, fmt.Sprintf("type %q is not supported", fp.typ))
	}
}

// deriveListType types an array attribute from its element schema, seen
// from both sides of the create/read fold.
func deriveListType(attr *Attribute, create, read *specmodel.Schema) {
	createItems, readItems := flatten(create).items, flatten(read).items
	primary := createItems
	if primary == nil {
		primary = readItems
	}
	fi := flatten(primary)
	switch {
	case fi.empty:
		refuse(attr, "array declares no items schema")
	case fi.typ == "string":
		attr.Kind, attr.ElemKind = TypeList, TypeString
	case fi.typ == "boolean":
		attr.Kind, attr.ElemKind = TypeList, TypeBool
	case fi.typ == "integer":
		attr.Kind, attr.ElemKind = TypeList, TypeInt64
	case fi.typ == "number":
		attr.Kind, attr.ElemKind = TypeList, TypeFloat64
	case fi.typ == "object" || (fi.typ == "" && len(fi.props) > 0):
		if len(fi.props) == 0 {
			refuse(attr, "array of free-form objects: map support is out of scope")
			return
		}
		attr.Kind, attr.ElemKind = TypeList, TypeObject
		attr.Nested = buildTree(createItems, readItems, false)
	default:
		refuse(attr, fmt.Sprintf("array of %q elements is not supported", fi.typ))
	}
}

// refuse marks an attribute unsupported with the reason a person reads.
func refuse(attr *Attribute, reason string) {
	attr.Kind = ""
	attr.Unsupported = true
	attr.UnsupportedReason = reason
}

// mergeExtensions folds the read side's property extensions under the
// create side's, the create side winning a collision: the writable view is
// where behaviour annotations are authored.
func mergeExtensions(create, read specmodel.Extensions) specmodel.Extensions {
	out := specmodel.Extensions{}
	for k, v := range read {
		out[k] = v
	}
	for k, v := range create {
		out[k] = v
	}
	return out
}

// renderEnum spells enum values for a validator, in document order.
func renderEnum(values []any) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, fmt.Sprintf("%v", v))
	}
	return out
}

// ensureID guarantees the id attribute every resource and datasource
// carries: computed, mapped from the response's id field when the schema
// declares one, otherwise synthesized from the item path parameter.
func ensureID(tree *AttributeTree, keyParam string, keyType TypeKind) {
	for i := range tree.Attributes {
		if tree.Attributes[i].Name == "id" {
			tree.Attributes[i].Presence = PresenceComputed
			tree.Attributes[i].RequiresReplace = false
			return
		}
	}
	wire := keyParam
	if wire == "" {
		wire = "id"
	}
	kind := keyType
	if kind == "" {
		kind = TypeString
	}
	tree.Attributes = append([]Attribute{{
		Name:     "id",
		WireName: wire,
		Kind:     kind,
		Presence: PresenceComputed,
	}}, tree.Attributes...)
}

// requireKey turns the lookup key into the datasource's single required
// argument: the matching attribute becomes required, or a new one is
// prepended when the response object does not carry the key.
func requireKey(tree *AttributeTree, keyParam string, keyType TypeKind) {
	name := snakeCase(keyParam)
	for i := range tree.Attributes {
		if tree.Attributes[i].Name == name {
			tree.Attributes[i].Presence = PresenceRequired
			return
		}
	}
	kind := keyType
	if kind == "" {
		kind = TypeString
	}
	tree.Attributes = append([]Attribute{{
		Name:     name,
		WireName: keyParam,
		Kind:     kind,
		Presence: PresenceRequired,
	}}, tree.Attributes...)
}
