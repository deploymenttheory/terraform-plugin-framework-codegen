package cassette

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// redactionPrefix marks a substituted value. The structure is preserved -- a redacted
// string is still a string -- so a replay can still match on shape even where it cannot
// match on value.
const redactionPrefix = "<REDACTED:"

// neverCaptured are the headers the recording transport must not populate at all.
//
// Not "redacted": never captured. A token cannot leak from a field that was never
// filled, and that distinction is the difference between one bug away from a leak and
// structurally unable to leak.
var neverCaptured = map[string]bool{
	"authorisation":       true,
	"proxy-authorisation": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"api-key":             true,
}

// Redactor substitutes secrets out of recorded traffic, and refuses to let any through.
type Redactor struct {
	// literals maps a secret value to the name it is replaced with. Longest first when
	// applied, so a token that contains a shorter secret as a substring is replaced
	// whole rather than leaving a fragment behind.
	literals []literal

	// patterns are named regexes from the profile.
	patterns []pattern
}

type literal struct {
	value string
	name  string
}

type pattern struct {
	re   *regexp.Regexp
	name string
}

// NewRedactor builds a redactor from literal values and named patterns.
//
// values maps a name to the secret; a value shorter than minSecretLength is refused
// rather than accepted, because substituting a two-character string out of every body
// would corrupt the transcript far more thoroughly than it would protect anything.
func NewRedactor(values map[string]string, patterns map[string]string) (*Redactor, error) {
	r := &Redactor{}

	for name, v := range values {
		if v == "" {
			continue
		}
		if len(v) < minSecretLength {
			return nil, fmt.Errorf("%w: the value for %q is %d characters; "+
				"substituting something that short would corrupt the transcript",
				ErrInvalidCassette, name, len(v))
		}
		r.literals = append(r.literals, literal{value: v, name: name})
	}

	// Longest first, so a value containing another is replaced whole.
	sort.Slice(r.literals, func(i, j int) bool {
		return len(r.literals[i].value) > len(r.literals[j].value)
	})

	names := make([]string, 0, len(patterns))
	for name := range patterns {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		re, err := regexp.Compile(patterns[name])
		if err != nil {
			return nil, fmt.Errorf("%w: pattern %q: %w", ErrInvalidCassette, name, err)
		}
		r.patterns = append(r.patterns, pattern{re: re, name: name})
	}

	return r, nil
}

// minSecretLength is the shortest value worth substituting.
const minSecretLength = 8

// Apply substitutes secrets throughout a value, at any depth.
//
// Recurses through maps and slices rather than operating on the serialised form, so that
// a token nested six levels down in a response is caught -- and so that the result is
// still valid JSON of the same shape. FuzzCassette_Redaction plants a secret at a random
// depth precisely because this traversal is where a miss would hide.
func (r *Redactor) Apply(v any) any {
	switch t := v.(type) {
	case string:
		return r.applyString(t)

	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			// Keys as well as values: an API that echoes a token as an object key is
			// perverse but not impossible, and the cost of checking is nothing.
			out[r.applyString(k)] = r.Apply(val)
		}
		return out

	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = r.Apply(val)
		}
		return out

	default:
		// Numbers, bools and nil cannot carry a secret.
		return v
	}
}

func (r *Redactor) applyString(s string) string {
	for _, l := range r.literals {
		s = strings.ReplaceAll(s, l.value, redactionPrefix+l.name+">")
	}
	for _, p := range r.patterns {
		s = p.re.ReplaceAllString(s, redactionPrefix+p.name+">")
	}
	return s
}

// ApplyToHeaders substitutes secrets in a header map.
func (r *Redactor) ApplyToHeaders(h map[string]string) map[string]string {
	if len(h) == 0 {
		return h
	}

	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = r.applyString(v)
	}

	return out
}

// ApplyToQuery substitutes secrets in a query map.
func (r *Redactor) ApplyToQuery(q map[string][]string) map[string][]string {
	if len(q) == 0 {
		return q
	}

	out := make(map[string][]string, len(q))
	for k, vs := range q {
		redacted := make([]string, len(vs))
		for i, v := range vs {
			redacted[i] = r.applyString(v)
		}
		out[k] = redacted
	}

	return out
}

// secretShapes are the patterns that mean "this looks like a credential" regardless of
// whether anybody declared it.
//
// The point of scanning for shapes as well as declared literals is that the declared list
// is always incomplete: an API that returns a session token nobody knew about would
// otherwise be committed verbatim. These are deliberately specific enough not to fire on
// ordinary content -- a bare base64-looking run is included, but only at 32 characters or
// more, which ordinary prose does not reach.
var secretShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"jwt", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{"pem", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"slack-token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"aws-access-key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b`)},
	{"bearer-literal", regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{20,}`)},
	{"long-base64ish", regexp.MustCompile(`\b[A-Za-z0-9+/]{40,}={0,2}\b`)},
}

