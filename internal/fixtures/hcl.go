package fixtures

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// HCL renders the form's values as the body of a terraform block:
// attribute assignments, two-space indented one level deep, aligned the
// way terraform fmt aligns them. The caller wraps the body in its own
// block header and footer.
func (s Fixture) HCL(a Form) string {
	var b strings.Builder
	writeHCLLevel(&b, s.topLevel(a), a, 1)
	return b.String()
}

// writeHCLLevel renders one level of attribute assignments. Alignment
// follows terraform fmt: consecutive single-line assignments align their
// equals signs, and a multi-line value ends the run.
func writeHCLLevel(b *strings.Builder, values []Entry, a Form, depth int) {
	indent := strings.Repeat("  ", depth)

	flushRun := func(run []Entry) {
		width := 0
		for _, v := range run {
			if len(v.Name) > width {
				width = len(v.Name)
			}
		}
		for _, v := range run {
			fmt.Fprintf(b, "%s%-*s = %s\n", indent, width, v.Name, scalarHCL(v))
		}
	}

	var run []Entry
	for _, v := range values {
		if v.Nested == nil {
			run = append(run, v)
			continue
		}
		flushRun(run)
		run = nil
		writeNestedHCL(b, v, a, depth)
	}
	flushRun(run)
}

// writeNestedHCL renders an object attribute, a list of objects or a map of
// objects as a multi-line value.
func writeNestedHCL(b *strings.Builder, v Entry, a Form, depth int) {
	indent := strings.Repeat("  ", depth)
	nested := selected(v.Nested, a)

	if v.Kind == ir.TypeList {
		fmt.Fprintf(b, "%s%s = [\n%s  {\n", indent, v.Name, indent)
		writeHCLLevel(b, nested, a, depth+2)
		fmt.Fprintf(b, "%s  },\n%s]\n", indent, indent)
		return
	}

	// One entry, keyed by the attribute's own name: a map's keys are the
	// practitioner's, so the specification names none to take.
	if v.Kind == ir.TypeMap {
		fmt.Fprintf(b, "%s%s = {\n%s  %q = {\n", indent, v.Name, indent, v.Name)
		writeHCLLevel(b, nested, a, depth+2)
		fmt.Fprintf(b, "%s  }\n%s}\n", indent, indent)
		return
	}

	fmt.Fprintf(b, "%s%s = {\n", indent, v.Name)
	writeHCLLevel(b, nested, a, depth+1)
	fmt.Fprintf(b, "%s}\n", indent)
}

// scalarHCL renders a scalar value, or the single-element list carrying
// one.
func scalarHCL(v Entry) string {
	if v.Expression != "" {
		if v.Kind == ir.TypeList {
			return "[" + v.Expression + "]"
		}
		return v.Expression
	}
	literal := hclLiteral(v.Scalar)
	switch v.Kind {
	case ir.TypeList:
		return "[" + literal + "]"
	case ir.TypeMap:
		// A replayed body carries the keys the API took, and they are the
		// point: rendering them as one synthetic entry sends a map the API
		// never saw. A derived fixture has none, because a map's keys are the
		// practitioner's and the document names none to take, so it falls
		// back to one entry keyed by the attribute's own name.
		if carried, ok := v.Scalar.(map[string]any); ok {
			return mapHCL(carried)
		}
		return "{ " + hclLiteral(v.Name) + " = " + literal + " }"
	}
	return literal
}

// mapHCL renders a map's own entries, in key order so a regenerated fixture
// is byte-identical.
func mapHCL(carried map[string]any) string {
	keys := make([]string, 0, len(carried))
	for key := range carried {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, hclLiteral(key)+" = "+hclLiteral(carried[key]))
	}
	return "{ " + strings.Join(entries, ", ") + " }"
}

// hclLiteral renders one plain value as HCL.
func hclLiteral(value any) string {
	switch v := value.(type) {
	case bool:
		return strconv.FormatBool(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return strconv.Quote(fmt.Sprintf("%v", v))
	}
}
