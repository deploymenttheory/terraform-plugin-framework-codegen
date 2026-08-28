// Replaying a recorded create into a fixture: the values the API demonstrably
// took, in place of the values the document says it should take.
//
// The two halves are separated because they answer different questions. The
// derivation beside this one asks what a valid configuration looks like; this
// asks what one the API has already accepted looked like, and drops whatever
// terraform could not hold in state afterwards.

package fixtures

import (
	"reflect"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// RunSuffixExpr is the terraform expression an acceptance configuration
// suffixes its synthesised names with, and the block that supplies it.
//
// A live API that requires a name to be unique refuses the second run of a
// test whose name is a constant, and an object a failed run leaves behind
// holds that name for good. The committed configuration stays byte-identical
// because the expression, not a value, is what it carries.
const (
	RunSuffixExpr  = "${random_string.tfpfgen_run.result}"
	RunSuffixBlock = `resource "random_string" "tfpfgen_run" {
  length  = 10
  special = false
  upper   = false
}`
)

// WithRunSuffix answers a copy whose synthesised names carry the run suffix.
//
// Only the names this package invented are suffixed: a value the document
// supplied is one the API is known to accept, and appending to it could make
// it invalid — a URL, an enum member, a formatted identifier.
func (s Fixture) WithRunSuffix() Fixture {
	out := s
	out.Entries = suffixedEntries(s.Entries)
	return out
}

// suffixedEntries copies a level, suffixing the synthesised names in it.
func suffixedEntries(values []Entry) []Entry {
	if values == nil {
		return nil
	}
	out := make([]Entry, len(values))
	copy(out, values)
	for i := range out {
		if text, ok := out[i].Scalar.(string); ok && strings.HasPrefix(text, NamePrefix) {
			out[i].Scalar = suffixed(text)
		}
		out[i].Nested = suffixedEntries(out[i].Nested)
	}
	return out
}

// suffixed appends the run suffix to an invented value: at the end of a
// name, and before the domain of an invented address, where a suffix after
// the domain would spell an address no API accepts.
func suffixed(text string) string {
	if at := strings.Index(text, "@"); at >= 0 {
		return text[:at] + "-" + RunSuffixExpr + text[at:]
	}
	return text + "-" + RunSuffixExpr
}

// FromAcceptedRequestBody answers the entries a recorded create actually
// carried, with the values it carried them as.
//
// This is the difference between a configuration that looks like one the API
// would take and one it demonstrably did. A value derived from the document is
// a guess about what is acceptable; these were accepted.
//
// A property the request carried and the response did not is dropped, with the
// reason recorded: terraform compares what it planned against what the
// provider answers, so a value the API never echoes reads as the provider
// losing it, and no configuration can hold one.
func (s Fixture) FromAcceptedRequestBody(request, response map[string]any, requiredWire map[string]bool) Fixture {
	out := s
	out.Entries, out.Omissions = overlayEntries(s.Entries, request, response, requiredWire, nil, false)
	out.Omissions = append(out.Omissions, s.Omissions...)
	out.Entries = restoredNames(out.Entries)
	return out
}

// WithInventedNames answers a copy whose name-bearing entries carry the
// invented, prefixed name in place of any declared example, for the
// configurations a live API meets: an example name is a constant, and an
// API that requires names to be unique refuses it on every run after the
// first — and an object created under it carries nothing cleanup can match.
func (s Fixture) WithInventedNames() Fixture {
	out := s
	out.Entries = restoredNames(s.Entries)
	return out
}

// restoredNames puts back the invented name of every name-bearing entry a
// declared example displaced.
//
// A replayed body carries the values the API accepted, and for most properties
// that is exactly what a configuration wants. A name is the exception. An API
// that requires one to be unique accepted the document's example once and
// refuses it for good afterwards, so the value that proves the shape is the one
// value the configuration cannot reuse. The invented name carries NamePrefix,
// which is what WithRunSuffix needs to make it unique per run and what the
// audit's cleanup contract matches a live object by.
//
// Only name-bearing entries are restored: every other property keeps the value
// the API took, because nothing about it demands a different one. A field
// whose document declares a format has no invented name to put back —
// scalarFor keeps none — so a URL, an email or a uuid is never rewritten.
func restoredNames(values []Entry) []Entry {
	if values == nil {
		return nil
	}
	out := make([]Entry, len(values))
	copy(out, values)
	for i := range out {
		if out[i].synthesised != "" && nameBearing(out[i].Name) {
			out[i].Scalar = out[i].synthesised
		}
		out[i].Nested = restoredNames(out[i].Nested)
	}
	return out
}

// nameBearing reports whether an attribute names its object — the attributes
// whose values must be unique per run and must carry the prefix cleanup
// matches on.
func nameBearing(name string) bool {
	lower := strings.ToLower(name)
	for _, suffix := range []string{"name", "title", "label"} {
		if lower == suffix || strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// overlayEntries keeps the entries the body carried, taking their values from
// it, and reports the ones it dropped.
//
// retained marks a level under a root the configuration keeps whatever the
// response says — a required one, or one whose state keeps the planned value
// — so no member of it is judged against the echo either: the root is kept
// or dropped whole.
func overlayEntries(values []Entry, request, response map[string]any, requiredWire map[string]bool, path []string, retained bool) ([]Entry, []Omission) {
	var kept []Entry
	var dropped []Omission
	for _, v := range values {
		at := append(append([]string{}, path...), v.Name)
		carried, inRequest := request[v.Wire]
		if !inRequest {
			continue
		}
		keepWhole := retained || (path == nil && requiredWire[v.Wire])
		// A field the API takes and never returns cannot live in a
		// configuration; a required one has to be sent anyway, so it stays
		// and the risk is the API's rather than the generator's.
		if response != nil && !keepWhole {
			echoed, present := response[v.Wire]
			if !present {
				dropped = append(dropped, Omission{
					Name:   strings.Join(at, "."),
					Reason: "the API accepted this property and did not return it, so terraform cannot hold it in state",
				})
				continue
			}
			// A property the API answers with a different value: terraform
			// compares what it planned against what the provider answers, so
			// the value sent cannot live in a configuration. The value
			// answered can, where it is one of the same type and not a mask
			// — the API's own spelling of what it stored is the one spelling
			// it will not rewrite again. A mask is not a value at all, and
			// the property goes with the reason recorded.
			//
			// Only a value the fixture carries whole is compared. An object
			// recurses and its own fields are compared as leaves, and a list
			// renders one element rather than the collection, so comparing
			// the collection would judge a value no configuration carries.
			if v.Nested == nil && v.Kind != ir.TypeList && !reflect.DeepEqual(carried, echoed) {
				if text, ok := echoed.(string); ok && isMask(text) {
					dropped = append(dropped, Omission{
						Name:   strings.Join(at, "."),
						Reason: "the API returns this property masked, so no configuration can hold a value it will read back",
					})
					continue
				}
				if sameScalarType(carried, echoed) {
					carried = echoed
				} else {
					dropped = append(dropped, Omission{
						Name:   strings.Join(at, "."),
						Reason: "the API returned this property with a different value from the one it was sent, so no configuration can hold it",
					})
					continue
				}
			}
		}
		overlaid := overlayOne(v, carried, response, requiredWire, at, &dropped, keepWhole)
		// An object the body carried empty leaves nothing to render: no
		// scalar, and no field of its own that survived the overlay. Rendered
		// anyway it spells the absent value as a literal, and a configuration
		// carrying one does not plan.
		if overlaid.Scalar == nil && len(overlaid.Nested) == 0 {
			dropped = append(dropped, Omission{
				Name:   strings.Join(at, "."),
				Reason: "the API accepted this property with no value in it, so there is nothing for a configuration to carry",
			})
			continue
		}
		kept = append(kept, overlaid)
	}
	return kept, dropped
}

// overlayOne sets one entry from the value the body carried, recursing into
// the nested shapes a body spells as objects and arrays of objects.
func overlayOne(v Entry, carried any, response map[string]any, requiredWire map[string]bool, at []string, dropped *[]Omission, retained bool) Entry {
	switch nested := carried.(type) {
	case map[string]any:
		if v.Nested != nil {
			var inner []Omission
			v.Nested, inner = overlayEntries(v.Nested, nested, nestedResponse(response, v.Wire), requiredWire, at, retained)
			*dropped = append(*dropped, inner...)
			return v
		}
	case []any:
		if v.Nested != nil && len(nested) > 0 {
			if first, ok := nested[0].(map[string]any); ok {
				var inner []Omission
				v.Nested, inner = overlayEntries(v.Nested, first, nil, requiredWire, at, retained)
				*dropped = append(*dropped, inner...)
				return v
			}
		}
		// A list of scalars: the fixture carries one element.
		if len(nested) > 0 {
			v.Scalar = nested[0]
		}
		return v
	}
	if v.Nested == nil {
		v.Scalar = carried
	}
	return v
}

// nestedResponse is the object the response carried under one property, or nil
// when it carried none — in which case the level below is not echo-checked,
// because absence of the parent says nothing about its children.
func nestedResponse(response map[string]any, wire string) map[string]any {
	if response == nil {
		return nil
	}
	if inner, ok := response[wire].(map[string]any); ok {
		return inner
	}
	return nil
}

// sameScalarType reports whether two scalars are of one JSON type, so the
// answered one can stand in a configuration where the sent one stood.
func sameScalarType(sent, got any) bool {
	switch sent.(type) {
	case string:
		_, ok := got.(string)
		return ok
	case bool:
		_, ok := got.(bool)
		return ok
	case float64, int64, int:
		switch got.(type) {
		case float64, int64, int:
			return true
		}
	}
	return false
}

// isMask reports whether a string is made of mask characters alone — the
// asterisks or bullets an API answers a secret with.
func isMask(s string) bool {
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
