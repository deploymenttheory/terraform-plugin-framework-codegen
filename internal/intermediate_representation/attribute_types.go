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
// object has no declared shape, and the refusal says which was seen.
//
// A map of objects is derived the same way a list of objects is: the value
// schema is folded from both sides of the create/read pair and becomes the
// nested tree. terraform-plugin-framework has MapNestedAttribute for
// exactly this, and a document that keys objects by a name the practitioner
// chooses has no other shape to become.
func deriveMapType(attribute *Attribute, governingSchema resolvedSchema, create, read, update *specmodel.Schema) {
	valueSchema := governingSchema.additionalProperties
	if valueSchema == nil {
		if governingSchema.additionalPropertiesDeclared {
			exclude(attribute, Cause{Code: CauseUntypedAdditionalProperties}, "object whose additionalProperties is a bare boolean: it declares no value type to map")
			return
		}
		exclude(attribute, Cause{Code: CauseObjectWithoutPropertiesOrAdditionalProperties}, "object declaring neither properties nor additionalProperties: it has no declared shape")
		return
	}

	resolvedValue := resolveSchema(valueSchema)
	switch {
	case resolvedValue.declaredType == "string":
		attribute.Type, attribute.ElementType = TypeMap, TypeString
	case resolvedValue.declaredType == "boolean":
		attribute.Type, attribute.ElementType = TypeMap, TypeBool
	case resolvedValue.declaredType == "integer":
		attribute.Type, attribute.ElementType = TypeMap, TypeInt64
	case resolvedValue.declaredType == "number":
		attribute.Type, attribute.ElementType = TypeMap, TypeFloat64
	case resolvedValue.declaredType == "object" || (resolvedValue.declaredType == "" && len(resolvedValue.properties) > 0):
		if len(resolvedValue.properties) == 0 {
			// A value that is itself a map has no properties and is not
			// shapeless: its own additionalProperties says what it holds.
			// terraform-plugin-framework carries this as a MapAttribute
			// whose ElementType is a types.MapType; the derivation cannot
			// yet describe an element that is itself a collection, so the
			// refusal names that rather than blaming the document.
			if resolvedValue.additionalProperties != nil {
				exclude(attribute, Cause{Code: CauseMapOfMaps},
					"map whose values are themselves maps: the derivation carries one element kind and cannot yet nest one")
				return
			}
			exclude(attribute, Cause{Code: CauseMapOfObjects},
				"map of objects the specification gives no properties: there are no attributes to map")
			return
		}
		attribute.Type, attribute.ElementType = TypeMap, TypeObject
		attribute.NestedAttributes = buildAttributeTree(
			resolveSchema(create).additionalProperties,
			resolveSchema(read).additionalProperties,
			resolveSchema(update).additionalProperties,
			false)
	default:
		exclude(attribute, Cause{Code: CauseUnsupportedMapValue, Subject: resolvedValue.declaredType},
			fmt.Sprintf("map of %q values is not supported", resolvedValue.declaredType))
	}
}

// deriveListType types an array attribute from its element schema, seen
// from both sides of the create/read fold.
func deriveListType(attribute *Attribute, create, read, update *specmodel.Schema) {
	createItems, readItems, updateItems := resolveSchema(create).items, resolveSchema(read).items, resolveSchema(update).items
	governingItems := createItems
	if governingItems == nil {
		governingItems = readItems
	}
	resolvedItems := resolveSchema(governingItems)
	switch {
	case resolvedItems.empty:
		exclude(attribute, Cause{Code: CauseItemlessArray}, "array declares no items schema")
	case resolvedItems.declaredType == "string":
		attribute.Type, attribute.ElementType = TypeList, TypeString
		// The element's closed set is the list's: each member the
		// practitioner writes is validated against it, and a fixture takes
		// its member from it.
		if len(resolvedItems.enum) > 0 {
			attribute.OneOf = renderEnum(resolvedItems.enum)
		}
	case resolvedItems.declaredType == "boolean":
		attribute.Type, attribute.ElementType = TypeList, TypeBool
	case resolvedItems.declaredType == "integer":
		attribute.Type, attribute.ElementType = TypeList, TypeInt64
	case resolvedItems.declaredType == "number":
		attribute.Type, attribute.ElementType = TypeList, TypeFloat64
	case resolvedItems.declaredType == "object" || (resolvedItems.declaredType == "" && len(resolvedItems.properties) > 0):
		if len(resolvedItems.properties) == 0 {
			exclude(attribute, Cause{Code: CauseFreeFormArrayElement}, "array of free-form objects: map support is out of scope")
			return
		}
		attribute.Type, attribute.ElementType = TypeList, TypeObject
		attribute.NestedAttributes = buildAttributeTree(createItems, readItems, updateItems, false)
	default:
		exclude(attribute, Cause{Code: CauseUnsupportedArrayElement, Subject: resolvedItems.declaredType},
			fmt.Sprintf("array of %q elements is not supported", resolvedItems.declaredType))
	}
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
