package sdk

import (
	"context"
	"net/http"
)

// TagsAPIService carries the tag operations.
type TagsAPIService struct{}

// ApiCreateTagRequest is the create builder; the body setter is named
// after the spec's body parameter, which no document derivation can see.
type ApiCreateTagRequest struct {
	svc *TagsAPIService
	ctx context.Context
	tag *Tag
}

// Tag sets the request body.
func (r ApiCreateTagRequest) Tag(tag Tag) ApiCreateTagRequest {
	r.tag = &tag
	return r
}

// Execute performs the create.
func (r ApiCreateTagRequest) Execute() (*Tag, *http.Response, error) { return r.tag, nil, nil }

// CreateTag starts a create.
func (a *TagsAPIService) CreateTag(ctx context.Context) ApiCreateTagRequest {
	return ApiCreateTagRequest{svc: a, ctx: ctx}
}

// ApiGetTagRequest is the read builder.
type ApiGetTagRequest struct {
	svc   *TagsAPIService
	ctx   context.Context
	tagId string
}

// Execute performs the read.
func (r ApiGetTagRequest) Execute() (*Tag, *http.Response, error) { return nil, nil, nil }

// GetTag starts a read.
func (a *TagsAPIService) GetTag(ctx context.Context, tagId string) ApiGetTagRequest {
	return ApiGetTagRequest{svc: a, ctx: ctx, tagId: tagId}
}

// ApiUpdateTagRequest is the update builder.
type ApiUpdateTagRequest struct {
	svc   *TagsAPIService
	ctx   context.Context
	tagId string
	tag   *Tag
}

// Tag sets the request body.
func (r ApiUpdateTagRequest) Tag(tag Tag) ApiUpdateTagRequest {
	r.tag = &tag
	return r
}

// Execute performs the update.
func (r ApiUpdateTagRequest) Execute() (*Tag, *http.Response, error) { return r.tag, nil, nil }

// UpdateTag starts an update.
func (a *TagsAPIService) UpdateTag(ctx context.Context, tagId string) ApiUpdateTagRequest {
	return ApiUpdateTagRequest{svc: a, ctx: ctx, tagId: tagId}
}

// ApiDeleteTagRequest is the delete builder.
type ApiDeleteTagRequest struct {
	svc   *TagsAPIService
	ctx   context.Context
	tagId string
}

// Execute performs the delete.
func (r ApiDeleteTagRequest) Execute() (*http.Response, error) { return nil, nil }

// DeleteTag starts a delete.
func (a *TagsAPIService) DeleteTag(ctx context.Context, tagId string) ApiDeleteTagRequest {
	return ApiDeleteTagRequest{svc: a, ctx: ctx, tagId: tagId}
}

// ApiListTagsRequest is the list builder; its payload is a bare slice.
type ApiListTagsRequest struct {
	svc *TagsAPIService
	ctx context.Context
}

// Execute performs the list.
func (r ApiListTagsRequest) Execute() ([]Tag, *http.Response, error) { return nil, nil, nil }

// ListTags starts a list.
func (a *TagsAPIService) ListTags(ctx context.Context) ApiListTagsRequest {
	return ApiListTagsRequest{svc: a, ctx: ctx}
}

// GroupsAPIService carries the group operations.
type GroupsAPIService struct{}

// ApiListGroupsRequest is a list builder whose payload is an envelope.
type ApiListGroupsRequest struct {
	svc *GroupsAPIService
	ctx context.Context
}

// Execute performs the list.
func (r ApiListGroupsRequest) Execute() (*GroupList, *http.Response, error) { return nil, nil, nil }

// ListGroups starts a list.
func (a *GroupsAPIService) ListGroups(ctx context.Context) ApiListGroupsRequest {
	return ApiListGroupsRequest{svc: a, ctx: ctx}
}
