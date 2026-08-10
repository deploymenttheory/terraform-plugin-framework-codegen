// A hand-written stand-in for an openapi-generator SDK, exposing exactly
// the surface the provider core touches: the configuration, the server list, and
// the client constructor. A real generated SDK would make this fixture
// larger than the thing it tests.
package sdk

import (
	"net/http"
)

// Configuration stands in for the generated configuration.
type Configuration struct {
	UserAgent  string
	HTTPClient *http.Client
	Servers    ServerConfigurations
}

// NewConfiguration stands in for the generated constructor.
func NewConfiguration() *Configuration {
	return &Configuration{}
}

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
}

// NewAPIClient stands in for the generated constructor.
func NewAPIClient(configuration *Configuration) *APIClient {
	return &APIClient{configuration: configuration}
}
