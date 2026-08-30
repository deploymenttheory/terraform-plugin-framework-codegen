// A hand-written stand-in for the kiota-generated models package: read-side
// interfaces the builders return, concrete structs with constructors for the
// write side, pointer-typed scalars behind Get/Set pairs, and one generated
// enumeration with its Parse companion.
package models

import (
	"errors"

	"github.com/microsoft/kiota-abstractions-go/serialization"
)

// PatchUpdatedResourceEnumString is the generated enumeration for the patchUpdatedResource enumString.
type PatchUpdatedResourceEnumString int

// Enumeration members.
const (
	OPTIONONE_PATCHUPDATEDRESOURCEENUMSTRING PatchUpdatedResourceEnumString = iota
	OPTIONTWO_PATCHUPDATEDRESOURCEENUMSTRING
)

// String spells the member the way the API does.
func (e PatchUpdatedResourceEnumString) String() string {
	return [...]string{"optionOne", "optionTwo"}[e]
}

// ParsePatchUpdatedResourceEnumString parses the wire spelling, kiota-shaped.
func ParsePatchUpdatedResourceEnumString(v string) (any, error) {
	result := OPTIONONE_PATCHUPDATEDRESOURCEENUMSTRING
	switch v {
	case "optionOne":
		result = OPTIONONE_PATCHUPDATEDRESOURCEENUMSTRING
	case "optionTwo":
		result = OPTIONTWO_PATCHUPDATEDRESOURCEENUMSTRING
	default:
		return nil, errors.New("unknown PatchUpdatedResourceEnumString value: " + v)
	}
	return &result, nil
}

// PatchUpdatedResourceable is the read-side patchUpdatedResource interface.
type PatchUpdatedResourceable interface {
	GetId() *string
	GetRequiredString() *string
	GetEnumString() *PatchUpdatedResourceEnumString
	GetImmutableString() *string
	GetBoolean() *bool
	GetInteger() *int32
	GetNumber() *float64
	GetListOfStrings() []string
	GetNestedObject() PatchUpdatedResourceNestedObjectable
	GetListOfNestedObjects() []PatchUpdatedResourceListElementable
	GetMapOfNestedObjects() map[string]PatchUpdatedResourceMapValueable
	GetMapOfStrings() PatchUpdatedResource_mapOfStringsable
	GetMapOfIntegers() PatchUpdatedResource_mapOfIntegersable
	GetMapOfNumbers() PatchUpdatedResource_mapOfNumbersable
	GetMapOfBooleans() PatchUpdatedResource_mapOfBooleansable
	GetListOfLists() serialization.UntypedNodeable
	GetListOfMaps() []PatchUpdatedResource_listOfMapsable
	GetMapOfLists() PatchUpdatedResource_mapOfListsable
	GetMapOfMaps() PatchUpdatedResource_mapOfMapsable
	GetComputedString() *string
}

// PatchUpdatedResource is the concrete model the constructor yields.
type PatchUpdatedResource struct {
	id                  *string
	requiredString      *string
	enumString          *PatchUpdatedResourceEnumString
	immutableString     *string
	boolean             *bool
	integer             *int32
	number              *float64
	listOfStrings       []string
	nestedObject        PatchUpdatedResourceNestedObjectable
	listOfNestedObjects []PatchUpdatedResourceListElementable
	mapOfNestedObjects  map[string]PatchUpdatedResourceMapValueable
	mapOfStrings        PatchUpdatedResource_mapOfStringsable
	mapOfIntegers       PatchUpdatedResource_mapOfIntegersable
	mapOfNumbers        PatchUpdatedResource_mapOfNumbersable
	mapOfBooleans       PatchUpdatedResource_mapOfBooleansable
	listOfLists         serialization.UntypedNodeable
	listOfMaps          []PatchUpdatedResource_listOfMapsable
	mapOfLists          PatchUpdatedResource_mapOfListsable
	mapOfMaps           PatchUpdatedResource_mapOfMapsable
	computedString      *string
}

// NewPatchUpdatedResource constructs a settable PatchUpdatedResource.
func NewPatchUpdatedResource() *PatchUpdatedResource { return &PatchUpdatedResource{} }

// GetId reads the identifier.
func (m *PatchUpdatedResource) GetId() *string { return m.id }

