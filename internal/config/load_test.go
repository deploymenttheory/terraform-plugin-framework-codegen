package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnit_Config_LoadReadsAndValidatesAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tfpfgen.yaml")
	if err := os.WriteFile(path, []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	configuration, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Provider.Name != "example" {
		t.Fatalf("provider.name = %q, want example", configuration.Provider.Name)
	}
}

func TestUnit_Config_LoadNamesAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "absent.yaml") {
		t.Fatalf("Load error = %v, want it to name the missing file", err)
	}
}

func TestUnit_Config_RemainingRefusals(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"uppercase registry namespace", replace(t, "registry_namespace:", "registry_namespace: DeploymentTheory"),
			"provider.registry_namespace"},
		{"empty sdk backend", replace(t, "backend:", "backend: \"\""), "sdk.backend: required"},
		{"explicitly empty client_type_name", strings.Replace(valid,
			"backend_version: \"1.34.1\"", "backend_version: \"1.34.1\"\n  client_type_name: \"\"", 1),
			"sdk.client_type_name: must not be empty"},
		{"empty auth method", replace(t, "method:", "method: \"\""), "auth.method: required"},
		{"unknown key with no plausible neighbour", valid + "zzqqxxyy: true\n", `unknown key "zzqqxxyy"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml), "tfpfgen.yaml")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
