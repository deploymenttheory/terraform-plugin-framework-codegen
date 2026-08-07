package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe/apierr"
)

// This is the only file in internal/probe that constructs an HTTP client, and
// TestUnit_Probe_OnlyTheSessionBuildsAnHTTPClient asserts it stays that way. Every request
// goes through here because this is where the budget is enforced, where the ledger will be
// written, and where the read-only guarantee is real rather than aspirational. A probe with
// its own client would bypass all three while passing every behavioural test in the package.

// httpSession is the concrete ReadSession.
type httpSession struct {
	client *http.Client
	// base is the API root, e.g. "https://api.example.com".
	base string
	// authorization is the finished Authorization header value, e.g.
	// "Bearer x" or "Basic y". Held here and nowhere else: it is not in the
	// profile, not in a flag, and not in anything written to disk.
	authorization string

	collection string
	item       string

	budget *budgetCounter
	// pace holds the rate-limit budget the server most recently stated, so pacing comes
	// from what the API says rather than from a guess.
	pace *pacer

	// inFlight enforces maxConcurrentLive. Not a mutex: a mutex would serialise a
	// concurrent probe silently, and silently is the problem. Two requests in flight at
	// once land in a cassette in whichever order the server answered, so the recording
	// would replay only by luck -- and the failure would surface months later as a
	// mismatch in CI with nothing to point at.
	inFlight atomic.Int32
}

// maxConcurrentLive is one, and it is a correctness requirement rather than politeness.
//
// A cassette is a strictly ordered transcript. Concurrency makes the order a property of the
// server's latency, which makes a recording unreplayable, which removes the offline gate the
// whole evidence pipeline rests on.
const maxConcurrentLive = 1

// SessionConfig is what building a session needs.
type SessionConfig struct {
	// Transport is the round tripper. Recording wraps a real one; replay and verify use a
	// cassette-backed one whose base is DenyTransport, so they cannot reach the network
	// even if something above them tries.
	Transport http.RoundTripper
	// BaseURL is the API root.
	BaseURL string
	// Authorization is the finished Authorization header value, or empty in
	// replay where no credential is needed.
	Authorization string

	// CollectionTemplate and ItemTemplate come from the subject.
	CollectionTemplate string
	ItemTemplate       string

	Budget Budget
	// Timeout bounds a single request. Separate from the run's wall clock: one hung
	// request must not consume the whole budget's worth of time.
	Timeout time.Duration
}

// defaultRequestTimeout bounds a single request.
const defaultRequestTimeout = 30 * time.Second

