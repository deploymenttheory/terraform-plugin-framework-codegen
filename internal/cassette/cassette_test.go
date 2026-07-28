package cassette

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain installs DenyTransport as the default transport.
//
// Any test in this package that accidentally dials out fails loudly rather than reaching a
// real API. The package's whole purpose is offline replay, so a test that needed the
// network would be testing the wrong thing.
func TestMain(m *testing.M) {
	http.DefaultTransport = DenyTransport{}
	os.Exit(m.Run())
}

func newRedactor(t *testing.T, values map[string]string) *Redactor {
	t.Helper()

	r, err := NewRedactor(values, nil)
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}
	return r
}

// apiServer stands up a small JSON API with the quirks worth exercising here: a body that
// echoes what it was sent, a header the allow list drops, and one that it keeps.
func apiServer(t *testing.T) *httptest.Server {
	t.Helper()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Kept by the allow list.
		w.Header().Set("X-Organization-Rate-Limit-Limit", "240")
		// Dropped: pure churn, and recording it would make every re-record diff.
		w.Header().Set("X-Request-Id", fmt.Sprintf("req-%d", time.Now().UnixNano()))
		w.Header().Set("Date", time.Now().Format(http.TimeFormat))

		switch {
		case r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(body)

		case r.URL.Path == "/tags/absent":
			w.Header().Del("Content-Type")
			w.WriteHeader(http.StatusNotFound)

		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"1","key":"probe","nested":{"b":2,"a":1}}`))
		}
	}))

	t.Cleanup(s.Close)

	return s
}

// TestUnit_Cassette_RecordThenReplay is the Phase 4.2 milestone.
//
// Record against a real server, then replay the recording with the network denied and get
// the same responses. That is the property every fact in the store rests on: if derivation
// is not reproducible from the transcript alone, a committed fact is not checkable.
func TestUnit_Cassette_RecordThenReplay(t *testing.T) {
	t.Parallel()

	srv := apiServer(t)

	// An explicit transport, not http.DefaultTransport: TestMain replaced that with
	// DenyTransport so that a test which dials out by accident fails. Recording is the one
	// operation here that legitimately reaches the network.
	rec, err := NewRecordingTransport(&http.Transport{}, newRedactor(t, nil), nil)
	if err != nil {
		t.Fatalf("NewRecordingTransport: %v", err)
	}

	live := &http.Client{Transport: rec}

	var liveBodies []string
	for _, req := range sequence(t, srv.URL) {
		resp, err := live.Do(req)
		if err != nil {
			t.Fatalf("live request: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		liveBodies = append(liveBodies, fmt.Sprintf("%d:%s", resp.StatusCode, body))
	}

	interactions, err := rec.Interactions()
	if err != nil {
		t.Fatalf("Interactions: %v", err)
	}
	if len(interactions) != 3 {
		t.Fatalf("recorded %d interactions, want 3", len(interactions))
	}

	// Replay with no network at all: the base transport is never consulted, and
	// DenyTransport is the default, so anything that escaped would fail.
	replay := &http.Client{Transport: NewReplayTransport(interactions)}

	var replayBodies []string
	for _, req := range sequence(t, srv.URL) {
		resp, err := replay.Do(req)
		if err != nil {
			t.Fatalf("replayed request: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		replayBodies = append(replayBodies, fmt.Sprintf("%d:%s", resp.StatusCode, body))
	}

	for i := range liveBodies {
		// Compared as canonical JSON where both are JSON, because the recording
		// re-encodes: the point is that the *content* survives, not the byte order the
		// server happened to send.
		if !sameJSONOrText(liveBodies[i], replayBodies[i]) {
			t.Errorf("interaction %d: replay gave %q, live gave %q", i+1, replayBodies[i], liveBodies[i])
		}
	}

	if got := NewReplayTransport(interactions).Remaining(); got != 3 {
		t.Errorf("Remaining on a fresh replayer = %d, want 3", got)
	}
}

func sequence(t *testing.T, base string) []*http.Request {
	t.Helper()

	mustReq := func(method, path, body string) *http.Request {
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, base+path, r) //nolint:noctx // a test fixture
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		return req
	}

	return []*http.Request{
		mustReq(http.MethodGet, "/tags?limit=10&expand=assignments", ""),
		mustReq(http.MethodPost, "/tags", `{"key":"probe","value":"x"}`),
		mustReq(http.MethodGet, "/tags/absent", ""),
	}
}

func sameJSONOrText(a, b string) bool {
	if a == b {
		return true
	}

	statusA, bodyA, _ := strings.Cut(a, ":")
	statusB, bodyB, _ := strings.Cut(b, ":")
	if statusA != statusB {
		return false
	}

	var va, vb any
	if json.Unmarshal([]byte(bodyA), &va) != nil || json.Unmarshal([]byte(bodyB), &vb) != nil {
		return false
	}

	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)

	return bytes.Equal(ja, jb)
}

// TestUnit_Cassette_ReplayIsStrictAndOrdered.
//
// A probe is a sequence, so a refactor that reorders its requests has changed the
// protocol. A lenient matcher would let that drift while the facts stayed green -- and the
// facts would then be evidence for a protocol nobody runs.
func TestUnit_Cassette_ReplayIsStrictAndOrdered(t *testing.T) {
	t.Parallel()

	interactions := []Interaction{
		{ID: "001-get-tags", Seq: 1, Request: Request{Method: "GET", Path: "/tags"}, Response: Response{Status: 200}},
		{ID: "002-get-tags-1", Seq: 2, Request: Request{Method: "GET", Path: "/tags/1"}, Response: Response{Status: 200}},
	}

	tests := []struct {
		name   string
		method string
		path   string
		want   error
	}{
		{"right order", "GET", "/tags", nil},
		{"wrong path", "GET", "/tags/1", ErrMismatch},
		{"wrong method", "POST", "/tags", ErrMismatch},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := &http.Client{Transport: NewReplayTransport(interactions)}

			req, err := http.NewRequest(tc.method, "http://example.test"+tc.path, nil) //nolint:noctx // a test fixture
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}

			resp, err := client.Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}

			if tc.want == nil {
				if err != nil {
					t.Errorf("expected a match, got %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}

	// Running past the end is its own error, distinct from a mismatch: it means the probe
	// issued more requests than the transcript holds.
	rt := NewReplayTransport(interactions[:1])
	client := &http.Client{Transport: rt}

	for i := range 2 {
		req, _ := http.NewRequest(http.MethodGet, "http://example.test/tags", nil) //nolint:noctx // a test fixture
		resp, err := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if i == 1 && !errors.Is(err, ErrExhausted) {
			t.Errorf("second request error = %v, want ErrExhausted", err)
		}
	}
}

// TestUnit_Cassette_ReplayMatchesOnBody: the update-style protocol turns on which fields a
// request body carried, so a replay that ignored bodies could not catch a probe that
// stopped sending one.
func TestUnit_Cassette_ReplayMatchesOnBody(t *testing.T) {
	t.Parallel()

	recorded := []Interaction{{
		ID: "001-post-tags", Seq: 1,
		Request: Request{
			Method: "POST", Path: "/tags",
			Body: map[string]any{"key": "probe", "value": "x"},
		},
		Response: Response{Status: 201},
	}}

	send := func(body string) error {
		client := &http.Client{Transport: NewReplayTransport(recorded)}
		req, err := http.NewRequest(http.MethodPost, "http://example.test/tags", strings.NewReader(body)) //nolint:noctx // a test fixture
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		return err
	}

	// Key order differs, content does not: must match, because the recording stores
	// parsed JSON and Go's encoder sorts keys.
	if err := send(`{"value":"x","key":"probe"}`); err != nil {
		t.Errorf("reordered keys should still match: %v", err)
	}

	if err := send(`{"key":"probe"}`); !errors.Is(err, ErrMismatch) {
		t.Errorf("a missing field must mismatch, got %v", err)
	}
	if err := send(`{"key":"probe","value":"y"}`); !errors.Is(err, ErrMismatch) {
		t.Errorf("a changed value must mismatch, got %v", err)
	}
}

// TestUnit_Cassette_RedactedQueryIsSkippedOnMatch.
//
// Redaction destroys the information a comparison would need. Without this, substituting
// an account id out of a query string would make the cassette permanently unreplayable --
// which would force a choice between redacting and replaying, and the whole design needs
// both.
func TestUnit_Cassette_RedactedQueryIsSkippedOnMatch(t *testing.T) {
	t.Parallel()

	recorded := []Interaction{{
		ID: "001-get-tags", Seq: 1,
		Request: Request{
			Method: "GET", Path: "/tags",
			Query: map[string][]string{"aid": {"<REDACTED:accountGroup>"}, "limit": {"10"}},
		},
		Response: Response{Status: 200},
	}}

	client := &http.Client{Transport: NewReplayTransport(recorded)}

	// A different account id, which is the realistic case: the cassette was recorded on
	// one tenant and replays on another.
	req, _ := http.NewRequest(http.MethodGet, "http://example.test/tags?aid=99999&limit=10", nil) //nolint:noctx // a test fixture

	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Errorf("a redacted query value must not block a replay: %v", err)
	}

	// A non-redacted value still has to match, or the matcher would be useless.
	client = &http.Client{Transport: NewReplayTransport(recorded)}
	req, _ = http.NewRequest(http.MethodGet, "http://example.test/tags?aid=1&limit=25", nil) //nolint:noctx // a test fixture

	resp, err = client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrMismatch) {
		t.Errorf("a changed non-redacted query must mismatch, got %v", err)
	}
}

// TestUnit_Cassette_DenyTransportRefusesEverything.
func TestUnit_Cassette_DenyTransportRefusesEverything(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: DenyTransport{}}

	req, _ := http.NewRequest(http.MethodGet, "http://example.test/tags", nil) //nolint:noctx // a test fixture

	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrNetworkDenied) {
		t.Errorf("error = %v, want ErrNetworkDenied", err)
	}

	// And the default transport is denied too, which is what TestMain set up: a test that
	// dials out by accident must fail rather than reach a real API.
	if _, err := http.Get("http://example.test/tags"); !errors.Is(err, ErrNetworkDenied) { //nolint:noctx,bodyclose // asserting the refusal
		t.Errorf("http.Get error = %v, want ErrNetworkDenied", err)
	}
}

// TestUnit_Cassette_CredentialHeadersAreNeverCaptured.
//
// Never captured, not captured-and-scrubbed. A token cannot leak from a field that was
// never filled, and that is a meaningfully stronger claim than "the redactor removes it".
func TestUnit_Cassette_CredentialHeadersAreNeverCaptured(t *testing.T) {
	t.Parallel()

	srv := apiServer(t)

	rec, err := NewRecordingTransport(&http.Transport{}, newRedactor(t, nil), nil)
	if err != nil {
		t.Fatalf("NewRecordingTransport: %v", err)
	}

	client := &http.Client{Transport: rec}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/tags", nil) //nolint:noctx // a test fixture
	req.Header.Set("Authorization", "Bearer 01ab-secret-token-value-here")
	req.Header.Set("Cookie", "session=abc123")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	interactions, err := rec.Interactions()
	if err != nil {
		t.Fatalf("Interactions: %v", err)
	}

	serialized, err := json.Marshal(interactions)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	for _, forbidden := range []string{"Bearer", "secret-token", "session=abc123", "authorization", "cookie"} {
		if strings.Contains(strings.ToLower(string(serialized)), strings.ToLower(forbidden)) {
			t.Errorf("the recording contains %q:\n%s", forbidden, serialized)
		}
	}

	// The allowed header did survive, or this test would pass on a recorder that captured
	// nothing at all.
	if got := interactions[0].Request.Header["content-type"]; got != "application/json" {
		t.Errorf("the allowed request header was dropped: %v", interactions[0].Request.Header)
	}
}

// TestUnit_Cassette_ChurningHeadersAreDropped: a cassette is committed, so a header that
// changes on every request would make every re-record diff for no information.
func TestUnit_Cassette_ChurningHeadersAreDropped(t *testing.T) {
	t.Parallel()

	srv := apiServer(t)

	rec, err := NewRecordingTransport(&http.Transport{}, newRedactor(t, nil), nil)
	if err != nil {
		t.Fatalf("NewRecordingTransport: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/tags", nil) //nolint:noctx // a test fixture

	resp, err := (&http.Client{Transport: rec}).Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	interactions, err := rec.Interactions()
	if err != nil {
		t.Fatalf("Interactions: %v", err)
	}

	header := interactions[0].Response.Header

	if _, ok := header["x-request-id"]; ok {
		t.Error("x-request-id churns and must be dropped")
	}
	if _, ok := header["date"]; ok {
		t.Error("date churns and must be dropped")
	}
	// The rate-limit budget is real information a probe reads.
	if got := header["x-organization-rate-limit-limit"]; got != "240" {
		t.Errorf("the rate-limit header should be kept, got %q", got)
	}
	// The drop is visible, so a reader who wonders why a header is missing can see that
	// headers were dropped rather than concluding the server sent none.
	if interactions[0].Response.DroppedHeaders == 0 {
		t.Error("DroppedHeaders should record that headers were filtered")
	}
}
