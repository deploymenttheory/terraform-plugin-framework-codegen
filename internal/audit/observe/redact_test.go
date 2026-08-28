package observe

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// mustNotContain is the fail-closed property itself: after redaction, the
// excerpt's complete encoded form carries no trace of the secret.
func mustNotContain(t *testing.T, e Excerpt, secret string) {
	t.Helper()
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshalling the redacted excerpt: %v", err)
	}
	if bytes.Contains(out, []byte(secret)) {
		t.Fatalf("secret %q survived redaction: %s", secret, out)
	}
	if enc, _ := json.Marshal(secret); bytes.Contains(out, bytes.Trim(enc, `"`)) {
		t.Fatalf("secret %q survived redaction in escaped form: %s", secret, out)
	}
}

func TestUnit_Observe_RedactFailsClosed(t *testing.T) {
	const secret = "s3cr3t-value-9000"
	cases := []struct {
		name    string
		excerpt Excerpt
	}{
		{"secret as a value", Excerpt{
			Method: "POST", PathTemplate: "/things",
			RequestFragment: []byte(`{"token_field":"x","note":"` + secret + `"}`),
		}},
		{"secret inside a longer string", Excerpt{
			Method:           "POST",
			ResponseFragment: []byte(`{"note":"prefix ` + secret + ` suffix"}`),
		}},
		{"secret nested deep", Excerpt{
			Method:          "POST",
			RequestFragment: []byte(`{"a":{"b":[{"c":"` + secret + `"}]}}`),
		}},
		{"secret as a key", Excerpt{
			Method:          "POST",
			RequestFragment: []byte(`{"` + secret + `":"v"}`),
		}},
		{"secret as a number", Excerpt{
			Method:           "POST",
			ResponseFragment: []byte(`{"pin":900190019001}`),
		}},
		{"unparseable fragment", Excerpt{
			Method:          "POST",
			RequestFragment: []byte(`Bearer ` + secret + ` and no JSON`),
		}},
		{"secret in the path template", Excerpt{
			Method: "GET", PathTemplate: "/things/" + secret,
		}},
		{"secret with JSON-escaping characters", Excerpt{
			Method:          "POST",
			RequestFragment: []byte(`{"note":"has \"` + secret + `\" quoted"}`),
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sec := secret
			if testCase.name == "secret as a number" {
				sec = "900190019001"
			}
			if testCase.name == "secret with JSON-escaping characters" {
				sec = `"` + secret + `"`
			}
			got := Redact(testCase.excerpt, []string{sec, ""})
			mustNotContain(t, got, sec)
			mustNotContain(t, got, secret)
		})
	}
}

func TestUnit_Observe_RedactRemovesAuthorizationStyleValuesUnprompted(t *testing.T) {
	// No secrets list at all: header-shaped keys lose their values anyway.
	e := Excerpt{
		Method: "POST", PathTemplate: "/things", Status: 200,
		RequestFragment: []byte(`{
			"headers": {
				"Authorization": "Bearer eyJhbGciOi",
				"X-Api-Key": "key-12345",
				"Cookie": "session=abcdef",
				"Content-Type": "application/json"
			},
			"client_secret": "shhh",
			"sessionToken": "tok-1",
			"privateKey": "-----BEGIN",
			"password": "hunter2",
			"name": "kept"
		}`),
	}
	got := Redact(e, nil)
	for _, leaked := range []string{"eyJhbGciOi", "key-12345", "session=abcdef", "shhh", "tok-1", "-----BEGIN", "hunter2"} {
		mustNotContain(t, got, leaked)
	}
	if !bytes.Contains(got.RequestFragment, []byte(`"name":"kept"`)) {
		t.Errorf("non-sensitive content did not survive: %s", got.RequestFragment)
	}
	if !bytes.Contains(got.RequestFragment, []byte("application/json")) {
		t.Errorf("Content-Type value should survive: %s", got.RequestFragment)
	}
}

func TestUnit_Observe_RedactBoundsAndPreservesFragments(t *testing.T) {
	// An oversize fragment is withheld, not truncated.
	big := `{"filler":"` + strings.Repeat("a", MaxFragmentBytes) + `"}`
	got := Redact(Excerpt{Method: "GET", ResponseFragment: []byte(big)}, nil)
	if len(got.ResponseFragment) > MaxFragmentBytes || bytes.Contains(got.ResponseFragment, []byte("aaaa")) {
		t.Fatalf("oversize fragment survived: %d bytes", len(got.ResponseFragment))
	}
	if !json.Valid(got.ResponseFragment) {
		t.Fatal("the withheld marker is not valid JSON")
	}

	// A clean fragment passes through intact, arrays and scalars included.
	clean := Excerpt{
		Method: "GET", PathTemplate: "/tags/{tagId}", Status: 200,
		ResponseFragment: []byte(`{"tags":["a","b"],"count":2,"open":true,"next":null}`),
	}
	got = Redact(clean, []string{"nowhere"})
	var v map[string]any
	if err := json.Unmarshal(got.ResponseFragment, &v); err != nil {
		t.Fatalf("clean fragment mangled: %v", err)
	}
	if v["count"] != float64(2) || v["open"] != true || v["next"] != nil {
		t.Errorf("scalars did not survive: %v", v)
	}
	if got.Method != "GET" || got.PathTemplate != "/tags/{tagId}" || got.Status != 200 {
		t.Errorf("metadata did not survive: %+v", got)
	}

	// Empty fragments stay empty.
	got = Redact(Excerpt{Method: "DELETE"}, []string{"x"})
	if got.RequestFragment != nil || got.ResponseFragment != nil {
		t.Errorf("empty fragments grew content: %+v", got)
	}

	// The redacted result is storable: it passes excerpt validation.
	o := valid()
	o.Excerpts = []Excerpt{Redact(Excerpt{
		Method: "POST", PathTemplate: "/things",
		RequestFragment: []byte(`not json at all`),
	}, []string{"whatever"})}
	if err := o.Validate(); err != nil {
		t.Fatalf("a redacted excerpt failed validation: %v", err)
	}
}
