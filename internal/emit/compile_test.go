package emit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
)

// TestUnit_RenderShell_TheRenderedTreeCompiles is the full-strength gate:
// render the shell for a fictional provider, stand a hand-written stub SDK
// where `sdk generate` would put the real one, and hold the tree to `go
// mod tidy`, `go build` and `go vet` against the real dependency pins.
//
// Resolving those pins needs the module proxy, so a failure whose output
// carries an offline signature skips rather than fails — a developer on a
// train should not read a red suite as a broken template — while CI, which
// is online, gets the real answer. Everything the compile would prove
// about template syntax is already proven without the network by the
// render tests' gofmt and parse gates.
func TestUnit_RenderShell_TheRenderedTreeCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the toolchain compile in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}

	cases := []struct {
		name    string
		backend string
		method  string
		stub    string
	}{
		{"kiota with oauth2", config.BackendKiota, config.AuthOAuth2ClientCredentials, "kiota"},
		{"kiota with api key header", config.BackendKiota, config.AuthAPIKeyHeader, "kiota"},
		{"openapi-generator with bearer", config.BackendOpenAPIGenerator, config.AuthBearerToken, "openapigenerator"},
		{"openapi-generator with basic", config.BackendOpenAPIGenerator, config.AuthBasic, "openapigenerator"},
		{"openapi-generator with github app", config.BackendOpenAPIGenerator, config.AuthGitHubApp, "openapigenerator"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sh, err := FromConfig(testConfig(tc.backend, tc.method), "https://api.example.test")
			if err != nil {
				t.Fatalf("FromConfig: %v", err)
			}
			files, err := RenderShell(sh)
			if err != nil {
				t.Fatalf("RenderShell: %v", err)
			}

			root := t.TempDir()
			if _, err := Write(root, files); err != nil {
				t.Fatalf("Write: %v", err)
			}
			installStubSDK(t, tc.stub, root)

			runGo(t, root, "mod", "tidy")
			runGo(t, root, "build", "./...")
			runGo(t, root, "vet", "./...")
		})
	}
}

// installStubSDK copies the named stub into the tree at the path the
// shell's SDK import expects.
func installStubSDK(t *testing.T, name, root string) {
	t.Helper()

	src := filepath.Join("testdata", "sdkstub", name, "client.go")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading the %s stub: %v", name, err)
	}

	dir := filepath.Join(root, "internal", "sdk")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "client.go"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// offlineSignatures are the toolchain messages that mean "no network",
// not "broken template".
var offlineSignatures = []string{
	"module lookup disabled",
	"dial tcp",
	"no such host",
	"connection refused",
	"i/o timeout",
	"TLS handshake timeout",
	"proxy.golang.org",
	"sum.golang.org",
}

// runGo runs one toolchain command in dir, failing the test with the
// toolchain's own words — or skipping when those words say offline.
func runGo(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}

	for _, signature := range offlineSignatures {
		if strings.Contains(string(out), signature) {
			t.Skipf("go %s needs the network and cannot reach it:\n%s", strings.Join(args, " "), out)
		}
	}

	t.Fatalf("go %s:\n%s", strings.Join(args, " "), out)
}