// NewReadSession builds a read-only session.
func NewReadSession(cfg SessionConfig) (ReadSession, error) {
	s, err := newHTTPSession(cfg)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func newHTTPSession(cfg SessionConfig) (*httpSession, error) {
	if cfg.Transport == nil {
		return nil, fmt.Errorf("%w: a session needs a transport", ErrInvalidPlan)
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: a session needs a base URL", ErrInvalidPlan)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}

	return &httpSession{
		client:        &http.Client{Transport: cfg.Transport, Timeout: timeout},
		base:          strings.TrimSuffix(cfg.BaseURL, "/"),
		authorization: cfg.Authorization,
		collection:    cfg.CollectionTemplate,
		item:          cfg.ItemTemplate,
		budget:        &budgetCounter{limits: cfg.Budget.WithDefaults()},
		pace:          &pacer{},
	}, nil
}

func (s *httpSession) CollectionPath() string    { return s.collection }
func (s *httpSession) ItemPath(id string) string { return resolvePath(s.item, id) }

// Get issues a GET and returns the response in the reduced form a probe may see.
func (s *httpSession) Get(ctx context.Context, path string, query url.Values) (*Response, error) {
	return s.do(ctx, http.MethodGet, path, query, nil)
}

// write issues a request with a JSON body through the same choke point Get uses.
//
// Unexported and taking a method, so the budget, the pacer, the concurrency guard and the
// recorder all apply to a write exactly as they do to a read. A mutating session that built its
// own request would bypass every one of them.
func (s *httpSession) write(
	ctx context.Context,
	method, path string,
	body map[string]any,
) (*Response, error) {
	var reader io.Reader

	if body != nil {
		// Encoded here rather than by the caller because Go's map iteration order is
		// randomised, and json.Marshal sorts keys. A body assembled by string concatenation
		// would produce a different byte sequence on every run, and a cassette matches on
		// bytes.
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding the body for %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}

	return s.do(ctx, method, path, nil, reader)
}

// do is the choke point.
func (s *httpSession) do(
	ctx context.Context,
	method, path string,
	query url.Values,
	body io.Reader,
) (*Response, error) {
	// Checked before the request, not after: a budget enforced afterwards has already
	// spent what it was meant to cap.
	if err := s.budget.spend(method); err != nil {
		return nil, err
	}

	if s.inFlight.Add(1) > maxConcurrentLive {
		s.inFlight.Add(-1)

		return nil, fmt.Errorf(
			"%w: a second request (%s %s) was issued while one was still in flight, and a "+
				"cassette is a strictly ordered transcript -- probes must not be concurrent",
			ErrInvalidPlan, method, path)
	}
	defer s.inFlight.Add(-1)

	// Paced from what the server last said its budget was, rather than from a guess. A
	// probe that ignored this would spend a tenant's whole rate-limit allowance and then
	// attribute the resulting 429s to whichever field it was testing.
	s.pace.wait(ctx)

	target := s.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("building a %s request for %s: %w", method, path, err)
	}

	// One finished header, built once by whoever knew the method. A new auth
	// method is a new way to compute this string and nothing else in the
	// transport: the request path, the budget, the redaction and the cassette
	// are all indifferent to how the caller proved who it was.
	if s.authorization != "" {
		req.Header.Set("Authorization", s.authorization)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}

	raw, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("reading the response to %s %s: %w", method, path, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("closing the response to %s %s: %w", method, path, closeErr)
	}

	s.pace.observe(resp.Header)

	out := newResponse(resp, raw)

	// Asked of the transport rather than tracked here, because only the transport knows what it
	// wrote: the recorder assigns the sequence number, and the replayer decides which recorded
	// interaction answered.
	if namer, ok := s.client.Transport.(interactionNamer); ok {
		out.Interaction = namer.LastInteraction()
	}

	return out, nil
}

// interactionNamer is implemented by both cassette transports.
//
// One method, satisfied structurally, so internal/probe needs no knowledge of which transport it
// was given -- and a live unrecorded transport simply does not implement it, leaving the citation
// empty rather than wrong.
type interactionNamer interface {
	LastInteraction() string
}

// newResponse reduces an http.Response to what a probe is allowed to see.
//
// Deliberately not the http.Response itself: a probe must not be able to read the body
// twice, reach the connection, or depend on a header the cassette does not record. A probe
// that read an unrecorded header would derive facts that could not be re-derived offline,
// and CI would catch that as a replay failure a long way from the cause.
func newResponse(resp *http.Response, raw []byte) *Response {
	out := &Response{Status: resp.StatusCode, Raw: raw}

	// Only the headers a cassette keeps. Kept in step with internal/cassette's allow list
	// by TestUnit_Probe_VisibleHeadersAreRecordable.
	out.Header = map[string]string{}
	for _, name := range visibleHeaders {
		if v := resp.Header.Get(name); v != "" {
			out.Header[name] = v
		}
	}

	out.Body = parseJSON(raw)

	return out
}

// visibleHeaders are the response headers a probe may read.
//
// Every one is also on internal/cassette's recording allow list, and it has to stay that way:
// a probe reading a header the cassette drops would work live and fail on replay. Adding one
// here means adding it there too.
var visibleHeaders = []string{
	"content-type",
	"location",
	"deprecation",
	"sunset",
	"retry-after",
	"www-authenticate",
	"x-organization-rate-limit-limit",
}

// Error classifies a non-success response.
func (r *Response) Error() apierr.Error {
	return apierr.Classify(r.Status, r.Raw)
}

// FieldOutcome is the outcome of reading a dotted path.
type FieldOutcome int

const (
	// OutcomeAbsent means no such path. Distinct from present-and-null, which is the whole basis of
	// the writability and default protocols: a field that came back null is a very different
	// observation from one that did not come back at all.
	OutcomeAbsent FieldOutcome = iota
	// OutcomePresent means the path resolved, possibly to null.
	OutcomePresent
	// OutcomeAmbiguous means the path crossed an array holding more than one element, so which
	// element the probe meant cannot be known.
	//
	// Its own outcome rather than folded into Absent, because the two demand opposite
	// responses. Absent is an observation a probe may build a fact on; OutcomeAmbiguous is a note,
	// and treating it as absence would emit ReturnedOnRead=false at Observed for a field that
	// was demonstrably returned.
	OutcomeAmbiguous
)

func (o FieldOutcome) String() string {
	switch o {
	case OutcomePresent:
		return "present"
	case OutcomeAmbiguous:
		return "ambiguous"
	default:
		return "absent"
	}
}

// Field reads a dotted JSON path out of a response body.
//
// Dotted because a probe addresses fields the way the API does, and a field inside a nested
// object needs a path.
//
// The bool is Present and nothing else: Ambiguous reads as false here, which is the safe
// direction for a caller that has not thought about arrays. A probe that has -- and every probe
// that reports a fact about a nested path must -- uses LookupField instead.
func (r *Response) Field(jsonPath string) (any, bool) {
	v, outcome := r.LookupField(jsonPath)

	return v, outcome == OutcomePresent
}

// LookupField reads a dotted path and distinguishes all three outcomes.
func (r *Response) LookupField(jsonPath string) (any, FieldOutcome) {
	if r == nil || r.Body == nil {
		return nil, OutcomeAbsent
	}

	return lookupIn(r.Body, jsonPath)
}

func fieldIn(body any, jsonPath string) (any, bool) {
	v, outcome := lookupIn(body, jsonPath)

	return v, outcome == OutcomePresent
}

// lookupIn walks a dotted path, descending into a single-element array.
//
// The array case is not a convenience. A great many real APIs model a nested object as an array
// of one -- a tag's filters, a rule's conditions -- and without this every fact about such a path
// came out "the field was not returned" at Observed confidence, because the field was
// demonstrably *sent*. Merge would write it, and the generated state mapper would then blank a
// real value on every refresh.
//
// Exactly one element, and more than one is Ambiguous rather than "the first". Taking the first
// would produce a fact about element zero and label it as a fact about the field, which is the
// kind of quietly wrong conclusion this package is arranged to avoid.
func lookupIn(body any, jsonPath string) (any, FieldOutcome) {
	current := body

	for _, segment := range strings.Split(jsonPath, ".") {
		if arr, ok := current.([]any); ok {
			switch len(arr) {
			case 1:
				current = arr[0]
			default:
				return nil, OutcomeAmbiguous
			}
		}

		obj, ok := current.(map[string]any)
		if !ok {
			return nil, OutcomeAbsent
		}

		v, present := obj[segment]
		if !present {
			return nil, OutcomeAbsent
		}

		current = v
	}

	return current, OutcomePresent
}

// Items pulls a collection out of a list response.
//
// A list endpoint usually wraps its items under a key named after the resource, and which key
// that is cannot be known in advance. So the first array-valued key wins, and a bare array is
// accepted too. Guessing here is acceptable because the consequence of guessing wrong is a
// probe that finds no fixture, not a wrong fact.
func (r *Response) Items() []any {
	if r == nil || r.Body == nil {
		return nil
	}

	if direct, ok := r.Body.([]any); ok {
		return direct
	}

	obj, ok := r.Body.(map[string]any)
	if !ok {
		return nil
	}

	// Sorted, so which key wins does not depend on map iteration order.
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if arr, ok := obj[k].([]any); ok {
			return arr
		}
	}

	return nil
}

