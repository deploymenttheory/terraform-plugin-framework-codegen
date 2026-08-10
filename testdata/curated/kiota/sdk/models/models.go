// A hand-written stand-in for the kiota-generated models package: read-side
// interfaces the builders return, concrete structs with constructors for the
// write side, pointer-typed scalars behind Get/Set pairs, and one generated
// enumeration with its Parse companion.
package models

import "errors"

// ModuleKind is the generated enumeration for the module kind.
type ModuleKind int

// Enumeration members.
const (
	HABITAT_MODULEKIND ModuleKind = iota
	LAB_MODULEKIND
)

// String spells the member the way the API does.
func (e ModuleKind) String() string {
	return [...]string{"habitat", "lab"}[e]
}

// ParseModuleKind parses the wire spelling, kiota-shaped.
func ParseModuleKind(v string) (any, error) {
	result := HABITAT_MODULEKIND
	switch v {
	case "habitat":
		result = HABITAT_MODULEKIND
	case "lab":
		result = LAB_MODULEKIND
	default:
		return nil, errors.New("unknown ModuleKind value: " + v)
	}
	return &result, nil
}

// Moduleable is the read-side module interface.
type Moduleable interface {
	GetId() *string
	GetName() *string
	GetKind() *ModuleKind
	GetSerial() *string
	GetOccupied() *bool
	GetBerthCount() *int32
	GetMassRatio() *float64
	GetTags() []string
	GetBerth() ModuleBerthable
	GetHatches() []ModuleHatchable
	GetStatus() *string
}

// Module is the concrete model the constructor yields.
type Module struct {
	id         *string
	name       *string
	kind       *ModuleKind
	serial     *string
	occupied   *bool
	berthCount *int32
	massRatio  *float64
	tags       []string
	berth      ModuleBerthable
	hatches    []ModuleHatchable
	status     *string
}

// NewModule constructs a settable Module.
func NewModule() *Module { return &Module{} }

// GetId reads the identifier.
func (m *Module) GetId() *string { return m.id }

// GetName reads the name.
func (m *Module) GetName() *string { return m.name }

// SetName writes the name.
func (m *Module) SetName(v *string) { m.name = v }

// GetKind reads the enumeration.
func (m *Module) GetKind() *ModuleKind { return m.kind }

// SetKind writes the enumeration.
func (m *Module) SetKind(v *ModuleKind) { m.kind = v }

// GetSerial reads the create-only serial.
func (m *Module) GetSerial() *string { return m.serial }

// SetSerial writes the serial.
func (m *Module) SetSerial(v *string) { m.serial = v }

// GetOccupied reads the flag.
func (m *Module) GetOccupied() *bool { return m.occupied }

// SetOccupied writes the flag.
func (m *Module) SetOccupied(v *bool) { m.occupied = v }

// GetBerthCount reads a scalar the SDK carries narrower than the document.
func (m *Module) GetBerthCount() *int32 { return m.berthCount }

// SetBerthCount writes the narrow scalar.
func (m *Module) SetBerthCount(v *int32) { m.berthCount = v }

// GetMassRatio reads the ratio.
func (m *Module) GetMassRatio() *float64 { return m.massRatio }

// SetMassRatio writes the ratio.
func (m *Module) SetMassRatio(v *float64) { m.massRatio = v }

// GetTags reads the scalar slice.
func (m *Module) GetTags() []string { return m.tags }

// SetTags writes the scalar slice.
func (m *Module) SetTags(v []string) { m.tags = v }

// GetBerth reads the nested object.
func (m *Module) GetBerth() ModuleBerthable { return m.berth }

// SetBerth writes the nested object.
func (m *Module) SetBerth(v ModuleBerthable) { m.berth = v }

// GetHatches reads the nested list.
func (m *Module) GetHatches() []ModuleHatchable { return m.hatches }

// SetHatches writes the nested list.
func (m *Module) SetHatches(v []ModuleHatchable) { m.hatches = v }

// GetStatus reads the server's assessment.
func (m *Module) GetStatus() *string { return m.status }

// ModuleBerthable is the read-side berth interface.
type ModuleBerthable interface {
	GetRing() *int64
	GetSpin() *bool
}

// ModuleBerth is the concrete berth model.
type ModuleBerth struct {
	ring *int64
	spin *bool
}

// NewModuleBerth constructs a settable ModuleBerth.
func NewModuleBerth() *ModuleBerth { return &ModuleBerth{} }

// GetRing reads the ring.
func (m *ModuleBerth) GetRing() *int64 { return m.ring }

// SetRing writes the ring.
func (m *ModuleBerth) SetRing(v *int64) { m.ring = v }

// GetSpin reads the flag.
func (m *ModuleBerth) GetSpin() *bool { return m.spin }

// SetSpin writes the flag.
func (m *ModuleBerth) SetSpin(v *bool) { m.spin = v }

// ModuleHatchable is the read-side hatch interface.
type ModuleHatchable interface {
	GetLabel() *string
	GetWidth() *int64
}

// ModuleHatch is the concrete hatch model.
type ModuleHatch struct {
	label *string
	width *int64
}

// NewModuleHatch constructs a settable ModuleHatch.
func NewModuleHatch() *ModuleHatch { return &ModuleHatch{} }

// GetLabel reads the label.
func (m *ModuleHatch) GetLabel() *string { return m.label }

// SetLabel writes the label.
func (m *ModuleHatch) SetLabel(v *string) { m.label = v }

// GetWidth reads the width.
func (m *ModuleHatch) GetWidth() *int64 { return m.width }

// SetWidth writes the width.
func (m *ModuleHatch) SetWidth(v *int64) { m.width = v }

