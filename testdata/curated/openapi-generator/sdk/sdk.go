// A hand-written stand-in for the openapi-generator SDK the curated fixture
// binds against: the configuration and client the provider core touches,
// flat per-service structs with Execute-style request builders, value-typed
// models with WithDefaults constructors, and one generated string
// enumeration with its FromValue companion. The method bodies never run.
package sdk

import (
	"context"
	"errors"
	"net/http"
)

// errStub is what every stub call answers; nothing ever invokes one.
var errStub = errors.New("the stub SDK carries shape, not behaviour")

// Configuration stands in for the generated configuration.
type Configuration struct {
	UserAgent  string
	HTTPClient *http.Client
	Servers    ServerConfigurations
}

// NewConfiguration stands in for the generated constructor.
func NewConfiguration() *Configuration { return &Configuration{} }

// ServerConfiguration stands in for one generated server entry.
type ServerConfiguration struct {
	URL         string
	Description string
}

// ServerConfigurations stands in for the generated server list.
type ServerConfigurations []ServerConfiguration

// APIClient stands in for the generated client.
type APIClient struct {
	configuration *Configuration

	// The flat service structs the bindings reach through.
	PatchUpdatedResourcesAPI   *PatchUpdatedResourcesAPIService
	ReplaceOnlyResourcesAPI    *ReplaceOnlyResourcesAPIService
	PutUpdatedResourcesAPI     *PutUpdatedResourcesAPIService
	KeyAddressedDatasourcesAPI *KeyAddressedDatasourcesAPIService
	ListOnlyDatasourcesAPI     *ListOnlyDatasourcesAPIService
}

// NewAPIClient stands in for the generated constructor.
func NewAPIClient(configuration *Configuration) *APIClient {
	return &APIClient{
		configuration:              configuration,
		PatchUpdatedResourcesAPI:   &PatchUpdatedResourcesAPIService{},
		ReplaceOnlyResourcesAPI:    &ReplaceOnlyResourcesAPIService{},
		PutUpdatedResourcesAPI:     &PutUpdatedResourcesAPIService{},
		KeyAddressedDatasourcesAPI: &KeyAddressedDatasourcesAPIService{},
		ListOnlyDatasourcesAPI:     &ListOnlyDatasourcesAPIService{},
	}
}

// PatchUpdatedResourcesAPIService is the generated patchUpdatedResource service.
type PatchUpdatedResourcesAPIService struct{}

// ApiCreatePatchUpdatedResourceRequest is the create request builder.
type ApiCreatePatchUpdatedResourceRequest struct{ patchUpdatedResource *PatchUpdatedResource }

// PatchUpdatedResource sets the request body.
func (r ApiCreatePatchUpdatedResourceRequest) PatchUpdatedResource(v PatchUpdatedResource) ApiCreatePatchUpdatedResourceRequest {
	r.patchUpdatedResource = &v
	return r
}

// Execute runs the request.
func (r ApiCreatePatchUpdatedResourceRequest) Execute() (*PatchUpdatedResource, *http.Response, error) {
	return nil, nil, errStub
}

// CreatePatchUpdatedResource begins a create.
func (s *PatchUpdatedResourcesAPIService) CreatePatchUpdatedResource(_ context.Context) ApiCreatePatchUpdatedResourceRequest {
	return ApiCreatePatchUpdatedResourceRequest{}
}

// ApiListPatchUpdatedResourcesRequest is the list request builder.
type ApiListPatchUpdatedResourcesRequest struct{}

// Execute runs the request.
func (r ApiListPatchUpdatedResourcesRequest) Execute() ([]PatchUpdatedResource, *http.Response, error) {
	return nil, nil, errStub
}

// ListPatchUpdatedResources begins a list.
func (s *PatchUpdatedResourcesAPIService) ListPatchUpdatedResources(_ context.Context) ApiListPatchUpdatedResourcesRequest {
	return ApiListPatchUpdatedResourcesRequest{}
}

// ApiGetPatchUpdatedResourceRequest is the read request builder.
type ApiGetPatchUpdatedResourceRequest struct{}

// Execute runs the request.
func (r ApiGetPatchUpdatedResourceRequest) Execute() (*PatchUpdatedResource, *http.Response, error) {
	return nil, nil, errStub
}

