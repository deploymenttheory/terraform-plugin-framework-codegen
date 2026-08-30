package providergen

import (
	"context"
	"os"
	"os/exec"
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
	// harness; once the toolchain has finalised go.mod and go.sum, as tree
	// verification does, the second finds them and binds in place. The
	// generated tree must not change between them; only the three files
	// the toolchain owns may.
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
	finaliseToolchainFiles(t, root)
	if _, err := os.Stat(filepath.Join(root, "go.sum")); err != nil {
		t.Fatalf("tidying wrote no go.sum: %v", err)
	}
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("in-place-bound Run: %v", err)
	}
	second, err := digestTree(root)
	if err != nil {
		t.Fatal(err)
	}
	toolchainOwned := map[string]bool{"go.mod": true, "go.sum": true, "manifest.json": true}
	for path, summary := range first {
		if toolchainOwned[path] {
			continue
		}
		if second[path] != summary {
			t.Errorf("%s differs between the harness-bound and in-place-bound runs", path)
		}
	}
	if len(first)+1 != len(second) {
		t.Errorf("file count differs between runs beyond the go.sum tidying wrote: %d -> %d", len(first), len(second))
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

// finaliseToolchainFiles does for a test tree what tree verification does
// for a real one: `go mod tidy` fetches the pinned dependencies and writes
// go.mod and go.sum, and the manifest is re-recorded to describe them. The
// module cache is tried first, so a warm machine stays offline; the proxy
// is the second attempt.
func finaliseToolchainFiles(t *testing.T, root string) {
	t.Helper()
	offline := exec.Command("go", "mod", "tidy")
	offline.Dir = root
	offline.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	if _, err := offline.CombinedOutput(); err != nil {
		runGo(t, root, "mod", "tidy")
	}
	if err := recordToolchainWritten(root); err != nil {
		t.Fatalf("recording go.mod and go.sum in the manifest: %v", err)
	}
}
