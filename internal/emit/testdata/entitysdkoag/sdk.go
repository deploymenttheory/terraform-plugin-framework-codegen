// A hand-written stand-in for an openapi-generator SDK, exposing exactly
// the surface the oag fixture bindings name: the configuration and client
// the provider core touches, one flat service struct with Execute-style
// request builders, and value-typed models with WithDefaults
// constructors.
package sdk

import (
	"context"
	"errors"
	"net/http"
	"time"
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

	// TagsAPI is the flat service struct the bindings reach through.
	TagsAPI *TagsAPIService
}

// NewAPIClient stands in for the generated constructor.
func NewAPIClient(configuration *Configuration) *APIClient {
	c := &APIClient{configuration: configuration}
	c.TagsAPI = &TagsAPIService{}
	return c
}

// TagsAPIService is the generated service struct.
type TagsAPIService struct{}

// ApiCreateTagRequest is the create request builder.
type ApiCreateTagRequest struct{ tag *Tag }

// Tag sets the request body.
func (r ApiCreateTagRequest) Tag(tag Tag) ApiCreateTagRequest { r.tag = &tag; return r }

// Execute runs the request.
func (r ApiCreateTagRequest) Execute() (*Tag, *http.Response, error) { return nil, nil, errStub }

// CreateTag begins a create.
func (s *TagsAPIService) CreateTag(_ context.Context) ApiCreateTagRequest {
	return ApiCreateTagRequest{}
}

// ApiGetTagRequest is the read request builder.
type ApiGetTagRequest struct{}

// Execute runs the request.
func (r ApiGetTagRequest) Execute() (*Tag, *http.Response, error) { return nil, nil, errStub }

// GetTag begins a read.
func (s *TagsAPIService) GetTag(_ context.Context, _ string) ApiGetTagRequest {
	return ApiGetTagRequest{}
}

// ApiUpdateTagRequest is the update request builder.
type ApiUpdateTagRequest struct{ tag *Tag }

// Tag sets the request body.
func (r ApiUpdateTagRequest) Tag(tag Tag) ApiUpdateTagRequest { r.tag = &tag; return r }

// Execute runs the request.
func (r ApiUpdateTagRequest) Execute() (*Tag, *http.Response, error) { return nil, nil, errStub }

// UpdateTag begins an update.
func (s *TagsAPIService) UpdateTag(_ context.Context, _ string) ApiUpdateTagRequest {
	return ApiUpdateTagRequest{}
}

// ApiDeleteTagRequest is the delete request builder.
type ApiDeleteTagRequest struct{}

// Execute runs the request.
func (r ApiDeleteTagRequest) Execute() (*http.Response, error) { return nil, errStub }

// DeleteTag begins a delete.
func (s *TagsAPIService) DeleteTag(_ context.Context, _ string) ApiDeleteTagRequest {
	return ApiDeleteTagRequest{}
}

// ApiRotateTagRequest is the invoke request builder.
type ApiRotateTagRequest struct{ body *RotateRequest }

// RotateRequest sets the request body.
func (r ApiRotateTagRequest) RotateRequest(body RotateRequest) ApiRotateTagRequest {
	r.body = &body
	return r
}

// Execute runs the request.
func (r ApiRotateTagRequest) Execute() (*http.Response, error) { return nil, errStub }

// RotateTag begins an invocation.
func (s *TagsAPIService) RotateTag(_ context.Context, _ int64) ApiRotateTagRequest {
	return ApiRotateTagRequest{}
}

// ApiListTagsRequest is the list request builder.
type ApiListTagsRequest struct{}

// Execute runs the request, answering a wrapped envelope.
func (r ApiListTagsRequest) Execute() (*TagList, *http.Response, error) { return nil, nil, errStub }

// ListTags begins a collection read.
func (s *TagsAPIService) ListTags(_ context.Context) ApiListTagsRequest {
	return ApiListTagsRequest{}
}

// TagList is the openapi-generator wrapped-list envelope: a struct whose
// single exported slice field is named for the "tags" wire key. The
// generator emits no getter on the envelope, so the collection is reached
// through the field directly.
type TagList struct {
	Tags []Tag `json:"tags"`
}

// TagStatus is a generated string enumeration.
type TagStatus string

// NewTagStatusFromValue parses the wire spelling, generator-shaped.
func NewTagStatusFromValue(v string) (*TagStatus, error) {
	s := TagStatus(v)
	return &s, nil
}

// TagPhase is a second generated string enumeration.
type TagPhase string

// NewTagPhaseFromValue parses the wire spelling, generator-shaped.
func NewTagPhaseFromValue(v string) (*TagPhase, error) {
	p := TagPhase(v)
	return &p, nil
}

// Mode is a slice-carried string enumeration.
type Mode string

// NewModeFromValue parses the wire spelling, generator-shaped.
func NewModeFromValue(v string) (*Mode, error) {
	m := Mode(v)
	return &m, nil
}

