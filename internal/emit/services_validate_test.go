package emit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// validateDriver is a hand-authored test dropped into the generated
// http_server resource package. It builds three deliberately-bad configs —
// two mutually-exclusive attributes set together, a variant-specific
// attribute set under the wrong discriminator value, and a dependency without
// its partner — and one valid config, and drives the generated ValidateConfig
// against each. It proves at runtime, against the real framework, that the
// emitted validators reject a bad multi-variant config and accept a good one.
//
// The config is built from the resource's own schema: every attribute defaults
// to null, then the case overrides the few that matter, so the driver does not
// depend on the full attribute list.
const validateDriver = `package pkgservers

import (
	"context"
	"math/big"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func buildConfig(t *testing.T, overrides map[string]tftypes.Value) *resource.ValidateConfigResponse {
	t.Helper()
	ctx := context.Background()
	r := NewHTTPServerResource().(*HTTPServerResource)
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	objType := sr.Schema.Type().TerraformType(ctx).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for name, at := range objType.AttributeTypes {
		vals[name] = tftypes.NewValue(at, nil)
	}
	for name, v := range overrides {
		vals[name] = v
	}
	cfg := tfsdk.Config{Schema: sr.Schema, Raw: tftypes.NewValue(objType, vals)}
	var resp resource.ValidateConfigResponse
	r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: cfg}, &resp)
	return &resp
}

func attrType(t *testing.T, name string) tftypes.Type {
	t.Helper()
	r := NewHTTPServerResource().(*HTTPServerResource)
	var sr resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sr)
	return sr.Schema.Type().TerraformType(context.Background()).(tftypes.Object).AttributeTypes[name]
}

func emptyList(t *testing.T, name string) tftypes.Value {
	return tftypes.NewValue(attrType(t, name), []tftypes.Value{})
}

func setObject(t *testing.T, name string) tftypes.Value {
	obj := attrType(t, name).(tftypes.Object)
	sub := map[string]tftypes.Value{}
	for n, at := range obj.AttributeTypes {
		sub[n] = tftypes.NewValue(at, nil)
	}
	return tftypes.NewValue(obj, sub)
}

func TestValidateConfigRejectsBadMultiVariantConfig(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]tftypes.Value
	}{
		{"both mutually-exclusive attributes set", map[string]tftypes.Value{
			"protocols": emptyList(t, "protocols"),
			"tags":      emptyList(t, "tags"),
		}},
		{"gated attribute under the wrong discriminator value", map[string]tftypes.Value{
			"kind":     tftypes.NewValue(tftypes.String, "basic"),
			"settings": setObject(t, "settings"),
		}},
		{"dependency without its partner", map[string]tftypes.Value{
			"ratio": tftypes.NewValue(tftypes.Number, big.NewFloat(1.5)),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if resp := buildConfig(t, tc.overrides); !resp.Diagnostics.HasError() {
				t.Fatalf("the bad config was accepted: %s", tc.name)
			}
		})
	}
}

func TestValidateConfigAcceptsAGoodConfig(t *testing.T) {
	resp := buildConfig(t, map[string]tftypes.Value{
		"name": tftypes.NewValue(tftypes.String, "ok"),
		"kind": tftypes.NewValue(tftypes.String, "basic"),
		"port": tftypes.NewValue(tftypes.Number, big.NewFloat(8080)),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("a valid config was rejected: %v", resp.Diagnostics.Errors())
	}
}
`

// TestUnit_RenderServices_ValidateConfigRejectsBadConfig renders the fictional
// tree, drops the validateDriver into the http_server resource package and
// runs go test on it. This executes the generated ValidateConfig against bad
// and good configs, proving the emitted edge validators reject a bad
// multi-variant config — the runtime half the render assertions cannot show.
func TestUnit_RenderServices_ValidateConfigRejectsBadConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the toolchain run in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}

	pc := fictionalProviderCore()
	root := writeFictionalKiotaModule(t, pc)

	driver := filepath.Join(root, filepath.FromSlash(
		"internal/services/resources/servers/v7/http_server/zz_validate_config_test.go"))
	if err := os.WriteFile(driver, []byte(validateDriver), 0o600); err != nil {
		t.Fatal(err)
	}

	runGo(t, root, "mod", "tidy")
	runGo(t, root, "test", "-run", "TestValidateConfig",
		"./internal/services/resources/servers/v7/http_server/")
}
