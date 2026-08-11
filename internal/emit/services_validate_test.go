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
// its partner — and one valid config, and validates each through the real
// terraform-plugin-framework server the way terraform itself would. That path
// runs both stock layers together: the schema's attribute-level AlsoRequires
// and the resource's ConfigValidators. It proves at runtime that the emitted
// stock-idiom validators reject a bad multi-variant config and accept a good
// one.
//
// The config is built from the resource's own schema: every attribute defaults
// to null, then the case overrides the few that matter, so the driver does not
// depend on the full attribute list.
const validateDriver = `package pkgservers

import (
	"context"
	"math/big"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// validateProvider is a throwaway single-resource provider exposing only the
// http_server resource, so a config can be validated through the framework
// server without pulling in the whole generated provider (which would cycle).
type validateProvider struct{}

func (validateProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "petstore"
}
func (validateProvider) Schema(_ context.Context, _ provider.SchemaRequest, _ *provider.SchemaResponse) {
}
func (validateProvider) Configure(_ context.Context, _ provider.ConfigureRequest, _ *provider.ConfigureResponse) {
}
func (validateProvider) DataSources(_ context.Context) []func() datasource.DataSource { return nil }
func (validateProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewHTTPServerResource}
}

// validateConfig builds a config from the resource schema — every attribute
// null, then the case's overrides — and validates it through the provider
// server, returning the diagnostics terraform would see. This runs the schema
// attribute validators and the resource ConfigValidators in one pass.
func validateConfig(t *testing.T, overrides map[string]tftypes.Value) []*tfprotov6.Diagnostic {
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
	dv, err := tfprotov6.NewDynamicValue(objType, tftypes.NewValue(objType, vals))
	if err != nil {
		t.Fatal(err)
	}

	server := providerserver.NewProtocol6(validateProvider{})()
	if _, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{}); err != nil {
		t.Fatal(err)
	}
	resp, err := server.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{
		TypeName: ResourceName,
		Config:   &dv,
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Diagnostics
}

func hasError(diags []*tfprotov6.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			return true
		}
	}
	return false
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
			if !hasError(validateConfig(t, tc.overrides)) {
				t.Fatalf("the bad config was accepted: %s", tc.name)
			}
		})
	}
}

func TestValidateConfigAcceptsAGoodConfig(t *testing.T) {
	diags := validateConfig(t, map[string]tftypes.Value{
		"name": tftypes.NewValue(tftypes.String, "ok"),
		"kind": tftypes.NewValue(tftypes.String, "basic"),
		"port": tftypes.NewValue(tftypes.Number, big.NewFloat(8080)),
	})
	if hasError(diags) {
		t.Fatalf("a valid config was rejected: %v", diags)
	}
}
`

// TestUnit_RenderServices_ValidateConfigRejectsBadConfig renders the fictional
// tree, drops the validateDriver into the http_server resource package and
// runs go test on it. This validates bad and good configs through the real
// framework server, proving the emitted stock-idiom validators reject a bad
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
