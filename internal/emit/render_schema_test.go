package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// declarationOf renders one attribute through a builder of the given kind, which is
// the whole of what a presence, a sensitivity or a deprecation decides.
func declarationOf(kind schemaKind, attribute ir.Attribute) string {
	sb := &schemaBuilder{kind: kind, imports: newImportSet("example.com/m")}
	return sb.attributeDeclaration(node{attribute: attribute}, 0)
}

// TestUnit_AttributeDecl_MarksASecretSensitive proves a value the document
// declares write-only or formats as a password is kept out of terraform's
// output, and that an ordinary value is not.
func TestUnit_AttributeDecl_MarksASecretSensitive(t *testing.T) {
	for _, kind := range []schemaKind{schemaResource, schemaDatasource} {
		declaration := declarationOf(kind, ir.Attribute{
			Name: "password", Kind: ir.TypeString,
			ComputedOptionalRequired: ir.Optional, Sensitive: true,
		})
		if !strings.Contains(declaration, "Sensitive: true,") {
			t.Errorf("kind %d does not mark a secret sensitive:\n%s", kind, declaration)
		}
	}

	plain := declarationOf(schemaResource, ir.Attribute{
		Name: "name", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional,
	})
	if strings.Contains(plain, "Sensitive") {
		t.Errorf("an ordinary attribute is marked sensitive:\n%s", plain)
	}
}

// TestUnit_AttributeDecl_OmitsSensitiveWhereThePackageLacksIt proves the
// action and list packages get no Sensitive field: their attribute types do
// not declare one, so emitting it would not compile.
func TestUnit_AttributeDecl_OmitsSensitiveWhereThePackageLacksIt(t *testing.T) {
	for _, kind := range []schemaKind{schemaAction, schemaListResource} {
		declaration := declarationOf(kind, ir.Attribute{
			Name: "password", Kind: ir.TypeString,
			ComputedOptionalRequired: ir.Optional, Sensitive: true,
		})
		if strings.Contains(declaration, "Sensitive") {
			t.Errorf("kind %d declares Sensitive, which its package has no field for:\n%s", kind, declaration)
		}
	}
}

// TestUnit_AttributeDecl_WarnsOnADeprecatedAttribute proves a deprecated
// attribute carries the warning in every schema package, all four of which
// declare DeprecationMessage.
func TestUnit_AttributeDecl_WarnsOnADeprecatedAttribute(t *testing.T) {
	for _, kind := range []schemaKind{schemaResource, schemaDatasource, schemaAction, schemaListResource} {
		declaration := declarationOf(kind, ir.Attribute{
			Name: "legacy", Kind: ir.TypeString,
			ComputedOptionalRequired: ir.Optional, Deprecated: true,
		})
		if !strings.Contains(declaration, `DeprecationMessage: "This attribute is deprecated and may be removed in a future API version.",`) {
			t.Errorf("kind %d does not warn on a deprecated attribute:\n%s", kind, declaration)
		}
	}

	current := declarationOf(schemaResource, ir.Attribute{
		Name: "name", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional,
	})
	if strings.Contains(current, "DeprecationMessage") {
		t.Errorf("an undeprecated attribute carries a warning:\n%s", current)
	}
}

// TestUnit_AttributeDecl_PinsAComputedOptionalValue proves a value the server
// settles keeps the one state holds, so a resource nothing changed plans empty
// rather than re-planning that value as unknown for ever.
func TestUnit_AttributeDecl_PinsAComputedOptionalValue(t *testing.T) {
	pinned := declarationOf(schemaResource, ir.Attribute{
		Name: "timezone", Kind: ir.TypeString,
		ComputedOptionalRequired: ir.ComputedOptional,
	})
	if !strings.Contains(pinned, "UseStateForUnknown()") {
		t.Errorf("a computed-optional attribute is not pinned:\n%s", pinned)
	}

	// A server-owned value the document says nothing about is not stable just
	// because it is computed, and pinning it makes terraform insist on a value
	// the next read contradicts.
	unpinned := declarationOf(schemaResource, ir.Attribute{
		Name: "modified_at", Kind: ir.TypeString,
		ComputedOptionalRequired: ir.Computed,
	})
	if strings.Contains(unpinned, "UseStateForUnknown()") {
		t.Errorf("a plain computed attribute was pinned:\n%s", unpinned)
	}

	// A datasource has no plan to modify at all.
	ds := declarationOf(schemaDatasource, ir.Attribute{
		Name: "timezone", Kind: ir.TypeString,
		ComputedOptionalRequired: ir.ComputedOptional,
	})
	if strings.Contains(ds, "PlanModifiers") {
		t.Errorf("a datasource attribute carries plan modifiers:\n%s", ds)
	}
}
