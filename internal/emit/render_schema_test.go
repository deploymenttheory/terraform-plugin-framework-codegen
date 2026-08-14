package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// declOf renders one attribute through a builder of the given kind, which is
// the whole of what a presence, a sensitivity or a deprecation decides.
func declOf(kind schemaKind, attr ir.Attribute) string {
	sb := &schemaBuilder{kind: kind, imports: newImportSet("example.com/m")}
	return sb.attributeDecl(node{attr: attr}, 0)
}

// TestUnit_AttributeDecl_MarksASecretSensitive proves a value the document
// declares write-only or formats as a password is kept out of terraform's
// output, and that an ordinary value is not.
func TestUnit_AttributeDecl_MarksASecretSensitive(t *testing.T) {
	for _, kind := range []schemaKind{schemaResource, schemaDatasource} {
		decl := declOf(kind, ir.Attribute{
			Name: "password", Kind: ir.TypeString,
			ComputedOptionalRequired: ir.Optional, Sensitive: true,
		})
		if !strings.Contains(decl, "Sensitive: true,") {
			t.Errorf("kind %d does not mark a secret sensitive:\n%s", kind, decl)
		}
	}

	plain := declOf(schemaResource, ir.Attribute{
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
		decl := declOf(kind, ir.Attribute{
			Name: "password", Kind: ir.TypeString,
			ComputedOptionalRequired: ir.Optional, Sensitive: true,
		})
		if strings.Contains(decl, "Sensitive") {
			t.Errorf("kind %d declares Sensitive, which its package has no field for:\n%s", kind, decl)
		}
	}
}

// TestUnit_AttributeDecl_WarnsOnADeprecatedAttribute proves a deprecated
// attribute carries the warning in every schema package, all four of which
// declare DeprecationMessage.
func TestUnit_AttributeDecl_WarnsOnADeprecatedAttribute(t *testing.T) {
	for _, kind := range []schemaKind{schemaResource, schemaDatasource, schemaAction, schemaListResource} {
		decl := declOf(kind, ir.Attribute{
			Name: "legacy", Kind: ir.TypeString,
			ComputedOptionalRequired: ir.Optional, Deprecated: true,
		})
		if !strings.Contains(decl, `DeprecationMessage: "This attribute is deprecated and may be removed in a future API version.",`) {
			t.Errorf("kind %d does not warn on a deprecated attribute:\n%s", kind, decl)
		}
	}

	current := declOf(schemaResource, ir.Attribute{
		Name: "name", Kind: ir.TypeString, ComputedOptionalRequired: ir.Optional,
	})
	if strings.Contains(current, "DeprecationMessage") {
		t.Errorf("an undeprecated attribute carries a warning:\n%s", current)
	}
}
