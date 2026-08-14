package intermediate_representation

import (
	"fmt"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// flat is one schema with references resolved and allOf composition folded
// in, so tree derivation reads a single view of what the object declares.
type flat struct {
	empty        bool
	declaredType string
	readOnly     bool
	// description is the document's own prose for the schema, folded from
	// the first branch that states any. It is the only human-written text in
	// the whole derivation; everything else the generated schema says about
	// an attribute is inferred.
	description string
	enum        []any
	required    map[string]bool
	// properties preserves encounter order — document order first, allOf
	// branches after — with the first declaration of a name winning.
	properties []specmodel.Property
	items      *specmodel.Schema
	// additionalProperties is the value schema of a map-shaped object, and
	// additionalPropertiesDeclared records one spelled as a bare boolean.
	additionalProperties         *specmodel.Schema
	additionalPropertiesDeclared bool
	// unionBranches are the oneOf/anyOf branches, kept so a union can be
	// collapsed rather than refused.
	unionBranches []*specmodel.Schema
	hasUnion      bool
	extensions    specmodel.Extensions
	// dependentRequired accumulates every dependentRequired entry across the
	// folded schema, so a 3.1 co-requirement reaches the tree alongside the
	// x-tfpfgen-depends-on form a 3.0 document uses.
	dependentRequired []specmodel.DependentRequired
}

// flatten folds a schema and its allOf branches flat. Extensions written
// beside a $ref win over the target'schema own, matching how a reference site
// annotates the use rather than the definition.
func flatten(schema *specmodel.Schema) flat {
	flattened := flat{required: map[string]bool{}, extensions: specmodel.Extensions{}}
	if schema == nil {
		flattened.empty = true
		return flattened
	}
	seenSchema := map[*specmodel.Schema]bool{}
	seenProp := map[string]bool{}

	var walk func(schema *specmodel.Schema)
	walk = func(schema *specmodel.Schema) {
		if schema == nil || seenSchema[schema] {
			return
		}
		seenSchema[schema] = true
		for key, value := range schema.Extensions {
			if _, taken := flattened.extensions[key]; !taken {
				flattened.extensions[key] = value
			}
		}
		if resolved := schema.Resolved(); resolved != schema {
			walk(resolved)
			return
		}
		if flattened.declaredType == "" {
			flattened.declaredType = schema.Type
		}
		if schema.ReadOnly {
			flattened.readOnly = true
		}
		if flattened.description == "" {
			flattened.description = strings.TrimSpace(schema.Description)
		}
		if flattened.enum == nil {
			flattened.enum = schema.Enum
		}
		for _, name := range schema.Required {
			flattened.required[name] = true
		}
		for _, property := range schema.Properties {
			if !seenProp[property.Name] {
				seenProp[property.Name] = true
				flattened.properties = append(flattened.properties, property)
			}
		}
		if flattened.items == nil {
			flattened.items = schema.Items
		}
		if flattened.additionalProperties == nil {
			flattened.additionalProperties = schema.AdditionalProperties
		}
		if schema.AdditionalPropertiesDeclared {
			flattened.additionalPropertiesDeclared = true
		}
		if len(schema.OneOf)+len(schema.AnyOf) > 0 {
			flattened.hasUnion = true
			flattened.unionBranches = append(flattened.unionBranches, schema.OneOf...)
			flattened.unionBranches = append(flattened.unionBranches, schema.AnyOf...)
		}
		flattened.dependentRequired = append(flattened.dependentRequired, schema.DependentRequired...)
		for _, branch := range schema.AllOf {
			walk(branch)
		}
	}
	walk(schema)
	flattened.resolveUnion()
	return flattened
}

// scalarTypes are the declared types a single terraform attribute holds
// directly.
var scalarTypes = map[string]bool{"string": true, "integer": true, "number": true, "boolean": true}

// resolveUnion collapses a oneOf/anyOf whose branches are all scalars to one
// declared type: the shared one, or string when they disagree, because a
// string carries any scalar's text and nothing else carries a string.
//
// A union with an object branch is left alone. The generated SDK models one
// as a composed type carrying a separate accessor per branch, so the shape
// that fits is an attribute per variant rather than one collapsed type, and
// that is not derivable from the document alone.
func (flattened *flat) resolveUnion() {
	if len(flattened.unionBranches) == 0 || flattened.declaredType != "" || len(flattened.properties) > 0 {
		return
	}

	collapsed := ""
	for i, branch := range flattened.unionBranches {
		declared := flatten(branch).declaredType
		if !scalarTypes[declared] {
			return
		}
		switch {
		case i == 0:
			collapsed = declared
		case declared != collapsed:
			collapsed = "string"
		}
	}
	flattened.declaredType = collapsed
}

// findProperty finds a flattened schema'schema property by wire name.
func (flattened flat) findProperty(name string) *specmodel.Schema {
	for _, property := range flattened.properties {
		if property.Name == name {
			return property.Schema
		}
	}
	return nil
}

// buildTree derives an object'schema attribute tree from its create request
// schema combined with its read response schema. Either side may be nil: a
// nil create side means everything is computed (a read-only view), a nil
// read side means the request stands alone. Response-only properties come
// out computed. replaceAll marks every writable attribute RequiresReplace,
// which is what a missing update operation amounts to.
//
// The update request schema is the third side, and it is read for one fact
// only: which writable properties the update refuses to take. A property
// the create body declares and the update body does not is one the API
// will not let a practitioner change, which is RequiresReplace. A nil
// update side says nothing — either the entity has no update operation, in
// which case replaceAll already covers every attribute, or the document
// declares no schema for it, and an absent schema is not evidence of an
// absent field.
func buildTree(create, read, update *specmodel.Schema, replaceAll bool) *AttributeTree {
	flatCreate, flatRead := flatten(create), flatten(read)
	flatUpdate := flatten(update)
	// An update body that declares properties is what makes the difference
	// readable at all; without one, every writable attribute would look
	// refused. A body that resolves to no properties is silence — the
	// document declaring a request it never spells — not a claim that
	// nothing is updatable, and reading it as one would force replacement
	// on every writable attribute of the entity.
	hasUpdateBody := update != nil && len(flatUpdate.properties) > 0
	tree := &AttributeTree{}
	// The object's own prose, from whichever schema declares any. A response
	// schema is the better-annotated of the two about as often as a request
	// schema is.
	tree.Description = flatRead.description
	if tree.Description == "" {
		tree.Description = flatCreate.description
	}
	required := map[[2]string][]string{}
	valid := map[[2]string][]string{}
	dependencies := map[string][]string{}

	addAttribute := func(name string, createSide, readSide *specmodel.Schema) {
		attribute, edges := buildAttribute(name, site{
			create:         createSide,
			read:           readSide,
			update:         flatUpdate.findProperty(name),
			hasUpdateBody:  hasUpdateBody,
			requiredCreate: flatCreate.required[name],
			requiredRead:   flatRead.required[name],
			replaceAll:     replaceAll,
		})
		tree.Attributes = append(tree.Attributes, attribute)
		if edges.requiredWhen != nil {
			gate := [2]string{snakeCase(edges.requiredWhen.Property), edges.requiredWhen.Equals}
			required[gate] = append(required[gate], attribute.Name)
		}
		if edges.validWhen != nil {
			gate := [2]string{snakeCase(edges.validWhen.Property), edges.validWhen.Equals}
			valid[gate] = append(valid[gate], attribute.Name)
		}
		if edges.dependsOn != nil {
			dependencies[attribute.Name] = append(dependencies[attribute.Name], snakeCase(edges.dependsOn.Requires))
		}
	}

	for _, property := range flatCreate.properties {
		addAttribute(property.Name, property.Schema, flatRead.findProperty(property.Name))
	}
	for _, property := range flatRead.properties {
		if flatCreate.findProperty(property.Name) == nil {
			addAttribute(property.Name, nil, property.Schema)
		}
	}

	// dependentRequired (JSON Schema 3.1) folds into the same dependency set
	// as x-tfpfgen-depends-on, both keyed by the dependent attribute.
	for _, dependentRequired := range append(append([]specmodel.DependentRequired(nil), flatCreate.dependentRequired...), flatRead.dependentRequired...) {
		for _, requiredName := range dependentRequired.Requires {
			dependencies[snakeCase(dependentRequired.Property)] = append(dependencies[snakeCase(dependentRequired.Property)], snakeCase(requiredName))
		}
	}

	tree.ConditionalRequirements = sortedConditionals(required)
	tree.ConditionalValidities = sortedValidities(valid)
	tree.Dependencies = sortedDependencies(dependencies)
	schemaExt := mergeExtensions(flatCreate.extensions, flatRead.extensions)
	if names, ok := schemaExt.MutuallyExclusive(); ok {
		tree.MutuallyExclusiveGroups = [][]string{sortedUnique(snakeAll(names))}
	}
	if validConfiguration, ok := schemaExt.ValidConfiguration(); ok {
		tree.ValidConfigurations = []ValidConfiguration{convertValidConfiguration(validConfiguration)}
	}
	return tree
}

// convertValidConfiguration renders a specmodel variant structure into the IR
// form, attribute names snake-cased and order fixed.
func convertValidConfiguration(validConfiguration specmodel.ValidConfiguration) ValidConfiguration {
	out := ValidConfiguration{Discriminator: snakeCase(validConfiguration.Discriminator)}
	for _, value := range validConfiguration.Variants {
		out.Variants = append(out.Variants, ConfigVariant{Value: value.Value, Valid: sortedUnique(snakeAll(value.Fields))})
	}
	sort.Slice(out.Variants, func(index, j int) bool { return out.Variants[index].Value < out.Variants[j].Value })
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
	sort.Slice(out, func(index, j int) bool {
		if out[index].Property != out[j].Property {
			return out[index].Property < out[j].Property
		}
		return out[index].Equals < out[j].Equals
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
	sort.Slice(out, func(index, j int) bool {
		if out[index].Property != out[j].Property {
			return out[index].Property < out[j].Property
		}
		return out[index].Equals < out[j].Equals
	})
	return out
}

// sortedDependencies renders the co-requirements in a fixed order, one entry
// per dependent attribute with its required attributes sorted and de-duped.
func sortedDependencies(dependencies map[string][]string) []Dependency {
	if len(dependencies) == 0 {
		return nil
	}
	out := make([]Dependency, 0, len(dependencies))
	for attribute, requires := range dependencies {
		out = append(out, Dependency{Attribute: attribute, Requires: sortedUnique(requires)})
	}
	sort.Slice(out, func(index, j int) bool { return out[index].Attribute < out[j].Attribute })
	return out
}

// snakeAll snake-cases a slice of wire names, preserving order.
func snakeAll(wire []string) []string {
	out := make([]string, len(wire))
	for index, character := range wire {
		out[index] = snakeCase(character)
	}
	return out
}

// sortedUnique sorts a copy of the names and drops duplicates.
func sortedUnique(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	copied := append([]string(nil), names...)
	sort.Strings(copied)
	out := copied[:0:0]
	for index, schema := range copied {
		if index == 0 || schema != copied[index-1] {
			out = append(out, schema)
		}
	}
	return out
}

// site is one property seen from both sides of the create/read fold.
type site struct {
	create *specmodel.Schema // nil when response-only
	read   *specmodel.Schema // nil when the response omits it
	// update is the property as the update request declares it, nil when
	// the update body omits it. Read only alongside hasUpdateBody: without
	// a resolvable update body, nil says nothing.
	update *specmodel.Schema
	// hasUpdateBody reports that the entity has an update operation whose
	// request schema resolved, so an absent property is evidence rather
	// than silence.
	hasUpdateBody  bool
	requiredCreate bool
	requiredRead   bool
	replaceAll     bool
}

// attributeEdges carries the per-attribute cross-attribute rules buildAttribute
// reads off one property for buildTree to aggregate: a value-conditional
// requirement, a value-conditional validity, and a co-requirement.
type attributeEdges struct {
	requiredWhen *specmodel.RequiredWhen
	validWhen    *specmodel.ValidWhen
	dependsOn    *specmodel.DependsOn
}

// buildAttribute decides one attribute, returning the cross-attribute rules
// declared on it for the tree to aggregate.
func buildAttribute(wire string, attributeSite site) (Attribute, attributeEdges) {
	attribute := Attribute{Name: snakeCase(wire), WireName: wire}

	writable := attributeSite.create != nil
	flatCreate, flatRead := flatten(attributeSite.create), flatten(attributeSite.read)
	flatPrimary := flatCreate
	if !writable {
		flatPrimary = flatRead
	}
	extensions := mergeExtensions(flatCreate.extensions, flatRead.extensions)

	serverForced, _ := extensions.ServerForced()
	volatile, _ := extensions.Volatile()
	createOnly, _ := extensions.CreateOnly()
	_, serverFills := extensions.ServerDefault()
	attribute.SilentlyIgnoredOnUpdate, _ = extensions.SilentlyIgnoredOnUpdate()

	// The document's prose, taken from whichever side declares any. A
	// request schema and a response schema describe the same field, and one
	// of them is routinely annotated where the other is bare.
	attribute.Description = flatPrimary.description
	if attribute.Description == "" {
		if writable {
			attribute.Description = flatRead.description
		} else {
			attribute.Description = flatCreate.description
		}
	}

	// Every attribute lands in exactly one of five outcomes, and this is where
	// four of them are chosen (the fifth, omitted entirely, is decided by
	// deriveType marking the attribute unsupported).
	switch {
	case !writable || flatPrimary.readOnly || serverForced || volatile:
		// The practitioner cannot set it: not in the create body, declared
		// read-only, overwritten by the server, or different on every read.
		attribute.ComputedOptionalRequired = Computed
	case attributeSite.requiredCreate:
		attribute.ComputedOptionalRequired = Required
	case serverFills || attributeSite.requiredRead:
		// Writable, and the response carries a value whether or not the
		// request supplied one: the practitioner may set it and Terraform
		// must accept the server'schema choice when they dependsOnEdge not.
		//
		// requiredRead alone is too weak to find these. It reads the response
		// schema's `required` list, and an API that declares none — as real
		// documents routinely do throughout — sends every writable
		// optional field to plain Optional below, which is a perpetual diff
		// for any field the server fills. x-tfpfgen-server-default is the
		// audit'schema measurement of the same fact, and it does not depend on the
		// document being diligent.
		attribute.ComputedOptionalRequired = ComputedOptional
	default:
		// Writable, and the server leaves it absent when the request omits it.
		// Genuinely rare: most APIs answer with something.
		attribute.ComputedOptionalRequired = Optional
	}
	// refusedByUpdate is the document's own account of immutability: the
	// create body declares this property and the update body does not, so
	// the API offers no way to change it after create. Free, offline, and
	// true of the document whether or not an audit has ever run — which is
	// the difference between this and x-tfpfgen-create-only.
	//
	// Guarded on writable, because a response-only property is absent from
	// the update body for the uninteresting reason that it is absent from
	// every request body, and on hasUpdateBody, because a document that
	// declares no update schema is silent rather than restrictive.
	refusedByUpdate := writable && attributeSite.hasUpdateBody && attributeSite.update == nil

	if attribute.ComputedOptionalRequired != Computed && (createOnly || attributeSite.replaceAll || refusedByUpdate) {
		attribute.RequiresReplace = true
	}

	// A computed attribute'schema children are computed too, whatever the
	// create schema declared for them.
	childCreate := attributeSite.create
	if attribute.ComputedOptionalRequired == Computed {
		childCreate = nil
	}
	deriveType(&attribute, flatPrimary, childCreate, attributeSite.read, attributeSite.update)

	if len(flatPrimary.enum) > 0 && attribute.Kind != TypeList && attribute.Kind != TypeObject && !attribute.Unsupported {
		values := renderEnum(flatPrimary.enum)
		if open, _ := extensions.ValuesOpen(); open {
			attribute.AdvisoryValues = values
		} else {
			attribute.OneOf = values
		}
	}

	var edges attributeEdges
	if requiredWhenEdge, ok := extensions.RequiredWhen(); ok {
		edges.requiredWhen = &requiredWhenEdge
	}
	if validWhenEdge, ok := extensions.ValidWhen(); ok {
		edges.validWhen = &validWhenEdge
	}
	if dependsOnEdge, ok := extensions.DependsOn(); ok {
		edges.dependsOn = &dependsOnEdge
	}
	return attribute, edges
}

// deriveType maps the schema shape onto an attribute type, refusing the
// shapes the toolkit does not model rather than guessing: an Unsupported
// attribute names its reason and generates nothing.
func deriveType(attribute *Attribute, flatPrimary flat, create, read, update *specmodel.Schema) {
	switch {
	case flatPrimary.declaredType == "string":
		attribute.Kind = TypeString
	case flatPrimary.declaredType == "boolean":
		attribute.Kind = TypeBool
	case flatPrimary.declaredType == "integer":
		attribute.Kind = TypeInt64
	case flatPrimary.declaredType == "number":
		attribute.Kind = TypeFloat64
	case flatPrimary.declaredType == "array":
		deriveListType(attribute, create, read, update)
	case flatPrimary.declaredType == "object" || (flatPrimary.declaredType == "" && len(flatPrimary.properties) > 0):
		if len(flatPrimary.properties) == 0 {
			deriveMapType(attribute, flatPrimary)
			return
		}
		attribute.Kind = TypeObject
		attribute.Nested = buildTree(create, read, update, false)
	case flatPrimary.declaredType == "" && flatPrimary.hasUnion:
		// resolveUnion collapses a union whose branches are all scalars.
		// What is left has an object branch, which the generated SDK models
		// as a composed type carrying an accessor per branch — an attribute
		// per variant, not one collapsed type.
		refuse(attribute, "oneOf/anyOf union with an object branch: it needs one attribute per variant, which the document alone does not name")
	case flatPrimary.declaredType == "":
		refuse(attribute, "no type declared")
	default:
		refuse(attribute, fmt.Sprintf("type %q is not supported", flatPrimary.declaredType))
	}
}

// deriveMapType types an object that declares no properties. Only
// additionalProperties carrying a schema names the value type, which is
// what a map attribute needs; a bare boolean or nothing at all says the
// object has no declared shape, and the refusal says which was seen.
func deriveMapType(attribute *Attribute, flatPrimary flat) {
	value := flatPrimary.additionalProperties
	if value == nil {
		if flatPrimary.additionalPropertiesDeclared {
			refuse(attribute, "object whose additionalProperties is a bare boolean: it declares no value type to map")
			return
		}
		refuse(attribute, "object declaring neither properties nor additionalProperties: it has no declared shape")
		return
	}

	flatValue := flatten(value)
	switch {
	case flatValue.declaredType == "string":
		attribute.Kind, attribute.ElementType = TypeMap, TypeString
	case flatValue.declaredType == "boolean":
		attribute.Kind, attribute.ElementType = TypeMap, TypeBool
	case flatValue.declaredType == "integer":
		attribute.Kind, attribute.ElementType = TypeMap, TypeInt64
	case flatValue.declaredType == "number":
		attribute.Kind, attribute.ElementType = TypeMap, TypeFloat64
	case flatValue.declaredType == "object" || (flatValue.declaredType == "" && len(flatValue.properties) > 0):
		// A map of objects needs a nested model, nested state mapping and
		// nested fixtures; only maps of scalars are modelled.
		refuse(attribute, "map of objects: only maps of scalar values are modelled")
	default:
		refuse(attribute, fmt.Sprintf("map of %q values is not supported", flatValue.declaredType))
	}
}

// deriveListType types an array attribute from its element schema, seen
// from both sides of the create/read fold.
func deriveListType(attribute *Attribute, create, read, update *specmodel.Schema) {
	createItems, readItems, updateItems := flatten(create).items, flatten(read).items, flatten(update).items
	primary := createItems
	if primary == nil {
		primary = readItems
	}
	flatItems := flatten(primary)
	switch {
	case flatItems.empty:
		refuse(attribute, "array declares no items schema")
	case flatItems.declaredType == "string":
		attribute.Kind, attribute.ElementType = TypeList, TypeString
	case flatItems.declaredType == "boolean":
		attribute.Kind, attribute.ElementType = TypeList, TypeBool
	case flatItems.declaredType == "integer":
		attribute.Kind, attribute.ElementType = TypeList, TypeInt64
	case flatItems.declaredType == "number":
		attribute.Kind, attribute.ElementType = TypeList, TypeFloat64
	case flatItems.declaredType == "object" || (flatItems.declaredType == "" && len(flatItems.properties) > 0):
		if len(flatItems.properties) == 0 {
			refuse(attribute, "array of free-form objects: map support is out of scope")
			return
		}
		attribute.Kind, attribute.ElementType = TypeList, TypeObject
		attribute.Nested = buildTree(createItems, readItems, updateItems, false)
	default:
		refuse(attribute, fmt.Sprintf("array of %q elements is not supported", flatItems.declaredType))
	}
}

// refuse marks an attribute unsupported with the reason a person reads.
func refuse(attribute *Attribute, reason string) {
	attribute.Kind = ""
	attribute.Unsupported = true
	attribute.UnsupportedReason = reason
}

// mergeExtensions folds the read side'schema property extensions under the
// create side'schema, the create side winning a collision: the writable view is
// where behaviour annotations are authored.
func mergeExtensions(create, read specmodel.Extensions) specmodel.Extensions {
	out := specmodel.Extensions{}
	for key, value := range read {
		out[key] = value
	}
	for key, value := range create {
		out[key] = value
	}
	return out
}

// renderEnum spells enum values for a validator, in document order.
func renderEnum(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, fmt.Sprintf("%v", value))
	}
	return out
}

// ensureID guarantees the id attribute every resource and datasource
// carries: computed, mapped from the response'schema id field when the schema
// declares one, otherwise synthesized from the item path parameter.
func ensureID(tree *AttributeTree, keyParam string, keyType AttributeType) {
	for index := range tree.Attributes {
		if tree.Attributes[index].Name == "id" {
			tree.Attributes[index].ComputedOptionalRequired = Computed
			tree.Attributes[index].RequiresReplace = false
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
		Name:                     "id",
		WireName:                 wire,
		Kind:                     kind,
		ComputedOptionalRequired: Computed,
	}}, tree.Attributes...)
}

