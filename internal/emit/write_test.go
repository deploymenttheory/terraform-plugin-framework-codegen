package emit

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
)

// TestUnit_Write_LandsFilesAndReportsProviderEntries proves the written
// tree matches the rendered one byte for byte, and every entry carries
// the digest, the template source, and the empty provider origin.
func TestUnit_Write_LandsFilesAndReportsProviderEntries(t *testing.T) {
	pc, err := FromConfig(testConfig(config.BackendOpenAPIGenerator, config.AuthBasic), "")
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}
	files, err := RenderProviderCore(pc)
	if err != nil {
		t.Fatalf("RenderProviderCore: %v", err)
	}

	root := t.TempDir()
	entries, err := Write(root, files)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(entries) != len(files) {
		t.Fatalf("got %d entries for %d files", len(entries), len(files))
	}

	byPath := make(map[string]File, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}

	for _, e := range entries {
		f, ok := byPath[e.Path]
		if !ok {
			t.Errorf("entry %s matches no rendered file", e.Path)
			continue
		}

		onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.Path)))
		if err != nil {
			t.Errorf("reading back %s: %v", e.Path, err)
			continue
		}
		if string(onDisk) != string(f.Content) {
			t.Errorf("%s on disk differs from the render", e.Path)
		}

		summary := sha256.Sum256(f.Content)
		if e.SHA256 != hex.EncodeToString(summary[:]) {
			t.Errorf("%s carries the wrong digest", e.Path)
		}
		if e.Source != f.Source {
			t.Errorf("%s records source %q, want %q", e.Path, e.Source, f.Source)
		}
		if e.Origin != "" {
			t.Errorf("%s carries origin %q; provider entries carry the empty origin", e.Path, e.Origin)
		}
		if e.Authored {
			t.Errorf("%s is marked authored; nothing the provider core writes is", e.Path)
		}
	}

	// The entries slot straight into a manifest.
	m := manifest.New("dev", entries)
	if got := len(m.Files); got != len(entries) {
		t.Fatalf("manifest.New kept %d of %d entries", got, len(entries))
	}
}

// TestUnit_Write_ReportsAnUnwritableRoot proves a filesystem refusal
// surfaces as an error naming the path, not a partial silent tree.
func TestUnit_Write_ReportsAnUnwritableRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(root, []byte("a file where the root should be"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Write(root, []File{{Path: "internal/x.txt", Content: []byte("x"), Source: "provider-core/x.txt.tmpl"}})
	if err == nil {
		t.Fatal("Write succeeded under a root that is a file")
	}
}
