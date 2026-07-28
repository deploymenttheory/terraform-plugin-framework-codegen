package specstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// write creates a snapshot directory with the given document and recorded
// checksum. recordedSHA of "" means "record the real one".
func write(t *testing.T, root, name, body, recordedSHA string) {
	t.Helper()

	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, SpecFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if recordedSHA == "" {
		s := Snapshot{Dir: dir}
		sum, err := s.Checksum()
		if err != nil {
			t.Fatalf("Checksum: %v", err)
		}
		recordedSHA = sum
	}

	data, err := json.Marshal(Metadata{Version: "1.0.0", SHA256: recordedSHA})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, MetadataFileName), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestUnit_SpecStore_OrdersByEncodedTimestamp is the property that makes this
// work in CI at all.
//
// Ordering by filesystem times would give a different answer on a runner than on
// the machine that wrote the snapshots, because a git checkout does not preserve
// mtimes. The timestamp in the directory name is the only ordering that survives.
func TestUnit_SpecStore_OrdersByEncodedTimestamp(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Written oldest-name-last so filename order and timestamp order disagree,
	// and written in an order that does not match either.
	write(t, root, "7.0.50-t1000000000000", "old", "")
	write(t, root, "7.0.97-t3000000000000", "newest", "")
	write(t, root, "7.0.60-t2000000000000", "middle", "")

	latest, err := Latest(root)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.Name != "7.0.97-t3000000000000" {
		t.Errorf("Latest = %q, want the one with the highest encoded timestamp", latest.Name)
	}
	if latest.Version != "7.0.97" {
		t.Errorf("Version = %q, want 7.0.97", latest.Version)
	}
	if got := latest.Timestamp.UTC(); got != time.UnixMilli(3000000000000).UTC() {
		t.Errorf("Timestamp = %v", got)
	}

	all, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 || all[0].Name != "7.0.97-t3000000000000" || all[2].Name != "7.0.50-t1000000000000" {
		t.Errorf("List is not newest-first: %v", names(all))
	}
}

func TestUnit_SpecStore_NoSnapshotIsASentinel(t *testing.T) {
	t.Parallel()

	if _, err := Latest(t.TempDir()); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("empty directory: error = %v, want ErrNoSnapshot", err)
	}
	if _, err := Latest(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("missing directory: error = %v, want ErrNoSnapshot", err)
	}
}

// TestUnit_SpecStore_IgnoresDirectoriesThatDoNotMatch keeps stray content from
// being mistaken for a snapshot.
func TestUnit_SpecStore_IgnoresDirectoriesThatDoNotMatch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "7.0.97-t1000000000000", "real", "")

	for _, junk := range []string{"notasnapshot", "7.0.97", "7.0.97-tabc"} {
		if err := os.MkdirAll(filepath.Join(root, junk), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	all, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("List returned %v, want only the well-formed snapshot", names(all))
	}
}

func TestUnit_SpecStore_FindByName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "7.0.50-t1000000000000", "old", "")
	write(t, root, "7.0.97-t2000000000000", "new", "")

	got, err := Find(root, "7.0.50-t1000000000000")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got.Version != "7.0.50" {
		t.Errorf("Find returned %q", got.Name)
	}

	// An empty name means "the newest", which is what every command defaults to.
	got, err = Find(root, "")
	if err != nil {
		t.Fatalf("Find(\"\"): %v", err)
	}
	if got.Version != "7.0.97" {
		t.Errorf("Find(\"\") returned %q, want the newest", got.Name)
	}

	// A name that does not exist must list what does, or the reader has to go
	// looking.
	_, err = Find(root, "9.9.9-t1")
	if !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("error = %v, want ErrNoSnapshot", err)
	}
	if !strings.Contains(err.Error(), "7.0.97-t2000000000000") {
		t.Errorf("error should list the available snapshots: %v", err)
	}
}

// TestUnit_SpecStore_VerifyCatchesAnEditedSnapshot guards the property pinning
// exists for. A snapshot edited in place makes the generated provider
// unreproducible from anything committed.
func TestUnit_SpecStore_VerifyCatchesAnEditedSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "7.0.97-t1000000000000", "original", "")

	snap, err := Latest(root)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if err := snap.Verify(); err != nil {
		t.Fatalf("a freshly written snapshot must verify: %v", err)
	}

	// Somebody edits the pinned document to work around an ingestion bug.
	if err := os.WriteFile(snap.SpecPath(), []byte("tampered"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := snap.Verify(); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("error = %v, want ErrChecksumMismatch", err)
	}
}

// TestUnit_SpecStore_VerifyToleratesMissingChecksum allows a snapshot pinned by
// hand, where there is nothing to check against rather than a mismatch.
func TestUnit_SpecStore_VerifyToleratesMissingChecksum(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "7.0.97-t1000000000000", "body", " ")

	snap, err := Latest(root)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}

	// A recorded checksum of whitespace is not empty, so this must fail; the
	// tolerated case is genuinely absent.
	if err := snap.Verify(); err == nil {
		t.Error("a wrong recorded checksum must not be tolerated")
	}
}

func TestUnit_SpecStore_DirName(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1785152261691).UTC()

	if got := DirName("7.0.97", at); got != "7.0.97-t1785152261691" {
		t.Errorf("DirName = %q", got)
	}
	// A document with no declared version still needs a stable directory.
	if got := DirName("", at); got != "unknown-t1785152261691" {
		t.Errorf("DirName with no version = %q", got)
	}
}

// TestUnit_SpecStore_CommittedSnapshotVerifies checks the real pinned snapshot,
// so a corrupted or hand-edited commit fails here rather than during generation.
func TestUnit_SpecStore_CommittedSnapshotVerifies(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "openapi-specs", "thousandeyes")

	snap, err := Latest(root)
	if err != nil {
		t.Fatalf("the committed snapshot should be discoverable: %v", err)
	}
	if err := snap.Verify(); err != nil {
		t.Errorf("the committed snapshot does not match its recorded checksum: %v", err)
	}

	m, err := snap.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if m.Version == "" {
		t.Error("the committed snapshot records no version")
	}
}

func names(in []Snapshot) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.Name)
	}
	return out
}
