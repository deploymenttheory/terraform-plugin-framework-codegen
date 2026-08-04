package quirkserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe/apierr"
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

	req, err := http.NewRequest(method, url, r) //nolint:noctx // a test fixture
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
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

	return resp.StatusCode, out
}

// TestUnit_Quirkserver_EachQuirkIsExhibited is the Phase 4.3 milestone.
//
// Every switch is asserted to actually misbehave. This matters more than it looks: a quirk
// that silently stopped working would make every probe test depending on it pass for the
// wrong reason, and a ground-truth fixture that lies is worse than no fixture at all.
//
// One subtest per quirk, each asserting the *observable* consequence rather than an internal
// flag.
func TestUnit_Quirkserver_EachQuirkIsExhibited(t *testing.T) {
	t.Parallel()

	t.Run("SilentlyDiscardsOnUpdate", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{SilentlyDiscardsOnUpdate: []string{"colour"}})

		// Create stores it, which is what separates this from SilentlyDiscards.
		status, created := post(t, s.CollectionURL(), map[string]any{"key": "k", "colour": "blue"})
		if status != http.StatusCreated || created["colour"] != "blue" {
			t.Fatalf("create should store the field: %d %v", status, created)
		}

		id, _ := created["id"].(string)

		// The update answers success and changes nothing, which is the whole point: an API that
		// refused the change would say so, and this one does not.
		status, updated := put(t, s.ItemURL(id), map[string]any{"key": "k", "colour": "red"})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 -- a refusal would be immutability, not this", status)
		}
		if updated["colour"] != "blue" {
			t.Errorf("colour = %v, want the original blue", updated["colour"])
		}
	})

	t.Run("DiscardsWhen", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{DiscardsWhen: &Conditional{
			WhenField: "mode", WhenValue: "static", Then: "colour",
		}})

		// On the matching branch: 201, and the value is gone -- the matchType case.
		status, created := post(t, s.CollectionURL(),
			map[string]any{"key": "k", "mode": "static", "colour": "blue"})
		if status != http.StatusCreated {
			t.Fatalf("create = %d, want 201; a refusal would be requiredness, not this", status)
		}
		if _, present := created["colour"]; present {
			t.Errorf("colour = %v, want it silently dropped on the static branch", created["colour"])
		}

		// On every other branch it is stored, which is what makes the unconditional
		// answer a half-truth in both directions.
		status, created = post(t, s.CollectionURL(),
			map[string]any{"key": "k2", "mode": "dynamic", "colour": "blue"})
		if status != http.StatusCreated || created["colour"] != "blue" {
			t.Fatalf("the other branch should store the field: %d %v", status, created)
		}
	})

	t.Run("SilentlyDiscards", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{SilentlyDiscards: []string{"colour"}})

		// 201, and the field is simply not there. This is the trap that makes a naive
		// ReturnedOnRead probe wrong: it was demonstrably sent and is demonstrably absent.
		status, created := post(t, s.CollectionURL(), map[string]any{"key": "k", "colour": "blue"})
		if status != http.StatusCreated {
			t.Fatalf("status = %d, want 201", status)
		}
		if _, present := created["colour"]; present {
			t.Errorf("a silently discarded field came back: %v", created)
		}
		if created["key"] != "k" {
			t.Errorf("an ordinary field was lost: %v", created)
		}
	})

	t.Run("ImmutableAfterCreate", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{ImmutableAfterCreate: []string{"key"}})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "one", "value": "v"})
		id, _ := created["id"].(string)

		// The same value is fine, which is what makes the protocol's control request work.
		if status, _ := put(t, s.ItemURL(id), map[string]any{"key": "one", "value": "w"}); status != http.StatusOK {
			t.Errorf("an unchanged immutable field should be accepted, got %d", status)
		}

		status, body := put(t, s.ItemURL(id), map[string]any{"key": "two"})
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		// The error names the field, which is what lets a probe emit Observed rather than
		// Inferred.
		if detail, _ := body["detail"].(string); detail != "key" {
			t.Errorf("the error should name the field, got %v", body)
		}
	})

	t.Run("RequiresExtraFieldOnUpdate", func(t *testing.T) {
		t.Parallel()

		// The quirk that proves the immutability protocol's control request is
		// load-bearing: a probe without it sees a 4xx and concludes immutability when the
		// request shape was simply wrong.
		s := New(t, Quirks{RequiresExtraFieldOnUpdate: "version"})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "k"})
		id, _ := created["id"].(string)

		if status, _ := put(t, s.ItemURL(id), map[string]any{"key": "k2"}); status != http.StatusBadRequest {
			t.Errorf("an update omitting the extra field should fail, got %d", status)
		}
		if status, _ := put(t, s.ItemURL(id), map[string]any{"key": "k2", "version": 1}); status != http.StatusOK {
			t.Errorf("an update including it should succeed, got %d", status)
		}
	})

	t.Run("ConstantDefaults", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{ConstantDefaults: map[string]any{"colour": "blue"}})

		_, first := post(t, s.CollectionURL(), map[string]any{"key": "a"})
		_, second := post(t, s.CollectionURL(), map[string]any{"key": "b"})

		// Identical across creates, which is what makes it a constant rather than derived.
		if first["colour"] != "blue" || second["colour"] != "blue" {
			t.Errorf("a constant default should be the same every time: %v, %v", first, second)
		}

		// And an explicit value wins, or it would not be a default.
		_, explicit := post(t, s.CollectionURL(), map[string]any{"key": "c", "colour": "red"})
		if explicit["colour"] != "red" {
			t.Errorf("an explicit value must win: %v", explicit)
		}
	})

	t.Run("DerivedDefaults", func(t *testing.T) {
		t.Parallel()

		// A prober without the derivation check writes this into the blueprint as a static
		// default, which is then a permanent lie.
		s := New(t, Quirks{DerivedDefaults: map[string]string{"colour": "key"}})

		_, a := post(t, s.CollectionURL(), map[string]any{"key": "alpha"})
		_, b := post(t, s.CollectionURL(), map[string]any{"key": "beta"})

		if a["colour"] == b["colour"] {
			t.Errorf("a derived default must vary with its source: %v vs %v", a["colour"], b["colour"])
		}
		if !strings.Contains(fmt.Sprint(a["colour"]), "alpha") {
			t.Errorf("the derived value should reflect its source: %v", a["colour"])
		}
	})

	t.Run("CounterDefault", func(t *testing.T) {
		t.Parallel()

		// Two byte-identical creates differ, which is the check that rules out a constant.
		s := New(t, Quirks{CounterDefault: "ordinal"})

		_, a := post(t, s.CollectionURL(), map[string]any{"key": "same"})
		_, b := post(t, s.CollectionURL(), map[string]any{"key": "same"})

		if a["ordinal"] == b["ordinal"] {
			t.Errorf("a counter default must differ between identical creates: %v", a["ordinal"])
		}
	})

	t.Run("RequiredButUndeclared", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{RequiredButUndeclared: []string{"key"}})

		status, body := post(t, s.CollectionURL(), map[string]any{"value": "v"})
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		if detail, _ := body["detail"].(string); detail != "key" {
			t.Errorf("the error should name the missing field, got %v", body)
		}

		if status, _ := post(t, s.CollectionURL(), map[string]any{"key": "k"}); status != http.StatusCreated {
			t.Errorf("supplying it should succeed, got %d", status)
		}
	})

	t.Run("ConditionallyRequired", func(t *testing.T) {
		t.Parallel()

		// The ICMP/port case from a fixup table: the quirk that proves one-field-at-a-time
		// omission from a single fixture reports half a truth either way.
		s := New(t, Quirks{ConditionallyRequired: &Conditional{
			WhenField: "protocol", WhenValue: "tcp", Then: "port",
		}})

		if status, _ := post(t, s.CollectionURL(), map[string]any{"protocol": "icmp"}); status != http.StatusCreated {
			t.Errorf("port is not required for icmp, got %d", status)
		}
		if status, _ := post(t, s.CollectionURL(), map[string]any{"protocol": "tcp"}); status != http.StatusBadRequest {
			t.Errorf("port is required for tcp, got %d", status)
		}
		if status, _ := post(t, s.CollectionURL(), map[string]any{"protocol": "tcp", "port": 443}); status != http.StatusCreated {
			t.Errorf("tcp with a port should succeed, got %d", status)
		}
	})

	t.Run("WriteSideEffects", func(t *testing.T) {
		t.Parallel()

		// networkMeasurements to bandwidthMeasurements: the one quirk in util.go a human
		// would never have guessed and a prober genuinely can find.
		s := New(t, Quirks{WriteSideEffects: map[string]string{
			"networkMeasurements": "bandwidthMeasurements",
		}})

		_, on := post(t, s.CollectionURL(), map[string]any{"networkMeasurements": true})
		if on["bandwidthMeasurements"] != true {
			t.Errorf("the side effect did not fire: %v", on)
		}

		_, off := post(t, s.CollectionURL(), map[string]any{"networkMeasurements": false})
		if _, present := off["bandwidthMeasurements"]; present {
			t.Errorf("the side effect fired without its trigger: %v", off)
		}
	})

	t.Run("NormalisesCase", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{NormalisesCase: []string{"key"}})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "MiXeD"})
		if created["key"] != "mixed" {
			t.Errorf("case was not normalised: %v", created["key"])
		}
	})

	t.Run("TrimsWhitespace", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{TrimsWhitespace: []string{"key"}})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "  padded  "})
		if created["key"] != "padded" {
			t.Errorf("whitespace was not trimmed: %q", created["key"])
		}
	})

	t.Run("SortsLists", func(t *testing.T) {
		t.Parallel()

		// a runtime collection re-sorter exists in the current provider purely to suppress
		// the drift this causes, at runtime, which is the wrong layer to fix it at.
		s := New(t, Quirks{SortsLists: []string{"values"}})

		_, created := post(t, s.CollectionURL(), map[string]any{"values": []any{"c", "a", "b"}})

		got, _ := created["values"].([]any)
		if len(got) != 3 || got[0] != "a" || got[2] != "c" {
			t.Errorf("the list was not sorted: %v", got)
		}
	})

	t.Run("ExpansionGated", func(t *testing.T) {
		t.Parallel()

		// The assignments trap. A probe that reads back once concludes the field is never
		// returned, and the generated state mapper then blanks a real value on every
		// refresh.
		s := New(t, Quirks{ExpansionGated: map[string]string{"assignments": "assignments"}})

		id := s.Seed(map[string]any{"key": "k", "assignments": []any{"a"}})

		_, bare := get(t, s.ItemURL(id))
		if _, present := bare["assignments"]; present {
			t.Errorf("a gated field appeared without its expansion: %v", bare)
		}

		_, expanded := get(t, s.ItemURL(id)+"?expand=assignments")
		if _, present := expanded["assignments"]; !present {
			t.Errorf("a gated field did not appear with its expansion: %v", expanded)
		}
	})

	t.Run("PutClearsOmitted", func(t *testing.T) {
		t.Parallel()

		// Getting this wrong makes a generated provider "silently erase attributes the
		// practitioner never mentioned", in the IR's own words.
		merge := New(t, Quirks{})
		replace := New(t, Quirks{PutClearsOmitted: true})

		for name, s := range map[string]*Server{"merge": merge, "replace": replace} {
			_, created := post(t, s.CollectionURL(), map[string]any{"key": "k", "value": "v"})
			id, _ := created["id"].(string)

			_, updated := put(t, s.ItemURL(id), map[string]any{"key": "k2"})

			_, survived := updated["value"]
			if name == "merge" && !survived {
				t.Error("merge semantics should preserve an omitted field")
			}
			if name == "replace" && survived {
				t.Errorf("replace semantics should clear an omitted field: %v", updated)
			}
		}
	})

	t.Run("EventuallyConsistentReads", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{EventuallyConsistentReads: 2})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "k"})
		id, _ := created["id"].(string)

		for i := 1; i <= 2; i++ {
			if status, _ := get(t, s.ItemURL(id)); status != http.StatusNotFound {
				t.Errorf("read %d: status = %d, want 404", i, status)
			}
		}
		if status, _ := get(t, s.ItemURL(id)); status != http.StatusOK {
			t.Errorf("the third read should succeed, got %d", status)
		}
	})

	t.Run("ErrorEnvelope", func(t *testing.T) {
		t.Parallel()

		// All four shapes the real API uses. A prober that assumed one could not tell
		// "rejected because immutable" from "rejected because the token expired".
		tests := map[apierr.Envelope]func(map[string]any) bool{
			apierr.EnvelopeProblem: func(b map[string]any) bool { _, ok := b["title"]; return ok },
			apierr.EnvelopeOAuth:   func(b map[string]any) bool { _, ok := b["error_description"]; return ok },
			apierr.EnvelopeLegacy:  func(b map[string]any) bool { _, ok := b["errorMessage"]; return ok },
			apierr.EnvelopeEmpty:   func(b map[string]any) bool { return len(b) == 0 },
		}

		for kind, check := range tests {
			s := New(t, Quirks{ErrorEnvelope: kind})

			status, body := get(t, s.ItemURL("absent"))
			if status != http.StatusNotFound {
				t.Errorf("%s: status = %d, want 404", kind, status)
			}
			if !check(body) {
				t.Errorf("%s: body has the wrong shape: %v", kind, body)
			}
		}
	})

	t.Run("ClosedEnum", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{ClosedEnum: map[string][]string{"objectType": {"test", "agent"}}})

		if status, _ := post(t, s.CollectionURL(), map[string]any{"objectType": "test"}); status != http.StatusCreated {
			t.Errorf("a permitted value should be accepted, got %d", status)
		}
		if status, _ := post(t, s.CollectionURL(), map[string]any{"objectType": "octopus"}); status != http.StatusBadRequest {
			t.Errorf("a value outside the set should be refused, got %d", status)
		}
	})

	t.Run("RejectsDocumentedValue", func(t *testing.T) {
		t.Parallel()

		// The valuable enum result: the specification is stale, and a spec-derived
		// validator would have been actively harmful.
		s := New(t, Quirks{RejectsDocumentedValue: map[string]string{"objectType": "deprecated"}})

		if status, _ := post(t, s.CollectionURL(), map[string]any{"objectType": "deprecated"}); status != http.StatusBadRequest {
			t.Errorf("a documented-but-rejected value should be refused, got %d", status)
		}
		if status, _ := post(t, s.CollectionURL(), map[string]any{"objectType": "test"}); status != http.StatusCreated {
			t.Errorf("another value should still work, got %d", status)
		}
	})

	t.Run("DeleteFails", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{DeleteFails: true})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "k"})
		id, _ := created["id"].(string)

		status, _ := do(t, http.MethodDelete, s.ItemURL(id), nil)
		if status != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", status)
		}
		// And the object survives, which is what makes it an orphan.
		if len(s.Objects()) != 1 {
			t.Errorf("a failed delete must leave the object: %v", s.Objects())
		}
	})

	t.Run("DeleteFlakyEvery", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{DeleteFlakyEvery: 2})

		var statuses []int
		for range 3 {
			_, created := post(t, s.CollectionURL(), map[string]any{"key": "k"})
			id, _ := created["id"].(string)
			status, _ := do(t, http.MethodDelete, s.ItemURL(id), nil)
			statuses = append(statuses, status)
		}

		// The second attempt fails, the first and third do not.
		if statuses[0] == http.StatusInternalServerError || statuses[1] != http.StatusInternalServerError {
			t.Errorf("every second delete should fail, got %v", statuses)
		}
	})

	t.Run("RateLimitHeaders", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{RateLimitHeaders: true, RateLimit: 2})

		resp, err := http.Get(s.CollectionURL()) //nolint:noctx // a test fixture
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		_ = resp.Body.Close()

		// The server states the budget, which is why the prober paces from the headers
		// rather than guessing.
		if got := resp.Header.Get("x-organization-rate-limit-limit"); got != "2" {
			t.Errorf("limit header = %q, want 2", got)
		}
		if resp.Header.Get("x-organization-rate-limit-remaining") == "" {
			t.Error("the remaining header should be present")
		}

		// Spending the budget yields 429 with a retry-after.
		var last *http.Response
		for range 3 {
			last, err = http.Get(s.CollectionURL()) //nolint:noctx // a test fixture
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			_ = last.Body.Close()
		}
		if last.StatusCode != http.StatusTooManyRequests {
			t.Errorf("status = %d, want 429 once the budget is spent", last.StatusCode)
		}
		if last.Header.Get("retry-after") == "" {
			t.Error("a 429 should carry retry-after")
		}
	})

	t.Run("VolatileFields", func(t *testing.T) {
		t.Parallel()

		// The modifiedDate perpetual-diff class: every plan reports drift forever.
		s := New(t, Quirks{VolatileFields: []string{"modifiedDate"}})

		id := s.Seed(map[string]any{"key": "k"})

		_, first := get(t, s.ItemURL(id))
		_, second := get(t, s.ItemURL(id))

		if first["modifiedDate"] == second["modifiedDate"] {
			t.Errorf("a volatile field must differ between reads: %v", first["modifiedDate"])
		}
		// A stable field must not, or the probe could not tell them apart.
		if first["key"] != second["key"] {
			t.Errorf("a stable field changed: %v vs %v", first["key"], second["key"])
		}
	})

	t.Run("IgnoresUnknownQueryParams", func(t *testing.T) {
		t.Parallel()

		// real APIs do exactly this, and it calibrates every
		// probe that depends on unknown *body* fields being ignored.
		strict := New(t, Quirks{})
		lax := New(t, Quirks{IgnoresUnknownQueryParams: true})

		if status, _ := get(t, strict.CollectionURL()+"?tfpfgen_probe=1"); status != http.StatusBadRequest {
			t.Errorf("a strict server should reject an unknown parameter, got %d", status)
		}
		if status, _ := get(t, lax.CollectionURL()+"?tfpfgen_probe=1"); status != http.StatusOK {
			t.Errorf("a lax server should ignore it, got %d", status)
		}
	})

	t.Run("TypedQueryParams", func(t *testing.T) {
		t.Parallel()

		// How the error-envelope probe provokes an error without mutating anything.
		s := New(t, Quirks{TypedQueryParams: []string{"limit"}})

		if status, _ := get(t, s.CollectionURL()+"?limit=10"); status != http.StatusOK {
			t.Errorf("a valid value should be accepted, got %d", status)
		}
		if status, _ := get(t, s.CollectionURL()+"?limit=abc"); status != http.StatusBadRequest {
			t.Errorf("a bad value should be rejected, got %d", status)
		}
	})

	t.Run("NotFoundStatus", func(t *testing.T) {
		t.Parallel()

		// An API that returns 403 for another tenant's identifier is indistinguishable
		// from one that returns it for an absent object, and that is itself worth
		// recording rather than assuming.
		s := New(t, Quirks{NotFoundStatus: http.StatusForbidden})

		if status, _ := get(t, s.ItemURL("absent")); status != http.StatusForbidden {
			t.Errorf("status = %d, want 403", status)
		}
	})
}

