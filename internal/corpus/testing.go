package corpus

import (
	"errors"
	"fmt"
)

// TB is the part of *testing.T these helpers need.
//
// Declared rather than importing testing so this package stays usable from the
// CLI without dragging testing's flag registration into the binary.
type TB interface {
	Helper()
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// Document returns the pinned document, or ends the test.
//
// Skipping when offline and failing when TFPFGEN_CORPUS_REQUIRED is set is a
// deliberate split rather than a hedge. Failing always would turn a
// developer's flaky connection into a red suite, which teaches people to
// ignore red. Skipping always would let CI skip its way to green, which is a
// standing lie about coverage. So the machine that must be honest fails, and
// the machine that must be usable skips -- and after one warm run neither
// applies, because the cache is keyed by content and is only invalidated by a
// lock change.
//
// A document that was fetched but did not match its pin is never skipped: that
// is a change in what the tests mean, and it fails everywhere.
func Document(t TB, id string) CachedDocument {
	return document(t, id, Ensure)
}

// document is Document with the materialiser supplied, so the skip and fail
// policies are testable without the network or the embedded lock.
func document(t TB, id string, ensure func(string) (CachedDocument, error)) CachedDocument {
	t.Helper()

	cached, err := ensure(id)
	if err == nil {
		return cached
	}

	if errors.Is(err, ErrOffline) && !Required() {
		t.Skipf("the pinned %s document is not cached and could not be fetched: %v\n"+
			"run the tests once while online, or point %s at a warm cache",
			id, err, EnvCacheDir)
		return CachedDocument{}
	}

	t.Fatalf("%v", err)

	return CachedDocument{}
}

// DocumentRoot is the directory holding a document's cached copies, which is
// what a flag naming a document directory expects.
func DocumentRoot(t TB, id string) string {
	t.Helper()
	Document(t, id)

	return Root(id)
}

// SpecPath is the pinned document itself, for a flag that names a file.
func SpecPath(t TB, id string) string {
	t.Helper()

	return Document(t, id).SpecPath()
}

// MustPin returns a document's pin, so a test asserting on version or scale
// reads the assertion from the lock rather than restating it.
//
// A test that hardcodes "7.0.99" has to be found and edited on every pin
// update, and the one that is missed fails somewhere unrelated to the change.
func MustPin(t TB, id string) Pin {
	t.Helper()

	pin, err := PinFor(id)
	if err != nil {
		t.Fatalf("%v", err)
	}

	return pin
}

// Describe renders a pin for a failure message.
func (p Pin) Describe() string {
	return fmt.Sprintf("version %s (sha256:%s, %d paths, %d operations)",
		p.Version, shortSHA(p.SHA256), p.PathCount, p.OperationCount)
}
