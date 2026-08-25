package vendor_openapi_specs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// aDocument is a minimal OpenAPI document. Small on purpose: these tests are
// about pinning and caching, and a real specification would only make them
// slow and their failures harder to read.
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

// serve returns a server for body, and a counter of how many times it was
// asked.
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

// TestUnit_VendorOpenAPISpecs_LockPinsTheDocumentsTheTestsRead guards the lock itself. It
// is the file every other test's meaning rests on, so a malformed or truncated
// one should fail here rather than as a confusing failure somewhere
// downstream.
func TestUnit_VendorOpenAPISpecs_LockPinsTheDocumentsTheTestsRead(t *testing.T) {
	t.Parallel()

	l, err := LoadLock()
	if err != nil {
		t.Fatalf("LoadLock: %v", err)
	}

	ids, err := IDs()
	if err != nil {
		t.Fatalf("IDs: %v", err)
	}
	sort.Strings(ids)

	// The vendor ids live in the lock alone; this is the one place the pinned
	// set itself is asserted.
	if want := []string{"github", "thousandeyes"}; !slicesEqual(ids, want) {
		t.Errorf("the lock pins %v, want %v", ids, want)
	}

	for id, pin := range l.OpenAPI {
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
		if pin.Version == "" || pin.Version == "unparsed" {
			t.Errorf("%s: version %q, so the pin records no measurement of what it pinned", id, pin.Version)
		}
		if ref := movingRef(pin.UpstreamURL); ref != "" {
			t.Errorf("%s: the upstream URL names %q, a ref that moves; pin a commit or a tag instead", id, ref)
		}
	}
}

// movingRefs are the branch names a source-hosting URL carries when it names
// the tip of a branch rather than a fixed revision.
var movingRefs = []string{"main", "master", "HEAD", "latest", "trunk", "develop"}

// movingRef reports the moving ref an upstream URL names, empty when it names
// none.
//
// A pin whose URL follows a branch re-pins itself every time the vendor
// publishes: the hash stops matching, every test reading it fails, and the
// only remedy is to restate the pin, which is the review the lock exists to
// force. Pinning a revision makes the pin mean something.
func movingRef(upstream string) string {
	for _, segment := range strings.Split(upstream, "/") {
		for _, ref := range movingRefs {
			if segment == ref {
				return ref
			}
		}
	}
	return ""
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestUnit_VendorOpenAPISpecs_ColdCacheFetchesAndWarmCacheDoesNot is the property the
// whole scheme is for: the network is touched once, and never again until the
// pin changes.
func TestUnit_VendorOpenAPISpecs_ColdCacheFetchesAndWarmCacheDoesNot(t *testing.T) {
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
		t.Errorf("the document is not where the cached copy says: %v", err)
	}
}

// TestUnit_VendorOpenAPISpecs_ADocumentThatIsNotThePinnedOneFailsLoudly is the failure
// that must never be a skip: the tests assert properties of the pinned
// document, so different bytes mean something different, not merely a
// transport problem.
func TestUnit_VendorOpenAPISpecs_ADocumentThatIsNotThePinnedOneFailsLoudly(t *testing.T) {
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
		"vendor_openapi_specs.lock.json",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the mismatch message does not mention %q:\n%s", want, msg)
		}
	}
}