// GetPatchUpdatedResource begins a read.
func (s *PatchUpdatedResourcesAPIService) GetPatchUpdatedResource(_ context.Context, _ string) ApiGetPatchUpdatedResourceRequest {
	return ApiGetPatchUpdatedResourceRequest{}
}

// ApiUpdatePatchUpdatedResourceRequest is the update request builder.
type ApiUpdatePatchUpdatedResourceRequest struct{ patchUpdatedResource *PatchUpdatedResource }

// PatchUpdatedResource sets the request body.
func (r ApiUpdatePatchUpdatedResourceRequest) PatchUpdatedResource(v PatchUpdatedResource) ApiUpdatePatchUpdatedResourceRequest {
	r.patchUpdatedResource = &v
	return r
}

// Execute runs the request.
func (r ApiUpdatePatchUpdatedResourceRequest) Execute() (*PatchUpdatedResource, *http.Response, error) {
	return nil, nil, errStub
}

// UpdatePatchUpdatedResource begins an update.
func (s *PatchUpdatedResourcesAPIService) UpdatePatchUpdatedResource(_ context.Context, _ string) ApiUpdatePatchUpdatedResourceRequest {
	return ApiUpdatePatchUpdatedResourceRequest{}
}

// ApiDeletePatchUpdatedResourceRequest is the delete request builder.
type ApiDeletePatchUpdatedResourceRequest struct{}

// Execute runs the request.
func (r ApiDeletePatchUpdatedResourceRequest) Execute() (*http.Response, error) { return nil, errStub }

// DeletePatchUpdatedResource begins a delete.
func (s *PatchUpdatedResourcesAPIService) DeletePatchUpdatedResource(_ context.Context, _ string) ApiDeletePatchUpdatedResourceRequest {
	return ApiDeletePatchUpdatedResourceRequest{}
}

// ApiInvokeCustomActionRequest is the invocation request builder.
type ApiInvokeCustomActionRequest struct{ body *CustomActionRequest }

// CustomActionRequest sets the request body.
func (r ApiInvokeCustomActionRequest) CustomActionRequest(v CustomActionRequest) ApiInvokeCustomActionRequest {
	r.body = &v
	return r
}

// Execute runs the request.
func (r ApiInvokeCustomActionRequest) Execute() (*http.Response, error) { return nil, errStub }

// InvokeCustomAction begins an invocation.
func (s *PatchUpdatedResourcesAPIService) InvokeCustomAction(_ context.Context, _ string) ApiInvokeCustomActionRequest {
	return ApiInvokeCustomActionRequest{}
}

// ReplaceOnlyResourcesAPIService is the generated replaceOnlyResource service.
type ReplaceOnlyResourcesAPIService struct{}

// ApiCreateReplaceOnlyResourceRequest is the create request builder.
type ApiCreateReplaceOnlyResourceRequest struct{ replaceOnlyResource *ReplaceOnlyResource }

// ReplaceOnlyResource sets the request body.
func (r ApiCreateReplaceOnlyResourceRequest) ReplaceOnlyResource(v ReplaceOnlyResource) ApiCreateReplaceOnlyResourceRequest {
	r.replaceOnlyResource = &v
	return r
}

// Execute runs the request.
func (r ApiCreateReplaceOnlyResourceRequest) Execute() (*ReplaceOnlyResource, *http.Response, error) {
	return nil, nil, errStub
}

// CreateReplaceOnlyResource begins a create.
func (s *ReplaceOnlyResourcesAPIService) CreateReplaceOnlyResource(_ context.Context) ApiCreateReplaceOnlyResourceRequest {
	return ApiCreateReplaceOnlyResourceRequest{}
}

// ApiListReplaceOnlyResourcesRequest is the list request builder.
type ApiListReplaceOnlyResourcesRequest struct{}

// Execute runs the request.
func (r ApiListReplaceOnlyResourcesRequest) Execute() ([]ReplaceOnlyResource, *http.Response, error) {
	return nil, nil, errStub
}

// ListReplaceOnlyResources begins a list.
func (s *ReplaceOnlyResourcesAPIService) ListReplaceOnlyResources(_ context.Context) ApiListReplaceOnlyResourcesRequest {
	return ApiListReplaceOnlyResourcesRequest{}
}

// ApiGetReplaceOnlyResourceRequest is the read request builder.
type ApiGetReplaceOnlyResourceRequest struct{}

