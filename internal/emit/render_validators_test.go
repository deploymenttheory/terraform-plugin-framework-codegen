package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// TestUnit_ConstructNested_SkipsAnUnwritableBlock proves a nested block
// none of whose children are written renders nothing. Rendering the loop
// anyway declares an index nothing reads, which does not compile.
func TestUnit_ConstructNested_SkipsAnUnwritableBlock(t *testing.T) {
	readOnlyChild := node{
		attr: ir.Attribute{Name: "label", WireName: "label", Kind: ir.TypeString},
		fb:   &sdkbind.FieldBinding{Attr: "label", Wire: "label", Access: sdkbind.FieldAccess{Get: "GetLabel"}},
	}
	for _, kind := range []ir.AttributeType{ir.TypeList, ir.TypeObject} {
		n := node{
			attr: ir.Attribute{Name: "roles", WireName: "roles", Kind: kind, Nested: &ir.AttributeTree{}},
			fb: &sdkbind.FieldBinding{Attr: "roles", Wire: "roles",
				NestedWriteModel: "models.Role", NestedConstructor: "models.NewRole()",
				Access: sdkbind.FieldAccess{Get: "GetRoles", Set: "SetRoles", SDKType: "[]models.Roleable"}},
			children: []node{readOnlyChild},
		}
		lines, _, err := constructNested(n, "data", "body", "roles", 1)
		if err != nil {
			t.Fatalf("%s: constructNested: %v", kind, err)
		}
		if strings.TrimSpace(lines) != "" {
			t.Errorf("%s: rendered a block with nothing to write:\n%s", kind, lines)
		}
	}
}

// TestUnit_ConstructNested_BuildsTheWriteType proves construction builds the
// type the setter takes when the SDK emits one model per direction, not the
// type the getter answers.
func TestUnit_ConstructNested_BuildsTheWriteType(t *testing.T) {
	child := node{
		attr: ir.Attribute{Name: "name", WireName: "name", Kind: ir.TypeString},
		fb: &sdkbind.FieldBinding{Attr: "name", Wire: "name",
			Access: sdkbind.FieldAccess{Get: "GetName", Set: "SetName", ConvertSet: "ToPtrString"}},
	}
	n := node{
		attr: ir.Attribute{Name: "services", WireName: "services", Kind: ir.TypeList, Nested: &ir.AttributeTree{}},
		fb: &sdkbind.FieldBinding{Attr: "services", Wire: "services",
			NestedWriteModel: "models.ServiceCreate", NestedConstructor: "models.NewServiceCreate()",
			Access: sdkbind.FieldAccess{Get: "GetServices", Set: "SetServices",
				SDKType: "[]models.Serviceable", SDKWriteType: "[]models.ServiceCreateable"}},
		children: []node{child},
	}

	lines, _, err := constructNested(n, "data", "body", "services", 1)
	if err != nil {
		t.Fatalf("constructNested: %v", err)
	}
	if !strings.Contains(lines, "make([]models.ServiceCreateable") {
		t.Errorf("the slice is not typed for the setter:\n%s", lines)
	}
	if strings.Contains(lines, "make([]models.Serviceable") {
		t.Errorf("the slice is typed for the getter, which does not compile:\n%s", lines)
	}
}
