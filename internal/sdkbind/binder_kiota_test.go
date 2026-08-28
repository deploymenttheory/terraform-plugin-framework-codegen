package sdkbind

import (
	"reflect"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// TestKiotaChainSpelling holds the drafted fluent chain to the spelling a
// kiota-generated SDK actually uses, per path template.
func TestKiotaChainSpelling(t *testing.T) {
	n := names("tags", "tags")
	info := kiotaInfo()

	cases := []struct {
		name    string
		op      *ir.Operation
		hasBody bool
		want    string
	}{
		{
			name:    "create is a collection post",
			op:      op(ir.OperationCreate, "POST", "/tags", ""),
			hasBody: true,
			want:    "client.Tags().Post(ctx, body, nil)",
		},
		{
			name: "read indexes by the path parameter",
			op:   op(ir.OperationRead, "GET", "/tags/{tagId}", "", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
			want: "client.Tags().ByTagId(tagId).Get(ctx, nil)",
		},
		{
			name:    "update keeps the declared verb",
			op:      op(ir.OperationUpdate, "PATCH", "/tags/{tagId}", "", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
			hasBody: true,
			want:    "client.Tags().ByTagId(tagId).Patch(ctx, body, nil)",
		},
		{
			name: "delete takes no body",
			op:   op(ir.OperationDelete, "DELETE", "/tags/{tagId}", "", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
			want: "client.Tags().ByTagId(tagId).Delete(ctx, nil)",
		},
		{
			name: "kebab and nested segments become builder hops",
			op: op(ir.OperationRead, "GET", "/v7/http-server/{serverId}/status", "",
				ir.Parameter{Name: "serverId", Type: ir.TypeString}),
			want: "client.V7().HttpServer().ByServerId(serverId).Status().Get(ctx, nil)",
		},
		{
			name: "a keyword-named parameter gets a safe local",
			op: op(ir.OperationRead, "GET", "/types/{type}", "",
				ir.Parameter{Name: "type", Type: ir.TypeString}),
			want: "client.Types().ByType(type_).Get(ctx, nil)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := kiotaBinder{}.call(tc.op, n, tc.hasBody, info)
			if c.Expr != tc.want {
				t.Errorf("Expr = %q, want %q", c.Expr, tc.want)
			}
			if c.Expr != renderExpr(c.Segments) {
				t.Errorf("Expr disagrees with Segments: %q", c.Expr)
			}
		})
	}
}

// TestKiotaCallDrafts checks the payload drafts and parameters the chain
// carries beside its spelling.
func TestKiotaCallDrafts(t *testing.T) {
	n := names("tags", "tags")
	read := kiotaBinder{}.call(
		op(ir.OperationRead, "GET", "/tags/{tagId}", "", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
		n, false, kiotaInfo())

	if read.ResponseType != "models.Tagsable" {
		t.Errorf("drafted ResponseType = %q, want models.Tagsable", read.ResponseType)
	}
	wantParams := []CallParameter{{Local: "tagId", GoType: "string", Wire: "tagId"}}
	if !reflect.DeepEqual(read.Parameters, wantParams) {
		t.Errorf("Params = %+v, want %+v", read.Parameters, wantParams)
	}
	wantImports := []string{"example.com/kiotasdk", "example.com/kiotasdk/models"}
	if !reflect.DeepEqual(read.Imports, wantImports) {
		t.Errorf("Imports = %v, want %v", read.Imports, wantImports)
	}

	del := kiotaBinder{}.call(
		op(ir.OperationDelete, "DELETE", "/tags/{tagId}", "", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
		n, false, kiotaInfo())
	if del.ResponseType != "" || len(del.Results) != 1 || del.Results[0] != "error" {
		t.Errorf("delete draft = response %q results %v, want none and [error]", del.ResponseType, del.Results)
	}
}

// TestKiotaAccessSpelling holds drafted accessors to kiota's Get/Set
// spelling, including the reserved-word mangling.
func TestKiotaAccessSpelling(t *testing.T) {
	cases := []struct {
		name     string
		a        ir.Attribute
		mode     accessMode
		wantGet  string
		wantSet  string
		wantType string
	}{
		{
			name: "plain scalar", a: attr("name", "name", ir.TypeString, ir.Required),
			mode: accessReadWrite, wantGet: "GetName", wantSet: "SetName", wantType: "*string",
		},
		{
			name: "reserved word is escaped", a: attr("error", "error", ir.TypeString, ir.Optional),
			mode: accessReadWrite, wantGet: "GetErrorEscaped", wantSet: "SetErrorEscaped", wantType: "*string",
		},
		{
			name: "computed never writes", a: attr("id", "id", ir.TypeString, ir.Computed),
			mode: accessReadWrite, wantGet: "GetId", wantSet: "", wantType: "*string",
		},
		{
			name: "read-only mode never writes", a: attr("name", "name", ir.TypeString, ir.Required),
			mode: accessReadOnly, wantGet: "GetName", wantSet: "", wantType: "*string",
		},
		{
			name: "write-only mode never reads", a: attr("name", "name", ir.TypeString, ir.Required),
			mode: accessWriteOnly, wantGet: "", wantSet: "SetName", wantType: "*string",
		},
		{
			name: "camel wire name keeps its humps", a: attr("account_group_id", "accountGroupId", ir.TypeInt64, ir.Optional),
			mode: accessReadWrite, wantGet: "GetAccountGroupId", wantSet: "SetAccountGroupId", wantType: "*int64",
		},
		{
			name: "bool scalar", a: attr("enabled", "enabled", ir.TypeBool, ir.Optional),
			mode: accessReadWrite, wantGet: "GetEnabled", wantSet: "SetEnabled", wantType: "*bool",
		},
		{
			name: "float scalar", a: attr("weight", "weight", ir.TypeFloat64, ir.Optional),
			mode: accessReadWrite, wantGet: "GetWeight", wantSet: "SetWeight", wantType: "*float64",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fa := kiotaBinder{}.access(tc.a, tc.mode)
			if fa.Get != tc.wantGet || fa.Set != tc.wantSet || fa.SDKType != tc.wantType {
				t.Errorf("access = get %q set %q type %q, want %q %q %q",
					fa.Get, fa.Set, fa.SDKType, tc.wantGet, tc.wantSet, tc.wantType)
			}
			if fa.Get == "" && fa.ConvertGet != "" {
				t.Errorf("ConvertGet %q without a getter", fa.ConvertGet)
			}
			if fa.Set == "" && fa.ConvertSet != "" {
				t.Errorf("ConvertSet %q without a setter", fa.ConvertSet)
			}
		})
	}
}

// TestKiotaBindWalk checks the model walk drafts every kind and skips
// what derivation refused.
func TestKiotaBindWalk(t *testing.T) {
	b, err := kiotaBinder{}.Bind(kiotaModel(), kiotaInfo())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	rb := b.Resources["tags"]
	if rb == nil {
		t.Fatal("no binding for resource tags")
	}
	if rb.Create == nil || rb.Read == nil || rb.Update == nil || rb.Delete == nil {
		t.Fatal("resource tags is missing lifecycle calls")
	}
	if rb.ReadModel != "models.Tagsable" || rb.WriteModel != "models.Tags" {
		t.Errorf("drafted models = %q / %q", rb.ReadModel, rb.WriteModel)
	}
	for _, fb := range rb.Fields {
		if fb.Attr == "free" {
			t.Error("unsupported attribute free was drafted a binding")
		}
	}

	db := b.Datasources["tags"]
	if db == nil || db.Read == nil || db.List == nil {
		t.Fatal("datasource tags is missing calls")
	}
	// The companion's fields are the element's, not the filter vocabulary.
	if len(db.Fields) != 2 || db.Fields[0].Attr != "id" || db.Fields[1].Attr != "name" {
		t.Errorf("datasource fields = %+v, want the items element's", db.Fields)
	}
	for _, fb := range db.Fields {
		if fb.Access.Set != "" {
			t.Errorf("datasource field %s drafted a setter", fb.Attr)
		}
	}

	ab := b.Actions["tags_assign"]
	if ab == nil || ab.Invoke == nil {
		t.Fatal("action tags_assign is missing its invoke call")
	}
	if want := "client.Tags().ByTagId(tagId).Assign().Post(ctx, body, nil)"; ab.Invoke.Expr != want {
		t.Errorf("invoke Expr = %q, want %q", ab.Invoke.Expr, want)
	}
	if ab.WriteModel == "" || ab.WriteConstructor == "" {
		t.Error("action with a body drafted no write model")
	}
}
