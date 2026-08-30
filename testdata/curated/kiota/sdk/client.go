// A hand-written stand-in for the kiota-generated SDK the curated fixture
// binds against: fluent request builders hanging off a root client, typed
// indexers for path parameters, and verb methods taking (ctx, [body,]
// config). The method bodies never run — the fixture proves the generated
// tree binds and compiles against a real SDK shape, not that requests fly.
package sdk

import (
	"context"
	"errors"

	"github.com/example-org/terraform-provider-fixture/internal/sdk/models"
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

// PatchUpdatedResources reaches the /patchUpdatedResources collection.
func (c *APIClient) PatchUpdatedResources() *PatchUpdatedResourcesRequestBuilder {
	return &PatchUpdatedResourcesRequestBuilder{}
}

// ReplaceOnlyResources reaches the /replaceOnlyResources collection.
func (c *APIClient) ReplaceOnlyResources() *ReplaceOnlyResourcesRequestBuilder {
	return &ReplaceOnlyResourcesRequestBuilder{}
}

// PutUpdatedResources reaches the /putUpdatedResources collection.
func (c *APIClient) PutUpdatedResources() *PutUpdatedResourcesRequestBuilder {
	return &PutUpdatedResourcesRequestBuilder{}
}

// KeyAddressedDatasources reaches the /keyAddressedDatasources collection.
func (c *APIClient) KeyAddressedDatasources() *KeyAddressedDatasourcesRequestBuilder {
	return &KeyAddressedDatasourcesRequestBuilder{}
}

// ListOnlyDatasources reaches the /listOnlyDatasources collection.
func (c *APIClient) ListOnlyDatasources() *ListOnlyDatasourcesRequestBuilder {
	return &ListOnlyDatasourcesRequestBuilder{}
}

// PatchUpdatedResourcesRequestBuilder is the patchUpdatedResource collection builder.
type PatchUpdatedResourcesRequestBuilder struct{}

// Post creates one patchUpdatedResource.
func (b *PatchUpdatedResourcesRequestBuilder) Post(_ context.Context, body models.PatchUpdatedResourceable, _ any) (models.PatchUpdatedResourceable, error) {
	return body, errStub
}

// Get lists the collection.
func (b *PatchUpdatedResourcesRequestBuilder) Get(_ context.Context, _ any) (models.PatchUpdatedResourceCollectionResponseable, error) {
	return nil, errStub
}

// ByPatchUpdatedResourceId reaches one patchUpdatedResource.
func (b *PatchUpdatedResourcesRequestBuilder) ByPatchUpdatedResourceId(_ string) *PatchUpdatedResourceItemRequestBuilder {
	return &PatchUpdatedResourceItemRequestBuilder{}
}

// PatchUpdatedResourceItemRequestBuilder is the patchUpdatedResource item builder.
type PatchUpdatedResourceItemRequestBuilder struct{}

// Get reads the patchUpdatedResource.
func (b *PatchUpdatedResourceItemRequestBuilder) Get(_ context.Context, _ any) (models.PatchUpdatedResourceable, error) {
	return nil, errStub
}

// Patch updates the patchUpdatedResource.
func (b *PatchUpdatedResourceItemRequestBuilder) Patch(_ context.Context, body models.PatchUpdatedResourceable, _ any) (models.PatchUpdatedResourceable, error) {
	return body, errStub
}

// Delete removes the patchUpdatedResource.
func (b *PatchUpdatedResourceItemRequestBuilder) Delete(_ context.Context, _ any) error {
	return errStub
}

// CustomAction reaches the customAction invocation.
func (b *PatchUpdatedResourceItemRequestBuilder) CustomAction() *PatchUpdatedResourceCustomActionRequestBuilder {
	return &PatchUpdatedResourceCustomActionRequestBuilder{}
}

// PatchUpdatedResourceCustomActionRequestBuilder is the customAction invocation builder.
type PatchUpdatedResourceCustomActionRequestBuilder struct{}

// Post invokes the customAction.
func (b *PatchUpdatedResourceCustomActionRequestBuilder) Post(_ context.Context, _ models.CustomActionRequestable, _ any) error {
	return errStub
}

// ReplaceOnlyResourcesRequestBuilder is the replaceOnlyResource collection builder.
type ReplaceOnlyResourcesRequestBuilder struct{}

// Post creates one replaceOnlyResource.
func (b *ReplaceOnlyResourcesRequestBuilder) Post(_ context.Context, body models.ReplaceOnlyResourceable, _ any) (models.ReplaceOnlyResourceable, error) {
	return body, errStub
}

// Get lists the collection.
func (b *ReplaceOnlyResourcesRequestBuilder) Get(_ context.Context, _ any) (models.ReplaceOnlyResourceCollectionResponseable, error) {
	return nil, errStub
}

// ByReplaceOnlyResourceId reaches one replaceOnlyResource.
func (b *ReplaceOnlyResourcesRequestBuilder) ByReplaceOnlyResourceId(_ string) *ReplaceOnlyResourceItemRequestBuilder {
	return &ReplaceOnlyResourceItemRequestBuilder{}
}

// ReplaceOnlyResourceItemRequestBuilder is the replaceOnlyResource item builder.
type ReplaceOnlyResourceItemRequestBuilder struct{}

// Get reads the replaceOnlyResource.
func (b *ReplaceOnlyResourceItemRequestBuilder) Get(_ context.Context, _ any) (models.ReplaceOnlyResourceable, error) {
	return nil, errStub
}

// Delete removes the replaceOnlyResource.
func (b *ReplaceOnlyResourceItemRequestBuilder) Delete(_ context.Context, _ any) error {
	return errStub
}

// PutUpdatedResourcesRequestBuilder is the putUpdatedResource collection builder.
type PutUpdatedResourcesRequestBuilder struct{}

// Post creates one putUpdatedResource.
func (b *PutUpdatedResourcesRequestBuilder) Post(_ context.Context, body models.PutUpdatedResourceable, _ any) (models.PutUpdatedResourceable, error) {
	return body, errStub
}

// Get lists the collection.
func (b *PutUpdatedResourcesRequestBuilder) Get(_ context.Context, _ any) (models.PutUpdatedResourceCollectionResponseable, error) {
	return nil, errStub
}

// ByPutUpdatedResourceId reaches one putUpdatedResource.
func (b *PutUpdatedResourcesRequestBuilder) ByPutUpdatedResourceId(_ string) *PutUpdatedResourceItemRequestBuilder {
	return &PutUpdatedResourceItemRequestBuilder{}
}

// PutUpdatedResourceItemRequestBuilder is the putUpdatedResource item builder.
type PutUpdatedResourceItemRequestBuilder struct{}

// Get reads the putUpdatedResource.
func (b *PutUpdatedResourceItemRequestBuilder) Get(_ context.Context, _ any) (models.PutUpdatedResourceable, error) {
	return nil, errStub
}

// Put replaces the putUpdatedResource.
func (b *PutUpdatedResourceItemRequestBuilder) Put(_ context.Context, body models.PutUpdatedResourceable, _ any) (models.PutUpdatedResourceable, error) {
	return body, errStub
}

// Delete removes the putUpdatedResource.
func (b *PutUpdatedResourceItemRequestBuilder) Delete(_ context.Context, _ any) error { return errStub }

// KeyAddressedDatasourcesRequestBuilder is the keyAddressedDatasource collection builder.
type KeyAddressedDatasourcesRequestBuilder struct{}

// ByKeyAddressedDatasourceCode reaches one keyAddressedDatasource.
func (b *KeyAddressedDatasourcesRequestBuilder) ByKeyAddressedDatasourceCode(_ string) *KeyAddressedDatasourceItemRequestBuilder {
	return &KeyAddressedDatasourceItemRequestBuilder{}
}

// KeyAddressedDatasourceItemRequestBuilder is the keyAddressedDatasource item builder.
type KeyAddressedDatasourceItemRequestBuilder struct{}

// Get reads the keyAddressedDatasource.
func (b *KeyAddressedDatasourceItemRequestBuilder) Get(_ context.Context, _ any) (models.KeyAddressedDatasourceable, error) {
	return nil, errStub
}

// ListOnlyDatasourcesRequestBuilder is the listOnlyDatasource collection builder.
type ListOnlyDatasourcesRequestBuilder struct{}

// Get lists the collection.
func (b *ListOnlyDatasourcesRequestBuilder) Get(_ context.Context, _ any) (models.ListOnlyDatasourceCollectionResponseable, error) {
	return nil, errStub
}
