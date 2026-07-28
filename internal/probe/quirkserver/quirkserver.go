// Package quirkserver is an API that misbehaves on purpose.
//
// It exists so that every claim the prober makes is checked against a server whose
// behaviour is known by construction. That is a different and much stronger thing than
// exercising a probe against a real API: a live tenant tells you what *that* API does,
// while a quirk server tells you whether the probe would notice if it did something else.
//
// Each switch on Quirks encodes one behaviour observed in a real API. Most were drawn from the
// hardcoded special cases that accumulate in hand-written providers -- fixup tables, runtime
// re-normalisers, lists of fields to treat specially. Those are a catalogue of quirks
// discovered the hard way, one production bug at a time, and turning them into switches is
// what lets a probe be *validated* rather than merely run.
//
// Every quirk is asserted to be exhibited by TestUnit_Quirkserver_EachQuirkIsExhibited. A
// switch that silently stopped working would make the probe tests that depend on it pass
// for the wrong reason -- which is the one failure mode a ground-truth fixture must not
// have.
package quirkserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// EnvelopeKind is one of the error body shapes a real API uses.
//
// Four, because an API can genuinely use all four across one endpoint family -- see
// internal/probe/apierr. A prober that assumed one shape could not tell "rejected because the
// field is immutable" from "rejected because the token expired", and that distinction is what
// makes the immutability protocol possible at all.
type EnvelopeKind string

const (
	// EnvelopeProblem is RFC 7807 application/problem+json, used for validation failures.
	EnvelopeProblem EnvelopeKind = "problem"
	// EnvelopeOAuth is {"error","error_description"}, returned for an invalid token.
	EnvelopeOAuth EnvelopeKind = "oauth"
	// EnvelopeLegacy is {"errorMessage"}, returned when no credentials are supplied.
	EnvelopeLegacy EnvelopeKind = "legacy"
	// EnvelopeEmpty is no body at all, which is what a 404 returns.
	EnvelopeEmpty EnvelopeKind = "empty"
)

// Conditional describes a requirement that depends on another field's value.
//
// The archetypal case is a port field that matters only when a protocol field says tcp. It is
// the quirk that proves one-field-at-a-time omission reports half a truth, and that a probe
// must emit a note rather than a fact when two fixtures disagree.
type Conditional struct {
	// When is the field/value pair that triggers the requirement.
	WhenField string
	WhenValue any
	// Then is the field that becomes required.
	Then string
}

