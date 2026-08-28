package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleYAML = `openapi: 3.0.3
info:
  title: sample
  version: 1.2.3
paths: {}
`

const sampleJSON = `{"openapi":"3.1.0","info":{"title":"sample","version":"9.0.0"},"paths":{}}`

func writeSample(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "published.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnit_SpecStore_FirstImportPinsDocumentAndLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spec")

	res, err := Import(dir, []byte(sampleYAML), "https://vendor.example/openapi.yaml")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Outcome != Pinned {
		t.Fatalf("outcome = %v, want Pinned", res.Outcome)
	}
	if res.Previous != nil {
		t.Fatalf("a first import has nothing previous, got %+v", res.Previous)
	}

	document, err := os.ReadFile(filepath.Join(dir, DocumentName))
	if err != nil {
		t.Fatalf("the document was not written: %v", err)
	}
	if string(document) != sampleYAML {
		t.Fatalf("the stored document is not byte-for-byte what was published:\n%s", document)
	}

	lock, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify straight after Import: %v", err)
	}
	if lock.Source != "https://vendor.example/openapi.yaml" {
		t.Errorf("lock.Source = %q", lock.Source)
	}
	if lock.OpenAPI != "3.0.3" || lock.DocumentVersion != "1.2.3" {
		t.Errorf("lock versions = %q / %q, want 3.0.3 / 1.2.3", lock.OpenAPI, lock.DocumentVersion)
	}
	if lock.Format != "yaml" {
		t.Errorf("lock.Format = %q, want yaml", lock.Format)
	}
	if lock.FetchedAt.IsZero() || lock.FetchedAt.Location() != time.UTC {
		t.Errorf("lock.FetchedAt = %v, want a non-zero UTC time", lock.FetchedAt)
	}
	if len(lock.SHA256) != 64 {
		t.Errorf("lock.SHA256 = %q, want a full hex digest", lock.SHA256)
	}
}

func TestUnit_SpecStore_JSONDocumentIsStoredAsFetchedNotConverted(t *testing.T) {
	dir := t.TempDir()

	res, err := Import(dir, []byte(sampleJSON), "https://vendor.example/openapi.json")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.Lock.Format != "json" {
		t.Errorf("lock.Format = %q, want json", res.Lock.Format)
	}
	if res.Lock.OpenAPI != "3.1.0" || res.Lock.DocumentVersion != "9.0.0" {
		t.Errorf("lock versions = %q / %q", res.Lock.OpenAPI, res.Lock.DocumentVersion)
	}

	document, err := os.ReadFile(res.DocumentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(document) != sampleJSON {
		t.Fatalf("the JSON bytes were reformatted:\n%s", document)
	}
}

func TestUnit_SpecStore_ReimportingIdenticalContentChangesNothing(t *testing.T) {
	dir := t.TempDir()

	if _, err := Import(dir, []byte(sampleYAML), "first.yaml"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, LockName))
	if err != nil {
		t.Fatal(err)
	}

	res, err := Import(dir, []byte(sampleYAML), "second.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != Unchanged {
		t.Fatalf("outcome = %v, want Unchanged", res.Outcome)
	}
	// The returned lock is the existing pin — source and timestamp included.
	if res.Lock.Source != "first.yaml" {
		t.Errorf("res.Lock.Source = %q, want the original pin's source", res.Lock.Source)
	}

	after, err := os.ReadFile(filepath.Join(dir, LockName))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("an identical re-import rewrote the lock:\n%s\nvs\n%s", before, after)
	}
}

func TestUnit_SpecStore_ReimportingChangedContentMovesThePinAndReportsThePrevious(t *testing.T) {
	dir := t.TempDir()

	first, err := Import(dir, []byte(sampleYAML), "v1.yaml")
	if err != nil {
		t.Fatal(err)
	}

	changed := strings.ReplaceAll(sampleYAML, "1.2.3", "2.0.0")
	res, err := Import(dir, []byte(changed), "v2.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != Repinned {
		t.Fatalf("outcome = %v, want Repinned", res.Outcome)
	}
	if res.Previous == nil || res.Previous.SHA256 != first.Lock.SHA256 {
		t.Fatalf("Previous does not record the replaced pin: %+v", res.Previous)
	}
	if res.Lock.SHA256 == first.Lock.SHA256 {
		t.Fatal("the pin did not move")
	}
	if res.Lock.DocumentVersion != "2.0.0" {
		t.Errorf("new lock version = %q, want 2.0.0", res.Lock.DocumentVersion)
	}

	if _, err := Verify(dir); err != nil {
		t.Fatalf("Verify after a repin: %v", err)
	}
}

