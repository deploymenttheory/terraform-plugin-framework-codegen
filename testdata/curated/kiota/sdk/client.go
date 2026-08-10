// A hand-written stand-in for the kiota-generated SDK the curated fixture
// binds against: fluent request builders hanging off a root client, typed
// indexers for path parameters, and verb methods taking (ctx, [body,]
// config). The method bodies never run — the fixture proves the generated
// tree binds and compiles against a real SDK shape, not that requests fly.
package sdk

import (
	"context"
	"errors"

	"github.com/example-org/terraform-provider-orbital/internal/sdk/models"
)

// errStub is what every stub call answers; nothing ever invokes one.
var errStub = errors.New("the stub SDK carries shape, not behaviour")

// APIClient stands in for the generated fluent root.
type APIClient struct{}

// NewAPIClient stands in for the generated constructor. The adapter
// parameter is untyped so the stub needs no kiota runtime of its own; the
// provider core hands it the real request adapter.
func NewAPIClient(adapter any) *APIClient {
	_ = adapter
	return &APIClient{}
}

// Modules reaches the /modules collection.
func (c *APIClient) Modules() *ModulesRequestBuilder { return &ModulesRequestBuilder{} }

// Beacons reaches the /beacons collection.
func (c *APIClient) Beacons() *BeaconsRequestBuilder { return &BeaconsRequestBuilder{} }

// Docks reaches the /docks collection.
func (c *APIClient) Docks() *DocksRequestBuilder { return &DocksRequestBuilder{} }

// Permits reaches the /permits collection.
func (c *APIClient) Permits() *PermitsRequestBuilder { return &PermitsRequestBuilder{} }

// Transits reaches the /transits collection.
func (c *APIClient) Transits() *TransitsRequestBuilder { return &TransitsRequestBuilder{} }

// ModulesRequestBuilder is the module collection builder.
type ModulesRequestBuilder struct{}

// Post creates one module.
func (b *ModulesRequestBuilder) Post(_ context.Context, body models.Moduleable, _ any) (models.Moduleable, error) {
	return body, errStub
}

// Get lists the collection.
func (b *ModulesRequestBuilder) Get(_ context.Context, _ any) (models.ModuleCollectionResponseable, error) {
	return nil, errStub
}

// ByModuleId reaches one module.
func (b *ModulesRequestBuilder) ByModuleId(_ string) *ModuleItemRequestBuilder {
	return &ModuleItemRequestBuilder{}
}

// ModuleItemRequestBuilder is the module item builder.
type ModuleItemRequestBuilder struct{}

// Get reads the module.
func (b *ModuleItemRequestBuilder) Get(_ context.Context, _ any) (models.Moduleable, error) {
	return nil, errStub
}

// Patch updates the module.
func (b *ModuleItemRequestBuilder) Patch(_ context.Context, body models.Moduleable, _ any) (models.Moduleable, error) {
	return body, errStub
}

// Delete removes the module.
func (b *ModuleItemRequestBuilder) Delete(_ context.Context, _ any) error { return errStub }

// Reboot reaches the reboot invocation.
func (b *ModuleItemRequestBuilder) Reboot() *ModuleRebootRequestBuilder {
	return &ModuleRebootRequestBuilder{}
}

// ModuleRebootRequestBuilder is the reboot invocation builder.
type ModuleRebootRequestBuilder struct{}

// Post invokes the reboot.
func (b *ModuleRebootRequestBuilder) Post(_ context.Context, _ models.RebootRequestable, _ any) error {
	return errStub
}

// BeaconsRequestBuilder is the beacon collection builder.
type BeaconsRequestBuilder struct{}

// Post creates one beacon.
func (b *BeaconsRequestBuilder) Post(_ context.Context, body models.Beaconable, _ any) (models.Beaconable, error) {
	return body, errStub
}

// Get lists the collection.
func (b *BeaconsRequestBuilder) Get(_ context.Context, _ any) (models.BeaconCollectionResponseable, error) {
	return nil, errStub
}

// ByBeaconId reaches one beacon.
func (b *BeaconsRequestBuilder) ByBeaconId(_ string) *BeaconItemRequestBuilder {
	return &BeaconItemRequestBuilder{}
}

// BeaconItemRequestBuilder is the beacon item builder.
type BeaconItemRequestBuilder struct{}

// Get reads the beacon.
func (b *BeaconItemRequestBuilder) Get(_ context.Context, _ any) (models.Beaconable, error) {
	return nil, errStub
}

// Delete removes the beacon.
func (b *BeaconItemRequestBuilder) Delete(_ context.Context, _ any) error { return errStub }

// DocksRequestBuilder is the dock collection builder.
type DocksRequestBuilder struct{}

// Post creates one dock.
func (b *DocksRequestBuilder) Post(_ context.Context, body models.Dockable, _ any) (models.Dockable, error) {
	return body, errStub
}

// Get lists the collection.
func (b *DocksRequestBuilder) Get(_ context.Context, _ any) (models.DockCollectionResponseable, error) {
	return nil, errStub
}

// ByDockId reaches one dock.
func (b *DocksRequestBuilder) ByDockId(_ string) *DockItemRequestBuilder {
	return &DockItemRequestBuilder{}
}

// DockItemRequestBuilder is the dock item builder.
type DockItemRequestBuilder struct{}

// Get reads the dock.
func (b *DockItemRequestBuilder) Get(_ context.Context, _ any) (models.Dockable, error) {
	return nil, errStub
}

// Put replaces the dock.
func (b *DockItemRequestBuilder) Put(_ context.Context, body models.Dockable, _ any) (models.Dockable, error) {
	return body, errStub
}

// Delete removes the dock.
func (b *DockItemRequestBuilder) Delete(_ context.Context, _ any) error { return errStub }

// PermitsRequestBuilder is the permit collection builder.
type PermitsRequestBuilder struct{}

// ByPermitCode reaches one permit.
func (b *PermitsRequestBuilder) ByPermitCode(_ string) *PermitItemRequestBuilder {
	return &PermitItemRequestBuilder{}
}

// PermitItemRequestBuilder is the permit item builder.
type PermitItemRequestBuilder struct{}

// Get reads the permit.
func (b *PermitItemRequestBuilder) Get(_ context.Context, _ any) (models.Permitable, error) {
	return nil, errStub
}

// TransitsRequestBuilder is the transit collection builder.
type TransitsRequestBuilder struct{}

// Get lists the collection.
func (b *TransitsRequestBuilder) Get(_ context.Context, _ any) (models.TransitCollectionResponseable, error) {
	return nil, errStub
}
