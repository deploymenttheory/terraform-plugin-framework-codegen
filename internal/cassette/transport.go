package cassette

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// DenyTransport refuses every request.
//
// Installed as the base transport in replay and verify modes, and as
// http.DefaultTransport in this package's and internal/probe's tests. It is what turns
// "facts are re-derived offline" from an intention into an enforced property: any code
// path that bypasses the cassette fails loudly instead of quietly reaching a real API.
//
// Two lines of implementation for a guarantee that would otherwise rest on nobody making
// a mistake.
type DenyTransport struct{}

func (DenyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("%w: %s %s", ErrNetworkDenied, req.Method, req.URL)
}

// RecordingTransport wraps a real transport and records what passes through.
//
// Recording happens here rather than in the session so that it cannot be skipped: a
// request that reaches the network has, by construction, gone through this.
type RecordingTransport struct {
	// Base is the transport that actually makes the request.
	Base http.RoundTripper

	// Redactor substitutes declared secrets. Required: a nil redactor would record
	// traffic unscrubbed, so NewRecordingTransport refuses one.
	Redactor *Redactor

	// Secrets are values that must never appear in a recording whatever their shape --
	// the token, and every environment variable the profile names as secret. Checked by
	// the fail-closed scan.
	Secrets map[string]string

	mu           sync.Mutex
	seq          int
	interactions []Interaction
	// last is the id of the interaction most recently recorded, so a caller can cite the
	// exact request a fact came from instead of guessing by path.
	last string
	// findings accumulates redaction failures. A recording with any finding writes
	// nothing at all, so these are collected rather than returned per request: failing
	// the first request would leave the operator debugging one symptom at a time.
	findings []Finding
}

// NewRecordingTransport builds a recorder.
func NewRecordingTransport(
	base http.RoundTripper,
	r *Redactor,
	secrets map[string]string,
) (*RecordingTransport, error) {
	if base == nil {
		return nil, fmt.Errorf(
			"%w: a recording transport needs a base transport",
			ErrInvalidCassette,
		)
	}
	if r == nil {
		return nil, fmt.Errorf("%w: a recording transport needs a redactor; "+
			"recording unscrubbed traffic is not an option", ErrInvalidCassette)
	}

	return &RecordingTransport{Base: base, Redactor: r, Secrets: secrets}, nil
}

func (t *RecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	reqBody, err := drainRequest(req)
	if err != nil {
		return nil, err
	}

	resp, err := t.Base.RoundTrip(req)
	if err != nil {
		// A transport error is not an interaction: there is no response to record, and
		// recording a half-interaction would make the cassette unreplayable.
		return nil, err //nolint:wrapcheck // the base transport's error is the useful one
	}

	respBody, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading the response body: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("closing the response body: %w", closeErr)
	}

	// Handed back to the caller, since the body has been consumed.
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	t.record(req, reqBody, resp, respBody)

	return resp, nil
}

func (t *RecordingTransport) record(
	req *http.Request,
	reqBody []byte,
	resp *http.Response,
	respBody []byte,
) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.seq++

	reqHeader, _ := canonicalHeaders(req.Header, recordedRequestHeaders)
	respHeader, dropped := canonicalHeaders(resp.Header, recordedResponseHeaders)

	reqParsed, reqB64, reqCT := decodeBody(reqBody, req.Header.Get("Content-Type"))
	respParsed, respB64, respCT := decodeBody(respBody, resp.Header.Get("Content-Type"))

	id := interactionID(t.seq, req.Method, req.URL.Path)
	t.last = id

	i := Interaction{
		ID:  id,
		Seq: t.seq,
		Request: Request{
			Method:      req.Method,
			Path:        req.URL.Path,
			Query:       t.Redactor.ApplyToQuery(canonicalQuery(req.URL)),
			Header:      t.Redactor.ApplyToHeaders(reqHeader),
			Body:        t.Redactor.Apply(reqParsed),
			BodyBase64:  reqB64,
			ContentType: reqCT,
		},
		Response: Response{
			Status:         resp.StatusCode,
			Header:         t.Redactor.ApplyToHeaders(respHeader),
			Body:           t.Redactor.Apply(respParsed),
			BodyBase64:     respB64,
			ContentType:    respCT,
			DroppedHeaders: dropped,
		},
	}

	// Scanned as it is recorded rather than only before writing, so that a run which
	// crashes before the write still cannot have leaked into a partial artefact -- and
	// so the finding names the interaction it came from.
	findings, err := ScanInteraction(i, t.Secrets)
	if err != nil {
		t.findings = append(t.findings, Finding{
			Interaction: i.ID,
			Shape:       "unserializable",
			Pointer:     err.Error(),
		})
	}
	t.findings = append(t.findings, findings...)

	t.interactions = append(t.interactions, i)
}