// EnvelopeKey reports which key held the collection, for the list-shape fact.
func (r *Response) EnvelopeKey() string {
	if r == nil || r.Body == nil {
		return ""
	}

	obj, ok := r.Body.(map[string]any)
	if !ok {
		return ""
	}

	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if _, ok := obj[k].([]any); ok {
			return k
		}
	}

	return ""
}

// budgetCounter enforces the caps.
//
// In the session rather than the runner, so there is one place to enforce them and no way
// for a probe to spend without passing through it.
type budgetCounter struct {
	mu sync.Mutex

	limits Budget

	requests int
	creates  int
	deletes  int

	deleteFailures int

	// sweeping switches spending onto the reserve. A sweep must be able to spend after the
	// run's own cap is exhausted -- see enterSweep.
	sweeping      bool
	sweepRequests int

	// halted latches the first refusal. Without it a runner that ignored one ErrBudget would
	// get a *different* error message on every subsequent request, and the report would name
	// whichever probe happened to ask last rather than the one that hit the cap.
	halted error
}

// sweepReserveRequests is how many requests the sweeper may spend beyond the run's cap.
//
// Four per created object -- a delete, a retry, a second retry, and a confirming read -- plus
// eight for the collection reads the prefix pass needs. Derived from MaxCreates rather than
// fixed, because the work the sweeper has to do is a function of how much the run was allowed
// to create.
func sweepReserveRequests(limits Budget) int {
	return 4*limits.MaxCreates + 8
}

