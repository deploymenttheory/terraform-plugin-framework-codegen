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
	// deps maps a subject attribute to the attributes it requires, realized
	// as attribute-level AlsoRequires. Set only for a resource schema, and
	// applied only to the root attributes the dependencies name.
	deps map[string][]string
	// rootDepth is the depth the root attributes render at, so AlsoRequires
	// lands on the top-level subject and never on a same-named nested one.
	rootDepth int
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
		fmt.Fprintf(&b, "%s\tElementType: %s,\n", indent, frameworkElemType(n.attr.ElementKind))
	}

	b.WriteString(sb.validatorLines(n, indent+"\t", depth))

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

// validatorLines renders one attribute's Validators declaration, combining
// every stock validator it carries into a single typed slice: the enum OneOf
// on a closed-set string, and the AlsoRequires that realizes a dependency
// whose subject is this root attribute. Empty when the attribute carries none.
func (sb *schemaBuilder) validatorLines(n node, indent string, depth int) string {
	var exprs []string

	if len(n.attr.OneOf) > 0 && n.attr.Kind == ir.TypeString && n.attr.Nested == nil {
		sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator")
		quoted := make([]string, len(n.attr.OneOf))
		for i, v := range n.attr.OneOf {
			quoted[i] = strconv.Quote(v)
		}
		exprs = append(exprs, fmt.Sprintf("stringvalidator.OneOf(%s)", strings.Join(quoted, ", ")))
	}

	if depth == sb.rootDepth {
		if reqs, ok := sb.deps[n.attr.Name]; ok {
			pkg := validatorPackage(n)
			sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework-validators/"+pkg)
			sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework/path")
			paths := make([]string, len(reqs))
			for i, r := range reqs {
				paths[i] = fmt.Sprintf("path.MatchRoot(%q)", r)
			}
			exprs = append(exprs, fmt.Sprintf("%s.AlsoRequires(%s)", pkg, strings.Join(paths, ", ")))
		}
	}

	if len(exprs) == 0 {
		return ""
	}
	sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework/schema/validator")
	return fmt.Sprintf("%sValidators: []validator.%s{%s},\n",
		indent, planModifierValue(n), strings.Join(exprs, ", "))
}

