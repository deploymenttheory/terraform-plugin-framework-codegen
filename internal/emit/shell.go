// Package emit renders the provider shell — the runtime plumbing every
// generated provider repo carries — from the templates in
// internal/templates/shell into a target tree, and reports what it wrote
// as manifest entries.
//
// Templates carry shape, this package carries logic: every value a
// template interpolates leaves here as a finished string or a presence
// boolean, computed once in FromConfig. Per-entity emission (resources,
// datasources, list resources, actions) is a separate, later package —
// the shell is everything that exists before the first entity does.
package emit

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
)

// DefaultGoVersion is the Go directive the generated go.mod declares.
const DefaultGoVersion = "1.25"

// Shell is the render context for the provider shell: everything the
// templates interpolate, finished. Strings arrive complete — no template
// assembles a value — and the backend and auth booleans exist so
// templates branch on presence, never on meaning.
type Shell struct {
	// Module is the provider repo's Go module path, e.g.
	// "github.com/exampleco/terraform-provider-petstore".
	Module string
	// ProviderName is the terraform provider name, e.g. "petstore".
	ProviderName string
	// RegistryAddress is the full registry address main.go serves, e.g.
	// "registry.terraform.io/exampleco/petstore".
	RegistryAddress string
	// EnvPrefix prefixes every environment fallback the provider reads,
	// e.g. "PETSTORE".
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

// FromConfig derives the shell context from a validated tfpfgen.yaml.
// defaultEndpoint is the base URL the revised spec's servers declare —
// the caller reads the spec, this package does not — and may be empty.
//
// Everything else a template needs is derived here, in one place: the
// module path and registry address from the provider block, the
// environment prefix from the provider name, the SDK surface names from
// sdk.client_type_name.
func FromConfig(cfg *config.Config, defaultEndpoint string) (Shell, error) {
	name := cfg.Provider.Name
	namespace := cfg.Provider.RegistryNamespace
	if name == "" || namespace == "" {
		return Shell{}, fmt.Errorf("provider.name and provider.registry_namespace must both be set to render the shell")
	}
	if cfg.SDK.ClientTypeName == "" {
		return Shell{}, fmt.Errorf("sdk.client_type_name must be set to render the shell")
	}

	sh := Shell{
		Module:            fmt.Sprintf("github.com/%s/terraform-provider-%s", namespace, name),
		ProviderName:      name,
		RegistryAddress:   fmt.Sprintf("registry.terraform.io/%s/%s", namespace, name),
		EnvPrefix:         envPrefix(name),
		ClientType:        cfg.SDK.ClientTypeName,
		ClientConstructor: "New" + cfg.SDK.ClientTypeName,
		GoVersion:         DefaultGoVersion,
		DefaultEndpoint:   defaultEndpoint,
		DefaultTokenURL:   cfg.Auth.TokenURL,
		APIKeyHeader:      cfg.Auth.APIKeyHeader,
	}
	sh.SDKImport = sh.Module + "/internal/sdk"

	switch cfg.SDK.Backend {
	case config.BackendKiota:
		sh.BackendKiota = true
	case config.BackendOpenAPIGenerator:
		sh.BackendOpenAPIGenerator = true
	default:
		return Shell{}, fmt.Errorf("sdk.backend %q is not a supported backend (%s | %s)",
			cfg.SDK.Backend, config.BackendKiota, config.BackendOpenAPIGenerator)
	}

	switch cfg.Auth.Method {
	case config.AuthBearerToken:
		sh.AuthBearerToken = true
	case config.AuthAPIKeyHeader:
		sh.AuthAPIKeyHeader = true
	case config.AuthBasic:
		sh.AuthBasic = true
	case config.AuthOAuth2ClientCredentials:
		sh.AuthOAuth2ClientCredentials = true
	case config.AuthGitHubApp:
		sh.AuthGitHubApp = true
	default:
		return Shell{}, fmt.Errorf("auth.method %q is not a supported method", cfg.Auth.Method)
	}

	if sh.AuthAPIKeyHeader && sh.APIKeyHeader == "" {
		return Shell{}, fmt.Errorf("auth.api_key_header must be set when auth.method is %s", config.AuthAPIKeyHeader)
	}

	return sh, nil
}

// check refuses an inconsistent context before any template sees it, so
// a bad caller fails with one clear message rather than a template error.
func (sh Shell) check() error {
	var problems []string

	for _, field := range []struct{ name, value string }{
		{"Module", sh.Module},
		{"ProviderName", sh.ProviderName},
		{"RegistryAddress", sh.RegistryAddress},
		{"EnvPrefix", sh.EnvPrefix},
		{"ClientType", sh.ClientType},
		{"ClientConstructor", sh.ClientConstructor},
		{"SDKImport", sh.SDKImport},
		{"GoVersion", sh.GoVersion},
	} {
		if field.value == "" {
			problems = append(problems, field.name+" is empty")
		}
	}

	backends := 0
	for _, set := range []bool{sh.BackendKiota, sh.BackendOpenAPIGenerator} {
		if set {
			backends++
		}
	}
	if backends != 1 {
		problems = append(problems, "exactly one backend boolean must be set")
	}

	methods := 0
	for _, set := range []bool{sh.AuthBearerToken, sh.AuthAPIKeyHeader, sh.AuthBasic, sh.AuthOAuth2ClientCredentials, sh.AuthGitHubApp} {
		if set {
			methods++
		}
	}
	if methods != 1 {
		problems = append(problems, "exactly one auth boolean must be set")
	}

	if sh.AuthAPIKeyHeader && sh.APIKeyHeader == "" {
		problems = append(problems, "APIKeyHeader is empty for the api_key_header method")
	}

	if len(problems) > 0 {
		return fmt.Errorf("the shell context is not renderable: %s", strings.Join(problems, "; "))
	}
	return nil
}

// envPrefix renders a provider name as an environment-variable prefix:
// uppercased, with every non-alphanumeric run collapsed to one underscore.
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
