package emit

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/code"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
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

	fmt.Fprintf(&b, "%s%q: schema.%s{\n", indent, n.attr.Name, schemaTypeOf(n).SchemaAttribute)
	b.WriteString(sb.computedOptionalRequiredLines(n, indent+"\t"))

	if desc := attributeDescription(n.attr); desc != "" {
		fmt.Fprintf(&b, "%s\tMarkdownDescription: %s,\n", indent, strconv.Quote(desc))
	}

	if n.attr.Kind == ir.TypeList && n.attr.Nested == nil {
		sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
		fmt.Fprintf(&b, "%s\tElementType: %s,\n", indent, schemaTypeOf(n).ElementType)
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

// validators is every stock validator one attribute carries, each a
// finished expression travelling with the imports it needs: the enum OneOf
// on a closed-set string, and the AlsoRequires that realizes a dependency
// whose subject is this root attribute.
//
// Nothing here registers an import. An expression that needs a package says
// so on the value it returns, and validatorLines is the single place those
// declarations are honoured — so a validator can never be rendered into a
// file whose import block forgot it.
func (sb *schemaBuilder) validators(n node, depth int) []code.CustomValidator {
	var validators []code.CustomValidator

	if len(n.attr.OneOf) > 0 && n.attr.Kind == ir.TypeString && n.attr.Nested == nil {
		quoted := make([]string, len(n.attr.OneOf))
		for i, v := range n.attr.OneOf {
			quoted[i] = strconv.Quote(v)
		}
		validators = append(validators, code.CustomValidator{
			Imports: []code.Import{
				{Path: "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"},
			},
			SchemaDefinition: fmt.Sprintf("stringvalidator.OneOf(%s)", strings.Join(quoted, ", ")),
		})
	}

	if depth == sb.rootDepth {
		if reqs, ok := sb.deps[n.attr.Name]; ok {
			schema := schemaTypeOf(n)
			paths := make([]string, len(reqs))
			for i, r := range reqs {
				paths[i] = fmt.Sprintf("path.MatchRoot(%q)", r)
			}
			validators = append(validators, code.CustomValidator{
				Imports: []code.Import{
					schema.ValidatorImport,
					{Path: "github.com/hashicorp/terraform-plugin-framework/path"},
				},
				SchemaDefinition: fmt.Sprintf("%s.AlsoRequires(%s)", schema.ValidatorPackage(), strings.Join(paths, ", ")),
			})
		}
	}

	return validators
}

// validatorLines renders one attribute's Validators declaration, combining
// every validator it carries into a single typed slice and registering the
// imports those validators declared. Empty when the attribute carries none.
func (sb *schemaBuilder) validatorLines(n node, indent string, depth int) string {
	validators := sb.validators(n, depth)
	if len(validators) == 0 {
		return ""
	}

	definitions := make([]string, len(validators))
	for i, v := range validators {
		sb.imports.addImports(v.Imports)
		definitions[i] = v.SchemaDefinition
	}
	sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework/schema/validator")
	return fmt.Sprintf("%sValidators: []validator.%s{%s},\n",
		indent, schemaTypeOf(n).Validator, strings.Join(definitions, ", "))
}

// computedOptionalRequiredLines renders the presence booleans. Inside a datasource, computed
// stays computed. Inside an action there is no Computed to render: the
// action package's attribute types do not declare the field, because an
// invocation has arguments and a result and nothing in between for the
// framework to fill in. An attribute that is writable as well keeps the
// writable half; one that is only computed is dropped before it reaches
// here.
func (sb *schemaBuilder) computedOptionalRequiredLines(n node, indent string) string {
	switch n.attr.ComputedOptionalRequired {
	case ir.Required:
		return indent + "Required: true,\n"
	case ir.Computed:
		if sb.kind == schemaAction {
			return indent + "Optional: true,\n"
		}
		return indent + "Computed: true,\n"
	case ir.ComputedOptional:
		if sb.kind == schemaAction {
			return indent + "Optional: true,\n"
		}
		return indent + "Optional: true,\n" + indent + "Computed: true,\n"
	default:
		return indent + "Optional: true,\n"
	}
}

// planModifiers is every plan modifier one resource attribute carries,
// each a finished expression travelling with the imports it needs:
// RequiresReplace on create-only attributes, and UseStateForUnknown on the
// computed id — the one attribute whose value is stable across writes by
// definition. Empty for a datasource or an action, neither of which has
// plan modifiers at all.
func (sb *schemaBuilder) planModifiers(n node) []code.CustomPlanModifier {
	if sb.kind != schemaResource {
		return nil
	}

	schema := schemaTypeOf(n)
	pkg := schema.PlanModifierPackage()
	imports := []code.Import{
		{Path: "github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"},
		schema.PlanModifierImport,
	}

	var modifiers []code.CustomPlanModifier
	if n.attr.RequiresReplace {
		modifiers = append(modifiers, code.CustomPlanModifier{
			Imports:          imports,
			SchemaDefinition: pkg + ".RequiresReplace()",
		})
	}
	if n.attr.Name == "id" && n.attr.ComputedOptionalRequired == ir.Computed && n.attr.Kind == ir.TypeString && n.attr.Nested == nil {
		modifiers = append(modifiers, code.CustomPlanModifier{
			Imports:          imports,
			SchemaDefinition: pkg + ".UseStateForUnknown()",
		})
	}
	return modifiers
}

// planModifierLines renders one attribute's PlanModifiers declaration,
// combining every modifier it carries into a single typed slice and
// registering the imports those modifiers declared.
func (sb *schemaBuilder) planModifierLines(n node, indent string) string {
	modifiers := sb.planModifiers(n)
	if len(modifiers) == 0 {
		return ""
	}
	schema := schemaTypeOf(n)

	definitions := make([]string, len(modifiers))
	for i, m := range modifiers {
		sb.imports.addImports(m.Imports)
		definitions[i] = m.SchemaDefinition
	}
	return fmt.Sprintf("%sPlanModifiers: []planmodifier.%s{%s},\n",
		indent, schema.PlanModifier, strings.Join(definitions, ", "))
}

// attributeDescription renders one attribute's schema description: the
// document's own prose first, then the facts the derivation established
// about how the attribute behaves.
//
// The order is the point. The document's sentence is the only human-written
// text in the whole pipeline and it is what a practitioner actually needs;
// the inferred facts qualify it. Where the document says nothing — and real
// ones often do not, one pilot annotating 12% of its properties against
// another's 52% — the wire property name stands in, which is no worse than
// what was rendered before and no better.
func attributeDescription(a ir.Attribute) string {
	var parts []string
	if declared := strings.TrimSpace(a.Description); declared != "" {
		parts = append(parts, terminated(declared))
	} else {
		parts = append(parts, "The "+a.WireName+" property.")
	}
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

// terminated ends a borrowed sentence with a full stop, so the facts
// appended after it do not run on. A sentence already ending in punctuation
// is left as the document wrote it.
func terminated(sentence string) string {
	switch sentence[len(sentence)-1] {
	case '.', '!', '?', ':', ';':
		return sentence
	}
	return sentence + "."
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
func buildModels(rootName, typePrefix string, nodes []node, extraFields []string) []modelDecl {
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
	return decls
}

// fieldType is the Go type one model field carries.
// A nested attribute's model field is a generated struct, so only the
// nesting shape is decided here; everything else is the record's ValueType.
func fieldType(namer *modelNamer, path string, n node) string {
	switch {
	case n.attr.Nested != nil && n.attr.Kind == ir.TypeList:
		return "[]" + namer.name(path)
	case n.attr.Nested != nil:
		return "*" + namer.name(path)
	default:
		return schemaTypeOf(n).ValueType
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

// entityDescription is an entity's schema description: the derived sentence
// saying what the terraform surface does, then the document's own prose
// about the object it does it to.
//
// Both earn their place. The derived sentence is the only one that says
// whether this is a resource, a lookup or a filtered list — the document has
// no idea Terraform exists. The document's sentence is the only human-written
// text in the pipeline, and where it exists it is what a practitioner needs.
// Neither is a substitute for the other, so both are rendered.
func entityDescription(tree *ir.AttributeTree, derived string) string {
	if tree == nil || strings.TrimSpace(tree.Description) == "" {
		return derived
	}
	return derived + " " + terminated(strings.TrimSpace(tree.Description))
}
