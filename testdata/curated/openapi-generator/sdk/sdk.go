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
	ModulesAPI  *ModulesAPIService
	BeaconsAPI  *BeaconsAPIService
	DocksAPI    *DocksAPIService
	PermitsAPI  *PermitsAPIService
	TransitsAPI *TransitsAPIService
}

// NewAPIClient stands in for the generated constructor.
func NewAPIClient(configuration *Configuration) *APIClient {
	return &APIClient{
		configuration: configuration,
		ModulesAPI:    &ModulesAPIService{},
		BeaconsAPI:    &BeaconsAPIService{},
		DocksAPI:      &DocksAPIService{},
		PermitsAPI:    &PermitsAPIService{},
		TransitsAPI:   &TransitsAPIService{},
	}
}

// ModulesAPIService is the generated module service.
type ModulesAPIService struct{}

// ApiCreateModuleRequest is the create request builder.
type ApiCreateModuleRequest struct{ module *Module }

// Module sets the request body.
func (r ApiCreateModuleRequest) Module(v Module) ApiCreateModuleRequest { r.module = &v; return r }

// Execute runs the request.
func (r ApiCreateModuleRequest) Execute() (*Module, *http.Response, error) {
	return nil, nil, errStub
}

// CreateModule begins a create.
func (s *ModulesAPIService) CreateModule(_ context.Context) ApiCreateModuleRequest {
	return ApiCreateModuleRequest{}
}

// ApiListModulesRequest is the list request builder.
type ApiListModulesRequest struct{}

// Execute runs the request.
func (r ApiListModulesRequest) Execute() ([]Module, *http.Response, error) {
	return nil, nil, errStub
}

// ListModules begins a list.
func (s *ModulesAPIService) ListModules(_ context.Context) ApiListModulesRequest {
	return ApiListModulesRequest{}
}

// ApiGetModuleRequest is the read request builder.
type ApiGetModuleRequest struct{}

// Execute runs the request.
func (r ApiGetModuleRequest) Execute() (*Module, *http.Response, error) { return nil, nil, errStub }

// GetModule begins a read.
func (s *ModulesAPIService) GetModule(_ context.Context, _ string) ApiGetModuleRequest {
	return ApiGetModuleRequest{}
}

// ApiUpdateModuleRequest is the update request builder.
type ApiUpdateModuleRequest struct{ module *Module }

// Module sets the request body.
func (r ApiUpdateModuleRequest) Module(v Module) ApiUpdateModuleRequest { r.module = &v; return r }

// Execute runs the request.
func (r ApiUpdateModuleRequest) Execute() (*Module, *http.Response, error) {
	return nil, nil, errStub
}

// UpdateModule begins an update.
func (s *ModulesAPIService) UpdateModule(_ context.Context, _ string) ApiUpdateModuleRequest {
	return ApiUpdateModuleRequest{}
}

// ApiDeleteModuleRequest is the delete request builder.
type ApiDeleteModuleRequest struct{}

// Execute runs the request.
func (r ApiDeleteModuleRequest) Execute() (*http.Response, error) { return nil, errStub }

// DeleteModule begins a delete.
func (s *ModulesAPIService) DeleteModule(_ context.Context, _ string) ApiDeleteModuleRequest {
	return ApiDeleteModuleRequest{}
}

// ApiRebootModuleRequest is the invocation request builder.
type ApiRebootModuleRequest struct{ body *RebootRequest }

// RebootRequest sets the request body.
func (r ApiRebootModuleRequest) RebootRequest(v RebootRequest) ApiRebootModuleRequest {
	r.body = &v
	return r
}

// Execute runs the request.
func (r ApiRebootModuleRequest) Execute() (*http.Response, error) { return nil, errStub }

// RebootModule begins an invocation.
func (s *ModulesAPIService) RebootModule(_ context.Context, _ string) ApiRebootModuleRequest {
	return ApiRebootModuleRequest{}
}

// BeaconsAPIService is the generated beacon service.
type BeaconsAPIService struct{}

// ApiCreateBeaconRequest is the create request builder.
type ApiCreateBeaconRequest struct{ beacon *Beacon }

// Beacon sets the request body.
func (r ApiCreateBeaconRequest) Beacon(v Beacon) ApiCreateBeaconRequest { r.beacon = &v; return r }

// Execute runs the request.
func (r ApiCreateBeaconRequest) Execute() (*Beacon, *http.Response, error) {
	return nil, nil, errStub
}

// CreateBeacon begins a create.
func (s *BeaconsAPIService) CreateBeacon(_ context.Context) ApiCreateBeaconRequest {
	return ApiCreateBeaconRequest{}
}

// ApiListBeaconsRequest is the list request builder.
type ApiListBeaconsRequest struct{}

// Execute runs the request.
func (r ApiListBeaconsRequest) Execute() ([]Beacon, *http.Response, error) {
	return nil, nil, errStub
}

// ListBeacons begins a list.
func (s *BeaconsAPIService) ListBeacons(_ context.Context) ApiListBeaconsRequest {
	return ApiListBeaconsRequest{}
}