// Execute runs the request.
func (r ApiGetReplaceOnlyResourceRequest) Execute() (*ReplaceOnlyResource, *http.Response, error) {
	return nil, nil, errStub
}

// GetReplaceOnlyResource begins a read.
func (s *ReplaceOnlyResourcesAPIService) GetReplaceOnlyResource(_ context.Context, _ string) ApiGetReplaceOnlyResourceRequest {
	return ApiGetReplaceOnlyResourceRequest{}
}

// ApiDeleteReplaceOnlyResourceRequest is the delete request builder.
type ApiDeleteReplaceOnlyResourceRequest struct{}

// Execute runs the request.
func (r ApiDeleteReplaceOnlyResourceRequest) Execute() (*http.Response, error) { return nil, errStub }

// DeleteReplaceOnlyResource begins a delete.
func (s *ReplaceOnlyResourcesAPIService) DeleteReplaceOnlyResource(_ context.Context, _ string) ApiDeleteReplaceOnlyResourceRequest {
	return ApiDeleteReplaceOnlyResourceRequest{}
}

// PutUpdatedResourcesAPIService is the generated putUpdatedResource service.
type PutUpdatedResourcesAPIService struct{}

// ApiCreatePutUpdatedResourceRequest is the create request builder.
type ApiCreatePutUpdatedResourceRequest struct{ putUpdatedResource *PutUpdatedResource }

// PutUpdatedResource sets the request body.
func (r ApiCreatePutUpdatedResourceRequest) PutUpdatedResource(v PutUpdatedResource) ApiCreatePutUpdatedResourceRequest {
	r.putUpdatedResource = &v
	return r
}

// Execute runs the request.
func (r ApiCreatePutUpdatedResourceRequest) Execute() (*PutUpdatedResource, *http.Response, error) {
	return nil, nil, errStub
}

// CreatePutUpdatedResource begins a create.
func (s *PutUpdatedResourcesAPIService) CreatePutUpdatedResource(_ context.Context) ApiCreatePutUpdatedResourceRequest {
	return ApiCreatePutUpdatedResourceRequest{}
}

// ApiListPutUpdatedResourcesRequest is the list request builder.
type ApiListPutUpdatedResourcesRequest struct{}

// Execute runs the request.
func (r ApiListPutUpdatedResourcesRequest) Execute() ([]PutUpdatedResource, *http.Response, error) {
	return nil, nil, errStub
}

// ListPutUpdatedResources begins a list.
func (s *PutUpdatedResourcesAPIService) ListPutUpdatedResources(_ context.Context) ApiListPutUpdatedResourcesRequest {
	return ApiListPutUpdatedResourcesRequest{}
}

// ApiGetPutUpdatedResourceRequest is the read request builder.
type ApiGetPutUpdatedResourceRequest struct{}

// Execute runs the request.
func (r ApiGetPutUpdatedResourceRequest) Execute() (*PutUpdatedResource, *http.Response, error) {
	return nil, nil, errStub
}

// GetPutUpdatedResource begins a read.
func (s *PutUpdatedResourcesAPIService) GetPutUpdatedResource(_ context.Context, _ string) ApiGetPutUpdatedResourceRequest {
	return ApiGetPutUpdatedResourceRequest{}
}

// ApiReplacePutUpdatedResourceRequest is the replace request builder.
type ApiReplacePutUpdatedResourceRequest struct{ putUpdatedResource *PutUpdatedResource }

// PutUpdatedResource sets the request body.
func (r ApiReplacePutUpdatedResourceRequest) PutUpdatedResource(v PutUpdatedResource) ApiReplacePutUpdatedResourceRequest {
	r.putUpdatedResource = &v
	return r
}

// Execute runs the request.
func (r ApiReplacePutUpdatedResourceRequest) Execute() (*PutUpdatedResource, *http.Response, error) {
	return nil, nil, errStub
}

// ReplacePutUpdatedResource begins a replace.
func (s *PutUpdatedResourcesAPIService) ReplacePutUpdatedResource(_ context.Context, _ string) ApiReplacePutUpdatedResourceRequest {
	return ApiReplacePutUpdatedResourceRequest{}
}

// ApiDeletePutUpdatedResourceRequest is the delete request builder.
type ApiDeletePutUpdatedResourceRequest struct{}

