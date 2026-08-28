package run

// The refusal grammar: how a 4xx sentence is read into one adjustment. The
// fixed sentences are the test API server's stable vocabulary, and the
// looser ones are the shapes real APIs phrase the same facts in; every
// parser here is pure, so a sentence reads the same on every run.

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
)

var (
	reRequires  = regexp.MustCompile(`field (\S+) requires field (\S+) to be set`)
	reNotValid  = regexp.MustCompile(`field (\S+) is not valid(?: when (\w+)=(\S+))?`)
	reReference = regexp.MustCompile(`field (\S+) must reference an existing (\w+)`)
	reRequired  = regexp.MustCompile(`field (\S+) is required(?: when (\w+)=(\S+))?`)

	// reFieldNamed matches a refusal that names its field mid-sentence and
	// states the complaint after a separator, which is how a framework that
	// wraps its validation errors in prose reports them.
	reFieldNamed = regexp.MustCompile(`(?i)\bfield\s+([\w.\[\]-]+)\s*[:\-]\s*(.+)`)
	// reTheRequired matches the bare English an API writes when it names the
	// field it wanted and nothing else about it.
	reTheRequired = regexp.MustCompile(`(?i)\bthe\s+([\w.]+)\s+(?:is|are)\s+required\b`)
	// reFieldSaid matches the field-prefixed refusal a validation framework
	// emits: the property it rejected, a colon, then its complaint. The field
	// is a dotted path when the API validates a nested request object, and the
	// last segment is the name the request body spells.
	reFieldSaid = regexp.MustCompile(`^\s*([\w.\[\]-]+)\s*:\s*(.+)$`)
	// reAbsent matches the complaints that mean "you sent nothing for this",
	// as distinct from "what you sent is wrong": only absence is corrected by
	// adding a value.
	reAbsent = regexp.MustCompile(`(?i)\b(?:is required|is mandatory|must not be (?:null|empty|blank)|may not be (?:null|empty|blank)|cannot be (?:null|empty|blank)|must be (?:provided|specified|present)|missing)\b`)
	// reMissingProperty matches a deserialiser naming the property it could
	// not find, quoted — the shape a polymorphic body's discriminator is
	// reported missing in.
	reMissingProperty = regexp.MustCompile(`(?i)\bmissing (?:[\w-]+ )*property '([\w.]+)'`)
	// reAtLeastOne matches a refusal offering a choice of fields, one of
	// which has to be present; the list after the colon is the candidates.
	reAtLeastOne = regexp.MustCompile(`(?i)\bat least one of (?:the following )?(?:is |are )?(?:required|mandatory)\s*[:\-]?\s*(.+)`)
	// reUnwanted matches the complaints that mean "you sent this and must
	// not", the opposite of reAbsent: only presence is corrected by removal.
	reUnwanted = regexp.MustCompile(`(?i)^\s*(?:must be null|is not allowed|not allowed|should not be (?:set|present)|cannot be set|must not be set|unknown (?:property|field)|unrecognized (?:property|field))\b`)
	// reBareAbsent matches the word before an absence complaint, in a
	// sentence carrying no "field" marker: "endRepeat must be specified".
	reBareAbsent = regexp.MustCompile(`(?i)\b([A-Za-z][\w.]*)\s+(?:must be (?:specified|provided|present|set)|is required|is mandatory|cannot be (?:null|empty|blank)|must not be (?:null|empty|blank)|may not be (?:null|empty|blank))\b`)
)

