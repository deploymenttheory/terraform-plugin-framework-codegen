package apierr

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestUnit_Apierr_ClassifiesEveryEnvelope covers the four shapes the live API uses.
//
// The bodies are the ones documented against the real service in the SDK's client/errors.go,
// not invented examples -- a classifier tested against fabricated shapes would agree with
// itself and fail in production.
func TestUnit_Apierr_ClassifiesEveryEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		body     string
		envelope Envelope
		message  string
		detail   string
		code     string
	}{
		{
			name:   "problem details",
			status: 400,
			body: `{"type":"about:blank","title":"There were some errors in your request.",` +
				`"status":400,"instance":"/v7/tests/bgp"}`,
			envelope: EnvelopeProblem,
			message:  "There were some errors in your request.",
			// about:blank is RFC 7807's way of saying "no specific type", so it must not
			// become a code -- a code of "about:blank" would be worse than none.
			code: "",
		},
		{
			name:     "problem details with a detail and a real type",
			status:   422,
			body:     `{"type":"https://example.test/immutable","title":"cannot modify","detail":"key"}`,
			envelope: EnvelopeProblem,
			message:  "cannot modify",
			detail:   "key",
			code:     "https://example.test/immutable",
		},
		{
			name:     "oauth",
			status:   401,
			body:     `{"error":"invalid_token","error_description":"Invalid access token"}`,
			envelope: EnvelopeOAuth,
			message:  "Invalid access token",
			code:     "invalid_token",
		},
		{
			name:     "oauth with no description",
			status:   401,
			body:     `{"error":"invalid_token"}`,
			envelope: EnvelopeOAuth,
			message:  "invalid_token",
			code:     "invalid_token",
		},
		{
			name:     "legacy",
			status:   401,
			body:     `{"errorMessage":"401 Not Authorized\nPlease ensure you are using the correct token."}`,
			envelope: EnvelopeLegacy,
			message:  "401 Not Authorized\nPlease ensure you are using the correct token.",
		},
		{
			name:     "empty",
			status:   404,
			body:     "",
			envelope: EnvelopeEmpty,
			message:  "not found",
		},
		{
			name:     "whitespace only is still empty",
			status:   404,
			body:     "   \n  ",
			envelope: EnvelopeEmpty,
			message:  "not found",
		},
		{
			name: "an HTML error page from a proxy",
			// Worth distinguishing: it means the request did not reach the API at all, which
			// is a different problem from anything the API might have said.
			status:   502,
			body:     "<html><body>502 Bad Gateway</body></html>",
			envelope: EnvelopeUnknown,
			message:  "<html><body>502 Bad Gateway</body></html>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := Classify(tc.status, []byte(tc.body))

			if got.Envelope != tc.envelope {
				t.Errorf("Envelope = %q, want %q", got.Envelope, tc.envelope)
			}
			if got.Message != tc.message {
				t.Errorf("Message = %q, want %q", got.Message, tc.message)
			}
			if got.Detail != tc.detail {
				t.Errorf("Detail = %q, want %q", got.Detail, tc.detail)
			}
			if got.Code != tc.code {
				t.Errorf("Code = %q, want %q", got.Code, tc.code)
			}
			if got.Status != tc.status {
				t.Errorf("Status = %d, want %d", got.Status, tc.status)
			}
		})
	}
}

// TestUnit_Apierr_ShapesResolveDeterministically.
//
// The three JSON shapes are not mutually exclusive, so a body carrying fields from two of them
// has to resolve the same way every time rather than by map iteration order. Tried in the same
// order the SDK tries them.
func TestUnit_Apierr_ShapesResolveDeterministically(t *testing.T) {
	t.Parallel()

	ambiguous := `{"title":"a problem","errorMessage":"a legacy message","error":"invalid_token"}`

	first := Classify(400, []byte(ambiguous))

	for range 20 {
		if got := Classify(400, []byte(ambiguous)); got.Envelope != first.Envelope {
			t.Fatalf("classification is unstable: %q then %q", first.Envelope, got.Envelope)
		}
	}

	// Problem details win, matching the SDK's order.
	if first.Envelope != EnvelopeProblem {
		t.Errorf("Envelope = %q, want problem to take precedence", first.Envelope)
	}
}