// Quirks turns each misbehaviour on.
//
// The zero value is a well-behaved API, so a test enables exactly the quirk it is about.
type Quirks struct {
	// SilentlyDiscards accepts these fields on write and never stores them.
	//
	// Ground truth for Writable=false, and the trap that makes a naive ReturnedOnRead
	// probe wrong: the field was demonstrably sent and is demonstrably absent.
	SilentlyDiscards []string

	// ImmutableAfterCreate refuses to change these fields on update, with a
	// problem+json body that names the field.
	ImmutableAfterCreate []string

	// RequiresExtraFieldOnUpdate makes update reject any request omitting this field,
	// even though create does not need it.
	//
	// The quirk that proves the immutability protocol's control request is load-bearing
	// rather than ceremony: without the control, a probe sees a 4xx on update and
	// concludes immutability when the request shape was simply wrong.
	RequiresExtraFieldOnUpdate string

	// ConstantDefaults are values assigned when a field is omitted. Ground truth for a
	// real serverDefault.
	ConstantDefaults map[string]any

	// DerivedDefaults assigns a value computed from another field.
	//
	// A prober without the derivation check confidently writes this into the blueprint as
	// a static default, which is then a permanent lie. The map is target field to source
	// field.
	DerivedDefaults map[string]string

	// CounterDefault assigns an incrementing value to this field when omitted, so two
	// byte-identical creates differ. Exercises the two-identical-creates check.
	CounterDefault string

	// RequiredButUndeclared rejects a create omitting these, naming them in the error.
	// The key/object_type case.
	RequiredButUndeclared []string

	// ConditionallyRequired makes one field required only when another has a given value.
	ConditionallyRequired *Conditional

	// WriteSideEffects sets one field as a consequence of another being written.
	//
	// Enabling one measurement silently enabling another: the class of quirk a human would
	// never guess from a specification and a prober genuinely can find. Maps the trigger
	// field to the field it also sets.
	WriteSideEffects map[string]string

	// NormalisesCase lower-cases these fields' values.
	NormalisesCase []string
	// TrimsWhitespace strips surrounding whitespace from these fields.
	TrimsWhitespace []string
	// SortsLists sorts these list-valued fields.
	//
	// Hand-written providers carry runtime helpers that re-sort collections purely to
	// suppress the drift this causes, which is the wrong layer to fix it at.
	SortsLists []string

	// ExpansionGated returns these fields only when the matching expansion query
	// parameter is present.
	//
	// A probe that reads back once concludes the field is never returned, and the generated
	// state mapper then blanks a real value on every refresh. Any API with an expand,
	// include or fields parameter can do this. Maps field name to the value that reveals it.
	ExpansionGated map[string]string

	// PutClearsOmitted makes update replace rather than merge.
	PutClearsOmitted bool

	// EventuallyConsistentReads makes the first N reads of a new object 404.
	EventuallyConsistentReads int

	// ErrorEnvelope selects which shape errors take. Defaults to problem+json.
	ErrorEnvelope EnvelopeKind

	// ClosedEnum rejects values outside the listed set, per field.
	ClosedEnum map[string][]string
	// RejectsDocumentedValue refuses a value the specification documents, per field.
	//
	// The valuable enum result: the specification is stale, and a spec-derived validator
	// would have been actively harmful.
	RejectsDocumentedValue map[string]string

	// DeleteFails makes every delete fail, which must stop the run creating more and
	// leave orphans reported.
	DeleteFails bool
	// DeleteFlakyEvery makes every Nth delete fail.
	DeleteFlakyEvery int

	// RateLimitHeaders emits a rate-limit header trio and 429s once the budget is spent,
	// which lets pacing be tested with no live tenant.
	RateLimitHeaders bool
	// RateLimit is the budget when RateLimitHeaders is set.
	RateLimit int

	// VolatileFields change on every read. The modifiedDate perpetual-diff class.
	VolatileFields []string

	// IgnoresUnknownQueryParams returns 200 for an unrecognised query parameter rather
	// than 400.
	//
	// Calibrates every probe that depends on unknown *body* fields being ignored rather
	// than rejected.
	IgnoresUnknownQueryParams bool

	// TypedQueryParams reject a bad value, which is how the error-envelope probe provokes
	// an error without mutating anything.
	TypedQueryParams []string

	// BasePath serves the collection under a prefix, e.g. "/v7".
	//
	// Real endpoints carry one, and it is the difference between a probe's relative path and the
	// full path a cassette records. Two bugs hid behind its absence -- evidence citations and
	// replay matching both silently assumed the two were the same string -- so the fixture is
	// able to model it deliberately.
	BasePath string

	// NotFoundStatus overrides the status for an absent object. An API that returns 403
	// for another tenant's identifier is indistinguishable from one that returns it for
	// an absent one, and that is itself worth recording.
	NotFoundStatus int
}

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
	// requests counts everything, so a test can assert a probe's real cost.
	requests int
	// spent tracks the rate-limit budget.
	spent int
}

// collectionPath and itemPrefix are the paths this fixture serves.
//
// Arbitrary: a probe is driven by a blueprint's path templates, so the fixture only has to be
// self-consistent. "/things" rather than any real resource name, to keep the fixture from
// reading as a claim about a particular API.
const (
	collectionPath = "/things"
	itemPrefix     = "/things/"
	// envelopeKey is the key a list response wraps its items under, which probes discover
	// rather than assume.
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

// BaseURL is the root a session should be pointed at, including any configured prefix.
func (s *Server) BaseURL() string { return s.URL + s.quirks.BasePath }

// CollectionURL and ItemURL are the addresses a probe is pointed at.
func (s *Server) CollectionURL() string    { return s.BaseURL() + collectionPath }
func (s *Server) ItemURL(id string) string { return s.BaseURL() + itemPrefix + id }

// Requests returns how many requests were served, so a test can assert a probe's real cost
// against the worst case its Cost method declared.
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

	// The prefix is stripped once, here, so every handler below matches on the same paths
	// whether or not one is configured.
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

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	if !s.quirks.IgnoresUnknownQueryParams {
		if bad := s.badQueryParam(r); bad != "" {
			s.fail(w, http.StatusBadRequest, "unknown query parameter", bad)
			return
		}
	}
	if bad := s.badTypedParam(r); bad != "" {
		s.fail(w, http.StatusBadRequest, "invalid value for query parameter", bad)
		return
	}

	s.mu.Lock()
	ids := make([]string, 0, len(s.objects))
	for id := range s.objects {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	sort.Strings(ids)

	items := make([]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, s.project(id, r))
	}

	writeJSON(w, http.StatusOK, map[string]any{envelopeKey: items})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	body, err := readJSON(r)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "malformed body", "")
		return
	}

	if missing := s.missingRequired(body); missing != "" {
		// Naming the field is what lets a probe emit Observed rather than Inferred: an
		// error that does not name it could have been about anything.
		s.fail(w, http.StatusBadRequest, "missing required field", missing)
		return
	}

	if field, value := s.rejectedEnumValue(body); field != "" {
		s.fail(w, http.StatusBadRequest, "invalid value for "+field, fmt.Sprint(value))
		return
	}

	s.mu.Lock()

	id := strconv.Itoa(s.nextID)
	s.nextID++

	obj := map[string]any{"id": id}
	for k, v := range body {
		if contains(s.quirks.SilentlyDiscards, k) {
			// Accepted and thrown away. The response says 201 and the object never has it.
			continue
		}
		obj[k] = s.normalise(k, v)
	}

	s.applyDefaults(obj, body)
	s.applySideEffects(obj, body)

	s.objects[id] = obj
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, s.project(id, r))
}

