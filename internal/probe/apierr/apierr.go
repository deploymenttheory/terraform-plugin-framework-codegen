// Package apierr classifies API error responses.
//
// Real APIs routinely use more than one error shape, and a single endpoint family can use
// several: an RFC 7807 body for validation, an OAuth pair for a bad token, a bespoke form for
// absent credentials, and no body at all for a 404. The four handled here were each observed
// on a live API during development, and the set is deliberately open -- an unrecognised shape
// classifies as EnvelopeUnknown rather than being forced into one of the others.
//
// It is load-bearing rather than convenience. Every mutating protocol depends on telling
// three failures apart:
//
//   - "the field is immutable", which is the fact being sought
//   - "the token expired", which invalidates the whole run
//   - "some unrelated required field was missing", which means the request shape is wrong
//     and every conclusion drawn from the response is an artefact
//
// A prober that saw only a status code could not distinguish those, and would report the
// first explanation as though the others had been ruled out. Worse, it would do so
// confidently: a 400 is a 400 whatever caused it.
//
// # Why this is ported rather than imported
//
// internal/probe speaks raw net/http and depends on no provider SDK -- see that package's doc
// comment. Borrowing a classifier from one would drag that SDK's transport, logging and
// tracing dependencies into a module whose whole graph is a handful of packages, and would
// couple the toolkit to the SDK it is meant to generate against.
package apierr

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Envelope names the shape an error body took.
type Envelope string

const (
	// EnvelopeProblem is RFC 7807 application/problem+json, commonly used for validation
	// failures.
	EnvelopeProblem Envelope = "problem"
	// EnvelopeOAuth is {"error","error_description"}, returned when a bearer token is
	// present but invalid.
	EnvelopeOAuth Envelope = "oauth"
	// EnvelopeLegacy is {"errorMessage"}, returned when no credentials are supplied.
	EnvelopeLegacy Envelope = "legacy"
	// EnvelopeEmpty is no body at all, which is what a 404 answers with.
	EnvelopeEmpty Envelope = "empty"
	// EnvelopeUnknown is a body in none of the recognised shapes -- usually an HTML error
	// page from a proxy rather than the API itself, which is worth knowing.
	EnvelopeUnknown Envelope = "unknown"
)

// Error is a classified error response.
type Error struct {
	Status int
	// Envelope is which shape the body took.
	Envelope Envelope
	// Message is the human-readable summary, whichever field carried it.
	Message string
	// Detail is the more specific explanation where one was given. This is the field that
	// usually names the offending attribute.
	Detail string
	// Code is a machine-readable type, where the envelope carried one that means anything.
	Code string
}

// problem is the RFC 7807 body.
type problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

// oauth is the body for an invalid token.
//
// The snake_case tag is the wire format, fixed by RFC 6749, so tagliatelle's camelCase default
// does not apply: renaming it would simply stop the field decoding.
type oauth struct {
	Error string `json:"error"`
	//nolint:tagliatelle // RFC 6749 names this field; it is not ours to rename.
	Description string `json:"error_description"`
}

// legacy is the body for absent credentials.
type legacy struct {
	ErrorMessage string `json:"errorMessage"`
}

// Classify identifies an error response.
//
// Order matters here: the shapes are not mutually
// exclusive as JSON, so a body carrying both `title` and `errorMessage` has to resolve
// deterministically rather than by map iteration.
func Classify(status int, body []byte) Error {
	out := Error{Status: status, Envelope: EnvelopeUnknown}

	if len(strings.TrimSpace(string(body))) == 0 {
		out.Envelope = EnvelopeEmpty
		out.Message = defaultMessage(status)
		return out
	}

	var p problem
	if err := json.Unmarshal(body, &p); err == nil && p.Title != "" {
		out.Envelope = EnvelopeProblem
		out.Message = p.Title
		out.Detail = p.Detail
		// "about:blank" is RFC 7807's way of saying "no specific type", so it carries
		// nothing worth surfacing.
		if p.Type != "" && p.Type != "about:blank" {
			out.Code = p.Type
		}
		return out
	}

	var o oauth
	if err := json.Unmarshal(body, &o); err == nil && o.Error != "" {
		out.Envelope = EnvelopeOAuth
		out.Message = o.Description
		if out.Message == "" {
			out.Message = o.Error
		}
		out.Code = o.Error
		return out
	}

	var l legacy
	if err := json.Unmarshal(body, &l); err == nil && l.ErrorMessage != "" {
		out.Envelope = EnvelopeLegacy
		out.Message = l.ErrorMessage
		return out
	}

	out.Message = strings.TrimSpace(string(body))

	return out
}

// defaultMessage describes a status that came with no body.
func defaultMessage(status int) string {
	switch status {
	case 401:
		return "unauthorised"
	case 403:
		return "forbidden"
	case 404:
		return "not found"
	case 429:
		return "rate limited"
	default:
		return "no error body"
	}
}

