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
	// dependentRequired accumulates every dependentRequired entry across the
	// folded schema, so a 3.1 co-requirement reaches the tree alongside the
	// x-tfpfgen-depends-on form a 3.0 document uses.
	dependentRequired []specmodel.DependentRequired
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
		f.dependentRequired = append(f.dependentRequired, s.DependentRequired...)
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
	required := map[[2]string][]string{}
	valid := map[[2]string][]string{}
	deps := map[string][]string{}

	addAttr := func(name string, createSide, readSide *specmodel.Schema) {
		attr, edges := buildAttribute(name, site{
			create:         createSide,
			read:           readSide,
			requiredCreate: fc.required[name],
			requiredRead:   fr.required[name],
			replaceAll:     replaceAll,
		})
		tree.Attributes = append(tree.Attributes, attr)
		if edges.requiredWhen != nil {
			gate := [2]string{snakeCase(edges.requiredWhen.Property), edges.requiredWhen.Equals}
			required[gate] = append(required[gate], attr.Name)
		}
		if edges.validWhen != nil {
			gate := [2]string{snakeCase(edges.validWhen.Property), edges.validWhen.Equals}
			valid[gate] = append(valid[gate], attr.Name)
		}
		if edges.dependsOn != nil {
			deps[attr.Name] = append(deps[attr.Name], snakeCase(edges.dependsOn.Requires))
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

	// dependentRequired (JSON Schema 3.1) folds into the same dependency set
	// as x-tfpfgen-depends-on, both keyed by the dependent attribute.
	for _, dr := range append(append([]specmodel.DependentRequired(nil), fc.dependentRequired...), fr.dependentRequired...) {
		for _, req := range dr.Requires {
			deps[snakeCase(dr.Property)] = append(deps[snakeCase(dr.Property)], snakeCase(req))
		}
	}

	tree.ConditionalRequirements = sortedConditionals(required)
	tree.ConditionalValidities = sortedValidities(valid)
	tree.Dependencies = sortedDependencies(deps)
	schemaExt := mergeExtensions(fc.ext, fr.ext)
	if names, ok := schemaExt.MutuallyExclusive(); ok {
		tree.MutuallyExclusiveGroups = [][]string{sortedUnique(snakeAll(names))}
	}
	if vc, ok := schemaExt.ValidConfiguration(); ok {
		tree.ValidConfigurations = []ValidConfiguration{convertValidConfiguration(vc)}
	}
	return tree
}

// convertValidConfiguration renders a specmodel variant structure into the IR
// form, attribute names snake-cased and order fixed.
func convertValidConfiguration(vc specmodel.ValidConfiguration) ValidConfiguration {
	out := ValidConfiguration{Discriminator: snakeCase(vc.Discriminator)}
	for _, v := range vc.Variants {
		out.Variants = append(out.Variants, ConfigVariant{Value: v.Value, Valid: sortedUnique(snakeAll(v.Fields))})
	}
	sort.Slice(out.Variants, func(i, j int) bool { return out.Variants[i].Value < out.Variants[j].Value })
	return out
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

// sortedValidities renders the value-conditional validity rules in a fixed
// order, mirroring sortedConditionals for the valid-when edge.
func sortedValidities(gates map[[2]string][]string) []ConditionalValidity {
	if len(gates) == 0 {
		return nil
	}
	out := make([]ConditionalValidity, 0, len(gates))
	for gate, names := range gates {
		sort.Strings(names)
		out = append(out, ConditionalValidity{Property: gate[0], Equals: gate[1], Valid: names})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Property != out[j].Property {
			return out[i].Property < out[j].Property
		}
		return out[i].Equals < out[j].Equals
	})
	return out
}

// sortedDependencies renders the co-requirements in a fixed order, one entry
// per dependent attribute with its required attributes sorted and de-duped.
func sortedDependencies(deps map[string][]string) []Dependency {
	if len(deps) == 0 {
		return nil
	}
	out := make([]Dependency, 0, len(deps))
	for attr, requires := range deps {
		out = append(out, Dependency{Attribute: attr, Requires: sortedUnique(requires)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Attribute < out[j].Attribute })
	return out
}

// snakeAll snake-cases a slice of wire names, preserving order.
func snakeAll(wire []string) []string {
	out := make([]string, len(wire))
	for i, w := range wire {
		out[i] = snakeCase(w)
	}
	return out
}

// sortedUnique sorts a copy of the names and drops duplicates.
func sortedUnique(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	cp := append([]string(nil), names...)
	sort.Strings(cp)
	out := cp[:0:0]
	for i, s := range cp {
		if i == 0 || s != cp[i-1] {
			out = append(out, s)
		}
	}
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

// attrEdges carries the per-attribute cross-attribute rules buildAttribute
// reads off one property for buildTree to aggregate: a value-conditional
// requirement, a value-conditional validity, and a co-requirement.
type attrEdges struct {
	requiredWhen *specmodel.RequiredWhen
	validWhen    *specmodel.ValidWhen
	dependsOn    *specmodel.DependsOn
}

// buildAttribute decides one attribute, returning the cross-attribute rules
// declared on it for the tree to aggregate.
func buildAttribute(wire string, at site) (Attribute, attrEdges) {
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
	_, serverFills := ext.ServerDefault()
	attr.SilentlyIgnoredOnUpdate, _ = ext.SilentlyIgnoredOnUpdate()

	// Every attribute lands in exactly one of five outcomes, and this is where
	// four of them are chosen (the fifth, omitted entirely, is decided by
	// deriveType marking the attribute unsupported).
	switch {
	case !writable || fp.readOnly || serverForced || volatile:
		// The practitioner cannot set it: not in the create body, declared
		// read-only, overwritten by the server, or different on every read.
		attr.Presence = PresenceComputed
	case at.requiredCreate:
		attr.Presence = PresenceRequired
	case serverFills || at.requiredRead:
		// Writable, and the response carries a value whether or not the
		// request supplied one: the practitioner may set it and Terraform
		// must accept the server's choice when they do not.
		//
		// requiredRead alone is too weak to find these. It reads the response
		// schema's `required` list, and an API that declares none — as the
		// ThousandEyes document does throughout — sends every writable
		// optional field to plain Optional below, which is a perpetual diff
		// for any field the server fills. x-tfpfgen-server-default is the
		// audit's measurement of the same fact, and it does not depend on the
		// document being diligent.
		attr.Presence = PresenceOptionalComputed
	default:
		// Writable, and the server leaves it absent when the request omits it.
		// Genuinely rare: most APIs answer with something.
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

	var edges attrEdges
	if rw, ok := ext.RequiredWhen(); ok {
		edges.requiredWhen = &rw
	}
	if vw, ok := ext.ValidWhen(); ok {
		edges.validWhen = &vw
	}
	if do, ok := ext.DependsOn(); ok {
		edges.dependsOn = &do
	}
	return attr, edges
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
