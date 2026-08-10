package quirkserver

import (
	"net/http"
	"strings"
	"testing"
)

// readExhibits holds one exhibit per read-path and lifecycle quirk, keyed by
// the Quirks field it demonstrates. TestUnit_Quirkserver_EachQuirkIsExhibited
// drives them and refuses a field without an entry.
var readExhibits = map[string]func(*testing.T){
	"ExpansionGated": func(t *testing.T) {
		t.Parallel()

		// The assignments trap. An audit that reads back once concludes the
		// field is never returned, and the generated state mapper then blanks
		// a real value on every refresh.
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
	},

	"EventuallyConsistentReads": func(t *testing.T) {
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
	},

	"ErrorEnvelope": func(t *testing.T) {
		t.Parallel()

		// All four declared shapes, plus an undeclared one that must still
		// carry the status. An auditor that assumed one shape could not tell
		// "rejected because immutable" from "rejected because the token
		// expired".
		tests := map[Envelope]func(map[string]any) bool{
			EnvelopeProblem:     func(b map[string]any) bool { _, ok := b["title"]; return ok },
			EnvelopeOAuth:       func(b map[string]any) bool { _, ok := b["error_description"]; return ok },
			EnvelopeLegacy:      func(b map[string]any) bool { _, ok := b["errorMessage"]; return ok },
			EnvelopeEmpty:       func(b map[string]any) bool { return len(b) == 0 },
			Envelope("bizarre"): func(b map[string]any) bool { return len(b) == 0 },
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
	},

	"DeleteFails": func(t *testing.T) {
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
	},

	"DeleteFlakyEvery": func(t *testing.T) {
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
	},

	"RateLimitHeaders": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{RateLimitHeaders: true})

		resp, err := http.Get(s.CollectionURL()) //nolint:noctx // a test fixture
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		_ = resp.Body.Close()

		// The server states the budget, which is why the auditor paces from
		// the headers rather than guessing. Without an explicit budget the
		// default applies.
		if got := resp.Header.Get("x-organization-rate-limit-limit"); got != "240" {
			t.Errorf("limit header = %q, want the default 240", got)
		}
		if resp.Header.Get("x-organization-rate-limit-remaining") == "" {
			t.Error("the remaining header should be present")
		}
		if resp.Header.Get("x-organization-rate-limit-reset") == "" {
			t.Error("the reset header should be present")
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200 while the budget lasts", resp.StatusCode)
		}
	},

	"RateLimit": func(t *testing.T) {
		t.Parallel()

		// A configured budget replaces the default, and spending it yields 429
		// with a retry-after.
		s := New(t, Quirks{RateLimitHeaders: true, RateLimit: 2})

		var last *http.Response
		for range 3 {
			resp, err := http.Get(s.CollectionURL()) //nolint:noctx // a test fixture
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			_ = resp.Body.Close()
			last = resp
		}

		if got := last.Header.Get("x-organization-rate-limit-limit"); got != "2" {
			t.Errorf("limit header = %q, want the configured 2", got)
		}
		if last.StatusCode != http.StatusTooManyRequests {
			t.Errorf("status = %d, want 429 once the budget is spent", last.StatusCode)
		}
		if last.Header.Get("retry-after") == "" {
			t.Error("a 429 should carry retry-after")
		}
	},

	"VolatileFields": func(t *testing.T) {
		t.Parallel()

		// The modifiedDate perpetual-diff class: every plan reports drift
		// forever.
		s := New(t, Quirks{VolatileFields: []string{"modifiedDate"}})

		id := s.Seed(map[string]any{"key": "k"})

		_, first := get(t, s.ItemURL(id))
		_, second := get(t, s.ItemURL(id))

		if first["modifiedDate"] == second["modifiedDate"] {
			t.Errorf("a volatile field must differ between reads: %v", first["modifiedDate"])
		}
		// A stable field must not, or the audit could not tell them apart.
		if first["key"] != second["key"] {
			t.Errorf("a stable field changed: %v vs %v", first["key"], second["key"])
		}
	},

	"IgnoresUnknownQueryParams": func(t *testing.T) {
		t.Parallel()

		// Real APIs do exactly this, and it grounds every audit check that
		// depends on unknown *body* fields being ignored.
		strict := New(t, Quirks{})
		lax := New(t, Quirks{IgnoresUnknownQueryParams: true})

		if status, _ := get(t, strict.CollectionURL()+"?tfpfgen_audit=1"); status != http.StatusBadRequest {
			t.Errorf("a strict server should reject an unknown parameter, got %d", status)
		}
		if status, _ := get(t, lax.CollectionURL()+"?tfpfgen_audit=1"); status != http.StatusOK {
			t.Errorf("a lax server should ignore it, got %d", status)
		}
	},

	"TypedQueryParams": func(t *testing.T) {
		t.Parallel()

		// How the error-envelope check provokes an error without mutating
		// anything.
		s := New(t, Quirks{TypedQueryParams: []string{"limit"}})

		if status, _ := get(t, s.CollectionURL()+"?limit=10"); status != http.StatusOK {
			t.Errorf("a valid value should be accepted, got %d", status)
		}
		if status, _ := get(t, s.CollectionURL()+"?limit=abc"); status != http.StatusBadRequest {
			t.Errorf("a bad value should be rejected, got %d", status)
		}
	},

	"BasePath": func(t *testing.T) {
		t.Parallel()

		// The prefix appears in every address handed out, and the full
		// lifecycle works through it -- the difference between a relative path
		// and the full path the wire actually sees.
		s := New(t, Quirks{BasePath: "/v7"})

		if !strings.HasSuffix(s.CollectionURL(), "/v7/things") {
			t.Fatalf("CollectionURL = %q, want the /v7 prefix in it", s.CollectionURL())
		}

		status, created := post(t, s.CollectionURL(), map[string]any{"key": "k"})
		if status != http.StatusCreated {
			t.Fatalf("create through the prefix = %d, want 201", status)
		}
		id, _ := created["id"].(string)
		if !strings.Contains(s.ItemURL(id), "/v7/things/") {
			t.Errorf("ItemURL = %q, want the /v7 prefix in it", s.ItemURL(id))
		}
		if status, _ := get(t, s.ItemURL(id)); status != http.StatusOK {
			t.Errorf("read through the prefix = %d, want 200", status)
		}
	},

	"NotFoundStatus": func(t *testing.T) {
		t.Parallel()

		// An API that returns 403 for another tenant's identifier is
		// indistinguishable from one that returns it for an absent object, and
		// that is itself worth observing rather than assuming.
		s := New(t, Quirks{NotFoundStatus: http.StatusForbidden})

		if status, _ := get(t, s.ItemURL("absent")); status != http.StatusForbidden {
			t.Errorf("status = %d, want 403", status)
		}
	},
}
