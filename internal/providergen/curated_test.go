package providergen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/revise"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/store"
)

// The curated fixture is the chain's offline end-to-end gate: a committed, fictional
// OpenAPI document (testdata/curated at the repo root) plus one
// hand-written stub SDK per dialect, driven through the real verbs — the
// document is imported and revised with the spec packages, the stub stands
// where `sdk generate` would put the real tree, and Run generates the
// complete provider. Everything here runs offline; only the toolchain
// compile at the end needs the module proxy and skips without it.

// curatedDialects names each dialect's fixture directory.
var curatedDialects = []string{"kiota", "openapi-generator"}

// curatedDir is the committed fixture's location relative to this package.
func curatedDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "curated"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// curatedRepo stands up one provider repo from the fixture through the real
// chain: spec import, spec revise, the stub SDK at internal/sdk. It returns
// the repo root and the ready providergen options.
func curatedRepo(t *testing.T, dialect string) (string, Options) {
	t.Helper()
	fixture := curatedDir(t)
	root := t.TempDir()

	document, err := os.ReadFile(filepath.Join(fixture, "openapi.yaml"))
	if err != nil {
		t.Fatalf("reading the curated document: %v", err)
	}
	specDir := filepath.Join(root, "spec")
	if _, err := store.Import(specDir, document, "testdata/curated/openapi.yaml"); err != nil {
		t.Fatalf("spec import: %v", err)
	}
	if _, err := revise.WriteRevision(specDir); err != nil {
		t.Fatalf("spec revise: %v", err)
	}

	if err := copyTree(filepath.Join(fixture, dialect, "sdk"), filepath.Join(root, "internal", "sdk")); err != nil {
		t.Fatalf("standing the stub SDK: %v", err)
	}

	configuration, err := config.Load(filepath.Join(fixture, dialect, "tfpfgen.yaml"))
	if err != nil {
		t.Fatalf("loading the fixture config: %v", err)
	}

	return root, Options{
		Config:  configuration,
		SpecDir: specDir,
		SDKDir:  "internal/sdk",
		Root:    root,
	}
}

// TestUnit_Run_CuratedFixtureGeneratesTheCompleteTree drives the whole
// offline chain for both dialects and holds the result to the fixture's
// known shape: three resources, a companion datasource for each plus the
// key-addressed one and the list-only one, the custom action, and nothing
// pruned, because the stub SDKs carry everything the document promises.
func TestUnit_Run_CuratedFixtureGeneratesTheCompleteTree(t *testing.T) {
	for _, dialect := range curatedDialects {
		t.Run(dialect, func(t *testing.T) {
			root, opts := curatedRepo(t, dialect)

			res, err := Run(context.Background(), opts)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			// the list-only datasource is enumerable and not addressable, so it is a
			// datasource; the three resources are all enumerable, so each
			// carries a list resource of its own terraform type.
			if res.Resources != 3 || res.Datasources != 5 || res.ListResources != 3 || res.Actions != 1 {
				t.Errorf("entity counts = %d resources, %d datasources, %d list resources, %d actions; the fixture declares 3, 5, 3, 1",
					res.Resources, res.Datasources, res.ListResources, res.Actions)
			}
			// A refusal with no cause cannot be grouped with the ones that
			// share its fact, so it reads as its own finding. Asserted on a
			// whole run rather than per stage, because a new refusal path
			// that forgets one is exactly what this has to catch.
			for _, u := range res.Unsupported {
				if u.Cause == nil || u.Cause.Code == "" {
					t.Errorf("refusal carries no cause: %s %q attribute %q (%s): %s",
						u.Kind, u.Entity, u.Attribute, u.Stage, u.Reason)
				}
			}
			for _, r := range res.Removals {
				t.Errorf("pruned unexpectedly: %s", r)
			}
			if res.Files == 0 {
				t.Fatal("Run reported no files")
			}
			if res.TreeVerification.Ran || res.TreeVerification.SkippedReason != "tree verification disabled" {
				t.Errorf("tree verification = %+v; the fixture run disables it", res.TreeVerification)
			}

			for _, path := range []string{
				"go.mod",
				"main.go",
				"manifest.json",
				"internal/provider/provider.go",
				"internal/services/resources/patch_updated_resources/v1/patch_updated_resource/resource.go",
				"internal/services/resources/replace_only_resources/v1/replace_only_resource/resource.go",
				"internal/services/resources/put_updated_resources/v1/put_updated_resource/resource.go",
				"internal/services/datasources/key_addressed_datasources/v1/key_addressed_datasource/datasource.go",
				"internal/services/datasources/list_only_datasources/v1/list_only_datasource/datasource.go",
				"internal/services/list-resources/patch_updated_resources/v1/patch_updated_resource/list_resource.go",
				"internal/services/actions/patch_updated_resources/v1/patch_updated_resources_custom_action/action.go",
			} {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
					t.Errorf("the generated tree lacks %s: %v", path, err)
				}
			}

			registry, err := os.ReadFile(filepath.Join(root, "internal", "provider", "resources.go"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"NewPatchUpdatedResourceResource,", "NewReplaceOnlyResourceResource,", "NewPutUpdatedResourceResource,"} {
				if !strings.Contains(string(registry), want) {
					t.Errorf("resources.go carries no registered %s", want)
				}
			}

			assertDeclaredListWrapper(t, root)

			rep, err := Verify(context.Background(), opts)
			if err != nil {
				t.Fatalf("Verify after Run: %v", err)
			}
			if !rep.Clean() {
				t.Errorf("Verify after Run found drift: %v", rep.Drifts)
			}
		})
	}
}

