package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// dnsLabel matches the names the registry and file layout accept.
var dnsLabel = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)

// releaseTag matches an exact toolkit release. Branch names — "main"
// included — are deliberately unmatchable: a pipeline that reinstalls a
// moving target on every run was one of v1's failure modes.
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
		report("generator.version: %q is not an exact release tag (vX.Y.Z); branches are refused", c.Generator.Version)
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

	return p
}

func authMethods() []string {
	return []string{AuthBearerToken, AuthAPIKeyHeader, AuthBasic, AuthOAuth2ClientCredentials, AuthGitHubApp}
}
