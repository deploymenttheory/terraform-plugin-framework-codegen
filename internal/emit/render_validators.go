package emit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// render_validators.go realizes the attribute tree's cross-attribute rules as
// stock terraform-plugin-framework validator idioms. The mechanical edges use
// stock validators — mutually-exclusive groups become resourcevalidator.
// Conflicting in the resource's ConfigValidators, dependencies become
// attribute-level AlsoRequires in the schema — and the value-conditional edges,
// which no stock validator covers, become named custom validator types wired
// through the same ConfigValidators method, one type per edge keyed on its
// gate. The rule sets arrive in a fixed order from the IR, so every rendered
// expression is deterministic. Templates carry only the method skeleton — every
// statement here is a finished string.

// treeHasConfigValidators reports whether a tree declares any rule the resource
// realizes through its ConfigValidators method: the value-conditional customs
// and the mutually-exclusive stock Conflicting. Dependencies are excluded —
// they become attribute-level AlsoRequires on the subject attribute instead.
func treeHasConfigValidators(t *ir.AttributeTree) bool {
	return t != nil && (len(t.ConditionalRequirements) > 0 || len(t.ConditionalValidities) > 0 ||
		len(t.ValidConfigurations) > 0 || len(t.MutuallyExclusiveGroups) > 0)
}

// validatorNamer allocates a unique, deterministic Go identifier for each
// generated custom validator type. The name is keyed on the gate the edge
// enforces; a numeric suffix disambiguates a gate that carries more than one
// edge of the same kind.
type validatorNamer struct{ used map[string]int }

func (vn *validatorNamer) name(base string) string {
	if vn.used == nil {
		vn.used = map[string]int{}
	}
	n := vn.used[base]
	vn.used[base]++
	if n == 0 {
		return base + "Validator"
	}
	return fmt.Sprintf("%s%dValidator", base, n)
}

// configValidators renders the two halves of the conditional_validators file:
// the elements of the ConfigValidators return slice — one finished expression
// per line — and the named custom validator type declarations those elements
// refer to. typeName is the resource's Go type.
func configValidators(typeName string, t *ir.AttributeTree, nodes []node) (expressions, declarations string, err error) {
	byName := map[string]node{}
	for _, n := range nodes {
		byName[n.attribute.Name] = n
	}
	var declarationLines, expressionLines strings.Builder
	namer := &validatorNamer{}

	for _, request := range t.ConditionalRequirements {
		var body strings.Builder
		if err := emitRequiredWhen(&body, byName, request); err != nil {
			return "", "", err
		}
		name := namer.name(lowerFirst(ir.GoName(request.Property)) + "RequiredWhen")
		description := fmt.Sprintf("%s must be set when %s is %q.",
			strings.Join(request.Required, ", "), request.Property, request.WhenPropertyEquals)
		declarationLines.WriteString(renderCustomValidator(name, typeName, description, body.String()))
		fmt.Fprintf(&expressionLines, "\t\t%s{},\n", name)
	}
	for _, v := range t.ConditionalValidities {
		var body strings.Builder
		if err := emitValidWhen(&body, byName, v); err != nil {
			return "", "", err
		}
		name := namer.name(lowerFirst(ir.GoName(v.Property)) + "ValidWhen")
		description := fmt.Sprintf("%s may be set only when %s is %q.",
			strings.Join(v.AttributesValidWhenEqual, ", "), v.Property, v.WhenPropertyEquals)
		declarationLines.WriteString(renderCustomValidator(name, typeName, description, body.String()))
		fmt.Fprintf(&expressionLines, "\t\t%s{},\n", name)
	}
	for _, vc := range t.ValidConfigurations {
		var body strings.Builder
		if err := emitValidConfiguration(&body, byName, vc); err != nil {
			return "", "", err
		}
		name := namer.name(lowerFirst(ir.GoName(vc.Discriminator)) + "ValidConfiguration")
		description := fmt.Sprintf("only the attributes each %s value admits are set.", vc.Discriminator)
		declarationLines.WriteString(renderCustomValidator(name, typeName, description, body.String()))
		fmt.Fprintf(&expressionLines, "\t\t%s{},\n", name)
	}
	for _, group := range t.MutuallyExclusiveGroups {
		expr := conflictingExpr(byName, group)
		if expr == "" {
			continue
		}
		fmt.Fprintf(&expressionLines, "\t\t%s,\n", expr)
	}
	return expressionLines.String(), declarationLines.String(), nil
}

