package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/revise"
)

// valid is a complete, correct tfpfgen.yaml. Each table case below mutates
// one aspect of it, so every failure message is provoked in isolation.
const valid = `version: 1
provider:
  name: example
  registry_namespace: deploymenttheory
generator:
  version: v0.1.0
spec:
  document_url: https://api.example.invalid/openapi.yaml
sdk:
  backend: kiota
  backend_version: "1.34.1"
auth:
  method: bearer_token
`

// replace swaps one line of the valid document, keyed by its exact prefix.
func replace(t *testing.T, prefix, with string) string {
	t.Helper()
	lines := strings.Split(valid, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			indent := l[:len(l)-len(strings.TrimLeft(l, " "))]
			lines[i] = indent + with
			return strings.Join(lines, "\n")
		}
	}
	t.Fatalf("no line with prefix %q in the valid fixture", prefix)
	return ""
}

func TestUnit_Config_GoldenTable(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string // empty = must parse; otherwise a substring of the error
	}{
		{"minimal valid config parses", valid, ""},
		{"openapi-generator backend parses", replace(t, "backend:", "backend: openapi-generator"), ""},
		{"basic auth parses", replace(t, "method:", "method: basic"), ""},
		{"github_app auth parses", replace(t, "method:", "method: github_app"), ""},
		{"oauth2 with token_url parses", strings.Replace(valid,
			"method: bearer_token", "method: oauth2_client_credentials\n  token_url: https://auth.example.invalid/token", 1), ""},
		{"api_key_header with header name parses", strings.Replace(valid,
			"method: bearer_token", "method: api_key_header\n  api_key_header: X-Api-Key", 1), ""},

		{"unknown top-level key is refused with a suggestion",
			valid + "auditt:\n  enabled: true\n", `unknown key "auditt" (did you mean "audit"?)`},
		{"unknown nested key is refused with a suggestion",
			strings.Replace(valid, "document_url:", "documents_url:", 1), `unknown key "documents_url" (did you mean "document_url"?)`},
		{"wrong schema version is refused", replace(t, "version: 1", "version: 2"), "not schema version 1"},
		{"missing provider name is refused", replace(t, "name: example", "name: \"\""), "provider.name: required"},
		{"uppercase provider name is refused", replace(t, "name: example", "name: Example"), "not a lowercase DNS label"},
		{"branch as generator version is refused", replace(t, "version: v0.1.0", "version: main"),
			`generator.version: "main" is not an exact release tag`},
		{"prerelease generator version is refused", replace(t, "version: v0.1.0", "version: v0.1.0-rc1"),
			"not an exact release tag"},
		{"missing document url is refused", replace(t, "document_url:", "document_url: \"\""), "spec.document_url: required"},
		{"non-http document url is refused", replace(t, "document_url:", "document_url: ftp://example.invalid/spec.yaml"),
			"not an http(s) URL"},
		{"unsupported backend is refused", replace(t, "backend:", "backend: swagger-codegen"),
			`sdk.backend: "swagger-codegen" is not a supported backend`},
		{"missing backend version is refused", replace(t, "backend_version:", "backend_version: \"\""),
			"sdk.backend_version: required"},
		{"unsupported auth method is refused", replace(t, "method:", "method: mtls"),
			`auth.method: "mtls" is not a supported method`},
		{"api_key_header method without header name is refused", replace(t, "method:", "method: api_key_header"),
			"auth.api_key_header: required"},
		{"oauth2 without token_url is refused", replace(t, "method:", "method: oauth2_client_credentials"),
			"auth.token_url: required"},
		{"stray api_key_header under another method is refused",
			valid + "  api_key_header: X-Api-Key\n", "only meaningful when auth.method is api_key_header"},
		{"zero object budget is refused", valid + "audit:\n  max_objects: 0\n", "not a positive object budget"},
		{"zero rate limit is refused", valid + "audit:\n  rate_limit_rps: 0\n", "not a positive request rate"},
		{"empty audit name prefix is refused", valid + "audit:\n  name_prefix: \"\"\n",
			"audit.name_prefix: must not be empty"},
		{"malformed yaml is refused", "version: [1\n", "yaml"},

		// audit.auto_accept names observation kinds, checked against the
		// vocabulary the revision stage itself enforces.
		{"an absent auto-accept list parses", valid, ""},
		{"an empty auto-accept list parses", valid + "audit:\n  auto_accept: []\n", ""},
		{"known auto-accept kinds parse",
			valid + "audit:\n  auto_accept: [listResponseShape, deleteNotFoundOK, updateStyle]\n", ""},
		{"a kind with no correction form yet still parses",
			// normalisation compiles to a NoForm note rather than a
			// correction; naming it is inert, not an error, because the
			// list says which kinds skip review, not which kinds exist.
			valid + "audit:\n  auto_accept: [normalisation]\n", ""},
		{"a mistyped auto-accept kind is refused with the valid set",
			valid + "audit:\n  auto_accept: [listResponse]\n",
			`audit.auto_accept[0]: "listResponse" is not an observation kind (one of `},
		{"the offending entry is named by position",
			valid + "audit:\n  auto_accept: [writable, nonsense]\n",
			`audit.auto_accept[1]: "nonsense" is not an observation kind`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.yaml), "tfpfgen.yaml")
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if cfg == nil {
					t.Fatal("nil config without error")
				}
				return
			}
			if err == nil {
				t.Fatalf("parsed without error, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestUnit_Config_AutoAcceptVocabularyComesFromTheRevisionStage: the valid set is
// not a copy kept in step by hand — it is `tfpfgen spec revise`'s own, so a
// config this package accepts is one that stage accepts. The refusal spells
// the whole set out, since a human choosing kinds needs to see the choices.
func TestUnit_Config_AutoAcceptVocabularyComesFromTheRevisionStage(t *testing.T) {
	kinds := revise.CompilableKinds()
	if !slices.Contains(kinds, "listResponseShape") {
		t.Errorf("the vocabulary %v omits listResponseShape", kinds)
	}
	if !slices.Equal(autoAcceptKinds(), kinds) {
		t.Errorf("autoAcceptKinds() = %v, want the revision stage's %v", autoAcceptKinds(), kinds)
	}

	_, err := Parse([]byte(valid+"audit:\n  auto_accept: [listResponse]\n"), "tfpfgen.yaml")
	if err == nil {
		t.Fatal("a mistyped observation kind parsed")
	}
	for _, kind := range kinds {
		if !strings.Contains(err.Error(), kind) {
			t.Errorf("the refusal does not offer %q:\n%v", kind, err)
		}
	}
}

func TestUnit_Config_EverySemanticProblemIsReportedAtOnce(t *testing.T) {
	broken := `version: 2
provider:
  name: ""
  registry_namespace: ""
generator:
  version: main
spec:
  document_url: ""
sdk:
  backend: nope
  backend_version: ""
auth:
  method: nope
`
	_, err := Parse([]byte(broken), "tfpfgen.yaml")
	if err == nil {
		t.Fatal("a config broken nine ways parsed cleanly")
	}
	for _, want := range []string{
		"version:", "provider.name", "provider.registry_namespace", "generator.version",
		"spec.document_url", "sdk.backend:", "sdk.backend_version", "auth.method",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("aggregate error is missing %q:\n%v", want, err)
		}
	}
}

func TestUnit_Config_DefaultsSurviveDecode(t *testing.T) {
	cfg, err := Parse([]byte(valid), "tfpfgen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SDK.ClientTypeName != "APIClient" {
		t.Errorf("client_type_name default = %q, want APIClient", cfg.SDK.ClientTypeName)
	}
	if !cfg.Audit.Enabled || cfg.Audit.NamePrefix != "tfpfgen" || cfg.Audit.MaxObjects != 25 || cfg.Audit.RateLimitRPS != 2 {
		t.Errorf("audit defaults = %+v", cfg.Audit)
	}
}

func TestUnit_Config_ExplicitValuesOverrideDefaults(t *testing.T) {
	cfg, err := Parse([]byte(valid+"audit:\n  enabled: false\n  max_objects: 3\n"), "tfpfgen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Audit.Enabled {
		t.Error("audit.enabled: false did not override the default")
	}
	if cfg.Audit.MaxObjects != 3 {
		t.Errorf("audit.max_objects = %d, want 3", cfg.Audit.MaxObjects)
	}
}

func TestUnit_Secrets_RolesAreFixedByAuthMethod(t *testing.T) {
	cases := map[string][]string{
		AuthBearerToken:             {SecretToken},
		AuthAPIKeyHeader:            {SecretToken},
		AuthBasic:                   {SecretUsername, SecretPassword},
		AuthOAuth2ClientCredentials: {SecretClientID, SecretClientSecret},
		AuthGitHubApp:               {SecretAppID, SecretAppPrivateKey},
		"unknown":                   nil,
	}
	for method, want := range cases {
		got := RequiredSecrets(method)
		if len(got) != len(want) {
			t.Fatalf("%s: RequiredSecrets = %v, want %v", method, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: RequiredSecrets = %v, want %v", method, got, want)
			}
		}
	}
}

func TestUnit_Secrets_MissingAreReportedAllAtOnce(t *testing.T) {
	empty := func(string) (string, bool) { return "", false }
	missing := MissingSecrets(AuthBasic, empty)
	if len(missing) != 2 || missing[0] != SecretUsername || missing[1] != SecretPassword {
		t.Fatalf("MissingSecrets = %v, want both basic-auth roles", missing)
	}

	onlyUser := func(name string) (string, bool) { return "value", name == SecretUsername }
	missing = MissingSecrets(AuthBasic, onlyUser)
	if len(missing) != 1 || missing[0] != SecretPassword {
		t.Fatalf("MissingSecrets = %v, want just the password role", missing)
	}

	set := func(string) (string, bool) { return "value", true }
	if missing = MissingSecrets(AuthBearerToken, set); missing != nil {
		t.Fatalf("MissingSecrets = %v, want none", missing)
	}
}

func TestUnit_Secrets_EmptyValueCountsAsMissing(t *testing.T) {
	setButEmpty := func(string) (string, bool) { return "", true }
	if missing := MissingSecrets(AuthBearerToken, setButEmpty); len(missing) != 1 {
		t.Fatalf("an empty secret value passed the presence check: %v", missing)
	}
}