// TestUnit_Quirkserver_ZeroValueIsWellBehaved: a test enables exactly the quirk it is about,
// so the default has to be an ordinary API. Otherwise every probe test would be exercising
// misbehaviour it did not ask for.
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

	// So a probe test can assert its real cost against the worst case its Cost method
	// declared -- a Cost that drifts from reality makes every budget wrong.
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

	req, err := http.NewRequest(http.MethodPost, s.CollectionURL(), strings.NewReader("{not json")) //nolint:noctx // a test fixture
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a malformed body should give 400, got %d", resp.StatusCode)
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

// RejectsValueUnless is exhibited separately from the table above because it needs two
// requests to show both halves: the same value refused on one branch and taken on the
// other, which is precisely the half-truth the enum escalation exists to correct.
func TestUnit_Quirkserver_RejectsValueUnlessIsExhibited(t *testing.T) {
	t.Parallel()

	s := New(t, Quirks{RejectsValueUnless: map[string]Conditional{
		"objectType=endpoint-agent": {WhenField: "mode", WhenValue: "dynamic"},
	}})

	status, body := post(t, s.CollectionURL(),
		map[string]any{"key": "k", "objectType": "endpoint-agent", "mode": "static"})
	if status != http.StatusBadRequest {
		t.Fatalf("the static branch should refuse the value: %d %v", status, body)
	}
	if title, _ := body["title"].(string); !strings.Contains(title, "objectType") {
		t.Errorf("the refusal should name the field: %v", body)
	}

	status, _ = post(t, s.CollectionURL(),
		map[string]any{"key": "k2", "objectType": "endpoint-agent", "mode": "dynamic"})
	if status != http.StatusCreated {
		t.Fatalf("the dynamic branch should take the value: %d", status)
	}
}

