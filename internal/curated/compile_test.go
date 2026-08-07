package curated_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/curated"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/generate"
)

// providerModule is the module skeleton the generated tree is written into: a
// go.mod, a go.sum, and the fixture's hand-written SDK.
//
// It is testdata rather than part of the package's exported surface because it
// serves exactly one test. Nothing outside this file needs a compilable provider
// root, and exporting one would invite callers to depend on the SDK's shape,
// which is an implementation detail of proving the fixture compiles.
const providerModule = "testdata/providermod"

// TestUnit_Curated_TheGeneratedTreeCompiles is the bar the fixture is actually
// held to.
//
// Loading and validating a blueprint proves the document is well formed; it does
// not prove the emitter can turn it into Go. Every defect the curated layer is
// most likely to introduce -- a convert helper that does not exist, a state
// mapper whose signature disagrees with its caller, an SDK accessor named for a
// field the model does not have -- is invisible until something compiles the
// output. So this generates the whole provider, scaffolds the shell around it,
// drops in the fixture's SDK, and hands the result to the Go toolchain.
//
// The SDK is hand-written rather than kiota-generated. A real generated SDK for
// three resources is thousands of lines of serialisation the emitter never names
// and no reader would check, and committing one would make this fixture larger
// than the thing it tests; what the generated provider binds to is the builder
// chains and the model accessors, and those are what testdata/providermod
// carries. Its go.mod and go.sum are committed, so this needs no network beyond
// a cold module cache.
func TestUnit_Curated_TheGeneratedTreeCompiles(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("compiling a provider tree pulls the plugin framework in; skipped under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("the go toolchain is not on PATH: %v", err)
	}

	blueprintDir := curated.Dir(t)

	bp, err := blueprint.LoadDir(blueprintDir)
	if err != nil {
		t.Fatalf("loading the curated fixture: %v", err)
	}

	root := t.TempDir()
	copyTree(t, providerModule, root)

	// The shell first, because the generated resources import it. Force, exactly
	// as `provider scaffold` does: the shell adopts files rather than refusing
	// ones it did not write.
	params, err := generate.ShellParamsFrom(bp.Provider)
	if err != nil {
		t.Fatalf("ShellParamsFrom: %v", err)
	}

	shell, err := generate.BuildShell(
		params,
		filepath.ToSlash(filepath.Join(blueprintDir, "provider.blueprint.json")),
		generate.ProviderBlockDigest(bp),
	)
	if err != nil {
		t.Fatalf("BuildShell: %v", err)
	}
	if _, err := generate.Write(shell, generate.WriteOptions{Root: root, Force: true}); err != nil {
		t.Fatalf("writing the shell: %v", err)
	}

	gen, err := generate.New()
	if err != nil {
		t.Fatalf("generate.New: %v", err)
	}

	plan, err := gen.Build(bp, generate.BuildOptions{BlueprintPath: blueprintDir})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Files) == 0 {
		t.Fatal("the fixture generated no files")
	}
	if _, err := generate.Write(plan, generate.WriteOptions{Root: root, Force: true}); err != nil {
		t.Fatalf("writing the provider: %v", err)
	}

	// AssertGoMod is the same offline precondition the CLI's postcheck runs, and
	// it is cheap: an SDK the go.mod cannot resolve makes the compiler's message
	// about a missing package rather than about a missing requirement.
	if err := generate.AssertGoMod(root, bp); err != nil {
		t.Fatalf("AssertGoMod: %v", err)
	}

	// Build before vet. Vet compiles the generated _test.go files too, which is
	// worth having, but a build failure is the clearer message and vet would
	// report it second-hand.
	runGo(t, root, "build", "./...")
	runGo(t, root, "vet", "./...")
}

// runGo runs one go subcommand in the generated tree.
//
// A failure that is plainly the module cache being cold offline skips rather than
// fails: the fixture's go.sum pins everything, so on a warm cache or a networked
// machine this always runs, and a developer on a train should not read a red
// suite as a broken fixture. Anything else fails with the toolchain's own output,
// because that output is the bug report.
func runGo(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("go", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}

	msg := string(out)
	for _, offline := range []string{
		"module lookup disabled",
		"dial tcp",
		"no such host",
		"connection refused",
		"proxy.golang.org",
	} {
		if strings.Contains(msg, offline) {
			t.Skipf("go %s needs modules this machine has neither cached nor can fetch:\n%s",
				strings.Join(args, " "), msg)
		}
	}

	t.Fatalf("go %s failed in the generated tree:\n%s", strings.Join(args, " "), msg)
}

// copyTree copies the module skeleton out of testdata.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()

	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}

		body, err := os.ReadFile(path) //nolint:gosec // paths come from walking committed testdata
		if err != nil {
			return err
		}

		return os.WriteFile(target, body, 0o600)
	})
	if err != nil {
		t.Fatalf("copying %s: %v", src, err)
	}
}
