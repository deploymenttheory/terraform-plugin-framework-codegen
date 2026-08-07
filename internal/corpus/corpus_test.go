package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// aDocument is a minimal OpenAPI document. Small on purpose: these tests are
// about pinning and caching, and a real specification would only make them slow
// and their failures harder to read.
const aDocument = `openapi: 3.0.3
info:
  title: Fixture
  version: 1.2.3
paths:
  /widgets:
    get:
      operationId: listWidgets
      responses:
        "200":
          description: ok
`

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// serve returns a server for body, and a counter of how many times it was asked.
func serve(t *testing.T, body string) (url string, hits *atomic.Int64) {
	t.Helper()

	hits = &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv.URL, hits
}

func pinFor(t *testing.T, url, body string) Pin {
	t.Helper()

	return Pin{
		Version:        "1.2.3",
		SHA256:         digestOf(body),
		PinnedAt:       time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		UpstreamURL:    url,
		PathCount:      1,
		OperationCount: 1,
	}
}

// TestUnit_Corpus_LockPinsTheDocumentsTheTestsRead guards the lock itself. It is
// the file every other test's meaning rests on, so a malformed or truncated one
// should fail here rather than as a confusing failure somewhere downstream.
func TestUnit_Corpus_LockPinsTheDocumentsTheTestsRead(t *testing.T) {
	t.Parallel()

	l, err := LoadLock()
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}

	for _, id := range []string{ThousandEyes, JamfPro, GitHub} {
		pin, ok := l.OpenAPI[id]
		if !ok {
			t.Errorf("the lock pins no %q", id)
			continue
		}
		if len(pin.SHA256) != 64 {
			t.Errorf("%s: sha256 %q is not a full digest", id, pin.SHA256)
		}
		if pin.UpstreamURL == "" {
			t.Errorf("%s: no upstream URL, so it can never be fetched", id)
		}
		if pin.PathCount == 0 || pin.OperationCount == 0 {
			t.Errorf("%s: zero path or operation count, so a truncated download would pass unnoticed", id)
		}
		if pin.PinnedAt.IsZero() {
			t.Errorf("%s: no pinnedAt, so the cache directory name is not stable", id)
		}
	}
}