func (s *Server) read(w http.ResponseWriter, r *http.Request, path string) {
	id := strings.TrimPrefix(path, itemPrefix)

	s.mu.Lock()
	_, exists := s.objects[id]
	s.reads[id]++
	seen := s.reads[id]
	s.mu.Unlock()

	// Eventual consistency: the object exists but the first N reads deny it.
	if exists && seen <= s.quirks.EventuallyConsistentReads {
		s.notFound(w)
		return
	}

	if !exists {
		s.notFound(w)
		return
	}

	writeJSON(w, http.StatusOK, s.project(id, r))
}

func (s *Server) update(w http.ResponseWriter, r *http.Request, path string) {
	id := strings.TrimPrefix(path, itemPrefix)

	body, err := readJSON(r)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "malformed body", "")
		return
	}

	s.mu.Lock()
	current, exists := s.objects[id]
	s.mu.Unlock()

	if !exists {
		s.notFound(w)
		return
	}

	// Checked before immutability, deliberately: this is the quirk that makes a naive
	// probe read a 4xx as immutability when the request shape was simply wrong.
	if f := s.quirks.RequiresExtraFieldOnUpdate; f != "" {
		if _, ok := body[f]; !ok {
			s.fail(w, http.StatusBadRequest, "missing required field", f)
			return
		}
	}

	for _, field := range s.quirks.ImmutableAfterCreate {
		sent, ok := body[field]
		if !ok {
			continue
		}
		if !equalJSON(sent, current[field]) {
			s.fail(w, http.StatusBadRequest, "field cannot be modified after creation", field)
			return
		}
	}

	s.mu.Lock()

	updated := map[string]any{"id": id}
	if !s.quirks.PutClearsOmitted {
		// Merge: everything not mentioned survives.
		for k, v := range current {
			updated[k] = v
		}
	}
	for k, v := range body {
		if contains(s.quirks.SilentlyDiscards, k) {
			continue
		}
		updated[k] = s.normalise(k, v)
	}

	s.applySideEffects(updated, body)

	s.objects[id] = updated
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, s.project(id, r))
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request, path string) {
	id := strings.TrimPrefix(path, itemPrefix)

	s.mu.Lock()
	s.deletes++
	attempt := s.deletes
	s.mu.Unlock()

	if s.quirks.DeleteFails {
		s.fail(w, http.StatusInternalServerError, "delete failed", id)
		return
	}
	if n := s.quirks.DeleteFlakyEvery; n > 0 && attempt%n == 0 {
		s.fail(w, http.StatusInternalServerError, "delete failed", id)
		return
	}

	s.mu.Lock()
	_, exists := s.objects[id]
	delete(s.objects, id)
	s.mu.Unlock()

	if !exists {
		s.notFound(w)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// project renders an object as the API would return it, applying the quirks that affect
// what a read sees.
func (s *Server) project(id string, r *http.Request) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	obj := s.objects[id]
	out := make(map[string]any, len(obj))

	for k, v := range obj {
		// Expansion-gated fields are withheld unless asked for.
		if want, gated := s.quirks.ExpansionGated[k]; gated {
			if !expansionRequested(r, want) {
				continue
			}
		}
		out[k] = v
	}

	// Volatile fields differ on every read, which is what makes a perpetual diff.
	for _, field := range s.quirks.VolatileFields {
		s.counter++
		out[field] = fmt.Sprintf("v%d", s.counter)
	}

	return out
}

func expansionRequested(r *http.Request, want string) bool {
	if r == nil {
		return false
	}
	for _, v := range r.URL.Query()["expand"] {
		if v == want {
			return true
		}
	}
	return false
}

func (s *Server) applyDefaults(obj, sent map[string]any) {
	for field, value := range s.quirks.ConstantDefaults {
		if _, ok := sent[field]; !ok {
			obj[field] = value
		}
	}

	// Derived from another field, so two creates with different sources differ -- which is
	// what distinguishes a derived default from a constant one.
	for field, source := range s.quirks.DerivedDefaults {
		if _, ok := sent[field]; ok {
			continue
		}
		obj[field] = fmt.Sprintf("derived-from-%v", sent[source])
	}

	// A counter, so two byte-identical creates differ.
	if field := s.quirks.CounterDefault; field != "" {
		if _, ok := sent[field]; !ok {
			s.counter++
			obj[field] = fmt.Sprintf("counter-%d", s.counter)
		}
	}
}

func (s *Server) applySideEffects(obj, sent map[string]any) {
	for trigger, also := range s.quirks.WriteSideEffects {
		if v, ok := sent[trigger]; ok && v == true {
			obj[also] = true
		}
	}
}

// normalise applies the transforms that cause perpetual diffs.
func (s *Server) normalise(field string, v any) any {
	if contains(s.quirks.NormalisesCase, field) {
		if str, ok := v.(string); ok {
			v = strings.ToLower(str)
		}
	}
	if contains(s.quirks.TrimsWhitespace, field) {
		if str, ok := v.(string); ok {
			v = strings.TrimSpace(str)
		}
	}
	if contains(s.quirks.SortsLists, field) {
		if list, ok := v.([]any); ok {
			sorted := make([]any, len(list))
			copy(sorted, list)
			sort.Slice(sorted, func(i, j int) bool {
				return fmt.Sprint(sorted[i]) < fmt.Sprint(sorted[j])
			})
			v = sorted
		}
	}
	return v
}

func (s *Server) missingRequired(body map[string]any) string {
	for _, field := range s.quirks.RequiredButUndeclared {
		if _, ok := body[field]; !ok {
			return field
		}
	}

	if c := s.quirks.ConditionallyRequired; c != nil {
		if v, ok := body[c.WhenField]; ok && equalJSON(v, c.WhenValue) {
			if _, present := body[c.Then]; !present {
				return c.Then
			}
		}
	}

	return ""
}

func (s *Server) rejectedEnumValue(body map[string]any) (string, any) {
	for field, allowed := range s.quirks.ClosedEnum {
		v, ok := body[field]
		if !ok {
			continue
		}
		str := fmt.Sprint(v)
		if !contains(allowed, str) {
			return field, v
		}
	}

	// A documented value the API refuses: the specification is stale.
	for field, refused := range s.quirks.RejectsDocumentedValue {
		if v, ok := body[field]; ok && fmt.Sprint(v) == refused {
			return field, v
		}
	}

	return "", nil
}

// knownQueryParams are the parameters the server understands.
var knownQueryParams = map[string]bool{
	"expand": true, "limit": true, "cursor": true, "aid": true,
}

func (s *Server) badQueryParam(r *http.Request) string {
	keys := make([]string, 0, len(r.URL.Query()))
	for k := range r.URL.Query() {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if !knownQueryParams[k] {
			return k
		}
	}

	return ""
}

// badTypedParam rejects a bad value for a parameter that has a type, which is how the
// error-envelope probe provokes an error without mutating anything.
func (s *Server) badTypedParam(r *http.Request) string {
	for _, name := range s.quirks.TypedQueryParams {
		v := r.URL.Query().Get(name)
		if v == "" {
			continue
		}
		if _, err := strconv.Atoi(v); err != nil {
			return name
		}
	}
	return ""
}

func (s *Server) notFound(w http.ResponseWriter) {
	status := s.quirks.NotFoundStatus
	if status == 0 {
		status = http.StatusNotFound
	}
	s.fail(w, status, "not found", "")
}

// fail writes an error in whichever envelope the quirks select.
func (s *Server) fail(w http.ResponseWriter, status int, title, detail string) {
	switch s.quirks.ErrorEnvelope {
	case EnvelopeEmpty:
		// No body at all, which is what a real 404 returns and what the error classifier
		// has to have a fallback for.
		w.WriteHeader(status)

	case EnvelopeOAuth:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_token",
			"error_description": joinDetail(title, detail),
		})

	case EnvelopeLegacy:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errorMessage": joinDetail(title, detail),
		})

	case EnvelopeProblem, "":
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		body := map[string]any{
			"type":     "about:blank",
			"title":    title,
			"status":   status,
			"instance": collectionPath,
		}
		if detail != "" {
			// The detail names the offending field, which is what lets a probe emit
			// Observed rather than Inferred.
			body["detail"] = detail
		}
		_ = json.NewEncoder(w).Encode(body)

	default:
		w.WriteHeader(status)
	}
}

func joinDetail(title, detail string) string {
	if detail == "" {
		return title
	}
	return title + ": " + detail
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request) (map[string]any, error) {
	var out map[string]any

	if r.Body == nil {
		return map[string]any{}, nil
	}

	if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding the request body: %w", err)
	}

	if out == nil {
		out = map[string]any{}
	}

	return out, nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// equalJSON compares two decoded JSON values.
//
// Via their encodings, because a value that came from json.Unmarshal and one written as a Go
// literal differ in type -- 1 is an int in a literal and a float64 after decoding -- and
// comparing with == would report a difference that is not there.
func equalJSON(a, b any) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ja) == string(jb)
}
