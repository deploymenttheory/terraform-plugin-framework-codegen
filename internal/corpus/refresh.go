package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LockFile is the pin set's name.
const LockFile = "corpus.lock.json"

// EnvLockPath relocates the lock for a pin update invoked from outside the
// repository root.
const EnvLockPath = "TFPFGEN_CORPUS_LOCK"

// LockPath is where RewriteLock writes.
//
// The lock is embedded for reading, so tests never depend on the working
// directory; writing is a different act, performed deliberately from a
// checkout by a person or a scheduled job, and needs a real path.
func LockPath() string {
	if p := os.Getenv(EnvLockPath); p != "" {
		return p
	}
	return filepath.Join("internal", "corpus", "testdata", LockFile)
}

// Upstream is what a source is serving now, measured against a pin.
type Upstream struct {
	// Matches reports that the served bytes are the pinned bytes.
	Matches bool
	// SourceURL is which source answered.
	SourceURL string
	// MirrorURL is the pin's mirror, echoed so a caller can warn about its
	// absence without re-reading the lock.
	MirrorURL string

	SHA256         string
	Version        string
	PathCount      int
	OperationCount int
}

// Describe renders an upstream result the same way a pin describes itself, so
// the two lines of a comparison line up under each other.
func (u Upstream) Describe() string {
	return fmt.Sprintf("version %s (sha256:%s, %d paths, %d operations)",
		u.Version, shortSHA(u.SHA256), u.PathCount, u.OperationCount)
}

// CheckUpstream fetches a document and reports how it compares to its pin.
//
// It never writes and never consults the cache: the question is what the
// vendor is serving right now, which a cache would answer wrongly by
// construction.
func CheckUpstream(id string, describe Describer) (Upstream, error) {
	pin, err := PinFor(id)
	if err != nil {
		return Upstream{}, err
	}

	return checkPin(pin, describe)
}

// checkPin is CheckUpstream with the pin supplied, so RewriteLock can measure
// against the on-disk lock rather than the embedded one.
func checkPin(pin Pin, describe Describer) (Upstream, error) {
	doc, source, err := fetch(pin)
	if err != nil {
		return Upstream{}, err
	}

	sum := sha256.Sum256(doc)
	digest := hex.EncodeToString(sum[:])

	version, paths, operations := describe(doc)

	return Upstream{
		Matches:        digest == pin.SHA256,
		SourceURL:      source,
		MirrorURL:      pin.MirrorURL,
		SHA256:         digest,
		Version:        version,
		PathCount:      paths,
		OperationCount: operations,
	}, nil
}

// RewriteLock restates the given documents' pins from what upstream now
// serves.
//
// PinnedAt moves to the fetch time, which changes the cache directory name and
// so guarantees the new bytes are materialised rather than an old cache being
// reused under a new hash.
//
// Pins are measured against the on-disk lock rather than the embedded copy:
// the embedded bytes are whatever was compiled in, and a rewrite must not
// silently discard an edit made since.
func RewriteLock(ids []string, describe Describer) error {
	path := LockPath()

	raw, err := os.ReadFile(path) //nolint:gosec // a fixed name under the repository
	if err != nil {
		return fmt.Errorf("reading %s (run this from the repository root, or set %s): %w",
			path, EnvLockPath, err)
	}

	// Decoded twice on purpose. The typed decoding supplies the pins to
	// measure against; the generic one is what is rewritten, so the file's
	// comment key and any field this build does not know about survive the
	// round trip.
	var disk Lock
	if err := json.Unmarshal(raw, &disk); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	documents, ok := doc["openapi"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s has no openapi object to rewrite", path)
	}

	for _, id := range ids {
		pin, ok := disk.OpenAPI[id]
		if !ok {
			return fmt.Errorf("%s has no pin for %q", path, id)
		}

		up, err := checkPin(pin, describe)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if up.Matches {
			continue
		}

		entry, ok := documents[id].(map[string]any)
		if !ok {
			return fmt.Errorf("%s has no pin for %q", path, id)
		}

		entry["version"] = up.Version
		entry["sha256"] = up.SHA256
		entry["pathCount"] = up.PathCount
		entry["operationCount"] = up.OperationCount
		entry["pinnedAt"] = nowUTC().Format("2006-01-02T15:04:05.000000Z")

		documents[id] = entry
	}

	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	encoded = append(encoded, '\n')

	return os.WriteFile(path, encoded, 0o600)
}

// nowUTC is a seam so the lock's timestamp is testable.
var nowUTC = func() time.Time { return time.Now().UTC() }