// renderCustomValidator renders one named custom validator type: an empty
// struct implementing resource.ConfigValidator whose ValidateResource fetches
// the config into the model and runs the edge's checks. body is the finished
// check block, already indented for a method body.
func renderCustomValidator(name, typeName, description, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s enforces that %s\n", name, description)
	fmt.Fprintf(&b, "type %s struct{}\n\n", name)
	fmt.Fprintf(&b, "func (v %s) Description(_ context.Context) string {\n\treturn %s\n}\n\n",
		name, strconv.Quote(description))
	fmt.Fprintf(&b, "func (v %s) MarkdownDescription(ctx context.Context) string {\n\treturn v.Description(ctx)\n}\n\n",
		name)
	fmt.Fprintf(&b, "func (v %s) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {\n",
		name)
	fmt.Fprintf(&b, "\tvar data %sModel\n", typeName)
	b.WriteString("\tresp.Diagnostics.Append(req.Config.Get(ctx, &data)...)\n")
	b.WriteString("\tif resp.Diagnostics.HasError() {\n\t\treturn\n\t}\n\n")
	b.WriteString(body)
	b.WriteString("}\n\n")
	return b.String()
}

// conflictingExpr renders the stock resourcevalidator.Conflicting for one
// mutually-exclusive group, validating every named attribute exists.
// A name the group carries that is not an attribute is dropped rather than
// refused: pruning removes attributes the SDK cannot carry, and a validator
// cannot address one that is not in the schema. A group left with fewer
// than two survivors constrains nothing and answers the empty string.
func conflictingExpr(byName map[string]node, group []string) string {
	paths := make([]string, 0, len(group))
	for _, name := range group {
		if _, ok := byName[name]; !ok {
			continue
		}
		paths = append(paths, fmt.Sprintf("path.MatchRoot(%q)", name))
	}
	if len(paths) < 2 {
		return ""
	}
	return fmt.Sprintf("resourcevalidator.Conflicting(%s)", strings.Join(paths, ", "))
}

// dependencyMap builds the subject→required map the schema builder turns into
// attribute-level AlsoRequires, validating that every named attribute exists.
// A dependency is asymmetric — the subject requires its partners, not the
// other way round — so AlsoRequires attached to the subject is the exact
// stock idiom, whereas RequiredTogether would over-constrain.
func dependencyMap(t *ir.AttributeTree, byName map[string]node) (map[string][]string, error) {
	if len(t.Dependencies) == 0 {
		return nil, nil
	}
	out := make(map[string][]string, len(t.Dependencies))
	for _, dep := range t.Dependencies {
		if _, ok := byName[dep.Attribute]; !ok {
			return nil, fmt.Errorf("dependency names %q, which is not an attribute", dep.Attribute)
		}
		for _, request := range dep.Requires {
			if _, ok := byName[request]; !ok {
				return nil, fmt.Errorf("dependency requires %q, which is not an attribute", request)
			}
		}
		out[dep.Attribute] = append(out[dep.Attribute], dep.Requires...)
	}
	return out, nil
}

