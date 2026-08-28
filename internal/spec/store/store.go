// Package store pins the upstream OpenAPI document by hash.
//
// Everything downstream — audit observations, corrections, both generators —
// refers to one specific document the vendor published. If generation read
// that document from the network, two runs a week apart could differ with no
// committed evidence of why. So the document is imported once, deliberately:
// stored byte-for-byte as published in spec/upstream.yaml, beside a lock
// (spec/upstream.lock.json) recording where it came from, its SHA-256, and
// when it was taken. The imported document is immutable evidence; a vendor
// bump is a re-import that moves the pin visibly, never an edit in place.
//
// The bytes are stored exactly as fetched — a JSON-published document stays
// JSON. Reformatting happens, deterministically, at revision time; evidence
// does not get restyled.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// DocumentName is the pinned document's file name inside the spec
	// directory, whatever format the vendor published in.
	DocumentName = "upstream.yaml"
	// LockName is the lock beside it.
	LockName = "upstream.lock.json"
)

// Lock records what was pinned, from where, and when. Its JSON form is
// deterministic — a fixed field order from this struct, two-space indent, a
// trailing newline — so a re-import of identical content is a no-op in git
// terms too.
type Lock struct {
	// Source is the URL or file path the document was imported from, so a
	// vendor bump does not depend on somebody remembering.
	Source string `json:"source"`
	// SHA256 is the hex digest of the stored bytes. This is the pin.
	SHA256 string `json:"sha256"`
	// FetchedAt is when the document was imported, UTC.
	FetchedAt time.Time `json:"fetchedAt"`
	// Format is what the vendor published: "json" or "yaml".
	Format string `json:"format"`
	// OpenAPI is the document's declared specification version, e.g. "3.0.3".
	OpenAPI string `json:"openapi"`
	// DocumentVersion is the vendor's own info.version, when declared.
	DocumentVersion string `json:"documentVersion,omitempty"`
}

// ShortSHA renders the pin the way messages quote it.
func (l Lock) ShortSHA() string { return shortSHA(l.SHA256) }

// ImportAction is what an import did.
type ImportAction int

const (
	// Pinned: the first import — nothing was pinned before.
	Pinned ImportAction = iota
	// Unchanged: the document is already pinned byte-for-byte; nothing was
	// written.
	Unchanged
	// Repinned: a different document replaced the pinned one — a vendor bump
	// landing. Result.Previous records what it replaced.
	Repinned
)

// Result reports one import.
type Result struct {
	Outcome ImportAction
	Lock    Lock
	// Previous is the lock a repin replaced; nil otherwise.
	Previous *Lock

	DocumentPath string
	LockPath     string
}

// Import pins doc, as retrieved from source, into dir.
//
// The same content twice is a no-op: nothing is rewritten, not even the
// fetch timestamp, so an unchanged upstream leaves an unchanged tree.
// Different content replaces the pin and reports what it replaced.
func Import(dir string, document []byte, source string) (Result, error) {
	openAPI, docVersion, format, err := describe(document)
	if err != nil {
		return Result{}, err
	}

	res := Result{
		DocumentPath: filepath.Join(dir, DocumentName),
		LockPath:     filepath.Join(dir, LockName),
		Lock: Lock{
			Source:          source,
			SHA256:          digest(document),
			FetchedAt:       time.Now().UTC().Truncate(time.Second),
			Format:          format,
			OpenAPI:         openAPI,
			DocumentVersion: docVersion,
		},
	}

	previous, err := readLock(res.LockPath)
	switch {
	case err == nil && previous.SHA256 == res.Lock.SHA256 && storedDigest(res.DocumentPath) == res.Lock.SHA256:
		// Already pinned, and the stored bytes really are these bytes. (A
		// tampered or missing document falls through and is rewritten.)
		res.Outcome = Unchanged
		res.Lock = previous
		return res, nil
	case err == nil:
		res.Outcome = Repinned
		res.Previous = &previous
	case errors.Is(err, os.ErrNotExist):
		res.Outcome = Pinned
	default:
		return Result{}, err
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Result{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.WriteFile(res.DocumentPath, document, 0o600); err != nil {
		return Result{}, fmt.Errorf("writing the document: %w", err)
	}

	encoded, err := json.MarshalIndent(res.Lock, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("encoding the lock: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(res.LockPath, encoded, 0o600); err != nil {
		return Result{}, fmt.Errorf("writing the lock: %w", err)
	}

	return res, nil
}

// Verify recomputes the pinned document's digest and compares it to the
// lock. It catches the one edit pinning exists to make impossible: the
// stored document changing without the pin moving.
func Verify(dir string) (Lock, error) {
	documentPath := filepath.Join(dir, DocumentName)
	lockPath := filepath.Join(dir, LockName)

	lock, err := readLock(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		return Lock{}, fmt.Errorf("%s does not exist; nothing is pinned yet — run `tfpfgen spec import <url|file>`", lockPath)
	}
	if err != nil {
		return Lock{}, err
	}

	document, err := os.ReadFile(documentPath) //nolint:gosec // the fixed name under the operator-supplied dir
	if errors.Is(err, os.ErrNotExist) {
		return Lock{}, fmt.Errorf("%s does not exist but its lock does; restore the committed document or run `tfpfgen spec import %s`", documentPath, lock.Source)
	}
	if err != nil {
		return Lock{}, err
	}

	if got := digest(document); got != lock.SHA256 {
		return Lock{}, fmt.Errorf("%s does not match its lock: the lock pins sha256 %s but the file is %s — "+
			"the pinned document was edited in place; restore the committed bytes, or run `tfpfgen spec import %s` to move the pin deliberately",
			documentPath, shortSHA(lock.SHA256), shortSHA(digest(document)), lock.Source)
	}

	return lock, nil
}

// readLock reads and decodes a lock, passing os.ErrNotExist through for the
// callers that treat absence as a state rather than a failure.
func readLock(path string) (Lock, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the fixed name under the operator-supplied dir
	if err != nil {
		return Lock{}, err
	}
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return Lock{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return lock, nil
}

// describe validates that doc is an OpenAPI document and reports its
// declared versions and published format.
func describe(document []byte) (openAPI, docVersion, format string, err error) {
	var head struct {
		OpenAPI string `yaml:"openapi"`
		Swagger string `yaml:"swagger"`
		Info    struct {
			Version string `yaml:"version"`
		} `yaml:"info"`
	}
	if err := yaml.Unmarshal(document, &head); err != nil {
		return "", "", "", fmt.Errorf("the document is not parseable as YAML or JSON: %w", err)
	}
	if head.Swagger != "" {
		return "", "", "", fmt.Errorf("the document declares swagger %s — that is a Swagger 2.0 document, and tfpfgen imports OpenAPI 3 (an `openapi:` version key)", head.Swagger)
	}
	if head.OpenAPI == "" {
		return "", "", "", fmt.Errorf("the document has no `openapi:` version key, so it is not an OpenAPI document")
	}

	format = "yaml"
	if json.Valid(document) {
		format = "json"
	}
	return head.OpenAPI, head.Info.Version, format, nil
}

// digest is the hex SHA-256 of the given bytes.
func digest(document []byte) string {
	summary := sha256.Sum256(document)
	return hex.EncodeToString(summary[:])
}

// storedDigest is the digest of the file at path, or "" when it cannot be
// read — absence and unreadability both mean "not what the lock pins".
func storedDigest(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // the fixed name under the operator-supplied dir
	if err != nil {
		return ""
	}
	return digest(data)
}

// shortSHA quotes a hash the way messages want it: distinguishable at a
// glance, not sixty-four characters wide.
func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
