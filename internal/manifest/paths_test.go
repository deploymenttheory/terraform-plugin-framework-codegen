package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnit_Manifest_PathsIsTheFullSet(t *testing.T) {
	m := New("v0.1.0", entries())
	paths := m.Paths()
	if len(paths) != len(entries()) {
		t.Fatalf("Paths() has %d entries, want %d", len(paths), len(entries()))
	}
	if !paths["audit/inputs.json"] || !paths["internal/sdk/client.go"] {
		t.Fatalf("Paths() = %v", paths)
	}
}

func TestUnit_Manifest_SaveRefusesAnUnwritableRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent", "deeper")
	if err := Save(root, New("v0.1.0", nil)); err == nil {
		t.Fatal("Save into a nonexistent root succeeded")
	}
}

func TestUnit_Manifest_LoadSurfacesAReadFailure(t *testing.T) {
	root := t.TempDir()
	// The manifest path exists but is a directory: ReadFile fails with
	// something other than not-exist, which must surface rather than read as
	// "never generated".
	if err := os.Mkdir(filepath.Join(root, Name), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := Load(root); err == nil || ok {
		t.Fatalf("Load = %v, %v; want a surfaced read failure", ok, err)
	}
}

func TestUnit_Manifest_UnproducedFilesOfSurfacesAStatFailure(t *testing.T) {
	root := t.TempDir()
	// blocker is a file; blocker/child then fails Stat with ENOTDIR, which
	// is not not-exist and must surface.
	if err := os.WriteFile(filepath.Join(root, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New("v0.1.0", []Entry{{Path: "blocker/child.go", SHA256: "c"}})
	if _, err := m.UnproducedFilesOf(root, "", nil); err == nil {
		t.Fatal("a stat failure was swallowed")
	}
}
