package config

import (
	"os"
	"strings"
	"testing"
)

// docsPath is where the generated reference is committed, relative to this
// package.
const docsPath = "../../docs/config.md"

// allLeafPaths flattens the schema to the set of dotted leaf keys.
func allLeafPaths(t *testing.T) map[string]bool {
	t.Helper()
	paths := map[string]bool{}
	for _, s := range sections() {
		for _, l := range s.leaves {
			paths[l.path] = true
		}
	}
	if len(paths) == 0 {
		t.Fatal("schema walk found no leaf keys")
	}
	return paths
}

func TestUnit_Config_DescriptionsMatchSchemaBothWays(t *testing.T) {
	paths := allLeafPaths(t)
	for path := range paths {
		if _, ok := descriptions[path]; !ok {
			t.Errorf("leaf key %q has no description entry", path)
		}
	}
	for key := range descriptions {
		if !paths[key] {
			t.Errorf("description entry %q matches no leaf key in the schema — dead docs", key)
		}
	}
}

func TestUnit_Config_EnumAndDefaultKeysExistInSchema(t *testing.T) {
	paths := allLeafPaths(t)
	for key := range allowedValues() {
		if !paths[key] {
			t.Errorf("allowed-values entry %q matches no leaf key in the schema", key)
		}
	}
	for key := range defaultValues() {
		if !paths[key] {
			t.Errorf("setDefaults registers %q, which matches no leaf key in the schema", key)
		}
	}
}

func TestUnit_Config_ReferenceCarriesDefaultsEnumsAndSecrets(t *testing.T) {
	ref := Reference()
	for _, want := range []string{
		// Defaults, rendered from the same viper instance Load uses.
		"`\"APIClient\"`", "`\"tfpfgen\"`", "`25`", "`2`", "`true`",
		// The closed sets.
		BackendKiota, BackendOpenAPIGenerator,
		AuthBearerToken, AuthAPIKeyHeader, AuthBasic, AuthOAuth2ClientCredentials, AuthGitHubApp,
		// The role→secret contract.
		SecretToken, SecretClientID, SecretClientSecret,
		SecretUsername, SecretPassword, SecretAppID, SecretAppPrivateKey,
	} {
		if !strings.Contains(ref, want) {
			t.Errorf("Reference() does not mention %q", want)
		}
	}
	for path := range allLeafPaths(t) {
		if !strings.Contains(ref, "`"+path+"`") {
			t.Errorf("Reference() does not document leaf key %q", path)
		}
	}
}

func TestUnit_Config_ReferenceMatchesDocs(t *testing.T) {
	got := Reference()
	if os.Getenv("UPDATE_DOCS") == "1" {
		if err := os.WriteFile(docsPath, []byte(got), 0o644); err != nil {
			t.Fatalf("rewriting %s: %v", docsPath, err)
		}
	}
	want, err := os.ReadFile(docsPath)
	if err != nil {
		t.Fatalf("reading %s: %v", docsPath, err)
	}
	if string(want) != got {
		t.Errorf("%s is stale relative to the schema; regenerate with UPDATE_DOCS=1 go test ./internal/config -run TestUnit_Config_ReferenceMatchesDocs", docsPath)
	}
}