// lowerFirst lowercases the first rune, turning an exported Go name into the
// unexported identifier a package-private validator type takes.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// emitRequiredWhen renders x-tfpfgen-required-when: when the gate holds the
// value, every dependent attribute must be set.
func emitRequiredWhen(b *strings.Builder, byName map[string]node, request ir.ConditionalRequirement) error {
	if err := stringGate(byName, request.Property, "conditional requirement"); err != nil {
		return err
	}
	fmt.Fprintf(b, "\tif data.%s.ValueString() == %s {\n", ir.GoName(request.Property), strconv.Quote(request.WhenPropertyEquals))
	for _, required := range request.Required {
		target, ok := byName[required]
		if !ok {
			return fmt.Errorf("conditional requirement requires %q, which is not an attribute", required)
		}
		fmt.Fprintf(b, "\t\tif %s {\n", nullCheck(target))
		writeError(b, "\t\t\t", required, "Missing required attribute",
			fmt.Sprintf("%s must be set when %s is %q.", required, request.Property, request.WhenPropertyEquals))
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
	fmt.Fprintf(b, "\tif data.%s.ValueString() != %s {\n", ir.GoName(v.Property), strconv.Quote(v.WhenPropertyEquals))
	for _, name := range v.AttributesValidWhenEqual {
		target, ok := byName[name]
		if !ok {
			return fmt.Errorf("conditional validity allows %q, which is not an attribute", name)
		}
		fmt.Fprintf(b, "\t\tif %s {\n", notNull(target))
		writeError(b, "\t\t\t", name, "Invalid attribute combination",
			fmt.Sprintf("%s is valid only when %s is %q.", name, v.Property, v.WhenPropertyEquals))
		b.WriteString("\t\t}\n")
	}
	b.WriteString("\t}\n")
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
		for _, f := range variant.AttributesValidWhenEqual {
			if _, seen := allowed[f]; !seen {
				fields = append(fields, f)
			}
			allowed[f] = append(allowed[f], variant.Value)
		}
	}
	sort.Strings(fields)
	discriminator := ir.GoName(vc.Discriminator)
	for _, f := range fields {
		target, ok := byName[f]
		if !ok {
			return fmt.Errorf("valid configuration admits %q, which is not an attribute", f)
		}
		conds := make([]string, len(allowed[f]))
		for i, v := range allowed[f] {
			conds[i] = fmt.Sprintf("data.%s.ValueString() != %s", discriminator, strconv.Quote(v))
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
//
// Either is a fact about this entity's document, so the entity is refused
// and the rest of the provider generates.
func stringGate(byName map[string]node, name, label string) error {
	gate, ok := byName[name]
	if !ok {
		return unrenderable(CauseUnvalidatableAttribute, "%s names %q, which is not an attribute", label, name)
	}
	if gate.attribute.Type != ir.TypeString || gate.attribute.NestedAttributes != nil {
		return unrenderable(CauseUnvalidatableAttribute, "%s on %q needs a string attribute", label, name)
	}
	return nil
}

// writeError renders one AddAttributeError call at the given indent.
func writeError(b *strings.Builder, indent, attribute, summary, detail string) {
	fmt.Fprintf(b, "%sresp.Diagnostics.AddAttributeError(path.Root(%q),\n", indent, attribute)
	fmt.Fprintf(b, "%s\t%s,\n", indent, strconv.Quote(summary))
	fmt.Fprintf(b, "%s\t%s)\n", indent, strconv.Quote(detail))
}

// nullCheck is the absent-value test for one attribute's model field.
// Every field is a framework value, nested ones included, so one test
// serves them all.
func nullCheck(n node) string {
	return "data." + ir.GoName(n.attribute.Name) + ".IsNull()"
}

// notNull is the present-value test, the negation of nullCheck.
func notNull(n node) string {
	return "!data." + ir.GoName(n.attribute.Name) + ".IsNull()"
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
// when the list response is a bare array. The observed key is used rather than
// a fixed one, because an API that wraps its collection under something other
// than "value" would otherwise get a mock the generated SDK cannot parse.
// slice is the expression yielding the item slice (a variable such as "items"
// or a literal such as "[]map[string]any{object()}").
func listPayloadExpr(listWrapperKey, slice string) string {
	if listWrapperKey == "" {
		return slice
	}
	return fmt.Sprintf("map[string]any{%s: %s}", strconv.Quote(listWrapperKey), slice)
}

// listResponseJSON renders the committed list-response fixture the list mock
// serves: the item wrapped under the observed envelope key, or a bare array
// when the response is a bare array. Mirrors listPayloadExpr for the fixture
// file, so the mock and the fixture agree on the envelope.
func listResponseJSON(listWrapperKey, item string) string {
	if listWrapperKey == "" {
		return "[\n" + reindentJSON(item, "  ") + "\n]\n"
	}
	return "{\n  " + strconv.Quote(listWrapperKey) + ": [\n" + reindentJSON(item, "    ") + "\n  ]\n}\n"
}
