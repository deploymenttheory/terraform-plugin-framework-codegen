// Package code holds the source-code vocabulary the emitters share: an
// import, and a finished expression together with the imports it needs.
//
// The shapes are terraform-plugin-codegen-spec's, field for field, so the
// toolkit and the specification that could describe its output agree on
// what to call the same fact. They are mirrored here rather than imported
// because a mirrored struct costs nothing and a dependency costs a
// module: switching to the upstream package later is one import line.
package code

// Import represents a required source code import to ensure that generated
// code compiles using code from sources external to the generated file.
type Import struct {
	// Alias is an optional package name for the import, used where two
	// imports would otherwise collide, or an underscore to import a
	// package solely for its initialization side-effects.
	Alias *string `json:"alias,omitempty"`

	// Path is the package path, such as
	// github.com/hashicorp/terraform-plugin-framework/types.
	Path string `json:"path"`
}

// HasAlias reports whether the import carries an alias.
func (i Import) HasAlias() bool {
	return i.Alias != nil && *i.Alias != ""
}

// Equal reports whether two imports name the same package under the same
// alias.
func (i Import) Equal(other Import) bool {
	if i.Path != other.Path {
		return false
	}
	if i.HasAlias() != other.HasAlias() {
		return false
	}
	if !i.HasAlias() {
		return true
	}
	return *i.Alias == *other.Alias
}

// Aliased builds an import carrying an alias.
func Aliased(alias, path string) Import {
	return Import{Alias: &alias, Path: path}
}

// CustomValidator is one validator expression the generated schema
// declares, together with the imports the expression needs.
type CustomValidator struct {
	// Imports are the packages SchemaDefinition references.
	Imports []Import `json:"imports,omitempty"`

	// SchemaDefinition is the finished validator expression, ready to sit
	// inside a Validators slice.
	SchemaDefinition string `json:"schema_definition"`
}

// CustomPlanModifier is one plan-modifier expression the generated schema
// declares, together with the imports the expression needs.
type CustomPlanModifier struct {
	// Imports are the packages SchemaDefinition references.
	Imports []Import `json:"imports,omitempty"`

	// SchemaDefinition is the finished plan-modifier expression, ready to
	// sit inside a PlanModifiers slice.
	SchemaDefinition string `json:"schema_definition"`
}
