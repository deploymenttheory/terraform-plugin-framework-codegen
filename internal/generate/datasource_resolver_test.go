package generate

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// selectorBlueprint is a fluent, selector-shaped data source: looked up by
// exactly one of id (direct read) or test_name (list-resolved).
func selectorBlueprint() (blueprint.Blueprint, blueprint.DataSource) {
	ds := blueprint.DataSource{
		Key: "test", Name: "test", GoPackage: "test",
		GoPackageAlias: "testv7", GoTypeName: "TestDataSource",
		ModelTypeName: "TestDataSourceModel",
		Binding: blueprint.DataSourceBinding{
			Service: blueprint.ServiceRef{
				ImportPath: "example.com/prov/internal/sdk/models",
				Alias:      "models",
				Accessor:   "d.client",
			},
			Read: &blueprint.Operation{
				Style: blueprint.CallStyleFluent,
				Chain: []blueprint.ChainSegment{
					{Method: "Tests"},
					{Method: "ByTestId", Args: []blueprint.Argument{{Kind: blueprint.ArgConfigField, Field: "ID"}}},
					{Method: "Get", Args: []blueprint.Argument{{Kind: blueprint.ArgContext}, {Kind: blueprint.ArgLiteral, Expr: "nil"}}},
				},
				Return: blueprint.ReturnResultError, ResultType: "models.Testable",
			},
			List: &blueprint.Operation{
				Style: blueprint.CallStyleFluent,
				Chain: []blueprint.ChainSegment{
					{Method: "Tests"},
					{Method: "Get", Args: []blueprint.Argument{{Kind: blueprint.ArgContext}, {Kind: blueprint.ArgLiteral, Expr: "nil"}}},
				},
				Return: blueprint.ReturnResultError, ResultType: "models.Testsable",
			},
			CollectionField: "GetTests()",
			ElementType:     "models.SimpleTestable",
			Selectors: []blueprint.Selector{
				{Attribute: "id", GoField: "ID", ViaRead: true},
				{Attribute: "test_name", GoField: "TestName", SDKField: "TestName"},
			},
			ElementIDField:   "TestId",
			ElementIDFlatten: &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
			Response: blueprint.ResponseModel{
				Type: "models.Testable", AccessStyle: blueprint.AccessMethod,
			},
		},
	}
	idAttr := attr("id", blueprint.KindString, blueprint.ComputedOptional)
	idAttr.GoField = "ID"
	idAttr.Wire = blueprint.WireBinding{
		JSONPath: "testId", SDKField: "TestId", SDKGoType: "*string",
		Flatten: &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
	}
	nameAttr := attr("test_name", blueprint.KindString, blueprint.ComputedOptional)
	nameAttr.GoField = "TestName"
	nameAttr.Wire = blueprint.WireBinding{
		JSONPath: "testName", SDKField: "TestName", SDKGoType: "*string",
		Flatten: &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
	}
	ds.Schema.Attributes = []blueprint.Attribute{idAttr, nameAttr}

	bp := blueprint.Blueprint{
		FormatVersion: blueprint.FormatVersion,
		Provider: blueprint.Provider{
			Name: "te", TypePrefix: "te", GoModule: "example.com/prov",
			SDK: blueprint.SDKModule{
				ModulePath: "example.com/prov", ClientType: "*sdk.Client",
				Dialect: blueprint.DialectKiotaFluent,
			},
		},
		DataSources: []blueprint.DataSource{ds},
	}
	return bp, ds
}

// TestUnit_Generate_SelectorResolverView proves the resolver view carries the
// contract: exactly-one enforcement inputs, the matcher against the element,
// and the identifier handoff into the direct read.
func TestUnit_Generate_SelectorResolverView(t *testing.T) {
	t.Parallel()

	bp, ds := selectorBlueprint()

	v, err := DataSource(bp, ds, Options{})
	if err != nil {
		t.Fatalf("DataSource: %v", err)
	}
	if v.Resolve == nil {
		t.Fatal("a binding with a list must produce a resolver")
	}

	r := v.Resolve
	if r.MapsElement {
		t.Error("a binding with a direct read must not map the element")
	}
	if r.IDGoField != "ID" {
		t.Errorf("IDGoField = %q", r.IDGoField)
	}
	if r.SelectorList != "id, test_name" {
		t.Errorf("SelectorList = %q", r.SelectorList)
	}
	if len(r.Matchers) != 1 || r.Matchers[0].Getter != "el.GetTestName()" {
		t.Errorf("matchers = %+v", r.Matchers)
	}
	if r.ElementIDExpr != "convert.PtrStringToFramework(match.GetTestId())" {
		t.Errorf("ElementIDExpr = %q", r.ElementIDExpr)
	}
	if r.List.ResultVar != "listing" {
		t.Errorf("the list call must bind listing, got %q", r.List.ResultVar)
	}
}

