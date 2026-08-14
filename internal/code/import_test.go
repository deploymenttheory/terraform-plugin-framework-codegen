package code

import "testing"

func TestUnit_Import_HasAlias(t *testing.T) {
	empty := ""
	types := "github.com/hashicorp/terraform-plugin-framework/types"

	for _, testCase := range []struct {
		name     string
		imported Import
		want     bool
	}{
		{"no alias", Import{Path: types}, false},
		{"empty alias", Import{Alias: &empty, Path: types}, false},
		{"aliased", Aliased("tftypes", types), true},
	} {
		if got := testCase.imported.HasAlias(); got != testCase.want {
			t.Errorf("%s: HasAlias() = %t, want %t", testCase.name, got, testCase.want)
		}
	}
}

func TestUnit_Import_Equal(t *testing.T) {
	types := "github.com/hashicorp/terraform-plugin-framework/types"
	path := "github.com/hashicorp/terraform-plugin-framework/path"

	for _, testCase := range []struct {
		name  string
		left  Import
		right Import
		want  bool
	}{
		{"same path, both unaliased", Import{Path: types}, Import{Path: types}, true},
		{"different path", Import{Path: types}, Import{Path: path}, false},
		{"same path, same alias", Aliased("tftypes", types), Aliased("tftypes", types), true},
		{"same path, different alias", Aliased("tftypes", types), Aliased("fwtypes", types), false},
		{"aliased against unaliased", Aliased("tftypes", types), Import{Path: types}, false},
		{"unaliased against aliased", Import{Path: types}, Aliased("tftypes", types), false},
	} {
		if got := testCase.left.Equal(testCase.right); got != testCase.want {
			t.Errorf("%s: Equal() = %t, want %t", testCase.name, got, testCase.want)
		}
	}
}

// TestUnit_Import_EmptyAliasEqualsNoAlias pins the one comparison the
// renderer depends on: importSet.add passes a pointer to an empty string
// for an unaliased import, and that must not read as a different import
// from one declared with no alias at all, or the same package would be
// imported twice.
func TestUnit_Import_EmptyAliasEqualsNoAlias(t *testing.T) {
	empty := ""
	types := "github.com/hashicorp/terraform-plugin-framework/types"

	withEmpty := Import{Alias: &empty, Path: types}
	withNone := Import{Path: types}

	if !withEmpty.Equal(withNone) {
		t.Error("an empty alias must equal no alias")
	}
	if !withNone.Equal(withEmpty) {
		t.Error("no alias must equal an empty alias")
	}
}

// TestUnit_Definitions_CarryTheirImports pins the contract the whole
// SchemaDefinition shape exists for: an expression travels with the
// packages it references, so a renderer cannot emit one and forget the
// other.
func TestUnit_Definitions_CarryTheirImports(t *testing.T) {
	validator := CustomValidator{
		Imports:          []Import{{Path: "github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"}},
		SchemaDefinition: `stringvalidator.OneOf("a", "b")`,
	}
	if len(validator.Imports) != 1 || validator.Imports[0].Path == "" {
		t.Fatalf("a validator must carry the import its expression references, got %+v", validator.Imports)
	}
	if validator.SchemaDefinition == "" {
		t.Error("a validator must carry a finished expression")
	}

	planModifier := CustomPlanModifier{
		Imports:          []Import{{Path: "github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"}},
		SchemaDefinition: "stringplanmodifier.RequiresReplace()",
	}
	if len(planModifier.Imports) != 1 || planModifier.Imports[0].Path == "" {
		t.Fatalf("a plan modifier must carry the import its expression references, got %+v", planModifier.Imports)
	}
	if planModifier.SchemaDefinition == "" {
		t.Error("a plan modifier must carry a finished expression")
	}
}