// enterSweep moves the counter onto its reserve.
//
// This exists because of a contradiction in the design as first written. `budgetCounter.spend`
// refuses everything once MaxRequests is reached, so "exceed the budget, then sweep, then exit
// 4" was unimplementable: the sweeper's own DELETEs would have been refused, and the cap meant
// to bound the blast radius would have manufactured exactly the orphans it exists to prevent.
//
// A reserve rather than a raised cap, so a sweep still cannot run away, and so the report can
// show what the run spent separately from what cleaning up after it cost.
func (b *budgetCounter) enterSweep() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sweeping = true
	// The latch is cleared for the sweep and only for the sweep. The run is over; what
	// remains is the obligation to leave nothing behind.
	b.halted = nil
}

func (b *budgetCounter) spend(method string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.halted != nil {
		return b.halted
	}

	if b.sweeping {
		return b.spendFromReserve(method)
	}

	// Counted only once the request is allowed. Incrementing first and then refusing would
	// report one more request than the run actually issued, and the report is the document a
	// reader uses to reason about what a run did to somebody's tenant.
	if b.requests+1 > b.limits.MaxRequests {
		b.halted = fmt.Errorf("%w: the cap of %d requests is reached",
			ErrBudget, b.limits.MaxRequests)
		return b.halted
	}

	if method == http.MethodPost && b.creates+1 > b.limits.MaxCreates {
		b.halted = fmt.Errorf("%w: the cap of %d creates is reached",
			ErrBudget, b.limits.MaxCreates)
		return b.halted
	}

	b.requests++

	switch method {
	case http.MethodPost:
		b.creates++
	case http.MethodDelete:
		b.deletes++
	}

	return nil
}

// spendFromReserve accounts a sweep request. Caller holds the lock.
//
// A create during a sweep is a bug in the sweeper rather than a budget matter, and it is
// refused as such: the sweeper's whole contract is that it only ever removes.
func (b *budgetCounter) spendFromReserve(method string) error {
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		return fmt.Errorf("%w: a sweep issued a %s, which it must never do", ErrLedger, method)
	}

	if reserve := sweepReserveRequests(b.limits); b.sweepRequests+1 > reserve {
		b.halted = fmt.Errorf(
			"%w: the sweep spent its reserve of %d requests; anything still outstanding is "+
				"reported as an orphan rather than retried indefinitely",
			ErrBudget, reserve)
		return b.halted
	}

	b.sweepRequests++

	if method == http.MethodDelete {
		b.deletes++
	}

	return nil
}

// recordDeleteFailure counts a delete that did not succeed.
//
// MaxDeleteFailures defaults to zero, so the first failure stops the run creating anything
// new. Continuing to create after demonstrating you cannot clean up is the worst available
// behaviour, which is why this is counted in the choke point and not left to a caller.
func (b *budgetCounter) recordDeleteFailure() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.deleteFailures++

	return b.deleteFailures > b.limits.MaxDeleteFailures
}

func (b *budgetCounter) report() BudgetReport {
	b.mu.Lock()
	defer b.mu.Unlock()

	return BudgetReport{
		Requests:       b.requests,
		MaxRequests:    b.limits.MaxRequests,
		Creates:        b.creates,
		MaxCreates:     b.limits.MaxCreates,
		Deletes:        b.deletes,
		DeleteFailures: b.deleteFailures,
		SweepRequests:  b.sweepRequests,
	}
}

// pacer spaces requests according to what the server said its budget was.
type pacer struct {
	// interval is how long to wait before the next request, derived from the stated limit.
	interval time.Duration
	// retryAfter is set when the server explicitly asked us to wait.
	retryAfter time.Duration
}