// GetRequiredString reads the requiredString.
func (m *PatchUpdatedResource) GetRequiredString() *string { return m.requiredString }

// SetRequiredString writes the requiredString.
func (m *PatchUpdatedResource) SetRequiredString(v *string) { m.requiredString = v }

// GetEnumString reads the enumeration.
func (m *PatchUpdatedResource) GetEnumString() *PatchUpdatedResourceEnumString { return m.enumString }

// SetEnumString writes the enumeration.
func (m *PatchUpdatedResource) SetEnumString(v *PatchUpdatedResourceEnumString) { m.enumString = v }

// GetImmutableString reads the create-only string.
func (m *PatchUpdatedResource) GetImmutableString() *string { return m.immutableString }

// SetImmutableString writes the immutableString.
func (m *PatchUpdatedResource) SetImmutableString(v *string) { m.immutableString = v }

// GetBoolean reads the flag.
func (m *PatchUpdatedResource) GetBoolean() *bool { return m.boolean }

// SetBoolean writes the flag.
func (m *PatchUpdatedResource) SetBoolean(v *bool) { m.boolean = v }

// GetInteger reads a scalar the SDK carries narrower than the document.
func (m *PatchUpdatedResource) GetInteger() *int32 { return m.integer }

// SetInteger writes the narrow scalar.
func (m *PatchUpdatedResource) SetInteger(v *int32) { m.integer = v }

// GetNumber reads the ratio.
func (m *PatchUpdatedResource) GetNumber() *float64 { return m.number }

// SetNumber writes the ratio.
func (m *PatchUpdatedResource) SetNumber(v *float64) { m.number = v }

// GetListOfStrings reads the scalar slice.
func (m *PatchUpdatedResource) GetListOfStrings() []string { return m.listOfStrings }

// SetListOfStrings writes the scalar slice.
func (m *PatchUpdatedResource) SetListOfStrings(v []string) { m.listOfStrings = v }

// GetNestedObject reads the nested object.
func (m *PatchUpdatedResource) GetNestedObject() PatchUpdatedResourceNestedObjectable {
	return m.nestedObject
}

// SetNestedObject writes the nested object.
func (m *PatchUpdatedResource) SetNestedObject(v PatchUpdatedResourceNestedObjectable) {
	m.nestedObject = v
}

// GetListOfNestedObjects reads the nested list.
func (m *PatchUpdatedResource) GetListOfNestedObjects() []PatchUpdatedResourceListElementable {
	return m.listOfNestedObjects
}

// SetListOfNestedObjects writes the nested list.
func (m *PatchUpdatedResource) SetListOfNestedObjects(v []PatchUpdatedResourceListElementable) {
	m.listOfNestedObjects = v
}

// GetMapOfNestedObjects reads the nested map.
func (m *PatchUpdatedResource) GetMapOfNestedObjects() map[string]PatchUpdatedResourceMapValueable {
	return m.mapOfNestedObjects
}

// SetMapOfNestedObjects writes the nested map.
func (m *PatchUpdatedResource) SetMapOfNestedObjects(v map[string]PatchUpdatedResourceMapValueable) {
	m.mapOfNestedObjects = v
}

// PatchUpdatedResourceMapValue is the map's value, read through its
// interface the way a generated collection value is.
type PatchUpdatedResourceMapValueable interface {
	GetValueInteger() *int64
	GetValueBoolean() *bool
}

// PatchUpdatedResourceMapValue is the concrete map value model.
type PatchUpdatedResourceMapValue struct {
	valueInteger *int64
	valueBoolean *bool
}

// NewPatchUpdatedResourceMapValue constructs a settable value.
func NewPatchUpdatedResourceMapValue() *PatchUpdatedResourceMapValue {
	return &PatchUpdatedResourceMapValue{}
}

// GetValueInteger reads the integer.
func (m *PatchUpdatedResourceMapValue) GetValueInteger() *int64 { return m.valueInteger }

// SetValueInteger writes the integer.
func (m *PatchUpdatedResourceMapValue) SetValueInteger(v *int64) { m.valueInteger = v }

// GetValueBoolean reads the boolean.
func (m *PatchUpdatedResourceMapValue) GetValueBoolean() *bool { return m.valueBoolean }

// SetValueBoolean writes the boolean.
func (m *PatchUpdatedResourceMapValue) SetValueBoolean(v *bool) { m.valueBoolean = v }

