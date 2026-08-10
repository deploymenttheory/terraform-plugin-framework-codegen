package plan

import (
	"fmt"
	"math/bits"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// maxDepth bounds nested-object synthesis, so a self-referencing schema —
// a tag with a parent tag — produces a finite body instead of a loop. A
// nested required field past the ceiling fails that object's synthesis,
// which surfaces as the entity being skipped with a reason.
const maxDepth = 8

// synth derives the values a step's body will send. Every value is a
// deterministic function of the inputs, the schema and the attribute name
// — never the clock, never randomness — which is what keeps Derive a pure
// function.
type synth struct {
	entity string
	// prefix is audit.name_prefix: every synthesized name-bearing string
	// carries it, so live objects the audit creates are recognisable as
	// toolkit debris and cleanup can match them by name.
	prefix string
	inputs EntityInputs
}

// minimalBody synthesizes the smallest valid create body: required,
// writable fields only. A required field no value can be derived for is a
// reason to skip the entity, returned as prose.
func (sy synth) minimalBody(s *specmodel.Schema) (map[string]any, string) {
	props, required := flatProps(s)
	body := map[string]any{}
	for _, p := range props {
		if !required[p.Name] || p.Schema.Resolved().ReadOnly {
			continue
		}
		v, ok := sy.value(p.Name, p.Schema, 0, true)
		if !ok {
			return nil, fmt.Sprintf("cannot synthesize a value for required field %q; supply one in %s", p.Name, InputsPath)
		}
		body[p.Name] = v
	}
	return body, ""
}

// maximalBody synthesizes a body with every writable top-level field
// populated, and reports how many of them were optional — the createMaximal
// bisection allowance is sized from that. An optional field no value can
// be derived for is left out: maximal covers what it can.
func (sy synth) maximalBody(s *specmodel.Schema) (map[string]any, int) {
	props, required := flatProps(s)
	body := map[string]any{}
	optional := 0
	for _, p := range props {
		if p.Schema.Resolved().ReadOnly {
			continue
		}
		v, ok := sy.value(p.Name, p.Schema, 0, true)
		if !ok {
			continue
		}
		body[p.Name] = v
		if !required[p.Name] {
			optional++
		}
	}
	return body, optional
}

// bisectionAllowance is the extra createMaximal attempts worth reserving:
// enough to halve the optional set down to one field, plus the retry that
// confirms it — ceil(log2(n)) + 1.
func bisectionAllowance(optional int) int {
	if optional == 0 {
		return 0
	}
	return bits.Len(uint(optional-1)) + 1
}

// value synthesizes one field's value. The priority order is fixed:
// operator inputs, then the document's example, then its default, then the
// first enum value, then a format-driven value, then a type-driven one.
// topLevel scopes the inputs override to the body's own fields — nested
// values always come from the document.
func (sy synth) value(field string, s *specmodel.Schema, depth int, topLevel bool) (any, bool) {
	if topLevel {
		if v, ok := sy.inputs.Values[field]; ok {
			return v, true
		}
	}
	if depth > maxDepth {
		return nil, false
	}
	r := s.Resolved()
	if r.Example != nil {
		return r.Example, true
	}
	if r.Default != nil {
		return r.Default, true
	}
	if len(r.Enum) > 0 {
		return r.Enum[0], true
	}
	if v, ok := sy.formatValue(r.Format); ok {
		return v, true
	}
	return sy.typeValue(field, r, depth)
}

// formatValue derives a value from a declared string format. Constants
// throughout: date-time is a fixed instant, not the clock, because a plan
// derived twice must be identical.
func (sy synth) formatValue(format string) (any, bool) {
	switch format {
	case "email":
		return sy.prefix + "-" + RunIDToken + "@example.invalid", true
	case "uri", "url":
		return "https://example.invalid/" + sy.prefix, true
	case "uuid":
		return "00000000-0000-4000-8000-000000000000", true
	case "date-time":
		return "2026-01-01T00:00:00Z", true
	case "date":
		return "2026-01-01", true
	case "hostname":
		return "example.invalid", true
	case "ipv4":
		return "192.0.2.1", true
	default:
		return nil, false
	}
}

// typeValue derives a value from the declared type alone — the last
// resort.
func (sy synth) typeValue(field string, r *specmodel.Schema, depth int) (any, bool) {
	switch {
	case r.Type == "string":
		if nameBearing(field) {
			return sy.nameToken(field), true
		}
		return "sample-" + field, true
	case r.Type == "boolean":
		return true, true
	case r.Type == "integer":
		return 1, true
	case r.Type == "number":
		return 1.5, true
	case r.Type == "array":
		if r.Items == nil {
			return nil, false
		}
		v, ok := sy.value(field, r.Items, depth+1, false)
		if !ok {
			return nil, false
		}
		return []any{v}, true
	case r.Type == "object" || len(r.Properties) > 0 || len(r.AllOf) > 0:
		return sy.objectValue(r, depth)
	case len(r.OneOf) > 0:
		return sy.value(field, r.OneOf[0], depth+1, false)
	case len(r.AnyOf) > 0:
		return sy.value(field, r.AnyOf[0], depth+1, false)
	default:
		return nil, false
	}
}

// objectValue synthesizes a nested object: its required writable fields,
// recursively. Optional nested fields are left out even for a maximal
// body — depth is bounded, and the maximal claim is about the entity's own
// attributes, not the transitive closure.
func (sy synth) objectValue(r *specmodel.Schema, depth int) (any, bool) {
	props, required := flatProps(r)
	out := map[string]any{}
	for _, p := range props {
		if !required[p.Name] || p.Schema.Resolved().ReadOnly {
			continue
		}
		v, ok := sy.value(p.Name, p.Schema, depth+1, false)
		if !ok {
			return nil, false
		}
		out[p.Name] = v
	}
	return out, true
}

// variant synthesizes a second, distinct value for an update: something
// the API should store differently from base. Scalars only — updating one
// element of a nested object is a claim about the nested field, not the
// attribute — and an enum with a single documented value has no variant,
// so the field yields no update step.
func (sy synth) variant(field string, s *specmodel.Schema, base any) (any, bool) {
	r := s.Resolved()
	if len(r.Enum) > 0 {
		for _, v := range r.Enum {
			if !equalScalar(v, base) {
				return v, true
			}
		}
		return nil, false
	}
	if v, ok := sy.formatVariant(r.Format); ok {
		return v, true
	}
	switch r.Type {
	case "boolean":
		if b, ok := base.(bool); ok {
			return !b, true
		}
		return false, true
	case "string":
		if bs, ok := base.(string); ok {
			return bs + "-2", true
		}
		return "sample-" + field + "-2", true
	case "integer":
		switch b := base.(type) {
		case int:
			return b + 1, true
		case int64:
			return b + 1, true
		case float64:
			return b + 1, true
		}
		return 2, true
	case "number":
		if b, ok := base.(float64); ok {
			return b + 1, true
		}
		return 2.5, true
	default:
		return nil, false
	}
}

// formatVariant is the second value for a formatted string.
func (sy synth) formatVariant(format string) (any, bool) {
	switch format {
	case "email":
		return sy.prefix + "-" + RunIDToken + "-2@example.invalid", true
	case "uri", "url":
		return "https://example.invalid/" + sy.prefix + "-2", true
	case "uuid":
		return "00000000-0000-4000-8000-000000000001", true
	case "date-time":
		return "2026-02-02T00:00:00Z", true
	case "date":
		return "2026-02-02", true
	case "hostname":
		return "alt.example.invalid", true
	case "ipv4":
		return "192.0.2.2", true
	default:
		return nil, false
	}
}

// nameToken is the value of a synthesized name-bearing string: the
// configured prefix, the run-id placeholder, and the attribute's address —
// recognisable as audit debris, unique per attribute, substituted at
// execution time.
func (sy synth) nameToken(field string) string {
	return sy.prefix + "-" + RunIDToken + "-" + sy.entity + "-" + field
}

// nameBearing reports whether a string field names its object — the
// fields whose synthesized values must carry the cleanup prefix.
func nameBearing(field string) bool {
	lf := strings.ToLower(field)
	for _, suffix := range []string{"name", "title", "label"} {
		if lf == suffix || strings.HasSuffix(lf, suffix) {
			return true
		}
	}
	return false
}

// equalScalar compares two decoded scalars loosely enough for enum
// matching: 1 and 1.0 are the same enum value however YAML and JSON
// spelled them.
func equalScalar(a, b any) bool {
	if a == b {
		return true
	}
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	return aok && bok && af == bf
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case float64:
		return t, true
	default:
		return 0, false
	}
}

// flatProps returns a schema's effective properties and required set,
// folding allOf branches in order. The first declaration of a property
// name wins, matching how the SDK backends resolve the same collision.
func flatProps(s *specmodel.Schema) ([]specmodel.Property, map[string]bool) {
	var props []specmodel.Property
	required := map[string]bool{}
	seen := map[string]bool{}

	var walk func(r *specmodel.Schema, depth int)
	walk = func(r *specmodel.Schema, depth int) {
		if r == nil || depth > maxDepth {
			return
		}
		r = r.Resolved()
		for _, p := range r.Properties {
			if !seen[p.Name] {
				seen[p.Name] = true
				props = append(props, p)
			}
		}
		for _, name := range r.Required {
			required[name] = true
		}
		for _, branch := range r.AllOf {
			walk(branch, depth+1)
		}
	}
	walk(s, 0)
	return props, required
}
