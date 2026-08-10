package corpus

// The cache layout, one directory per pinned document:
//
//	<root>/openapi/<id>/<version>-t<epochMillis>/
//	  api.yaml        the document
//	  metadata.json   version, checksum, source and counts
//
// A directory is never rewritten. It is named from the pin's version and
// timestamp, so a lock change resolves to a new directory and an old cache can
// never be silently reused under a new pin. The metadata file makes each copy
// self-describing, so a corrupted or hand-edited cache is detected by Verify
// rather than surfacing as a nonsense test failure downstream.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// ErrNoDocument reports that a document is not in the cache, which is the
// normal state before the first fetch rather than a failure.
var ErrNoDocument = errors.New("no cached copy of the document")

// ErrChecksumMismatch reports a cached document that does not match its own
// metadata, meaning it was edited or truncated in place.
var ErrChecksumMismatch = errors.New("the cached document does not match its metadata checksum")

const (
	// SpecFileName is what every cached copy stores its document as. The
	// directory carries the version, so the file does not need to.
	SpecFileName = "api.yaml"
	// MetadataFileName describes a cached copy without re-parsing the
	// document.
	MetadataFileName = "metadata.json"
)

// namePattern matches "<version>-t<epochMillis>".
var namePattern = regexp.MustCompile(`^(.+)-t(\d+)$`)

// CachedDocument is one pinned document materialised on disk.
type CachedDocument struct {
	// Dir is the document's directory.
	Dir string
	// Name is the directory's base name, encoding version and timestamp.
	Name string
	// Version is the upstream version it was pinned at.
	Version string
	// Timestamp is when it was pinned, decoded from the directory name.
	Timestamp time.Time
}

// SpecPath is the document's path.
func (d CachedDocument) SpecPath() string { return filepath.Join(d.Dir, SpecFileName) }

// MetadataPath is the metadata's path.
func (d CachedDocument) MetadataPath() string { return filepath.Join(d.Dir, MetadataFileName) }

// Metadata is written beside each document so a cached copy is
// self-describing.
type Metadata struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	// SourceURL is where the document came from, so a pin review does not
	// depend on somebody remembering.
	SourceURL string    `json:"sourceUrl,omitempty"`
	FetchedAt time.Time `json:"fetchedAt"`
	// PathCount and OperationCount make the scale of a change visible without
	// reading megabytes of YAML.
	PathCount      int `json:"pathCount,omitempty"`
	OperationCount int `json:"operationCount,omitempty"`
}

// dirName builds the directory name for a version pinned at a given time.
func dirName(version string, at time.Time) string {
	if version == "" {
		version = "unknown"
	}
	return fmt.Sprintf("%s-t%d", version, at.UnixMilli())
}

// find resolves a cached copy by its directory name.
func find(root, name string) (CachedDocument, error) {
	dir := filepath.Join(root, name)

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return CachedDocument{}, fmt.Errorf("%w: %s", ErrNoDocument, dir)
	}

	version := name
	var at time.Time
	if m := namePattern.FindStringSubmatch(name); m != nil {
		version = m[1]
		if millis, pErr := strconv.ParseInt(m[2], 10, 64); pErr == nil {
			at = time.UnixMilli(millis).UTC()
		}
	}

	return CachedDocument{Dir: dir, Name: name, Version: version, Timestamp: at}, nil
}

// LoadMetadata reads a cached copy's metadata.
func (d CachedDocument) LoadMetadata() (Metadata, error) {
	data, err := os.ReadFile(d.MetadataPath()) //nolint:gosec // a name this package built
	if err != nil {
		return Metadata{}, fmt.Errorf("reading %s: %w", d.MetadataPath(), err)
	}

	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return Metadata{}, fmt.Errorf("parsing %s: %w", d.MetadataPath(), err)
	}

	return m, nil
}

// Verify checks the cached document against the checksum its metadata claims.
//
// A cached copy is meant to be immutable, and the failure this catches is
// somebody editing one in place to work around a parsing bug. That would make
// every test that reads it assert against bytes nothing pins, which is the one
// property this package exists to prevent.
func (d CachedDocument) Verify() error {
	m, err := d.LoadMetadata()
	if err != nil {
		return err
	}

	sum, err := d.Checksum()
	if err != nil {
		return err
	}

	if m.SHA256 != "" && sum != m.SHA256 {
		return fmt.Errorf("%w: %s claims %s but the file is %s",
			ErrChecksumMismatch, d.Name, shortSHA(m.SHA256), shortSHA(sum))
	}

	return nil
}

// Checksum returns the cached document's SHA-256.
func (d CachedDocument) Checksum() (string, error) {
	data, err := os.ReadFile(d.SpecPath()) //nolint:gosec // a name this package built
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", d.SpecPath(), err)
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:]), nil
}

// store writes a new cached copy: the document plus its self-describing
// metadata.
//
// It refuses to overwrite. A cached copy is immutable by design -- Verify
// exists to catch in-place edits -- so the only way to take a new one is a new
// directory, and a collision on the millisecond timestamp is an error rather
// than a shrug.
func store(root string, doc []byte, meta Metadata) (CachedDocument, error) {
	if meta.SHA256 == "" {
		sum := sha256.Sum256(doc)
		meta.SHA256 = hex.EncodeToString(sum[:])
	}

	dir := filepath.Join(root, dirName(meta.Version, meta.FetchedAt))
	if _, err := os.Stat(dir); err == nil {
		return CachedDocument{}, fmt.Errorf("%s already exists; a cached copy is immutable "+
			"and is replaced by storing a new one", dir)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return CachedDocument{}, fmt.Errorf("creating %s: %w", dir, err)
	}

	if err := os.WriteFile(filepath.Join(dir, SpecFileName), doc, 0o600); err != nil {
		return CachedDocument{}, fmt.Errorf("writing the document: %w", err)
	}

	encoded, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return CachedDocument{}, fmt.Errorf("encoding metadata: %w", err)
	}
	encoded = append(encoded, '\n')

	if err := os.WriteFile(filepath.Join(dir, MetadataFileName), encoded, 0o600); err != nil {
		return CachedDocument{}, fmt.Errorf("writing metadata: %w", err)
	}

	cached := CachedDocument{
		Dir:       dir,
		Name:      filepath.Base(dir),
		Version:   meta.Version,
		Timestamp: meta.FetchedAt,
	}

	// Read back through the same gate every consumer uses, so a store that
	// wrote something Verify would refuse cannot return success.
	if err := cached.Verify(); err != nil {
		return CachedDocument{}, fmt.Errorf("verifying the cached copy just written: %w", err)
	}

	return cached, nil
}
