// Package sdk is the Nimbus fixture SDK: a hand-written stand-in for what
// `tfpfgen sdk generate` produces with kiota, in the one shape the generated
// provider actually binds to -- a root client, request-builder hops, typed
// indexers, and verbs returning (interface, error) or error alone.
//
// It exists so the curated blueprint fixture can be compiled end to end. The
// alternative was to commit a real kiota SDK, which for three resources is
// thousands of lines of serialisation the generator never names; a fixture is
// worth having only while its failures stay readable.
//
// No method here performs a request. The adapter is held because the shell's
// client package constructs the client with one, and because a builder that
// took no adapter would not be the shape a generated SDK has.
package sdk

import (
	abstractions "github.com/microsoft/kiota-abstractions-go"
)

// ApiClient is the root request builder, named as `sdk generate` names it by
// default and as the provider block's clientType declares.
//
// RequestAdapter is exported because kiota exports it -- every generated
// builder embeds a BaseRequestBuilder carrying that field -- and the shell's
// own client wire test reaches for it by that name.
type ApiClient struct {
	RequestAdapter abstractions.RequestAdapter
}

// NewApiClient is what the scaffolded client package calls once it has built an
// authenticated adapter.
func NewApiClient(adapter abstractions.RequestAdapter) *ApiClient {
	return &ApiClient{RequestAdapter: adapter}
}

// RequestConfiguration is the trailing argument every kiota verb takes. One
// type serves every operation here; a generated SDK mints one per operation,
// and the difference is invisible to a caller passing nil.
type RequestConfiguration struct {
	Headers map[string]string
}

// Labels anchors the label chains.
func (c *ApiClient) Labels() *LabelsRequestBuilder {
	return &LabelsRequestBuilder{adapter: c.RequestAdapter}
}

// Releases anchors the release chains.
func (c *ApiClient) Releases() *ReleasesRequestBuilder {
	return &ReleasesRequestBuilder{adapter: c.RequestAdapter}
}

// RunnerPools anchors the runner-pool chains, which are the deepest the fixture
// has: an indexer followed by a named sub-builder followed by a verb.
func (c *ApiClient) RunnerPools() *RunnerPoolsRequestBuilder {
	return &RunnerPoolsRequestBuilder{adapter: c.RequestAdapter}
}
