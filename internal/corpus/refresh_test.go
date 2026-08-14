package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUnit_Refresh_LockPathHonoursTheEnvironment: a rewrite invoked from
// outside the repository root needs to be pointed at the lock, and defaults to
// where the lock lives in a checkout.
func TestUnit_Refresh_LockPathHonoursTheEnvironment(t *testing.T) {
	t.Setenv(EnvLockPath, filepath.Join("somewhere", LockFile))
	if got, want := LockPath(), filepath.Join("somewhere", LockFile); got != want {
		t.Errorf("LockPath() = %q, want %q", got, want)
	}

	t.Setenv(EnvLockPath, "")
	if got, want := LockPath(), filepath.Join("internal", "corpus", "testdata", LockFile); got != want {
		t.Errorf("LockPath() = %q, want %q", got, want)
	}
}

// TestUnit_Refresh_CheckUpstreamMeasuresWithoutJudging: the check reports what
// a source serves against the pin, for a match and a mismatch alike.
func TestUnit_Refresh_CheckUpstreamMeasuresWithoutJudging(t *testing.T) {
	t.Parallel()

	url, _ := serve(t, aDocument)

	matching, err := checkPin(pinFor(t, url, aDocument), unparsed)
	if err != nil {
		t.Fatalf("checkPin: %v", err)
	}
	if !matching.Matches || matching.SourceURL != url || matching.SHA256 != digestOf(aDocument) {
		t.Errorf("a matching upstream measured as %+v", matching)
	}

	moved := pinFor(t, url, "what the lock used to pin")
	differing, err := checkPin(moved, unparsed)
	if err != nil {
		t.Fatalf("checkPin: %v", err)
	}
	if differing.Matches {
		t.Error("bytes that differ from the pin measured as a match")
	}
	if differing.SHA256 != digestOf(aDocument) {
		t.Error("the measurement did not describe what was actually served")
	}
	if got := differing.Describe(); !strings.Contains(got, shortSHA(digestOf(aDocument))) {
		t.Errorf("Describe() = %q does not carry the digest", got)
	}
}

// TestUnit_Refresh_CheckUpstreamRefusesAnUnpinnedID: measuring an id the lock
// does not carry is a caller bug, and must not touch the network.
func TestUnit_Refresh_CheckUpstreamRefusesAnUnpinnedID(t *testing.T) {
	t.Parallel()

	if _, err := CheckUpstream("no-such-document", unparsed); err == nil ||
		!strings.Contains(err.Error(), "pins no document") {
		t.Fatalf("CheckUpstream of an unpinned id: %v", err)
	}
}

// stubDescriber stands where a real parser goes, answering values no fixture
// could produce by accident so a pin carrying them proves the describer was
// consulted.
func stubDescriber([]byte) (version string, paths, operations int) {
	return "9.9.9", 7, 11
}

// writeLock writes a lock file for RewriteLock tests and points EnvLockPath at
// it.
func writeLock(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), LockFile)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvLockPath, path)

	return path
}