// GetMapOfStrings reads the map-shaped object.
func (m *PatchUpdatedResource) GetMapOfStrings() PatchUpdatedResource_mapOfStringsable {
	return m.mapOfStrings
}

// SetMapOfStrings writes the map-shaped object.
func (m *PatchUpdatedResource) SetMapOfStrings(v PatchUpdatedResource_mapOfStringsable) {
	m.mapOfStrings = v
}

// GetMapOfIntegers reads the map-shaped object.
func (m *PatchUpdatedResource) GetMapOfIntegers() PatchUpdatedResource_mapOfIntegersable {
	return m.mapOfIntegers
}

// SetMapOfIntegers writes the map-shaped object.
func (m *PatchUpdatedResource) SetMapOfIntegers(v PatchUpdatedResource_mapOfIntegersable) {
	m.mapOfIntegers = v
}

// GetMapOfNumbers reads the map-shaped object.
func (m *PatchUpdatedResource) GetMapOfNumbers() PatchUpdatedResource_mapOfNumbersable {
	return m.mapOfNumbers
}

// SetMapOfNumbers writes the map-shaped object.
func (m *PatchUpdatedResource) SetMapOfNumbers(v PatchUpdatedResource_mapOfNumbersable) {
	m.mapOfNumbers = v
}

// GetMapOfBooleans reads the map-shaped object.
func (m *PatchUpdatedResource) GetMapOfBooleans() PatchUpdatedResource_mapOfBooleansable {
	return m.mapOfBooleans
}

// SetMapOfBooleans writes the map-shaped object.
func (m *PatchUpdatedResource) SetMapOfBooleans(v PatchUpdatedResource_mapOfBooleansable) {
	m.mapOfBooleans = v
}

// GetComputedString reads the value only the API sets.
func (m *PatchUpdatedResource) GetComputedString() *string { return m.computedString }

// PatchUpdatedResourceNestedObjectable is the read-side nestedObject interface.
type PatchUpdatedResourceNestedObjectable interface {
	GetNestedInteger() *int64
	GetNestedBoolean() *bool
}

// PatchUpdatedResourceNestedObject is the concrete nestedObject model.
type PatchUpdatedResourceNestedObject struct {
	nestedInteger *int64
	nestedBoolean *bool
}

// NewPatchUpdatedResourceNestedObject constructs a settable PatchUpdatedResourceNestedObject.
func NewPatchUpdatedResourceNestedObject() *PatchUpdatedResourceNestedObject {
	return &PatchUpdatedResourceNestedObject{}
}

// GetNestedInteger reads the nestedInteger.
func (m *PatchUpdatedResourceNestedObject) GetNestedInteger() *int64 { return m.nestedInteger }

// SetNestedInteger writes the nestedInteger.
func (m *PatchUpdatedResourceNestedObject) SetNestedInteger(v *int64) { m.nestedInteger = v }

// GetNestedBoolean reads the flag.
func (m *PatchUpdatedResourceNestedObject) GetNestedBoolean() *bool { return m.nestedBoolean }

// SetNestedBoolean writes the flag.
func (m *PatchUpdatedResourceNestedObject) SetNestedBoolean(v *bool) { m.nestedBoolean = v }

// PatchUpdatedResourceListElementable is the read-side list element interface.
type PatchUpdatedResourceListElementable interface {
	GetElementString() *string
	GetElementInteger() *int64
}

// PatchUpdatedResourceListElement is the concrete list element model.
type PatchUpdatedResourceListElement struct {
	elementString  *string
	elementInteger *int64
}

// NewPatchUpdatedResourceListElement constructs a settable PatchUpdatedResourceListElement.
func NewPatchUpdatedResourceListElement() *PatchUpdatedResourceListElement {
	return &PatchUpdatedResourceListElement{}
}

// GetElementString reads the elementString.
func (m *PatchUpdatedResourceListElement) GetElementString() *string { return m.elementString }

// SetElementString writes the elementString.
func (m *PatchUpdatedResourceListElement) SetElementString(v *string) { m.elementString = v }

// GetElementInteger reads the elementInteger.
func (m *PatchUpdatedResourceListElement) GetElementInteger() *int64 { return m.elementInteger }

