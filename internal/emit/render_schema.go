package emit

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/code"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// schemaKind selects which terraform-plugin-framework schema package a
// declaration is rendered against. The four packages spell the same
// shapes, but only the resource one carries plan modifiers, and only the
// resource and datasource ones carry Computed.
type schemaKind int

const (
	schemaResource schemaKind = iota
	schemaDatasource
	schemaAction
	schemaListResource
)

// pkg is the package name a schema declaration qualifies its attribute types
// with. Three of the four packages are imported under the framework's own
// name; list/schema is imported as listschema, because a list resource file
// also names the resource schema package.
func (sb *schemaBuilder) goPackage() string {
	if sb.kind == schemaListResource {
		return "listschema"
	}
	return "schema"
}

// rendersComputed reports whether the schema package declares Computed at
// all. An action's does not — an invocation has arguments and a result and
// nothing in between for the framework to fill in — and a list resource's
// does not either: every list/schema attribute answers false from IsComputed.
func (sb *schemaBuilder) rendersComputed() bool {
	return sb.kind == schemaResource || sb.kind == schemaDatasource
}

// rendersSensitive reports whether the schema package declares Sensitive.
// The action and list packages do not: their attribute types carry
// DeprecationMessage but no Sensitive field, so declaring one would not
// compile. A secret passed as an action argument or a list filter is
// therefore unmarked, which is the framework's limit rather than a choice
// made here.
//
// The membership matches rendersComputed's today and is spelled separately
// because nothing ties the two facts together.
func (sb *schemaBuilder) rendersSensitive() bool {
	return sb.kind == schemaResource || sb.kind == schemaDatasource
}

// deprecationMessage is what a generated schema says about an attribute the
// document declares deprecated. OpenAPI's deprecated is a bare flag carrying
// no prose, and the framework's DeprecationMessage is the warning text a
// practitioner reads, so the sentence is the toolkit's own and is fixed.
const deprecationMessage = "This attribute is deprecated and may be removed in a future API version."

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

// attributeDeclarations renders one level of attribute declarations, one line
// block per attribute, each ending with a comma — ready to sit inside a
// map[string]schema.Attribute literal.
func (sb *schemaBuilder) attributeDeclarations(nodes []node, depth int) string {
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(sb.attributeDeclaration(n, depth))
	}
	return b.String()
}

// attributeDeclaration renders one attribute declaration.
func (sb *schemaBuilder) attributeDeclaration(n node, depth int) string {
	indent := strings.Repeat("\t", depth)
	var b strings.Builder

	fmt.Fprintf(&b, "%s%q: %s.%s{\n", indent, n.attribute.Name, sb.goPackage(), schemaTypeOf(n).SchemaAttribute)
	b.WriteString(sb.computedOptionalRequiredLines(n, indent+"\t"))

	if description := attributeDescription(n.attribute, n.fb != nil && n.fb.KeptFromPlan); description != "" {
		fmt.Fprintf(&b, "%s\tMarkdownDescription: %s,\n", indent, strconv.Quote(description))
	}

	// Sensitive keeps the value out of plan output and logs. It is a plain
	// bool on the attribute type, so unlike a validator or a plan modifier
	// it needs no import to travel with it.
	if n.attribute.Sensitive && sb.rendersSensitive() {
		fmt.Fprintf(&b, "%s\tSensitive: true,\n", indent)
	}

	if n.attribute.Deprecated {
		fmt.Fprintf(&b, "%s\tDeprecationMessage: %s,\n", indent, strconv.Quote(deprecationMessage))
	}

	if (n.attribute.Type == ir.TypeList || n.attribute.Type == ir.TypeMap) && n.attribute.NestedAttributes == nil {
		sb.imports.add("", "github.com/hashicorp/terraform-plugin-framework/types")
		fmt.Fprintf(&b, "%s\tElementType: %s,\n", indent, schemaTypeOf(n).ElementType)
	}

	b.WriteString(sb.validatorLines(n, indent+"\t", depth))

	b.WriteString(sb.planModifierLines(n, indent+"\t"))

	if n.attribute.NestedAttributes != nil {
		// A list and a map both wrap their attributes in a
		// NestedAttributeObject; only a single nested object carries them
		// directly.
		if n.attribute.Type == ir.TypeList || n.attribute.Type == ir.TypeMap {
			fmt.Fprintf(&b, "%s\tNestedObject: %s.NestedAttributeObject{\n", indent, sb.goPackage())
			fmt.Fprintf(&b, "%s\t\tAttributes: map[string]%s.Attribute{\n", indent, sb.goPackage())
			b.WriteString(sb.attributeDeclarations(n.children, depth+3))
			fmt.Fprintf(&b, "%s\t\t},\n%s\t},\n", indent, indent)
		} else {
			fmt.Fprintf(&b, "%s\tAttributes: map[string]%s.Attribute{\n", indent, sb.goPackage())
			b.WriteString(sb.attributeDeclarations(n.children, depth+2))
			fmt.Fprintf(&b, "%s\t},\n", indent)
		}
	}

	fmt.Fprintf(&b, "%s},\n", indent)
	return b.String()
}