// TestUnit_Generate_SelectorResolverRenders proves the read template renders
// the whole contract: the exactly-one refusal, the list-and-match loop, the
// zero and many refusals, and the identifier handoff before the direct read.
func TestUnit_Generate_SelectorResolverRenders(t *testing.T) {
	t.Parallel()

	bp, ds := selectorBlueprint()

	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v, err := DataSource(bp, ds, Options{})
	if err != nil {
		t.Fatalf("DataSource: %v", err)
	}
	out, err := g.renderFile("datasource_read.go.tmpl", v)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	text := string(out)

	for _, want := range []string{
		`Set exactly one of: id, test_name.`,
		`if data.ID.IsNull() {`,
		`listing, err := d.client.Tests().Get(ctx, nil)`,
		`for _, el := range listing.GetTests() {`,
		`el.GetTestName() == nil || *el.GetTestName() != data.TestName.ValueString()`,
		`"Ambiguous match"`,
		`"No match"`,
		`data.ID = convert.PtrStringToFramework(match.GetTestId())`,
		`remote, err := d.client.Tests().ByTestId(data.ID.ValueString()).Get(ctx, nil)`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the rendered read is missing %q:\n%s", want, text)
		}
	}
}

// TestUnit_Generate_AnEnumSelectorComparesThroughString.
//
// A generated SDK holds a documented value set as a named type of its own, not
// as a string. Comparing that against a configured value is not a wrong answer
// but a compile error, so the resolver goes through the enumeration's String().
// The whole comparison is decided here rather than in the template, which only
// knows there is one.
func TestUnit_Generate_AnEnumSelectorComparesThroughString(t *testing.T) {
	t.Parallel()

	bp, ds := selectorBlueprint()
	ds.Binding.Selectors[1].SDKEnum = true
	bp.DataSources[0] = ds

	v, err := DataSource(bp, ds, Options{})
	if err != nil {
		t.Fatalf("DataSource: %v", err)
	}
	if v.Resolve == nil || len(v.Resolve.Matchers) != 1 {
		t.Fatalf("expected one matcher, got %+v", v.Resolve)
	}
	const want = "el.GetTestName().String() != data.TestName.ValueString()"
	if got := v.Resolve.Matchers[0].Mismatch; got != want {
		t.Errorf("Mismatch = %q, want %q", got, want)
	}

	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := g.renderFile("datasource_read.go.tmpl", v)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// The nil guard still comes first, so reaching String() through the pointer
	// is safe.
	if wantLine := "el.GetTestName() == nil || " + want; !strings.Contains(string(out), wantLine) {
		t.Errorf("the rendered read is missing %q:\n%s", wantLine, out)
	}
}

// TestUnit_Generate_AnEnumSelectorRefusesANonStringAttribute: String() yields a
// string and nothing else, so an enumeration selector on an attribute of some
// other kind is refused by name rather than rendered into a compile error.
func TestUnit_Generate_AnEnumSelectorRefusesANonStringAttribute(t *testing.T) {
	t.Parallel()

	bp, ds := selectorBlueprint()
	ds.Binding.Selectors[1].SDKEnum = true
	ds.Schema.Attributes[1].Type.Kind = blueprint.KindInt64
	bp.DataSources[0] = ds

	_, err := DataSource(bp, ds, Options{})
	if err == nil || !strings.Contains(err.Error(), "test_name") {
		t.Fatalf("the refusal must name the selector: %v", err)
	}
}

// TestUnit_Generate_AnInt64IdentifierIsFormattedForTheSDK.
//
// The toolkit was written against an API whose object identifiers are all
// strings, and rendered .ValueString() on every one of them. types.Int64 has no
// such method. The identifier still reaches the SDK as a string -- a kiota
// indexer takes one whatever the path parameter's declared type -- so what the
// attribute's kind decides is how the string is produced.
func TestUnit_Generate_AnInt64IdentifierIsFormattedForTheSDK(t *testing.T) {
	t.Parallel()

	bp, ds := selectorBlueprint()
	id := &ds.Schema.Attributes[0]
	id.Type.Kind = blueprint.KindInt64
	id.Wire.SDKGoType = "*int64"
	id.Wire.Flatten = &blueprint.ConvertCall{Func: "convert.PtrInt64ToFramework"}
	ds.Binding.ElementIDFlatten = &blueprint.ConvertCall{Func: "convert.PtrInt64ToFramework"}
	bp.DataSources[0] = ds

	v, err := DataSource(bp, ds, Options{})
	if err != nil {
		t.Fatalf("DataSource: %v", err)
	}
	const want = "d.client.Tests().ByTestId(strconv.FormatInt(data.ID.ValueInt64(), 10)).Get(ctx, nil)"
	if v.Read.Call != want {
		t.Errorf("Read.Call = %q, want %q", v.Read.Call, want)
	}
	// The conversion declares its own import, or read.go does not compile.
	if !strings.Contains(v.Imports.Read, `"strconv"`) {
		t.Errorf("read.go must import strconv:\n%s", v.Imports.Read)
	}
}
