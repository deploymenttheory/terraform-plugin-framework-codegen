package fixtures

import (
	"fmt"
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

// writeNestedHCL renders an object attribute or a list of objects as a
// multi-line value.
func writeNestedHCL(b *strings.Builder, v Entry, a Form, depth int) {
	indent := strings.Repeat("  ", depth)
	nested := selected(v.Nested, a)

	if v.Kind == ir.TypeList {
		fmt.Fprintf(b, "%s%s = [\n%s  {\n", indent, v.Name, indent)
		writeHCLLevel(b, nested, a, depth+2)
		fmt.Fprintf(b, "%s  },\n%s]\n", indent, indent)
		return
	}

	fmt.Fprintf(b, "%s%s = {\n", indent, v.Name)
	writeHCLLevel(b, nested, a, depth+1)
	fmt.Fprintf(b, "%s}\n", indent)
}

// scalarHCL renders a scalar value, or the single-element list carrying
// one.
func scalarHCL(v Entry) string {
	literal := hclLiteral(v.Scalar)
	if v.Kind == ir.TypeList {
		return "[" + literal + "]"
	}
	return literal
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