// Execute runs the request.
func (r ApiDeletePutUpdatedResourceRequest) Execute() (*http.Response, error) { return nil, errStub }

// DeletePutUpdatedResource begins a delete.
func (s *PutUpdatedResourcesAPIService) DeletePutUpdatedResource(_ context.Context, _ string) ApiDeletePutUpdatedResourceRequest {
	return ApiDeletePutUpdatedResourceRequest{}
}

// KeyAddressedDatasourcesAPIService is the generated keyAddressedDatasource service.
type KeyAddressedDatasourcesAPIService struct{}

// ApiGetKeyAddressedDatasourceRequest is the read request builder.
type ApiGetKeyAddressedDatasourceRequest struct{}

// Execute runs the request.
func (r ApiGetKeyAddressedDatasourceRequest) Execute() (*KeyAddressedDatasource, *http.Response, error) {
	return nil, nil, errStub
}

// GetKeyAddressedDatasource begins a read.
func (s *KeyAddressedDatasourcesAPIService) GetKeyAddressedDatasource(_ context.Context, _ string) ApiGetKeyAddressedDatasourceRequest {
	return ApiGetKeyAddressedDatasourceRequest{}
}

// ListOnlyDatasourcesAPIService is the generated listOnlyDatasource service.
type ListOnlyDatasourcesAPIService struct{}

// ApiListListOnlyDatasourcesRequest is the list request builder.
type ApiListListOnlyDatasourcesRequest struct{}

// Execute runs the request.
func (r ApiListListOnlyDatasourcesRequest) Execute() ([]ListOnlyDatasource, *http.Response, error) {
	return nil, nil, errStub
}

// ListListOnlyDatasources begins a list.
func (s *ListOnlyDatasourcesAPIService) ListListOnlyDatasources(_ context.Context) ApiListListOnlyDatasourcesRequest {
	return ApiListListOnlyDatasourcesRequest{}
}

// PatchUpdatedResourceEnumString is the generated string enumeration.
type PatchUpdatedResourceEnumString string

// NewPatchUpdatedResourceEnumStringFromValue parses the wire spelling, generator-shaped.
func NewPatchUpdatedResourceEnumStringFromValue(v string) (*PatchUpdatedResourceEnumString, error) {
	k := PatchUpdatedResourceEnumString(v)
	return &k, nil
}

// PatchUpdatedResource is the flat model; getters deref, setters take values.
type PatchUpdatedResource struct {
	id                  string
	requiredString      string
	enumString          PatchUpdatedResourceEnumString
	immutableString     string
	boolean             bool
	integer             int32
	number              float64
	listOfStrings       []string
	nestedObject        PatchUpdatedResourceNestedObject
	listOfNestedObjects []PatchUpdatedResourceListElement
	mapOfNestedObjects  map[string]PatchUpdatedResourceMapValue
	mapOfStrings        map[string]string
	mapOfIntegers       map[string]int64
	mapOfNumbers        map[string]float64
	mapOfBooleans       map[string]bool
	computedString      string
}

// NewPatchUpdatedResourceWithDefaults stands in for the generated constructor.
func NewPatchUpdatedResourceWithDefaults() *PatchUpdatedResource { return &PatchUpdatedResource{} }

func (m *PatchUpdatedResource) GetId() string                                  { return m.id }
func (m *PatchUpdatedResource) GetRequiredString() string                      { return m.requiredString }
func (m *PatchUpdatedResource) SetRequiredString(v string)                     { m.requiredString = v }
func (m *PatchUpdatedResource) GetEnumString() PatchUpdatedResourceEnumString  { return m.enumString }
func (m *PatchUpdatedResource) SetEnumString(v PatchUpdatedResourceEnumString) { m.enumString = v }
func (m *PatchUpdatedResource) GetImmutableString() string                     { return m.immutableString }
func (m *PatchUpdatedResource) SetImmutableString(v string)                    { m.immutableString = v }
func (m *PatchUpdatedResource) GetBoolean() bool                               { return m.boolean }
func (m *PatchUpdatedResource) SetBoolean(v bool)                              { m.boolean = v }
func (m *PatchUpdatedResource) GetInteger() int32                              { return m.integer }
func (m *PatchUpdatedResource) SetInteger(v int32)                             { m.integer = v }
func (m *PatchUpdatedResource) GetNumber() float64                             { return m.number }
func (m *PatchUpdatedResource) SetNumber(v float64)                            { m.number = v }
func (m *PatchUpdatedResource) GetListOfStrings() []string                     { return m.listOfStrings }
func (m *PatchUpdatedResource) SetListOfStrings(v []string)                    { m.listOfStrings = v }
func (m *PatchUpdatedResource) GetNestedObject() PatchUpdatedResourceNestedObject {
	return m.nestedObject
}
func (m *PatchUpdatedResource) SetNestedObject(v PatchUpdatedResourceNestedObject) {
	m.nestedObject = v
}
func (m *PatchUpdatedResource) GetListOfNestedObjects() []PatchUpdatedResourceListElement {
	return m.listOfNestedObjects
}
func (m *PatchUpdatedResource) SetListOfNestedObjects(v []PatchUpdatedResourceListElement) {
	m.listOfNestedObjects = v
}

