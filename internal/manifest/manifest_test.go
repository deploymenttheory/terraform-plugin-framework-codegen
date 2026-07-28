package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func entries() []Entry {
	return []Entry{
		{Path: "b/second.go", SHA256: "bbb", Blueprint: "blueprints/x"},
		{Path: "a/first.go", SHA256: "aaa", Blueprint: "blueprints/x"},
	}
}

// TestUnit_Manifest_IsSortedAndDeterministic guards the property that makes a
// committed manifest reviewable. Ordering that depended on map iteration or on
// blueprint order would produce a diff on every run.
func TestUnit_Manifest_IsSortedAndDeterministic(t *testing.T) {
	t.Parallel()

	m := New("dev", entries())

	if m.Files[0].Path != "a/first.go" || m.Files[1].Path != "b/second.go" {
		t.Errorf("entries are not sorted by path: %v", m.Files)
	}

	first, err := Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for i := range 20 {
		again, err := Marshal(New("dev", entries()))
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("Marshal is not deterministic; run %d differed", i)
		}
	}
}

// TestUnit_Manifest_NewDoesNotMutateItsInput matters because the caller keeps
// using the plan it passed in; sorting in place would reorder emitted output.
func TestUnit_Manifest_NewDoesNotMutateItsInput(t *testing.T) {
	t.Parallel()

	in := entries()
	New("dev", in)

	if in[0].Path != "b/second.go" {
		t.Errorf("New reordered its input: %v", in)
	}
}

func TestUnit_Manifest_SaveAndLoad(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	want := New("v1.2.3", entries())
	if err := Save(root, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok {
		t.Fatal("Load reported no manifest after saving one")
	}
	if got.ToolVersion != "v1.2.3" || len(got.Files) != 2 {
		t.Errorf("loaded manifest differs: %+v", got)
	}

	// The tool version lives here and nowhere else, so that a release does not
	// rewrite every generated file.
	data, err := os.ReadFile(filepath.Join(root, Name))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"toolVersion": "v1.2.3"`) {
		t.Errorf("manifest should record the tool version:\n%s", data)
	}
}

// TestUnit_Manifest_MissingIsNotAnError distinguishes "no orphans" from "cannot
// know". A tree generated before manifests existed must not be reported as clean
// when nothing actually checked it.
func TestUnit_Manifest_MissingIsNotAnError(t *testing.T) {
	t.Parallel()

	m, ok, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("a missing manifest must not be an error: %v", err)
	}
	if ok {
		t.Error("Load reported a manifest that does not exist")
	}
	if len(m.Files) != 0 {
		t.Errorf("expected an empty manifest, got %+v", m)
	}
}

func TestUnit_Manifest_RejectsAnUnsupportedFormat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, Name)

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"formatVersion":"999","files":[]}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, _, err := Load(root); !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("error = %v, want it to wrap ErrUnsupportedFormat", err)
	}
}

// TestUnit_Manifest_OrphansAreFilesNoLongerProduced covers the case the manifest
// exists for: renaming a resource leaves the old package on disk, still
// compiling, registered nowhere.
func TestUnit_Manifest_OrphansAreFilesNoLongerProduced(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Two files were generated last time; both still exist.
	for _, p := range []string{"old/gone.go", "kept/still.go"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte("package p\n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	m := New("dev", []Entry{{Path: "old/gone.go"}, {Path: "kept/still.go"}})

	orphans, err := m.Orphans(root, map[string]bool{"kept/still.go": true})
	if err != nil {
		t.Fatalf("Orphans: %v", err)
	}

	if len(orphans) != 1 || orphans[0] != "old/gone.go" {
		t.Errorf("orphans = %v, want [old/gone.go]", orphans)
	}
}

// TestUnit_Manifest_AlreadyDeletedIsNotAnOrphan: a recorded path that somebody has
// already removed needs no report. The blueprints agree it should not exist and
// it does not.
func TestUnit_Manifest_AlreadyDeletedIsNotAnOrphan(t *testing.T) {
	t.Parallel()

	m := New("dev", []Entry{{Path: "never/written.go"}})

	orphans, err := m.Orphans(t.TempDir(), map[string]bool{})
	if err != nil {
		t.Fatalf("Orphans: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("orphans = %v, want none", orphans)
	}
}

func TestUnit_Manifest_Paths(t *testing.T) {
	t.Parallel()

	got := New("dev", entries()).Paths()
	if !got["a/first.go"] || !got["b/second.go"] || len(got) != 2 {
		t.Errorf("Paths = %v", got)
	}
}