// observe reads the pacing headers.
func (p *pacer) observe(h http.Header) {
	if v := h.Get("retry-after"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			p.retryAfter = time.Duration(secs) * time.Second
			return
		}
	}

	p.retryAfter = 0

	// Where an API states its per-period budget in a header, safe spacing is derivable
	// rather than guessable. Deliberately conservative: a probe run is not
	// latency-sensitive, and spending somebody's whole allowance to save a few seconds is a
	// poor trade.
	if v := h.Get("x-organization-rate-limit-limit"); v != "" {
		if limit, err := strconv.Atoi(v); err == nil && limit > 0 {
			p.interval = time.Minute / time.Duration(limit)
		}
	}
}

// wait sleeps for the current interval, or until the context is done.
func (p *pacer) wait(ctx context.Context) {
	d := p.interval
	if p.retryAfter > d {
		d = p.retryAfter
	}
	if d <= 0 {
		return
	}

	// In replay there is no server and no pacing headers, so this does nothing -- which is
	// what makes a replayed run fast enough to be a CI gate.
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// parseJSON decodes a body, returning nil for anything that is not JSON.
//
// A probe reads Body for structure and Raw for the handful of observations that turn on the
// bytes -- notably whether a 404 has a body at all.
func parseJSON(raw []byte) any {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}

	return v
}

// UnrecordedTransport is a live transport whose traffic is not recorded.
//
// Two callers, and both need the traffic left out of the cassette rather than merely not caring.
// A sweep derives no facts, so a transcript would support no claim. And the gate's tenant reads
// must stay out of a recording precisely because replay has no gate: a cassette holding requests
// the replayed run will never issue is a cassette that cannot reproduce itself.
//
// Here rather than in the caller because this file is the only place permitted to name net/http's
// transport, which a structural test enforces.
func UnrecordedTransport() http.RoundTripper { return &http.Transport{} }

// ErrAuth is returned when a response indicates a credential problem.
//
// Surfaced as its own error rather than a note, because it invalidates the whole run: every
// subsequent response would be about the token, and a probe that carried on would attribute
// an authentication failure to whichever field it happened to be testing.
var ErrAuth = errors.New("the API rejected the credential")

// TransportFor builds the transport for a run mode, and the recorder where there is one.
//
// It lives in this file rather than in run.go for a reason the structural test enforces: this
// is the only place in internal/probe permitted to name net/http's real transport. Deciding
// how requests reach the network is exactly the decision the choke point exists to own, and
// run.go picking the base transport itself would have made "only the session reaches the
// network" untrue in the one place it matters.
//
// The safety property: replay and verify are given a cassette-backed transport, and nothing
// live is constructed for them at all. A code path that bypassed the cassette would reach
// http.DefaultTransport, which the caller has set to DenyTransport -- so it fails loudly
// rather than quietly reaching a real API.
func TransportFor(
	mode Mode,
	interactions []cassette.Interaction,
	r *cassette.Redactor,
	secrets map[string]string,
) (http.RoundTripper, *cassette.RecordingTransport, error) {
	switch mode {
	case ModeRecord:
		if r == nil {
			return nil, nil, fmt.Errorf("%w: recording needs a redactor", ErrInvalidPlan)
		}
		// The one live transport in the package. Explicitly constructed rather than taking
		// http.DefaultTransport, because tests replace that with DenyTransport and a
		// recording run legitimately needs the real thing.
		rec, err := cassette.NewRecordingTransport(&http.Transport{}, r, secrets)
		if err != nil {
			return nil, nil, fmt.Errorf("building the recorder: %w", err)
		}
		return rec, rec, nil

	case ModeReplay, ModeVerify:
		if len(interactions) == 0 {
			return nil, nil, fmt.Errorf("%w: %s needs a recorded cassette", ErrInvalidPlan, mode)
		}
		return cassette.NewReplayTransport(interactions), nil, nil

	case ModeSweep:
		// A sweep reaches a real API and records nothing. Nothing to record: it derives no
		// facts, so there is no offline claim a transcript would have to support, and a
		// cassette of deletions would only invite somebody to replay it.
		return UnrecordedTransport(), nil, nil

	default:
		return nil, nil, fmt.Errorf("%w: unknown mode %q", ErrInvalidPlan, mode)
	}
}
