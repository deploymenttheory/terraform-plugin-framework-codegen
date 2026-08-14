package emit

import (
	"path"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/code"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// schemaType is the framework spelling of one attribute type: every name
// that follows from "this attribute is a list of objects" or "this
// attribute is a bool", resolved once and read as fields.
//
// It exists because those names used to live in six switches on the same
// (Kind, ElementType, Nested != nil) triple, spread across four files, each
// returning a bare string. Nothing tied them together, so adding an
// attribute type meant editing six places and hoping none was missed, and
// the compiler could not help — every one of them returned string. One of
// the six had already drifted into a duplicate: frameworkValueType in
// render_datasource.go answered exactly what fieldType answered for a
// scalar, spelled separately.
//
// Field names are terraform-plugin-codegen-spec's, so this record and the
// specification that could describe its output agree: ValueType from
// CustomType, ElementType, PlanModifier, Validator, and code.Import.
type schemaType struct {
	// SchemaAttribute is the schema package's attribute type name, e.g.
	// "ListNestedAttribute", rendered as schema.ListNestedAttribute.
	SchemaAttribute string
	// ValueType is the framework value type a model field holds the
	// attribute as, e.g. "types.List". Empty for a nested object or a
	// nested list, whose model field is a generated struct whose name only
	// the model namer knows — see fieldType.
	ValueType string
	// ElementType is the framework type of one element of a list of
	// scalars, e.g. "types.StringType". Empty for everything else.
	ElementType string
	// PlanModifier is the planmodifier interface name, e.g. "List",
	// rendered as planmodifier.List.
	PlanModifier string
	// PlanModifierImport is the per-type plan modifier package.
	PlanModifierImport code.Import
	// Validator is the validator interface name, e.g. "List", rendered as
	// validator.List. It is the same word as PlanModifier — the framework
	// spells the two interfaces alike — but they are separate fields
	// because nothing guarantees that stays true.
	Validator string
	// ValidatorImport is the per-type validator package.
	ValidatorImport code.Import
}

// PlanModifierPackage is the package name PlanModifierImport is referenced
// by, e.g. "listplanmodifier" — the last segment of its path, because that
// is what a Go import declares and what an expression must qualify with.
func (t schemaType) PlanModifierPackage() string {
	return path.Base(t.PlanModifierImport.Path)
}

// ValidatorPackage is the package name ValidatorImport is referenced by,
// e.g. "listvalidator".
func (t schemaType) ValidatorPackage() string {
	return path.Base(t.ValidatorImport.Path)
}

// Framework package roots the per-type imports hang off.
const (
	planModifierRoot = "github.com/hashicorp/terraform-plugin-framework/resource/schema/"
	validatorRoot    = "github.com/hashicorp/terraform-plugin-framework-validators/"
)

// newSchemaType builds one record from the framework's naming rules: the
// per-type packages and interfaces are all derived from one base word, so
// they are spelled once here rather than in a switch each.
func newSchemaType(schemaAttribute, valueType, base string) schemaType {
	lower := lowerASCII(base)
	return schemaType{
		SchemaAttribute:    schemaAttribute,
		ValueType:          valueType,
		PlanModifier:       base,
		PlanModifierImport: code.Import{Path: planModifierRoot + lower + "planmodifier"},
		Validator:          base,
		ValidatorImport:    code.Import{Path: validatorRoot + lower + "validator"},
	}
}

// lowerASCII lowercases an ASCII base word. The base words are a closed set
// of Go type names, so this needs none of unicode's machinery.
func lowerASCII(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + ('a' - 'A')
		}
	}
	return string(out)
}

// scalarSchemaTypes is the framework spelling of every scalar attribute
// type. String is the fallback, matching what the switches it replaces did
// with an unrecognised kind.
var scalarSchemaTypes = map[ir.AttributeType]schemaType{
	ir.TypeBool:    newSchemaType("BoolAttribute", "types.Bool", "Bool"),
	ir.TypeInt64:   newSchemaType("Int64Attribute", "types.Int64", "Int64"),
	ir.TypeFloat64: newSchemaType("Float64Attribute", "types.Float64", "Float64"),
	ir.TypeString:  newSchemaType("StringAttribute", "types.String", "String"),
}

// scalarSchemaType is one scalar kind's record, defaulting to string for a
// kind no case names — an attribute that reached emission with no kind is
// rendered as a string rather than refused, which is what the switches this
// replaces did.
func scalarSchemaType(kind ir.AttributeType) schemaType {
	if resolved, ok := scalarSchemaTypes[kind]; ok {
		return resolved
	}
	return scalarSchemaTypes[ir.TypeString]
}

// frameworkElementTypes is the framework element type of one scalar kind,
// as a list attribute's ElementType declares it.
//
// Spelled out rather than derived from ValueType by appending "Type": the
// framework happens to spell them that way today, and a table that would
// silently produce a wrong name for a type that broke the pattern is worse
// than four lines.
var frameworkElementTypes = map[ir.AttributeType]string{
	ir.TypeBool:    "types.BoolType",
	ir.TypeInt64:   "types.Int64Type",
	ir.TypeFloat64: "types.Float64Type",
	ir.TypeString:  "types.StringType",
}

// frameworkElementType is one list element kind's framework type,
// defaulting to string for the same reason scalarSchemaType does.
func frameworkElementType(kind ir.AttributeType) string {
	if resolved, ok := frameworkElementTypes[kind]; ok {
		return resolved
	}
	return frameworkElementTypes[ir.TypeString]
}

// schemaTypeOf resolves one attribute's record. Nesting outranks kind: a
// list of objects is a ListNestedAttribute and an object is a
// SingleNestedAttribute, whatever their element kind says.
func schemaTypeOf(n node) schemaType {
	switch {
	case n.attr.Nested != nil && n.attr.Kind == ir.TypeList:
		return newSchemaType("ListNestedAttribute", "", "List")
	case n.attr.Nested != nil:
		return newSchemaType("SingleNestedAttribute", "", "Object")
	case n.attr.Kind == ir.TypeList:
		resolved := newSchemaType("ListAttribute", "types.List", "List")
		resolved.ElementType = frameworkElementType(n.attr.ElementType)
		return resolved
	default:
		return scalarSchemaType(n.attr.Kind)
	}
}