// minDistinctChars is how varied a long run has to be before it counts as secret-shaped.
//
// The long-base64ish pattern is the one heuristic here with no structure to anchor on, so
// it needs a second condition or it fires on any long alphanumeric run. FuzzCassette_Redaction
// found the case: forty zeroes matched, which would refuse a recording over a padded
// identifier or a column of repeated digits.
//
// A real encoded secret draws on most of its alphabet; a padded field or a repeated
// placeholder does not. Sixteen distinct characters in a forty-character run is far below
// what base64 of random bytes produces and far above what padding does.
const minDistinctChars = 16

// base64ishSecret decides whether an unanchored long run is worth refusing a
// recording over.
//
// Slash is in the base64 alphabet, so a URL path is a single long "run" -- the
// live case was a hypermedia _links.self.href, "v7/dashboards/filters/<24-hex
// id>", refused as a secret when every piece of it is public addressing. A path
// is only worrying if some single slash-free segment is itself secret-length and
// secret-varied; a real base64 credential embedded in a path still trips that,
// while segments of ordinary words and object ids never reach forty characters.
func base64ishSecret(s string) bool {
	if !strings.Contains(s, "/") {
		return looksHighEntropy(s)
	}

	for _, segment := range strings.Split(s, "/") {
		if len(segment) >= 40 && looksHighEntropy(segment) {
			return true
		}
	}

	return false
}

// looksHighEntropy reports whether a matched run is varied enough to be a credential.
func looksHighEntropy(s string) bool {
	seen := map[rune]struct{}{}
	for _, r := range s {
		seen[r] = struct{}{}
		if len(seen) >= minDistinctChars {
			return true
		}
	}
	return false
}

// Leak is one thing the scanner objected to.
type Leak struct {
	// Interaction is the id the secret was found in, empty when scanning loose bytes.
	Interaction string
	// Shape names what matched: a declared secret's name, or a secret-shaped pattern.
	Shape string
	// Pointer locates it well enough to fix, without quoting it.
	Pointer string
}

func (f Leak) String() string {
	at := f.Pointer
	if f.Interaction != "" {
		at = f.Interaction + " " + at
	}
	// Deliberately never includes the matched text. A failure report that quoted the
	// secret would put it in a CI log, which is the problem it exists to prevent.
	return fmt.Sprintf("%s: matched %s", at, f.Shape)
}

// Scan checks serialised bytes for anything secret-shaped.
//
// Run over the fully serialised form immediately before writing, which is what makes the
// guarantee hold: it does not matter which layer above failed to substitute, or whether
// the secret arrived somewhere nobody thought to look. If it is in the bytes, it is
// found, and nothing is written.
//
// extraSecrets are values that must not appear whatever their shape -- the token itself,
// and the value of every environment variable the profile names.
func Scan(interaction string, data []byte, extraSecrets map[string]string) []Leak {
	var findings []Leak

	text := string(data)

	names := make([]string, 0, len(extraSecrets))
	for name := range extraSecrets {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		secret := extraSecrets[name]
		if len(secret) < minSecretLength {
			continue
		}
		if strings.Contains(text, secret) {
			findings = append(findings, Leak{
				Interaction: interaction,
				Shape:       "declared secret " + name,
				Pointer:     "somewhere in the serialised interaction",
			})
		}
	}

	for _, shape := range secretShapes {
		loc := shape.re.FindStringIndex(text)
		if loc == nil {
			continue
		}

		// The unanchored heuristic gets a second condition; the structured patterns -- a
		// JWT header, a PEM banner, an AKIA prefix -- are specific enough on their own.
		if shape.name == "long-base64ish" && !base64ishSecret(text[loc[0]:loc[1]]) {
			continue
		}

		findings = append(findings, Leak{
			Interaction: interaction,
			Shape:       shape.name,
			Pointer:     fmt.Sprintf("at byte offset %d", loc[0]),
		})
	}

	return findings
}

// ScanInteraction serialises an interaction and scans it.
func ScanInteraction(i Interaction, extraSecrets map[string]string) ([]Leak, error) {
	data, err := json.Marshal(i)
	if err != nil {
		return nil, fmt.Errorf("%w: serialising interaction %s: %w", ErrInvalidCassette, i.ID, err)
	}

	return Scan(i.ID, data, extraSecrets), nil
}

// LeaksError turns findings into an error carrying ErrSecretFound.
func LeaksError(findings []Leak) error {
	if len(findings) == 0 {
		return nil
	}

	msg := fmt.Sprintf("%d finding(s):", len(findings))
	for _, f := range findings {
		msg += "\n  " + f.String()
	}

	return fmt.Errorf("%w: %s", ErrSecretFound, msg)
}
