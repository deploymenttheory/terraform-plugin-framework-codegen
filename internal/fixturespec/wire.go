package fixturespec

import (
	"encoding/json"
	"fmt"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
)

// WireJSON renders the audience's values as the JSON body the API speaks,
// keyed by wire names, two-space indented, ending in one newline. The
// object's key order is the attribute-tree order — encoding/json's map
// ordering would randomise the file between runs, so the writer walks the
// derived order itself.
func (s Spec) WireJSON(a Audience) []byte {
	var b strings.Builder
	writeWireObject(&b, selected(s.Values, a), a, 0)
	b.WriteString("\n")
	return []byte(b.String())
}

// WireValue renders the audience's values as the plain Go shape a JSON
// encoder or a mock responder consumes: map keys are wire names, scalars
// stay typed, and nesting mirrors the tree.
func (s Spec) WireValue(a Audience) map[string]any {
	return wireLevel(selected(s.Values, a), a)
}

// wireLevel builds one object level of the wire shape.
func wireLevel(values []Value, a Audience) map[string]any {
	out := make(map[string]any, len(values))
	for _, v := range values {
		out[v.Wire] = wireOne(v, a)
	}
	return out
}

// wireOne renders one value in wire shape.
func wireOne(v Value, a Audience) any {
	switch {
	case v.Nested != nil && v.Kind == ir.TypeList:
		return []any{wireLevel(selected(v.Nested, a), a)}
	case v.Nested != nil:
		return wireLevel(selected(v.Nested, a), a)
	case v.Kind == ir.TypeList:
		return []any{v.Scalar}
	default:
		return v.Scalar
	}
}

// writeWireObject writes one JSON object with deterministic key order.
func writeWireObject(b *strings.Builder, values []Value, a Audience, depth int) {
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
func writeWireValue(b *strings.Builder, v Value, a Audience, depth int) {
	switch {
	case v.Nested != nil && v.Kind == ir.TypeList:
		b.WriteString("[\n" + strings.Repeat("  ", depth+1))
		writeWireObject(b, selected(v.Nested, a), a, depth+1)
		b.WriteString("\n" + strings.Repeat("  ", depth) + "]")
	case v.Nested != nil:
		writeWireObject(b, selected(v.Nested, a), a, depth)
	case v.Kind == ir.TypeList:
		b.WriteString("[" + jsonScalar(v.Scalar) + "]")
	default:
		b.WriteString(jsonScalar(v.Scalar))
	}
}

// jsonScalar renders one scalar as JSON; the values are all
// fixturespec-synthesised, so a marshal failure cannot happen.
func jsonScalar(value any) string {
	out, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(out)
}