// TestUnit_Apierr_Names is the distinction between an Observed fact and an Inferred one.
//
// A 4xx that names the field is strong evidence about that field; a bare 400 is evidence that
// something was wrong, which is a far weaker claim and must not be recorded as the stronger
// one.
func TestUnit_Apierr_Names(t *testing.T) {
	t.Parallel()

	named := Classify(400, []byte(`{"title":"cannot modify","detail":"objectType"}`))

	if !named.Names("objectType") {
		t.Error("a detail naming the field should be recognised")
	}
	// Case-insensitive, because APIs are inconsistent about it.
	if !named.Names("OBJECTTYPE") {
		t.Error("matching should be case-insensitive")
	}
	if named.Names("colour") {
		t.Error("a different field must not match")
	}
	// An empty field name must not match everything, which would silently upgrade every
	// fact to Observed.
	if named.Names("") {
		t.Error("an empty field name must not match")
	}

	// A bare error names nothing, so a probe seeing this can only claim Inferred.
	bare := Classify(400, []byte(`{"title":"There were some errors in your request."}`))
	if bare.Names("objectType") {
		t.Error("a message that does not mention the field must not match")
	}

	// The message and code are searched too, because not every API uses detail.
	inMessage := Classify(400, []byte(`{"title":"field key is immutable"}`))
	if !inMessage.Names("key") {
		t.Error("a field named in the message should be recognised")
	}
}

// TestUnit_Apierr_AuthIsNarrow pins a correctness decision worth stating.
//
// 401 means the credential was not accepted, which is unambiguous and has to stop a run.
// **403 does not**, and treating it as a dead credential would abort a run that was working
// perfectly and had merely asked about an object it could not see -- while discarding the
// observation that the API hides absence behind 403, which is itself a fact.
func TestUnit_Apierr_AuthIsNarrow(t *testing.T) {
	t.Parallel()

	if !Classify(401, nil).IsAuth() {
		t.Error("401 is a credential failure")
	}
	if !Classify(200, []byte(`{"error":"invalid_token"}`)).IsAuth() {
		t.Error("the OAuth envelope is a credential failure whatever the status")
	}

	forbidden := Classify(403, nil)
	if forbidden.IsAuth() {
		t.Error("403 must not be treated as a credential failure: many APIs use it for " +
			"per-object authorisation, and aborting on it would discard a real observation")
	}
	if !forbidden.IsForbidden() {
		t.Error("403 should be reported as forbidden")
	}

	if !Classify(429, nil).IsRateLimit() {
		t.Error("429 is a rate limit")
	}
	if !Classify(404, nil).IsNotFound() {
		t.Error("404 is absence")
	}
	if Classify(400, nil).IsAuth() || Classify(400, nil).IsRateLimit() {
		t.Error("400 is none of those")
	}
}

func TestUnit_Apierr_DefaultMessages(t *testing.T) {
	t.Parallel()

	// A status with no body still has to describe itself, or a report says only "error".
	tests := map[int]string{
		401: "unauthorised",
		403: "forbidden",
		404: "not found",
		429: "rate limited",
		500: "no error body",
	}

	for status, want := range tests {
		if got := Classify(status, nil).Message; got != want {
			t.Errorf("Classify(%d).Message = %q, want %q", status, got, want)
		}
	}
}

func TestUnit_Apierr_String(t *testing.T) {
	t.Parallel()

	got := Classify(422, []byte(`{"title":"cannot modify","detail":"key"}`)).String()
	for _, want := range []string{"cannot modify", "key"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}

	// A detail identical to the message must not be repeated.
	same := Classify(400, []byte(`{"title":"bad","detail":"bad"}`)).String()
	if strings.Count(same, "bad") != 1 {
		t.Errorf("String() = %q, want the duplicate detail suppressed", same)
	}

	// And something with nothing to say still says something.
	if got := (Error{}).String(); got != "error" {
		t.Errorf("String() on an empty error = %q, want \"error\"", got)
	}
}

// TestUnit_Apierr_NamesMatchesWholeWordsOnly.
//
// Two failures a live run found, in opposite directions.
//
// Under-claiming: "Invalid Access Type: qqqqqq" names accessType, and a substring test misses it
// because the API spells the field with a space and a capital. Every refusal of that shape went
// uncounted, so a value set the API demonstrably enforces was reported as unprobed.
//
// Over-claiming: "id" is a substring of "invalid", so any 400 whose message contained the word
// invalid read as naming the resource's identifier -- on the commonest field name there is.
func TestUnit_Apierr_NamesMatchesWholeWordsOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		detail string
		field  string
		want   bool
	}{
		{"prose with a space and a capital", "Invalid Access Type: qqqqqq", "accessType", true},
		{"the API's own spelling", "Invalid Object Type: zzz", "objectType", true},
		{"snake case in the message", "access_type is invalid", "accessType", true},
		{"exact", "objectType must be set", "objectType", true},
		{"an unsplittable field name", "objectType must be set", "OBJECTTYPE", true},

		{"id is not inside invalid", "the request is invalid", "id", false},
		{"a different field", "accessType is invalid", "objectType", false},
		{"a word that merely starts the same", "keyboard is wrong", "key", false},
		{"nothing at all", "", "objectType", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := Classify(400, []byte(`{"title":"bad request","detail":`+quote(tc.detail)+`}`))

			if got := e.Names(tc.field); got != tc.want {
				t.Errorf("Names(%q) against %q = %v, want %v", tc.field, tc.detail, got, tc.want)
			}
		})
	}
}

// quote renders a string as a JSON string literal.
func quote(s string) string {
	out, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(out)
}
