package blueprint

import "fmt"

// BlockKind is a top-level Terraform block kind, and the level the whole IR is organised around.
//
// The framework defines each kind by a required method set plus opt-in With* interfaces, and gives
// each its own schema package. Those packages are structurally similar and deliberately not
// identical, which is why an attribute's legal fields depend on the kind rendering it rather than on
// the attribute alone.
type BlockKind string

const (
	// BlockResource is a managed resource: Metadata, Schema, Create, Read, Update, Delete.
	BlockResource BlockKind = "resource"
	// BlockDataSource is a data source: Metadata, Schema, Read.
	BlockDataSource BlockKind = "datasource"
	// BlockEphemeral is an ephemeral resource: Metadata, Schema, Open.
	BlockEphemeral BlockKind = "ephemeral"
	// BlockAction is an action: Metadata, Schema, Invoke.
	BlockAction BlockKind = "action"
	// BlockList is a list resource, which is a facet of a managed resource rather than an
	// independent block: its type name must equal the resource's, and it requires that resource to
	// declare an identity.
	BlockList BlockKind = "list"
)

// SchemaPackage is the framework import path suffix this kind's schema types come from.
//
// The attribute types in each are structurally identical, so schema rendering is parameterised by
// this rather than duplicated per kind.
func (k BlockKind) SchemaPackage() string {
	switch k {
	case BlockResource:
		return "resource/schema"
	case BlockDataSource:
		return "datasource/schema"
	case BlockEphemeral:
		return "ephemeral/schema"
	case BlockAction:
		return "action/schema"
	case BlockList:
		return "list/schema"
	default:
		return ""
	}
}

// SupportsComputed reports whether this kind's attributes may be Computed.
//
// Action and list attributes may not. For a list resource that is the structural reason its config
// schema is filter-only: there is nowhere to put a result.
func (k BlockKind) SupportsComputed() bool {
	return k == BlockResource || k == BlockDataSource || k == BlockEphemeral
}

// SupportsSensitive reports whether this kind's attributes may be Sensitive.
func (k BlockKind) SupportsSensitive() bool { return k.SupportsComputed() }

// SupportsPlanModifiers reports whether this kind's attributes may carry plan modifiers.
//
// Only a managed resource has a plan to modify.
func (k BlockKind) SupportsPlanModifiers() bool { return k == BlockResource }

// SupportsDefault reports whether this kind's attributes may carry a default.
func (k BlockKind) SupportsDefault() bool { return k == BlockResource }

// SupportsWriteOnly reports whether this kind's attributes may be write-only.
func (k BlockKind) SupportsWriteOnly() bool { return k == BlockResource || k == BlockAction }

// validateForKind refuses a field the target block kind has no home for.
//
// Without this the generator emits code that does not compile: a Default on a data source attribute
// becomes `datasourceschema.StringAttribute{Default: ...}` and that field does not exist. Refusing in
// the blueprint names the attribute and the kind, which a compiler error in generated output does
// not.
func (a Attribute) validateForKind(kind BlockKind, at string, p *problems) {
	if !kind.SupportsComputed() && a.ComputedOptionalRequired.IsComputed() {
		p.add(at+".computedOptionalRequired",
			"%q is not available on a %s attribute; only required and optional are",
			a.ComputedOptionalRequired, kind)
	}

	if !kind.SupportsSensitive() && a.Sensitive {
		p.add(at+".sensitive", "a %s attribute cannot be sensitive", kind)
	}

	if !kind.SupportsPlanModifiers() && len(a.PlanModifiers) > 0 {
		p.add(at+".planModifiers",
			"a %s attribute has no plan to modify; plan modifiers are available on resources only",
			kind)
	}

	if !kind.SupportsDefault() && a.Default != nil {
		p.add(at+".default",
			"a %s attribute cannot carry a default; defaults are available on resources only", kind)
	}

	if !kind.SupportsWriteOnly() && a.WriteOnly {
		p.add(at+".writeOnly", "a %s attribute cannot be write-only", kind)
	}

	for i, nested := range a.nestedAttributes() {
		nat := fmt.Sprintf("%s.type.nestedObject.attributes[%d]", at, i)
		if nested.Name != "" {
			nat = fmt.Sprintf("%s.type.nestedObject.attributes[%s]", at, nested.Name)
		}

		nested.validateForKind(kind, nat, p)
	}
}

// nestedAttributes returns the attributes of a nested object, or nothing.
func (a Attribute) nestedAttributes() []Attribute {
	if a.Type.NestedObject == nil {
		return nil
	}

	return a.Type.NestedObject.Attributes
}