// Interactions returns what was recorded, or a redaction error.
//
// The error takes precedence over the data, deliberately: a caller that ignored it and
// wrote the interactions anyway would defeat the whole fail-closed design, so there is no
// way to get the interactions without also getting the error.
// LastInteraction reports the id of the interaction most recently recorded.
//
// This is what makes a fact's evidence exact rather than approximate. Without it a citation can
// only be matched by method and path suffix, which is fine for a read tier issuing a handful of
// distinguishable requests and useless for a write tier issuing forty POSTs to the same
// collection: every fact would cite all forty, and a fact whose evidence is "all of it" is
// unfalsifiable.
func (t *RecordingTransport) LastInteraction() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.last
}

func (t *RecordingTransport) Interactions() ([]Interaction, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.findings) > 0 {
		return nil, FindingsError(t.findings)
	}

	out := make([]Interaction, len(t.interactions))
	copy(out, t.interactions)

	return out, nil
}

// ReplayTransport serves recorded interactions in order.
//
// Strict and ordered on purpose. A probe is a sequence -- create, read, update, read --
// and a refactor that reorders the requests has changed the protocol, which is precisely
// what a committed transcript should catch. A matcher that searched the cassette for
// something plausible would let the protocol drift while the facts stayed green, and the
// facts would then be evidence for a protocol nobody runs.
type ReplayTransport struct {
	mu           sync.Mutex
	interactions []Interaction
	next         int
	// last is the id of the interaction most recently served. On replay this is what makes a
	// re-derived fact cite the same interaction the recorded one did.
	last string
}

// NewReplayTransport builds a replayer over an ordered set of interactions.
func NewReplayTransport(interactions []Interaction) *ReplayTransport {
	return &ReplayTransport{interactions: interactions}
}

func (t *ReplayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := drainRequest(req)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.next >= len(t.interactions) {
		return nil, fmt.Errorf("%w: %d interaction(s) recorded, but %s %s is request %d",
			ErrExhausted, len(t.interactions), req.Method, req.URL.Path, t.next+1)
	}

	i := t.interactions[t.next]

	parsed, _, _ := decodeBody(body, req.Header.Get("Content-Type"))

	if err := i.Matches(req.Method, req.URL.Path, canonicalQuery(req.URL), parsed); err != nil {
		return nil, err
	}

	t.next++
	t.last = i.ID

	return i.httpResponse(req)
}

// LastInteraction reports the id of the interaction most recently served.
//
// See RecordingTransport.LastInteraction for why this exists.
func (t *ReplayTransport) LastInteraction() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.last
}

// Remaining reports how many interactions were not replayed.
//
// A replay that used fewer interactions than were recorded is as much a mismatch as one
// that used the wrong ones: it means a request the transcript expected was never made,
// so some fact is now derived from less traffic than it was recorded from.
func (t *ReplayTransport) Remaining() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.interactions) - t.next
}

// httpResponse rebuilds an http.Response from a recorded one.
func (i Interaction) httpResponse(req *http.Request) (*http.Response, error) {
	body, err := i.Response.RawBody()
	if err != nil {
		return nil, err
	}

	header := http.Header{}
	for k, v := range i.Response.Header {
		header.Set(k, v)
	}

	return &http.Response{
		Status:        fmt.Sprintf("%d", i.Response.Status),
		StatusCode:    i.Response.Status,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}

// drainRequest reads a request body and puts it back.
//
// Both transports need the body: one to record it, one to match on it. Reading it without
// restoring it would break the request for the base transport, which is a bug that only
// shows up on requests that have a body -- so every mutating request and nothing else.
func drainRequest(req *http.Request) ([]byte, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return nil, nil
	}

	body, err := io.ReadAll(req.Body)
	closeErr := req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("reading the request body: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("closing the request body: %w", closeErr)
	}

	req.Body = io.NopCloser(bytes.NewReader(body))

	return body, nil
}
