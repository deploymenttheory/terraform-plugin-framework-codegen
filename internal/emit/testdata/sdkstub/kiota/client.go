// A hand-written stand-in for a kiota-generated SDK, exposing exactly the
// surface the provider core touches: the client type and its adapter-taking
// constructor. A real generated SDK would make this fixture larger than
// the thing it tests.
package sdk

import (
	abstractions "github.com/microsoft/kiota-abstractions-go"
)

// APIClient stands in for the generated client.
type APIClient struct {
	adapter abstractions.RequestAdapter
}

// NewAPIClient stands in for the generated constructor.
func NewAPIClient(adapter abstractions.RequestAdapter) *APIClient {
	return &APIClient{adapter: adapter}
}