// ApiGetBeaconRequest is the read request builder.
type ApiGetBeaconRequest struct{}

// Execute runs the request.
func (r ApiGetBeaconRequest) Execute() (*Beacon, *http.Response, error) { return nil, nil, errStub }

// GetBeacon begins a read.
func (s *BeaconsAPIService) GetBeacon(_ context.Context, _ string) ApiGetBeaconRequest {
	return ApiGetBeaconRequest{}
}

// ApiDeleteBeaconRequest is the delete request builder.
type ApiDeleteBeaconRequest struct{}

// Execute runs the request.
func (r ApiDeleteBeaconRequest) Execute() (*http.Response, error) { return nil, errStub }

// DeleteBeacon begins a delete.
func (s *BeaconsAPIService) DeleteBeacon(_ context.Context, _ string) ApiDeleteBeaconRequest {
	return ApiDeleteBeaconRequest{}
}

// DocksAPIService is the generated dock service.
type DocksAPIService struct{}

// ApiCreateDockRequest is the create request builder.
type ApiCreateDockRequest struct{ dock *Dock }

// Dock sets the request body.
func (r ApiCreateDockRequest) Dock(v Dock) ApiCreateDockRequest { r.dock = &v; return r }

// Execute runs the request.
func (r ApiCreateDockRequest) Execute() (*Dock, *http.Response, error) { return nil, nil, errStub }

// CreateDock begins a create.
func (s *DocksAPIService) CreateDock(_ context.Context) ApiCreateDockRequest {
	return ApiCreateDockRequest{}
}

// ApiListDocksRequest is the list request builder.
type ApiListDocksRequest struct{}

// Execute runs the request.
func (r ApiListDocksRequest) Execute() ([]Dock, *http.Response, error) { return nil, nil, errStub }

// ListDocks begins a list.
func (s *DocksAPIService) ListDocks(_ context.Context) ApiListDocksRequest {
	return ApiListDocksRequest{}
}

// ApiGetDockRequest is the read request builder.
type ApiGetDockRequest struct{}

// Execute runs the request.
func (r ApiGetDockRequest) Execute() (*Dock, *http.Response, error) { return nil, nil, errStub }

// GetDock begins a read.
func (s *DocksAPIService) GetDock(_ context.Context, _ string) ApiGetDockRequest {
	return ApiGetDockRequest{}
}

// ApiReplaceDockRequest is the replace request builder.
type ApiReplaceDockRequest struct{ dock *Dock }

// Dock sets the request body.
func (r ApiReplaceDockRequest) Dock(v Dock) ApiReplaceDockRequest { r.dock = &v; return r }

// Execute runs the request.
func (r ApiReplaceDockRequest) Execute() (*Dock, *http.Response, error) { return nil, nil, errStub }

// ReplaceDock begins a replace.
func (s *DocksAPIService) ReplaceDock(_ context.Context, _ string) ApiReplaceDockRequest {
	return ApiReplaceDockRequest{}
}

// ApiDeleteDockRequest is the delete request builder.
type ApiDeleteDockRequest struct{}

// Execute runs the request.
func (r ApiDeleteDockRequest) Execute() (*http.Response, error) { return nil, errStub }

// DeleteDock begins a delete.
func (s *DocksAPIService) DeleteDock(_ context.Context, _ string) ApiDeleteDockRequest {
	return ApiDeleteDockRequest{}
}

// PermitsAPIService is the generated permit service.
type PermitsAPIService struct{}

// ApiGetPermitRequest is the read request builder.
type ApiGetPermitRequest struct{}

// Execute runs the request.
func (r ApiGetPermitRequest) Execute() (*Permit, *http.Response, error) { return nil, nil, errStub }

// GetPermit begins a read.
func (s *PermitsAPIService) GetPermit(_ context.Context, _ string) ApiGetPermitRequest {
	return ApiGetPermitRequest{}
}

// TransitsAPIService is the generated transit service.
type TransitsAPIService struct{}

// ApiListTransitsRequest is the list request builder.
type ApiListTransitsRequest struct{}

// Execute runs the request.
func (r ApiListTransitsRequest) Execute() ([]Transit, *http.Response, error) {
	return nil, nil, errStub
}

// ListTransits begins a list.
func (s *TransitsAPIService) ListTransits(_ context.Context) ApiListTransitsRequest {
	return ApiListTransitsRequest{}
}

// ModuleKind is the generated string enumeration.
type ModuleKind string

// NewModuleKindFromValue parses the wire spelling, generator-shaped.
func NewModuleKindFromValue(v string) (*ModuleKind, error) {
	k := ModuleKind(v)
	return &k, nil
}

// Module is the flat model; getters deref, setters take values.
type Module struct {
	id         string
	name       string
	kind       ModuleKind
	serial     string
	occupied   bool
	berthCount int32
	massRatio  float64
	tags       []string
	berth      ModuleBerth
	hatches    []ModuleHatch
	status     string
}

// NewModuleWithDefaults stands in for the generated constructor.
func NewModuleWithDefaults() *Module { return &Module{} }

