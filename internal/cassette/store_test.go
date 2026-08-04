package cassette

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleInteractions() []Interaction {
	return []Interaction{
		{
			ID: "001-get-tags", Seq: 1,
			Request:  Request{Method: "GET", Path: "/v7/tags", Query: map[string][]string{"limit": {"10"}}},
			Response: Response{Status: 200, Body: map[string]any{"tags": []any{}}},
		},
		{
			ID: "002-post-tags", Seq: 2,
			Request:  Request{Method: "POST", Path: "/v7/tags", Body: map[string]any{"key": "probe"}},
			Response: Response{Status: 201, Body: map[string]any{"id": "1", "key": "probe"}},
		},
	}
}

func sampleMetadata() Metadata {
	return Metadata{
		Provider:     "example",
		Resource:     "tag",
		APIVersion:   "7.0.97",
		Host:         "api.example.com",
		ProbeVersion: "1",
	}
}

func TestUnit_Cassette_WriteAndRead(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	at := time.UnixMilli(1785152261691)

	snap, err := Record(root, sampleMetadata(), sampleInteractions(), nil, at)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// The directory name mirrors the snapshot store's, so both stores read the same way.
	if got := filepath.Base(snap.Dir); got != "7.0.97-t1785152261691" {
		t.Errorf("directory = %q, want 7.0.97-t1785152261691", got)
	}

	// One file per interaction, not one array: a re-record that changes one response gives
	// a one-file diff, and the numbered prefix makes ordering explicit rather than
	// positional.
	entries, err := os.ReadDir(snap.InteractionsDir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("wrote %d interaction files, want 2", len(entries))
	}
	if entries[0].Name() != "001-get-tags.json" {
		t.Errorf("first file = %q", entries[0].Name())
	}

	meta, err := snap.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.Interactions != 2 || meta.SHA256 == "" {
		t.Errorf("metadata = %+v", meta)
	}

	got, err := snap.LoadInteractions()
	if err != nil {
		t.Fatalf("LoadInteractions: %v", err)
	}
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Errorf("loaded %d interactions in the wrong order: %+v", len(got), got)
	}

	if err := snap.Verify(); err != nil {
		t.Errorf("Verify on a freshly written snapshot: %v", err)
	}
}

// TestUnit_Cassette_WriteIsDeterministic.
//
// A cassette is committed and diffed by CI, so writing the same session twice must produce
// identical bytes. Otherwise the drift gate fires on runs that changed nothing, and a gate
// that cries wolf gets disabled.
//
// Twenty-five iterations for the same reason internal/generate uses that many: map iteration
// order is randomised per run, so a single comparison would pass by luck often enough to be
// useless.
func TestUnit_Cassette_WriteIsDeterministic(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1785152261691)

	var first map[string]string

	for range 25 {
		root := t.TempDir()

		snap, err := Record(root, sampleMetadata(), sampleInteractions(), nil, at)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}

		got := readTree(t, snap.Dir)

		if first == nil {
			first = got
			continue
		}

		if len(got) != len(first) {
			t.Fatalf("wrote %d files, previously %d", len(got), len(first))
		}
		for name, content := range got {
			if content != first[name] {
				t.Fatalf("%s differs between runs:\n--- first\n%s\n--- now\n%s", name, first[name], content)
			}
		}
	}
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()

	out := map[string]string{}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // a temp dir this test just wrote
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	return out
}

// TestUnit_Cassette_KeyOrderProducesNoDiff.
//
// Bodies are stored as parsed JSON and re-encoded canonically, so a server that reorders
// its own output produces no diff at all. This is the single biggest source of churn a
// naive recorder would have.
func TestUnit_Cassette_KeyOrderProducesNoDiff(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1785152261691)

	oneOrder := sampleInteractions()
	oneOrder[1].Response.Body = map[string]any{"id": "1", "key": "probe", "colour": "blue"}

	otherOrder := sampleInteractions()
	otherOrder[1].Response.Body = map[string]any{"colour": "blue", "key": "probe", "id": "1"}

	rootA, rootB := t.TempDir(), t.TempDir()

	snapA, err := Record(rootA, sampleMetadata(), oneOrder, nil, at)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	snapB, err := Record(rootB, sampleMetadata(), otherOrder, nil, at)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	treeA, treeB := readTree(t, snapA.Dir), readTree(t, snapB.Dir)

	for name := range treeA {
		if treeA[name] != treeB[name] {
			t.Errorf("%s differs on key order alone:\n--- a\n%s\n--- b\n%s", name, treeA[name], treeB[name])
		}
	}
}

