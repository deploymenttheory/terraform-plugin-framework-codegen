package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// TestUnit_ParamDeclaration_UUIDParsesWithADiagnostic proves a path
// parameter the SDK types as uuid.UUID is parsed rather than refused, and
// that a malformed value reports against its own attribute.
func TestUnit_ParamDeclaration_UUIDParsesWithADiagnostic(t *testing.T) {
	p := sdkbind.CallParameter{Local: "agentId", Wire: "agentId", GoType: "uuid.UUID"}

	declaration, imports, err := parameterDeclaration(p, "data", "AgentID", ir.TypeString, "agent_id", respDiagnostics())
	if err != nil {
		t.Fatalf("parameterDeclaration refused a uuid path parameter: %v", err)
	}
	for _, want := range []string{
		"agentId, agentIdErr := uuid.Parse(data.AgentID.ValueString())",
		"if agentIdErr != nil {",
		`resp.Diagnostics.AddAttributeError(path.Root("agent_id")`,
		"return",
	} {
		if !strings.Contains(declaration, want) {
			t.Errorf("the declaration does not carry %q:\n%s", want, declaration)
		}
	}
	for _, want := range []string{"github.com/google/uuid", "github.com/hashicorp/terraform-plugin-framework/path"} {
		found := false
		for _, i := range imports {
			if i == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the declaration does not declare the import %q; got %v", want, imports)
		}
	}
}

// TestUnit_ParamDeclaration_IntegerParsesWithADiagnostic proves a string
// path parameter the SDK types as an integer is parsed rather than refused,
// that the parse is sized to the SDK's own width, and that the parsed value
// reaches the local the call passes.
func TestUnit_ParamDeclaration_IntegerParsesWithADiagnostic(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		parameter sdkbind.CallParameter
		want      []string
	}{
		{
			"narrowed to int32",
			sdkbind.CallParameter{Local: "hookId", Wire: "hook_id", GoType: "int32"},
			[]string{
				"hookIdParsed, hookIdErr := strconv.ParseInt(data.HookID.ValueString(), 10, 32)",
				`resp.Diagnostics.AddAttributeError(path.Root("hook_id"), "Invalid hook_id", hookIdErr.Error())`,
				"hookId := int32(hookIdParsed)",
			},
		},
		{
			"int64 needs no cast",
			sdkbind.CallParameter{Local: "hookId", Wire: "hook_id", GoType: "int64"},
			[]string{"hookId, hookIdErr := strconv.ParseInt(data.HookID.ValueString(), 10, 64)"},
		},
		{
			"unsigned parses unsigned",
			sdkbind.CallParameter{Local: "hookId", Wire: "hook_id", GoType: "uint32"},
			[]string{
				"hookIdParsed, hookIdErr := strconv.ParseUint(data.HookID.ValueString(), 10, 32)",
				"hookId := uint32(hookIdParsed)",
			},
		},
		{
			"platform width int",
			sdkbind.CallParameter{Local: "hookId", Wire: "hook_id", GoType: "int"},
			[]string{
				"hookIdParsed, hookIdErr := strconv.ParseInt(data.HookID.ValueString(), 10, 0)",
				"hookId := int(hookIdParsed)",
			},
		},
	} {
		declaration, imports, err := parameterDeclaration(testCase.parameter, "data", "HookID", ir.TypeString, "hook_id", respDiagnostics())
		if err != nil {
			t.Errorf("%s: parameterDeclaration refused an integer path parameter: %v", testCase.name, err)
			continue
		}
		for _, want := range testCase.want {
			if !strings.Contains(declaration, want) {
				t.Errorf("%s: the declaration does not carry %q:\n%s", testCase.name, want, declaration)
			}
		}
		if !strings.Contains(declaration, "return") {
			t.Errorf("%s: the declaration does not stop on a failed parse:\n%s", testCase.name, declaration)
		}
		for _, want := range []string{"strconv", "github.com/hashicorp/terraform-plugin-framework/path"} {
			found := false
			for _, i := range imports {
				if i == want {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: the declaration does not declare the import %q; got %v", testCase.name, want, imports)
			}
		}
	}
}

// TestUnit_ParamDeclaration_ExcludesATruncatingConversion proves a numeric
// field into a narrower integer parameter is still refused: the conversion
// loses information silently, and no parse reports that.
func TestUnit_ParamDeclaration_ExcludesATruncatingConversion(t *testing.T) {
	p := sdkbind.CallParameter{Local: "groupId", Wire: "runner_group_id", GoType: "int32"}

	if _, _, err := parameterDeclaration(p, "data", "GroupID", ir.TypeFloat64, "runner_group_id", respDiagnostics()); err == nil {
		t.Fatal("parameterDeclaration rendered a float64 into an int32 parameter; it must refuse")
	}
}

// TestUnit_ParamDeclaration_InfallibleConversionsStayOneLine proves the
// declaration for a conversion that cannot fail is still a plain
// assignment, with no diagnostic scaffolding around it.
func TestUnit_ParamDeclaration_InfallibleConversionsStayOneLine(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		parameter sdkbind.CallParameter
		kind      ir.AttributeType
		want      string
		needed    string
	}{
		{"string to string", sdkbind.CallParameter{Local: "id", Wire: "id", GoType: "string"}, ir.TypeString, "id := data.ID.ValueString()", ""},
		{"int64 narrowed", sdkbind.CallParameter{Local: "id", Wire: "id", GoType: "int32"}, ir.TypeInt64, "id := int32(data.ID.ValueInt64())", ""},
		{"int64 to string", sdkbind.CallParameter{Local: "id", Wire: "id", GoType: "string"}, ir.TypeInt64, "id := strconv.FormatInt(data.ID.ValueInt64(), 10)", "strconv"},
	} {
		declaration, imports, err := parameterDeclaration(testCase.parameter, "data", "ID", testCase.kind, "id", respDiagnostics())
		if err != nil {
			t.Errorf("%s: %v", testCase.name, err)
			continue
		}
		if declaration != testCase.want {
			t.Errorf("%s: declaration = %q, want %q", testCase.name, declaration, testCase.want)
		}
		if testCase.needed == "" && len(imports) != 0 {
			t.Errorf("%s: declared imports %v, want none", testCase.name, imports)
		}
		if testCase.needed != "" && (len(imports) != 1 || imports[0] != testCase.needed) {
			t.Errorf("%s: declared imports %v, want [%s]", testCase.name, imports, testCase.needed)
		}
	}
}
