package probe

import (
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
	// token is the bearer credential. Held here and nowhere else: it is not in the
	// profile, not in a flag, and not in anything written to disk.
	token string

	collection string
	item       string

	budget *budgetCounter
	// pace holds the rate-limit budget the server most recently stated, so pacing comes
	// from what the API says rather than from a guess.
	pace *pacer
}

// SessionConfig is what building a session needs.
type SessionConfig struct {
	// Transport is the round tripper. Recording wraps a real one; replay and verify use a
	// cassette-backed one whose base is DenyTransport, so they cannot reach the network
	// even if something above them tries.
	Transport http.RoundTripper
	// BaseURL is the API root.
	BaseURL string
	// Token is the bearer credential, or empty in replay where none is needed.
	Token string

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
		client:     &http.Client{Transport: cfg.Transport, Timeout: timeout},
		base:       strings.TrimSuffix(cfg.BaseURL, "/"),
		token:      cfg.Token,
		collection: cfg.CollectionTemplate,
		item:       cfg.ItemTemplate,
		budget:     &budgetCounter{limits: cfg.Budget.WithDefaults()},
		pace:       &pacer{},
	}, nil
}

func (s *httpSession) CollectionPath() string    { return s.collection }
func (s *httpSession) ItemPath(id string) string { return resolvePath(s.item, id) }

// Get issues a GET and returns the response in the reduced form a probe may see.
func (s *httpSession) Get(ctx context.Context, path string, query url.Values) (*Response, error) {
	return s.do(ctx, http.MethodGet, path, query, nil)
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

	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
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

	return newResponse(resp, raw), nil
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

// Field reads a dotted JSON path out of a response body.
//
// Dotted because a probe addresses fields the way the API does, and a field inside a nested
// object needs a path. The bool distinguishes "absent" from "present and null", which is
// the whole basis of the writability and default protocols: a field that came back null is
// a very different observation from one that did not come back at all.
func (r *Response) Field(jsonPath string) (any, bool) {
	if r == nil || r.Body == nil {
		return nil, false
	}
	return fieldIn(r.Body, jsonPath)
}

func fieldIn(body any, jsonPath string) (any, bool) {
	current := body

	for _, segment := range strings.Split(jsonPath, ".") {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		v, present := obj[segment]
		if !present {
			return nil, false
		}

		current = v
	}

	return current, true
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
	limits Budget

	requests int
	creates  int
	deletes  int

	deleteFailures int
}

func (b *budgetCounter) spend(method string) error {
	b.requests++
	if b.requests > b.limits.MaxRequests {
		return fmt.Errorf("%w: %d requests, cap is %d", ErrBudget, b.requests, b.limits.MaxRequests)
	}

	switch method {
	case http.MethodPost:
		b.creates++
		if b.creates > b.limits.MaxCreates {
			return fmt.Errorf(
				"%w: %d creates, cap is %d",
				ErrBudget,
				b.creates,
				b.limits.MaxCreates,
			)
		}
	case http.MethodDelete:
		b.deletes++
	}

	return nil
}

func (b *budgetCounter) report() BudgetReport {
	return BudgetReport{
		Requests:       b.requests,
		MaxRequests:    b.limits.MaxRequests,
		Creates:        b.creates,
		MaxCreates:     b.limits.MaxCreates,
		Deletes:        b.deletes,
		DeleteFailures: b.deleteFailures,
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
		return nil, nil, fmt.Errorf("%w: sweep is not a probe run", ErrInvalidPlan)

	default:
		return nil, nil, fmt.Errorf("%w: unknown mode %q", ErrInvalidPlan, mode)
	}
}
