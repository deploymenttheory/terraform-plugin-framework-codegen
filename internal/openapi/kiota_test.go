package openapi

import (
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

func TestUnit_OpenAPI_KiotaNames(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"accountGroupId": "AccountGroupId", // no initialism conventions, unlike our own models
		"tag_value":      "TagValue",
		"name":           "Name",
		"match-type":     "MatchType",
	} {
		if got := kiotaName(in); got != want {
			t.Errorf("kiotaName(%q) = %q, want %q", in, got, want)
		}
	}

	// The keyword mangling: kiota reaches a property named "error" through
	// GetErrorEscaped, and "type" through GetTypeEscaped.
	if got := kiotaAccessorBase("error"); got != "ErrorEscaped" {
		t.Errorf("kiotaAccessorBase(error) = %q", got)
	}
	if got := kiotaAccessorBase("type"); got != "TypeEscaped" {
		t.Errorf("kiotaAccessorBase(type) = %q", got)
	}
	if got := kiotaAccessorBase("name"); got != "Name" {
		t.Errorf("kiotaAccessorBase(name) = %q", got)
	}
}

func TestUnit_OpenAPI_KiotaChainFromPathTemplate(t *testing.T) {
	t.Parallel()

	ctx := blueprint.Argument{Kind: blueprint.ArgContext}
	nilCfg := blueprint.Argument{Kind: blueprint.ArgLiteral, Expr: "nil"}

	chain := kiotaChain("/tags/{tagId}", "Get", []blueprint.Argument{ctx, nilCfg})

	if len(chain) != 3 {
		t.Fatalf("chain length = %d: %+v", len(chain), chain)
	}
	if chain[0].Method != "Tags" || len(chain[0].Args) != 0 {
		t.Errorf("segment 0 = %+v", chain[0])
	}
	if chain[1].Method != "ByTagId" || len(chain[1].Args) != 1 ||
		chain[1].Args[0].Kind != blueprint.ArgStateField {
		t.Errorf("segment 1 = %+v", chain[1])
	}
	if chain[2].Method != "Get" || len(chain[2].Args) != 2 {
		t.Errorf("segment 2 = %+v", chain[2])
	}

	// A deeper path is one builder hop per literal segment.
	deep := kiotaChain("/dashboards/filters/{id}", "Delete", []blueprint.Argument{ctx, nilCfg})
	if len(deep) != 4 || deep[0].Method != "Dashboards" || deep[1].Method != "Filters" || deep[2].Method != "ById" {
		t.Errorf("deep chain = %+v", deep)
	}
}

func TestUnit_OpenAPI_KiotaNumberWidths(t *testing.T) {
	t.Parallel()

	// Kiota reads formats literally: a plain integer is *int32, format int64
	// widens; the resty dialect is untouched.
	sdkType, _, _ := conversionsFor(Field{Name: "n", Kind: blueprint.KindInt64}, "models", blueprint.DialectKiotaFluent)
	if sdkType != "*int32" {
		t.Errorf("kiota plain integer = %q, want *int32", sdkType)
	}
	sdkType, _, _ = conversionsFor(Field{Name: "n", Kind: blueprint.KindInt64, Format: "int64"}, "models", blueprint.DialectKiotaFluent)
	if sdkType != "*int64" {
		t.Errorf("kiota int64 integer = %q, want *int64", sdkType)
	}
	sdkType, _, _ = conversionsFor(Field{Name: "n", Kind: blueprint.KindInt64}, "tags", blueprint.DialectRestyService)
	if sdkType != "*int64" {
		t.Errorf("resty integer = %q, want *int64", sdkType)
	}
}

func TestUnit_OpenAPI_KiotaEnumWiring(t *testing.T) {
	t.Parallel()

	f := Field{Name: "matchType", Kind: blueprint.KindString, EnumTypeName: "matchType", EnumValues: []string{"and", "or"}}
	sdkType, flatten, expand := conversionsFor(f, "models", blueprint.DialectKiotaFluent)

	if sdkType != "*models.MatchType" {
		t.Errorf("enum sdk type = %q", sdkType)
	}
	if flatten.Func != "convert.KiotaEnumToFramework" {
		t.Errorf("flatten = %+v", flatten)
	}
	if expand.Func != "convert.FrameworkToKiotaEnum" || !expand.ReturnsError ||
		len(expand.ExtraArgs) != 1 || expand.ExtraArgs[0] != "models.ParseMatchType" {
		t.Errorf("expand = %+v", expand)
	}
}
