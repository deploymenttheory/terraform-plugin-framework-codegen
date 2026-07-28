package cassette

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testToken is a fabricated value shaped like a real bearer token.
//
// Deliberately not a real credential, and not the sandbox one: a test fixture is committed,
// and a committed secret is the precise failure this package exists to prevent. Anything of
// the right shape exercises the code identically.
const testToken = "00zz-11111111-2222-3333-4444-555555555555"

// TestUnit_Cassette_RedactionReachesEveryDepth.
//
// The traversal is where a miss would hide: a token six levels down in a response, or used
// as an object key, is exactly the case a scrubber written against a flat map gets wrong.
// TestUnit_Cassette_NeverCapturedBeatsTheAllowList.
//
// The "never captured" guarantee has to be enforced by its own code path rather than by the
// allow list happening to omit those headers. An allow list is a thing people add entries
// to, and "we need to see which auth scheme the API wants" is a plausible reason to add
// authorization to one. This asserts that even then the value does not reach a cassette.
func TestUnit_Cassette_NeverCapturedBeatsTheAllowList(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Authorization", "Bearer "+testToken)
	h.Set("Cookie", "session=abc")
	h.Set("Content-Type", "application/json")

	// An allow list that has been widened to include the credential headers, which is the
	// mistake being defended against.
	widened := map[string]bool{
		"authorization": true,
		"cookie":        true,
		"content-type":  true,
	}

	got, dropped := canonicalHeaders(h, widened)

	for _, forbidden := range []string{"authorization", "cookie"} {
		if _, present := got[forbidden]; present {
			t.Errorf("%s was captured despite being on the never-captured list: %v", forbidden, got)
		}
	}
	if got["content-type"] != "application/json" {
		t.Errorf("an ordinary allowed header was lost: %v", got)
	}
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2", dropped)
	}
}

func TestUnit_Cassette_RedactionReachesEveryDepth(t *testing.T) {
	t.Parallel()

	r := newRedactor(t, map[string]string{"token": testToken})

	nested := map[string]any{
		"a": []any{
			map[string]any{
				"b": map[string]any{
					"c": []any{"prefix " + testToken + " suffix"},
				},
			},
		},
		// As a key, which is perverse but costs nothing to handle.
		testToken: "value",
		// And untouched types must come back unchanged.
		"n":    float64(42),
		"ok":   true,
		"none": nil,
	}

	out := r.Apply(nested)

	// Encoded the way a cassette file is written -- marshalCanonical turns HTML escaping
	// off. Plain json.Marshal would render the sentinel as \u003cREDACTED:token\u003e,
	// which is correct but is not what lands on disk, and asserting against the wrong
	// form would hide a real change to the committed output.
	encoded, err := marshalCanonical(out)
	if err != nil {
		t.Fatalf("marshalCanonical: %v", err)
	}

	if strings.Contains(string(encoded), testToken) {
		t.Errorf("the token survived redaction:\n%s", encoded)
	}
	if !strings.Contains(string(encoded), "<REDACTED:token>") {
		t.Errorf("no redaction sentinel was written:\n%s", encoded)
	}
	// Structure is preserved, so a replay can still match on shape.
	if !strings.Contains(string(encoded), "prefix ") || !strings.Contains(string(encoded), " suffix") {
		t.Errorf("redaction destroyed surrounding text:\n%s", encoded)
	}

	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("Apply changed the type: %T", out)
	}
	if m["n"] != float64(42) || m["ok"] != true || m["none"] != nil {
		t.Errorf("non-string values were altered: %v", m)
	}
}

