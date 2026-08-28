package sdkbind

import (
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// TestOpMethodName holds the drafted service method to the generator's
// naming: the camelised operationId, or its synthesis from path and
// method when the document declares none.
func TestOpMethodName(t *testing.T) {
	cases := []struct {
		name string
		op   *ir.Operation
		want string
	}{
		{"operationId camelises", op(ir.OperationRead, "GET", "/tags/{tagId}", "getTag"), "GetTag"},
		{"kebab operationId", op(ir.OperationRead, "GET", "/tags", "list-tags"), "ListTags"},
		{"snake operationId", op(ir.OperationRead, "GET", "/tags", "list_tags"), "ListTags"},
		{"no operationId synthesises from the path", op(ir.OperationRead, "GET", "/tags/{tagId}", ""), "TagsTagIdGet"},
		{"synthesis keeps every literal segment", op(ir.OperationCreate, "POST", "/v2/account-groups", ""), "V2AccountGroupsPost"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := opMethodName(tc.op); got != tc.want {
				t.Errorf("opMethodName = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestOAGCallSpelling holds the drafted call to the flat service-struct
// shape: service field, operation method with path parameters as
// arguments, fluent body setter, Execute.
func TestOAGCallSpelling(t *testing.T) {
	n := names("tags", "tags")
	info := oagInfo()

	cases := []struct {
		name    string
		op      *ir.Operation
		hasBody bool
		want    string
	}{
		{
			name:    "create carries the body through a setter",
			op:      op(ir.OperationCreate, "POST", "/tags", "createTag"),
			hasBody: true,
			want:    "client.TagsAPI.CreateTag(ctx).Tags(*body).Execute()",
		},
		{
			name: "read passes the path parameter",
			op:   op(ir.OperationRead, "GET", "/tags/{tagId}", "getTag", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
			want: "client.TagsAPI.GetTag(ctx, tagId).Execute()",
		},
		{
			name:    "update passes parameter then body",
			op:      op(ir.OperationUpdate, "PATCH", "/tags/{tagId}", "updateTag", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
			hasBody: true,
			want:    "client.TagsAPI.UpdateTag(ctx, tagId).Tags(*body).Execute()",
		},
		{
			name: "delete",
			op:   op(ir.OperationDelete, "DELETE", "/tags/{tagId}", "deleteTag", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
			want: "client.TagsAPI.DeleteTag(ctx, tagId).Execute()",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := openAPIGeneratorBinder{}.call(tc.op, n, tc.hasBody, info)
			if c.Expr != tc.want {
				t.Errorf("Expr = %q, want %q", c.Expr, tc.want)
			}
			if c.Expr != renderExpr(c.Segments) {
				t.Errorf("Expr disagrees with Segments: %q", c.Expr)
			}
		})
	}
}

// TestOAGCallDrafts checks payload drafts per operation kind.
func TestOAGCallDrafts(t *testing.T) {
	n := names("tags", "tags")
	info := oagInfo()

	create := openAPIGeneratorBinder{}.call(op(ir.OperationCreate, "POST", "/tags", "createTag"), n, true, info)
	if create.ResponseType != "*sdk.Tags" || create.RequestType != "sdk.Tags" {
		t.Errorf("create drafts = response %q request %q", create.ResponseType, create.RequestType)
	}
	if len(create.Results) != 3 {
		t.Errorf("create Results = %v, want 3 entries", create.Results)
	}

	del := openAPIGeneratorBinder{}.call(
		op(ir.OperationDelete, "DELETE", "/tags/{tagId}", "deleteTag", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
		n, false, info)
	if del.ResponseType != "" || len(del.Results) != 2 {
		t.Errorf("delete drafts = response %q results %v", del.ResponseType, del.Results)
	}
}

// TestOAGAccessSpelling holds drafted accessors to the generator's
// deref-on-the-way-out helper shape: value-typed, no mangling.
func TestOAGAccessSpelling(t *testing.T) {
	fa := openAPIGeneratorBinder{}.access(attribute("name", "name", ir.TypeString, ir.Required), accessReadWrite)
	if fa.Get != "GetName" || fa.Set != "SetName" || fa.SDKType != "string" {
		t.Errorf("access = %+v, want value-typed GetName/SetName", fa)
	}
	if fa.ConvertGet != "FromString" || fa.ConvertSet != "ToString" {
		t.Errorf("converts = %q/%q, want FromString/ToString", fa.ConvertGet, fa.ConvertSet)
	}

	labels := attribute("labels", "labels", ir.TypeList, ir.Optional)
	labels.ElementType = ir.TypeString
	fa = openAPIGeneratorBinder{}.access(labels, accessReadWrite)
	if fa.SDKType != "[]string" || fa.ConvertGet != "FromStringSlice" {
		t.Errorf("slice access = %+v", fa)
	}

	// The generator does not mangle reserved words the way kiota does.
	fa = openAPIGeneratorBinder{}.access(attribute("error", "error", ir.TypeString, ir.Optional), accessReadWrite)
	if fa.Get != "GetError" {
		t.Errorf("reserved wire name drafted %q, want GetError", fa.Get)
	}
}

// TestBinderFor checks selection follows the config's backend.
func TestBinderFor(t *testing.T) {
	cases := []struct {
		backend string
		want    string
		wantErr bool
	}{
		{"kiota", "kiota", false},
		{"openapi-generator", "openapi-generator", false},
		{"resty", "", true},
	}
	for _, tc := range cases {
		configuration := minimalConfig(tc.backend)
		b, err := For(configuration)
		if tc.wantErr {
			if err == nil {
				t.Errorf("For(%q) accepted an unsupported backend", tc.backend)
			}
			continue
		}
		if err != nil {
			t.Errorf("For(%q): %v", tc.backend, err)
			continue
		}
		if b.Name() != tc.want {
			t.Errorf("For(%q).Name() = %q", tc.backend, b.Name())
		}
	}
}

// TestInfoFor checks the models package placement per backend.
func TestInfoFor(t *testing.T) {
	kio, err := InfoFor(minimalConfig("kiota"), "example.com/provider/internal/sdk")
	if err != nil {
		t.Fatalf("InfoFor kiota: %v", err)
	}
	if kio.ModelsImportPath != "example.com/provider/internal/sdk/models" {
		t.Errorf("kiota models import = %q", kio.ModelsImportPath)
	}

	oag, err := InfoFor(minimalConfig("openapi-generator"), "example.com/provider/internal/sdk")
	if err != nil {
		t.Fatalf("InfoFor openapi-generator: %v", err)
	}
	if oag.ModelsImportPath != "example.com/provider/internal/sdk" {
		t.Errorf("openapi-generator models import = %q", oag.ModelsImportPath)
	}
	if oag.ClientTypeName != "APIClient" {
		t.Errorf("client type = %q, want the config default", oag.ClientTypeName)
	}

	if _, err := InfoFor(minimalConfig("resty"), "example.com/x"); err == nil {
		t.Error("InfoFor accepted an unsupported backend")
	}
}
