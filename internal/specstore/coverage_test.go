package specstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnit_SpecStore_LoadMetadataFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dir := filepath.Join(root, "1.0.0-t1")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	s := Snapshot{Dir: dir, Name: "1.0.0-t1"}

	// No metadata at all.
	if _, err := s.LoadMetadata(); err == nil {
		t.Error("missing metadata should fail")
	}

	// Malformed metadata must not read as an empty record, which would make
	// Verify silently pass.
	if err := os.WriteFile(s.MetadataPath(), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := s.LoadMetadata(); err == nil {
		t.Error("malformed metadata should fail")
	}
	if err := s.Verify(); err == nil {
		t.Error("Verify should fail when the metadata cannot be read")
	}
}

func TestUnit_SpecStore_ChecksumOfAMissingDocument(t *testing.T) {
	t.Parallel()

	s := Snapshot{Dir: t.TempDir()}
	if _, err := s.Checksum(); err == nil {
		t.Error("checksumming a missing document should fail")
	}
}
