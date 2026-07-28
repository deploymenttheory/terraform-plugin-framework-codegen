// Package cassette records and replays HTTP traffic.
//
// It exists so that probe facts can be re-derived offline. A fact is only worth as much
// as the traffic behind it, so the traffic is committed, and CI re-derives every fact
// from the committed transcript with the network denied. If derivation is not a pure
// function of the transcript, CI fails -- which is a much stronger guarantee than "the
// probe looked right when somebody ran it".
//
// Three properties shape everything here.
//
// # Determinism
//
// A cassette is committed, so re-recording has to produce a reviewable diff rather than
// a wall of churn. Bodies are stored as parsed JSON and re-encoded canonically, so a
// server that reorders its keys produces no diff at all. Headers are lower-cased,
// sorted and filtered through an allow list. Nothing carries a timestamp, a duration or
// a request id.
//
// Volatile *response* values are recorded verbatim, because catching them is the point
// of the volatility probe. An id changing between recordings is real information, not
// noise.
//
// # Redaction fails closed
//
// Cassettes are as public as the repository. Three layers: credential headers are never
// captured at all rather than captured and scrubbed; declared literals are substituted;
// and then the serialised bytes are scanned for anything secret-shaped *before any file
// is written*. A hit writes nothing -- not a partial directory, not a single interaction
// -- and fails the run. So a bug in the substitution layer cannot commit a token; it can
// only stop the build.
//
// # Replay is strict and ordered
//
// Interaction N must match request N. A probe is a sequence, so a refactor that changes
// the order has changed the protocol, and that is exactly what a committed transcript
// should catch. A lenient matcher would let the protocol drift while the facts stayed
// green.
package cassette

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

var (
	// ErrMismatch marks a replayed request that does not match the cassette.
	ErrMismatch = errors.New("request does not match the cassette")

	// ErrExhausted marks a replay that ran out of interactions.
	ErrExhausted = errors.New("cassette has no more interactions")

	// ErrNetworkDenied is returned by DenyTransport. Seeing it means code that was
	// supposed to be replaying reached for the network instead.
	ErrNetworkDenied = errors.New("network access is denied")

	// ErrSecretFound marks traffic that would have committed a secret. Nothing is
	// written when this is returned.
	ErrSecretFound = errors.New("recorded traffic contains a secret")

	// ErrInvalidCassette marks a cassette that cannot be read.
	ErrInvalidCassette = errors.New("invalid cassette")
)

// Interaction is one request and its response.
//
// The JSON tags are camelCase, matching the house convention that tagliatelle enforces.
type Interaction struct {
	// ID is a stable, deterministic identifier: "<seq>-<method>-<path-slug>". Facts
	// cite it as evidence, so it must not change when an unrelated interaction is
	// re-recorded.
	ID string `json:"id"`
	// Seq is the position in the sequence, from 1.
	Seq int `json:"seq"`

	Request  Request  `json:"request"`
	Response Response `json:"response"`
}

// Request is the part of a request worth recording.
type Request struct {
	Method string `json:"method"`
	// Path excludes the host: a cassette recorded against one tenant must replay
	// against another, and the host is in the profile rather than the transcript.
	Path string `json:"path"`
	// Query is canonicalised: keys sorted, values sorted within a key.
	Query map[string][]string `json:"query,omitempty"`
	// Header holds only what the allow list permits.
	Header map[string]string `json:"header,omitempty"`

	// Body is parsed JSON where the request had a JSON body.
	Body any `json:"body,omitempty"`
	// BodyBase64 holds a non-JSON body verbatim.
	BodyBase64 string `json:"bodyBase64,omitempty"`
	// ContentType is recorded when the body is not JSON, so a replay can tell what it
	// was looking at.
	ContentType string `json:"contentType,omitempty"`
}

