package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
)

const validConfig = `version: 1
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

// writeConfig drops a config file into a temp dir and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tfpfgen.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnit_ConfigValidate_ValidFileSummarisesAndExitsOK(t *testing.T) {
	path := writeConfig(t, validConfig)
	code, stdout, stderr := run(t, "config", "validate", "--file", path)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitOK, stderr)
	}
	for _, want := range []string{"is valid", "example", "kiota@1.34.1", "bearer_token"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("summary missing %q: %q", want, stdout)
		}
	}
}

func TestUnit_ConfigValidate_InvalidFileNamesTheProblemAndFails(t *testing.T) {
	path := writeConfig(t, strings.Replace(validConfig, "v0.1.0", "main", 1))
	code, _, stderr := run(t, "config", "validate", "--file", path)
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "generator.version") {
		t.Fatalf("stderr does not name the defective key: %q", stderr)
	}
}

func TestUnit_ConfigValidate_MissingFileFails(t *testing.T) {
	code, _, stderr := run(t, "config", "validate", "--file", filepath.Join(t.TempDir(), "absent.yaml"))
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "absent.yaml") {
		t.Fatalf("stderr does not name the missing file: %q", stderr)
	}
}

func TestUnit_ConfigValidate_SecretsFlagReportsEveryMissingRole(t *testing.T) {
	t.Setenv(config.SecretToken, "")
	path := writeConfig(t, validConfig)
	code, _, stderr := run(t, "config", "validate", "--file", path, "--secrets")
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, config.SecretToken) {
		t.Fatalf("stderr does not name the missing secret: %q", stderr)
	}
}

func TestUnit_ConfigValidate_SecretsFlagPassesWhenRolesAreSet(t *testing.T) {
	t.Setenv(config.SecretToken, "present")
	path := writeConfig(t, validConfig)
	code, _, stderr := run(t, "config", "validate", "--file", path, "--secrets")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitOK, stderr)
	}
}

func TestUnit_ConfigValidate_TrailingArgumentsAreRefused(t *testing.T) {
	code, _, stderr := run(t, "config", "validate", "stray")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "usage: tfpfgen config validate") {
		t.Fatalf("stderr missing verb usage: %q", stderr)
	}
}

func TestUnit_ConfigValidate_UnknownFlagIsUsageError(t *testing.T) {
	if code, _, _ := run(t, "config", "validate", "--no-such-flag"); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}
