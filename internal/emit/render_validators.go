package emit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
)

// render_validators.go builds the resource ValidateConfig body from the
// attribute tree's cross-attribute rules. Each rule becomes exactly one check,
// keyed on its gate and erroring with a path expression at plan time; common
// attributes carry no validator. The rule sets arrive in a fixed order from
// the IR, so the rendered body is deterministic. Templates carry only the
// method skeleton — every statement here is a finished string.

// treeHasValidators reports whether a tree declares any cross-attribute rule
// the ValidateConfig method must enforce.
func treeHasValidators(t *ir.AttributeTree) bool {
	return t != nil && (len(t.ConditionalRequirements) > 0 || len(t.ConditionalValidities) > 0 ||
		len(t.Dependencies) > 0 || len(t.MutuallyExclusiveGroups) > 0 || len(t.ValidConfigurations) > 0)
}

// validatorBody renders the ValidateConfig body: the requirement, validity,
// dependency, exclusion and variant checks, in that fixed order.
func validatorBody(t *ir.AttributeTree, nodes []node) (string, error) {
	byName := map[string]node{}
	for _, n := range nodes {
		byName[n.attr.Name] = n
	}
	var b strings.Builder
	for _, req := range t.ConditionalRequirements {
		if err := emitRequiredWhen(&b, byName, req); err != nil {
			return "", err
		}
	}
	for _, v := range t.ConditionalValidities {
		if err := emitValidWhen(&b, byName, v); err != nil {
			return "", err
		}
	}
	for _, dep := range t.Dependencies {
		if err := emitDependsOn(&b, byName, dep); err != nil {
			return "", err
		}
	}
	for _, group := range t.MutuallyExclusiveGroups {
		if err := emitMutuallyExclusive(&b, byName, group); err != nil {
			return "", err
		}
	}
	for _, vc := range t.ValidConfigurations {
		if err := emitValidConfiguration(&b, byName, vc); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// emitRequiredWhen renders x-tfpfgen-required-when: when the gate holds the
// value, every dependent attribute must be set.
func emitRequiredWhen(b *strings.Builder, byName map[string]node, req ir.ConditionalRequirement) error {
	if err := stringGate(byName, req.Property, "conditional requirement"); err != nil {
		return err
	}
	fmt.Fprintf(b, "\tif data.%s.ValueString() == %s {\n", ir.GoName(req.Property), strconv.Quote(req.Equals))
	for _, required := range req.Required {
		target, ok := byName[required]
		if !ok {
			return fmt.Errorf("conditional requirement requires %q, which is not an attribute", required)
		}
		fmt.Fprintf(b, "\t\tif %s {\n", nullCheck(target))
		writeError(b, "\t\t\t", required, "Missing required attribute",
			fmt.Sprintf("%s must be set when %s is %q.", required, req.Property, req.Equals))
		b.WriteString("\t\t}\n")
	}
	b.WriteString("\t}\n")
	return nil
}

// emitValidWhen renders x-tfpfgen-valid-when: while the gate does not hold the
// value, none of the gated attributes may be set.
func emitValidWhen(b *strings.Builder, byName map[string]node, v ir.ConditionalValidity) error {
	if err := stringGate(byName, v.Property, "conditional validity"); err != nil {
		return err
	}
	fmt.Fprintf(b, "\tif data.%s.ValueString() != %s {\n", ir.GoName(v.Property), strconv.Quote(v.Equals))
	for _, name := range v.Valid {
		target, ok := byName[name]
		if !ok {
			return fmt.Errorf("conditional validity allows %q, which is not an attribute", name)
		}
		fmt.Fprintf(b, "\t\tif %s {\n", notNull(target))
		writeError(b, "\t\t\t", name, "Invalid attribute combination",
			fmt.Sprintf("%s is valid only when %s is %q.", name, v.Property, v.Equals))
		b.WriteString("\t\t}\n")
	}
	b.WriteString("\t}\n")
	return nil
}

// emitDependsOn renders x-tfpfgen-depends-on / dependentRequired: an attribute
// may be set only when every attribute it requires is set too.
func emitDependsOn(b *strings.Builder, byName map[string]node, dep ir.Dependency) error {
	subject, ok := byName[dep.Attribute]
	if !ok {
		return fmt.Errorf("dependency names %q, which is not an attribute", dep.Attribute)
	}
	fmt.Fprintf(b, "\tif %s {\n", notNull(subject))
	for _, req := range dep.Requires {
		target, ok := byName[req]
		if !ok {
			return fmt.Errorf("dependency requires %q, which is not an attribute", req)
		}
		fmt.Fprintf(b, "\t\tif %s {\n", nullCheck(target))
		writeError(b, "\t\t\t", dep.Attribute, "Missing dependency",
			fmt.Sprintf("%s may be set only when %s is also set.", dep.Attribute, req))
		b.WriteString("\t\t}\n")
	}
	b.WriteString("\t}\n")
	return nil
}

// emitMutuallyExclusive renders x-tfpfgen-mutually-exclusive: at most one of
// the group may be set, checked pairwise so each conflict names both sides.
func emitMutuallyExclusive(b *strings.Builder, byName map[string]node, group []string) error {
	for i := 0; i < len(group); i++ {
		a, ok := byName[group[i]]
		if !ok {
			return fmt.Errorf("mutually-exclusive group names %q, which is not an attribute", group[i])
		}
		for j := i + 1; j < len(group); j++ {
			other, ok := byName[group[j]]
			if !ok {
				return fmt.Errorf("mutually-exclusive group names %q, which is not an attribute", group[j])
			}
			fmt.Fprintf(b, "\tif %s && %s {\n", notNull(a), notNull(other))
			writeError(b, "\t\t", group[i], "Conflicting attributes",
				fmt.Sprintf("%s and %s are mutually exclusive; set at most one.", group[i], group[j]))
			b.WriteString("\t}\n")
		}
	}
	return nil
}

// emitValidConfiguration renders x-tfpfgen-valid-configuration: a
// variant-specific attribute may be set only when the discriminator holds a
// value whose variant admits it.
func emitValidConfiguration(b *strings.Builder, byName map[string]node, vc ir.ValidConfiguration) error {
	if err := stringGate(byName, vc.Discriminator, "valid configuration"); err != nil {
		return err
	}
	// allowed maps each variant-specific attribute to the discriminator
	// values whose variant admits it, in sorted order for a stable render.
	allowed := map[string][]string{}
	var fields []string
	for _, variant := range vc.Variants {
		for _, f := range variant.Valid {
			if _, seen := allowed[f]; !seen {
				fields = append(fields, f)
			}
			allowed[f] = append(allowed[f], variant.Value)
		}
	}
	sort.Strings(fields)
	disc := ir.GoName(vc.Discriminator)
	for _, f := range fields {
		target, ok := byName[f]
		if !ok {
			return fmt.Errorf("valid configuration admits %q, which is not an attribute", f)
		}
		conds := make([]string, len(allowed[f]))
		for i, v := range allowed[f] {
			conds[i] = fmt.Sprintf("data.%s.ValueString() != %s", disc, strconv.Quote(v))
		}
		fmt.Fprintf(b, "\tif %s && %s {\n", notNull(target), strings.Join(conds, " && "))
		writeError(b, "\t\t", f, "Invalid attribute for configuration",
			fmt.Sprintf("%s is valid only when %s is %s.", f, vc.Discriminator, orList(allowed[f])))
		b.WriteString("\t}\n")
	}
	return nil
}

// stringGate refuses a gate attribute the generated comparison cannot read: a
// missing one or a non-string one. label names the rule in the error.
func stringGate(byName map[string]node, name, label string) error {
	gate, ok := byName[name]
	if !ok {
		return fmt.Errorf("%s names %q, which is not an attribute", label, name)
	}
	if gate.attr.Kind != ir.TypeString || gate.attr.Nested != nil {
		return fmt.Errorf("%s on %q needs a string attribute", label, name)
	}
	return nil
}

// writeError renders one AddAttributeError call at the given indent.
func writeError(b *strings.Builder, indent, attr, summary, detail string) {
	fmt.Fprintf(b, "%sresp.Diagnostics.AddAttributeError(path.Root(%q),\n", indent, attr)
	fmt.Fprintf(b, "%s\t%s,\n", indent, strconv.Quote(summary))
	fmt.Fprintf(b, "%s\t%s)\n", indent, strconv.Quote(detail))
}

// nullCheck is the absent-value test for one attribute's model field.
func nullCheck(n node) string {
	field := "data." + ir.GoName(n.attr.Name)
	if n.attr.Nested != nil {
		return field + " == nil"
	}
	return field + ".IsNull()"
}

// notNull is the present-value test, the negation of nullCheck.
func notNull(n node) string {
	field := "data." + ir.GoName(n.attr.Name)
	if n.attr.Nested != nil {
		return field + " != nil"
	}
	return "!" + field + ".IsNull()"
}

// orList renders a set of values as human prose: "a", "a or b",
// "a, b or c".
func orList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = strconv.Quote(v)
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " or " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
	}
}

// listPayloadExpr is the finished Go expression the list mock answers with:
// the item slice wrapped under the observed envelope key, or the bare slice
// when the list response is a bare array. It closes the ListWrap defect — the
// generated mock no longer assumes every API wraps its collection under
// "value". slice is the expression yielding the item slice (a variable such
// as "items" or a literal such as "[]map[string]any{object()}").
func listPayloadExpr(envelopeKey, slice string) string {
	if envelopeKey == "" {
		return slice
	}
	return fmt.Sprintf("map[string]any{%s: %s}", strconv.Quote(envelopeKey), slice)
}

// listResponseJSON renders the committed list-response fixture the list mock
// serves: the item wrapped under the observed envelope key, or a bare array
// when the response is a bare array. Mirrors listPayloadExpr for the fixture
// file, so the mock and the fixture agree on the envelope.
func listResponseJSON(envelopeKey, item string) string {
	if envelopeKey == "" {
		return "[\n" + reindentJSON(item, "  ") + "\n]\n"
	}
	return "{\n  " + strconv.Quote(envelopeKey) + ": [\n" + reindentJSON(item, "    ") + "\n  ]\n}\n"
}