// Response is the part of a response worth recording.
type Response struct {
	Status      int               `json:"status"`
	Header      map[string]string `json:"header,omitempty"`
	Body        any               `json:"body,omitempty"`
	BodyBase64  string            `json:"bodyBase64,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	// DroppedHeaders counts headers the allow list removed.
	//
	// Recorded so the drop is visible. A reader who wonders why a rate-limit header is
	// missing should be able to see that headers *were* dropped rather than concluding
	// the server did not send any.
	DroppedHeaders int `json:"droppedHeaders,omitempty"`
}

// recordedRequestHeaders is the allow list for request headers.
//
// An allow list rather than a deny list, and that is the whole point: a deny list leaks
// the header nobody thought of. Authorization is not on it, and is not merely absent
// from it -- the recording transport never populates it at all, so a token cannot leak
// from a field that was never filled.
var recordedRequestHeaders = map[string]bool{
	"content-type": true,
	"accept":       true,
}

// recordedResponseHeaders is the allow list for response headers.
//
// Every entry earns its place by being something a probe reads or a fact cites.
// Deliberately absent: date and request-id headers (churn), the remaining/reset half of a
// rate-limit trio (churn -- the limit is stable and useful, the countdown is neither),
// set-cookie (a session credential), and trace headers.
var recordedResponseHeaders = map[string]bool{
	"content-type":     true,
	"location":         true,
	"deprecation":      true,
	"sunset":           true,
	"retry-after":      true,
	"www-authenticate": true,
	// A rate-limit *ceiling*, which is stable and lets a prober pace itself. Vendors spell
	// this differently; add the header your API uses.
	"x-organization-rate-limit-limit": true,
}

// canonicalQuery normalises a query string for storage and matching.
func canonicalQuery(u *url.URL) map[string][]string {
	if u.RawQuery == "" {
		return nil
	}

	values := u.Query()
	if len(values) == 0 {
		return nil
	}

	out := make(map[string][]string, len(values))
	for k, v := range values {
		vs := make([]string, len(v))
		copy(vs, v)
		sort.Strings(vs)
		out[k] = vs
	}

	return out
}

// canonicalHeaders filters a header set through an allow list, lower-casing and
// returning how many were dropped.
func canonicalHeaders(h http.Header, allowed map[string]bool) (map[string]string, int) {
	if len(h) == 0 {
		return nil, 0
	}

	out := map[string]string{}
	dropped := 0

	for k, v := range h {
		lower := strings.ToLower(k)

		// Checked before the allow list, and separately from it. The allow list happens
		// to exclude these today, but an allow list is a thing people add entries to --
		// and "we need to see which auth scheme the API wants" is a plausible reason to
		// add authorization to it. This check means that would still not leak the value.
		if neverCaptured[lower] {
			dropped++
			continue
		}

		if !allowed[lower] || len(v) == 0 {
			dropped++
			continue
		}

		out[lower] = v[0]
	}

	if len(out) == 0 {
		return nil, dropped
	}

	return out, dropped
}

// decodeBody parses a body as JSON where it can, and falls back to base64.
//
// Parsing rather than storing raw is what makes the diff reviewable: Go's encoder sorts
// map keys, so a server that reorders its own output produces no diff at all. The
// fallback exists because not every response is JSON, and a cassette that silently
// dropped a non-JSON body would make a replay mismatch impossible to diagnose.
func decodeBody(raw []byte, contentType string) (parsed any, b64, ct string) {
	if len(raw) == 0 {
		return nil, "", ""
	}

	if isJSON(contentType) || looksLikeJSON(raw) {
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			return v, "", ""
		}
		// Content-type claimed JSON and the body is not. Worth keeping verbatim: it is
		// usually an error page from a proxy, and that is a real observation about the
		// API's edge.
	}

	return nil, base64.StdEncoding.EncodeToString(raw), contentType
}

// jsonContentTypes matches the JSON media types an API actually uses, including the
// vendor and HAL forms.
func isJSON(contentType string) bool {
	ct := strings.ToLower(contentType)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(ct)

	switch {
	case ct == "application/json", ct == "application/hal+json", ct == "application/problem+json":
		return true
	case strings.HasPrefix(ct, "application/") && strings.HasSuffix(ct, "+json"):
		return true
	default:
		return false
	}
}

// looksLikeJSON is the fallback for a response with no content type, which happens.
func looksLikeJSON(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case '{', '[':
		return true
	default:
		return false
	}
}

// RawBody returns a response body as bytes, whichever form it was stored in.
//
// Probes need this for the observations that turn on the bytes rather than the
// structure -- notably whether a 404 has an empty body at all, which is one of the four
// error shapes the pilot API uses.
func (r Response) RawBody() ([]byte, error) {
	switch {
	case r.BodyBase64 != "":
		out, err := base64.StdEncoding.DecodeString(r.BodyBase64)
		if err != nil {
			return nil, fmt.Errorf("%w: decoding a base64 body: %w", ErrInvalidCassette, err)
		}
		return out, nil

	case r.Body != nil:
		out, err := json.Marshal(r.Body)
		if err != nil {
			return nil, fmt.Errorf("%w: re-encoding a body: %w", ErrInvalidCassette, err)
		}
		return out, nil

	default:
		return nil, nil
	}
}

// interactionID builds the stable identifier for one interaction.
//
// Deterministic given the sequence number, method and path, so re-recording a session
// that has not changed shape produces the same ids -- which is what lets a fact cite one
// as evidence and still be checkable a year later.
func interactionID(seq int, method, path string) string {
	return fmt.Sprintf("%03d-%s-%s", seq, strings.ToLower(method), slug(path))
}

// slug turns a path into a filename-safe fragment.
func slug(path string) string {
	var b strings.Builder

	lastDash := true

	for _, r := range strings.ToLower(strings.Trim(path, "/")) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			// Collapse runs of separators, so "/v7/tags/{id}" does not become a string
			// of dashes.
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}

	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "root"
	}

	// Bounded, because a path with an embedded token or a long filter can be
	// arbitrarily long and these become filenames.
	const maxSlug = 48
	if len(out) > maxSlug {
		out = strings.Trim(out[:maxSlug], "-")
	}

	return out
}

// Matches reports whether a live request corresponds to this recorded one.
//
// Method, path, canonical query and canonical body. Not headers: the allow list keeps so
// few of them that matching on headers would mostly assert that the transport still sets
// its own content type.
//
// A query key or header whose recorded value is a redaction sentinel is skipped, because
// redaction destroyed the information needed to compare it. Without this, redacting an
// account id out of a query string would make the cassette unreplayable.
func (i Interaction) Matches(method, path string, query map[string][]string, body any) error {
	if !strings.EqualFold(i.Request.Method, method) {
		return fmt.Errorf("%w: interaction %s expects %s, got %s",
			ErrMismatch, i.ID, i.Request.Method, method)
	}

	if i.Request.Path != path {
		return fmt.Errorf("%w: interaction %s expects path %q, got %q",
			ErrMismatch, i.ID, i.Request.Path, path)
	}

	if err := queriesMatch(i.ID, i.Request.Query, query); err != nil {
		return err
	}

	if !bodiesEqual(i.Request.Body, body) {
		return fmt.Errorf("%w: interaction %s body differs\n  recorded: %s\n  actual:   %s",
			ErrMismatch, i.ID, mustJSON(i.Request.Body), mustJSON(body))
	}

	return nil
}

func queriesMatch(id string, recorded, actual map[string][]string) error {
	for k, want := range recorded {
		if isRedacted(want) {
			continue
		}

		got, ok := actual[k]
		if !ok {
			return fmt.Errorf("%w: interaction %s expects query %q", ErrMismatch, id, k)
		}
		if !stringsEqual(want, got) {
			return fmt.Errorf(
				"%w: interaction %s query %q = %v, want %v",
				ErrMismatch,
				id,
				k,
				got,
				want,
			)
		}
	}

	for k := range actual {
		if _, ok := recorded[k]; !ok {
			return fmt.Errorf("%w: interaction %s did not record query %q", ErrMismatch, id, k)
		}
	}

	return nil
}

func isRedacted(values []string) bool {
	for _, v := range values {
		if strings.HasPrefix(v, redactionPrefix) {
			return true
		}
	}
	return false
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// bodiesEqual compares two parsed bodies by their canonical encoding.
//
// Comparing encodings rather than walking the values is both simpler and stricter in the
// right way: Go's encoder sorts map keys, so key order does not matter, but a numeric
// type change does.
func bodiesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	return bytes.Equal(mustJSON(a), mustJSON(b))
}

func mustJSON(v any) []byte {
	if v == nil {
		return []byte("null")
	}
	out, err := json.Marshal(v)
	if err != nil {
		// Unreachable for values that came out of json.Unmarshal, which is the only way
		// a body enters a cassette.
		return []byte(fmt.Sprintf("%q", fmt.Sprint(v)))
	}
	return out
}