// The generator carries additionalProperties as a real Go map, so these
// four need no bag and no runtime assertion.
func (m *PatchUpdatedResource) GetMapOfNestedObjects() map[string]PatchUpdatedResourceMapValue {
	return m.mapOfNestedObjects
}

func (m *PatchUpdatedResource) SetMapOfNestedObjects(v map[string]PatchUpdatedResourceMapValue) {
	m.mapOfNestedObjects = v
}

// PatchUpdatedResourceMapValue is the value-typed map value.
type PatchUpdatedResourceMapValue struct {
	valueInteger int64
	valueBoolean bool
}

// NewPatchUpdatedResourceMapValueWithDefaults stands in for the generated constructor.
func NewPatchUpdatedResourceMapValueWithDefaults() *PatchUpdatedResourceMapValue {
	return &PatchUpdatedResourceMapValue{}
}

func (m *PatchUpdatedResourceMapValue) GetValueInteger() int64  { return m.valueInteger }
func (m *PatchUpdatedResourceMapValue) SetValueInteger(v int64) { m.valueInteger = v }
func (m *PatchUpdatedResourceMapValue) GetValueBoolean() bool   { return m.valueBoolean }
func (m *PatchUpdatedResourceMapValue) SetValueBoolean(v bool)  { m.valueBoolean = v }

func (m *PatchUpdatedResource) GetMapOfStrings() map[string]string   { return m.mapOfStrings }
func (m *PatchUpdatedResource) SetMapOfStrings(v map[string]string)  { m.mapOfStrings = v }
func (m *PatchUpdatedResource) GetMapOfIntegers() map[string]int64   { return m.mapOfIntegers }
func (m *PatchUpdatedResource) SetMapOfIntegers(v map[string]int64)  { m.mapOfIntegers = v }
func (m *PatchUpdatedResource) GetMapOfNumbers() map[string]float64  { return m.mapOfNumbers }
func (m *PatchUpdatedResource) SetMapOfNumbers(v map[string]float64) { m.mapOfNumbers = v }
func (m *PatchUpdatedResource) GetMapOfBooleans() map[string]bool    { return m.mapOfBooleans }
func (m *PatchUpdatedResource) SetMapOfBooleans(v map[string]bool)   { m.mapOfBooleans = v }
func (m *PatchUpdatedResource) GetComputedString() string            { return m.computedString }

// PatchUpdatedResourceNestedObject is the value-typed nested model.
type PatchUpdatedResourceNestedObject struct {
	nestedInteger int64
	nestedBoolean bool
}

// NewPatchUpdatedResourceNestedObjectWithDefaults stands in for the generated constructor.
func NewPatchUpdatedResourceNestedObjectWithDefaults() *PatchUpdatedResourceNestedObject {
	return &PatchUpdatedResourceNestedObject{}
}

func (m *PatchUpdatedResourceNestedObject) GetNestedInteger() int64  { return m.nestedInteger }
func (m *PatchUpdatedResourceNestedObject) SetNestedInteger(v int64) { m.nestedInteger = v }
func (m *PatchUpdatedResourceNestedObject) GetNestedBoolean() bool   { return m.nestedBoolean }
func (m *PatchUpdatedResourceNestedObject) SetNestedBoolean(v bool)  { m.nestedBoolean = v }

// PatchUpdatedResourceListElement is the value-typed list element.
type PatchUpdatedResourceListElement struct {
	elementString  string
	elementInteger int64
}

