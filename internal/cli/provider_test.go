package cli

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/revise"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/store"
)

// providerRepo stands up a provider repo from the curated fixture through
// the real chain — spec import, spec revise, the kiota stub SDK at
// internal/sdk, the fixture config as tfpfgen.yaml — and makes it the
// working directory, which is what the provider verbs' defaults assume.
func providerRepo(t *testing.T) string {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("..", "..", "testdata", "curated"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()

	document, err := os.ReadFile(filepath.Join(fixture, "openapi.yaml"))
	if err != nil {
		t.Fatalf("reading the curated document: %v", err)
	}
	if _, err := store.Import(filepath.Join(root, "spec"), document, "testdata/curated/openapi.yaml"); err != nil {
		t.Fatalf("spec import: %v", err)
	}
	if _, err := revise.WriteRevision(filepath.Join(root, "spec")); err != nil {
		t.Fatalf("spec revise: %v", err)
	}

	if err := copyDir(filepath.Join(fixture, "kiota", "sdk"), filepath.Join(root, "internal", "sdk")); err != nil {
		t.Fatalf("standing the stub SDK: %v", err)
	}
	configuration, err := os.ReadFile(filepath.Join(fixture, "kiota", "tfpfgen.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tfpfgen.yaml"), configuration, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Chdir(root)
	return root
}

// copyDir copies every regular file under src to the same relative path
// under dst.
func copyDir(source, destination string) error {
	return filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(source, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(destination, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, rerr := os.ReadFile(p) //nolint:gosec // walking the committed fixture
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0o600)
	})
}

func TestUnit_ProviderGenerateThenVerify_RoundTripsClean(t *testing.T) {
	root := providerRepo(t)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"provider", "generate", "--verify-tree=false"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("generate exit = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "3 resources, 5 datasources, 3 list resources, 1 actions") {
		t.Errorf("generate output does not report the fixture's entity counts:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "tree verification skipped: tree verification disabled") {
		t.Errorf("generate output does not report the tree-verification decision:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "manifest.json")); err != nil {
		t.Errorf("generate left no manifest: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"provider", "verify"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("verify exit = %d, stderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no drift") {
		t.Errorf("clean verify does not confirm:\n%s", stdout.String())
	}
}

func TestUnit_ProviderVerify_DriftListsPathsAndFails(t *testing.T) {
	root := providerRepo(t)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"provider", "generate", "--verify-tree=false"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("generate exit = %d, stderr:\n%s", code, stderr.String())
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"provider", "verify"}, &stdout, &stderr)
	if code != ExitFailure {
		t.Fatalf("verify exit = %d on a drifted tree", code)
	}
	if !strings.Contains(stdout.String(), "changed: main.go") ||
		!strings.Contains(stdout.String(), "hand-edited: main.go") {
		t.Errorf("verify output does not name the drift:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "tfpfgen provider generate") {
		t.Errorf("verify failure does not name the fix:\n%s", stderr.String())
	}
}

func TestUnit_ProviderGenerate_PrintIRDumpsJSONAndGeneratesNothing(t *testing.T) {
	root := providerRepo(t)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"provider", "generate", "--print-ir"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d, stderr:\n%s", code, stderr.String())
	}

	var decoded struct {
		Provider struct{ Name string }
	}
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("--print-ir did not print JSON: %v", err)
	}
	if decoded.Provider.Name != "fixture" {
		t.Errorf("dumped provider = %q", decoded.Provider.Name)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); !os.IsNotExist(err) {
		t.Error("--print-ir generated files")
	}
}

func TestUnit_ProviderGenerate_MissingRevisedFails(t *testing.T) {
	root := providerRepo(t)
	if err := os.Remove(filepath.Join(root, "spec", "revised.yaml")); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"provider", "generate", "--verify-tree=false"}, &stdout, &stderr); code != ExitFailure {
		t.Fatalf("exit = %d; a missing revised document is a failure", code)
	}
	if !strings.Contains(stderr.String(), "tfpfgen spec revise") {
		t.Errorf("the failure does not name the verb to run:\n%s", stderr.String())
	}
}

func TestUnit_ProviderGenerate_MissingConfigFails(t *testing.T) {
	t.Chdir(t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"provider", "generate"}, &stdout, &stderr); code != ExitFailure {
		t.Fatalf("exit = %d; a missing tfpfgen.yaml is a failure", code)
	}
}

func TestUnit_ProviderVerbs_TrailingArgumentsAreUsage(t *testing.T) {
	for _, verb := range []string{"generate", "verify"} {
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"provider", verb, "extra"}, &stdout, &stderr); code != ExitUsage {
			t.Errorf("provider %s extra: exit = %d, want %d", verb, code, ExitUsage)
		}
	}
}

func TestUnit_Provider_BareGroupListsItsVerbs(t *testing.T) {
	// A bare noun answers with its verbs, the way the other groups do.
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"provider"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, verb := range []string{"generate", "verify"} {
		if !strings.Contains(stdout.String(), verb) {
			t.Errorf("the group help does not list %s:\n%s", verb, stdout.String())
		}
	}
}