// ensureParentParameters gives every path parameter above the item key an
// attribute to be read from: required, and prepended in path order ahead of
// the id.
//
// An item path is not always /things/{id}. A parent-scoped API spells it
// /repos/{owner}/{repo}/rulesets/{ruleset_id}, and owner and repo appear in
// no request or response body — they are addressing, not content. Emission
// had nothing to feed them from and refused the entity, which on a
// thoroughly parent-scoped document is most of the API.
//
// A parent the body does declare is left as the body declares it; the
// document is a better authority on its own field than the URL is. Only a
// parameter no attribute answers is added.
func ensureParentParameters(tree *AttributeTree, parents []Parameter) {
	if tree == nil || len(parents) == 0 {
		return
	}
	// A name the tree already uses cannot be added again, whatever it holds:
	// two attributes of one name is not a schema. Where the sitting tenant is
	// an object, it is a different thing the document spells the same way — a
	// repository's owner block beside the owner segment of its path — and it
	// cannot answer the parameter either. Emission refuses the entity by
	// name, which is a better answer than a renamed attribute nobody asked
	// for or a schema that does not load.
	declared := make(map[string]bool, len(tree.Attributes))
	for _, attribute := range tree.Attributes {
		declared[attribute.Name] = true
	}

	added := make([]Attribute, 0, len(parents))
	for _, parent := range parents {
		name := snakeCase(parent.Name)
		if declared[name] {
			continue
		}
		declared[name] = true
		kind := parent.Type
		if kind == "" {
			kind = TypeString
		}
		added = append(added, Attribute{
			Name:                     name,
			WireName:                 parent.Name,
			Kind:                     kind,
			ComputedOptionalRequired: Required,
			// Addressing is not editable: an object does not move to another
			// parent in place, and every API that admits the move spells it
			// as its own operation.
			RequiresReplace: true,
		})
	}
	if len(added) == 0 {
		return
	}
	tree.Attributes = append(added, tree.Attributes...)
}