// TestUnit_Cassette_RedactionPrefersTheLongestSecret.
//
// A token that contains a shorter declared secret as a substring must be replaced whole.
// Otherwise the shorter one is substituted first and leaves the rest of the token behind --
// a partial leak, which is worse than none because it looks redacted.
func TestUnit_Cassette_RedactionPrefersTheLongestSecret(t *testing.T) {
	t.Parallel()

	// "11111111" is a substring of the full token. Substituted first, it would leave the
	// rest of the token in the transcript -- a partial leak, which is worse than none
	// because it looks redacted.
	r := newRedactor(t, map[string]string{
		"fragment": "11111111",
		"token":    testToken,
	})

	got, ok := r.Apply(testToken).(string)
	if !ok {
		t.Fatalf("Apply changed the type: %T", got)
	}

	if got != "<REDACTED:token>" {
		t.Errorf("the longer secret was not replaced whole: %q", got)
	}
	for _, leftover := range []string{"2222", "3333", "555555555555"} {
		if strings.Contains(got, leftover) {
			t.Errorf("a fragment of the token survived: %q contains %q", got, leftover)
		}
	}
}

func TestUnit_Cassette_RedactorRefusesAShortSecret(t *testing.T) {
	t.Parallel()

	// Substituting a two-character string out of every body would corrupt the transcript
	// far more thoroughly than it would protect anything.
	_, err := NewRedactor(map[string]string{"tiny": "ab"}, nil)
	if !errors.Is(err, ErrInvalidCassette) {
		t.Errorf("error = %v, want ErrInvalidCassette", err)
	}

	// An empty value is simply ignored: an unset optional secret is not a mistake.
	if _, err := NewRedactor(map[string]string{"unset": ""}, nil); err != nil {
		t.Errorf("an empty secret should be ignored, got %v", err)
	}

	// A bad pattern is a configuration error worth reporting.
	if _, err := NewRedactor(nil, map[string]string{"bad": "([unclosed"}); !errors.Is(err, ErrInvalidCassette) {
		t.Errorf("error = %v, want ErrInvalidCassette", err)
	}
}

func TestUnit_Cassette_RedactionByPattern(t *testing.T) {
	t.Parallel()

	r, err := NewRedactor(nil, map[string]string{"accountGroup": `\baid=\d+\b`})
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}

	got := r.Apply("filter aid=12345 and more")
	if s, _ := got.(string); strings.Contains(s, "12345") {
		t.Errorf("the pattern did not apply: %v", got)
	}

	// And through headers and queries, which are separate code paths.
	headers := r.ApplyToHeaders(map[string]string{"location": "/tags?aid=999"})
	if strings.Contains(headers["location"], "999") {
		t.Errorf("headers were not redacted: %v", headers)
	}

	query := r.ApplyToQuery(map[string][]string{"filter": {"aid=777"}})
	if strings.Contains(query["filter"][0], "777") {
		t.Errorf("queries were not redacted: %v", query)
	}

	// Empty inputs pass through rather than allocating.
	if got := r.ApplyToHeaders(nil); got != nil {
		t.Errorf("ApplyToHeaders(nil) = %v", got)
	}
	if got := r.ApplyToQuery(nil); got != nil {
		t.Errorf("ApplyToQuery(nil) = %v", got)
	}
}

// TestUnit_Cassette_ScanFindsSecretShapes.
//
// Scanning for shapes as well as declared literals matters because the declared list is
// always incomplete: an API that returns a session token nobody knew about would otherwise
// be committed verbatim.
func TestUnit_Cassette_ScanFindsSecretShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"jwt", `{"t":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0"}`, "jwt"},
		{"pem", `{"k":"-----BEGIN RSA PRIVATE KEY-----"}`, "pem"},
		{"slack", `{"t":"xoxb-1234567890-abcdefghij"}`, "slack-token"},
		{"aws", `{"k":"AKIAIOSFODNN7EXAMPLE"}`, "aws-access-key"},
		{"github", `{"t":"ghp_abcdefghijklmnopqrstuvwxyz0123456789"}`, "github-token"},
		{"bearer", `{"h":"Bearer abcdefghijklmnopqrstuvwxyz012345"}`, "bearer-literal"},
		{"long base64ish", `{"b":"YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXowMTIzNDU2Nzg5QUJDREVG"}`, "long-base64ish"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			findings := Scan("001-get-tags", []byte(tc.body), nil)
			if len(findings) == 0 {
				t.Fatalf("nothing found in %s", tc.body)
			}

			var shapes []string
			for _, f := range findings {
				shapes = append(shapes, f.Shape)
			}
			if !containsString(shapes, tc.want) {
				t.Errorf("shapes = %v, want one to be %q", shapes, tc.want)
			}

			// A finding must never quote the secret: the report goes into a CI log, which
			// is the problem it exists to prevent.
			for _, f := range findings {
				if strings.Contains(f.String(), "eyJ") || strings.Contains(f.String(), "AKIA") {
					t.Errorf("the finding quotes the secret: %s", f)
				}
			}
		})
	}

	// Ordinary content must not fire, or the scanner becomes noise nobody heeds.
	clean := `{"id":"1","key":"probe","description":"A tag for grouping tests together."}`
	if findings := Scan("001", []byte(clean), nil); len(findings) != 0 {
		t.Errorf("ordinary content triggered %v", findings)
	}
}