// classifyRefusal reads a 4xx response body and decides what to change. The
// order is deliberate: the two-field grammars (requires, reference) and the
// removal grammar are checked before the bare "is required", because "requires
// field Y" and "is required" share a stem.
func classifyRefusal(res *httpResult) parsedRefusal {
	message := refusalMessage(res.body)
	if message == "" {
		return parsedRefusal{kind: adjustmentNone}
	}
	if m := reRequires.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentRequires, field: cleanField(m[2]), trigger: cleanField(m[1])}
	}
	if m := reNotValid.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentRemove, field: cleanField(m[1]), condGate: m[2], condVal: cleanField(m[3])}
	}
	if m := reReference.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentBorrow, field: cleanField(m[1]), collection: strings.ToLower(m[2])}
	}
	if m := reRequired.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentAdd, field: cleanField(m[1]), condGate: m[2], condVal: cleanField(m[3])}
	}
	// A choice of fields is read before the bare "the X is required", which
	// would otherwise take "the following" for a field name.
	if m := reAtLeastOne.FindStringSubmatch(message); m != nil {
		if candidates := listedCandidates(m[1]); len(candidates) > 0 {
			return parsedRefusal{kind: adjustmentAdd, field: candidates[0], candidates: candidates, mustBeDeclared: true}
		}
	}
	if m := reMissingProperty.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentAdd, field: leafField(m[1])}
	}
	if m := reFieldNamed.FindStringSubmatch(message); m != nil && reAbsent.MatchString(m[2]) {
		return parsedRefusal{kind: adjustmentAdd, field: leafField(m[1])}
	}
	if m := reTheRequired.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentAdd, field: leafField(m[1])}
	}
	// Checked after every marked grammar, because it is loose: any sentence
	// at all can be read as "<field>: <complaint>", so it must not pre-empt a
	// grammar that names two fields or a gate.
	if m := reFieldSaid.FindStringSubmatch(message); m != nil && reAbsent.MatchString(m[2]) {
		return parsedRefusal{kind: adjustmentAdd, field: leafField(m[1])}
	}
	if m := reFieldSaid.FindStringSubmatch(message); m != nil && reUnwanted.MatchString(m[2]) {
		return parsedRefusal{kind: adjustmentRemove, field: cleanField(m[1])}
	}
	if m := reBareAbsent.FindStringSubmatch(message); m != nil {
		return parsedRefusal{kind: adjustmentAdd, field: leafField(m[1]), mustBeDeclared: true}
	}
	return parsedRefusal{kind: adjustmentNone}
}

// listedCandidates splits the field list a choice refusal ends with — "a, b
// or c" — into its members, in the order offered.
func listedCandidates(list string) []string {
	list = strings.TrimRight(strings.TrimSpace(list), ".")
	list = strings.ReplaceAll(list, " or ", ",")
	list = strings.ReplaceAll(list, " and ", ",")
	var out []string
	for _, part := range strings.Split(list, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// declaredSpelling answers the wire name an entity declares for a field a
// refusal spelt in its own words — "query params" for queryParams — comparing
// with case and punctuation removed. Empty when nothing declared matches.
func declaredSpelling(candidate string, known map[string]strategy.SyntheticValueRules) string {
	wanted := lettersOf(candidate)
	if wanted == "" {
		return ""
	}
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if lettersOf(name) == wanted {
			return name
		}
	}
	return ""
}

// lettersOf lower-cases a name and drops everything but letters and digits.
func lettersOf(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func leafField(s string) string {
	s = cleanField(s)
	if i := strings.LastIndex(s, "."); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// refusalMessage pulls the human-legible sentence out of whichever error
// envelope the API used — problem+json's detail, an oauth error_description, a
// legacy errorMessage — falling back to the raw body when it is not JSON. It
// deliberately does not join the title: a bare field name in detail beside a
// generic title must not be mistaken for a "field X is required" sentence.
func refusalMessage(raw []byte) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err == nil {
		// The listed complaints come first: an envelope that carries both
		// names the field it rejected in the list and only summarises it in
		// the sentence, and a summary corrects nothing.
		if listed := firstListed(m); listed != "" {
			return listed
		}
		for _, k := range []string{"detail", "message", "error_description", "errorMessage", "error", "title"} {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	return string(raw)
}

// firstListed pulls the first complaint out of an envelope that carries them
// as a list rather than a sentence, which is how an API that validates every
// property before answering reports what it rejected.
//
// Only the first is read: one refusal corrects one field, and the next attempt
// re-reads whatever the API then complains about, so taking them one at a time
// converges without assuming the list is ordered or complete.
func firstListed(m map[string]any) string {
	for _, k := range []string{"errors", "messages", "details", "errorMessages", "validationErrors"} {
		listed, ok := m[k].([]any)
		if !ok {
			continue
		}
		for _, entry := range listed {
			switch e := entry.(type) {
			case string:
				if e != "" {
					return e
				}
			case map[string]any:
				// An entry that names the field separately is spelled back
				// into the "<field>: <complaint>" shape the grammar reads.
				field, _ := firstString(e, "field", "name", "property", "path", "pointer", "code")
				complaint, found := firstString(e, "message", "defaultMessage", "detail", "description", "error", "reason")
				if !found {
					continue
				}
				if field != "" {
					return field + ": " + complaint
				}
				return complaint
			}
		}
	}
	return ""
}

// firstString returns the first of the named keys the map carries as a
// non-empty string, and whether it found one.
func firstString(m map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s, true
		}
	}
	return "", false
}

// cleanField strips the trailing punctuation a refusal sentence might carry
// after a field name.
func cleanField(s string) string {
	return strings.TrimRight(s, ".,;:")
}