func TestUnit_Cassette_VerifyDetectsTampering(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	snap, err := Record(root, sampleMetadata(), sampleInteractions(), nil, time.UnixMilli(1))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// A hand-edited response body. Verify has to catch it, or a committed cassette could be
	// altered to support a fact the API never produced -- which would make the whole
	// evidence chain worthless.
	path := filepath.Join(snap.InteractionsDir(), "002-post-tags.json")

	data, err := os.ReadFile(path) //nolint:gosec // a temp dir this test just wrote
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	tampered := strings.Replace(string(data), `"probe"`, `"tampered"`, 1)
	if tampered == string(data) {
		t.Fatal("the substitution did not apply, so the tampering case is untested")
	}
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := snap.Verify(); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("error = %v, want ErrChecksumMismatch", err)
	}

	// A removed interaction is caught by the count as well as the checksum.
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := snap.Verify(); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("a missing interaction should fail Verify, got %v", err)
	}
}

// TestUnit_Cassette_LoadRejectsASequenceGap.
//
// Ordering by the Seq field and then checking for gaps, rather than trusting filename
// order. The two agree today because the id embeds the sequence, but relying on that would
// make a hand-edited cassette replay in a subtly wrong order rather than failing.
func TestUnit_Cassette_LoadRejectsASequenceGap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	gapped := []Interaction{
		{ID: "001-get-tags", Seq: 1, Request: Request{Method: "GET", Path: "/tags"}, Response: Response{Status: 200}},
		{ID: "003-get-tags", Seq: 3, Request: Request{Method: "GET", Path: "/tags"}, Response: Response{Status: 200}},
	}

	snap, err := Record(root, sampleMetadata(), gapped, nil, time.UnixMilli(1))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := snap.LoadInteractions(); !errors.Is(err, ErrInvalidCassette) {
		t.Errorf("error = %v, want ErrInvalidCassette", err)
	}
}

func TestUnit_Cassette_ListLatestAndFind(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	older, err := Record(root, sampleMetadata(), sampleInteractions(), nil, time.UnixMilli(1000))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	newer, err := Record(root, sampleMetadata(), sampleInteractions(), nil, time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	all, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List gave %d snapshots, want 2", len(all))
	}
	// Oldest first, ordered by the millis encoded in the name rather than by mtime: a git
	// checkout does not preserve modification times, so mtime ordering would give a
	// different answer in CI than locally.
	if all[0].Millis != 1000 || all[1].Millis != 2000 {
		t.Errorf("List is not ordered by encoded timestamp: %+v", all)
	}

	latest, err := Latest(root)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.Dir != newer.Dir {
		t.Errorf("Latest = %s, want %s", latest.Dir, newer.Dir)
	}

	found, err := Find(root, filepath.Base(older.Dir))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found.Dir != older.Dir {
		t.Errorf("Find = %s, want %s", found.Dir, older.Dir)
	}

	if _, err := Find(root, "9.9.9-t1"); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("Find on an absent snapshot = %v, want ErrNoSnapshot", err)
	}

	// A directory that is not a snapshot is ignored rather than breaking the listing.
	if err := os.MkdirAll(filepath.Join(root, "not-a-snapshot"), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if all, err = List(root); err != nil || len(all) != 2 {
		t.Errorf("a stray directory broke List: %v, %d", err, len(all))
	}
}

func TestUnit_Cassette_StoreEdgeCases(t *testing.T) {
	t.Parallel()

	// An absent root lists nothing rather than erroring: a first run has no evidence
	// directory yet, and that is not a failure.
	all, err := List(filepath.Join(t.TempDir(), "absent"))
	if err != nil || all != nil {
		t.Errorf("List on an absent root = %v, %v", all, err)
	}

	if _, err := Latest(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("Latest on an absent root = %v, want ErrNoSnapshot", err)
	}

	// Writing nothing is refused: an empty snapshot would satisfy Verify and support no
	// facts, which is a worse artefact than none.
	if _, err := Record(t.TempDir(), sampleMetadata(), nil, nil, time.UnixMilli(1)); !errors.Is(err, ErrInvalidCassette) {
		t.Errorf("error = %v, want ErrInvalidCassette", err)
	}

	// An unversioned recording still gets a usable directory name.
	root := t.TempDir()
	meta := sampleMetadata()
	meta.APIVersion = ""

	snap, err := Record(root, meta, sampleInteractions(), nil, time.UnixMilli(5))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(snap.Dir), "unknown-t") {
		t.Errorf("directory = %q, want an unknown- prefix", filepath.Base(snap.Dir))
	}

	// Reading a snapshot that is not there.
	missing := Snapshot{Dir: filepath.Join(t.TempDir(), "nope")}
	if _, err := missing.LoadMetadata(); err == nil {
		t.Error("LoadMetadata on an absent snapshot should fail")
	}
	if _, err := missing.LoadInteractions(); err == nil {
		t.Error("LoadInteractions on an absent snapshot should fail")
	}
	if err := missing.Verify(); err == nil {
		t.Error("Verify on an absent snapshot should fail")
	}

	// Paths are derived, not stored.
	s := Snapshot{Dir: "/tmp/x"}
	for name, got := range map[string]string{
		"metadata.json": s.MetadataPath(),
		"facts.json":    s.FactsPath(),
		"report.json":   s.ReportPath(),
		"interactions":  s.InteractionsDir(),
	} {
		if filepath.Base(got) != name {
			t.Errorf("path for %s = %q", name, got)
		}
	}
}

