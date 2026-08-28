package observe

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The ways an API is seen to store a value in a spelling of its own. Each is
// a relation between the value sent and the value answered that a generated
// provider can check, so state keeps the configured spelling when the answer
// is the stored form of it. These are the values x-tfpfgen-normalisation
// takes.
const (
	// NormalisationCaseFolded: the answer is the value in another case.
	NormalisationCaseFolded = "case-folded"
	// NormalisationTrimmed: the answer is the value without its surrounding
	// whitespace.
	NormalisationTrimmed = "trimmed"
	// NormalisationExtended: the answer carries the value inside a longer
	// spelling — a scheme and a trailing slash around a host, a port after
	// it, a unit after a number.
	NormalisationExtended = "extended"
	// NormalisationSameInstant: the answer is the same timestamp in another
	// spelling — without the zone, the T or the fraction.
	NormalisationSameInstant = "same-instant"
	// NormalisationReordered: the answer is the list sent in another order.
	NormalisationReordered = "reordered"
)

// NormalisationKinds is every value x-tfpfgen-normalisation admits.
var NormalisationKinds = map[string]bool{
	NormalisationCaseFolded:  true,
	NormalisationTrimmed:     true,
	NormalisationExtended:    true,
	NormalisationSameInstant: true,
	NormalisationReordered:   true,
}

// Normalisation reports whether got is a recognisable transform of sent,
// answering which kind and the stored form as the string a normalisation
// observation carries. Equal values are not a normalisation.
func Normalisation(sent, got any) (kind, form string, ok bool) {
	if ss, isString := sent.(string); isString {
		gs, isString := got.(string)
		if !isString || gs == ss {
			return "", "", false
		}
		switch {
		case strings.EqualFold(gs, ss) && strings.TrimSpace(gs) == gs:
			return NormalisationCaseFolded, gs, true
		case gs == strings.TrimSpace(ss):
			return NormalisationTrimmed, gs, true
		case strings.EqualFold(gs, strings.TrimSpace(ss)):
			return NormalisationCaseFolded, gs, true
		case ss != "" && strings.Contains(gs, ss) && !isMaskText(gs):
			return NormalisationExtended, gs, true
		case SameInstant(ss, gs):
			return NormalisationSameInstant, gs, true
		}
		return "", "", false
	}
	sentList, okS := sent.([]any)
	gotList, okG := got.([]any)
	if okS && okG && len(sentList) == len(gotList) {
		sorted := make([]any, len(sentList))
		copy(sorted, sentList)
		sort.Slice(sorted, func(i, j int) bool { return fmt.Sprint(sorted[i]) < fmt.Sprint(sorted[j]) })
		if equalJSON(sorted, gotList) && !equalJSON(sentList, gotList) {
			raw, _ := json.Marshal(gotList)
			return NormalisationReordered, string(raw), true
		}
	}
	return "", "", false
}

// timestampLayouts is every spelling a timestamp is read in: RFC 3339
// with or without a fraction, and the forms that drop the zone, the T or
// the time of day.
var timestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// SameInstant reports whether two strings both spell a timestamp and spell
// the same one. A spelling without a zone is read as UTC, which is what an
// API answering a zoned value without its zone has done.
func SameInstant(a, b string) bool {
	ta, okA := ParseTimestamp(a)
	tb, okB := ParseTimestamp(b)
	return okA && okB && ta.Equal(tb)
}

// ParseTimestamp reads a timestamp in any of the spellings an API answers.
func ParseTimestamp(s string) (time.Time, bool) {
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// isMaskText reports whether a string is made of mask characters alone.
func isMaskText(s string) bool {
	if len(s) < 3 {
		return false
	}
	for _, r := range s {
		if r != '*' && r != '•' {
			return false
		}
	}
	return true
}

// equalJSON compares two values through their JSON encoding.
func equalJSON(a, b any) bool {
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ra) == string(rb)
}
