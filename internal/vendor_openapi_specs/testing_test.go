package vendor_openapi_specs

import (
	"fmt"
	"strings"
	"testing"
)

// fakeTB captures how a helper ended the test, since a real *testing.T would
// end this one.
type fakeTB struct {
	helped  bool
	skipped string
	fataled string
}

func (f *fakeTB) Helper() { f.helped = true }
func (f *fakeTB) Skipf(format string, args ...any) {
	f.skipped = fmt.Sprintf(format, args...)
}
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.fataled = fmt.Sprintf(format, args...)
}

func cachedFixture(err error) func(string) (CachedDocument, error) {
	return func(string) (CachedDocument, error) {
		if err != nil {
			return CachedDocument{}, err
		}
		return CachedDocument{Dir: "somewhere", Name: "1.2.3-t1"}, nil
	}
}

// TestUnit_Testing_DocumentReturnsTheCachedCopy is the happy path: no skip, no
// failure, the copy handed back.
func TestUnit_Testing_DocumentReturnsTheCachedCopy(t *testing.T) {
	t.Parallel()

	tb := &fakeTB{}
	cached := document(tb, "fixture", cachedFixture(nil))

	if !tb.helped || tb.skipped != "" || tb.fataled != "" {
		t.Errorf("a materialised document ended the test: %+v", tb)
	}
	if cached.Dir != "somewhere" {
		t.Errorf("document returned %+v", cached)
	}
}

// TestUnit_Testing_OfflineSkipsUnlessRequired is the policy split this helper
// exists for: a developer's machine skips, CI fails.
func TestUnit_Testing_OfflineSkipsUnlessRequired(t *testing.T) {
	t.Setenv(EnvRequired, "")

	offline := fmt.Errorf("%w: fixture: dialling nobody", ErrOffline)

	tb := &fakeTB{}
	document(tb, "fixture", cachedFixture(offline))
	if tb.fataled != "" {
		t.Errorf("offline without %s failed instead of skipping: %s", EnvRequired, tb.fataled)
	}
	for _, want := range []string{"fixture", EnvCacheDir} {
		if !strings.Contains(tb.skipped, want) {
			t.Errorf("the skip does not mention %q: %s", want, tb.skipped)
		}
	}

	t.Setenv(EnvRequired, "1")

	tb = &fakeTB{}
	document(tb, "fixture", cachedFixture(offline))
	if tb.skipped != "" || tb.fataled == "" {
		t.Errorf("offline with %s set should fail, not skip: %+v", EnvRequired, tb)
	}
}

// TestUnit_Testing_AMismatchNeverSkips: a fetched document that is not the
// pinned one is a change in meaning and must fail even without EnvRequired.
func TestUnit_Testing_AMismatchNeverSkips(t *testing.T) {
	t.Setenv(EnvRequired, "")

	tb := &fakeTB{}
	document(tb, "fixture", cachedFixture(fmt.Errorf("the pinned fixture document is not what upstream served")))

	if tb.skipped != "" {
		t.Errorf("a non-offline failure was skipped: %s", tb.skipped)
	}
	if !strings.Contains(tb.fataled, "not what upstream served") {
		t.Errorf("the failure lost its cause: %s", tb.fataled)
	}
}

// TestUnit_Testing_TheExportedHelpersFailOnAnUnpinnedID exercises the real
// wiring end to end without the network: an id the lock does not pin is a
// caller bug, not an offline condition.
func TestUnit_Testing_TheExportedHelpersFailOnAnUnpinnedID(t *testing.T) {
	t.Setenv(EnvRequired, "")

	tb := &fakeTB{}
	Document(tb, "no-such-document")
	if tb.skipped != "" || !strings.Contains(tb.fataled, "pins no document") {
		t.Errorf("Document: %+v", tb)
	}

	tb = &fakeTB{}
	if got := DocumentRoot(tb, "no-such-document"); got != Root("no-such-document") {
		t.Errorf("DocumentRoot = %q, want %q", got, Root("no-such-document"))
	}
	if tb.fataled == "" {
		t.Error("DocumentRoot of an unpinned id did not fail")
	}

	tb = &fakeTB{}
	SpecPath(tb, "no-such-document")
	if tb.fataled == "" {
		t.Error("SpecPath of an unpinned id did not fail")
	}
}

// TestUnit_Testing_MustPinReadsTheLockNotTheNetwork: assertions about a
// document's version or scale come from the lock, so they move with it.
func TestUnit_Testing_MustPinReadsTheLockNotTheNetwork(t *testing.T) {
	t.Parallel()

	tb := &fakeTB{}
	pin := MustPin(tb, "thousandeyes")
	if tb.fataled != "" {
		t.Fatalf("MustPin of a pinned id failed: %s", tb.fataled)
	}
	if pin.Version == "" || len(pin.SHA256) != 64 {
		t.Errorf("MustPin returned an incomplete pin: %+v", pin)
	}

	if got := pin.Describe(); !strings.Contains(got, pin.Version) ||
		!strings.Contains(got, shortSHA(pin.SHA256)) {
		t.Errorf("Describe() = %q does not describe the pin", got)
	}

	tb = &fakeTB{}
	MustPin(tb, "no-such-document")
	if !strings.Contains(tb.fataled, "pins no document") {
		t.Errorf("MustPin of an unpinned id: %+v", tb)
	}
}
