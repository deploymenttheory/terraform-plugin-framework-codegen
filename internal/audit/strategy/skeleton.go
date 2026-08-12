package strategy

import (
	"sort"
	"strconv"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// maxDepth bounds the allOf fold, so a self-referencing schema produces a
// finite walk instead of a loop. It mirrors the ceiling the plan package's
// synthesis uses, for the same reason.
const maxDepth = 8

// field is one effective property of a create body: its wire name, its
// resolved schema, and whether the flattened body requires it.
type field struct {
	name     string
	schema   *specmodel.Schema
	required bool
}

// flatFields returns a schema's effective properties, folding allOf branches
// in order with the first declaration of a name winning — the same collision
// rule the SDK backends apply — and marking each with the flattened required
// set. readOnly properties are excluded: the audit only ever sends writable
// fields, so a read-only property is never a skeleton field, a gate or an
// update target.
func flatFields(s *specmodel.Schema) []field {
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

	out := make([]field, 0, len(props))
	for _, p := range props {
		if p.Schema.Resolved().ReadOnly {
			continue
		}
		out = append(out, field{name: p.Name, schema: p.Schema, required: required[p.Name]})
	}
	return out
}

// fieldNames lists a field slice's names, sorted.
func fieldNames(fields []field) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.name)
	}
	sort.Strings(out)
	return out
}

// synthHint pulls one field's synthesis material out of its resolved schema.
func synthHint(f field) SynthHint {
	r := f.schema.Resolved()
	h := SynthHint{
		Field:    f.name,
		Required: f.required,
		Type:     r.Type,
		Format:   r.Format,
		Pattern:  r.Pattern,
		Example:  r.Example,
		Default:  r.Default,
		Minimum:  r.Minimum,
		Maximum:  r.Maximum,
	}
	if len(r.Enum) > 0 {
		h.Enum = dedupeValues(r.Enum)
	}
	return h
}

// hintsFor builds the sorted per-field synthesis hints for a set of field
// names, drawn from the given fields.
func hintsFor(names []string, byName map[string]field) []SynthHint {
	hints := make([]SynthHint, 0, len(names))
	for _, n := range names {
		if f, ok := byName[n]; ok {
			hints = append(hints, synthHint(f))
		}
	}
	sort.Slice(hints, func(i, j int) bool { return hints[i].Field < hints[j].Field })
	return hints
}

// skeleton assembles a Skeleton from a sorted name set and the field index.
func skeleton(names []string, byName map[string]field) Skeleton {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return Skeleton{Fields: sorted, Hints: hintsFor(sorted, byName)}
}

// indexFields maps a field slice by name.
func indexFields(fields []field) map[string]field {
	byName := make(map[string]field, len(fields))
	for _, f := range fields {
		byName[f.name] = f
	}
	return byName
}

// isEnum reports whether a resolved schema declares an enum.
func isEnum(s *specmodel.Schema) bool {
	return len(s.Resolved().Enum) > 0
}

// isBool reports whether a resolved schema is boolean-typed.
func isBool(s *specmodel.Schema) bool {
	return s.Resolved().Type == "boolean"
}

// dedupeValues drops repeated scalars while keeping the document's order and
// each value's decoded type. Order is kept because an enum's first member is
// the one synthesis reaches for, and the document's first member is a better
// guess at a workable value than whichever one sorts first as text. Type is
// kept because these values are sent on the wire.
func dedupeValues(vals []any) []any {
	seen := map[string]bool{}
	out := make([]any, 0, len(vals))
	for _, v := range vals {
		s := stringifyScalar(v)
		if !seen[s] {
			seen[s] = true
			out = append(out, v)
		}
	}
	return out
}

// stringifyValues renders decoded scalar values as strings, sorted and
// de-duplicated, so a value list is byte-stable however the document spelled
// the scalars. It is for prose and comparison only — never for a value that
// goes on the wire, which must keep the type dedupeValues preserves.
func stringifyValues(vals []any) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		s := stringifyScalar(v)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// stringifyScalar renders one decoded scalar canonically: a float that is a
// whole number loses its fraction, so 1 and 1.0 render identically whether
// YAML or JSON produced them.
func stringifyScalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	default:
		return ""
	}
}
