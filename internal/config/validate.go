package config

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/revise"
)

// dnsLabel matches the names the registry and file layout accept.
var dnsLabel = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)

// releaseTag matches an exact toolkit release. Branch names — "main"
// included — are deliberately unmatchable: a pipeline that reinstalls a
// moving target on every run cannot reproduce what it generated.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// problems returns every semantic defect at once. An empty slice means the
// config is valid.
func (c *Config) problems() []string {
	var p []string
	report := func(format string, args ...any) {
		p = append(p, fmt.Sprintf(format, args...))
	}

	if c.Version != SchemaVersion {
		report("version: %d is not schema version %d", c.Version, SchemaVersion)
	}

	switch {
	case c.Provider.Name == "":
		report("provider.name: required")
	case !dnsLabel.MatchString(c.Provider.Name):
		report("provider.name: %q is not a lowercase DNS label", c.Provider.Name)
	}
	switch {
	case c.Provider.RegistryNamespace == "":
		report("provider.registry_namespace: required")
	case !dnsLabel.MatchString(c.Provider.RegistryNamespace):
		report("provider.registry_namespace: %q is not a lowercase DNS label", c.Provider.RegistryNamespace)
	}

	switch {
	case c.Generator.Version == "":
		report("generator.version: required")
	case !releaseTag.MatchString(c.Generator.Version):
		report("generator.version: %q is not an exact release tag (vX.Y.Z); branches are not accepted", c.Generator.Version)
	}

	switch c.Spec.DocumentURL {
	case "":
		report("spec.document_url: required")
	default:
		if u, err := url.Parse(c.Spec.DocumentURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			report("spec.document_url: %q is not an http(s) URL", c.Spec.DocumentURL)
		}
	}

	switch c.SDK.Backend {
	case BackendKiota, BackendOpenAPIGenerator:
	case "":
		report("sdk.backend: required (%s)", strings.Join([]string{BackendKiota, BackendOpenAPIGenerator}, " | "))
	default:
		report("sdk.backend: %q is not a supported backend (%s)",
			c.SDK.Backend, strings.Join([]string{BackendKiota, BackendOpenAPIGenerator}, " | "))
	}
	if c.SDK.BackendVersion == "" {
		report("sdk.backend_version: required — the backend tool is installed at an exact pin")
	}
	if c.SDK.ClientTypeName == "" {
		report("sdk.client_type_name: must not be empty")
	}

	switch c.Auth.Method {
	case AuthBearerToken, AuthBasic, AuthGitHubApp:
	case AuthAPIKeyHeader:
		if c.Auth.APIKeyHeader == "" {
			report("auth.api_key_header: required when auth.method is %s", AuthAPIKeyHeader)
		}
	case AuthOAuth2ClientCredentials:
		if c.Auth.TokenURL == "" {
			report("auth.token_url: required when auth.method is %s", AuthOAuth2ClientCredentials)
		}
	case "":
		report("auth.method: required (%s)", strings.Join(authMethods(), " | "))
	default:
		report("auth.method: %q is not a supported method (%s)", c.Auth.Method, strings.Join(authMethods(), " | "))
	}
	if c.Auth.APIKeyHeader != "" && c.Auth.Method != AuthAPIKeyHeader {
		report("auth.api_key_header: only meaningful when auth.method is %s", AuthAPIKeyHeader)
	}

	if c.Audit.NamePrefix == "" {
		report("audit.name_prefix: must not be empty — every live object the audit creates carries it")
	}
	if c.Audit.MaxObjects < 1 {
		report("audit.max_objects: %d is not a positive object budget", c.Audit.MaxObjects)
	}
	if c.Audit.RateLimitRPS < 1 {
		report("audit.rate_limit_rps: %d is not a positive request rate", c.Audit.RateLimitRPS)
	}
	for i, kind := range c.Audit.AutoAccept {
		if !slices.Contains(autoAcceptKinds(), kind) {
			report("audit.auto_accept[%d]: %q is not an observation kind (one of %s)",
				i, kind, strings.Join(autoAcceptKinds(), ", "))
		}
	}

	return p
}

// autoAcceptKinds is the closed set an audit.auto_accept entry must name,
// taken from the revision stage itself rather than copied here: `tfpfgen
// spec revise` refuses the same list at the same vocabulary, and two
// hand-kept copies of a closed set drift the moment a kind is added.
// internal/spec/revise imports nothing from internal/config, so consuming it
// costs no cycle.
//
// The set is every observation kind revision knows, which includes the two
// with no correction form yet: listing one is inert — the observation is
// reported as NoForm and nothing is written — where refusing it here would
// fail a config that `spec revise` runs happily.
func autoAcceptKinds() []string { return revise.CompilableKinds() }

func authMethods() []string {
	return []string{AuthBearerToken, AuthAPIKeyHeader, AuthBasic, AuthOAuth2ClientCredentials, AuthGitHubApp}
}