func (m *Module) GetId() string              { return m.id }
func (m *Module) GetName() string            { return m.name }
func (m *Module) SetName(v string)           { m.name = v }
func (m *Module) GetKind() ModuleKind        { return m.kind }
func (m *Module) SetKind(v ModuleKind)       { m.kind = v }
func (m *Module) GetSerial() string          { return m.serial }
func (m *Module) SetSerial(v string)         { m.serial = v }
func (m *Module) GetOccupied() bool          { return m.occupied }
func (m *Module) SetOccupied(v bool)         { m.occupied = v }
func (m *Module) GetBerthCount() int32       { return m.berthCount }
func (m *Module) SetBerthCount(v int32)      { m.berthCount = v }
func (m *Module) GetMassRatio() float64      { return m.massRatio }
func (m *Module) SetMassRatio(v float64)     { m.massRatio = v }
func (m *Module) GetTags() []string          { return m.tags }
func (m *Module) SetTags(v []string)         { m.tags = v }
func (m *Module) GetBerth() ModuleBerth      { return m.berth }
func (m *Module) SetBerth(v ModuleBerth)     { m.berth = v }
func (m *Module) GetHatches() []ModuleHatch  { return m.hatches }
func (m *Module) SetHatches(v []ModuleHatch) { m.hatches = v }
func (m *Module) GetStatus() string          { return m.status }

// ModuleBerth is the value-typed nested model.
type ModuleBerth struct {
	ring int64
	spin bool
}

// NewModuleBerthWithDefaults stands in for the generated constructor.
func NewModuleBerthWithDefaults() *ModuleBerth { return &ModuleBerth{} }

func (m *ModuleBerth) GetRing() int64  { return m.ring }
func (m *ModuleBerth) SetRing(v int64) { m.ring = v }
func (m *ModuleBerth) GetSpin() bool   { return m.spin }
func (m *ModuleBerth) SetSpin(v bool)  { m.spin = v }

// ModuleHatch is the value-typed list element.
type ModuleHatch struct {
	label string
	width int64
}

// NewModuleHatchWithDefaults stands in for the generated constructor.
func NewModuleHatchWithDefaults() *ModuleHatch { return &ModuleHatch{} }

func (m *ModuleHatch) GetLabel() string  { return m.label }
func (m *ModuleHatch) SetLabel(v string) { m.label = v }
func (m *ModuleHatch) GetWidth() int64   { return m.width }
func (m *ModuleHatch) SetWidth(v int64)  { m.width = v }

// RebootRequest is the invocation body.
type RebootRequest struct {
	mode  string
	force bool
}

// NewRebootRequestWithDefaults stands in for the generated constructor.
func NewRebootRequestWithDefaults() *RebootRequest { return &RebootRequest{} }

// SetMode writes the mode.
func (m *RebootRequest) SetMode(v string) { m.mode = v }

// SetForce writes the flag.
func (m *RebootRequest) SetForce(v bool) { m.force = v }

// Beacon is the flat beacon model.
type Beacon struct {
	id       string
	callsign string
	rangeKm  int64
	active   bool
}

// NewBeaconWithDefaults stands in for the generated constructor.
func NewBeaconWithDefaults() *Beacon { return &Beacon{} }

func (m *Beacon) GetId() string        { return m.id }
func (m *Beacon) GetCallsign() string  { return m.callsign }
func (m *Beacon) SetCallsign(v string) { m.callsign = v }
func (m *Beacon) GetRangeKm() int64    { return m.rangeKm }
func (m *Beacon) SetRangeKm(v int64)   { m.rangeKm = v }
func (m *Beacon) GetActive() bool      { return m.active }
func (m *Beacon) SetActive(v bool)     { m.active = v }

// Dock is the flat dock model.
type Dock struct {
	id       string
	label    string
	shielded bool
}

// NewDockWithDefaults stands in for the generated constructor.
func NewDockWithDefaults() *Dock { return &Dock{} }

func (m *Dock) GetId() string      { return m.id }
func (m *Dock) GetLabel() string   { return m.label }
func (m *Dock) SetLabel(v string)  { m.label = v }
func (m *Dock) GetShielded() bool  { return m.shielded }
func (m *Dock) SetShielded(v bool) { m.shielded = v }

// Permit is the flat permit model.
type Permit struct {
	id         string
	permitCode string
	holder     string
	seats      int64
}

// NewPermitWithDefaults stands in for the generated constructor.
func NewPermitWithDefaults() *Permit { return &Permit{} }

func (m *Permit) GetId() string         { return m.id }
func (m *Permit) GetPermitCode() string { return m.permitCode }
func (m *Permit) GetHolder() string     { return m.holder }
func (m *Permit) GetSeats() int64       { return m.seats }

// Transit is the flat transit model.
type Transit struct {
	id     string
	window string
	craft  string
}

// NewTransitWithDefaults stands in for the generated constructor.
func NewTransitWithDefaults() *Transit { return &Transit{} }

func (m *Transit) GetId() string     { return m.id }
func (m *Transit) GetWindow() string { return m.window }
func (m *Transit) GetCraft() string  { return m.craft }
