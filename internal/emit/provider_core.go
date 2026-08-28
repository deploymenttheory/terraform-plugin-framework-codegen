// Package emit renders the provider core — the shared runtime plumbing
// every generated provider repo carries — from the templates in
// internal/templates/provider-core into a target tree, and reports what
// it wrote as manifest entries.
//
// Templates carry shape, this package carries logic: every value a
// template interpolates leaves here as a finished string or a presence
// boolean, computed once in FromConfig. Per-entity emission (resources,
// datasources, list resources, actions) is a separate, later package —
// the provider core is everything that exists before the first entity
// does.
package emit

import (
	"fmt"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
)

// DefaultGoVersion is the Go directive the generated go.mod declares.
const DefaultGoVersion = "1.25"

// ProviderCore is the render context for the provider core: everything
// the templates interpolate, finished. Strings arrive complete — no
// template assembles a value — and the backend and auth booleans exist so
// templates branch on presence, never on meaning.
type ProviderCore struct {
	// AcceptedRequestBodies is what the probe recorded of each entity's accepted
	// creates, keyed by entity. An acceptance fixture is built from these
	// rather than from values derived again from the document: the document
	// says what should be accepted, these are what was. Empty for an entity
	// no run has cleared, which falls back to the derivation.
	AcceptedRequestBodies map[string]observe.RequestBodies

	// Module is the provider repo's Go module path, e.g.
	// "github.com/exampleco/terraform-provider-petstore".
	Module string
	// ProviderName is the terraform provider name, e.g. "petstore".
	ProviderName string
	// RegistryAddress is the full registry address main.go serves, e.g.
	// "registry.terraform.io/exampleco/petstore".
	RegistryAddress string
	// EnvPrefix prefixes every environment fallback the provider reads:
	// the uppercased provider name, e.g. "PETSTORE". These operator-facing
	// variables (PETSTORE_API_TOKEN, …) are distinct from the toolkit
	// pipeline's TFPFGEN_AUTH_* secrets.
	EnvPrefix string
	// ClientType is the generated SDK's client type name, e.g. "APIClient".
	ClientType string
	// ClientConstructor is the generated SDK's client constructor name,
	// e.g. "NewAPIClient".
	ClientConstructor string
	// SDKImport is the generated SDK's import path, always aliased to
	// "sdk" where imported.
	SDKImport string
	// GoVersion is the go directive for the generated go.mod.
	GoVersion string
	// DefaultEndpoint is the API base URL the spec's servers declare,
	// empty when the operator must supply one.
	DefaultEndpoint string
	// DefaultTokenURL is the OAuth2 token endpoint tfpfgen.yaml declares,
	// empty when the provider configuration must supply one. Only the
	// oauth2_client_credentials method reads it.
	DefaultTokenURL string
	// APIKeyHeader is the header name the api_key_header method sends the
	// key in. Only that method reads it.
	APIKeyHeader string

	// Exactly one backend boolean is set; it selects which dialect
	// template of each pair is emitted.
	BackendKiota            bool
	BackendOpenAPIGenerator bool

	// Exactly one auth boolean is set; it selects the provider schema
	// fields, the client credential fields, and the transport.
	AuthBearerToken             bool
	AuthAPIKeyHeader            bool
	AuthBasic                   bool
	AuthOAuth2ClientCredentials bool
	AuthGitHubApp               bool
}

