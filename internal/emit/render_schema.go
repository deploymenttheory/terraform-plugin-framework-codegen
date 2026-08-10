package emit

import (
	"fmt"
	"strconv"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
)

// schemaKind selects which terraform-plugin-framework schema package a
// declaration is rendered against. The three packages spell the same
// shapes, but only the resource one carries plan modifiers.
type schemaKind int

const (
	schemaResource schemaKind = iota
	schemaDatasource
	schemaAction
)

// schemaBuilder accumulates the imports one schema declaration needs as
// it renders.
type schemaBuilder struct {
	kind    schemaKind
	imports *importSet
}

// attributeDecls renders one level of attribute declarations, one line
// block per attribute, each ending with a comma — ready to sit inside a
// map[string]schema.Attribute literal.
func (sb *schemaBuilder) attributeDecls(nodes []node, depth int) string {
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(sb.attributeDecl(n, depth))
	}
	return b.String()
}

// attributeDecl renders one attribute declaration.
func (sb *schemaBuilder) attributeDecl(n node, depth int) string {
	indent := strings.Repeat("\t", depth)
	var b strings.Builder

	fmt.Fprintf(&b, "%s%q: schema.%s{\n", indent, n.attr.Name, sb.attributeType(n))
	b.WriteString(sb.presenceLines(n, indent+"\t"))

	if desc := attributeDescription(n.attr); desc != "" {
		fmt.Fprintf(&b, "%s\tDescription: %s,\n", indent, strconv.Quote(desc))
	}

	if n.attr.Kind == ir.TypeList && n.attr.Nested == nil {
		sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
		fmt.Fprintf(&b, "%s\tElementType: %s,\n", indent, frameworkElemType(n.attr.ElemKind))
	}

	if len(n.attr.OneOf) > 0 && n.attr.Kind == ir.TypeString && n.attr.Nested == nil {
		sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator")
		sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework/schema/validator")
		quoted := make([]string, len(n.attr.OneOf))
		for i, v := range n.attr.OneOf {
			quoted[i] = strconv.Quote(v)
		}
		fmt.Fprintf(&b, "%s\tValidators: []validator.String{stringvalidator.OneOf(%s)},\n",
			indent, strings.Join(quoted, ", "))
	}

	b.WriteString(sb.planModifierLines(n, indent+"\t"))

	if n.attr.Nested != nil {
		if n.attr.Kind == ir.TypeList {
			fmt.Fprintf(&b, "%s\tNestedObject: schema.NestedAttributeObject{\n", indent)
			fmt.Fprintf(&b, "%s\t\tAttributes: map[string]schema.Attribute{\n", indent)
			b.WriteString(sb.attributeDecls(n.children, depth+3))
			fmt.Fprintf(&b, "%s\t\t},\n%s\t},\n", indent, indent)
		} else {
			fmt.Fprintf(&b, "%s\tAttributes: map[string]schema.Attribute{\n", indent)
			b.WriteString(sb.attributeDecls(n.children, depth+2))
			fmt.Fprintf(&b, "%s\t},\n", indent)
		}
	}

	fmt.Fprintf(&b, "%s},\n", indent)
	return b.String()
}

// attributeType is the schema type name for one attribute.
func (sb *schemaBuilder) attributeType(n node) string {
	switch {
	case n.attr.Nested != nil && n.attr.Kind == ir.TypeList:
		return "ListNestedAttribute"
	case n.attr.Nested != nil:
		return "SingleNestedAttribute"
	case n.attr.Kind == ir.TypeList:
		return "ListAttribute"
	case n.attr.Kind == ir.TypeBool:
		return "BoolAttribute"
	case n.attr.Kind == ir.TypeInt64:
		return "Int64Attribute"
	case n.attr.Kind == ir.TypeFloat64:
		return "Float64Attribute"
	default:
		return "StringAttribute"
	}
}

// presenceLines renders the presence booleans. Inside an action schema
// everything writable stays as declared; inside a datasource, computed
// stays computed.
func (sb *schemaBuilder) presenceLines(n node, indent string) string {
	switch n.attr.Presence {
	case ir.PresenceRequired:
		return indent + "Required: true,\n"
	case ir.PresenceComputed:
		return indent + "Computed: true,\n"
	case ir.PresenceOptionalComputed:
		return indent + "Optional: true,\n" + indent + "Computed: true,\n"
	default:
		return indent + "Optional: true,\n"
	}
}

// planModifierLines renders the plan modifiers a resource attribute
// carries: RequiresReplace on create-only attributes, and
// UseStateForUnknown on the computed id — the one attribute whose value
// is stable across writes by definition.
func (sb *schemaBuilder) planModifierLines(n node, indent string) string {
	if sb.kind != schemaResource {
		return ""
	}

	var modifiers []string
	pkg := planModifierPackage(n)

	if n.attr.RequiresReplace {
		modifiers = append(modifiers, pkg+".RequiresReplace()")
	}
	if n.attr.Name == "id" && n.attr.Presence == ir.PresenceComputed && n.attr.Kind == ir.TypeString && n.attr.Nested == nil {
		modifiers = append(modifiers, pkg+".UseStateForUnknown()")
	}
	if len(modifiers) == 0 {
		return ""
	}

	sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier")
	sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework/resource/schema/"+pkg)
	return fmt.Sprintf("%sPlanModifiers: []planmodifier.%s{%s},\n",
		indent, planModifierValue(n), strings.Join(modifiers, ", "))
}