// TestUnit_Cassette_ScanIgnoresLowEntropyRuns is a regression test for a false positive the
// fuzzer found.
//
// The long-base64ish heuristic has no structure to anchor on, so on its own it fires on any
// long alphanumeric run -- including forty zeroes, which is what FuzzCassette_Redaction
// produced. That would refuse a recording over a padded identifier or a column of repeated
// digits, and a fail-closed scanner that refuses legitimate traffic gets switched off.
//
// The refinement is a character-diversity floor. A real encoded secret draws on most of its
// alphabet; padding does not.
func TestUnit_Cassette_ScanIgnoresLowEntropyRuns(t *testing.T) {
	t.Parallel()

	lowEntropy := []string{
		`{"padded":"0000000000000000000000000000000000000000"}`,
		`{"repeated":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		`{"ids":"1212121212121212121212121212121212121212"}`,
	}

	for _, body := range lowEntropy {
		var shapes []string
		for _, f := range Scan("001", []byte(body), nil) {
			shapes = append(shapes, f.Shape)
		}
		if containsString(shapes, "long-base64ish") {
			t.Errorf("%s should not look secret-shaped, got %v", body, shapes)
		}
	}

	// And the refinement must not blind the heuristic to a real blob, or it has traded one
	// failure for a worse one.
	realistic := `{"blob":"dGhpcyBpcyBhIHJlYWxpc3RpYyBiYXNlNjQgYmxvYiB3aXRoIHZhcmlldHkxMjM0"}`

	var shapes []string
	for _, f := range Scan("001", []byte(realistic), nil) {
		shapes = append(shapes, f.Shape)
	}
	if !containsString(shapes, "long-base64ish") {
		t.Errorf("a varied base64 run should still be flagged, got %v", shapes)
	}

	// Directly, so the boundary is pinned rather than inferred from a body.
	if looksHighEntropy(strings.Repeat("0", 100)) {
		t.Error("a run of one repeated character is not high entropy")
	}
	if !looksHighEntropy("abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Error("a run spanning the alphabet is high entropy")
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestUnit_Cassette_ScanFindsDeclaredSecrets(t *testing.T) {
	t.Parallel()

	// A value with no distinctive shape at all: found only because it was declared.
	secrets := map[string]string{"accountGroup": "1234567890"}

	findings := Scan("001", []byte(`{"aid":"1234567890"}`), secrets)
	if len(findings) == 0 {
		t.Error("a declared secret must be found even with no recognisable shape")
	}

	// Too short to substitute safely, so it is not scanned for either -- consistent with
	// NewRedactor's refusal, rather than reporting something the redactor would not have
	// removed.
	if got := Scan("001", []byte(`{"x":"ab"}`), map[string]string{"tiny": "ab"}); len(got) != 0 {
		t.Errorf("a too-short secret should not be scanned for: %v", got)
	}
}

// TestUnit_Cassette_PlantedTokenWritesNothing is the other half of the Phase 4.2
// milestone.
//
// This is the guarantee the whole three-layer design exists for: it does not matter which
// layer failed to substitute, or that the secret arrived somewhere nobody thought to look.
// If it is in the bytes, the run fails and **no file is written at all** -- not a partial
// directory, not one interaction. A leak cannot be committed; it can only stop the build.
func TestUnit_Cassette_PlantedTokenWritesNothing(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "probe-evidence")

	// A redactor that does not know about the token, standing in for the realistic failure:
	// the operator declared the wrong value, or the API echoed a credential nobody
	// expected.
	interactions := []Interaction{{
		ID: "001-get-tags", Seq: 1,
		Request:  Request{Method: "GET", Path: "/tags"},
		Response: Response{Status: 200, Body: map[string]any{"token": testToken}},
	}}

	meta := Metadata{Provider: "thousandeyes", Resource: "tag", Host: "api.thousandeyes.com"}

	_, err := Write(root, meta, interactions, map[string]string{"bearer": testToken}, time.Unix(0, 0))
	if !errors.Is(err, ErrSecretFound) {
		t.Fatalf("error = %v, want ErrSecretFound", err)
	}

	// Nothing at all on disk. Not an empty snapshot directory, not a metadata file.
	if entries, statErr := os.ReadDir(root); statErr == nil && len(entries) > 0 {
		t.Errorf("a redaction failure wrote %d entr(ies); it must write nothing", len(entries))
	}
}

// TestUnit_Cassette_RecordingWithASecretRefusesToHandOverInteractions.
//
// The error takes precedence over the data: there is no way to obtain the interactions
// without also obtaining the error, so a caller cannot ignore it and write them anyway.
func TestUnit_Cassette_RecordingWithASecretRefusesToHandOverInteractions(t *testing.T) {
	t.Parallel()

	srv := tokenEchoServer(t)

	// The redactor knows nothing, so the scan is the only thing standing between the token
	// and the transcript.
	rec, err := NewRecordingTransport(&http.Transport{}, newRedactor(t, nil), map[string]string{"bearer": testToken})
	if err != nil {
		t.Fatalf("NewRecordingTransport: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/tags", nil) //nolint:noctx // a test fixture

	resp, err := (&http.Client{Transport: rec}).Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	got, err := rec.Interactions()
	if !errors.Is(err, ErrSecretFound) {
		t.Errorf("error = %v, want ErrSecretFound", err)
	}
	if got != nil {
		t.Error("interactions must not be returned alongside a redaction failure")
	}
}

// tokenEchoServer returns the token in its body, standing in for an API that echoes a
// credential back -- which is the realistic way a secret nobody declared ends up in a
// transcript.
func tokenEchoServer(t *testing.T) *httptest.Server {
	t.Helper()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session":"` + testToken + `"}`))
	}))
	t.Cleanup(s.Close)

	return s
}

func TestUnit_Cassette_RecordingRequiresARedactor(t *testing.T) {
	t.Parallel()

	// Recording unscrubbed traffic is not an option the API offers.
	if _, err := NewRecordingTransport(&http.Transport{}, nil, nil); !errors.Is(err, ErrInvalidCassette) {
		t.Errorf("error = %v, want ErrInvalidCassette", err)
	}
	if _, err := NewRecordingTransport(nil, newRedactor(t, nil), nil); !errors.Is(err, ErrInvalidCassette) {
		t.Errorf("error = %v, want ErrInvalidCassette", err)
	}
}

func TestUnit_Cassette_FindingsError(t *testing.T) {
	t.Parallel()

	if err := FindingsError(nil); err != nil {
		t.Errorf("no findings should give no error, got %v", err)
	}

	err := FindingsError([]Finding{
		{Interaction: "001", Shape: "jwt", Pointer: "at byte offset 12"},
		{Interaction: "002", Shape: "declared secret bearer", Pointer: "somewhere"},
	})

	if !errors.Is(err, ErrSecretFound) {
		t.Errorf("error = %v, want ErrSecretFound", err)
	}
	for _, want := range []string{"2 finding(s)", "001", "jwt", "002"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits %q: %v", want, err)
		}
	}
}
