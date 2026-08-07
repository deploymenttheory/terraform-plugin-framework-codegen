// Package curated holds a committed, vendor-neutral blueprint set carrying the
// curated layer, for tests that need a fixture rather than a product.
//
// It exists because the layer cannot be derived. A blueprint the toolkit drafts
// from an OpenAPI document is only half a blueprint: drafting reads what a
// specification declares, and everything a specification cannot say -- what the
// API actually enforces, what it returns, what it substitutes, what it does
// differently on each side of its own dispatch -- arrives later from probing and
// from human judgement. That later half is what the state mapper, the expand and
// flatten helpers and the conditional-behaviour paths are generated from, which
// makes it exactly the part a test must not do without. So the fixture is
// authored and committed rather than produced at test time, and the tests in
// this package assert the curated features are still in it, because a fixture
// that quietly loses them keeps passing while proving nothing.
//
// It is deliberately fictional. "Nimbus" is not an API anybody ships, and the
// point is that it need not be: the blueprint format, the emitter and the
// generated tree are this repository's, so a fixture exercising them is our own
// artefact rather than a vendored third-party document. The structural shapes --
// which chains a kiota SDK is called through, how a collection response is
// reached, what a nested element's accessors look like -- were taken from
// blueprints drafted against a pinned GitHub document, so the wire shapes are
// realistic rather than invented, and then renamed and trimmed.
//
// The name says what distinguishes it. internal/corpus holds pinned third-party
// specifications; internal/fixturespec holds acceptance fixture *values*. This
// holds the curated layer, and nothing else in the tree does.
package curated

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// files is the fixture, embedded so that both cmd/tfpfgen and internal/generate
// can reach it. A testdata directory could not be shared: the go tool scopes one
// to the package that contains it, and this fixture has to serve two.
//
//go:embed blueprints
var files embed.FS

// root is the embedded prefix the set lives under.
const root = "blueprints"

// TB is the part of *testing.T these helpers need.
//
// Declared rather than importing testing, for the reason internal/corpus gives:
// the package stays usable from non-test code without dragging testing's flag
// registration into a binary. TempDir is in the set because materialising the
// fixture somewhere the test framework will clean up is the whole of what Dir
// does, and an os.MkdirTemp nobody removes is a leak per test run.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	TempDir() string
}

// Dir materialises the fixture into a temporary directory and returns its path,
// for a test that needs a directory to point -blueprint at.
//
// Every call gets its own copy. A test that writes into the set -- adding an
// override file, deleting a resource to see what generation does without it --
// must not be able to affect the next one, and a shared directory is how that
// becomes a test-ordering bug.
func Dir(t TB) string {
	t.Helper()

	dir := t.TempDir()
	if err := materialise(dir); err != nil {
		t.Fatalf("materialising the curated fixture: %v", err)
	}

	return dir
}

// Blueprint is the fixture loaded and validated, for a test that works against
// the model rather than against files.
//
// It goes through Dir and blueprint.LoadDir rather than decoding the embedded
// bytes directly, so that what this returns is by construction the same document
// a CLI test gets from the same fixture -- including the directory merge, whose
// ordering and provider-block rules are part of what the fixture exercises.
func Blueprint(t TB) blueprint.Blueprint {
	t.Helper()

	bp, err := blueprint.LoadDir(Dir(t))
	if err != nil {
		t.Fatalf("loading the curated fixture: %v", err)
	}

	return bp
}

// materialise copies the embedded set out to dst.
func materialise(dst string) error {
	err := fs.WalkDir(files, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relating %s to %s: %w", path, root, err)
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))

		if d.IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return fmt.Errorf("creating %s: %w", target, err)
			}

			return nil
		}

		data, err := files.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading the embedded %s: %w", path, err)
		}

		if err := os.WriteFile(target, data, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", target, err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("copying the fixture into %s: %w", dst, err)
	}

	return nil
}