func TestUnit_SpecStore_ImportRestoresATamperedDocumentUnderAnUnchangedLock(t *testing.T) {
	dir := t.TempDir()

	if _, err := Import(dir, []byte(sampleYAML), "v1.yaml"); err != nil {
		t.Fatal(err)
	}
	// Somebody edits the pinned document in place; the lock still records the
	// published hash. Importing the published bytes again must heal the tree,
	// not report "nothing changed" over a broken pin.
	if err := os.WriteFile(filepath.Join(dir, DocumentName), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Import(dir, []byte(sampleYAML), "v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome == Unchanged {
		t.Fatal("a tampered document was reported as already pinned")
	}
	if _, err := Verify(dir); err != nil {
		t.Fatalf("Verify after the healing import: %v", err)
	}
}

func TestUnit_SpecStore_ImportRefusesADocumentWithoutAnOpenAPIKey(t *testing.T) {
	_, err := Import(t.TempDir(), []byte("info:\n  title: not a spec\n"), "x.yaml")
	if err == nil || !strings.Contains(err.Error(), "openapi") {
		t.Fatalf("err = %v, want a refusal naming the missing openapi key", err)
	}
}

func TestUnit_SpecStore_ImportRefusesASwagger2Document(t *testing.T) {
	_, err := Import(t.TempDir(), []byte("swagger: \"2.0\"\ninfo:\n  version: 1\n"), "x.yaml")
	if err == nil || !strings.Contains(err.Error(), "Swagger 2.0") {
		t.Fatalf("err = %v, want a refusal naming Swagger 2.0", err)
	}
}

func TestUnit_SpecStore_ImportRefusesUnparseableBytes(t *testing.T) {
	_, err := Import(t.TempDir(), []byte("\topenapi: {{{"), "x.yaml")
	if err == nil || !strings.Contains(err.Error(), "not parseable") {
		t.Fatalf("err = %v, want a parse refusal", err)
	}
}

func TestUnit_SpecStore_ImportRefusesACorruptLock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, LockName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Import(dir, []byte(sampleYAML), "x.yaml")
	if err == nil || !strings.Contains(err.Error(), "parsing") {
		t.Fatalf("err = %v, want a lock parse failure", err)
	}
}

func TestUnit_SpecStore_ImportReportsAnUncreatableDirectory(t *testing.T) {
	base := t.TempDir()
	inTheWay := filepath.Join(base, "spec")
	if err := os.WriteFile(inTheWay, []byte("a file, not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Import(inTheWay, []byte(sampleYAML), "x.yaml")
	if err == nil {
		t.Fatal("importing into a path occupied by a file succeeded")
	}
}

func TestUnit_SpecStore_ImportReportsAnUnwritableDocumentPath(t *testing.T) {
	dir := t.TempDir()
	// A directory squatting on the document's name makes the write fail.
	if err := os.MkdirAll(filepath.Join(dir, DocumentName), 0o750); err != nil {
		t.Fatal(err)
	}
	_, err := Import(dir, []byte(sampleYAML), "x.yaml")
	if err == nil || !strings.Contains(err.Error(), "writing the document") {
		t.Fatalf("err = %v, want a document write failure", err)
	}
}

func TestUnit_SpecStore_LockJSONIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	if _, err := Import(dir, []byte(sampleYAML), "x.yaml"); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, LockName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "}\n") {
		t.Errorf("the lock does not end with a trailing newline: %q", raw)
	}

	var decoded Lock
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("the lock does not round-trip: %v", err)
	}

	// Field order is the struct's, not a map's: source before sha256 before
	// fetchedAt, every time.
	text := string(raw)
	if strings.Index(text, `"source"`) > strings.Index(text, `"sha256"`) ||
		strings.Index(text, `"sha256"`) > strings.Index(text, `"fetchedAt"`) {
		t.Errorf("lock keys are not in the fixed order:\n%s", text)
	}
}

func TestUnit_SpecStore_VerifyFailsWhenNothingIsPinned(t *testing.T) {
	_, err := Verify(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "tfpfgen spec import") {
		t.Fatalf("err = %v, want a pointer at `tfpfgen spec import`", err)
	}
}

func TestUnit_SpecStore_VerifyFailsWhenTheDocumentIsMissing(t *testing.T) {
	dir := t.TempDir()
	if _, err := Import(dir, []byte(sampleYAML), "x.yaml"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, DocumentName)); err != nil {
		t.Fatal(err)
	}

	_, err := Verify(dir)
	if err == nil || !strings.Contains(err.Error(), "does not exist but its lock does") {
		t.Fatalf("err = %v, want the missing-document message", err)
	}
}

func TestUnit_SpecStore_VerifyFailsOnATamperedDocumentNamingBothHashes(t *testing.T) {
	dir := t.TempDir()
	res, err := Import(dir, []byte(sampleYAML), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DocumentName), []byte("openapi: 3.0.3\nedited: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Verify(dir)
	if err == nil {
		t.Fatal("a tampered document verified clean")
	}
	if !strings.Contains(err.Error(), res.Lock.ShortSHA()) {
		t.Errorf("the mismatch does not quote the pinned hash: %v", err)
	}
	if !strings.Contains(err.Error(), "edited in place") {
		t.Errorf("the mismatch does not say what happened: %v", err)
	}
}

func TestUnit_SpecStore_VerifyFailsOnACorruptLock(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, LockName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err == nil {
		t.Fatal("a corrupt lock verified clean")
	}
}

func TestUnit_SpecStore_ShortSHAKeepsShortStringsWhole(t *testing.T) {
	if got := (Lock{SHA256: "abc"}).ShortSHA(); got != "abc" {
		t.Fatalf("ShortSHA = %q, want abc", got)
	}
	long := strings.Repeat("ab", 32)
	if got := (Lock{SHA256: long}).ShortSHA(); got != long[:12] {
		t.Fatalf("ShortSHA = %q, want the first twelve characters", got)
	}
}