// SetElementInteger writes the elementInteger.
func (m *PatchUpdatedResourceListElement) SetElementInteger(v *int64) { m.elementInteger = v }

// PatchUpdatedResourceCollectionResponseable is the read-side collection envelope.
type PatchUpdatedResourceCollectionResponseable interface {
	GetValue() []PatchUpdatedResourceable
}

// PatchUpdatedResourceCollectionResponse is the concrete collection envelope.
type PatchUpdatedResourceCollectionResponse struct {
	value []PatchUpdatedResourceable
}

// GetValue reads the elements.
func (m *PatchUpdatedResourceCollectionResponse) GetValue() []PatchUpdatedResourceable {
	return m.value
}

// SetValue writes the elements.
func (m *PatchUpdatedResourceCollectionResponse) SetValue(v []PatchUpdatedResourceable) { m.value = v }

// CustomActionRequestable is the read-side customAction body interface.
type CustomActionRequestable interface {
	GetRequiredString() *string
	GetBoolean() *bool
}

// CustomActionRequest is the concrete customAction body.
type CustomActionRequest struct {
	requiredString *string
	boolean        *bool
}

// NewCustomActionRequest constructs a settable CustomActionRequest.
func NewCustomActionRequest() *CustomActionRequest { return &CustomActionRequest{} }

// GetRequiredString reads the requiredString.
func (m *CustomActionRequest) GetRequiredString() *string { return m.requiredString }

// SetRequiredString writes the requiredString.
func (m *CustomActionRequest) SetRequiredString(v *string) { m.requiredString = v }

// GetBoolean reads the flag.
func (m *CustomActionRequest) GetBoolean() *bool { return m.boolean }

// SetBoolean writes the flag.
func (m *CustomActionRequest) SetBoolean(v *bool) { m.boolean = v }

// ReplaceOnlyResourceable is the read-side replaceOnlyResource interface.
type ReplaceOnlyResourceable interface {
	GetId() *string
	GetRequiredString() *string
	GetInteger() *int64
	GetBoolean() *bool
}

// ReplaceOnlyResource is the concrete replaceOnlyResource model.
type ReplaceOnlyResource struct {
	id             *string
	requiredString *string
	integer        *int64
	boolean        *bool
}

// NewReplaceOnlyResource constructs a settable ReplaceOnlyResource.
func NewReplaceOnlyResource() *ReplaceOnlyResource { return &ReplaceOnlyResource{} }

// GetId reads the identifier.
func (m *ReplaceOnlyResource) GetId() *string { return m.id }

// GetRequiredString reads the requiredString.
func (m *ReplaceOnlyResource) GetRequiredString() *string { return m.requiredString }

// SetRequiredString writes the requiredString.
func (m *ReplaceOnlyResource) SetRequiredString(v *string) { m.requiredString = v }

// GetInteger reads the range.
func (m *ReplaceOnlyResource) GetInteger() *int64 { return m.integer }

// SetInteger writes the range.
func (m *ReplaceOnlyResource) SetInteger(v *int64) { m.integer = v }

// GetBoolean reads the flag.
func (m *ReplaceOnlyResource) GetBoolean() *bool { return m.boolean }

// SetBoolean writes the flag.
func (m *ReplaceOnlyResource) SetBoolean(v *bool) { m.boolean = v }

// ReplaceOnlyResourceCollectionResponseable is the read-side collection envelope.
type ReplaceOnlyResourceCollectionResponseable interface {
	GetValue() []ReplaceOnlyResourceable
}

// ReplaceOnlyResourceCollectionResponse is the concrete collection envelope.
type ReplaceOnlyResourceCollectionResponse struct {
	value []ReplaceOnlyResourceable
}

// GetValue reads the elements.
func (m *ReplaceOnlyResourceCollectionResponse) GetValue() []ReplaceOnlyResourceable { return m.value }

// PutUpdatedResourceable is the read-side putUpdatedResource interface.
type PutUpdatedResourceable interface {
	GetId() *string
	GetRequiredString() *string
	GetBoolean() *bool
}

// PutUpdatedResource is the concrete putUpdatedResource model.
type PutUpdatedResource struct {
	id             *string
	requiredString *string
	boolean        *bool
}

// NewPutUpdatedResource constructs a settable PutUpdatedResource.
func NewPutUpdatedResource() *PutUpdatedResource { return &PutUpdatedResource{} }

