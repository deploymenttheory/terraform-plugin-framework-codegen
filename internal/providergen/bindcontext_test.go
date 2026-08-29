package providergen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnit_BindContext_ModuleMismatchIsRefused(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/somewhere-else\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "example.com/somewhere-else") ||
		!strings.Contains(err.Error(), "github.com/example-org/terraform-provider-fixture") {
		t.Fatalf("err = %v; a go.mod disagreeing with the config names both modules", err)
	}
}

func TestUnit_BindContext_HarnessAndInPlaceAgree(t *testing.T) {
	// The first generate has no go.mod and binds through the temporary
	// harness; the second finds the rendered go.mod and binds in place.
	// The tree must not change between them.
	root, opts := curatedRepo(t, "kiota")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("harness-bound Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the first run rendered no go.mod: %v", err)
	}

	first, err := digestTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("in-place-bound Run: %v", err)
	}
	second, err := digestTree(root)
	if err != nil {
		t.Fatal(err)
	}
	for path, summary := range first {
		if second[path] != summary {
			t.Errorf("%s differs between the harness-bound and in-place-bound runs", path)
		}
	}
	if len(first) != len(second) {
		t.Errorf("file count differs between runs: %d -> %d", len(first), len(second))
	}
}

func TestUnit_BindContext_CustomSDKDirBindsThroughTheHarness(t *testing.T) {
	// An SDK parked outside internal/sdk still binds: the harness stands
	// it at the import path the module expects.
	root, opts := curatedRepo(t, "kiota")
	if err := os.Rename(filepath.Join(root, "internal", "sdk"), filepath.Join(root, "sdktree")); err != nil {
		t.Fatal(err)
	}
	opts.SDKDir = "sdktree"

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run with --sdk sdktree: %v", err)
	}
}

func TestUnit_ModuleLine_ReadsTheDeclaration(t *testing.T) {
	if got := moduleLine([]byte("// a comment\nmodule example.com/mod\n\ngo 1.25\n")); got != "example.com/mod" {
		t.Errorf("moduleLine = %q", got)
	}
	if got := moduleLine([]byte("go 1.25\n")); got != "" {
		t.Errorf("moduleLine on a module-less file = %q", got)
	}
}

func TestUnit_BindContext_HarnessSurfacesAnUnresolvableSDKDependency(t *testing.T) {
	// An SDK importing a module the proxy cannot serve must fail the bind
	// with the harness tidy's own explanation, not a bare type-check error.
	root, opts := curatedRepo(t, "kiota")
	poison := "package sdk\n\nimport _ \"example.invalid/unresolvable\"\n"
	if err := os.WriteFile(filepath.Join(root, "internal", "sdk", "poison.go"), []byte(poison), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPROXY", "off")

	_, err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "bind harness") {
		t.Fatalf("err = %v, want the harness tidy refusal", err)
	}
}
