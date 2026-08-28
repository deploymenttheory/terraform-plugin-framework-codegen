package emit

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
)

// TestUnit_FromConfig_DerivesEveryFinishedValue proves the one derivation
// site produces the strings the templates interpolate.
func TestUnit_FromConfig_DerivesEveryFinishedValue(t *testing.T) {
	configuration := testConfig(config.BackendKiota, config.AuthOAuth2ClientCredentials)

	pc, err := FromConfig(configuration, "https://api.example.test/v1")
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	for field, want := range map[string]string{
		"Module":            "github.com/exampleco/terraform-provider-petstore",
		"ProviderName":      "petstore",
		"RegistryAddress":   "registry.terraform.io/exampleco/petstore",
		"EnvPrefix":         "PETSTORE",
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

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := testConfig(config.BackendKiota, config.AuthBearerToken)
			testCase.mutate(configuration)
			_, err := FromConfig(configuration, "")
			if err == nil {
				t.Fatal("FromConfig accepted the broken config")
			}
			if !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("error %q does not mention %q", err, testCase.wantErr)
			}
		})
	}

	t.Run("api_key_header without a header name", func(t *testing.T) {
		configuration := testConfig(config.BackendKiota, config.AuthAPIKeyHeader)
		configuration.Auth.APIKeyHeader = ""
		if _, err := FromConfig(configuration, ""); err == nil {
			t.Fatal("FromConfig accepted the api_key_header method without a header name")
		}
	})
}

// TestUnit_EnvPrefix_IsTheBareCollapsedName proves the operator prefix is
// the uppercased name with separators collapsed to underscores, and nothing
// in front of it — the spelling every published provider uses.
func TestUnit_EnvPrefix_IsTheBareCollapsedName(t *testing.T) {
	for name, want := range map[string]string{
		"petstore":     "PETSTORE",
		"thousandeyes": "THOUSANDEYES",
		"jamf-pro":     "JAMF_PRO",
		"a--b_c.d":     "A_B_C_D",
		"-edge-":       "EDGE",
		"snake_case":   "SNAKE_CASE",
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
		Module: "m", ProviderName: "p", RegistryAddress: "r", EnvPrefix: "P",
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