// validators is every stock validator one attribute carries, each a
// finished expression travelling with the imports it needs: the enum OneOf
// on a closed-set string, the bounds the document declares about the value,
// and the AlsoRequires that realizes a dependency whose subject is this root
// attribute.
//
// Nothing here registers an import. An expression that needs a package says
// so on the value it returns, and validatorLines is the single place those
// declarations are honoured — so a validator can never be rendered into a
// file whose import block forgot it.
func (sb *schemaBuilder) validators(n node, depth int) []code.CustomValidator {
	var validators []code.CustomValidator

	if len(n.attribute.OneOf) > 0 && n.attribute.Type == ir.TypeString && n.attribute.NestedAttributes == nil {
		quoted := make([]string, len(n.attribute.OneOf))
		for i, v := range n.attribute.OneOf {
			quoted[i] = strconv.Quote(v)
		}
		validators = append(validators, code.CustomValidator{
			Imports: []code.Import{
				{Path: "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"},
			},
			SchemaDefinition: fmt.Sprintf("stringvalidator.OneOf(%s)", strings.Join(quoted, ", ")),
		})
	}

	if len(n.attribute.OneOf) > 0 && n.attribute.Type == ir.TypeList && n.attribute.ElementType == ir.TypeString {
		quoted := make([]string, len(n.attribute.OneOf))
		for i, v := range n.attribute.OneOf {
			quoted[i] = strconv.Quote(v)
		}
		validators = append(validators, code.CustomValidator{
			Imports: []code.Import{
				{Path: "github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"},
				{Path: "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"},
			},
			SchemaDefinition: fmt.Sprintf("listvalidator.ValueStringsAre(stringvalidator.OneOf(%s))", strings.Join(quoted, ", ")),
		})
	}

	validators = append(validators, constraintValidators(n)...)

	if depth == sb.rootDepth {
		if reqs, ok := sb.deps[n.attribute.Name]; ok {
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

// computedOptionalRequiredLines renders the presence booleans. Inside a
// datasource, computed stays computed. Where the schema package declares no
// Computed at all — see rendersComputed — an attribute that is writable as
// well keeps the writable half; one that is only computed is dropped before
// it reaches here.
func (sb *schemaBuilder) computedOptionalRequiredLines(n node, indent string) string {
	// An attribute whose state keeps the planned value is never computed:
	// nothing fills it after apply, so a computed one would be left unknown.
	if n.held && n.attribute.ComputedOptionalRequired != ir.Required {
		return indent + "Optional: true,\n"
	}
	switch n.attribute.ComputedOptionalRequired {
	case ir.Required:
		return indent + "Required: true,\n"
	case ir.Computed:
		if !sb.rendersComputed() {
			return indent + "Optional: true,\n"
		}
		return indent + "Computed: true,\n"
	case ir.ComputedOptional:
		if !sb.rendersComputed() {
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
// computed id and on every computed-optional attribute. Empty for a datasource or an action, neither of which has
// plan modifiers at all.
func (sb *schemaBuilder) planModifiers(n node) []code.CustomPlanModifier {
	if sb.kind != schemaResource {
		return nil
	}

	schema := schemaTypeOf(n)
	goPackage := schema.PlanModifierPackage()
	imports := []code.Import{
		{Path: "github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"},
		schema.PlanModifierImport,
	}

	var modifiers []code.CustomPlanModifier
	if n.attribute.RequiresReplace {
		modifiers = append(modifiers, code.CustomPlanModifier{
			Imports:          imports,
			SchemaDefinition: goPackage + ".RequiresReplace()",
		})
	}
	isID := n.attribute.Name == "id" && n.attribute.ComputedOptionalRequired == ir.Computed &&
		n.attribute.Type == ir.TypeString && n.attribute.NestedAttributes == nil
	// A computed-optional attribute is one the practitioner may set and the
	// server fills when they do not. Unpinned it re-plans as unknown on every
	// run, which is a permanent diff on a resource nothing has changed.
	//
	// Only computed-optional, never plain computed. A server-owned value the
	// document says nothing about — a link, a modification stamp — is not
	// stable just because it is computed, and pinning it makes terraform
	// insist on a value the next read contradicts. Which of those are settled
	// is a fact an audit measures, not one the document states.
	if isID || (n.attribute.ComputedOptionalRequired == ir.ComputedOptional && !n.held) {
		modifiers = append(modifiers, code.CustomPlanModifier{
			Imports:          imports,
			SchemaDefinition: goPackage + ".UseStateForUnknown()",
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
// ones routinely annotate only a fraction of their properties — the wire
// property name stands in.
func attributeDescription(a ir.Attribute, keptFromPlan bool) string {
	var parts []string
	if declared := strings.TrimSpace(a.Description); declared != "" {
		parts = append(parts, terminated(declared))
	} else {
		parts = append(parts, "The "+a.WireName+" property.")
	}
	if keptFromPlan {
		parts = append(parts, "The API does not return this attribute, so state keeps the configured value and drift in it is not detected.")
	}
	if len(a.AdvisoryValues) > 0 {
		parts = append(parts, "Known values: "+strings.Join(a.AdvisoryValues, ", ")+".")
	}
	if a.RequiresReplace {
		parts = append(parts, "Changing this attribute forces replacement.")
	}
	if a.IgnoredOnUpdate {
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

// modelDeclaration is one rendered model struct declaration.
type modelDeclaration struct {
	name string
	body string
}

// modelNamer assigns every nested object in one entity's tree the Go struct
// name its model declaration and every reference to it use.
//
// The short spelling — the type prefix, the attribute's Go name, "Model" —
// is what a tree gets whenever it is the only nesting site claiming it. Two
// sites claiming one spelling cannot be refused: a real document nests an
// object of one name at two depths of one entity, and a vendor will not
// rename it to suit a generator. Every claimant of a contested spelling is
// qualified by its ancestor path instead, and an uncontested one is left
// short — so qualification shows up only where a collision made it
// necessary.
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
			if n.attribute.NestedAttributes == nil {
				continue
			}
			path := n.attribute.Name
			if parent != "" {
				path = parent + "." + n.attribute.Name
			}
			short := typePrefix + ir.GoName(n.attribute.Name) + "Model"
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
		return n.attribute.Name
	}
	return parent + "." + n.attribute.Name
}

// buildModels renders the framework model structs for one entity: the
// root struct plus one struct per nested object shape, pre-order. The
// extra fields land in the root struct before the attribute fields.
func buildModels(rootName, typePrefix string, nodes []node, extraFields []string) []modelDeclaration {
	namer := newModelNamer(typePrefix, nodes)
	var declarations []modelDeclaration

	var walk func(name, path string, nodes []node, extra []string)
	walk = func(name, path string, nodes []node, extra []string) {
		var b strings.Builder
		fmt.Fprintf(&b, "type %s struct {\n", name)
		for _, f := range extra {
			b.WriteString("\t" + f + "\n")
		}
		for _, n := range nodes {
			fmt.Fprintf(&b, "\t%s %s `tfsdk:%q`\n",
				ir.GoName(n.attribute.Name), fieldType(n), n.attribute.Name)
		}
		b.WriteString("}")
		declarations = append(declarations, modelDeclaration{name: name, body: b.String()})

		// The root model is read and written whole through Get and Set, so
		// only nested shapes need an object type to be built from.
		if path != "" {
			var t strings.Builder
			fmt.Fprintf(&t, "// %s is the object type %s maps onto.\n",
				attrTypesFuncName(name), name)
			fmt.Fprintf(&t, "func %s() map[string]attr.Type {\n", attrTypesFuncName(name))
			t.WriteString("\treturn map[string]attr.Type{\n")
			for _, n := range nodes {
				fmt.Fprintf(&t, "\t\t%q: %s,\n",
					n.attribute.Name, attrTypeExpr(namer, childPath(path, n), n))
			}
			t.WriteString("\t}\n}")
			declarations = append(declarations, modelDeclaration{name: attrTypesFuncName(name), body: t.String()})
		}

		for _, n := range nodes {
			if n.attribute.NestedAttributes == nil {
				continue
			}
			nested := childPath(path, n)
			walk(namer.name(nested), nested, n.children, nil)
		}
	}

	walk(rootName, "", nodes, extraFields)
	return declarations
}

// fieldType is the Go type one model field carries.
//
// A nested attribute is held as types.Object, types.List or types.Map
// rather than as
// the generated struct, because a Computed attribute arrives unknown in the
// plan and neither a struct pointer nor a slice can represent unknown. The
// struct is still generated: it is what the object is built from and read
// back into, through the AttrTypes function beside it.
func fieldType(n node) string {
	switch {
	case n.attribute.NestedAttributes != nil && n.attribute.Type == ir.TypeList:
		return "types.List"
	case n.attribute.NestedAttributes != nil && n.attribute.Type == ir.TypeMap:
		return "types.Map"
	case n.attribute.NestedAttributes != nil:
		return "types.Object"
	default:
		return schemaTypeOf(n).ValueType
	}
}

// attrTypeExpr is the attr.Type one model field is described by, which an
// object or list value must be given to be built or nulled.
func attrTypeExpr(namer *modelNamer, path string, n node) string {
	switch {
	case n.attribute.NestedAttributes != nil && n.attribute.Type == ir.TypeList:
		return "types.ListType{ElemType: " + nestedObjectType(namer, path) + "}"
	case n.attribute.NestedAttributes != nil && n.attribute.Type == ir.TypeMap:
		return "types.MapType{ElemType: " + nestedObjectType(namer, path) + "}"
	case n.attribute.NestedAttributes != nil:
		return nestedObjectType(namer, path)
	default:
		resolved := schemaTypeOf(n)
		if resolved.ElementType != "" {
			return resolved.ValueType + "Type{ElemType: " + resolved.ElementType + "}"
		}
		return resolved.ValueType + "Type"
	}
}

// nestedObjectType is the object type one generated nested struct maps onto.
func nestedObjectType(namer *modelNamer, path string) string {
	return "types.ObjectType{AttrTypes: " + attrTypesFuncName(namer.name(path)) + "()}"
}

// attrTypesFuncName is the function beside a nested model that answers its
// attribute types. Generated rather than assembled at run time: the shape is
// known here, and a mismatch between the struct and its types is then a
// compile-time fact rather than a runtime diagnostic.
func attrTypesFuncName(modelName string) string {
	return modelName + "AttrTypes"
}

// renderModelDeclarations joins model declarations into one finished block.
func renderModelDeclarations(declarations []modelDeclaration) string {
	parts := make([]string, len(declarations))
	for i, d := range declarations {
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
