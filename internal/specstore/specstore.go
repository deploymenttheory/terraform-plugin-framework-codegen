// Package specstore manages pinned copies of an API specification.
//
// Generation must be reproducible offline. If ingestion read a specification
// from the network, two runs a week apart could produce different providers with
// no committed evidence of why, and the drift gate would report a change nobody
// made. So specifications are pinned: fetched deliberately, stored immutably, and
// committed alongside the blueprints they produced.
//
// The layout is inherited from go-sdk-thousandeyes, which pins specifications the
// same way for the same reason:
//
//	openapi-specs/<provider>/<version>-t<epochMillis>/
//	  api.yaml        the document
//	  metadata.json   version, checksum, source and counts
//
// A directory is never rewritten. Fetching an upstream change adds a new one, so
// the history of what was generated from what stays intact and a regression can
// be bisected against the specification as well as the generator.
package specstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// ErrNoSnapshot reports that nothing is pinned yet, which is the normal state
// before the first fetch rather than a failure.
var ErrNoSnapshot = errors.New("no specification snapshot found")

// ErrChecksumMismatch reports a stored document that does not match its recorded
// checksum, meaning it was edited in place.
var ErrChecksumMismatch = errors.New("snapshot checksum does not match its metadata")

const (
	// SpecFileName is what every snapshot stores its document as. The directory
	// carries the version, so the file does not need to.
	SpecFileName = "api.yaml"
	// MetadataFileName describes a snapshot without re-parsing the document.
	MetadataFileName = "metadata.json"
)

// dirPattern matches "<version>-t<epochMillis>".
var dirPattern = regexp.MustCompile(`^(.+)-t(\d+)$`)

// Snapshot is one pinned specification.
type Snapshot struct {
	// Dir is the snapshot directory, as given to Find.
	Dir string
	// Name is the directory's base name, which is what a blueprint records.
	Name string
	// Version is the upstream version it was taken at.
	Version string
	// Timestamp is when it was taken, decoded from the directory name.
	Timestamp time.Time
}

// SpecPath is the document's path.
func (s Snapshot) SpecPath() string { return filepath.Join(s.Dir, SpecFileName) }

// MetadataPath is the metadata's path.
func (s Snapshot) MetadataPath() string { return filepath.Join(s.Dir, MetadataFileName) }

// Metadata is written beside each document so a snapshot is self-describing.
type Metadata struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	// SourcePath and SourceURL record where the document came from, so a
	// refresh does not depend on somebody remembering.
	SourcePath string    `json:"sourcePath,omitempty"`
	SourceURL  string    `json:"sourceUrl,omitempty"`
	FetchedAt  time.Time `json:"fetchedAt"`
	// PathCount and OperationCount make the scale of a change visible in a diff
	// without reading 1.6 MB of YAML.
	PathCount      int `json:"pathCount,omitempty"`
	OperationCount int `json:"operationCount,omitempty"`
	// CatalogueEntries records the component documents a unified specification
	// was assembled from, when the upstream publishes them separately.
	CatalogueEntryCount int      `json:"catalogueEntryCount,omitempty"`
	CatalogueEntries    []string `json:"catalogueEntries,omitempty"`
}

// DirName builds the directory name for a version taken at a given time.
func DirName(version string, at time.Time) string {
	if version == "" {
		version = "unknown"
	}
	return fmt.Sprintf("%s-t%d", version, at.UnixMilli())
}

// List returns every snapshot under root, newest first.
//
// Ordering is by the timestamp encoded in the directory name, not by filesystem
// times: a git checkout does not preserve mtimes, so ordering by them would give
// a different answer on CI than on the machine that wrote them.
func List(root string) ([]Snapshot, error) {
	entries, err := os.ReadDir(root)
	switch {
	case os.IsNotExist(err):
		return nil, fmt.Errorf("%w under %s", ErrNoSnapshot, root)
	case err != nil:
		return nil, fmt.Errorf("reading %s: %w", root, err)
	}

	var out []Snapshot

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		m := dirPattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}

		millis, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			// The name matched \d+ so this cannot normally happen; skipping is
			// still better than failing the whole listing for one odd directory.
			continue
		}

		out = append(out, Snapshot{
			Dir:       filepath.Join(root, e.Name()),
			Name:      e.Name(),
			Version:   m[1],
			Timestamp: time.UnixMilli(millis).UTC(),
		})
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%w under %s", ErrNoSnapshot, root)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })

	return out, nil
}

// Latest returns the most recent snapshot under root.
func Latest(root string) (Snapshot, error) {
	all, err := List(root)
	if err != nil {
		return Snapshot{}, err
	}
	return all[0], nil
}

// Find resolves a snapshot by name, or the latest when name is empty.
func Find(root, name string) (Snapshot, error) {
	if name == "" {
		return Latest(root)
	}

	all, err := List(root)
	if err != nil {
		return Snapshot{}, err
	}

	for _, s := range all {
		if s.Name == name {
			return s, nil
		}
	}

	available := make([]string, 0, len(all))
	for _, s := range all {
		available = append(available, s.Name)
	}

	return Snapshot{}, fmt.Errorf("%w: %q under %s (available: %v)", ErrNoSnapshot, name, root, available)
}

// LoadMetadata reads a snapshot's metadata.
func (s Snapshot) LoadMetadata() (Metadata, error) {
	data, err := os.ReadFile(s.MetadataPath()) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return Metadata{}, fmt.Errorf("reading %s: %w", s.MetadataPath(), err)
	}

	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return Metadata{}, fmt.Errorf("parsing %s: %w", s.MetadataPath(), err)
	}

	return m, nil
}

// Verify checks the stored document against its recorded checksum.
//
// A snapshot is meant to be immutable, and the failure this catches is somebody
// editing one in place to work around an ingestion bug. That would make the
// generated provider unreproducible from anything committed, which is the one
// property pinning exists to provide.
func (s Snapshot) Verify() error {
	m, err := s.LoadMetadata()
	if err != nil {
		return err
	}

	sum, err := s.Checksum()
	if err != nil {
		return err
	}

	if m.SHA256 != "" && sum != m.SHA256 {
		return fmt.Errorf("%w: %s records %s but the file is %s",
			ErrChecksumMismatch, s.Name, short(m.SHA256), short(sum))
	}

	return nil
}

// Checksum returns the stored document's SHA-256.
func (s Snapshot) Checksum() (string, error) {
	data, err := os.ReadFile(s.SpecPath()) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", s.SpecPath(), err)
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:]), nil
}

func short(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