// Tag is the flat model; getters deref, setters take values, the
// generator's way.
type Tag struct {
	id      string
	name    string
	enabled bool
	count   int32
	big     int64
	score   float32
	weight  float64
	status  TagStatus
	phase   *TagPhase
	when    time.Time
	levels  []int64
	flags   []bool
	ratios  []float64
	sizes   []int32
	modes   []Mode
	meta    TagMeta
	rules   []TagRule
}

// NewTagWithDefaults stands in for the generated constructor.
func NewTagWithDefaults() *Tag { return &Tag{} }

func (t *Tag) GetId() string         { return t.id }
func (t *Tag) SetId(v string)        { t.id = v }
func (t *Tag) GetName() string       { return t.name }
func (t *Tag) SetName(v string)      { t.name = v }
func (t *Tag) GetEnabled() bool      { return t.enabled }
func (t *Tag) SetEnabled(v bool)     { t.enabled = v }
func (t *Tag) GetCount() int32       { return t.count }
func (t *Tag) SetCount(v int32)      { t.count = v }
func (t *Tag) GetBig() int64         { return t.big }
func (t *Tag) SetBig(v int64)        { t.big = v }
func (t *Tag) GetScore() float32     { return t.score }
func (t *Tag) SetScore(v float32)    { t.score = v }
func (t *Tag) GetWeight() float64    { return t.weight }
func (t *Tag) SetWeight(v float64)   { t.weight = v }
func (t *Tag) GetStatus() TagStatus  { return t.status }
func (t *Tag) SetStatus(v TagStatus) { t.status = v }
func (t *Tag) GetPhase() *TagPhase   { return t.phase }
func (t *Tag) SetPhase(v *TagPhase)  { t.phase = v }
func (t *Tag) GetWhen() time.Time    { return t.when }
func (t *Tag) SetWhen(v time.Time)   { t.when = v }
func (t *Tag) GetLevels() []int64    { return t.levels }
func (t *Tag) SetLevels(v []int64)   { t.levels = v }
func (t *Tag) GetFlags() []bool      { return t.flags }
func (t *Tag) SetFlags(v []bool)     { t.flags = v }
func (t *Tag) GetRatios() []float64  { return t.ratios }
func (t *Tag) SetRatios(v []float64) { t.ratios = v }
func (t *Tag) GetSizes() []int32     { return t.sizes }
func (t *Tag) SetSizes(v []int32)    { t.sizes = v }
func (t *Tag) GetModes() []Mode      { return t.modes }
func (t *Tag) SetModes(v []Mode)     { t.modes = v }
func (t *Tag) GetMeta() TagMeta      { return t.meta }
func (t *Tag) SetMeta(v TagMeta)     { t.meta = v }
func (t *Tag) GetRules() []TagRule   { return t.rules }
func (t *Tag) SetRules(v []TagRule)  { t.rules = v }

// TagMeta is the value-typed nested model.
type TagMeta struct {
	note string
}

// NewTagMetaWithDefaults stands in for the generated constructor.
func NewTagMetaWithDefaults() *TagMeta { return &TagMeta{} }

func (m *TagMeta) GetNote() string  { return m.note }
func (m *TagMeta) SetNote(v string) { m.note = v }

// TagRule is the concrete list element.
type TagRule struct {
	pattern string
}

// NewTagRuleWithDefaults stands in for the generated constructor.
func NewTagRuleWithDefaults() *TagRule { return &TagRule{} }

func (m *TagRule) GetPattern() string  { return m.pattern }
func (m *TagRule) SetPattern(v string) { m.pattern = v }

// RotateRequest is the invocation body.
type RotateRequest struct {
	force  bool
	factor float64
	labels []string
	window RotateWindow
	steps  []RotateStep
}

// NewRotateRequestWithDefaults stands in for the generated constructor.
func NewRotateRequestWithDefaults() *RotateRequest { return &RotateRequest{} }

func (m *RotateRequest) SetForce(v bool)          { m.force = v }
func (m *RotateRequest) SetFactor(v float64)      { m.factor = v }
func (m *RotateRequest) SetLabels(v []string)     { m.labels = v }
func (m *RotateRequest) SetWindow(v RotateWindow) { m.window = v }
func (m *RotateRequest) SetSteps(v []RotateStep)  { m.steps = v }

// RotateWindow is the invocation body's nested object.
type RotateWindow struct {
	hours int64
}

// NewRotateWindowWithDefaults stands in for the generated constructor.
func NewRotateWindowWithDefaults() *RotateWindow { return &RotateWindow{} }

// SetHours sets the window.
func (m *RotateWindow) SetHours(v int64) { m.hours = v }

// RotateStep is the invocation body's list element.
type RotateStep struct {
	order int64
}

// NewRotateStepWithDefaults stands in for the generated constructor.
func NewRotateStepWithDefaults() *RotateStep { return &RotateStep{} }

// SetOrder sets the position.
func (m *RotateStep) SetOrder(v int64) { m.order = v }
