package quirkserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// client is a plain HTTP client; these tests are about the server.
func get(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	return do(t, http.MethodGet, url, nil)
}

func post(t *testing.T, url string, body map[string]any) (int, map[string]any) {
	t.Helper()
	return do(t, http.MethodPost, url, body)
}

func put(t *testing.T, url string, body map[string]any) (int, map[string]any) {
	t.Helper()
	return do(t, http.MethodPut, url, body)
}

func do(t *testing.T, method, url string, body map[string]any) (int, map[string]any) {
	t.Helper()

	var r io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		r = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, url, r) //nolint:noctx // a test fixture
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}

	var out map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			// Not JSON, which is itself something a test may be asserting.
			out = map[string]any{"_raw": string(raw)}
		}
	}

	return response.StatusCode, out
}

// TestUnit_Quirkserver_EachQuirkIsExhibited asserts that every switch actually
// misbehaves. This matters more than it looks: a quirk that silently stopped
// working would make every audit test depending on it pass for the wrong
// reason, and a ground-truth fixture that lies is worse than no fixture at
// all.
//
// The exhibit registries are keyed by Quirks field name and the driver walks
// the struct by reflection, so adding a quirk without an exhibit -- or an
// exhibit for a quirk that no longer exists -- fails here rather than rotting
// quietly. Each exhibit asserts the *observable* consequence rather than an
// internal flag.
func TestUnit_Quirkserver_EachQuirkIsExhibited(t *testing.T) {
	t.Parallel()

	exhibits := make(map[string]func(*testing.T), len(writeExhibits)+len(readExhibits))
	for name, fn := range writeExhibits {
		exhibits[name] = fn
	}
	for name, fn := range readExhibits {
		if _, dup := exhibits[name]; dup {
			t.Fatalf("quirk %s has two exhibits; each belongs in exactly one registry", name)
		}
		exhibits[name] = fn
	}

	quirks := reflect.TypeOf(Quirks{})
	for i := range quirks.NumField() {
		name := quirks.Field(i).Name
		fn, ok := exhibits[name]
		if !ok {
			t.Errorf("quirk %s has no exhibit -- a switch nobody checks can silently stop working", name)
			continue
		}
		delete(exhibits, name)
		t.Run(name, fn)
	}

	for name := range exhibits {
		t.Errorf("exhibit %s matches no field of Quirks -- the quirk it asserted is gone", name)
	}
}

// TestUnit_Quirkserver_ZeroValueIsWellBehaved: a test enables exactly the
// quirk it is about, so the default has to be an ordinary API. Otherwise every
// audit test would be exercising misbehaviour it did not ask for.
func TestUnit_Quirkserver_ZeroValueIsWellBehaved(t *testing.T) {
	t.Parallel()

	s := New(t, Quirks{})

	status, created := post(t, s.CollectionURL(), map[string]any{"key": "k", "value": "v"})
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", status)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id was assigned: %v", created)
	}

	status, read := get(t, s.ItemURL(id))
	if status != http.StatusOK {
		t.Errorf("read status = %d, want 200", status)
	}
	// Everything sent comes back unchanged, twice.
	if read["key"] != "k" || read["value"] != "v" {
		t.Errorf("a well-behaved server should echo what it was sent: %v", read)
	}

	_, again := get(t, s.ItemURL(id))
	if read["key"] != again["key"] {
		t.Error("a well-behaved server should be stable across reads")
	}

	status, listed := get(t, s.CollectionURL())
	if status != http.StatusOK {
		t.Errorf("list status = %d, want 200", status)
	}
	if items, _ := listed["things"].([]any); len(items) != 1 {
		t.Errorf("the list should hold one object: %v", listed)
	}

	if status, _ := do(t, http.MethodDelete, s.ItemURL(id), nil); status != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204", status)
	}
	if len(s.Objects()) != 0 {
		t.Errorf("delete should remove the object: %v", s.Objects())
	}

	// And a second delete reports absence rather than succeeding again.
	if status, _ := do(t, http.MethodDelete, s.ItemURL(id), nil); status != http.StatusNotFound {
		t.Errorf("deleting twice should 404, got %d", status)
	}
}

func TestUnit_Quirkserver_CountsRequests(t *testing.T) {
	t.Parallel()

	// So a test can assert an audit's real cost against the worst case it
	// declared -- a request budget that drifts from reality makes every pacing
	// decision wrong.
	s := New(t, Quirks{})

	for range 3 {
		get(t, s.CollectionURL())
	}

	if got := s.Requests(); got != 3 {
		t.Errorf("Requests = %d, want 3", got)
	}
}

func TestUnit_Quirkserver_RejectsMalformedAndUnsupported(t *testing.T) {
	t.Parallel()

	s := New(t, Quirks{})

	request, err := http.NewRequest(http.MethodPost, s.CollectionURL(), strings.NewReader("{not json")) //nolint:noctx // a test fixture
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = response.Body.Close()

	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("a malformed body should give 400, got %d", response.StatusCode)
	}

	// A malformed update body, same contract.
	upReq, err := http.NewRequest(http.MethodPut, s.ItemURL("1"), strings.NewReader("{not json")) //nolint:noctx // a test fixture
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	upResp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = upResp.Body.Close()
	if upResp.StatusCode != http.StatusBadRequest {
		t.Errorf("a malformed update body should give 400, got %d", upResp.StatusCode)
	}

	// An unsupported method on the collection.
	if status, _ := do(t, http.MethodDelete, s.CollectionURL(), nil); status != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", status)
	}

	// Updating something absent.
	if status, _ := put(t, s.ItemURL("absent"), map[string]any{"key": "k"}); status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
}
