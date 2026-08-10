// Package corpus acquires the real third-party API documents the tests run
// against, without committing any of them to this repository.
//
// This repository turns OpenAPI documents into providers; it is not the home
// of any particular vendor's documents. But the tests still need real ones to
// run against -- inference bugs are found by documents that disagree with each
// other, not by synthetic ones that agree with the code -- so the documents
// are fetched at test time and pinned here rather than vendored.
//
// Pinned means pinned. testdata/corpus.lock.json pins a version, a SHA-256 and
// the path and operation counts for every document, and a fetch that does not
// match all of them fails loudly. That matters more than it looks: several
// tests assert properties of these documents -- enum members, field shapes,
// operation counts -- so a vendor editing their specification silently changes
// what those tests mean. The lock turns that from an inexplicable failure into
// a deliberate review.
//
// The cache holds one verified copy of each pinned document, in a directory
// named deterministically from the pin so a pinned document is findable again
// rather than merely present. See cache.go for the layout.
package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "embed"
)

// lockBytes is the pin set, embedded so a test binary run from any directory
// resolves it identically. It is the reviewable file of this whole scheme:
// every change to what the tests read against shows up as a diff here.
//
//go:embed testdata/corpus.lock.json
var lockBytes []byte

// EnvCacheDir relocates the cache. CI sets it so the cache lands somewhere
// actions/cache can restore.
const EnvCacheDir = "TFPFGEN_CORPUS_DIR"

// EnvRequired makes a missing document a failure rather than a skip. Set in
// CI, unset on a developer's machine: see the Document test helper.
const EnvRequired = "TFPFGEN_CORPUS_REQUIRED"

// cacheSubdir is the corpus's place within the user cache directory.
//
// Deliberately outside the repository, and this is not a preference. A
// relative default is resolved against the working directory, and `go test`
// runs every package in its own directory -- so one suite run would scatter a
// copy of every document through the tree, in package directories no
// root-anchored .gitignore pattern reaches. v1 learned this the hard way: it
// put 19 MB of fetched specifications into a commit whose entire purpose was
// removing generated output from the repository.
//
// A cache belongs where the operating system puts caches. CI overrides it with
// TFPFGEN_CORPUS_DIR to somewhere its cache action can restore.
var cacheSubdir = filepath.Join("tfpfgen", "corpus")

// ErrOffline reports that a document is not cached and could not be fetched.
// Callers decide whether that is fatal; the Document test helper applies the
// policy.
var ErrOffline = errors.New("the document is not cached and could not be fetched")

// Lock is corpus.lock.json.
type Lock struct {
	FormatVersion string         `json:"formatVersion"`
	OpenAPI       map[string]Pin `json:"openapi"`
}

// Pin is one document, identified precisely enough that a fetch either
// reproduces it or fails.
//
// Which documents are pinned is data, not code: the ids live in the lock file
// alone, so no vendor name appears in this package's source.
type Pin struct {
	// Version is the document's own declared version.
	Version string `json:"version"`
	// SHA256 is of the exact bytes. The whole scheme rests on this.
	SHA256 string `json:"sha256"`
	// PinnedAt seeds the cache directory name, which is what makes a cached
	// document findable again rather than merely present.
	PinnedAt time.Time `json:"pinnedAt"`
	// UpstreamURL is where the vendor publishes it.
	UpstreamURL string `json:"upstreamUrl"`
	// MirrorURL holds the exact pinned bytes under our own control, and is
	// tried first.
	//
	// Not a nicety. Several of these upstreams serve "current" rather than a
	// version, so the pinned bytes become unfetchable the moment the vendor
	// publishes again, and every pin update is forced on their schedule. A
	// mirror cannot alter test meaning -- the SHA-256 is checked identically
	// whichever source answered -- so it is a CDN, not a source of truth.
	MirrorURL string `json:"mirrorUrl,omitempty"`
	// PathCount and OperationCount catch a truncated download that happens to
	// parse. Without them a half-fetched document produces a nonsense test
	// failure a long way from its cause.
	PathCount      int `json:"pathCount"`
	OperationCount int `json:"operationCount"`
	// Large marks a document big enough that -short should skip work using it.
	Large bool `json:"large,omitempty"`
}

