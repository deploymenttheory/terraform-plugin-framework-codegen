package probe

import (
	"strings"
	"testing"
	"unicode"
)

// TestUnit_Probe_NegativeEnumCandidatesAreShapedLikeTheDocumentedOnes is the test that stops a
// probe concluding "closed" when it merely rejected a forty-character string.
//
// A rejection is only evidence about the *value set* if the candidate could not have been rejected
// for some other reason. So a candidate must match the documented values on length and character
// class, and differ only in being outside the set.
func TestUnit_Probe_NegativeEnumCandidatesAreShapedLikeTheDocumentedOnes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		documented []string
	}{
		{"short lowercase", []string{"and", "or"}},
		{"hyphenated", []string{"test", "dashboard", "endpoint-test", "connected-devices-test"}},
		{"upper case", []string{"ACTIVE", "PAUSED", "DISABLED"}},
		{"with digits", []string{"http2", "http3"}},
		{"single character", []string{"y", "n"}},
		{"one member only", []string{"only"}},
		{"very long", []string{strings.Repeat("verbose-", 12) + "value"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := negativeCandidates(tc.documented, negativeEnumCandidates)

			if len(got) != negativeEnumCandidates {
				t.Fatalf("got %d candidate(s), want %d: %v", len(got), negativeEnumCandidates, got)
			}

			shape := shapeOf(tc.documented)

			for _, c := range got {
				// Outside the set, which is the entire point.
				for _, d := range tc.documented {
					if strings.EqualFold(c, d) {
						t.Errorf("%q is a documented value, so rejecting it proves nothing about "+
							"the set being closed", c)
					}
				}

				// Long enough that a length check is not the likelier explanation for a
				// rejection.
				if len(c) < 2 {
					t.Errorf("%q is too short to be rejected for a reason about the enum", c)
				}
				if len(c) > maxCandidateLength {
					t.Errorf("%q is %d characters; a long body field invites a size limit to "+
						"reject it for the wrong reason", c, len(c))
				}

				// Same character classes, so a class check is not the likelier explanation
				// either.
				if shape.hyphenated && shape.length >= 3 && !strings.Contains(c, "-") {
					t.Errorf("%q has no separator, but the documented values do", c)
				}
				if shape.upper && c != strings.ToUpper(c) {
					t.Errorf("%q is not upper case, but the documented values are", c)
				}
				if shape.digits && strings.IndexFunc(c, unicode.IsDigit) < 0 {
					t.Errorf("%q has no digit, but the documented values do", c)
				}
			}

			// Distinct, or the second negative adds no evidence and the two-negative rule is
			// theatre.
			if len(got) == 2 && got[0] == got[1] {
				t.Errorf("both candidates are %q", got[0])
			}
		})
	}
}

// TestUnit_Probe_EnumCandidatesAreDeterministic.
//
// A mutating cassette records exact request bodies, so a random candidate would make every
// recorded enum interaction unreplayable -- and a replay mismatch looks exactly like a probe
// regression while being nothing of the kind.
func TestUnit_Probe_EnumCandidatesAreDeterministic(t *testing.T) {
	t.Parallel()

	f := Field{JSONPath: "objectType", Enum: []string{"test", "dashboard", "endpoint-agent"}}

	first := EnumCandidates(f)
	for range 20 {
		again := EnumCandidates(f)
		if strings.Join(again, ",") != strings.Join(first, ",") {
			t.Fatalf("candidates vary between calls:\n%v\n%v", first, again)
		}
	}

	// The documented values come first, in the order the specification declares them. Order is
	// part of the contract: a cassette is an ordered transcript.
	for i, want := range f.Enum {
		if first[i] != want {
			t.Errorf("candidate %d = %q, want the documented %q", i, first[i], want)
		}
	}
	if len(first) != len(f.Enum)+negativeEnumCandidates {
		t.Errorf("got %d candidates, want %d documented plus %d generated",
			len(first), len(f.Enum), negativeEnumCandidates)
	}
}

// TestUnit_Probe_NoEnumMeansNoCandidates: a field the specification says nothing about is not a
// field this protocol has anything to say about.
func TestUnit_Probe_NoEnumMeansNoCandidates(t *testing.T) {
	t.Parallel()

	if got := EnumCandidates(Field{JSONPath: "value"}); got != nil {
		t.Errorf("EnumCandidates = %v, want nil", got)
	}
	if got := negativeCandidates(nil, 2); got != nil {
		t.Errorf("negativeCandidates(nil) = %v", got)
	}
	if got := negativeCandidates([]string{"a"}, 0); got != nil {
		t.Errorf("asking for none should give none, got %v", got)
	}
}

// TestUnit_Probe_ACandidateSaysWhereItCameFrom.
//
// FactEnumRejectedDocumented means "the specification is stale", and that claim is only worth
// anything if a reader can tell a documented value from a generated one. A report that blurred the
// two would let a rejected *generated* value read as a stale specification.
func TestUnit_Probe_ACandidateSaysWhereItCameFrom(t *testing.T) {
	t.Parallel()

	documented := []string{"and", "or"}

	if got := describeCandidate(documented, "and"); !strings.Contains(got, "documented") {
		t.Errorf("describeCandidate = %q", got)
	}

	generated := negativeCandidates(documented, 1)[0]
	if got := describeCandidate(documented, generated); !strings.Contains(got, "generated") {
		t.Errorf("describeCandidate = %q", got)
	}
}

// TestUnit_Probe_ShapeUsesTheMedianNotTheMean.
//
// "connected-devices-test" beside "test" would drag a mean well outside the range the field
// actually accepts, and a candidate that long invites a size limit to reject it.
func TestUnit_Probe_ShapeUsesTheMedianNotTheMean(t *testing.T) {
	t.Parallel()

	documented := []string{"a", "b", "c", strings.Repeat("x", 200)}

	shape := shapeOf(documented)
	if shape.length > 10 {
		t.Errorf("median length = %d; one outlier should not set the shape", shape.length)
	}

	for _, c := range negativeCandidates(documented, negativeEnumCandidates) {
		if len(c) > 10 {
			t.Errorf("candidate %q is %d characters", c, len(c))
		}
	}
}