// planModifierPackage is the per-type plan modifier package name.
func planModifierPackage(n node) string {
	switch {
	case n.attr.Nested != nil && n.attr.Kind == ir.TypeList:
		return "listplanmodifier"
	case n.attr.Nested != nil:
		return "objectplanmodifier"
	case n.attr.Kind == ir.TypeList:
		return "listplanmodifier"
	case n.attr.Kind == ir.TypeBool:
		return "boolplanmodifier"
	case n.attr.Kind == ir.TypeInt64:
		return "int64planmodifier"
	case n.attr.Kind == ir.TypeFloat64:
		return "float64planmodifier"
	default:
		return "stringplanmodifier"
	}
}

// planModifierValue is the planmodifier interface name for one attribute.
func planModifierValue(n node) string {
	switch {
	case n.attr.Nested != nil && n.attr.Kind == ir.TypeList:
		return "List"
	case n.attr.Nested != nil:
		return "Object"
	case n.attr.Kind == ir.TypeList:
		return "List"
	case n.attr.Kind == ir.TypeBool:
		return "Bool"
	case n.attr.Kind == ir.TypeInt64:
		return "Int64"
	case n.attr.Kind == ir.TypeFloat64:
		return "Float64"
	default:
		return "String"
	}
}

// attributeDescription renders one attribute's schema description from
// what the document declares. The model carries no prose, so the
// description states the wire property and whatever behavioural facts the
// derivation recorded.
func attributeDescription(a ir.Attribute) string {
	parts := []string{"The " + a.WireName + " property."}
	if len(a.AdvisoryValues) > 0 {
		parts = append(parts, "Known values: "+strings.Join(a.AdvisoryValues, ", ")+".")
	}
	if a.RequiresReplace {
		parts = append(parts, "Changing this attribute forces replacement.")
	}
	if a.SilentlyIgnoredOnUpdate {
		parts = append(parts, "The API ignores this attribute on update.")
	}
	return strings.Join(parts, " ")
}

// frameworkElemType is the types package element type of a scalar list.
func frameworkElemType(k ir.TypeKind) string {
	switch k {
	case ir.TypeBool:
		return "types.BoolType"
	case ir.TypeInt64:
		return "types.Int64Type"
	case ir.TypeFloat64:
		return "types.Float64Type"
	default:
		return "types.StringType"
	}
}

// modelDecl is one rendered model struct declaration.
type modelDecl struct {
	name string
	body string
}

// buildModels renders the framework model structs for one entity: the
// root struct plus one struct per nested object shape, pre-order. The
// extra fields land in the root struct before the attribute fields.
func buildModels(rootName, typePrefix string, nodes []node, extraFields []string) ([]modelDecl, error) {
	claimed := map[string]string{}
	var decls []modelDecl

	var walk func(name, owner string, nodes []node, extra []string) error
	walk = func(name, owner string, nodes []node, extra []string) error {
		if prior, taken := claimed[name]; taken {
			return fmt.Errorf("attributes %s and %s would both declare model struct %s; rename one in the document",
				prior, owner, name)
		}
		claimed[name] = owner

		var b strings.Builder
		fmt.Fprintf(&b, "type %s struct {\n", name)
		for _, f := range extra {
			b.WriteString("\t" + f + "\n")
		}
		for _, n := range nodes {
			fmt.Fprintf(&b, "\t%s %s `tfsdk:%q`\n", ir.GoName(n.attr.Name), fieldType(typePrefix, n), n.attr.Name)
		}
		b.WriteString("}")
		decls = append(decls, modelDecl{name: name, body: b.String()})

		for _, n := range nodes {
			if n.attr.Nested == nil {
				continue
			}
			if err := walk(nestedModelName(typePrefix, n), owner+"."+n.attr.Name, n.children, nil); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(rootName, "the entity root", nodes, extraFields); err != nil {
		return nil, err
	}
	return decls, nil
}

// nestedModelName is the struct name one nested object maps to.
func nestedModelName(typePrefix string, n node) string {
	return typePrefix + ir.GoName(n.attr.Name) + "Model"
}

// fieldType is the Go type one model field carries.
func fieldType(typePrefix string, n node) string {
	switch {
	case n.attr.Nested != nil && n.attr.Kind == ir.TypeList:
		return "[]" + nestedModelName(typePrefix, n)
	case n.attr.Nested != nil:
		return "*" + nestedModelName(typePrefix, n)
	case n.attr.Kind == ir.TypeList:
		return "types.List"
	case n.attr.Kind == ir.TypeBool:
		return "types.Bool"
	case n.attr.Kind == ir.TypeInt64:
		return "types.Int64"
	case n.attr.Kind == ir.TypeFloat64:
		return "types.Float64"
	default:
		return "types.String"
	}
}

// renderModelDecls joins model declarations into one finished block.
func renderModelDecls(decls []modelDecl) string {
	parts := make([]string, len(decls))
	for i, d := range decls {
		parts[i] = d.body
	}
	return strings.Join(parts, "\n\n")
}
