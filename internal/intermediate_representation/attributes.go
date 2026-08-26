package intermediate_representation

import (
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// flat is one schema with references resolved and allOf composition folded
// in, so tree derivation reads a single view of what the object declares.
type flat struct {
	empty        bool
	declaredType string
	// format is the document's declared format, which says what a string
	// carries beyond being a string: a password to hide, a timestamp to
	// spell as one.
	format      string
	readOnly    bool
	writeOnly   bool
	deprecated  bool
	uniqueItems bool
	// hasDefault records that the document states a default for the
	// property. What the default is never reaches a generated schema; that
	// the API has one does.
	hasDefault bool
	// The declared constraints, nil when the document states none. They
	// become plan-time validators.
	pattern              string
	minimum, maximum     *float64
	minLength, maxLength *int64
	minItems, maxItems   *int64
	// description is the document's own prose for the schema, folded from
	// the first branch that states any. It is the only human-written text in
	// the whole derivation; everything else the generated schema says about
	// an attribute is inferred.
	description string
	enum        []any
	// example is the document's declared example value. It is the vendor's
	// own statement of a value the API accepts, which is the only thing a
	// document says about a string whose shape it otherwise leaves to prose.
	example  any
	required map[string]bool
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
		if schema.WriteOnly {
			flattened.writeOnly = true
		}
		if schema.Deprecated {
			flattened.deprecated = true
		}
		if schema.UniqueItems {
			flattened.uniqueItems = true
		}
		if schema.Default != nil {
			flattened.hasDefault = true
		}
		// The declared facts about the value, folded first-wins like the
		// description: a branch that states one is more specific than a
		// branch that states nothing.
		if flattened.format == "" {
			flattened.format = schema.Format
		}
		if flattened.pattern == "" {
			flattened.pattern = schema.Pattern
		}
		foldBound(&flattened.minimum, schema.Minimum)
		foldBound(&flattened.maximum, schema.Maximum)
		foldBound(&flattened.minLength, schema.MinLength)
		foldBound(&flattened.maxLength, schema.MaxLength)
		foldBound(&flattened.minItems, schema.MinItems)
		foldBound(&flattened.maxItems, schema.MaxItems)
		if flattened.description == "" {
			flattened.description = strings.TrimSpace(schema.Description)
		}
		if flattened.enum == nil {
			flattened.enum = schema.Enum
		}
		if flattened.example == nil {
			flattened.example = schema.Example
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

// foldBound takes a declared bound the fold has not seen yet. First
// declaration wins, matching how the description and the enum fold: a later
// branch that states the same bound states nothing new, and one that states a
// different bound is describing a different use of the same type.
func foldBound[T int64 | float64](dst **T, declared *T) {
	if *dst == nil && declared != nil {
		value := *declared
		*dst = &value
	}
}

// passwordFormat is the format a document gives a value that is a secret.
const passwordFormat = "password"

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
	serverDefault, serverFills := extensions.ServerDefault()
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

	// What the document declares about the value itself. These are taken
	// from the write side when there is one: a constraint is a rule about
	// what may be sent, and a response schema restating it says nothing
	// extra. writeOnly is the exception — only a request schema can declare
	// it, so a response-only attribute could never carry it anyway.
	attribute.Format = flatPrimary.format
	attribute.Example = flatPrimary.example
	// What the API itself answered for this property, where a run has read
	// one. It outranks every other source of a fixture value: a document says
	// what should be accepted, this is what was.
	attribute.ServerDefault = serverDefault
	attribute.WriteOnly = flatCreate.writeOnly
	attribute.Deprecated = flatCreate.deprecated || flatRead.deprecated
	attribute.UniqueItems = flatPrimary.uniqueItems
	attribute.Pattern = flatPrimary.pattern
	attribute.Minimum, attribute.Maximum = flatPrimary.minimum, flatPrimary.maximum
	attribute.MinLength, attribute.MaxLength = flatPrimary.minLength, flatPrimary.maxLength
	attribute.MinItems, attribute.MaxItems = flatPrimary.minItems, flatPrimary.maxItems

	// A value the document says is a secret. Either declaration is enough on
	// its own: format: password names what the value is, and writeOnly says
	// the API takes it and never gives it back, which is what a credential
	// does. Read from both sides — a request schema is where a secret is
	// declared, and a response schema that names one describes the same field.
	attribute.Sensitive = flatCreate.writeOnly || flatRead.writeOnly ||
		flatCreate.format == passwordFormat || flatRead.format == passwordFormat

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
	case serverFills || attributeSite.requiredRead || flatCreate.hasDefault || attributeSite.read != nil:
		// Writable, and the response carries a value whether or not the
		// request supplied one: the practitioner may set it and Terraform
		// must accept the server's choice when they do not.
		//
		// Three routes to the same fact, of decreasing authority.
		// x-tfpfgen-server-default is the audit's own measurement, taken by
		// omitting the attribute and reading what came back. The response
		// schema's `required` list is the document asserting it. A declared
		// default is the document stating what the server substitutes for an
		// omitted value, which is the same claim in different words.
		//
		// None is redundant, and none of the three is reached often enough
		// on its own. An API that declares nothing required in its responses
		// — as real documents routinely do — sent every writable optional
		// field to plain Optional below, and terraform refuses an apply whose
		// result differs from its plan, so each field the server filled
		// failed the apply outright rather than merely diffing.
		//
		// The response schema declaring the property at all is the fourth and
		// weakest route, and the one that catches those documents: a property
		// the response describes is a property the response can carry, and an
		// attribute terraform must let the server fill. Where the server does
		// leave it absent the cost is the one row 3 of docs/mapping.md
		// records — it plans as unknown until the create fills it, and
		// removal from configuration is sticky — which is a diff to explain
		// rather than an apply that cannot succeed.
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