// GetId reads the identifier.
func (m *PutUpdatedResource) GetId() *string { return m.id }

// GetRequiredString reads the requiredString.
func (m *PutUpdatedResource) GetRequiredString() *string { return m.requiredString }

// SetRequiredString writes the requiredString.
func (m *PutUpdatedResource) SetRequiredString(v *string) { m.requiredString = v }

// GetBoolean reads the flag.
func (m *PutUpdatedResource) GetBoolean() *bool { return m.boolean }

// SetBoolean writes the flag.
func (m *PutUpdatedResource) SetBoolean(v *bool) { m.boolean = v }

// PutUpdatedResourceCollectionResponseable is the read-side collection envelope.
type PutUpdatedResourceCollectionResponseable interface {
	GetValue() []PutUpdatedResourceable
}

// PutUpdatedResourceCollectionResponse is the concrete collection envelope.
type PutUpdatedResourceCollectionResponse struct {
	value []PutUpdatedResourceable
}

// GetValue reads the elements.
func (m *PutUpdatedResourceCollectionResponse) GetValue() []PutUpdatedResourceable { return m.value }

// KeyAddressedDatasourceable is the read-side keyAddressedDatasource interface.
type KeyAddressedDatasourceable interface {
	GetId() *string
	GetKeyAddressedDatasourceCode() *string
	GetPlainString() *string
	GetInteger() *int64
}

// KeyAddressedDatasource is the concrete keyAddressedDatasource model.
type KeyAddressedDatasource struct {
	id                         *string
	keyAddressedDatasourceCode *string
	plainString                *string
	integer                    *int64
}

// GetId reads the identifier.
func (m *KeyAddressedDatasource) GetId() *string { return m.id }

// GetKeyAddressedDatasourceCode reads the code.
func (m *KeyAddressedDatasource) GetKeyAddressedDatasourceCode() *string {
	return m.keyAddressedDatasourceCode
}

// GetPlainString reads the plainString.
func (m *KeyAddressedDatasource) GetPlainString() *string { return m.plainString }

// GetInteger reads the integer.
func (m *KeyAddressedDatasource) GetInteger() *int64 { return m.integer }

// ListOnlyDatasourceable is the read-side listOnlyDatasource interface.
type ListOnlyDatasourceable interface {
	GetId() *string
	GetFirstString() *string
	GetSecondString() *string
}

// ListOnlyDatasource is the concrete listOnlyDatasource model.
type ListOnlyDatasource struct {
	id           *string
	firstString  *string
	secondString *string
}

// GetId reads the identifier.
func (m *ListOnlyDatasource) GetId() *string { return m.id }

// GetFirstString reads the firstString.
func (m *ListOnlyDatasource) GetFirstString() *string { return m.firstString }

// GetSecondString reads the secondString.
func (m *ListOnlyDatasource) GetSecondString() *string { return m.secondString }

// ListOnlyDatasourceCollectionResponseable is the read-side collection envelope.
type ListOnlyDatasourceCollectionResponseable interface {
	GetValue() []ListOnlyDatasourceable
}

// ListOnlyDatasourceCollectionResponse is the concrete collection envelope.
type ListOnlyDatasourceCollectionResponse struct {
	value []ListOnlyDatasourceable
}

// GetValue reads the elements.
func (m *ListOnlyDatasourceCollectionResponse) GetValue() []ListOnlyDatasourceable { return m.value }

// kiota emits no typed accessor for additionalProperties. A map-shaped
// object generates as a model of its own whose only content is an untyped
// bag, one model per property, named for the property it came from — so
// these four carry map[string]string, map[string]int64, map[string]float64
// and map[string]bool with no Go type saying so.

// PatchUpdatedResource_mapOfStringsable is the read side of the mapOfStrings bag.
type PatchUpdatedResource_mapOfStringsable interface {
	GetAdditionalData() map[string]any
}

// PatchUpdatedResource_mapOfStrings is the concrete mapOfStrings bag.
type PatchUpdatedResource_mapOfStrings struct {
	additionalData map[string]any
}

// NewPatchUpdatedResource_mapOfStrings constructs a settable PatchUpdatedResource_mapOfStrings.
func NewPatchUpdatedResource_mapOfStrings() *PatchUpdatedResource_mapOfStrings {
	return &PatchUpdatedResource_mapOfStrings{additionalData: map[string]any{}}
}