var (
	lockOnce sync.Once
	lock     Lock
	lockErr  error
)

// LoadLock parses the embedded pin set.
func LoadLock() (Lock, error) {
	lockOnce.Do(func() {
		if err := json.Unmarshal(lockBytes, &lock); err != nil {
			lockErr = fmt.Errorf("parsing corpus.lock.json: %w", err)
			return
		}
		if len(lock.OpenAPI) == 0 {
			lockErr = errors.New("corpus.lock.json pins no documents")
		}
	})
	return lock, lockErr
}

// PinFor returns one document's pin.
func PinFor(id string) (Pin, error) {
	l, err := LoadLock()
	if err != nil {
		return Pin{}, err
	}
	p, ok := l.OpenAPI[id]
	if !ok {
		known := make([]string, 0, len(l.OpenAPI))
		for k := range l.OpenAPI {
			known = append(known, k)
		}
		return Pin{}, fmt.Errorf("corpus.lock.json pins no document %q (it pins %v)", id, known)
	}
	return p, nil
}

// IDs lists every pinned document.
func IDs() ([]string, error) {
	l, err := LoadLock()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(l.OpenAPI))
	for k := range l.OpenAPI {
		out = append(out, k)
	}
	return out, nil
}

// CacheDir is the corpus cache root.
func CacheDir() string {
	if d := os.Getenv(EnvCacheDir); d != "" {
		return d
	}

	base, err := os.UserCacheDir()
	if err != nil {
		// No user cache directory is unusual but survivable: fall back to the
		// system temp directory, which is still outside any repository. What
		// must not happen is a relative path, for the reason cacheSubdir
		// gives.
		base = os.TempDir()
	}

	return filepath.Join(base, cacheSubdir)
}

// Root is the directory holding a document's cached copies, which is what a
// flag naming a document directory wants.
func Root(id string) string {
	return filepath.Join(CacheDir(), "openapi", id)
}

// Required reports whether a document that cannot be obtained is fatal.
func Required() bool { return os.Getenv(EnvRequired) != "" }

// perID serialisation, so parallel tests in one process do not race to fetch
// the same document.
var (
	mu    sync.Mutex
	onces = map[string]*sync.Once{}
	got   = map[string]CachedDocument{}
	errs  = map[string]error{}
)

// Ensure returns the pinned document, fetching it if the cache is cold.
//
// Lazily and per id rather than from a TestMain: a TestMain cannot skip, so an
// offline developer would get a package-wide bail-out instead of a skip on the
// few tests that needed the network, and `go test -run` of one unrelated test
// would still pull megabytes it has no use for.
func Ensure(id string) (CachedDocument, error) {
	mu.Lock()
	once, ok := onces[id]
	if !ok {
		once = &sync.Once{}
		onces[id] = once
	}
	mu.Unlock()

	once.Do(func() {
		d, err := materialise(id)
		mu.Lock()
		got[id], errs[id] = d, err
		mu.Unlock()
	})

	mu.Lock()
	defer mu.Unlock()

	return got[id], errs[id]
}

// materialise resolves a document from the cache, or fetches and stores it.
func materialise(id string) (CachedDocument, error) {
	pin, err := PinFor(id)
	if err != nil {
		return CachedDocument{}, err
	}

	return materialisePin(id, Root(id), pin)
}

// materialisePin is materialise with the pin and destination supplied, so the
// cache, fetch and verification paths are testable without the embedded lock
// or the network.
func materialisePin(id, root string, pin Pin) (CachedDocument, error) {
	name := dirName(pin.Version, pin.PinnedAt)

	// Two gates on a cache hit, deliberately. Verify catches a corrupted or
	// edited cache; comparing against the lock catches a cache written by an
	// older lock, which would otherwise be silently reused under a new pin.
	if doc, fErr := find(root, name); fErr == nil {
		if vErr := doc.Verify(); vErr == nil {
			if sum, cErr := doc.Checksum(); cErr == nil && sum == pin.SHA256 {
				return doc, nil
			}
		}
		// A cached copy that fails either gate is not repairable in place --
		// cached copies are immutable by design -- so it is removed and
		// refetched.
		if rErr := os.RemoveAll(doc.Dir); rErr != nil {
			return CachedDocument{}, fmt.Errorf("removing the unusable cached %s: %w", id, rErr)
		}
	}

	doc, source, err := fetch(pin)
	if err != nil {
		return CachedDocument{}, fmt.Errorf("%w: %s: %w", ErrOffline, id, err)
	}

	if err := verifyAgainstPin(id, doc, pin, source); err != nil {
		return CachedDocument{}, err
	}

	return publish(root, name, doc, pin, source)
}

