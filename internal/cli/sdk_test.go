package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sdkTestConfig = `version: 1
provider:
  name: example
  registry_namespace: example-org
generator:
  version: v1.0.0
spec:
  document_url: https://example.test/openapi.yaml
sdk:
  backend: kiota
  backend_version: 1.2.3
auth:
  method: bearer_token
`

const sdkTestRevised = `openapi: 3.0.3
info:
  title: sample
  version: 1.0.0
paths:
  /widgets:
    get:
      responses:
        "200":
          description: ok
`

// kiotaStubScript answers --version with 1.2.3 and writes a tiny tree.
const kiotaStubScript = `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "1.2.3+heads/main"
  exit 0
fi
out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--output" ]; then out="$a"; fi
  prev="$a"
done
mkdir -p "$out"
printf 'package sdk\n' > "$out/client.go"
printf '{"descriptionHash": "AB", "descriptionLocation": "/tmp/x.yaml", "kiotaVersion": "1.2.3"}\n' > "$out/kiota-lock.json"
`

// sdkRepo builds a provider-repo root with a config, a revised spec, and a
// kiota stub on PATH, and makes it the working directory.
func sdkRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)

	if err := os.WriteFile(filepath.Join(root, "tfpfgen.yaml"), []byte(sdkTestConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "spec"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spec", "revised.yaml"), []byte(sdkTestRevised), 0o600); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "kiota"), []byte(kiotaStubScript), 0o755); err != nil { //nolint:gosec // a test stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root
}

func TestUnit_SDKGenerate_GeneratesTheTreeAndTheManifest(t *testing.T) {
	root := sdkRepo(t)

	code, stdout, stderr := run(t, "sdk", "generate")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitOK, stderr)
	}
	for _, want := range []string{"generated", "internal/sdk", "kiota 1.2.3", "spec/revised.yaml"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "sdk", "client.go")); err != nil {
		t.Errorf("the SDK tree was not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); err != nil {
		t.Errorf("the manifest was not written: %v", err)
	}
}

func TestUnit_SDKGenerate_FailsWithoutARevisedSpecNamingTheVerb(t *testing.T) {
	root := sdkRepo(t)
	if err := os.Remove(filepath.Join(root, "spec", "revised.yaml")); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := run(t, "sdk", "generate")
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "tfpfgen spec revise") {
		t.Fatalf("stderr does not say what to run: %q", stderr)
	}
}

func TestUnit_SDKGenerate_FailsOnABrokenConfig(t *testing.T) {
	root := sdkRepo(t)
	if err := os.WriteFile(filepath.Join(root, "tfpfgen.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := run(t, "sdk", "generate")
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitFailure, stderr)
	}
	if !strings.Contains(stderr, "tfpfgen.yaml") {
		t.Fatalf("stderr does not name the config: %q", stderr)
	}
}

func TestUnit_SDKGenerate_RefusesTrailingArguments(t *testing.T) {
	code, _, stderr := run(t, "sdk", "generate", "extra")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "usage: tfpfgen sdk generate") {
		t.Fatalf("stderr missing the verb usage line: %q", stderr)
	}
}
