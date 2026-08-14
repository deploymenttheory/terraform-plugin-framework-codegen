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

// reservedRootNames are the names terraform reserves at the root of a
// resource or datasource schema, because a practitioner writing one means the
// meta-argument rather than the attribute. The set is
// fwschema.ReservedResourceAttributeNames.
var reservedRootNames = map[string]bool{
	"connection": true, "count": true, "depends_on": true, "for_each": true,
	"lifecycle": true, "provider": true, "provisioner": true,
}

// refuseReservedRootNames refuses a root attribute terraform will not accept
// the name of.
//
// Refused rather than renamed: the name is what a practitioner writes, and
// choosing another belongs in a correction to the document rather than in a
// rule here. The cost of declaring one is the whole provider — terraform
// rejects the schema and loads none of it — so this is not a refusal that can
// be deferred to the operator's judgement.
//
// Root only, matching the framework: the same name nested inside an object is
// an ordinary field and needs no special syntax.
func refuseReservedRootNames(tree *AttributeTree) {
	if tree == nil {
		return
	}
	for index := range tree.Attributes {
		attribute := &tree.Attributes[index]
		if !reservedRootNames[attribute.Name] {
			continue
		}
		refuse(attribute, fmt.Sprintf(
			"terraform reserves %q at the root of a schema, and refuses to load a provider that declares it; rename the property in a correction",
			attribute.Name))
	}
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
