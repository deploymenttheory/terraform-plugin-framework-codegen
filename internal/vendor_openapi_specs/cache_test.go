package vendor_openapi_specs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var anInstant = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// TestUnit_Cache_StoreThenFindRoundTrips: what store writes, find resolves,
// and every accessor agrees about.
func TestUnit_Cache_StoreThenFindRoundTrips(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	meta := Metadata{
		Version:        "1.2.3",
		SourceURL:      "https://example.invalid/api.yaml",
		FetchedAt:      anInstant,
		PathCount:      1,
		OperationCount: 1,
	}

	stored, err := store(root, []byte(aDocument), meta)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	found, err := find(root, stored.Name)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Dir != stored.Dir || found.Version != "1.2.3" || !found.Timestamp.Equal(anInstant) {
		t.Errorf("find returned %+v, stored %+v", found, stored)
	}

	// store was given no SHA256, so it must have computed one.
	m, err := found.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if m.SHA256 != digestOf(aDocument) {
		t.Errorf("metadata sha256 = %s, want the document's digest", shortSHA(m.SHA256))
	}
	if m.SourceURL != meta.SourceURL || m.PathCount != 1 || m.OperationCount != 1 {
		t.Errorf("metadata did not round-trip: %+v", m)
	}

	if err := found.Verify(); err != nil {
		t.Errorf("a copy just stored does not verify: %v", err)
	}
	if sum, err := found.Checksum(); err != nil || sum != digestOf(aDocument) {
		t.Errorf("Checksum = %s, %v", shortSHA(sum), err)
	}
}

// TestUnit_Cache_StoreRefusesToOverwrite: immutability is the property the
// whole cache design rests on.
func TestUnit_Cache_StoreRefusesToOverwrite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	meta := Metadata{Version: "1.2.3", FetchedAt: anInstant}

	if _, err := store(root, []byte(aDocument), meta); err != nil {
		t.Fatalf("first store: %v", err)
	}
	if _, err := store(root, []byte("different bytes"), meta); err == nil {
		t.Fatal("a second store into the same directory was accepted")
	}
}

// TestUnit_Cache_AnEmptyVersionStillNamesADirectory: a document that declares
// no version must not produce the unfindable directory name "-t123".
func TestUnit_Cache_AnEmptyVersionStillNamesADirectory(t *testing.T) {
	t.Parallel()

	if got, want := dirName("", anInstant), "unknown-t"+"1767323045000"; got != want {
		t.Errorf("dirName(\"\") = %q, want %q", got, want)
	}
}

// TestUnit_Cache_FindReportsAMissingCopyAsErrNoDocument keeps "not cached yet"
// distinguishable from every other failure, because it is the one callers may
// treat as normal.
func TestUnit_Cache_FindReportsAMissingCopyAsErrNoDocument(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if _, err := find(root, "1.2.3-t123"); !errors.Is(err, ErrNoDocument) {
		t.Errorf("a missing directory: %v", err)
	}

	// A file where the directory should be is equally not a cached copy.
	if err := os.WriteFile(filepath.Join(root, "a-file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := find(root, "a-file"); !errors.Is(err, ErrNoDocument) {
		t.Errorf("a plain file: %v", err)
	}
}

// TestUnit_Cache_FindToleratesAForeignDirectoryName: a hand-made directory
// without the version-timestamp shape still resolves, with what can be known
// about it.
func TestUnit_Cache_FindToleratesAForeignDirectoryName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "plain"), 0o750); err != nil {
		t.Fatal(err)
	}

	found, err := find(root, "plain")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Version != "plain" || !found.Timestamp.IsZero() {
		t.Errorf("a foreign name resolved to %+v", found)
	}
}

// TestUnit_Cache_VerifyCatchesAnEditedDocument is the immutability gate
// itself.
func TestUnit_Cache_VerifyCatchesAnEditedDocument(t *testing.T) {
	t.Parallel()

	stored, err := store(t.TempDir(), []byte(aDocument), Metadata{Version: "1.2.3", FetchedAt: anInstant})
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	if err := os.WriteFile(stored.SpecPath(), []byte("edited in place"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := stored.Verify(); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("an edited document verified: %v", err)
	}
}

// TestUnit_Cache_ACopyWithBrokenMetadataDoesNotVerify: metadata is the
// checksum's home, so losing it must fail closed.
func TestUnit_Cache_ACopyWithBrokenMetadataDoesNotVerify(t *testing.T) {
	t.Parallel()

	stored, err := store(t.TempDir(), []byte(aDocument), Metadata{Version: "1.2.3", FetchedAt: anInstant})
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	if err := os.WriteFile(stored.MetadataPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stored.Verify(); err == nil || !strings.Contains(err.Error(), "parsing") {
		t.Errorf("broken metadata verified: %v", err)
	}

	if err := os.Remove(stored.MetadataPath()); err != nil {
		t.Fatal(err)
	}
	if err := stored.Verify(); err == nil || !strings.Contains(err.Error(), "reading") {
		t.Errorf("missing metadata verified: %v", err)
	}
}

// TestUnit_Cache_AMissingDocumentFailsChecksum: half a cached copy is no
// cached copy.
func TestUnit_Cache_AMissingDocumentFailsChecksum(t *testing.T) {
	t.Parallel()

	stored, err := store(t.TempDir(), []byte(aDocument), Metadata{Version: "1.2.3", FetchedAt: anInstant})
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	if err := os.Remove(stored.SpecPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := stored.Checksum(); err == nil {
		t.Error("a missing document produced a checksum")
	}
	if err := stored.Verify(); err == nil {
		t.Error("a missing document verified")
	}
}