// GetAdditionalData reads the bag.
func (m *PatchUpdatedResource_mapOfStrings) GetAdditionalData() map[string]any {
	return m.additionalData
}

// SetAdditionalData writes the bag.
func (m *PatchUpdatedResource_mapOfStrings) SetAdditionalData(v map[string]any) { m.additionalData = v }

// PatchUpdatedResource_mapOfIntegersable is the read side of the mapOfIntegers bag.
type PatchUpdatedResource_mapOfIntegersable interface {
	GetAdditionalData() map[string]any
}

// PatchUpdatedResource_mapOfIntegers is the concrete mapOfIntegers bag.
type PatchUpdatedResource_mapOfIntegers struct {
	additionalData map[string]any
}

// NewPatchUpdatedResource_mapOfIntegers constructs a settable PatchUpdatedResource_mapOfIntegers.
func NewPatchUpdatedResource_mapOfIntegers() *PatchUpdatedResource_mapOfIntegers {
	return &PatchUpdatedResource_mapOfIntegers{additionalData: map[string]any{}}
}

// GetAdditionalData reads the bag.
func (m *PatchUpdatedResource_mapOfIntegers) GetAdditionalData() map[string]any {
	return m.additionalData
}

// SetAdditionalData writes the bag.
func (m *PatchUpdatedResource_mapOfIntegers) SetAdditionalData(v map[string]any) {
	m.additionalData = v
}

// PatchUpdatedResource_mapOfNumbersable is the read side of the mapOfNumbers bag.
type PatchUpdatedResource_mapOfNumbersable interface {
	GetAdditionalData() map[string]any
}

// PatchUpdatedResource_mapOfNumbers is the concrete mapOfNumbers bag.
type PatchUpdatedResource_mapOfNumbers struct {
	additionalData map[string]any
}

// NewPatchUpdatedResource_mapOfNumbers constructs a settable PatchUpdatedResource_mapOfNumbers.
func NewPatchUpdatedResource_mapOfNumbers() *PatchUpdatedResource_mapOfNumbers {
	return &PatchUpdatedResource_mapOfNumbers{additionalData: map[string]any{}}
}

// GetAdditionalData reads the bag.
func (m *PatchUpdatedResource_mapOfNumbers) GetAdditionalData() map[string]any {
	return m.additionalData
}

// SetAdditionalData writes the bag.
func (m *PatchUpdatedResource_mapOfNumbers) SetAdditionalData(v map[string]any) { m.additionalData = v }

// PatchUpdatedResource_mapOfBooleansable is the read side of the mapOfBooleans bag.
type PatchUpdatedResource_mapOfBooleansable interface {
	GetAdditionalData() map[string]any
}

// PatchUpdatedResource_mapOfBooleans is the concrete mapOfBooleans bag.
type PatchUpdatedResource_mapOfBooleans struct {
	additionalData map[string]any
}

// NewPatchUpdatedResource_mapOfBooleans constructs a settable PatchUpdatedResource_mapOfBooleans.
func NewPatchUpdatedResource_mapOfBooleans() *PatchUpdatedResource_mapOfBooleans {
	return &PatchUpdatedResource_mapOfBooleans{additionalData: map[string]any{}}
}

// GetAdditionalData reads the bag.
func (m *PatchUpdatedResource_mapOfBooleans) GetAdditionalData() map[string]any {
	return m.additionalData
}

// SetAdditionalData writes the bag.
func (m *PatchUpdatedResource_mapOfBooleans) SetAdditionalData(v map[string]any) {
	m.additionalData = v
}

// A collection of collections generates three ways, none of them a Go map
// or slice of a Go type. A list of lists is an untyped node: kiota declares
// no element type at all and hands the parsed tree over as-is. A list of
// maps is a slice of bag models, one per element. A map of lists or of maps
// is one bag model whose values are the parsed arrays or objects.

// GetListOfLists reads the untyped node.
func (m *PatchUpdatedResource) GetListOfLists() serialization.UntypedNodeable { return m.listOfLists }

// SetListOfLists writes the untyped node.
func (m *PatchUpdatedResource) SetListOfLists(v serialization.UntypedNodeable) { m.listOfLists = v }

