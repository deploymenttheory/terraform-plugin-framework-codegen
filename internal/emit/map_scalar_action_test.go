package emit

import (
	"go/ast"
	"go/parser"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/fixtures"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// TestUnit_Emit_AMapValueRendersAGoLiteralOfItsElementKind guards the pair
// of expressions tftypeNewValue composes for a map: the tftypes type comes
// from the element kind and the literal comes from the derived value. A
// derivation that hands back the wrong type still parses as Go, because an
// unquoted tfpfgen-test-limits reads as a subtraction of three identifiers,
// so parsing is not the check. The literal has to be a literal.
func TestUnit_Emit_AMapValueRendersAGoLiteralOfItsElementKind(t *testing.T) {
	tree := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "limits", WireName: "limits", Kind: ir.TypeMap, ElementType: ir.TypeInt64, ComputedOptionalRequired: ir.Optional},
		{Name: "weights", WireName: "weights", Kind: ir.TypeMap, ElementType: ir.TypeFloat64, ComputedOptionalRequired: ir.Optional},
		{Name: "flags", WireName: "flags", Kind: ir.TypeMap, ElementType: ir.TypeBool, ComputedOptionalRequired: ir.Optional},
		{Name: "labels", WireName: "labels", Kind: ir.TypeMap, ElementType: ir.TypeString, ComputedOptionalRequired: ir.Optional},
	}}

	for _, v := range fixtures.Derive(tree).Entries {
		literal := tftypeScalarLiteral(v.ElementType, v.Scalar)
		parsed, err := parser.ParseExpr(literal)
		if err != nil {
			t.Errorf("%s literal %s does not parse: %v", v.Name, literal, err)
			continue
		}
		switch node := parsed.(type) {
		case *ast.BasicLit:
		case *ast.Ident:
			// A bool travels as the predeclared identifier.
			if node.Name != "true" && node.Name != "false" {
				t.Errorf("%s literal is the identifier %s, not a value", v.Name, node.Name)
			}
		default:
			t.Errorf("%s literal %s is a %T, not a Go literal", v.Name, literal, parsed)
		}
	}
}

// TestUnit_Emit_AMapLiteralMatchesItsDeclaredTftype asserts the two halves
// name the same kind rather than merely parsing.
func TestUnit_Emit_AMapLiteralMatchesItsDeclaredTftype(t *testing.T) {
	tree := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "limits", WireName: "limits", Kind: ir.TypeMap, ElementType: ir.TypeInt64, ComputedOptionalRequired: ir.Optional},
	}}
	got := tftypeNewValue(fixtures.Derive(tree).Entries[0])

	want := `tftypes.NewValue(tftypes.Map{ElementType: tftypes.Number}, map[string]tftypes.Value{"limits": tftypes.NewValue(tftypes.Number, 7)})`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, `"`+fixtures.NamePrefix) {
		t.Error("a number was rendered as a quoted string")
	}
}