// ModuleCollectionResponseable is the read-side collection envelope.
type ModuleCollectionResponseable interface {
	GetValue() []Moduleable
}

// ModuleCollectionResponse is the concrete collection envelope.
type ModuleCollectionResponse struct {
	value []Moduleable
}

// GetValue reads the elements.
func (m *ModuleCollectionResponse) GetValue() []Moduleable { return m.value }

// SetValue writes the elements.
func (m *ModuleCollectionResponse) SetValue(v []Moduleable) { m.value = v }

// RebootRequestable is the read-side reboot body interface.
type RebootRequestable interface {
	GetMode() *string
	GetForce() *bool
}

// RebootRequest is the concrete reboot body.
type RebootRequest struct {
	mode  *string
	force *bool
}

// NewRebootRequest constructs a settable RebootRequest.
func NewRebootRequest() *RebootRequest { return &RebootRequest{} }

// GetMode reads the mode.
func (m *RebootRequest) GetMode() *string { return m.mode }

// SetMode writes the mode.
func (m *RebootRequest) SetMode(v *string) { m.mode = v }

// GetForce reads the flag.
func (m *RebootRequest) GetForce() *bool { return m.force }

// SetForce writes the flag.
func (m *RebootRequest) SetForce(v *bool) { m.force = v }

// Beaconable is the read-side beacon interface.
type Beaconable interface {
	GetId() *string
	GetCallsign() *string
	GetRangeKm() *int64
	GetActive() *bool
}

// Beacon is the concrete beacon model.
type Beacon struct {
	id       *string
	callsign *string
	rangeKm  *int64
	active   *bool
}

// NewBeacon constructs a settable Beacon.
func NewBeacon() *Beacon { return &Beacon{} }

// GetId reads the identifier.
func (m *Beacon) GetId() *string { return m.id }

// GetCallsign reads the callsign.
func (m *Beacon) GetCallsign() *string { return m.callsign }

// SetCallsign writes the callsign.
func (m *Beacon) SetCallsign(v *string) { m.callsign = v }

// GetRangeKm reads the range.
func (m *Beacon) GetRangeKm() *int64 { return m.rangeKm }

// SetRangeKm writes the range.
func (m *Beacon) SetRangeKm(v *int64) { m.rangeKm = v }

// GetActive reads the flag.
func (m *Beacon) GetActive() *bool { return m.active }

// SetActive writes the flag.
func (m *Beacon) SetActive(v *bool) { m.active = v }

// BeaconCollectionResponseable is the read-side collection envelope.
type BeaconCollectionResponseable interface {
	GetValue() []Beaconable
}

// BeaconCollectionResponse is the concrete collection envelope.
type BeaconCollectionResponse struct {
	value []Beaconable
}

// GetValue reads the elements.
func (m *BeaconCollectionResponse) GetValue() []Beaconable { return m.value }

// Dockable is the read-side dock interface.
type Dockable interface {
	GetId() *string
	GetLabel() *string
	GetShielded() *bool
}

// Dock is the concrete dock model.
type Dock struct {
	id       *string
	label    *string
	shielded *bool
}

// NewDock constructs a settable Dock.
func NewDock() *Dock { return &Dock{} }

// GetId reads the identifier.
func (m *Dock) GetId() *string { return m.id }

// GetLabel reads the label.
func (m *Dock) GetLabel() *string { return m.label }

// SetLabel writes the label.
func (m *Dock) SetLabel(v *string) { m.label = v }

// GetShielded reads the flag.
func (m *Dock) GetShielded() *bool { return m.shielded }

// SetShielded writes the flag.
func (m *Dock) SetShielded(v *bool) { m.shielded = v }

// DockCollectionResponseable is the read-side collection envelope.
type DockCollectionResponseable interface {
	GetValue() []Dockable
}

// DockCollectionResponse is the concrete collection envelope.
type DockCollectionResponse struct {
	value []Dockable
}

// GetValue reads the elements.
func (m *DockCollectionResponse) GetValue() []Dockable { return m.value }

// Permitable is the read-side permit interface.
type Permitable interface {
	GetId() *string
	GetPermitCode() *string
	GetHolder() *string
	GetSeats() *int64
}

// Permit is the concrete permit model.
type Permit struct {
	id         *string
	permitCode *string
	holder     *string
	seats      *int64
}

// GetId reads the identifier.
func (m *Permit) GetId() *string { return m.id }

// GetPermitCode reads the code.
func (m *Permit) GetPermitCode() *string { return m.permitCode }

// GetHolder reads the holder.
func (m *Permit) GetHolder() *string { return m.holder }

// GetSeats reads the seat count.
func (m *Permit) GetSeats() *int64 { return m.seats }

// Transitable is the read-side transit interface.
type Transitable interface {
	GetId() *string
	GetWindow() *string
	GetCraft() *string
}

// Transit is the concrete transit model.
type Transit struct {
	id     *string
	window *string
	craft  *string
}

// GetId reads the identifier.
func (m *Transit) GetId() *string { return m.id }

// GetWindow reads the window.
func (m *Transit) GetWindow() *string { return m.window }

// GetCraft reads the craft.
func (m *Transit) GetCraft() *string { return m.craft }

// TransitCollectionResponseable is the read-side collection envelope.
type TransitCollectionResponseable interface {
	GetValue() []Transitable
}

// TransitCollectionResponse is the concrete collection envelope.
type TransitCollectionResponse struct {
	value []Transitable
}

// GetValue reads the elements.
func (m *TransitCollectionResponse) GetValue() []Transitable { return m.value }
