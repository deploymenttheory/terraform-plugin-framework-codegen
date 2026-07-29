package probe

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Values sent to test whether an enum is closed.
//
// # Why they are generated rather than declared
//
// The enum protocol asks two questions: does the API reject every documented value (in which case
// the specification is stale, which is the valuable answer), and does it reject values outside the
// documented set (in which case the set is closed).
//
// The second question is easy to answer wrongly. Send "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
// to a field whose documented values are "and" and "or", and a rejection tells you nothing about
// the enum: it may well have failed a length check, a character-class check, or a parser that
// never got as far as the value set. Conclude "closed" from that and the blueprint records a
// constraint the API does not have.
//
// So a negative candidate is shaped like the documented set -- the same median length, the same
// character classes -- and differs only in *being outside it*. Then a rejection is attributable.

// negativeCandidates builds values outside a documented set but shaped like it.
//
// Deterministic, because a mutating cassette records the exact request bodies and a random
// candidate would make every recording unreplayable. Derived from the documented values rather
// than from a fixed list, so it adapts to a field whose values are UUIDs, or dotted, or numeric.
func negativeCandidates(documented []string, n int) []string {
	if len(documented) == 0 || n <= 0 {
		return nil
	}

	shape := shapeOf(documented)

	known := make(map[string]bool, len(documented))
	for _, v := range documented {
		known[strings.ToLower(v)] = true
	}

	var out []string

	// The alphabet is walked in order, so the same documented set always yields the same
	// candidates. Each attempt is a run of one letter at the shape's length, which keeps the
	// candidate unmistakably synthetic while still matching on length and class -- somebody
	// reading the tenant's audit log should be able to tell this was a probe.
	for _, letter := range "qzxjkvwy" {
		if len(out) == n {
			break
		}

		candidate := shape.render(letter)
		if candidate == "" || known[strings.ToLower(candidate)] {
			continue
		}

		out = append(out, candidate)
	}

	return out
}

// enumShape is what the documented values have in common.
type enumShape struct {
	// length is the median documented length. Median rather than mean, because one unusually
	// long member -- "connected-devices-test" beside "test" -- would drag a mean well outside
	// the range the field actually accepts.
	length int
	// hyphenated is true when most documented values contain a separator, so a candidate that
	// omitted it might be rejected by a format check rather than by the value set.
	hyphenated bool
	// upper is true when the documented values are upper-case, which some APIs enforce
	// independently of the enum.
	upper bool
	// digits is true when the documented values contain digits.
	digits bool
}

func shapeOf(documented []string) enumShape {
	lengths := make([]int, 0, len(documented))

	var hyphenated, upper, digits int

	for _, v := range documented {
		lengths = append(lengths, len(v))

		if strings.ContainsAny(v, "-_.") {
			hyphenated++
		}
		if v == strings.ToUpper(v) && strings.IndexFunc(v, unicode.IsLetter) >= 0 {
			upper++
		}
		if strings.IndexFunc(v, unicode.IsDigit) >= 0 {
			digits++
		}
	}

	sort.Ints(lengths)

	half := len(documented) / 2

	return enumShape{
		length:     lengths[len(lengths)/2],
		hyphenated: hyphenated > half,
		upper:      upper > half,
		digits:     digits > half,
	}
}

// render builds one candidate of this shape from a single letter.
func (s enumShape) render(letter rune) string {
	length := s.length
	if length < 2 {
		// A one-character enum is real -- "y"/"n" -- and a candidate has to be at least as
		// long as one member or a length check would reject it for the wrong reason.
		length = 2
	}
	// Long documented values do not need an equally long candidate, and a very long body field
	// invites a size limit to reject it. Capped where a rejection is still attributable.
	if length > maxCandidateLength {
		length = maxCandidateLength
	}

	body := strings.Repeat(string(letter), length)

	if s.hyphenated && length >= 3 {
		// Split so the candidate carries the separator the documented values do, at the same
		// kind of position.
		mid := length / 2
		body = body[:mid] + "-" + body[mid+1:]
	}

	if s.digits && length >= 2 {
		body = body[:length-1] + "9"
	}

	if s.upper {
		body = strings.ToUpper(body)
	}

	return body
}

// maxCandidateLength bounds a generated candidate.
const maxCandidateLength = 24

// EnumCandidates is the full set of values the enum protocol will send for one field.
//
// The documented values first, in the order the specification declares them, then the generated
// negatives. Order is part of the contract: a cassette is an ordered transcript, so a change here
// invalidates every recorded enum interaction and must be a deliberate act.
func EnumCandidates(f Field) []string {
	out := append([]string(nil), f.AllowedValues...)

	return append(out, negativeCandidates(f.AllowedValues, negativeEnumCandidates)...)
}

// describeCandidate is how a note or a fact refers to a generated value.
func describeCandidate(documented []string, candidate string) string {
	for _, v := range documented {
		if v == candidate {
			return fmt.Sprintf("%q (documented)", candidate)
		}
	}

	return fmt.Sprintf("%q (generated, shaped like the documented values)", candidate)
}