// addressingSchema is a collection path's addressing attributes as a tree of
// their own, for a list resource to declare as the configuration of its list
// block. Nil when the path takes no parameters.
//
// Every parameter is a parent: a collection path carries no item key, so
// there is no id to absorb the last one. None carries RequiresReplace — a
// list block declares a query, and a query has no plan for a modifier to act
// on.
func addressingSchema(parameters []Parameter) *AttributeTree {
	if len(parameters) == 0 {
		return nil
	}
	tree := &AttributeTree{}
	ensureParentParameters(tree, parameters)
	for index := range tree.Attributes {
		tree.Attributes[index].RequiresReplace = false
	}
	return tree
}

// parentParameters is an operation's path parameters above the item key: all
// of them but the last, which addresses the object itself and becomes the id.
func parentParameters(parameters []Parameter) []Parameter {
	if len(parameters) < 2 {
		return nil
	}
	return parameters[:len(parameters)-1]
}

// requireKey turns the lookup key into the datasource'schema single required
// argument: the matching attribute becomes required, or a new one is
// prepended when the response object does not carry the key.
func requireKey(tree *AttributeTree, keyParam string, keyType AttributeType) {
	name := snakeCase(keyParam)
	for index := range tree.Attributes {
		if tree.Attributes[index].Name == name {
			tree.Attributes[index].ComputedOptionalRequired = Required
			return
		}
	}
	kind := keyType
	if kind == "" {
		kind = TypeString
	}
	tree.Attributes = append([]Attribute{{
		Name:                     name,
		WireName:                 keyParam,
		Kind:                     kind,
		ComputedOptionalRequired: Required,
	}}, tree.Attributes...)
}
