package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// generatedSDKRepo builds a provider-repo root whose SDK tree `sdk generate`
// just committed — the drift-free baseline.
func generatedSDKRepo(t *testing.T) string {
	t.Helper()
	root := sdkRepo(t)
	if code, _, stderr := run(t, "sdk", "generate"); code != ExitOK {
		t.Fatalf("the baseline generation failed: %s", stderr)
	}
	return root
}

func TestUnit_SDKVerify_ConfirmsAnIdenticalTree(t *testing.T) {
	generatedSDKRepo(t)

	code, stdout, stderr := run(t, "sdk", "verify")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitOK, stderr)
	}
	if lines := strings.Count(strings.TrimRight(stdout, "\n"), "\n") + 1; lines != 1 {
		t.Errorf("a clean verify confirms in one line, got %d:\n%s", lines, stdout)
	}
	for _, want := range []string{"internal/sdk", "kiota 1.2.3", "no drift"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestUnit_SDKVerify_FailsListingEachDriftedPathSorted(t *testing.T) {
	root := generatedSDKRepo(t)
	if err := os.WriteFile(filepath.Join(root, "internal", "sdk", "client.go"),
		[]byte("package sdk // edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "sdk", "extra.go"),
		[]byte("package sdk\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := run(t, "sdk", "verify")
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d (stdout: %s)", code, ExitFailure, stdout)
	}
	want := []string{
		"changed: internal/sdk/client.go",
		"hand-edited: internal/sdk/client.go",
		"extra: internal/sdk/extra.go",
	}
	if got := strings.Split(strings.TrimRight(stdout, "\n"), "\n"); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("stdout = %v, want %v", got, want)
	}
	for _, wanted := range []string{"drifted", "3 paths", "tfpfgen sdk generate"} {
		if !strings.Contains(stderr, wanted) {
			t.Errorf("stderr missing %q: %s", wanted, stderr)
		}
	}
}

func TestUnit_SDKVerify_FailsWithoutARevisedSpecNamingTheVerb(t *testing.T) {
	root := generatedSDKRepo(t)
	if err := os.Remove(filepath.Join(root, "spec", "revised.yaml")); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := run(t, "sdk", "verify")
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "tfpfgen spec revise") {
		t.Fatalf("stderr does not say what to run: %q", stderr)
	}
}

func TestUnit_SDKVerify_FailsOnABrokenConfig(t *testing.T) {
	root := sdkRepo(t)
	if err := os.WriteFile(filepath.Join(root, "tfpfgen.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := run(t, "sdk", "verify")
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitFailure, stderr)
	}
	if !strings.Contains(stderr, "tfpfgen.yaml") {
		t.Fatalf("stderr does not name the config: %q", stderr)
	}
}

func TestUnit_SDKVerify_RefusesTrailingArguments(t *testing.T) {
	code, _, stderr := run(t, "sdk", "verify", "extra")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "usage: tfpfgen sdk verify") {
		t.Fatalf("stderr missing the verb usage line: %q", stderr)
	}
}
