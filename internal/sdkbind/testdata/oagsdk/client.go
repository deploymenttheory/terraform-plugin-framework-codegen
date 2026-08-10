// Package sdk is a miniature openapi-generator-shaped go client: flat
// per-tag API services hanging off the client, one request-builder struct
// per operation with fluent parameter setters, and Execute() returning
// (payload, *http.Response, error). It exists so the binding checks run
// against real type information without invoking a generator.
package sdk

// APIClient is the generated client, services grouped by spec tag.
type APIClient struct {
	TagsAPI   *TagsAPIService
	GroupsAPI *GroupsAPIService
}

// NewAPIClient constructs the client.
func NewAPIClient() *APIClient {
	return &APIClient{TagsAPI: &TagsAPIService{}, GroupsAPI: &GroupsAPIService{}}
}
