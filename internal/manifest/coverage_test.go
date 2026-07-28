package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnit_Manifest_SaveReportsWriteErrors(t *testing.T) {
	t.Parallel()

	// A root that is a file, not a directory.
	root := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(root, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := Save(root, New("dev", nil)); err == nil {
		t.Error("saving under a file should fail")
	}
}

func TestUnit_Manifest_LoadReportsMalformedContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, Name)

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, _, err := Load(root); err == nil {
		t.Error("malformed JSON should fail rather than read as an empty manifest")
	}
}

// TestUnit_Manifest_OrphansReportsStatErrors: a path that cannot be inspected is
// not silently treated as absent, because that would hide an orphan.
func TestUnit_Manifest_OrphansReportsStatErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// A path whose parent is a file makes Stat fail with something other than
	// "not found".
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m := New("dev", []Entry{{Path: "blocker/under.go"}})

	if _, err := m.Orphans(root, map[string]bool{}); err == nil {
		t.Error("an unreadable path should be reported, not treated as absent")
	}
}
