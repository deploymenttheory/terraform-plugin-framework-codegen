// Package kiotasdk is a miniature kiota-shaped SDK: fluent request
// builders hanging off a root client, typed indexers for path parameters,
// and verb methods taking (ctx, [body,] config). It exists so the binding
// checks run against real type information without invoking a generator.
package kiotasdk

import (
	"context"

	"example.com/kiotasdk/abstractions"
	"example.com/kiotasdk/models"
)

// APIClient is the fluent root.
type APIClient struct{}

// NewAPIClient constructs the root client.
func NewAPIClient() *APIClient { return &APIClient{} }

// Tags reaches the /tags collection.
func (c *APIClient) Tags() *TagsRequestBuilder { return &TagsRequestBuilder{} }

// RequestConfiguration is the per-request options every verb accepts.
type RequestConfiguration struct{}

// TagsRequestBuilder is the collection builder.
type TagsRequestBuilder struct{}

// ByTagId indexes into one tag.
func (b *TagsRequestBuilder) ByTagId(tagId string) *TagsItemRequestBuilder {
	_ = tagId
	return &TagsItemRequestBuilder{}
}

// Post creates a tag.
func (b *TagsRequestBuilder) Post(ctx context.Context, body models.Tagable, cfg *RequestConfiguration) (models.Tagable, error) {
	_, _ = ctx, cfg
	return body, nil
}

// Get lists the collection.
func (b *TagsRequestBuilder) Get(ctx context.Context, cfg *RequestConfiguration) (models.TagCollectionResponseable, error) {
	_, _ = ctx, cfg
	return nil, nil
}

// TagsItemRequestBuilder is the item builder.
type TagsItemRequestBuilder struct{}

// Get reads one tag.
func (b *TagsItemRequestBuilder) Get(ctx context.Context, cfg *RequestConfiguration) (models.Tagable, error) {
	_, _ = ctx, cfg
	return nil, nil
}

// Patch updates one tag.
func (b *TagsItemRequestBuilder) Patch(ctx context.Context, body models.Tagable, cfg *RequestConfiguration) (models.Tagable, error) {
	_, _ = ctx, cfg
	return body, nil
}

// TagsItemRequestBuilderDeleteQueryParameters is the query the delete
// takes, each field tagged with the wire parameter it carries.
type TagsItemRequestBuilderDeleteQueryParameters struct {
	Confirm *bool   `uriparametername:"confirm"`
	Reason  *string `uriparametername:"reason"`
}

// Delete removes one tag, confirmed through its query.
func (b *TagsItemRequestBuilder) Delete(ctx context.Context, cfg *abstractions.RequestConfiguration[TagsItemRequestBuilderDeleteQueryParameters]) error {
	_, _ = ctx, cfg
	return nil
}

// Assign reaches the /tags/{tagId}/assign invocation.
func (b *TagsItemRequestBuilder) Assign() *TagsItemAssignRequestBuilder {
	return &TagsItemAssignRequestBuilder{}
}

// TagsItemAssignRequestBuilder is an invocation builder.
type TagsItemAssignRequestBuilder struct{}

// Post invokes the assignment.
func (b *TagsItemAssignRequestBuilder) Post(ctx context.Context, body models.Tagable, cfg *RequestConfiguration) (models.Tagable, error) {
	_, _ = ctx, cfg
	return body, nil
}

// Gizmos reaches a wrapped-list collection whose envelope is an interface
// with a single slice getter named for the "gizmos" wire key.
func (c *APIClient) Gizmos() *GizmosRequestBuilder { return &GizmosRequestBuilder{} }

// GizmosRequestBuilder is the wrapped collection builder.
type GizmosRequestBuilder struct{}

// Get lists the collection, wrapped under the "gizmos" key.
func (b *GizmosRequestBuilder) Get(ctx context.Context, cfg *RequestConfiguration) (models.GizmoCollectionResponseable, error) {
	_, _ = ctx, cfg
	return nil, nil
}

// Blobs reaches a collection whose envelope carries two slice getters and
// no wire key to choose between them.
func (c *APIClient) Blobs() *BlobsRequestBuilder { return &BlobsRequestBuilder{} }

// BlobsRequestBuilder is the ambiguous collection builder.
type BlobsRequestBuilder struct{}

// Get lists the collection through an ambiguous envelope.
func (b *BlobsRequestBuilder) Get(ctx context.Context, cfg *RequestConfiguration) (models.BlobCollectionResponseable, error) {
	_, _ = ctx, cfg
	return nil, nil
}

// Orphans reaches a collection whose body model has no constructor.
func (c *APIClient) Orphans() *OrphansRequestBuilder { return &OrphansRequestBuilder{} }

// OrphansRequestBuilder is the orphan collection builder.
type OrphansRequestBuilder struct{}

// Post creates an orphan.
func (b *OrphansRequestBuilder) Post(ctx context.Context, body models.Orphanable, cfg *RequestConfiguration) (models.Orphanable, error) {
	_, _ = ctx, cfg
	return body, nil
}

// ByOrphanId indexes into one orphan.
func (b *OrphansRequestBuilder) ByOrphanId(orphanId string) *OrphansItemRequestBuilder {
	_ = orphanId
	return &OrphansItemRequestBuilder{}
}

// OrphansItemRequestBuilder is the orphan item builder.
type OrphansItemRequestBuilder struct{}

// Get reads one orphan.
func (b *OrphansItemRequestBuilder) Get(ctx context.Context, cfg *RequestConfiguration) (models.Orphanable, error) {
	_, _ = ctx, cfg
	return nil, nil
}
