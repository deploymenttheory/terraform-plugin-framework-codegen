package emit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnit_Emit_FileSHA256(t *testing.T) {
	t.Parallel()

	a := File{Path: "a.go", Content: []byte("package a\n")}
	b := File{Path: "b.go", Content: []byte("package a\n")}
	c := File{Path: "c.go", Content: []byte("package c\n")}

	// The digest is of content, not of the path: the manifest uses it to detect a
	// changed file, not a moved one.
	if a.SHA256() != b.SHA256() {
		t.Error("identical content should digest identically")
	}
	if a.SHA256() == c.SHA256() {
		t.Error("different content must digest differently")
	}
	if len(a.SHA256()) != 64 {
		t.Errorf("digest should be hex-encoded SHA-256, got %q", a.SHA256())
	}
}

// TestUnit_Emit_WriteReportsUnreadableTargets covers the path where the target
// exists but cannot be inspected, which must fail rather than overwrite blindly.
func TestUnit_Emit_WriteReportsUnreadableTargets(t *testing.T) {
	t.Parallel()

	bp := pilotBlueprint(t)

	gen, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plan, err := gen.Build(bp, Options{BlueprintPath: "b"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	root := t.TempDir()

	// A directory where a file should be: reading it fails with something other
	// than "not found". The first *generated* file -- a scaffold's existence check
	// takes a different path.
	target := filepath.Join(root, firstGenerated(t, plan).Path)
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if _, err := Write(plan, WriteOptions{Root: root}); err == nil {
		t.Error("writing over a directory should fail")
	}
}

func TestUnit_Emit_RenderFileRejectsAnUnknownTemplate(t *testing.T) {
	t.Parallel()

	gen, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := gen.renderFile("nosuch.tmpl", nil); err == nil {
		t.Error("an unknown template should fail")
	}
}

func TestUnit_Emit_NumberLines(t *testing.T) {
	t.Parallel()

	got := numberLines([]byte("one\ntwo"))
	for _, want := range []string{"   1 | one", "   2 | two"} {
		if !strings.Contains(got, want) {
			t.Errorf("numbered output omits %q:\n%s", want, got)
		}
	}
}
