package strategy

import (
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// The prose phrase set. It is deliberately small, general English, and
// vendor-neutral: never a phrase from one API's documentation, only the
// conditional-language patterns any REST document uses. The repo's
// pilot-leakage lint guards that nothing here names a pilot vendor.
//
// A phrase yields a hypothesis only when it also NAMES a sibling property (and,
// where the category needs one, a value of that sibling). A phrase that names
// nothing is discarded — prose is the weakest signal, and an unanchored one is
// no signal at all.
type proseCategory int

const (
	// catRequired: the field is required when the named condition holds.
	catRequired proseCategory = iota
	// catValid: the field is valid, applies, or is ignored only when the
	// named condition holds — a value-gated validity edge either way.
	catValid
	// catExclusive: the field cannot be combined with the named sibling.
	catExclusive
)

// prosePhrase is one recognised phrase and the edge category it signals.
type prosePhrase struct {
	text     string
	category proseCategory
}

// prosePhrases is the closed, ordered phrase set. Order is fixed so extraction
// is deterministic; longer, more specific phrases precede the shorter phrases
// they contain so the specific reading wins.
var prosePhrases = []prosePhrase{
	{"required only when", catRequired},
	{"required when", catRequired},
	{"required if", catRequired},
	{"must be set when", catRequired},
	{"applies only when", catValid},
	{"applies only to", catValid},
	{"applies when", catValid},
	{"applies to", catValid},
	{"only applies when", catValid},
	{"only when", catValid},
	{"only if", catValid},
	{"ignored when", catValid},
	{"mutually exclusive with", catExclusive},
	{"mutually exclusive", catExclusive},
	{"cannot be used with", catExclusive},
}

// proseHypotheses mines every writable field's description for conditional
// language, emitting prose-provenance hypotheses. Fields are walked in name
// order and phrases in their fixed order, so the output is deterministic.
func proseHypotheses(createBody *specmodel.Schema) []Hypothesis {
	fields := flatFields(createBody)
	sort.Slice(fields, func(i, j int) bool { return fields[i].name < fields[j].name })

	siblingEnum := map[string][]string{}
	var siblingNames []string
	for _, f := range fields {
		siblingNames = append(siblingNames, f.name)
		if e := f.schema.Resolved().Enum; len(e) > 0 {
			siblingEnum[f.name] = stringifyValues(e)
		}
	}

	var out []Hypothesis
	for _, f := range fields {
		desc := f.schema.Resolved().Description
		if desc == "" {
			continue
		}
		out = append(out, extractProse(f.name, desc, siblingNames, siblingEnum)...)
	}
	return out
}

// extractProse runs the phrase set over one field's description. For each
// phrase found, it looks — in the text after the phrase — for a sibling name,
// and for a value of that sibling. What it finds decides the edge; what it
// cannot anchor it discards.
func extractProse(field, desc string, siblingNames []string, siblingEnum map[string][]string) []Hypothesis {
	lower := strings.ToLower(desc)
	var out []Hypothesis
	for _, ph := range prosePhrases {
		idx := strings.Index(lower, ph.text)
		if idx < 0 {
			continue
		}
		tail := lower[idx+len(ph.text):]

		sibling := earliestSibling(tail, field, siblingNames)
		if sibling == "" {
			continue // names no sibling: discarded
		}

		if ph.category == catExclusive {
			out = append(out, mutuallyExclusiveHypothesis(field, sibling))
			continue
		}

		value := earliestValue(tail, siblingEnum[sibling])
		if value == "" {
			// A conditional phrase that names a sibling but no value is a
			// weaker co-requirement: the field relates to the sibling's
			// presence.
			out = append(out, proseRequiresField(field, sibling))
			continue
		}
		out = append(out, conditionalHypothesis(ph.category, field, sibling, value))
	}
	return out
}

// conditionalHypothesis builds a value-gated prose edge: requiredWhen for the
// required category, validWhen for the validity category.
func conditionalHypothesis(cat proseCategory, field, sibling, value string) Hypothesis {
	kind := HypothesisValidWhen
	if cat == catRequired {
		kind = HypothesisRequiredWhen
	}
	return Hypothesis{
		Kind:       kind,
		Subjects:   []string{field},
		GateField:  sibling,
		GateValue:  value,
		Provenance: ProvenanceProse,
		Check: Check{
			Step: stepCreatePerEnumValue, Field: field,
			GateField: sibling, GateValue: value, Expect: "conditional",
		},
	}
}

// proseRequiresField builds a prose co-requirement edge naming a sibling but no
// value.
func proseRequiresField(field, sibling string) Hypothesis {
	subjects := []string{field, sibling}
	sort.Strings(subjects)
	return Hypothesis{
		Kind:       HypothesisRequiresField,
		Subjects:   subjects,
		Provenance: ProvenanceProse,
		Check:      Check{Step: stepCreateMaximal, Field: field, Expect: "conditional"},
	}
}

// mutuallyExclusiveHypothesis builds a prose exclusion edge between two fields.
func mutuallyExclusiveHypothesis(field, sibling string) Hypothesis {
	subjects := []string{field, sibling}
	sort.Strings(subjects)
	return Hypothesis{
		Kind:       HypothesisMutuallyExclusive,
		Subjects:   subjects,
		Provenance: ProvenanceProse,
		Check:      Check{Step: stepCreateMaximal, Expect: "reject"},
	}
}

// earliestSibling returns the sibling name whose whole-word occurrence is
// earliest in tail, excluding the field itself; empty when none occurs. Ties
// break by name so the choice is deterministic.
func earliestSibling(tail, field string, siblingNames []string) string {
	best, bestAt := "", len(tail)+1
	for _, name := range siblingNames {
		if name == field {
			continue
		}
		at := tokenIndex(tail, strings.ToLower(name))
		if at < 0 {
			continue
		}
		if at < bestAt || (at == bestAt && name < best) {
			best, bestAt = name, at
		}
	}
	return best
}

// earliestValue returns the enum value whose whole-word occurrence is earliest
// in tail; empty when none of the values occur. Ties break by value.
func earliestValue(tail string, values []string) string {
	best, bestAt := "", len(tail)+1
	for _, v := range values {
		at := tokenIndex(tail, strings.ToLower(v))
		if at < 0 {
			continue
		}
		if at < bestAt || (at == bestAt && v < best) {
			best, bestAt = v, at
		}
	}
	return best
}

// tokenIndex finds the earliest whole-token occurrence of needle in haystack,
// bounded by non-alphanumeric characters so "type" does not match inside
// "prototype". Returns -1 when absent or when needle is empty.
func tokenIndex(haystack, needle string) int {
	if needle == "" {
		return -1
	}
	from := 0
	for {
		rel := strings.Index(haystack[from:], needle)
		if rel < 0 {
			return -1
		}
		at := from + rel
		if boundedToken(haystack, at, len(needle)) {
			return at
		}
		from = at + 1
	}
}

// boundedToken reports whether the substring at [at, at+n) is bounded by a
// non-alphanumeric character (or the string edge) on both sides.
func boundedToken(s string, at, n int) bool {
	before := at == 0 || !isAlphaNum(s[at-1])
	end := at + n
	after := end == len(s) || !isAlphaNum(s[end])
	return before && after
}

// isAlphaNum reports whether a byte is an ASCII letter or digit.
func isAlphaNum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