// The four rehearsal-era quirks, each drawn from a behaviour the classic-tests wave met
// live at acceptance time. Same contract as every switch above: asserted observable
// consequence, or the probe tests depending on it pass for the wrong reason.
func TestUnit_Quirkserver_RehearsalQuirksAreExhibited(t *testing.T) {
	t.Parallel()

	t.Run("Forces", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{Forces: map[string]any{"networkMeasurements": true}})

		// Send false, get true -- on the create response, not just a later read.
		status, created := post(t, s.CollectionURL(),
			map[string]any{"key": "k", "networkMeasurements": false})
		if status != http.StatusCreated {
			t.Fatalf("status = %d, want 201; forcing is silent, never a refusal", status)
		}
		if created["networkMeasurements"] != true {
			t.Errorf("networkMeasurements = %v, want the forced true", created["networkMeasurements"])
		}

		// The update path forces identically.
		id, _ := created["id"].(string)
		status, updated := put(t, s.ItemURL(id),
			map[string]any{"key": "k", "networkMeasurements": false})
		if status != http.StatusOK || updated["networkMeasurements"] != true {
			t.Errorf("update should force too: %d %v", status, updated)
		}
	})

	t.Run("NullsInWriteResponse", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{NullsInWriteResponse: []string{"includeHeaders"}})

		status, created := post(t, s.CollectionURL(),
			map[string]any{"key": "k", "includeHeaders": true})
		if status != http.StatusCreated {
			t.Fatalf("status = %d, want 201", status)
		}

		// Present and null -- the axis LookupField distinguishes, and the difference
		// from SilentlyDiscards, which answers absence.
		v, present := created["includeHeaders"]
		if !present || v != nil {
			t.Errorf("includeHeaders = %v (present=%v), want explicit null", v, present)
		}

		id, _ := created["id"].(string)
		status, read := get(t, s.ItemURL(id))
		if status != http.StatusOK {
			t.Fatalf("read = %d", status)
		}
		if v, present := read["includeHeaders"]; !present || v != nil {
			t.Errorf("the read answers explicit null too, got %v (present=%v)", v, present)
		}
	})

	t.Run("SuppressWhenSibling", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{SuppressWhenSibling: &Conditional{
			WhenField: "requestMethod", WhenValue: "get", Then: "postBody",
		}})

		// Alone, the field round-trips -- which is exactly what makes the interaction
		// invisible to any probe that never sends the maximal body.
		status, created := post(t, s.CollectionURL(),
			map[string]any{"key": "k", "postBody": "b"})
		if status != http.StatusCreated || created["postBody"] != "b" {
			t.Fatalf("the field alone should round-trip: %d %v", status, created)
		}

		// With the sibling riding along on an update, it is stripped -- including the
		// value the create had stored, because a merge carrying it through would hide
		// the suppression from a PUT probe.
		id, _ := created["id"].(string)
		status, updated := put(t, s.ItemURL(id),
			map[string]any{"key": "k", "postBody": "b", "requestMethod": "get"})
		if status != http.StatusOK {
			t.Fatalf("suppression is silent: %d", status)
		}
		if _, present := updated["postBody"]; present {
			t.Errorf("postBody = %v, want it stripped when requestMethod is get", updated["postBody"])
		}
	})

	t.Run("UpdateDefaults", func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{UpdateDefaults: map[string]any{"colour": "grey"}})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "k", "colour": "blue"})
		id, _ := created["id"].(string)

		// Omitted on update: not preserved, not cleared -- reset to the update-path
		// constant, which need not be any default create ever applied.
		status, updated := put(t, s.ItemURL(id), map[string]any{"key": "k"})
		if status != http.StatusOK {
			t.Fatalf("update = %d", status)
		}
		if updated["colour"] != "grey" {
			t.Errorf("colour = %v, want the update-path grey rather than the stored blue",
				updated["colour"])
		}

		// Sending the field still wins the usual way.
		status, updated = put(t, s.ItemURL(id), map[string]any{"key": "k", "colour": "red"})
		if status != http.StatusOK || updated["colour"] != "red" {
			t.Errorf("a sent value must beat the update default: %d %v", status, updated)
		}
	})
}
