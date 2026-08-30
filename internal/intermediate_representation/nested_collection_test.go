package intermediate_representation

import (
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// arrayOf is an array schema holding the given items.
func arrayOf(items *specmodel.Schema) *specmodel.Schema {
	return &specmodel.Schema{Type: "array", Items: items}
}

// mapOf is a map-shaped object schema holding the given values.
func mapOf(values *specmodel.Schema) *specmodel.Schema {
	return &specmodel.Schema{Type: "object", AdditionalProperties: values}
}

// scalar is a schema of one declared scalar type.
func scalar(declaredType string) *specmodel.Schema {
	return &specmodel.Schema{Type: declaredType}
}

// TestUnit_DeriveElement_CarriesEveryLevel holds the derivation of a
// collection element to the levels the document declares, outermost first
// and ending in the leaf, for every pairing of list and map at two levels.
func TestUnit_DeriveElement_CarriesEveryLevel(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		container AttributeType
		element   *specmodel.Schema
		want      []AttributeType
	}{
		{"list of lists of strings", TypeList, arrayOf(scalar("string")), []AttributeType{TypeList, TypeString}},
		{"list of maps of booleans", TypeList, mapOf(scalar("boolean")), []AttributeType{TypeMap, TypeBool}},
		{"map of lists of integers", TypeMap, arrayOf(scalar("integer")), []AttributeType{TypeList, TypeInt64}},
		{"map of maps of numbers", TypeMap, mapOf(scalar("number")), []AttributeType{TypeMap, TypeFloat64}},
		{"list of lists of lists", TypeList, arrayOf(arrayOf(scalar("string"))), []AttributeType{TypeList, TypeList, TypeString}},
		{"one level stays one level", TypeList, scalar("string"), []AttributeType{TypeString}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			element, cause, reason := deriveElement(testCase.container, testCase.element, nil, nil)
			if cause.Code != "" {
				t.Fatalf("excluded: %s (%s)", reason, cause.Code)
			}
			if !reflect.DeepEqual(element.levels, testCase.want) {
				t.Errorf("levels = %v, want %v", element.levels, testCase.want)
			}
		})
	}
}

// TestUnit_DeriveElement_AListOfMapsIsAMapElementNotAFreeFormObject
// distinguishes an array whose items declare additionalProperties from one
// whose items declare nothing: the first is a map at the bottom, the second
// is the free-form exclusion.
func TestUnit_DeriveElement_AListOfMapsIsAMapElementNotAFreeFormObject(t *testing.T) {
	element, cause, _ := deriveElement(TypeList, mapOf(scalar("string")), nil, nil)
	if cause.Code != "" || !reflect.DeepEqual(element.levels, []AttributeType{TypeMap, TypeString}) {
		t.Errorf("a list of maps derived %v with cause %q, want [map string] and no cause", element.levels, cause.Code)
	}
	_, cause, reason := deriveElement(TypeList, &specmodel.Schema{Type: "object"}, nil, nil)
	if cause.Code != CauseFreeFormArrayElement || !strings.Contains(reason, "free-form") {
		t.Errorf("a list of shapeless objects excluded with %q (%s), want %q", cause.Code, reason, CauseFreeFormArrayElement)
	}
}

// TestUnit_DeriveElement_AnObjectAtTheBottomCarriesItsTree proves an
// object beneath any number of levels folds into a nested tree the way a
// list of objects does at one level.
func TestUnit_DeriveElement_AnObjectAtTheBottomCarriesItsTree(t *testing.T) {
	object := &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "name", Schema: scalar("string")},
	}}
	element, cause, reason := deriveElement(TypeMap, arrayOf(object), nil, nil)
	if cause.Code != "" {
		t.Fatalf("excluded: %s", reason)
	}
	if !reflect.DeepEqual(element.levels, []AttributeType{TypeList, TypeObject}) {
		t.Errorf("levels = %v, want [list object]", element.levels)
	}
	if element.nestedAttributes == nil || len(element.nestedAttributes.Attributes) != 1 || element.nestedAttributes.Attributes[0].Name != "name" {
		t.Errorf("the object at the bottom carries %+v, want its one attribute", element.nestedAttributes)
	}
}

