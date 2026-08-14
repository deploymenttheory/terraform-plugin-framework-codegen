package emit

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
)

// TestUnit_FromConfig_DerivesEveryFinishedValue proves the one derivation
// site produces the strings the templates interpolate.
func TestUnit_FromConfig_DerivesEveryFinishedValue(t *testing.T) {
	cfg := testConfig(config.BackendKiota, config.AuthOAuth2ClientCredentials)

	pc, err := FromConfig(cfg, "https://api.example.test/v1")
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	for field, want := range map[string]string{
		"Module":            "github.com/exampleco/terraform-provider-petstore",
		"ProviderName":      "petstore",
		"RegistryAddress":   "registry.terraform.io/exampleco/petstore",
		"EnvPrefix":         "TF_PETSTORE",
		"ClientType":        "APIClient",
		"ClientConstructor": "NewAPIClient",
		"SDKImport":         "github.com/exampleco/terraform-provider-petstore/internal/sdk",
		"GoVersion":         DefaultGoVersion,
		"DefaultEndpoint":   "https://api.example.test/v1",
		"DefaultTokenURL":   "https://login.example.test/oauth2/token",
	} {
		got := map[string]string{
			"Module":            pc.Module,
			"ProviderName":      pc.ProviderName,
			"RegistryAddress":   pc.RegistryAddress,
			"EnvPrefix":         pc.EnvPrefix,
			"ClientType":        pc.ClientType,
			"ClientConstructor": pc.ClientConstructor,
			"SDKImport":         pc.SDKImport,
			"GoVersion":         pc.GoVersion,
			"DefaultEndpoint":   pc.DefaultEndpoint,
			"DefaultTokenURL":   pc.DefaultTokenURL,
		}[field]
		if got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}

	if !pc.BackendKiota || pc.BackendOpenAPIGenerator {
		t.Error("the kiota config did not select exactly the kiota backend")
	}
	if !pc.AuthOAuth2ClientCredentials {
		t.Error("the oauth2 config did not select the oauth2 method")
	}
}

// TestUnit_FromConfig_RefusesWhatItCannotDerive proves each gap fails
// with a message naming the config key.
func TestUnit_FromConfig_RefusesWhatItCannotDerive(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr string
	}{
		{
			name:    "missing provider name",
			mutate:  func(c *config.Config) { c.Provider.Name = "" },
			wantErr: "provider.name",
		},
		{
			name:    "missing registry namespace",
			mutate:  func(c *config.Config) { c.Provider.RegistryNamespace = "" },
			wantErr: "provider.registry_namespace",
		},
		{
			name:    "missing client type name",
			mutate:  func(c *config.Config) { c.SDK.ClientTypeName = "" },
			wantErr: "sdk.client_type_name",
		},
		{
			name:    "unknown backend",
			mutate:  func(c *config.Config) { c.SDK.Backend = "swagger-codegen" },
			wantErr: "sdk.backend",
		},
		{
			name:    "unknown auth method",
			mutate:  func(c *config.Config) { c.Auth.Method = "kerberos" },
			wantErr: "auth.method",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(config.BackendKiota, config.AuthBearerToken)
			tc.mutate(cfg)
			_, err := FromConfig(cfg, "")
			if err == nil {
				t.Fatal("FromConfig accepted the broken config")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}

	t.Run("api_key_header without a header name", func(t *testing.T) {
		cfg := testConfig(config.BackendKiota, config.AuthAPIKeyHeader)
		cfg.Auth.APIKeyHeader = ""
		if _, err := FromConfig(cfg, ""); err == nil {
			t.Fatal("FromConfig accepted the api_key_header method without a header name")
		}
	})
}

// TestUnit_EnvPrefix_IsTFThenTheCollapsedName proves the operator prefix
// is TF_ then the uppercased name, separators collapsed to underscores.
func TestUnit_EnvPrefix_IsTFThenTheCollapsedName(t *testing.T) {
	for name, want := range map[string]string{
		"petstore":     "TF_PETSTORE",
		"thousandeyes": "TF_THOUSANDEYES",
		"jamf-pro":     "TF_JAMF_PRO",
		"a--b_c.d":     "TF_A_B_C_D",
		"-edge-":       "TF_EDGE",
		"snake_case":   "TF_SNAKE_CASE",
	} {
		if got := envPrefix(name); got != want {
			t.Errorf("envPrefix(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestUnit_ProviderCoreCheck_NamesEveryProblemAtOnce proves the context
// check reports all gaps in one message, config-style.
func TestUnit_ProviderCoreCheck_NamesEveryProblemAtOnce(t *testing.T) {
	err := ProviderCore{APIKeyHeader: ""}.check()
	if err == nil {
		t.Fatal("check accepted the zero context")
	}
	for _, want := range []string{"Module is empty", "exactly one backend", "exactly one auth"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("check error %q does not mention %q", err, want)
		}
	}

	pc := ProviderCore{
		Module: "m", ProviderName: "p", RegistryAddress: "r", EnvPrefix: "TF_P",
		ClientType: "C", ClientConstructor: "NewC", SDKImport: "m/internal/sdk", GoVersion: "1.25",
		BackendKiota: true, AuthAPIKeyHeader: true,
	}
	if err := pc.check(); err == nil || !strings.Contains(err.Error(), "APIKeyHeader") {
		t.Fatalf("check did not report the missing api key header: %v", err)
	}

	pc.APIKeyHeader = "X-Api-Key"
	if err := pc.check(); err != nil {
		t.Fatalf("check refused a complete context: %v", err)
	}
}