// assertDeclaredListWrapper holds the generated datasource to the
// wrapper the fixture's x-tfpfgen-list-wrapper declares. The document's own
// list response is a bare array, so a schema-derived wrapper would be empty:
// every wrapper below exists only because the extension carried the audit's
// finding all the way through derivation into the emitted list code.
func assertDeclaredListWrapper(t *testing.T, root string) {
	t.Helper()
	read := func(parts ...string) string {
		raw, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
		if err != nil {
			t.Fatalf("reading the generated list code: %v", err)
		}
		return string(raw)
	}

	ds := filepath.Join("internal", "services", "datasources", "replace_only_resources", "v1", "replace_only_resource")
	if got := read(ds, "mocks", "responders.go"); !strings.Contains(got,
		`map[string]any{"replaceOnlyResources": []map[string]any{object()}}`) {
		t.Errorf("the datasource list mock ignores the declared list wrapper:\n%s", got)
	}
	if got := read(ds, "tests", "responses", "datasource.json"); !strings.Contains(got, `"replaceOnlyResources": [`) {
		t.Errorf("the datasource list fixture ignores the declared list wrapper:\n%s", got)
	}
	res := filepath.Join("internal", "services", "resources", "replace_only_resources", "v1", "replace_only_resource")
	if got := read(res, "mocks", "responders.go"); !strings.Contains(got,
		`map[string]any{"replaceOnlyResources": items}`) {
		t.Errorf("the resource list mock ignores the declared list wrapper:\n%s", got)
	}
}

// TestUnit_Run_CuratedFixtureIsDeterministic generates the same fixture
// twice into independent repos and requires the two trees byte-identical,
// manifest included.
func TestUnit_Run_CuratedFixtureIsDeterministic(t *testing.T) {
	for _, dialect := range curatedDialects {
		t.Run(dialect, func(t *testing.T) {
			rootA, optsA := curatedRepo(t, dialect)
			rootB, optsB := curatedRepo(t, dialect)

			if _, err := Run(context.Background(), optsA); err != nil {
				t.Fatalf("first Run: %v", err)
			}
			if _, err := Run(context.Background(), optsB); err != nil {
				t.Fatalf("second Run: %v", err)
			}

			a, err := digestTree(rootA)
			if err != nil {
				t.Fatal(err)
			}
			b, err := digestTree(rootB)
			if err != nil {
				t.Fatal(err)
			}
			for path, summary := range a {
				switch other, there := b[path]; {
				case !there:
					t.Errorf("only the first run produced %s", path)
				case other != summary:
					t.Errorf("%s differs between runs", path)
				}
			}
			for path := range b {
				if _, there := a[path]; !there {
					t.Errorf("only the second run produced %s", path)
				}
			}
		})
	}
}

// TestUnit_Run_CuratedTreeCompiles is the full-strength gate: the generated
// tree for each dialect — stub SDK in place — is held to `go mod tidy`,
// `go build` and `go vet` against the real dependency pins. Resolving the
// pins needs the module proxy, so an offline failure skips rather than
// fails, the same bargain emit's compile tests strike.
func TestUnit_Run_CuratedTreeCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the toolchain compile in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}

	for _, dialect := range curatedDialects {
		t.Run(dialect, func(t *testing.T) {
			root, opts := curatedRepo(t, dialect)
			if _, err := Run(context.Background(), opts); err != nil {
				t.Fatalf("Run: %v", err)
			}

			runGo(t, root, "mod", "tidy")
			runGo(t, root, "build", "./...")
			runGo(t, root, "vet", "./...")
			// The provider core ships its own tests, and the conversion
			// bridges are the part of it no repo test can reach: they are a
			// template here and only Go once emitted.
			runGo(t, root, "test", "./internal/services/common/...")
		})
	}
}

// digestTree hashes every file under root, keyed by slash-relative path.
// The spec directory is skipped: the import timestamp in the lock is the
// run's input, not its output.
func digestTree(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			if rel == "spec" {
				return filepath.SkipDir
			}
			return nil
		}
		data, rerr := os.ReadFile(p) //nolint:gosec // walking the tree the test generated
		if rerr != nil {
			return rerr
		}
		summary := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(summary[:])
		return nil
	})
	return out, err
}

// runGo runs one toolchain command in dir, failing with the toolchain's own
// words — or skipping when those words say offline.
func runGo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	for _, signature := range offlineToolchainMessages {
		if strings.Contains(string(out), signature) {
			t.Skipf("go %s needs the network and cannot reach it:\n%s", strings.Join(args, " "), out)
		}
	}
	t.Fatalf("go %s:\n%s", strings.Join(args, " "), out)
}
