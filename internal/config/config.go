// Package config owns tfpfgen.yaml: the schema as Go structs, strict
// decoding, semantic validation, and the auth-role→secret-name contract.
// This package is the single definition site for the cross-repo config
// contract — the reusable workflows and docs/config.md both derive from it,
// so a key that nothing reads cannot exist.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the config schema this build understands. Bumps follow
// the toolkit's semver: a key rename or removal is a breaking change.
const SchemaVersion = 1

// Config is the complete tfpfgen.yaml. Field additions land here first;
// docs/config.md is generated from this schema and CI holds it to that.
type Config struct {
	Version   int       `yaml:"version"`
	Provider  Provider  `yaml:"provider"`
	Generator Generator `yaml:"generator"`
	Spec      Spec      `yaml:"spec"`
	SDK       SDK       `yaml:"sdk"`
	Auth      Auth      `yaml:"auth"`
	Audit     Audit     `yaml:"audit"`
	Services  Services  `yaml:"services"`
}

// Provider names the provider this repo publishes.
type Provider struct {
	Name              string `yaml:"name"`
	RegistryNamespace string `yaml:"registry_namespace"`
}

// Generator pins the toolkit release the pipeline installs.
type Generator struct {
	Version string `yaml:"version"`
}

// Spec locates the upstream OpenAPI document.
type Spec struct {
	DocumentURL string `yaml:"document_url"`
}

// SDK selects and pins the SDK backend.
type SDK struct {
	Backend        string   `yaml:"backend"`
	BackendVersion string   `yaml:"backend_version"`
	ClientTypeName string   `yaml:"client_type_name"`
	IncludePaths   []string `yaml:"include_paths"`
	ExcludePaths   []string `yaml:"exclude_paths"`
}

// Auth names how the audit authenticates. Secret values never appear in
// config — names are fixed by role (see secrets.go).
type Auth struct {
	Method       string `yaml:"method"`
	APIKeyHeader string `yaml:"api_key_header"`
	TokenURL     string `yaml:"token_url"`
}

// Audit bounds the credentialed live-API stage.
type Audit struct {
	Enabled         bool     `yaml:"enabled"`
	BaseURLOverride string   `yaml:"base_url_override"`
	NamePrefix      string   `yaml:"name_prefix"`
	MaxObjects      int      `yaml:"max_objects"`
	RateLimitRPS    int      `yaml:"rate_limit_rps"`
	AutoAccept      []string `yaml:"auto_accept"`
}

// Services scopes which spec entities become provider code.
type Services struct {
	Exclude []string `yaml:"exclude"`
}

// SDK backends. The set is closed; config validation refuses others.
const (
	BackendKiota            = "kiota"
	BackendOpenAPIGenerator = "openapi-generator"
)

// Auth methods. The set is closed; each maps to fixed secret roles.
const (
	AuthBearerToken             = "bearer_token"
	AuthAPIKeyHeader            = "api_key_header"
	AuthBasic                   = "basic"
	AuthOAuth2ClientCredentials = "oauth2_client_credentials"
	AuthGitHubApp               = "github_app"
)

// withDefaults returns a Config pre-populated with the values a decode
// leaves in place when the key is absent.
func withDefaults() Config {
	return Config{
		SDK: SDK{ClientTypeName: "APIClient"},
		Audit: Audit{
			Enabled:      true,
			NamePrefix:   "tfpfgen",
			MaxObjects:   25,
			RateLimitRPS: 2,
		},
	}
}

// Load reads, strictly decodes, and validates a tfpfgen.yaml. Unknown keys
// are an error naming the key and its nearest valid neighbour; semantic
// problems are reported all at once, never one per run.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return Parse(raw, path)
}

// Parse is Load without the filesystem, for callers holding bytes.
func Parse(raw []byte, path string) (*Config, error) {
	cfg := withDefaults()
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%s: %s", path, describeDecodeError(err))
	}
	if problems := cfg.problems(); len(problems) > 0 {
		return nil, fmt.Errorf("%s is not a valid tfpfgen.yaml:\n  - %s",
			path, strings.Join(problems, "\n  - "))
	}
	return &cfg, nil
}

// describeDecodeError rewrites yaml.v3's unknown-field message to also name
// the nearest known key, so a typo reads as "did you mean" rather than a
// schema lecture.
func describeDecodeError(err error) string {
	msg := err.Error()
	const marker = "field "
	i := strings.Index(msg, marker)
	if i < 0 || !strings.Contains(msg, "not found in type") {
		return msg
	}
	rest := msg[i+len(marker):]
	key := rest[:strings.IndexAny(rest+" ", " ")]
	if suggestion := nearestKey(key); suggestion != "" {
		return fmt.Sprintf("unknown key %q (did you mean %q?)", key, suggestion)
	}
	return fmt.Sprintf("unknown key %q", key)
}

// knownKeys is every yaml key in the schema, for typo suggestions.
func knownKeys() []string {
	return []string{
		"version", "provider", "generator", "spec", "sdk", "auth", "audit", "services",
		"name", "registry_namespace",
		"document_url",
		"backend", "backend_version", "client_type_name", "include_paths", "exclude_paths",
		"method", "api_key_header", "token_url",
		"enabled", "base_url_override", "name_prefix", "max_objects", "rate_limit_rps", "auto_accept",
		"exclude",
	}
}

// nearestKey returns the known key closest to got, or "" when nothing is
// close enough to be a plausible typo.
func nearestKey(got string) string {
	best, bestDist := "", len(got)/2+1
	for _, k := range knownKeys() {
		if d := levenshtein(got, k); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// levenshtein is the standard two-row edit distance.
func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, min(curr[j-1]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