// FromConfig derives the provider-core context from a validated
// tfpfgen.yaml. defaultEndpoint is the base URL the revised spec's
// servers declare — the caller reads the spec, this package does not —
// and may be empty.
//
// Everything else a template needs is derived here, in one place: the
// module path and registry address from the provider block, the
// environment prefix from the provider name, the SDK surface names from
// sdk.client_type_name.
func FromConfig(configuration *config.Config, defaultEndpoint string) (ProviderCore, error) {
	name := configuration.Provider.Name
	namespace := configuration.Provider.RegistryNamespace
	if name == "" || namespace == "" {
		return ProviderCore{}, fmt.Errorf("provider.name and provider.registry_namespace must both be set to render the provider core")
	}
	if configuration.SDK.ClientTypeName == "" {
		return ProviderCore{}, fmt.Errorf("sdk.client_type_name must be set to render the provider core")
	}

	pc := ProviderCore{
		Module:            fmt.Sprintf("github.com/%s/terraform-provider-%s", namespace, name),
		ProviderName:      name,
		RegistryAddress:   fmt.Sprintf("registry.terraform.io/%s/%s", namespace, name),
		EnvPrefix:         envPrefix(name),
		ClientType:        configuration.SDK.ClientTypeName,
		ClientConstructor: "New" + configuration.SDK.ClientTypeName,
		GoVersion:         DefaultGoVersion,
		DefaultEndpoint:   defaultEndpoint,
		DefaultTokenURL:   configuration.Auth.TokenURL,
		APIKeyHeader:      configuration.Auth.APIKeyHeader,
	}
	pc.SDKImport = pc.Module + "/internal/sdk"

	switch configuration.SDK.Backend {
	case config.BackendKiota:
		pc.BackendKiota = true
	case config.BackendOpenAPIGenerator:
		pc.BackendOpenAPIGenerator = true
	default:
		return ProviderCore{}, fmt.Errorf("sdk.backend %q is not a supported backend (%s | %s)",
			configuration.SDK.Backend, config.BackendKiota, config.BackendOpenAPIGenerator)
	}

	switch configuration.Auth.Method {
	case config.AuthBearerToken:
		pc.AuthBearerToken = true
	case config.AuthAPIKeyHeader:
		pc.AuthAPIKeyHeader = true
	case config.AuthBasic:
		pc.AuthBasic = true
	case config.AuthOAuth2ClientCredentials:
		pc.AuthOAuth2ClientCredentials = true
	case config.AuthGitHubApp:
		pc.AuthGitHubApp = true
	default:
		return ProviderCore{}, fmt.Errorf("auth.method %q is not a supported method", configuration.Auth.Method)
	}

	if pc.AuthAPIKeyHeader && pc.APIKeyHeader == "" {
		return ProviderCore{}, fmt.Errorf("auth.api_key_header must be set when auth.method is %s", config.AuthAPIKeyHeader)
	}

	return pc, nil
}

// check refuses an inconsistent context before any template sees it, so
// a bad caller fails with one clear message rather than a template error.
func (pc ProviderCore) check() error {
	var problems []string

	for _, field := range []struct{ name, value string }{
		{"Module", pc.Module},
		{"ProviderName", pc.ProviderName},
		{"RegistryAddress", pc.RegistryAddress},
		{"EnvPrefix", pc.EnvPrefix},
		{"ClientType", pc.ClientType},
		{"ClientConstructor", pc.ClientConstructor},
		{"SDKImport", pc.SDKImport},
		{"GoVersion", pc.GoVersion},
	} {
		if field.value == "" {
			problems = append(problems, field.name+" is empty")
		}
	}

	backends := 0
	for _, set := range []bool{pc.BackendKiota, pc.BackendOpenAPIGenerator} {
		if set {
			backends++
		}
	}
	if backends != 1 {
		problems = append(problems, "exactly one backend boolean must be set")
	}

	methods := 0
	for _, set := range []bool{pc.AuthBearerToken, pc.AuthAPIKeyHeader, pc.AuthBasic, pc.AuthOAuth2ClientCredentials, pc.AuthGitHubApp} {
		if set {
			methods++
		}
	}
	if methods != 1 {
		problems = append(problems, "exactly one auth boolean must be set")
	}

	if pc.AuthAPIKeyHeader && pc.APIKeyHeader == "" {
		problems = append(problems, "APIKeyHeader is empty for the api_key_header method")
	}

	if len(problems) > 0 {
		return fmt.Errorf("the provider-core context is not renderable: %s", strings.Join(problems, "; "))
	}
	return nil
}

// envPrefix renders a provider name as the operator environment-variable
// prefix: the name uppercased, with every non-alphanumeric run collapsed to
// one underscore.
//
// Bare, with no TF_ in front. Published providers spell these AWS_,
// GOOGLE_, CLOUDFLARE_ — an operator reaching for PETSTORE_API_TOKEN is
// reaching for the name every other provider taught them. The toolkit's
// own pipeline secrets use the TFPFGEN_AUTH_ prefix, so the two sets still
// cannot be confused for one another.
func envPrefix(name string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToUpper(name) {
		alnum := (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if alnum {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