// NewPatchUpdatedResourceListElementWithDefaults stands in for the generated constructor.
func NewPatchUpdatedResourceListElementWithDefaults() *PatchUpdatedResourceListElement {
	return &PatchUpdatedResourceListElement{}
}

func (m *PatchUpdatedResourceListElement) GetElementString() string  { return m.elementString }
func (m *PatchUpdatedResourceListElement) SetElementString(v string) { m.elementString = v }
func (m *PatchUpdatedResourceListElement) GetElementInteger() int64  { return m.elementInteger }
func (m *PatchUpdatedResourceListElement) SetElementInteger(v int64) { m.elementInteger = v }

// CustomActionRequest is the invocation body.
type CustomActionRequest struct {
	requiredString string
	boolean        bool
}

// NewCustomActionRequestWithDefaults stands in for the generated constructor.
func NewCustomActionRequestWithDefaults() *CustomActionRequest { return &CustomActionRequest{} }

// SetRequiredString writes the requiredString.
func (m *CustomActionRequest) SetRequiredString(v string) { m.requiredString = v }

// SetBoolean writes the flag.
func (m *CustomActionRequest) SetBoolean(v bool) { m.boolean = v }

// ReplaceOnlyResource is the flat replaceOnlyResource model.
type ReplaceOnlyResource struct {
	id             string
	requiredString string
	integer        int64
	boolean        bool
}

// NewReplaceOnlyResourceWithDefaults stands in for the generated constructor.
func NewReplaceOnlyResourceWithDefaults() *ReplaceOnlyResource { return &ReplaceOnlyResource{} }

func (m *ReplaceOnlyResource) GetId() string              { return m.id }
func (m *ReplaceOnlyResource) GetRequiredString() string  { return m.requiredString }
func (m *ReplaceOnlyResource) SetRequiredString(v string) { m.requiredString = v }
func (m *ReplaceOnlyResource) GetInteger() int64          { return m.integer }
func (m *ReplaceOnlyResource) SetInteger(v int64)         { m.integer = v }
func (m *ReplaceOnlyResource) GetBoolean() bool           { return m.boolean }
func (m *ReplaceOnlyResource) SetBoolean(v bool)          { m.boolean = v }

// PutUpdatedResource is the flat putUpdatedResource model.
type PutUpdatedResource struct {
	id             string
	requiredString string
	boolean        bool
}

// NewPutUpdatedResourceWithDefaults stands in for the generated constructor.
func NewPutUpdatedResourceWithDefaults() *PutUpdatedResource { return &PutUpdatedResource{} }

func (m *PutUpdatedResource) GetId() string              { return m.id }
func (m *PutUpdatedResource) GetRequiredString() string  { return m.requiredString }
func (m *PutUpdatedResource) SetRequiredString(v string) { m.requiredString = v }
func (m *PutUpdatedResource) GetBoolean() bool           { return m.boolean }
func (m *PutUpdatedResource) SetBoolean(v bool)          { m.boolean = v }

// KeyAddressedDatasource is the flat keyAddressedDatasource model.
type KeyAddressedDatasource struct {
	id                         string
	keyAddressedDatasourceCode string
	plainString                string
	integer                    int64
}

// NewKeyAddressedDatasourceWithDefaults stands in for the generated constructor.
func NewKeyAddressedDatasourceWithDefaults() *KeyAddressedDatasource {
	return &KeyAddressedDatasource{}
}

func (m *KeyAddressedDatasource) GetId() string { return m.id }
func (m *KeyAddressedDatasource) GetKeyAddressedDatasourceCode() string {
	return m.keyAddressedDatasourceCode
}
func (m *KeyAddressedDatasource) GetPlainString() string { return m.plainString }
func (m *KeyAddressedDatasource) GetInteger() int64      { return m.integer }

// ListOnlyDatasource is the flat listOnlyDatasource model.
type ListOnlyDatasource struct {
	id           string
	firstString  string
	secondString string
}

// NewListOnlyDatasourceWithDefaults stands in for the generated constructor.
func NewListOnlyDatasourceWithDefaults() *ListOnlyDatasource { return &ListOnlyDatasource{} }

func (m *ListOnlyDatasource) GetId() string           { return m.id }
func (m *ListOnlyDatasource) GetFirstString() string  { return m.firstString }
func (m *ListOnlyDatasource) GetSecondString() string { return m.secondString }
