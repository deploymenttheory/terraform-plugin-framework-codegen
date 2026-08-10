package quirkserver

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// writeExhibits holds one exhibit per write-path quirk, keyed by the Quirks
// field it demonstrates. TestUnit_Quirkserver_EachQuirkIsExhibited drives
// them and refuses a field without an entry.
var writeExhibits = map[string]func(*testing.T){
	"SilentlyDiscards": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{SilentlyDiscards: []string{"colour"}})

		// 201, and the field is simply not there. This is the trap that makes
		// a naive read-back check wrong: it was demonstrably sent and is
		// demonstrably absent.
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
	},

	"SilentlyDiscardsOnUpdate": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{SilentlyDiscardsOnUpdate: []string{"colour"}})

		// Create stores it, which is what separates this from SilentlyDiscards.
		status, created := post(t, s.CollectionURL(), map[string]any{"key": "k", "colour": "blue"})
		if status != http.StatusCreated || created["colour"] != "blue" {
			t.Fatalf("create should store the field: %d %v", status, created)
		}

		id, _ := created["id"].(string)

		// The update answers success and changes nothing, which is the whole
		// point: an API that refused the change would say so, and this one
		// does not.
		status, updated := put(t, s.ItemURL(id), map[string]any{"key": "k", "colour": "red"})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 -- a refusal would be immutability, not this", status)
		}
		if updated["colour"] != "blue" {
			t.Errorf("colour = %v, want the original blue", updated["colour"])
		}
	},

	"DiscardsWhen": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{DiscardsWhen: &Conditional{
			WhenField: "mode", WhenValue: "static", Then: "colour",
		}})

		// On the matching branch: 201, and the value is gone -- the matchType
		// case.
		status, created := post(t, s.CollectionURL(),
			map[string]any{"key": "k", "mode": "static", "colour": "blue"})
		if status != http.StatusCreated {
			t.Fatalf("create = %d, want 201; a refusal would be requiredness, not this", status)
		}
		if _, present := created["colour"]; present {
			t.Errorf("colour = %v, want it silently dropped on the static branch", created["colour"])
		}

		// On every other branch it is stored, which is what makes the
		// unconditional answer a half-truth in both directions.
		status, created = post(t, s.CollectionURL(),
			map[string]any{"key": "k2", "mode": "dynamic", "colour": "blue"})
		if status != http.StatusCreated || created["colour"] != "blue" {
			t.Fatalf("the other branch should store the field: %d %v", status, created)
		}
	},

	"ImmutableAfterCreate": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{ImmutableAfterCreate: []string{"key"}})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "one", "value": "v"})
		id, _ := created["id"].(string)

		// The same value is fine, which is what makes the protocol's control
		// request work.
		if status, _ := put(t, s.ItemURL(id), map[string]any{"key": "one", "value": "w"}); status != http.StatusOK {
			t.Errorf("an unchanged immutable field should be accepted, got %d", status)
		}

		status, body := put(t, s.ItemURL(id), map[string]any{"key": "two"})
		if status != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		// The error names the field, which is what lets the auditor write the
		// cause down as observed rather than guessed.
		if detail, _ := body["detail"].(string); detail != "key" {
			t.Errorf("the error should name the field, got %v", body)
		}
	},

	"RequiresExtraFieldOnUpdate": func(t *testing.T) {
		t.Parallel()

		// The quirk that proves the immutability protocol's control request is
		// load-bearing: an audit without it sees a 4xx and concludes
		// immutability when the request shape was simply wrong.
		s := New(t, Quirks{RequiresExtraFieldOnUpdate: "version"})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "k"})
		id, _ := created["id"].(string)

		if status, _ := put(t, s.ItemURL(id), map[string]any{"key": "k2"}); status != http.StatusBadRequest {
			t.Errorf("an update omitting the extra field should fail, got %d", status)
		}
		if status, _ := put(t, s.ItemURL(id), map[string]any{"key": "k2", "version": 1}); status != http.StatusOK {
			t.Errorf("an update including it should succeed, got %d", status)
		}
	},

	"ConstantDefaults": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{ConstantDefaults: map[string]any{"colour": "blue"}})

		_, first := post(t, s.CollectionURL(), map[string]any{"key": "a"})
		_, second := post(t, s.CollectionURL(), map[string]any{"key": "b"})

		// Identical across creates, which is what makes it a constant rather
		// than derived.
		if first["colour"] != "blue" || second["colour"] != "blue" {
			t.Errorf("a constant default should be the same every time: %v, %v", first, second)
		}

		// And an explicit value wins, or it would not be a default.
		_, explicit := post(t, s.CollectionURL(), map[string]any{"key": "c", "colour": "red"})
		if explicit["colour"] != "red" {
			t.Errorf("an explicit value must win: %v", explicit)
		}
	},

	"DerivedDefaults": func(t *testing.T) {
		t.Parallel()

		// An auditor without the derivation check writes this down as a static
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
	},

	"CounterDefault": func(t *testing.T) {
		t.Parallel()

		// Two byte-identical creates differ, which is the check that rules out
		// a constant.
		s := New(t, Quirks{CounterDefault: "ordinal"})

		_, a := post(t, s.CollectionURL(), map[string]any{"key": "same"})
		_, b := post(t, s.CollectionURL(), map[string]any{"key": "same"})

		if a["ordinal"] == b["ordinal"] {
			t.Errorf("a counter default must differ between identical creates: %v", a["ordinal"])
		}
	},

	"RequiredButUndeclared": func(t *testing.T) {
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
	},

	"ConditionallyRequired": func(t *testing.T) {
		t.Parallel()

		// The ICMP/port case from a fixup table: the quirk that proves
		// one-field-at-a-time omission from a single fixture reports half a
		// truth either way.
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
	},

	"WriteSideEffects": func(t *testing.T) {
		t.Parallel()

		// networkMeasurements to bandwidthMeasurements: the class of quirk a
		// human would never have guessed from a specification and an auditor
		// genuinely can find.
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
	},

	"NormalisesCase": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{NormalisesCase: []string{"key"}})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "MiXeD"})
		if created["key"] != "mixed" {
			t.Errorf("case was not normalised: %v", created["key"])
		}
	},

	"TrimsWhitespace": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{TrimsWhitespace: []string{"key"}})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "  padded  "})
		if created["key"] != "padded" {
			t.Errorf("whitespace was not trimmed: %q", created["key"])
		}
	},

	"SortsLists": func(t *testing.T) {
		t.Parallel()

		// Hand-written providers carry a runtime collection re-sorter purely
		// to suppress the drift this causes, at runtime, which is the wrong
		// layer to fix it at.
		s := New(t, Quirks{SortsLists: []string{"values"}})

		_, created := post(t, s.CollectionURL(), map[string]any{"values": []any{"c", "a", "b"}})

		got, _ := created["values"].([]any)
		if len(got) != 3 || got[0] != "a" || got[2] != "c" {
			t.Errorf("the list was not sorted: %v", got)
		}
	},

	"PutClearsOmitted": func(t *testing.T) {
		t.Parallel()

		// Getting this wrong makes a generated provider silently erase
		// attributes the practitioner never mentioned.
		preserve := New(t, Quirks{})
		replace := New(t, Quirks{PutClearsOmitted: true})

		for name, s := range map[string]*Server{"preserve": preserve, "replace": replace} {
			_, created := post(t, s.CollectionURL(), map[string]any{"key": "k", "value": "v"})
			id, _ := created["id"].(string)

			_, updated := put(t, s.ItemURL(id), map[string]any{"key": "k2"})

			_, survived := updated["value"]
			if name == "preserve" && !survived {
				t.Error("the default semantics should preserve an omitted field")
			}
			if name == "replace" && survived {
				t.Errorf("replace semantics should clear an omitted field: %v", updated)
			}
		}
	},

	"ClosedEnum": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{ClosedEnum: map[string][]string{"objectType": {"test", "agent"}}})

		if status, _ := post(t, s.CollectionURL(), map[string]any{"objectType": "test"}); status != http.StatusCreated {
			t.Errorf("a permitted value should be accepted, got %d", status)
		}
		if status, _ := post(t, s.CollectionURL(), map[string]any{"objectType": "octopus"}); status != http.StatusBadRequest {
			t.Errorf("a value outside the set should be refused, got %d", status)
		}
	},

	// RejectsValueUnless needs two requests to show both halves: the same
	// value refused on one branch and taken on the other, which is precisely
	// the half-truth the enum escalation exists to correct.
	"RejectsValueUnless": func(t *testing.T) {
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
	},

	"RejectsDocumentedValue": func(t *testing.T) {
		t.Parallel()

		// The valuable enum result: the specification is stale, and a
		// spec-derived validator would have been actively harmful.
		s := New(t, Quirks{RejectsDocumentedValue: map[string]string{"objectType": "deprecated"}})

		if status, _ := post(t, s.CollectionURL(), map[string]any{"objectType": "deprecated"}); status != http.StatusBadRequest {
			t.Errorf("a documented-but-rejected value should be refused, got %d", status)
		}
		if status, _ := post(t, s.CollectionURL(), map[string]any{"objectType": "test"}); status != http.StatusCreated {
			t.Errorf("another value should still work, got %d", status)
		}
	},

	"Forces": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{Forces: map[string]any{"networkMeasurements": true}})

		// Send false, get true -- on the create response, not just a later
		// read.
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
	},

	"NullsInWriteResponse": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{NullsInWriteResponse: []string{"includeHeaders"}})

		status, created := post(t, s.CollectionURL(),
			map[string]any{"key": "k", "includeHeaders": true})
		if status != http.StatusCreated {
			t.Fatalf("status = %d, want 201", status)
		}

		// Present and null -- the axis the auditor must keep, and the
		// difference from SilentlyDiscards, which answers absence.
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
	},

	"SuppressWhenSibling": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{SuppressWhenSibling: &Conditional{
			WhenField: "requestMethod", WhenValue: "get", Then: "postBody",
		}})

		// Alone, the field round-trips -- which is exactly what makes the
		// interaction invisible to any audit that never sends the maximal
		// body.
		status, created := post(t, s.CollectionURL(),
			map[string]any{"key": "k", "postBody": "b"})
		if status != http.StatusCreated || created["postBody"] != "b" {
			t.Fatalf("the field alone should round-trip: %d %v", status, created)
		}

		// With the sibling riding along on an update, it is stripped --
		// including the value the create had stored, because carrying it
		// through would hide the suppression from a PUT-based audit.
		id, _ := created["id"].(string)
		status, updated := put(t, s.ItemURL(id),
			map[string]any{"key": "k", "postBody": "b", "requestMethod": "get"})
		if status != http.StatusOK {
			t.Fatalf("suppression is silent: %d", status)
		}
		if _, present := updated["postBody"]; present {
			t.Errorf("postBody = %v, want it stripped when requestMethod is get", updated["postBody"])
		}
	},

	"UpdateDefaults": func(t *testing.T) {
		t.Parallel()

		s := New(t, Quirks{UpdateDefaults: map[string]any{"colour": "grey"}})

		_, created := post(t, s.CollectionURL(), map[string]any{"key": "k", "colour": "blue"})
		id, _ := created["id"].(string)

		// Omitted on update: not preserved, not cleared -- reset to the
		// update-path constant, which need not be any default create ever
		// applied.
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
	},
}