// TestUnit_Corpus_ColdCacheFetchesAndWarmCacheDoesNot is the property the whole
// scheme is for: the network is touched once, and never again until the pin
// changes.
func TestUnit_Corpus_ColdCacheFetchesAndWarmCacheDoesNot(t *testing.T) {
	t.Parallel()

	url, hits := serve(t, aDocument)
	root := filepath.Join(t.TempDir(), "openapi", "fixture")
	pin := pinFor(t, url, aDocument)

	first, err := materialisePin("fixture", root, pin)
	if err != nil {
		t.Fatalf("cold materialise: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("cold cache made %d request(s), want 1", got)
	}

	second, err := materialisePin("fixture", root, pin)
	if err != nil {
		t.Fatalf("warm materialise: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("warm cache made %d request(s) in total, want 1 -- it refetched", got)
	}

	if first.Dir != second.Dir {
		t.Errorf("the same pin resolved to two directories: %s then %s", first.Dir, second.Dir)
	}
	if _, err := os.Stat(first.SpecPath()); err != nil {
		t.Errorf("the document is not where the snapshot says: %v", err)
	}
}

// TestUnit_Corpus_ADocumentThatIsNotThePinnedOneFailsLoudly is the failure that
// must never be a skip: the tests assert facts about the pinned document, so
// different bytes mean something different, not merely a transport problem.
func TestUnit_Corpus_ADocumentThatIsNotThePinnedOneFailsLoudly(t *testing.T) {
	t.Parallel()

	url, _ := serve(t, aDocument)
	root := filepath.Join(t.TempDir(), "openapi", "fixture")

	pin := pinFor(t, url, aDocument)
	pin.SHA256 = digestOf("something else entirely")

	_, err := materialisePin("fixture", root, pin)
	if err == nil {
		t.Fatal("a document that did not match its pin was accepted")
	}

	msg := err.Error()
	for _, want := range []string{
		"is not what",
		shortSHA(pin.SHA256),
		shortSHA(digestOf(aDocument)),
		"1.2.3",
		"corpus refresh -id fixture",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the mismatch message does not mention %q:\n%s", want, msg)
		}
	}
}

// TestUnit_Corpus_AStaleCachedCopyIsReplaced covers the case a plain existence
// check would get wrong: a directory with the right name whose bytes are not the
// pinned ones, which is what an interrupted write or a reverted lock leaves.
func TestUnit_Corpus_AStaleCachedCopyIsReplaced(t *testing.T) {
	t.Parallel()

	url, hits := serve(t, aDocument)
	root := filepath.Join(t.TempDir(), "openapi", "fixture")
	pin := pinFor(t, url, aDocument)

	if _, err := materialisePin("fixture", root, pin); err != nil {
		t.Fatalf("seeding the cache: %v", err)
	}

	// Corrupt the cached document, leaving its metadata claiming the old digest.
	name := filepath.Join(root, "1.2.3-t"+fmt.Sprint(pin.PinnedAt.UnixMilli()), "api.yaml")
	if err := os.WriteFile(name, []byte("not the document"), 0o600); err != nil {
		t.Fatal(err)
	}

	snap, err := materialisePin("fixture", root, pin)
	if err != nil {
		t.Fatalf("recovering from a corrupted cache: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("made %d request(s) in total, want 2 -- the corrupted copy was reused", got)
	}
	if err := snap.Verify(); err != nil {
		t.Errorf("the recovered snapshot does not verify: %v", err)
	}
}

// TestUnit_Corpus_TheMirrorIsPreferredAndCannotChangeMeaning proves both halves
// of the mirror's contract: it is asked first, and bytes that are not the pinned
// ones are refused however authoritative the source looked.
func TestUnit_Corpus_TheMirrorIsPreferredAndCannotChangeMeaning(t *testing.T) {
	t.Parallel()

	mirror, mirrorHits := serve(t, aDocument)
	upstream, upstreamHits := serve(t, aDocument)

	root := filepath.Join(t.TempDir(), "openapi", "fixture")
	pin := pinFor(t, upstream, aDocument)
	pin.MirrorURL = mirror

	if _, err := materialisePin("fixture", root, pin); err != nil {
		t.Fatalf("materialise: %v", err)
	}
	if mirrorHits.Load() != 1 || upstreamHits.Load() != 0 {
		t.Errorf("mirror asked %d time(s), upstream %d -- the mirror should answer first",
			mirrorHits.Load(), upstreamHits.Load())
	}

	// A mirror serving the wrong bytes is refused exactly as an upstream would be.
	wrong, _ := serve(t, "a different document")
	bad := pinFor(t, upstream, aDocument)
	bad.MirrorURL = wrong

	if _, err := materialisePin("fixture", filepath.Join(t.TempDir(), "o", "f"), bad); err == nil {
		t.Error("a mirror serving unpinned bytes was accepted")
	}
}

// TestUnit_Corpus_AnUnreachableSourceIsOfflineNotCorruption keeps the two
// failure modes apart. Offline is skippable; a mismatch never is.
func TestUnit_Corpus_AnUnreachableSourceIsOfflineNotCorruption(t *testing.T) {
	t.Parallel()

	pin := Pin{
		Version:     "1.2.3",
		SHA256:      digestOf(aDocument),
		PinnedAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		UpstreamURL: "http://127.0.0.1:1/never-listening",
	}

	_, err := materialisePin("fixture", filepath.Join(t.TempDir(), "openapi", "fixture"), pin)
	if err == nil {
		t.Fatal("an unreachable source produced no error")
	}
	if !strings.Contains(err.Error(), ErrOffline.Error()) {
		t.Errorf("an unreachable source should be reported as offline, got: %v", err)
	}
}

// TestUnit_Corpus_CacheDirIsRedirectable matters because CI must be able to put
// the cache where actions/cache can restore it.
func TestUnit_Corpus_CacheDirIsRedirectable(t *testing.T) {
	t.Setenv(EnvCacheDir, filepath.Join("somewhere", "else"))

	if got, want := CacheDir(), filepath.Join("somewhere", "else"); got != want {
		t.Errorf("CacheDir() = %q, want %q", got, want)
	}
	if got, want := Root("fixture"), filepath.Join("somewhere", "else", "openapi", "fixture"); got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

// TestUnit_Corpus_TheDefaultCacheIsNotRelative is the guard for a mistake that
// has already been made once.
//
// A relative default resolves against the working directory, and `go test` sets
// that per package -- so a single suite run scatters a copy of every document
// through the tree, in directories no root-anchored .gitignore pattern reaches.
// It put 19 MB of fetched specifications into a commit that existed to remove
// artefacts from this repository. The cache must be absolute and outside any
// checkout, whatever else changes.
func TestUnit_Corpus_TheDefaultCacheIsNotRelative(t *testing.T) {
	t.Setenv(EnvCacheDir, "")

	dir := CacheDir()
	if !filepath.IsAbs(dir) {
		t.Fatalf("the default cache is %q, which is relative to the working directory", dir)
	}

	// The working directory during a test is its own package directory, so a
	// cache anywhere beneath it is a cache inside the repository.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(wd, dir); err == nil && !strings.HasPrefix(rel, "..") {
		t.Errorf("the default cache %q is inside the package directory %q", dir, wd)
	}
}
