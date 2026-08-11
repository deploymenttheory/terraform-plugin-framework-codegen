package providergen

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/emit"
)

// sdkRelPath is where the provider module carries its generated SDK — the
// spelling pc.SDKImport appends to the module path.
const sdkRelPath = "internal/sdk"

// bindContext answers where go/types should load the SDK from so its
// packages resolve at the import path the bindings name: the provider
// module path plus internal/sdk.
//
// A repo that already carries a go.mod is its own context: the SDK loads in
// place, against the committed go.mod and go.sum, and nothing is copied. A
// repo not yet generated has no go.mod — the first `provider generate` is
// what writes it — so the SDK is copied into a temporary module harness
// declaring exactly the module path the bindings expect, and `go mod tidy`
// resolves the SDK's own imports there before anything type-checks.
//
// The returned cleanup releases whatever the answer allocated.
func bindContext(root, module, sdkAbs string) (string, func(), error) {
	noop := func() {}

	goMod := filepath.Join(root, "go.mod")
	if data, err := os.ReadFile(goMod); err == nil { //nolint:gosec // the fixed name under the repo root
		declared := moduleLine(data)
		if declared != module {
			return "", noop, fmt.Errorf(
				"%s declares module %q but tfpfgen.yaml derives %q; align provider.name and provider.registry_namespace with the repo or regenerate go.mod",
				goMod, declared, module)
		}
		if inPlace, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(sdkRelPath))); err == nil && inPlace == sdkAbs {
			return sdkAbs, noop, nil
		}
	} else if !os.IsNotExist(err) {
		return "", noop, err
	}

	harness, err := os.MkdirTemp("", "tfpfgen-provider-bind-")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { os.RemoveAll(harness) } //nolint:errcheck // best-effort temp cleanup

	minimal := fmt.Sprintf("module %s\n\ngo %s\n", module, emit.DefaultGoVersion)
	if err := os.WriteFile(filepath.Join(harness, "go.mod"), []byte(minimal), 0o600); err != nil {
		cleanup()
		return "", noop, err
	}
	if data, err := os.ReadFile(filepath.Join(root, "go.sum")); err == nil { //nolint:gosec // the fixed name under the repo root
		if err := os.WriteFile(filepath.Join(harness, "go.sum"), data, 0o600); err != nil {
			cleanup()
			return "", noop, err
		}
	}

	target := filepath.Join(harness, filepath.FromSlash(sdkRelPath))
	if err := copyTree(sdkAbs, target); err != nil {
		cleanup()
		return "", noop, err
	}

	// The harness go.mod declares no requirements, and a real SDK imports
	// beyond the standard library — a kiota tree pulls the kiota runtime on
	// every page. `go mod tidy` here is what lets the first generate of a
	// repo, the run with no committed go.mod, type-check such an SDK at
	// all; without it the bind fails on the SDK's own imports. The go tool
	// is already a hard requirement of binding (go/types loads through it),
	// so there is no toolchain-less path to preserve.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = harness
	if out, err := tidy.CombinedOutput(); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("resolving the SDK's dependencies in the bind harness (go mod tidy): %v\n%s", err, out)
	}

	return target, cleanup, nil
}

// moduleLine reads the module path a go.mod declares.
func moduleLine(data []byte) string {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// copyTree copies every regular file under src to the same relative path
// under dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := os.ReadFile(p) //nolint:gosec // walking the operator-supplied SDK tree
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}