// TestUnit_VendorOpenAPISpecs_ACorruptedCachedCopyIsReplaced covers the case a plain
// existence check would get wrong: a directory with the right name whose bytes
// are not the pinned ones, which is what an interrupted write leaves.
func TestUnit_VendorOpenAPISpecs_ACorruptedCachedCopyIsReplaced(t *testing.T) {
	t.Parallel()

	url, hits := serve(t, aDocument)
	root := filepath.Join(t.TempDir(), "openapi", "fixture")
	pin := pinFor(t, url, aDocument)

	if _, err := materialisePin("fixture", root, pin); err != nil {
		t.Fatalf("seeding the cache: %v", err)
	}

	// Corrupt the cached document, leaving its metadata claiming the old
	// digest.
	name := filepath.Join(root, "1.2.3-t"+fmt.Sprint(pin.PinnedAt.UnixMilli()), "api.yaml")
	if err := os.WriteFile(name, []byte("not the document"), 0o600); err != nil {
		t.Fatal(err)
	}

	cached, err := materialisePin("fixture", root, pin)
	if err != nil {
		t.Fatalf("recovering from a corrupted cache: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("made %d request(s) in total, want 2 -- the corrupted copy was reused", got)
	}
	if err := cached.Verify(); err != nil {
		t.Errorf("the recovered copy does not verify: %v", err)
	}
}

// TestUnit_VendorOpenAPISpecs_ACacheWrittenByAnOlderLockIsNotReused covers the second gate
// on a cache hit: a cached copy that is internally consistent but was written
// under a pin the lock no longer states.
func TestUnit_VendorOpenAPISpecs_ACacheWrittenByAnOlderLockIsNotReused(t *testing.T) {
	t.Parallel()

	oldURL, _ := serve(t, aDocument)
	root := filepath.Join(t.TempDir(), "openapi", "fixture")

	if _, err := materialisePin("fixture", root, pinFor(t, oldURL, aDocument)); err != nil {
		t.Fatalf("seeding the cache under the old pin: %v", err)
	}

	// The new pin keeps the version and timestamp -- so it resolves to the
	// same directory -- but names different bytes.
	const newDocument = aDocument + "# revised\n"
	newURL, newHits := serve(t, newDocument)
	pin := pinFor(t, newURL, newDocument)

	cached, err := materialisePin("fixture", root, pin)
	if err != nil {
		t.Fatalf("materialise under the new pin: %v", err)
	}
	if got := newHits.Load(); got != 1 {
		t.Errorf("the new pin made %d request(s), want 1 -- the old cache was reused or refetched twice", got)
	}
	if sum, err := cached.Checksum(); err != nil || sum != pin.SHA256 {
		t.Errorf("the cache still holds the old bytes: sum %s err %v", shortSHA(sum), err)
	}
}

// TestUnit_VendorOpenAPISpecs_TheMirrorIsPreferredAndCannotChangeMeaning proves both
// halves of the mirror's contract: it is asked first, and bytes that are not
// the pinned ones are refused however authoritative the source looked.
func TestUnit_VendorOpenAPISpecs_TheMirrorIsPreferredAndCannotChangeMeaning(t *testing.T) {
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

	// A mirror serving the wrong bytes is refused exactly as an upstream would
	// be.
	wrong, _ := serve(t, "a different document")
	bad := pinFor(t, upstream, aDocument)
	bad.MirrorURL = wrong

	if _, err := materialisePin("fixture", filepath.Join(t.TempDir(), "o", "f"), bad); err == nil {
		t.Error("a mirror serving unpinned bytes was accepted")
	}
}

// TestUnit_VendorOpenAPISpecs_AnUnreachableSourceIsOfflineNotCorruption keeps the two
// failure modes apart. Offline is skippable; a mismatch never is.
func TestUnit_VendorOpenAPISpecs_AnUnreachableSourceIsOfflineNotCorruption(t *testing.T) {
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

// TestUnit_VendorOpenAPISpecs_PublishToleratesLosingTheRace simulates the concurrent
// publisher: the destination already holds the identical bytes when the rename
// lands, and that is success, not failure.
func TestUnit_VendorOpenAPISpecs_PublishToleratesLosingTheRace(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "openapi", "fixture")
	pin := pinFor(t, "http://unused.invalid/", aDocument)
	name := dirName(pin.Version, pin.PinnedAt)

	if _, err := publish(root, name, []byte(aDocument), pin, "first"); err != nil {
		t.Fatalf("first publish: %v", err)
	}

	cached, err := publish(root, name, []byte(aDocument), pin, "second")
	if err != nil {
		t.Fatalf("publishing after another process already had: %v", err)
	}
	if err := cached.Verify(); err != nil {
		t.Errorf("the surviving copy does not verify: %v", err)
	}
}

// TestUnit_VendorOpenAPISpecs_PublishReportsAnUnwritableDestination: a destination that
// cannot be created is reported at the point of failure, not as a later
// missing-copy mystery.
func TestUnit_VendorOpenAPISpecs_PublishReportsAnUnwritableDestination(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	occupied := filepath.Join(base, "occupied")
	if err := os.WriteFile(occupied, []byte("a file, not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	pin := pinFor(t, "http://unused.invalid/", aDocument)
	name := dirName(pin.Version, pin.PinnedAt)

	// The destination's parent is a file.
	if _, err := publish(filepath.Join(occupied, "child"), name, []byte(aDocument), pin, "src"); err == nil {
		t.Error("publishing under a file succeeded")
	}

	// The destination itself is a file.
	if _, err := publish(occupied, name, []byte(aDocument), pin, "src"); err == nil {
		t.Error("publishing onto a file succeeded")
	}
}

// TestUnit_VendorOpenAPISpecs_ShortSHAAbbreviatesOnlyWhatIsLong keeps failure messages
// readable without ever truncating a digest into ambiguity silently.
func TestUnit_VendorOpenAPISpecs_ShortSHAAbbreviatesOnlyWhatIsLong(t *testing.T) {
	t.Parallel()

	if got := shortSHA("abc"); got != "abc" {
		t.Errorf("shortSHA of a short string = %q", got)
	}
	if got := shortSHA(digestOf(aDocument)); len(got) != 12 {
		t.Errorf("shortSHA of a digest = %q, want 12 characters", got)
	}
}

// TestUnit_VendorOpenAPISpecs_EnsureNamesTheLockOnAnUnknownID pins Ensure's failure mode
// for an id the lock does not carry, and that asking twice gives the one
// answer rather than re-resolving.
func TestUnit_VendorOpenAPISpecs_EnsureNamesTheLockOnAnUnknownID(t *testing.T) {
	t.Parallel()

	_, err := Ensure("no-such-document")
	if err == nil || !strings.Contains(err.Error(), "pins no document") {
		t.Fatalf("Ensure of an unpinned id: %v", err)
	}

	_, again := Ensure("no-such-document")
	if again == nil || again.Error() != err.Error() {
		t.Errorf("a second Ensure gave a different answer: %v then %v", err, again)
	}
}

// TestUnit_VendorOpenAPISpecs_CacheDirIsRedirectable matters because CI must be able to
// put the cache where actions/cache can restore it.
func TestUnit_VendorOpenAPISpecs_CacheDirIsRedirectable(t *testing.T) {
	t.Setenv(EnvCacheDir, filepath.Join("somewhere", "else"))

	if got, want := CacheDir(), filepath.Join("somewhere", "else"); got != want {
		t.Errorf("CacheDir() = %q, want %q", got, want)
	}
	if got, want := Root("fixture"), filepath.Join("somewhere", "else", "openapi", "fixture"); got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

// TestUnit_VendorOpenAPISpecs_TheDefaultCacheIsNotRelative is the guard for a mistake v1
// made once.
//
// A relative default resolves against the working directory, and `go test`
// sets that per package -- so a single suite run scatters a copy of every
// document through the tree, in directories no root-anchored .gitignore
// pattern reaches. The cache must be absolute and outside any checkout,
// whatever else changes.
func TestUnit_VendorOpenAPISpecs_TheDefaultCacheIsNotRelative(t *testing.T) {
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

// TestUnit_VendorOpenAPISpecs_NoUserCacheDirFallsBackToTempNotToRelative covers the
// machine with no resolvable user cache directory: the fallback must still be
// absolute and outside the checkout.
func TestUnit_VendorOpenAPISpecs_NoUserCacheDirFallsBackToTempNotToRelative(t *testing.T) {
	t.Setenv(EnvCacheDir, "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")

	dir := CacheDir()
	if !filepath.IsAbs(dir) {
		t.Fatalf("the fallback cache is %q, which is relative", dir)
	}
	if !strings.HasSuffix(dir, filepath.Join("tfpfgen", "vendor_openapi_specs")) {
		t.Errorf("the fallback cache %q is not under tfpfgen/vendor_openapi_specs", dir)
	}
}

// TestUnit_VendorOpenAPISpecs_AMovingRefIsRejected proves the guard above catches the
// shape it exists for, so it cannot pass by accident on a lock that happens
// to hold no branch URL.
func TestUnit_VendorOpenAPISpecs_AMovingRefIsRejected(t *testing.T) {
	t.Parallel()

	for _, url := range []string{
		"https://raw.githubusercontent.com/o/r/main/spec.json",
		"https://raw.githubusercontent.com/o/r/master/spec.json",
		"https://example.invalid/latest/openapi.yaml",
	} {
		if movingRef(url) == "" {
			t.Errorf("%s names a moving ref and was accepted", url)
		}
	}

	for _, url := range []string{
		"https://raw.githubusercontent.com/o/r/67c14c7efb01cdeeac0ecd8cee9fae8d7a80e2aa/spec.json",
		"https://raw.githubusercontent.com/o/r/v2.1.0/spec.json",
		"https://pubhub.devnetcloud.com/media/000-v7-apis/docs/reference/unified-oas/api.yaml",
	} {
		if ref := movingRef(url); ref != "" {
			t.Errorf("%s names a fixed revision and was rejected as %q", url, ref)
		}
	}
}