// TestUnit_Refresh_RewriteLockRestatesOnlyThePinsThatMoved is the pin-update
// contract: a moved upstream is restated from what it now serves, an unmoved
// one is left byte-for-byte alone, and keys this build does not know about
// survive.
func TestUnit_Refresh_RewriteLockRestatesOnlyThePinsThatMoved(t *testing.T) {
	movedURL, _ := serve(t, aDocument)
	unmovedURL, _ := serve(t, aDocument)

	// The seam keeps the rewritten timestamp assertable.
	was := nowUTC
	nowUTC = func() time.Time { return time.Date(2026, 8, 9, 10, 11, 12, 0, time.UTC) }
	defer func() { nowUTC = was }()

	path := writeLock(t, `{
  "$comment": "kept",
  "formatVersion": "1",
  "unknownTopLevel": true,
  "openapi": {
    "moved": {
      "version": "0.9.0",
      "sha256": "`+digestOf("what the lock used to pin")+`",
      "pinnedAt": "2026-01-02T03:04:05Z",
      "upstreamUrl": "`+movedURL+`",
      "unknownEntryKey": "kept too",
      "pathCount": 9,
      "operationCount": 9
    },
    "unmoved": {
      "version": "1.2.3",
      "sha256": "`+digestOf(aDocument)+`",
      "pinnedAt": "2026-01-02T03:04:05Z",
      "upstreamUrl": "`+unmovedURL+`",
      "pathCount": 1,
      "operationCount": 1
    }
  }
}`)

	if err := RewriteLock([]string{"moved", "unmoved"}, stubDescriber); err != nil {
		t.Fatalf("RewriteLock: %v", err)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the rewritten lock is not JSON: %v", err)
	}
	if doc["$comment"] != "kept" || doc["unknownTopLevel"] != true {
		t.Error("rewriting dropped keys it does not own")
	}

	entries := doc["openapi"].(map[string]any)

	moved := entries["moved"].(map[string]any)
	if moved["sha256"] != digestOf(aDocument) {
		t.Errorf("the moved pin's sha256 was not restated: %v", moved["sha256"])
	}
	if moved["pinnedAt"] != "2026-08-09T10:11:12.000000Z" {
		t.Errorf("the moved pin's pinnedAt = %v, want the rewrite time", moved["pinnedAt"])
	}
	if moved["unknownEntryKey"] != "kept too" {
		t.Error("rewriting dropped an entry key it does not own")
	}
	// The pin records what the describer measured. A rewrite that ignored it
	// would write the old version back, or none at all, and either reads as
	// a document that did not move.
	if moved["version"] != "9.9.9" {
		t.Errorf("the moved pin's version = %v, want the describer's measurement", moved["version"])
	}
	if moved["pathCount"] != float64(7) || moved["operationCount"] != float64(11) {
		t.Errorf("the moved pin's counts = %v/%v, want the describer's measurement",
			moved["pathCount"], moved["operationCount"])
	}

	unmoved := entries["unmoved"].(map[string]any)
	if unmoved["pinnedAt"] != "2026-01-02T03:04:05Z" || unmoved["version"] != "1.2.3" {
		t.Errorf("a pin that still matches upstream was restated: %+v", unmoved)
	}
}

// TestUnit_Refresh_RewriteLockFailsClosed pins every way a rewrite can refuse:
// no file, unparsable file, no openapi object, an id the lock does not pin,
// and an unreachable upstream. None may write.
func TestUnit_Refresh_RewriteLockFailsClosed(t *testing.T) {
	t.Setenv(EnvLockPath, filepath.Join(t.TempDir(), "absent", LockFile))
	if err := RewriteLock(nil, unparsed); err == nil || !strings.Contains(err.Error(), "reading") {
		t.Errorf("a missing lock: %v", err)
	}

	writeLock(t, "{not json")
	if err := RewriteLock(nil, unparsed); err == nil || !strings.Contains(err.Error(), "parsing") {
		t.Errorf("an unparsable lock: %v", err)
	}

	writeLock(t, `{"formatVersion": "1"}`)
	if err := RewriteLock(nil, unparsed); err == nil || !strings.Contains(err.Error(), "no openapi object") {
		t.Errorf("a lock with no openapi object: %v", err)
	}

	writeLock(t, `{"openapi": {}}`)
	if err := RewriteLock([]string{"ghost"}, unparsed); err == nil || !strings.Contains(err.Error(), `no pin for "ghost"`) {
		t.Errorf("an id the lock does not pin: %v", err)
	}

	path := writeLock(t, `{"openapi": {"dead": {
  "version": "1.2.3",
  "sha256": "`+digestOf(aDocument)+`",
  "pinnedAt": "2026-01-02T03:04:05Z",
  "upstreamUrl": "http://127.0.0.1:1/never-listening",
  "pathCount": 1,
  "operationCount": 1
}}}`)
	before, err := os.ReadFile(path) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}
	if err := RewriteLock([]string{"dead"}, unparsed); err == nil || !strings.Contains(err.Error(), "dead:") {
		t.Errorf("an unreachable upstream: %v", err)
	}
	after, err := os.ReadFile(path) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a failed rewrite still wrote the lock")
	}
}