// TestUnit_DeriveElement_TheLeafEnumBecomesOneOf takes a closed set on the
// leaf string as the collection's own, at any depth and in a map as well as
// a list.
func TestUnit_DeriveElement_TheLeafEnumBecomesOneOf(t *testing.T) {
	enum := &specmodel.Schema{Type: "string", Enum: []any{"a", "b"}}
	for name, element := range map[string]*specmodel.Schema{
		"list of enum":          enum,
		"list of lists of enum": arrayOf(enum),
		"map of enum":           enum,
	} {
		derived, cause, reason := deriveElement(TypeList, element, nil, nil)
		if cause.Code != "" {
			t.Fatalf("%s: excluded: %s", name, reason)
		}
		if !reflect.DeepEqual(derived.oneOf, []string{"a", "b"}) {
			t.Errorf("%s: oneOf = %v, want [a b]", name, derived.oneOf)
		}
	}
}

// TestUnit_DeriveElement_FoldsEveryLevelFromBothSides reads the create and
// read sides at every level: a property the response declares beneath two
// levels of collection and the request does not is computed, exactly as it
// would be at the root.
func TestUnit_DeriveElement_FoldsEveryLevelFromBothSides(t *testing.T) {
	createLeaf := &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "name", Schema: scalar("string")},
	}}
	readLeaf := &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "name", Schema: scalar("string")},
		{Name: "createdAt", Schema: scalar("string")},
	}}
	element, cause, reason := deriveElement(TypeList, mapOf(createLeaf), mapOf(readLeaf), nil)
	if cause.Code != "" {
		t.Fatalf("excluded: %s", reason)
	}
	got := map[string]ComputedOptionalRequired{}
	for _, attribute := range element.nestedAttributes.Attributes {
		got[attribute.Name] = attribute.ComputedOptionalRequired
	}
	if got["name"] != Optional && got["name"] != ComputedOptional {
		t.Errorf("name derived %q, want a writable presence", got["name"])
	}
	if got["created_at"] != Computed {
		t.Errorf("created_at derived %q, want computed: it is on the read side only", got["created_at"])
	}
}

// TestUnit_DeriveElement_ExclusionsNameTheLevelThatCannotBeTyped holds
// each exclusion to the level that caused it, so a document wrong two
// levels down is reported for what it declared there.
func TestUnit_DeriveElement_ExclusionsNameTheLevelThatCannotBeTyped(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		container AttributeType
		element   *specmodel.Schema
		wantCause string
		wantWords string
	}{
		{"inner array with no items", TypeList, arrayOf(nil), CauseItemlessArray, "no items"},
		{"inner free-form object in a list", TypeList, arrayOf(&specmodel.Schema{Type: "object"}), CauseFreeFormArrayElement, "free-form"},
		{"inner object with no properties in a map", TypeMap, mapOf(&specmodel.Schema{Type: "object"}), CauseMapOfObjects, "gives no properties"},
		{"inner bare-boolean additionalProperties", TypeList, arrayOf(&specmodel.Schema{Type: "object", AdditionalPropertiesDeclared: true}), CauseUntypedAdditionalProperties, "bare boolean"},
		{"inner unsupported array element", TypeMap, arrayOf(scalar("null")), CauseUnsupportedArrayElement, `array of "null"`},
		{"inner unsupported map value", TypeList, mapOf(scalar("null")), CauseUnsupportedMapValue, `map of "null"`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, cause, reason := deriveElement(testCase.container, testCase.element, nil, nil)
			if cause.Code != testCase.wantCause {
				t.Errorf("cause = %q (%s), want %q", cause.Code, reason, testCase.wantCause)
			}
			if !strings.Contains(reason, testCase.wantWords) {
				t.Errorf("reason = %q, want it to mention %q", reason, testCase.wantWords)
			}
		})
	}
}

