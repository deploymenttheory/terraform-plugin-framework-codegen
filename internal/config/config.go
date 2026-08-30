// Package config owns tfpfgen.yaml: the schema as Go structs, strict
// decoding through viper, semantic validation, and the auth-role→secret-name
// contract. This package is the single definition site for the cross-repo
// config contract — the reusable workflows and docs/config.md both derive
// from it, so a key that nothing reads cannot exist.
package config

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/viper"
)

// SchemaVersion is the config schema this build understands. Bumps follow
// the toolkit's semver: a key rename or removal is a breaking change.
const SchemaVersion = 1

// Config is the complete tfpfgen.yaml. Field additions land here first;
// docs/config.md is generated from this schema and CI holds it to that.
type Config struct {
	Version   int       `mapstructure:"version"`
	Provider  Provider  `mapstructure:"provider"`
	Generator Generator `mapstructure:"generator"`
	Spec      Spec      `mapstructure:"spec"`
	SDK       SDK       `mapstructure:"sdk"`
	Auth      Auth      `mapstructure:"auth"`
	Audit     Audit     `mapstructure:"audit"`
	Services  Services  `mapstructure:"services"`
}

// Provider names the provider this repo publishes.
type Provider struct {
	Name              string `mapstructure:"name"`
	RegistryNamespace string `mapstructure:"registry_namespace"`
}

// Generator pins the toolkit release the pipeline installs.
type Generator struct {
	Version string `mapstructure:"version"`
}

// Spec locates the upstream OpenAPI document.
type Spec struct {
	DocumentURL string `mapstructure:"document_url"`
}

// SDK selects and pins the SDK backend.
type SDK struct {
	Backend        string   `mapstructure:"backend"`
	BackendVersion string   `mapstructure:"backend_version"`
	ClientTypeName string   `mapstructure:"client_type_name"`
	IncludePaths   []string `mapstructure:"include_paths"`
	ExcludePaths   []string `mapstructure:"exclude_paths"`
}

// Auth names how the audit authenticates. Secret values never appear in
// config — names are fixed by role (see secrets.go).
type Auth struct {
	Method       string `mapstructure:"method"`
	APIKeyHeader string `mapstructure:"api_key_header"`
	TokenURL     string `mapstructure:"token_url"`
}

// Audit bounds the credentialed live-API stage.
type Audit struct {
	Enabled         bool     `mapstructure:"enabled"`
	BaseURLOverride string   `mapstructure:"base_url_override"`
	NamePrefix      string   `mapstructure:"name_prefix"`
	MaxObjects      int      `mapstructure:"max_objects"`
	RateLimitRPS    int      `mapstructure:"rate_limit_rps"`
	AutoAccept      []string `mapstructure:"auto_accept"`
}

// Services scopes which spec entities become provider code.
type Services struct {
	Exclude []string `mapstructure:"exclude"`
}

// SDK backends. The set is closed; config validation does not accept others.
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

// setDefaults registers the values a decode leaves in place when the key is
// absent from the file.
func setDefaults(v *viper.Viper) {
	v.SetDefault("sdk.client_type_name", "APIClient")
	v.SetDefault("audit.enabled", true)
	v.SetDefault("audit.name_prefix", "tfpfgen")
	v.SetDefault("audit.max_objects", 25)
	v.SetDefault("audit.rate_limit_rps", 2)
}

// Load reads, strictly decodes, and validates a tfpfgen.yaml. Unknown keys
// are an error naming the key and its nearest valid neighbour; semantic
// problems are reported all at once, never one per run.
func Load(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return finish(v, path)
}

// Parse is Load without the filesystem, for callers holding bytes.
func Parse(raw []byte, path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return finish(v, path)
}

// finish strictly unmarshals and semantically validates a read config.
func finish(v *viper.Viper, path string) (*Config, error) {
	var configuration Config
	if err := v.UnmarshalExact(&configuration); err != nil {
		return nil, fmt.Errorf("%s: %s", path, describeDecodeError(err))
	}
	if problems := configuration.problems(); len(problems) > 0 {
		return nil, fmt.Errorf("%s is not a valid tfpfgen.yaml:\n  - %s",
			path, strings.Join(problems, "\n  - "))
	}
	return &configuration, nil
}

// invalidKeys matches mapstructure's unknown-key report, e.g.
// "* 'audit' has invalid keys: enabledd, foo".
var invalidKeys = regexp.MustCompile(`has invalid keys: ([^\n]+)`)

// describeDecodeError rewrites the strict-unmarshal message so a typo reads
// as "did you mean" rather than a schema lecture.
func describeDecodeError(err error) string {
	matches := invalidKeys.FindAllStringSubmatch(err.Error(), -1)
	if len(matches) == 0 {
		return err.Error()
	}

	var parts []string
	for _, m := range matches {
		for _, key := range strings.Split(m[1], ",") {
			key = strings.TrimSpace(key)
			if suggestion := nearestKey(key); suggestion != "" {
				parts = append(parts, fmt.Sprintf("unknown key %q (did you mean %q?)", key, suggestion))
			} else {
				parts = append(parts, fmt.Sprintf("unknown key %q", key))
			}
		}
	}
	return strings.Join(parts, "; ")
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
	previous := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(previous[j]+1, min(curr[j-1]+1, previous[j-1]+cost))
		}
		previous, curr = curr, previous
	}
	return previous[len(b)]
}