// GetListOfMaps reads the slice of bags.
func (m *PatchUpdatedResource) GetListOfMaps() []PatchUpdatedResource_listOfMapsable {
	return m.listOfMaps
}

// SetListOfMaps writes the slice of bags.
func (m *PatchUpdatedResource) SetListOfMaps(v []PatchUpdatedResource_listOfMapsable) {
	m.listOfMaps = v
}

// GetMapOfLists reads the bag.
func (m *PatchUpdatedResource) GetMapOfLists() PatchUpdatedResource_mapOfListsable {
	return m.mapOfLists
}

// SetMapOfLists writes the bag.
func (m *PatchUpdatedResource) SetMapOfLists(v PatchUpdatedResource_mapOfListsable) {
	m.mapOfLists = v
}

// GetMapOfMaps reads the bag.
func (m *PatchUpdatedResource) GetMapOfMaps() PatchUpdatedResource_mapOfMapsable { return m.mapOfMaps }

// SetMapOfMaps writes the bag.
func (m *PatchUpdatedResource) SetMapOfMaps(v PatchUpdatedResource_mapOfMapsable) { m.mapOfMaps = v }

// PatchUpdatedResource_listOfMapsable is one element of the listOfMaps
// slice. The generated interface embeds AdditionalDataHolder, so it reads
// and writes the bag.
type PatchUpdatedResource_listOfMapsable interface {
	GetAdditionalData() map[string]any
	SetAdditionalData(map[string]any)
}

// PatchUpdatedResource_listOfMaps is the concrete listOfMaps element.
type PatchUpdatedResource_listOfMaps struct {
	additionalData map[string]any
}

// NewPatchUpdatedResource_listOfMaps constructs a settable PatchUpdatedResource_listOfMaps.
func NewPatchUpdatedResource_listOfMaps() *PatchUpdatedResource_listOfMaps {
	return &PatchUpdatedResource_listOfMaps{additionalData: map[string]any{}}
}

// GetAdditionalData reads the bag.
func (m *PatchUpdatedResource_listOfMaps) GetAdditionalData() map[string]any { return m.additionalData }

// SetAdditionalData writes the bag.
func (m *PatchUpdatedResource_listOfMaps) SetAdditionalData(v map[string]any) { m.additionalData = v }

// PatchUpdatedResource_mapOfListsable is the read side of the mapOfLists bag.
type PatchUpdatedResource_mapOfListsable interface {
	GetAdditionalData() map[string]any
	SetAdditionalData(map[string]any)
}

// PatchUpdatedResource_mapOfLists is the concrete mapOfLists bag.
type PatchUpdatedResource_mapOfLists struct {
	additionalData map[string]any
}

// NewPatchUpdatedResource_mapOfLists constructs a settable PatchUpdatedResource_mapOfLists.
func NewPatchUpdatedResource_mapOfLists() *PatchUpdatedResource_mapOfLists {
	return &PatchUpdatedResource_mapOfLists{additionalData: map[string]any{}}
}

// GetAdditionalData reads the bag.
func (m *PatchUpdatedResource_mapOfLists) GetAdditionalData() map[string]any { return m.additionalData }

// SetAdditionalData writes the bag.
func (m *PatchUpdatedResource_mapOfLists) SetAdditionalData(v map[string]any) { m.additionalData = v }

// PatchUpdatedResource_mapOfMapsable is the read side of the mapOfMaps bag.
type PatchUpdatedResource_mapOfMapsable interface {
	GetAdditionalData() map[string]any
	SetAdditionalData(map[string]any)
}

// PatchUpdatedResource_mapOfMaps is the concrete mapOfMaps bag.
type PatchUpdatedResource_mapOfMaps struct {
	additionalData map[string]any
}

// NewPatchUpdatedResource_mapOfMaps constructs a settable PatchUpdatedResource_mapOfMaps.
func NewPatchUpdatedResource_mapOfMaps() *PatchUpdatedResource_mapOfMaps {
	return &PatchUpdatedResource_mapOfMaps{additionalData: map[string]any{}}
}

// GetAdditionalData reads the bag.
func (m *PatchUpdatedResource_mapOfMaps) GetAdditionalData() map[string]any { return m.additionalData }

// SetAdditionalData writes the bag.
func (m *PatchUpdatedResource_mapOfMaps) SetAdditionalData(v map[string]any) { m.additionalData = v }
