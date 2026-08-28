package sdkbind

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

func TestExportedName(t *testing.T) {
	cases := map[string]string{
		"tags":           "Tags",
		"http-server":    "HttpServer",
		"account_group":  "AccountGroup",
		"accountGroupId": "AccountGroupId",
		"a.b c":          "ABC",
		"":               "",
	}
	for in, want := range cases {
		if got := exportedName(in); got != want {
			t.Errorf("exportedName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLocalFor(t *testing.T) {
	cases := map[string]string{
		"tagId":  "tagId",
		"tag_id": "tagId",
		"type":   "type_",
		"":       "parameter_",
	}
	for in, want := range cases {
		if got := localFor(in); got != want {
			t.Errorf("localFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGoTypeOf(t *testing.T) {
	cases := map[ir.AttributeType]string{
		ir.TypeString:  "string",
		ir.TypeBool:    "bool",
		ir.TypeInt64:   "int64",
		ir.TypeFloat64: "float64",
		ir.TypeList:    "string",
	}
	for in, want := range cases {
		if got := goTypeOf(in); got != want {
			t.Errorf("goTypeOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDatasourceElementTree(t *testing.T) {
	itemTree := &ir.AttributeTree{Attributes: []ir.Attribute{attr("name", "name", ir.TypeString, ir.Computed)}}
	items := attr("items", "items", ir.TypeList, ir.Computed)
	items.Nested = itemTree

	lookup := ir.Datasource{LookupByKey: true, Schema: itemTree}
	if got := datasourceElementTree(lookup); got != itemTree {
		t.Error("lookup datasource should bind its whole schema")
	}

	companion := ir.Datasource{Schema: &ir.AttributeTree{Attributes: []ir.Attribute{items}}}
	if got := datasourceElementTree(companion); got != itemTree {
		t.Error("companion datasource should bind the items element")
	}

	if got := datasourceElementTree(ir.Datasource{}); got != nil {
		t.Error("nil schema should stay nil")
	}

	odd := ir.Datasource{Schema: itemTree}
	if got := datasourceElementTree(odd); got != itemTree {
		t.Error("a tree without items falls back to itself")
	}
}

func TestRenderExpr(t *testing.T) {
	segs := []Segment{
		{Name: "TagsAPI"},
		{Name: "GetTag", Call: true, Args: []string{"ctx", "tagId"}},
		{Name: "Execute", Call: true},
	}
	if got, want := renderExpr(segs), "client.TagsAPI.GetTag(ctx, tagId).Execute()"; got != want {
		t.Errorf("renderExpr = %q, want %q", got, want)
	}
}

func TestRemovalString(t *testing.T) {
	whole := Removal{Kind: "resource", Key: "tags", Reason: "gone"}
	if got := whole.String(); got != "resource tags: gone" {
		t.Errorf("String() = %q", got)
	}
	attr := Removal{Kind: "resource", Key: "tags", Attribute: "detail.note", Reason: "gone"}
	if got := attr.String(); got != "resource tags.detail.note: gone" {
		t.Errorf("String() = %q", got)
	}
}

func TestProblemStringAndReportErr(t *testing.T) {
	p := Problem{Kind: "resource", Key: "tags", Path: "read.expr", Detail: "no"}
	if got := p.String(); got != "resource tags: read.expr: no" {
		t.Errorf("String() = %q", got)
	}
	if err := (Report{}).Err(); err != nil {
		t.Errorf("empty report errs: %v", err)
	}
	err := Report{Problems: []Problem{p}}.Err()
	if err == nil || !strings.Contains(err.Error(), "1 binding problem(s)") {
		t.Errorf("Err() = %v", err)
	}
}

func TestDidYouMean(t *testing.T) {
	if got := didYouMean("x", nil); got != "" {
		t.Errorf("no candidates should say nothing, got %q", got)
	}
	if got := didYouMean("tag", []string{"GetTag", "SetTag"}); !strings.Contains(got, "did you mean") {
		t.Errorf("close matches missing: %q", got)
	}
	many := []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "B"}
	if got := didYouMean("zzz", many); !strings.Contains(got, "available include") {
		t.Errorf("bounded sample missing: %q", got)
	}
	closeMany := []string{"GetA1", "GetA2", "GetA3", "GetA4", "GetA5", "GetA6", "GetA7"}
	if got := didYouMean("get", closeMany); !strings.Contains(got, ", ...?") {
		t.Errorf("bounded close list missing: %q", got)
	}
}

// TestLoaderLookups exercises the loader's misses against a real load,
// which also proves the fake trees type-check — the tests' own gate on
// the fixtures.
func TestLoaderLookups(t *testing.T) {
	l, err := loadSDK(testdataDir(t, "kiotasdk"))
	if err != nil {
		t.Fatalf("loadSDK: %v", err)
	}

	if _, err := l.pkg("example.com/nope"); err == nil {
		t.Error("pkg() found a package that is not there")
	}
	if _, err := l.lookupType("example.com/kiotasdk", "Nope"); err == nil {
		t.Error("lookupType() found a type that is not there")
	}
	if l.functionExists("example.com/nope", "New") {
		t.Error("functionExists() in a missing package")
	}
	if l.functionExists("example.com/kiotasdk/models", "Tag") {
		t.Error("a type is not a function")
	}
	if !l.functionExists("example.com/kiotasdk/models", "NewTag") {
		t.Error("NewTag exists and was not found")
	}

	if _, err := l.typeFromExpr(kiotaInfo(), "models.Tagable"); err != nil {
		t.Errorf("typeFromExpr models: %v", err)
	}
	if _, err := l.typeFromExpr(kiotaInfo(), "APIClient"); err != nil {
		t.Errorf("typeFromExpr unqualified: %v", err)
	}
	if _, err := l.typeFromExpr(kiotaInfo(), "[]models.Tagable"); err != nil {
		t.Errorf("typeFromExpr slice: %v", err)
	}
}

func TestFlipEscaped(t *testing.T) {
	fa := FieldAccess{Get: "GetVendor", Set: "SetVendor"}
	flipEscaped(&fa)
	if fa.Get != "GetVendorEscaped" || fa.Set != "SetVendorEscaped" {
		t.Errorf("flip on = %+v", fa)
	}
	flipEscaped(&fa)
	if fa.Get != "GetVendor" || fa.Set != "SetVendor" {
		t.Errorf("flip off = %+v", fa)
	}
	empty := FieldAccess{Get: "GetX"}
	flipEscaped(&empty)
	if empty.Set != "" {
		t.Errorf("an absent setter grew a spelling: %+v", empty)
	}
}