// Names reports whether this error appears to be about a named field.
//
// This is the distinction between an Observed fact and an Inferred one. A 4xx that names
// the field is strong evidence about that field; a bare 400 is evidence that *something*
// was wrong, which is a much weaker claim and must not be recorded as though it were the
// stronger one.
//
// Matched as a sequence of whole words rather than as a substring, across the detail, message and
// code. Two things follow, and a live run needed both.
//
// A field named in prose is recognised: "Invalid Access Type: qqqqqq" names accessType, and a
// substring test misses it because the API spells the field with a space and a capital. Every
// refusal of that shape was going uncounted, so a value set the API demonstrably enforces was
// being reported as unprobed.
//
// And "id" no longer matches "invalid". A substring test found it inside val-id, so any 400 whose
// message contained the word "invalid" was read as naming the resource's identifier -- a false
// positive on the commonest field name there is, in the direction that over-claims.
func (e Error) Names(field string) bool {
	if field == "" {
		return false
	}

	want := words(field)
	if len(want) == 0 {
		return false
	}

	for _, haystack := range []string{e.Detail, e.Message, e.Code} {
		if containsWords(words(haystack), want) {
			return true
		}
	}

	return false
}

// words splits a string into lower-case alphanumeric words, breaking on camelCase humps as well as
// on punctuation. So "accessType", "access_type" and "Access Type" all yield the same two words.
func words(s string) []string {
	var (
		out     []string
		current strings.Builder
	)

	flush := func() {
		if current.Len() > 0 {
			out = append(out, strings.ToLower(current.String()))
			current.Reset()
		}
	}

	runes := []rune(s)

	for i, r := range runes {
		switch {
		case unicode.IsUpper(r):
			// A hump starts a new word, unless it is part of a run of capitals like "ID" or
			// "URL" -- splitting those into single letters would match almost anything.
			prevLower := i > 0 && unicode.IsLower(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])

			if prevLower || (nextLower && current.Len() > 1) {
				flush()
			}

			current.WriteRune(r)

		case unicode.IsLetter(r) || unicode.IsDigit(r):
			current.WriteRune(r)

		default:
			flush()
		}
	}

	flush()

	return out
}

// containsWords reports whether want appears in haystack as a run of consecutive whole words.
//
// Compared as *joined* runs rather than word by word, which is what makes the test independent of
// how either side spells the boundaries. "accessType" and "Access Type" both reduce to the two
// words access+type, and "ACCESSTYPE" reduces to the single word accesstype -- joining the run puts
// all three on the same footing.
//
// And because the runs are whole words, "id" still does not match "invalid": the only candidate
// runs are complete words, so nothing inside one can match.
func containsWords(haystack, want []string) bool {
	if len(want) == 0 || len(haystack) == 0 {
		return false
	}

	needle := strings.Join(want, "")

	for i := range haystack {
		var run strings.Builder

		for _, w := range haystack[i:] {
			run.WriteString(w)

			if run.Len() > len(needle) {
				break
			}
			if run.String() == needle {
				return true
			}
		}
	}

	return false
}

// IsAuth reports whether the credential itself is the problem.
//
// A run that hits one has to stop rather than record facts: every subsequent response would
// be about the token, and a probe that carried on would attribute an authentication failure to
// whichever field it happened to be testing.
//
// **401 and the OAuth envelope only, deliberately not 403.** A 401 means the credential was
// not accepted, which is unambiguous. A 403 means this request was not permitted, which is a
// different claim: many APIs use it for per-object authorisation, and some answer 403 for an
// object that belongs to a tenant the credential cannot see. Treating every
// 403 as a dead credential would abort a run that was working perfectly and had just asked
// about something it could not see -- and it would discard the observation that the API hides
// absence behind 403, which is itself a fact worth recording.
func (e Error) IsAuth() bool {
	return e.Status == 401 || e.Envelope == EnvelopeOAuth
}

// IsForbidden reports a request that was understood and refused.
//
// Kept separate from IsAuth because the two need different handling: a probe may legitimately
// record a 403 as an observation about how the API reports absence, while a 401 has to stop
// the run.
func (e Error) IsForbidden() bool { return e.Status == 403 }

// IsRateLimit reports whether the API asked us to slow down.
func (e Error) IsRateLimit() bool { return e.Status == 429 }

// IsNotFound reports absence.
func (e Error) IsNotFound() bool { return e.Status == 404 }

func (e Error) String() string {
	var b strings.Builder

	b.WriteString(defaultIfEmpty(e.Message, "error"))
	if e.Detail != "" && e.Detail != e.Message {
		b.WriteString(": ")
		b.WriteString(e.Detail)
	}

	return b.String()
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
