// How a declared type becomes a terraform attribute kind, and the refusals
// that follow when it cannot.

package intermediate_representation

import (
	"fmt"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// deriveType maps the schema shape onto an attribute type, refusing the
// shapes the toolkit does not model rather than guessing: an Unsupported
// attribute names its reason and generates nothing.
func deriveType(attribute *Attribute, governingSchema resolvedSchema, create, read, update *specmodel.Schema) {
	switch {
	case governingSchema.declaredType == "string":
		attribute.Type = TypeString
	case governingSchema.declaredType == "boolean":
		attribute.Type = TypeBool
	case governingSchema.declaredType == "integer":
		attribute.Type = TypeInt64
	case governingSchema.declaredType == "number":
		attribute.Type = TypeFloat64
	case governingSchema.declaredType == "array":
		deriveListType(attribute, create, read, update)
	case governingSchema.declaredType == "object" || (governingSchema.declaredType == "" && len(governingSchema.properties) > 0):
		if len(governingSchema.properties) == 0 {
			deriveMapType(attribute, governingSchema, create, read, update)
			return
		}
		attribute.Type = TypeObject
		attribute.NestedAttributes = buildAttributeTree(create, read, update, false)
	case governingSchema.declaredType == "" && governingSchema.hasUnion:
		deriveUnionType(attribute, governingSchema, create, read)
	case governingSchema.declaredType == "":
		exclude(attribute, Cause{Code: CauseUndeclaredType}, "no type declared")
	default:
		exclude(attribute, Cause{Code: CauseUnsupportedType, Subject: governingSchema.declaredType},
			fmt.Sprintf("type %q is not supported", governingSchema.declaredType))
	}
}

// deriveUnionType types a oneOf/anyOf the scalar collapse left alone, which
// is one with an object branch. It becomes an object carrying one attribute
// per branch: the shape the generated SDK already has, since a union arrives
// there as a composed type with a field and an accessor per branch.
//
// A branch names itself by the component it references, which is also what
// the SDK names its accessor after, so the two agree without either being
// told about the other. An anonymous branch names nothing on either side, and
// one of those refuses the whole union rather than half of it: a union missing
// a variant is a schema that cannot hold what the API returns, which is worse
// than one that says so.
//
// Only the read-only case is served. A writable union needs its variants
// mutually exclusive in configuration, and no configurability alone expresses
// that.
func deriveUnionType(attribute *Attribute, governingSchema resolvedSchema, create, read *specmodel.Schema) {
	if create != nil {
		exclude(attribute, Cause{Code: CauseWritableUnion}, "oneOf/anyOf union in a writable position: its variants would have to be mutually exclusive in configuration, which the schema alone does not express")
		return
	}

	variants := make([]Attribute, 0, len(governingSchema.unionBranches))
	for _, branch := range governingSchema.unionBranches {
		if branch == nil || branch.Ref == "" {
			exclude(attribute, Cause{Code: CauseUnnamedUnionBranch}, fmt.Sprintf(
				"oneOf/anyOf union with %d branches, of which %d name no component: a variant the document does not name has no attribute to become",
				len(governingSchema.unionBranches), anonymousBranches(governingSchema.unionBranches)))
			return
		}
		variants = append(variants, Attribute{
			Name:     snakeCase(branch.Ref),
			WireName: branch.Ref,
			Type:     TypeObject,
			// Read-only throughout: the API answers with one branch and the
			// practitioner chooses none of them.
			ComputedOptionalRequired: Computed,
			Description:              resolveSchema(branch).description,
			NestedAttributes:         buildAttributeTree(nil, branch, nil, false),
		})
	}
	if len(variants) == 0 {
		exclude(attribute, Cause{Code: CauseEmptyUnion}, "oneOf/anyOf union declaring no branches")
		return
	}

	attribute.Type = TypeObject
	attribute.NestedAttributes = &AttributeTree{Attributes: variants, Description: resolveSchema(read).description}
}

// anonymousBranches counts the branches of a union that reference no
// component, for a refusal that says how much of the union is unnameable.
func anonymousBranches(branches []*specmodel.Schema) int {
	count := 0
	for _, branch := range branches {
		if branch == nil || branch.Ref == "" {
			count++
		}
	}
	return count
}

// deriveMapType types an object that declares no properties. Only
// additionalProperties carrying a schema names the value type, which is
// what a map attribute needs; a bare boolean or nothing at all says the
// object has no declared shape, and the exclusion says which was seen.
//
// The value is derived by deriveElement, which folds it from the three
// sides of the create/read/update pair and recurses where the value is
// itself a collection: a map of objects becomes a MapNestedAttribute, a map
// of scalars a MapAttribute, and a map of lists or maps a MapAttribute whose
// element type is composed to depth.
func deriveMapType(attribute *Attribute, governingSchema resolvedSchema, create, read, update *specmodel.Schema) {
	if governingSchema.additionalProperties == nil {
		if governingSchema.additionalPropertiesDeclared {
			exclude(attribute, Cause{Code: CauseUntypedAdditionalProperties}, "object whose additionalProperties is a bare boolean: it declares no value type to map")
			return
		}
		exclude(attribute, Cause{Code: CauseObjectWithoutPropertiesOrAdditionalProperties}, "object declaring neither properties nor additionalProperties: it has no declared shape")
		return
	}
	element, cause, reason := deriveElement(TypeMap,
		resolveSchema(create).additionalProperties,
		resolveSchema(read).additionalProperties,
		resolveSchema(update).additionalProperties)
	if cause.Code != "" {
		exclude(attribute, cause, reason)
		return
	}
	setCollectionElement(attribute, TypeMap, element)
}

// deriveListType types an array attribute from its element schema, seen
// from both sides of the create/read fold.
func deriveListType(attribute *Attribute, create, read, update *specmodel.Schema) {
	element, cause, reason := deriveElement(TypeList,
		resolveSchema(create).items,
		resolveSchema(read).items,
		resolveSchema(update).items)
	if cause.Code != "" {
		exclude(attribute, cause, reason)
		return
	}
	setCollectionElement(attribute, TypeList, element)
}

// collectionElement is what one collection level's element derives to:
// every level beneath it down to the leaf, the tree of an object at the
// bottom, and the closed set of a string at the bottom.
type collectionElement struct {
	levels           []AttributeType
	nestedAttributes *AttributeTree
	oneOf            []string
}

// deriveElement types the element one collection level holds, seen from the
// three sides of the fold, and recurses where the element is itself a
// collection. container is the type of the collection whose element this is:
// a list and a map name an untypeable element under different causes, so a
// reader of the report sees which of the two the document declared.
func deriveElement(container AttributeType, create, read, update *specmodel.Schema) (collectionElement, Cause, string) {
	governing := create
	if governing == nil {
		governing = read
	}
	resolved := resolveSchema(governing)
	switch {
	case resolved.empty:
		if container == TypeMap {
			return collectionElement{}, Cause{Code: CauseUntypedAdditionalProperties}, "object whose additionalProperties declares no value schema"
		}
		return collectionElement{}, Cause{Code: CauseItemlessArray}, "array declares no items schema"
	case resolved.declaredType == "string":
		element := collectionElement{levels: []AttributeType{TypeString}}
		// The element's closed set is the collection's: each member the
		// practitioner writes is validated against it, and a fixture takes
		// its member from it.
		if len(resolved.enum) > 0 {
			element.oneOf = renderEnum(resolved.enum)
		}
		return element, Cause{}, ""
	case resolved.declaredType == "boolean":
		return collectionElement{levels: []AttributeType{TypeBool}}, Cause{}, ""
	case resolved.declaredType == "integer":
		return collectionElement{levels: []AttributeType{TypeInt64}}, Cause{}, ""
	case resolved.declaredType == "number":
		return collectionElement{levels: []AttributeType{TypeFloat64}}, Cause{}, ""
	case resolved.declaredType == "array":
		inner, cause, reason := deriveElement(TypeList,
			resolveSchema(create).items, resolveSchema(read).items, resolveSchema(update).items)
		if cause.Code != "" {
			return collectionElement{}, cause, reason
		}
		inner.levels = append([]AttributeType{TypeList}, inner.levels...)
		return inner, Cause{}, ""
	case resolved.declaredType == "object" || (resolved.declaredType == "" && len(resolved.properties) > 0):
		if len(resolved.properties) > 0 {
			return collectionElement{
				levels:           []AttributeType{TypeObject},
				nestedAttributes: buildAttributeTree(create, read, update, false),
			}, Cause{}, ""
		}
		// An element that is itself a map has no properties and is not
		// shapeless: its own additionalProperties says what it holds.
		if resolved.additionalProperties != nil {
			inner, cause, reason := deriveElement(TypeMap,
				resolveSchema(create).additionalProperties,
				resolveSchema(read).additionalProperties,
				resolveSchema(update).additionalProperties)
			if cause.Code != "" {
				return collectionElement{}, cause, reason
			}
			inner.levels = append([]AttributeType{TypeMap}, inner.levels...)
			return inner, Cause{}, ""
		}
		if resolved.additionalPropertiesDeclared {
			return collectionElement{}, Cause{Code: CauseUntypedAdditionalProperties}, "object whose additionalProperties is a bare boolean: it declares no value type to map"
		}
		if container == TypeMap {
			return collectionElement{}, Cause{Code: CauseMapOfObjects}, "map of objects the specification gives no properties: there are no attributes to map"
		}
		return collectionElement{}, Cause{Code: CauseFreeFormArrayElement}, "array of free-form objects: the specification gives the items neither properties nor additionalProperties"
	default:
		if container == TypeMap {
			return collectionElement{}, Cause{Code: CauseUnsupportedMapValue, Subject: resolved.declaredType},
				fmt.Sprintf("map of %q values is not supported", resolved.declaredType)
		}
		return collectionElement{}, Cause{Code: CauseUnsupportedArrayElement, Subject: resolved.declaredType},
			fmt.Sprintf("array of %q elements is not supported", resolved.declaredType)
	}
}

// setCollectionElement records a derived element on a collection attribute,
// spelling ElementType and NestedCollectionElementTypes from one answer so
// the two cannot disagree: the leaf is the last level, and the levels are
// carried only where there is more than one.
//
// A collection whose element is itself a collection is described in full
// and then excluded here, because the emitter does not yet compose an
// element type to depth. The exclusion names every level, so the report
// says exactly what the document declared.
func setCollectionElement(attribute *Attribute, collection AttributeType, element collectionElement) {
	if len(element.levels) > 1 {
		spelled := describeCollectionLevels(collection, element.levels)
		exclude(attribute, Cause{Code: CauseNestedCollectionElement, Subject: spelled},
			fmt.Sprintf("%s: a collection whose element is itself a collection, which the emitter cannot yet compose", spelled))
		return
	}
	attribute.Type = collection
	attribute.ElementType = element.levels[len(element.levels)-1]
	attribute.NestedAttributes = element.nestedAttributes
	attribute.OneOf = element.oneOf
}

// describeCollectionLevels spells a collection and its levels the way a
// person would: "map of list of string".
func describeCollectionLevels(collection AttributeType, levels []AttributeType) string {
	spelled := string(collection)
	for _, level := range levels {
		spelled += " of " + string(level)
	}
	return spelled
}

// exclude marks an attribute unsupported with the reason a person reads.
func exclude(attribute *Attribute, cause Cause, reason string) {
	attribute.Type = ""
	attribute.Unsupported = true
	attribute.UnsupportedCause = cause
	attribute.UnsupportedReason = reason
}

// reservedRootNames are the names terraform reserves at the root of a
// resource or datasource schema, because a practitioner writing one means the
// meta-argument rather than the attribute. The set is
// fwschema.ReservedResourceAttributeNames.
var reservedRootNames = map[string]bool{
	"connection": true, "count": true, "depends_on": true, "for_each": true,
	"lifecycle": true, "provider": true, "provisioner": true,
}

// excludeReservedRootNames excludes a root attribute terraform will not accept
// the name of.
//
// Excluded rather than renamed: the name is what a practitioner writes, and
// choosing another belongs in a correction to the document rather than in a
// rule here. The cost of declaring one is the whole provider — terraform
// rejects the schema and loads none of it — so this is not an exclusion that can
// be deferred to the operator's judgement.
//
// Root only, matching the framework: the same name nested inside an object is
// an ordinary field and needs no special syntax.
func excludeReservedRootNames(tree *AttributeTree) {
	if tree == nil {
		return
	}
	for index := range tree.Attributes {
		attribute := &tree.Attributes[index]
		if !reservedRootNames[attribute.Name] {
			continue
		}
		exclude(attribute, Cause{Code: CauseReservedRootName, Subject: attribute.Name}, fmt.Sprintf(
			"terraform reserves %q at the root of a schema, and refuses to load a provider that declares it; rename the property in a correction",
			attribute.Name))
	}
}

// mergeExtensions folds the read side's property extensions under the
// create side's, the create side winning a collision: the writable view is
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
