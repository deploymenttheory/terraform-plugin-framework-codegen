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
// Matching is deliberately conservative -- substring, case-insensitive, across the detail,
// message and code. An API that names the field in a form this does not recognise
// downgrades a fact to Inferred, which errs toward under-claiming.
func (e Error) Names(field string) bool {
	if field == "" {
		return false
	}

	needle := strings.ToLower(field)

	for _, haystack := range []string{e.Detail, e.Message, e.Code} {
		if haystack != "" && strings.Contains(strings.ToLower(haystack), needle) {
			return true
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
