// Package quirkserver is an API that misbehaves on purpose.
//
// It exists so that every claim the auditor makes is checked against a server
// whose behaviour is known by construction. That is a different and much
// stronger thing than exercising the auditor against a real API: a live tenant
// tells you what *that* API does, while a quirk server tells you whether the
// audit would notice if it did something else. It is the only offline target
// the audit engine is tested against, and it stands in for the live API when
// the whole pipeline is exercised without credentials.
//
// Each switch on Quirks encodes one behaviour observed in a real API. Most
// were drawn from the hardcoded special cases that accumulate in hand-written
// providers -- fixup tables, runtime re-normalisers, lists of fields to treat
// specially. Those are a catalogue of quirks discovered the hard way, one
// production bug at a time, and turning them into switches is what lets an
// audit be *validated* rather than merely run.
//
// Every quirk is asserted to be exhibited by
// TestUnit_Quirkserver_EachQuirkIsExhibited. A switch that silently stopped
// working would make the audit tests that depend on it pass for the wrong
// reason -- which is the one failure mode a ground-truth fixture must not
// have.
package quirkserver

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

// Server is a running quirk server.
type Server struct {
	*httptest.Server

	quirks Quirks

	mu      sync.Mutex
	objects map[string]map[string]any
	nextID  int
	counter int
	// reads counts reads per object, for the eventual-consistency quirk.
	reads map[string]int
	// deletes counts delete attempts, for the flaky-delete quirk.
	deletes int
	// requests counts everything, so a test can assert an audit's real cost.
	requests int
	// spent tracks the rate-limit budget.
	spent int
}

// collectionPath and itemPrefix are the paths this fixture serves.
//
// Arbitrary: the auditor is driven by path templates it is handed, so the
// fixture only has to be self-consistent. "/things" rather than any real
// resource name, to keep the fixture from reading as a claim about a
// particular API.
const (
	collectionPath = "/things"
	itemPrefix     = "/things/"
	// envelopeKey is the key a list response wraps its items under, which the
	// auditor discovers rather than assumes.
	envelopeKey = "things"
)

// New starts a quirk server. It is closed when the test finishes.
func New(t interface {
	Cleanup(func())
	Helper()
}, q Quirks,
) *Server {
	t.Helper()

	s := &Server{
		quirks:  q,
		objects: map[string]map[string]any{},
		nextID:  1,
		reads:   map[string]int{},
	}

	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)

	return s
}

// BaseURL is the root a session should be pointed at, including any configured
// prefix.
func (s *Server) BaseURL() string { return s.URL + s.quirks.BasePath }

// CollectionURL and ItemURL are the addresses an audit is pointed at.
func (s *Server) CollectionURL() string    { return s.BaseURL() + collectionPath }
func (s *Server) ItemURL(id string) string { return s.BaseURL() + itemPrefix + id }

// Requests returns how many requests were served, so a test can assert an
// audit's real cost against the worst case it declared.
func (s *Server) Requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// Objects returns a snapshot of what exists, for asserting cleanup.
func (s *Server) Objects() map[string]map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]map[string]any, len(s.objects))
	for id, obj := range s.objects {
		copied := make(map[string]any, len(obj))
		for k, v := range obj {
			copied[k] = v
		}
		out[id] = copied
	}

	return out
}

// Seed inserts an object directly, for tests that need something to read.
func (s *Server) Seed(fields map[string]any) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := strconv.Itoa(s.nextID)
	s.nextID++

	obj := map[string]any{"id": id}
	for k, v := range fields {
		obj[k] = v
	}
	s.objects[id] = obj

	return id
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()

	if s.quirks.RateLimitHeaders {
		if done := s.applyRateLimit(w); done {
			return
		}
	}

	// The prefix is stripped once, here, so every handler below matches on the
	// same paths whether or not one is configured.
	path := strings.TrimPrefix(r.URL.Path, s.quirks.BasePath)

	switch {
	case path == collectionPath && r.Method == http.MethodGet:
		s.list(w, r)
	case path == collectionPath && r.Method == http.MethodPost:
		s.create(w, r)
	case strings.HasPrefix(path, itemPrefix) && r.Method == http.MethodGet:
		s.read(w, r, path)
	case strings.HasPrefix(path, itemPrefix) &&
		(r.Method == http.MethodPut || r.Method == http.MethodPatch):
		s.update(w, r, path)
	case strings.HasPrefix(path, itemPrefix) && r.Method == http.MethodDelete:
		s.delete(w, r, path)
	default:
		s.fail(w, http.StatusMethodNotAllowed, "unsupported", "")
	}
}

func (s *Server) applyRateLimit(w http.ResponseWriter) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	limit := s.quirks.RateLimit
	if limit <= 0 {
		limit = 240
	}

	s.spent++

	w.Header().Set("x-organization-rate-limit-limit", strconv.Itoa(limit))
	w.Header().Set("x-organization-rate-limit-remaining", strconv.Itoa(max(0, limit-s.spent)))
	w.Header().Set("x-organization-rate-limit-reset", "60")

	if s.spent > limit {
		w.Header().Set("retry-after", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	}

	return false
}
