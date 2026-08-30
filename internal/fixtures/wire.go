package fixtures

import (
	"encoding/json"
	"fmt"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// WireJSON renders the form's values as the JSON body the API speaks,
// keyed by wire names, two-space indented, ending in one newline. The
// object's key order is the attribute-tree order — encoding/json's map
// ordering would randomise the file between runs, so the writer walks the
// derived order itself.
func (s Fixture) WireJSON(a Form) []byte {
	var b strings.Builder
	writeWireObject(&b, s.topLevel(a), a, 0)
	b.WriteString("\n")
	return []byte(b.String())
}

// WireValue renders the form's values as the plain Go shape a JSON
// encoder or a mock responder consumes: map keys are wire names, scalars
// stay typed, and nesting mirrors the tree.
func (s Fixture) WireValue(a Form) map[string]any {
	return wireLevel(s.topLevel(a), a)
}

// wireLevel builds one object level of the wire shape.
func wireLevel(values []Entry, a Form) map[string]any {
	out := make(map[string]any, len(values))
	for _, v := range values {
		out[v.Wire] = wireOne(v, a)
	}
	return out
}

// wireOne renders one value in wire shape.
func wireOne(v Entry, a Form) any {
	switch {
	case v.CollectionNestingDepth() > 1:
		return collectionValue(v)
	case v.Nested != nil && v.Kind == ir.TypeList:
		return []any{wireLevel(selected(v.Nested, a), a)}
	case v.Nested != nil && v.Kind == ir.TypeMap:
		return map[string]any{v.Name: wireLevel(selected(v.Nested, a), a)}
	case v.Nested != nil:
		return wireLevel(selected(v.Nested, a), a)
	case v.Kind == ir.TypeList:
		return []any{v.Scalar}
	case v.Kind == ir.TypeMap:
		return map[string]any{v.Name: v.Scalar}
	default:
		return v.Scalar
	}
}

// writeWireObject writes one JSON object with deterministic key order.
func writeWireObject(b *strings.Builder, values []Entry, a Form, depth int) {
	if len(values) == 0 {
		b.WriteString("{}")
		return
	}
	indent := strings.Repeat("  ", depth+1)
	b.WriteString("{\n")
	for i, v := range values {
		fmt.Fprintf(b, "%s%s: ", indent, jsonScalar(v.Wire))
		writeWireValue(b, v, a, depth+1)
		if i < len(values)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat("  ", depth) + "}")
}

// writeWireValue writes one attribute's value.
func writeWireValue(b *strings.Builder, v Entry, a Form, depth int) {
	switch {
	case v.CollectionNestingDepth() > 1:
		b.WriteString(jsonScalar(collectionValue(v)))
	case v.Nested != nil && v.Kind == ir.TypeList:
		b.WriteString("[\n" + strings.Repeat("  ", depth+1))
		writeWireObject(b, selected(v.Nested, a), a, depth+1)
		b.WriteString("\n" + strings.Repeat("  ", depth) + "]")
	case v.Nested != nil && v.Kind == ir.TypeMap:
		b.WriteString("{\n" + strings.Repeat("  ", depth+1) + jsonScalar(v.Name) + ": ")
		writeWireObject(b, selected(v.Nested, a), a, depth+1)
		b.WriteString("\n" + strings.Repeat("  ", depth) + "}")
	case v.Nested != nil:
		writeWireObject(b, selected(v.Nested, a), a, depth)
	case v.Kind == ir.TypeList:
		b.WriteString("[" + jsonScalar(v.Scalar) + "]")
	case v.Kind == ir.TypeMap:
		// One entry, keyed by the attribute's own name: a map's keys are the
		// practitioner's, so the document names none to take.
		b.WriteString("{" + jsonScalar(v.Name) + ": " + jsonScalar(v.Scalar) + "}")
	default:
		b.WriteString(jsonScalar(v.Scalar))
	}
}

// jsonScalar renders one scalar as JSON; the values are all
// fixtures-synthesised, so a marshal failure cannot happen.
func jsonScalar(value any) string {
	out, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(out)
}

// collectionValue is the wire value of a collection of collections: the
// leaf wrapped once per level, innermost first, a list as a one-member
// slice and a map as one entry keyed by the attribute's own name.
func collectionValue(v Entry) any {
	var value any = v.Scalar
	levels := v.CollectionLevels()
	for index := len(levels) - 1; index >= 0; index-- {
		if levels[index] == ir.TypeMap {
			value = map[string]any{v.Name: value}
			continue
		}
		value = []any{value}
	}
	return value
}