// TestUnit_DeriveType_ANestedCollectionIsExcludedWithItsLevels proves the
// attribute a document declares as a collection of collections is excluded
// under its own cause, the subject spelling every level, rather than under
// the cause of an element the toolkit cannot type at all.
func TestUnit_DeriveType_ANestedCollectionIsExcludedWithItsLevels(t *testing.T) {
	tree := buildAttributeTree(&specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "grid", Schema: arrayOf(arrayOf(scalar("string")))},
		{Name: "headersByDomain", Schema: mapOf(mapOf(scalar("string")))},
		{Name: "tagsByGroup", Schema: mapOf(arrayOf(scalar("string")))},
		{Name: "rows", Schema: arrayOf(mapOf(scalar("integer")))},
	}}, nil, nil, false)
	for name, wantSubject := range map[string]string{
		"grid":              "list of list of string",
		"headers_by_domain": "map of map of string",
		"tags_by_group":     "map of list of string",
		"rows":              "list of map of int64",
	} {
		derived := attribute(t, tree, name)
		if !derived.Unsupported || derived.UnsupportedCause.Code != CauseNestedCollectionElement {
			t.Errorf("%s: derived %q with cause %q, want excluded as %q", name, derived.Type, derived.UnsupportedCause.Code, CauseNestedCollectionElement)
			continue
		}
		if derived.UnsupportedCause.Subject != wantSubject {
			t.Errorf("%s: subject = %q, want %q", name, derived.UnsupportedCause.Subject, wantSubject)
		}
		if !strings.Contains(derived.UnsupportedReason, wantSubject) {
			t.Errorf("%s: reason = %q, want it to spell %q", name, derived.UnsupportedReason, wantSubject)
		}
	}
}

// TestUnit_Attribute_CollectionNestingDepthCountsTheLevels holds the depth
// an attribute reports to what its fields describe.
func TestUnit_Attribute_CollectionNestingDepthCountsTheLevels(t *testing.T) {
	for name, testCase := range map[string]struct {
		attribute Attribute
		want      int
	}{
		"scalar":          {Attribute{Type: TypeString}, 0},
		"object":          {Attribute{Type: TypeObject, NestedAttributes: &AttributeTree{}}, 0},
		"list of strings": {Attribute{Type: TypeList, ElementType: TypeString}, 1},
		"map of objects":  {Attribute{Type: TypeMap, ElementType: TypeObject, NestedAttributes: &AttributeTree{}}, 1},
		"list of lists": {Attribute{Type: TypeList, ElementType: TypeString,
			NestedCollectionElementTypes: []AttributeType{TypeList, TypeString}}, 2},
		"map of lists of objects": {Attribute{Type: TypeMap, ElementType: TypeObject, NestedAttributes: &AttributeTree{},
			NestedCollectionElementTypes: []AttributeType{TypeList, TypeObject}}, 2},
	} {
		if got := testCase.attribute.CollectionNestingDepth(); got != testCase.want {
			t.Errorf("%s: depth = %d, want %d", name, got, testCase.want)
		}
	}
}

// TestUnit_SetCollectionElement_ElementTypeIsTheLastLevel holds the one
// level of a collection of scalars or objects to ElementType alone, with
// no levels carried, and its leaf enum to the attribute's OneOf.
func TestUnit_SetCollectionElement_ElementTypeIsTheLastLevel(t *testing.T) {
	var attribute Attribute
	setCollectionElement(&attribute, TypeMap, collectionElement{levels: []AttributeType{TypeString}, oneOf: []string{"a"}})
	if attribute.Type != TypeMap || attribute.ElementType != TypeString || attribute.NestedCollectionElementTypes != nil {
		t.Errorf("a map of strings set %+v", attribute)
	}
	if !reflect.DeepEqual(attribute.OneOf, []string{"a"}) {
		t.Errorf("OneOf = %v, want the leaf's set", attribute.OneOf)
	}
	if attribute.CollectionNestingDepth() != 1 {
		t.Errorf("depth = %d, want 1", attribute.CollectionNestingDepth())
	}
}

// TestUnit_DescribeCollectionLevels_SpellsEveryLevel holds the subject an
// exclusion carries to the English a reader expects.
func TestUnit_DescribeCollectionLevels_SpellsEveryLevel(t *testing.T) {
	if got := describeCollectionLevels(TypeMap, []AttributeType{TypeList, TypeList, TypeObject}); got != "map of list of list of object" {
		t.Errorf("spelled %q", got)
	}
}
