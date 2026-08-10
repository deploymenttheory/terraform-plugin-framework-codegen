package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// MaxFragmentBytes is the ceiling on one stored excerpt fragment. Excerpts
// are proof for a human, not a capture: anything larger is replaced with a
// marker rather than truncated, because a truncated JSON document is not a
// document.
const MaxFragmentBytes = 2048

// Excerpt is one redacted request/response fragment supporting an
// observation. It names the operation by template, never by concrete URL,
// and carries only the body fragments the finding was read from — enough
// for a reviewer to see why, deliberately not enough to replay.
type Excerpt struct {
	Method       string `json:"method"`
	PathTemplate string `json:"pathTemplate"`
	Status       int    `json:"status"`
	// RequestFragment and ResponseFragment are JSON fragments, at most
	// MaxFragmentBytes each, already redacted.
	RequestFragment  json.RawMessage `json:"requestFragment,omitempty"`
	ResponseFragment json.RawMessage `json:"responseFragment,omitempty"`
}

// validate checks the shape Write will commit: valid JSON fragments within
// the size ceiling.
func (e *Excerpt) validate() error {
	if e.Method == "" {
		return fmt.Errorf("an excerpt with no method")
	}
	for name, frag := range map[string]json.RawMessage{
		"requestFragment":  e.RequestFragment,
		"responseFragment": e.ResponseFragment,
	} {
		if len(frag) == 0 {
			continue
		}
		if !json.Valid(frag) {
			return fmt.Errorf("%s is not valid JSON", name)
		}
		if len(frag) > MaxFragmentBytes {
			return fmt.Errorf("%s is %d bytes, over the %d-byte ceiling", name, len(frag), MaxFragmentBytes)
		}
	}
	return nil
}

// redacted is what a removed value reads as.
const redacted = "[redacted]"

// sensitiveKeyParts marks a JSON key whose value is removed wholesale,
// whatever it holds — the Authorization-style headers and their relatives.
// Matching is by lower-cased substring, so "X-Api-Key", "sessionToken" and
// "client_secret" all hit without an exhaustive list.
var sensitiveKeyParts = []string{
	"authorization", "cookie", "token", "secret", "password",
	"apikey", "api-key", "api_key", "x-auth", "credential",
	"private", "session", "bearer", "signature",
}

// Redact removes every secret from an excerpt before it can be stored.
//
// It replaces each occurrence of a secret value in the fragments, the
// method and the path template; removes the values of Authorization-style
// keys wholesale; and then fails closed: a fragment that cannot be parsed
// as JSON, or that still contains a secret in any encoding after
// substitution — a numeric secret, an escaped one — is replaced entirely.
// The guarantee is one-directional by construction: content may be lost,
// a secret may not survive.
func Redact(e Excerpt, secrets []string) Excerpt {
	live := make([]string, 0, len(secrets))
	for _, s := range secrets {
		// An empty secret matches everything and redacts nothing.
		if s != "" {
			live = append(live, s)
		}
	}
	e.Method = redactString(e.Method, live)
	e.PathTemplate = redactString(e.PathTemplate, live)
	e.RequestFragment = redactFragment(e.RequestFragment, live)
	e.ResponseFragment = redactFragment(e.ResponseFragment, live)
	return e
}

// redactFragment rewrites one JSON fragment, failing closed to a marker
// whenever it cannot prove the result is clean.
func redactFragment(raw json.RawMessage, secrets []string) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// Unparseable content cannot be searched for secrets, so none of
		// it survives.
		return marker("not valid JSON")
	}
	out, err := json.Marshal(redactValue(v, secrets))
	if err != nil {
		return marker("unencodable")
	}
	if containsSecret(out, secrets) {
		// A secret survived substitution — through a number, an escape
		// sequence, a spelling the walk did not reach. Drop everything.
		return marker("fragment withheld")
	}
	if len(out) > MaxFragmentBytes {
		return marker(fmt.Sprintf("fragment over %d bytes", MaxFragmentBytes))
	}
	return out
}

// marker is the JSON string a withheld fragment is replaced with.
func marker(why string) json.RawMessage {
	out, _ := json.Marshal(redacted + ": " + why)
	return out
}

// redactValue walks a decoded JSON value, substituting secrets in strings
// and keys and removing sensitive keys' values wholesale.
func redactValue(v any, secrets []string) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if sensitiveKey(k) {
				out[redactString(k, secrets)] = redacted
				continue
			}
			out[redactString(k, secrets)] = redactValue(val, secrets)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactValue(val, secrets)
		}
		return out
	case string:
		return redactString(t, secrets)
	default:
		// Numbers, booleans, null: nothing to substitute here — the
		// containsSecret sweep catches a secret that renders numerically.
		return t
	}
}

func redactString(s string, secrets []string) string {
	for _, secret := range secrets {
		s = strings.ReplaceAll(s, secret, redacted)
	}
	return s
}

func sensitiveKey(k string) bool {
	lk := strings.ToLower(k)
	for _, part := range sensitiveKeyParts {
		if strings.Contains(lk, part) {
			return true
		}
	}
	return false
}

// containsSecret reports whether encoded output still carries any secret,
// checking both the raw spelling and its JSON string encoding — a secret
// containing a quote or a non-ASCII rune is escaped in output and would
// dodge a raw comparison.
func containsSecret(out []byte, secrets []string) bool {
	for _, secret := range secrets {
		if bytes.Contains(out, []byte(secret)) {
			return true
		}
		if enc, err := json.Marshal(secret); err == nil {
			if trimmed := bytes.Trim(enc, `"`); len(trimmed) > 0 && bytes.Contains(out, trimmed) {
				return true
			}
		}
	}
	return false
}