func TestUnit_Cassette_InteractionIDIsStableAndBounded(t *testing.T) {
	t.Parallel()

	// Deterministic given sequence, method and path, so a fact can cite one as evidence and
	// still be checkable after a re-record that did not change the session's shape.
	if a, b := interactionID(4, "POST", "/v7/tags"), interactionID(4, "POST", "/v7/tags"); a != b {
		t.Errorf("the id is not stable: %q vs %q", a, b)
	}
	if got := interactionID(4, "POST", "/v7/tags"); got != "004-post-v7-tags" {
		t.Errorf("id = %q, want 004-post-v7-tags", got)
	}

	// Separator runs collapse, so a templated path does not become a string of dashes.
	if got := slug("/v7/tags/{id}/assignments"); strings.Contains(got, "--") {
		t.Errorf("slug = %q, want no repeated separators", got)
	}

	// These become filenames, so an absurd path has to be bounded.
	long := "/v7/" + strings.Repeat("segment/", 40)
	if got := slug(long); len(got) > 48 {
		t.Errorf("slug is %d characters, want at most 48", len(got))
	}

	// A path with nothing usable still yields a filename.
	if got := slug("///"); got != "root" {
		t.Errorf("slug(///) = %q, want root", got)
	}
}

func TestUnit_Cassette_RawBody(t *testing.T) {
	t.Parallel()

	// JSON re-encodes.
	r := Response{Body: map[string]any{"a": 1}}
	got, err := r.RawBody()
	if err != nil || string(got) != `{"a":1}` {
		t.Errorf("RawBody = %q, %v", got, err)
	}

	// A non-JSON body comes back verbatim, which is what the error-envelope probe needs
	// when a proxy returns HTML.
	r = Response{BodyBase64: "aGVsbG8="}
	got, err = r.RawBody()
	if err != nil || string(got) != "hello" {
		t.Errorf("RawBody = %q, %v", got, err)
	}

	// An empty body is distinct from an absent one, which is one of the four error shapes.
	if got, err := (Response{}).RawBody(); err != nil || got != nil {
		t.Errorf("RawBody on an empty response = %q, %v", got, err)
	}

	// Corrupt base64 is reported rather than silently yielding nothing.
	if _, err := (Response{BodyBase64: "!!!not base64!!!"}).RawBody(); !errors.Is(err, ErrInvalidCassette) {
		t.Errorf("error = %v, want ErrInvalidCassette", err)
	}
}

func TestUnit_Cassette_ContentTypeDetection(t *testing.T) {
	t.Parallel()

	jsonTypes := []string{
		"application/json",
		"application/json; charset=utf-8",
		"application/hal+json",
		"application/problem+json",
		"application/vnd.example.v2+json",
		"APPLICATION/JSON",
	}
	for _, ct := range jsonTypes {
		if !isJSON(ct) {
			t.Errorf("isJSON(%q) = false", ct)
		}
	}

	for _, ct := range []string{"text/html", "application/xml", "", "text/plain"} {
		if isJSON(ct) {
			t.Errorf("isJSON(%q) = true", ct)
		}
	}

	// A response with no content type but a JSON body is still parsed, because APIs do
	// this and storing it as base64 would make the cassette unreadable.
	if !looksLikeJSON([]byte(`  {"a":1}`)) {
		t.Error("a body starting with { should look like JSON")
	}
	if !looksLikeJSON([]byte(`[1,2]`)) {
		t.Error("a body starting with [ should look like JSON")
	}
	if looksLikeJSON([]byte("hello")) || looksLikeJSON(nil) {
		t.Error("non-JSON must not be treated as JSON")
	}
}

func TestUnit_Cassette_DecodeBodyFallsBackToBase64(t *testing.T) {
	t.Parallel()

	// Content type claims JSON and the body is not: usually an error page from a proxy,
	// which is a real observation about the API's edge and worth keeping verbatim.
	parsed, b64, ct := decodeBody([]byte("<html>502</html>"), "application/json")
	if parsed != nil {
		t.Errorf("a non-JSON body should not parse: %v", parsed)
	}
	if b64 == "" {
		t.Error("a non-JSON body must be kept as base64")
	}
	if ct != "application/json" {
		t.Errorf("the content type should be recorded, got %q", ct)
	}

	// An empty body yields nothing at all rather than an empty string.
	if parsed, b64, ct := decodeBody(nil, ""); parsed != nil || b64 != "" || ct != "" {
		t.Errorf("decodeBody(nil) = %v, %q, %q", parsed, b64, ct)
	}
}
