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

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/revise"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/store"
)

// The curated fixture is Phase 1's exit gate: a committed, fictional
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

	doc, err := os.ReadFile(filepath.Join(fixture, "openapi.yaml"))
	if err != nil {
		t.Fatalf("reading the curated document: %v", err)
	}
	specDir := filepath.Join(root, "spec")
	if _, err := store.Import(specDir, doc, "testdata/curated/openapi.yaml"); err != nil {
		t.Fatalf("spec import: %v", err)
	}
	if _, err := revise.Materialize(specDir); err != nil {
		t.Fatalf("spec revise: %v", err)
	}

	if err := copyTree(filepath.Join(fixture, dialect, "sdk"), filepath.Join(root, "internal", "sdk")); err != nil {
		t.Fatalf("standing the stub SDK: %v", err)
	}

	cfg, err := config.Load(filepath.Join(fixture, dialect, "tfpfgen.yaml"))
	if err != nil {
		t.Fatalf("loading the fixture config: %v", err)
	}

	return root, Options{
		Config:  cfg,
		SpecDir: specDir,
		SDKDir:  "internal/sdk",
		Root:    root,
	}
}

// TestUnit_Run_CuratedFixtureGeneratesTheCompleteTree drives the whole
// offline chain for both dialects and holds the result to the fixture's
// known shape: three resources, a companion datasource for each plus the
// lookup-by-key permit, the list-only transit, the reboot action — and
// nothing pruned, because the stub SDKs carry everything the document
// promises.
func TestUnit_Run_CuratedFixtureGeneratesTheCompleteTree(t *testing.T) {
	for _, dialect := range curatedDialects {
		t.Run(dialect, func(t *testing.T) {
			root, opts := curatedRepo(t, dialect)

			res, err := Run(context.Background(), opts)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if res.Resources != 3 || res.Datasources != 4 || res.ListResources != 1 || res.Actions != 1 {
				t.Errorf("entity counts = %d resources, %d datasources, %d list resources, %d actions; the fixture declares 3, 4, 1, 1",
					res.Resources, res.Datasources, res.ListResources, res.Actions)
			}
			for _, r := range res.Removals {
				t.Errorf("pruned unexpectedly: %s", r)
			}
			if res.Files == 0 {
				t.Fatal("Run reported no files")
			}
			if res.Postcheck.Ran || res.Postcheck.SkippedReason != "postcheck disabled" {
				t.Errorf("postcheck = %+v; the fixture run disables it", res.Postcheck)
			}

			for _, path := range []string{
				"go.mod",
				"main.go",
				"manifest.json",
				"internal/provider/provider.go",
				"internal/services/resources/modules/v1/module/resource.go",
				"internal/services/resources/beacons/v1/beacon/resource.go",
				"internal/services/resources/docks/v1/dock/resource.go",
				"internal/services/datasources/permits/v1/permit/datasource.go",
				"internal/services/list-resources/transits/v1/transit/list_resource.go",
				"internal/services/actions/modules/v1/modules_reboot/action.go",
			} {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
					t.Errorf("the generated tree lacks %s: %v", path, err)
				}
			}

			registry, err := os.ReadFile(filepath.Join(root, "internal", "provider", "resources.go"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"NewModuleResource,", "NewBeaconResource,", "NewDockResource,"} {
				if !strings.Contains(string(registry), want) {
					t.Errorf("resources.go carries no spliced %s", want)
				}
			}

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
			for path, sum := range a {
				switch other, there := b[path]; {
				case !there:
					t.Errorf("only the first run produced %s", path)
				case other != sum:
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
		sum := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
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
	for _, signature := range offlineSignatures {
		if strings.Contains(string(out), signature) {
			t.Skipf("go %s needs the network and cannot reach it:\n%s", strings.Join(args, " "), out)
		}
	}
	t.Fatalf("go %s:\n%s", strings.Join(args, " "), out)
}