// validatorPackage is the per-type validator package name, mirroring the
// per-type plan modifier packages.
func validatorPackage(n node) string {
	switch {
	case n.attr.Nested != nil && n.attr.Kind == ir.TypeList:
		return "listvalidator"
	case n.attr.Nested != nil:
		return "objectvalidator"
	case n.attr.Kind == ir.TypeList:
		return "listvalidator"
	case n.attr.Kind == ir.TypeBool:
		return "boolvalidator"
	case n.attr.Kind == ir.TypeInt64:
		return "int64validator"
	case n.attr.Kind == ir.TypeFloat64:
		return "float64validator"
	default:
		return "stringvalidator"
	}
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

// presenceLines renders the presence booleans. Inside a datasource, computed
// stays computed. Inside an action there is no Computed to render: the
// action package's attribute types do not declare the field, because an
// invocation has arguments and a result and nothing in between for the
// framework to fill in. An attribute that is writable as well keeps the
// writable half; one that is only computed is dropped before it reaches
// here.
func (sb *schemaBuilder) presenceLines(n node, indent string) string {
	switch n.attr.Presence {
	case ir.PresenceRequired:
		return indent + "Required: true,\n"
	case ir.PresenceComputed:
		if sb.kind == schemaAction {
			return indent + "Optional: true,\n"
		}
		return indent + "Computed: true,\n"
	case ir.PresenceOptionalComputed:
		if sb.kind == schemaAction {
			return indent + "Optional: true,\n"
		}
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
func frameworkElemType(k ir.AttributeType) string {
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

// modelNamer assigns every nested object in one entity's tree the Go struct
// name its model declaration and every reference to it use.
//
// The short spelling — the type prefix, the attribute's Go name, "Model" —
// is what a tree gets whenever it is the only nesting site claiming it. Two
// sites claiming one spelling used to be a hard error naming both paths and
// telling the operator to "rename one in the document", which is not
// something a vendor's document will do: a real document nests an object of
// one name at two depths of one entity, and that single collision aborted
// every resource the provider had. Every claimant of a contested spelling is
// therefore qualified by its ancestor path instead, and an uncontested one is
// left exactly as it was — so qualification shows up only where a collision
// made it necessary.
//
// Which sites are contested is decided from the whole tree before any name
// is handed out, so the answer never depends on the order names are asked
// for, and regeneration stays byte-identical.
type modelNamer struct {
	typePrefix string
	byPath     map[string]string
}

// newModelNamer resolves one entity's nested model names. The path key is
// the attribute chain from the root, dot-separated.
func newModelNamer(typePrefix string, nodes []node) *modelNamer {
	shortOf := map[string]string{}
	claimants := map[string]int{}

	var survey func(parent string, nodes []node)
	survey = func(parent string, nodes []node) {
		for _, n := range nodes {
			if n.attr.Nested == nil {
				continue
			}
			path := n.attr.Name
			if parent != "" {
				path = parent + "." + n.attr.Name
			}
			short := typePrefix + ir.GoName(n.attr.Name) + "Model"
			shortOf[path] = short
			claimants[short]++
			survey(path, n.children)
		}
	}
	survey("", nodes)

	namer := &modelNamer{typePrefix: typePrefix, byPath: make(map[string]string, len(shortOf))}
	for path, short := range shortOf {
		if claimants[short] == 1 {
			namer.byPath[path] = short
			continue
		}
		var qualified strings.Builder
		qualified.WriteString(typePrefix)
		for _, segment := range strings.Split(path, ".") {
			qualified.WriteString(ir.GoName(segment))
		}
		qualified.WriteString("Model")
		namer.byPath[path] = qualified.String()
	}
	return namer
}

// name is the struct name the nested object at one attribute path maps to.
func (nm *modelNamer) name(path string) string {
	if resolved, ok := nm.byPath[path]; ok {
		return resolved
	}
	// A path the survey never saw cannot arise from a tree the survey
	// walked; spell it the short way rather than returning nothing.
	segments := strings.Split(path, ".")
	return nm.typePrefix + ir.GoName(segments[len(segments)-1]) + "Model"
}

// childPath extends an attribute path by one segment.
func childPath(parent string, n node) string {
	if parent == "" {
		return n.attr.Name
	}
	return parent + "." + n.attr.Name
}

// buildModels renders the framework model structs for one entity: the
// root struct plus one struct per nested object shape, pre-order. The
// extra fields land in the root struct before the attribute fields.
func buildModels(rootName, typePrefix string, nodes []node, extraFields []string) ([]modelDecl, error) {
	namer := newModelNamer(typePrefix, nodes)
	var decls []modelDecl

	var walk func(name, path string, nodes []node, extra []string)
	walk = func(name, path string, nodes []node, extra []string) {
		var b strings.Builder
		fmt.Fprintf(&b, "type %s struct {\n", name)
		for _, f := range extra {
			b.WriteString("\t" + f + "\n")
		}
		for _, n := range nodes {
			fmt.Fprintf(&b, "\t%s %s `tfsdk:%q`\n",
				ir.GoName(n.attr.Name), fieldType(namer, childPath(path, n), n), n.attr.Name)
		}
		b.WriteString("}")
		decls = append(decls, modelDecl{name: name, body: b.String()})

		for _, n := range nodes {
			if n.attr.Nested == nil {
				continue
			}
			nested := childPath(path, n)
			walk(namer.name(nested), nested, n.children, nil)
		}
	}

	walk(rootName, "", nodes, extraFields)
	return decls, nil
}

// fieldType is the Go type one model field carries.
func fieldType(namer *modelNamer, path string, n node) string {
	switch {
	case n.attr.Nested != nil && n.attr.Kind == ir.TypeList:
		return "[]" + namer.name(path)
	case n.attr.Nested != nil:
		return "*" + namer.name(path)
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