// publish writes the document into a private directory and renames it into
// place.
//
// `go test ./...` runs packages as separate processes, so several can race to
// materialise the same document on a cold cache. store refuses to overwrite --
// immutability is the property it exists to defend -- so without atomic
// publication the first run after a lock change would fail spuriously in
// whichever packages lost the race. A rename is atomic, and losing the race
// means somebody else already published the identical bytes.
func publish(root, name string, doc []byte, pin Pin, source string) (CachedDocument, error) {
	// Staged as a sibling of the destination so the publishing rename stays
	// within one filesystem, which is what makes it atomic.
	if err := os.MkdirAll(filepath.Dir(root), 0o750); err != nil {
		return CachedDocument{}, fmt.Errorf("creating %s: %w", filepath.Dir(root), err)
	}

	staging, err := os.MkdirTemp(filepath.Dir(root), ".corpus-staging-*")
	if err != nil {
		return CachedDocument{}, fmt.Errorf("creating a staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	if _, err := store(staging, doc, Metadata{
		Version:        pin.Version,
		SHA256:         pin.SHA256,
		SourceURL:      source,
		FetchedAt:      pin.PinnedAt,
		PathCount:      pin.PathCount,
		OperationCount: pin.OperationCount,
	}); err != nil {
		return CachedDocument{}, err
	}

	if err := os.MkdirAll(root, 0o750); err != nil {
		return CachedDocument{}, fmt.Errorf("creating %s: %w", root, err)
	}

	final := filepath.Join(root, name)
	if err := os.Rename(filepath.Join(staging, name), final); err != nil && !os.IsExist(err) {
		// A concurrent publisher may have won between the check and the
		// rename.
		if _, sErr := os.Stat(final); sErr != nil {
			return CachedDocument{}, fmt.Errorf("publishing %s: %w", final, err)
		}
	}

	cached, err := find(root, name)
	if err != nil {
		return CachedDocument{}, err
	}

	return cached, cached.Verify()
}

// verifyAgainstPin refuses a document that is not the one the lock names.
//
// Never a skip and never a silent re-pin. Tests assert properties of these
// documents, so a changed document is a change in what they mean.
func verifyAgainstPin(id string, doc []byte, pin Pin, source string) error {
	sum := sha256.Sum256(doc)
	digest := hex.EncodeToString(sum[:])
	if digest == pin.SHA256 {
		return nil
	}

	// Parsed only to describe the mismatch. What a reviewer needs is what
	// changed, not that a hash differed.
	version, paths, operations := describe(doc)

	return fmt.Errorf(`the pinned %s document is not what %s served.
  pinned  sha256:%s  version %s  %d path(s) / %d operation(s)
  fetched sha256:%s  version %s  %d path(s) / %d operation(s)
Tests assert properties of the pinned document -- enum members, field shapes,
operation counts -- so this changes what they mean rather than merely failing a
transport check. Review the change deliberately and update the %s pin in
internal/corpus/testdata/corpus.lock.json`,
		id, source,
		shortSHA(pin.SHA256), pin.Version, pin.PathCount, pin.OperationCount,
		shortSHA(digest), version, paths, operations,
		id)
}

// Describer parses a document enough to say what it is, for failure messages.
//
// A hook rather than a direct call on the OpenAPI parsing package, because
// that package's own tests are in-package and are among this package's most
// important consumers: importing it here would make them an import cycle. The
// CLI installs the real parser; anything that has not is describing a failure
// it is already reporting by digest.
var Describer = func([]byte) (version string, paths, operations int) {
	return "unparsed", 0, 0
}

// describe reports what a document says about itself, best effort.
func describe(doc []byte) (version string, paths, operations int) {
	return Describer(doc)
}

func shortSHA(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
