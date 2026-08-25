package vendor_openapi_specs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestUnit_Fetch_APinWithNoSourceCannotBeFetched: a lock entry with neither
// URL is a lock bug, and the error should say so rather than reporting a
// network problem that does not exist.
func TestUnit_Fetch_APinWithNoSourceCannotBeFetched(t *testing.T) {
	t.Parallel()

	_, _, err := fetch(Pin{})
	if err == nil || !strings.Contains(err.Error(), "neither a mirror nor an upstream") {
		t.Fatalf("a pin with no URLs: %v", err)
	}
}

// TestUnit_Fetch_ADeadMirrorFallsBackToUpstream: the mirror is an
// availability measure, so losing it must cost a retry, not the document.
func TestUnit_Fetch_ADeadMirrorFallsBackToUpstream(t *testing.T) {
	t.Parallel()

	upstream, hits := serve(t, aDocument)

	pin := pinFor(t, upstream, aDocument)
	pin.MirrorURL = "http://127.0.0.1:1/never-listening"

	doc, source, err := fetch(pin)
	if err != nil {
		t.Fatalf("fetch with a dead mirror: %v", err)
	}
	if string(doc) != aDocument {
		t.Error("the fallback served different bytes")
	}
	if source != upstream {
		t.Errorf("source = %q, want the upstream %q", source, upstream)
	}
	if hits.Load() != 1 {
		t.Errorf("upstream asked %d time(s), want 1", hits.Load())
	}
}

// TestUnit_Fetch_EverySourceFailingNamesEachFailure: the error a developer
// sees offline should list what was tried, because "could not fetch" alone
// sends them hunting through the lock.
func TestUnit_Fetch_EverySourceFailingNamesEachFailure(t *testing.T) {
	t.Parallel()

	pin := Pin{
		MirrorURL:   "http://127.0.0.1:1/mirror",
		UpstreamURL: "http://127.0.0.1:1/upstream",
	}

	_, _, err := fetch(pin)
	if err == nil {
		t.Fatal("two dead sources produced no error")
	}
	for _, want := range []string{"every source failed", "/mirror", "/upstream"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not mention %q: %v", want, err)
		}
	}
}

// TestUnit_Fetch_ANonOKStatusIsRefused: a 404 page or a rate-limit body must
// never be handed to the checksum gate as if it were the document.
func TestUnit_Fetch_ANonOKStatusIsRefused(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, _, err := fetch(Pin{UpstreamURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a 404 was not refused: %v", err)
	}
}

// TestUnit_Fetch_AnEmptyResponseIsRefused: an empty 200 is what a misbehaving
// proxy serves, and zero bytes can never be a pinned document.
func TestUnit_Fetch_AnEmptyResponseIsRefused(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)

	_, _, err := fetch(Pin{UpstreamURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("an empty response was not refused: %v", err)
	}
}

// TestUnit_Fetch_AMalformedURLFailsBeforeTheNetwork: a URL the request
// builder refuses is a lock typo, reported as such.
func TestUnit_Fetch_AMalformedURLFailsBeforeTheNetwork(t *testing.T) {
	t.Parallel()

	_, _, err := fetch(Pin{UpstreamURL: "http://not a url"})
	if err == nil || !strings.Contains(err.Error(), "building the request") {
		t.Fatalf("a malformed URL was not refused: %v", err)
	}
}
