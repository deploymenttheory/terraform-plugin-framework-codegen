// Package client is a miniature kiota-shaped fluent surface: a root client, a
// builder hop, a typed indexer, and verbs returning (interface, error).
package client

import (
	"context"

	"example.com/kiotasdk/models"
)

type ApiClient struct{}

func (c *ApiClient) Tags() *TagsRequestBuilder { return &TagsRequestBuilder{} }

type TagsRequestBuilder struct{}

func (b *TagsRequestBuilder) ByTagId(id string) *TagItemRequestBuilder {
	return &TagItemRequestBuilder{}
}

func (b *TagsRequestBuilder) Post(ctx context.Context, body *models.Tag, cfg *RequestConfiguration) (models.Tagable, error) {
	return nil, nil
}

type TagItemRequestBuilder struct{}

func (b *TagItemRequestBuilder) Get(ctx context.Context, cfg *RequestConfiguration) (models.Tagable, error) {
	return nil, nil
}

func (b *TagItemRequestBuilder) Delete(ctx context.Context, cfg *RequestConfiguration) error {
	return nil
}

// TwoResults exists so a builder hop that cannot anchor a chain is testable.
func (b *TagsRequestBuilder) TwoResults() (*TagItemRequestBuilder, error) { return nil, nil }

type RequestConfiguration struct{}
