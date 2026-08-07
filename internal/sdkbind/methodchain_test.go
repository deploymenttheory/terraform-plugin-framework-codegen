package sdkbind

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// kiotaLoader resolves the miniature kiota-shaped SDK module in testdata. One
// per test run, like sharedLoader: go/packages loads are the expensive part.
var kiotaLoader = NewLoader("testdata/kiotasdk")

func kiotaClientType(t *testing.T) *blueprint.ServiceRef {
	t.Helper()
	return &blueprint.ServiceRef{ImportPath: "example.com/kiotasdk/models", Alias: "models", Accessor: "r.client"}
}

func chain(segs ...blueprint.ChainSegment) []blueprint.ChainSegment { return segs }

func seg(method string, args ...blueprint.Argument) blueprint.ChainSegment {
	return blueprint.ChainSegment{Method: method, Args: args}
}

var (
	argCtx = blueprint.Argument{Kind: blueprint.ArgContext}
	argNil = blueprint.Argument{Kind: blueprint.ArgLiteral, Expr: "nil"}
	argID  = blueprint.Argument{Kind: blueprint.ArgStateField, Field: "ID"}
)

func TestUnit_SDKBind_MethodChainWalksAFluentCall(t *testing.T) {
	t.Parallel()

	client, err := kiotaLoader.LookupType("example.com/kiotasdk/client", "ApiClient")
	if err != nil {
		t.Fatalf("loading the client type: %v", err)
	}

	t.Run("a full read chain resolves to its verb", func(t *testing.T) {
		t.Parallel()
		m, err := kiotaLoader.MethodChain(client, chain(
			seg("Tags"),
			seg("ByTagId", argID),
			seg("Get", argCtx, argNil),
		))
		if err != nil {
			t.Fatalf("MethodChain: %v", err)
		}
		if m.Name != "Get" || len(m.Results) != 2 {
			t.Errorf("final method = %s %v", m.Name, m.Results)
		}
		if !strings.Contains(m.Results[0], "Tagable") {
			t.Errorf("result type = %v, want the Tagable interface", m.Results)
		}
	})

	t.Run("a delete chain returns error alone", func(t *testing.T) {
		t.Parallel()
		m, err := kiotaLoader.MethodChain(client, chain(
			seg("Tags"), seg("ByTagId", argID), seg("Delete", argCtx, argNil),
		))
		if err != nil {
			t.Fatalf("MethodChain: %v", err)
		}
		if len(m.Results) != 1 {
			t.Errorf("Delete results = %v", m.Results)
		}
	})

	t.Run("a misspelled hop names the neighbours", func(t *testing.T) {
		t.Parallel()
		_, err := kiotaLoader.MethodChain(client, chain(seg("Tag"), seg("Get", argCtx, argNil)))
		if err == nil || !strings.Contains(err.Error(), "Tags") {
			t.Errorf("want a did-you-mean naming Tags, got: %v", err)
		}
	})

	t.Run("a wrong argument count names the signature", func(t *testing.T) {
		t.Parallel()
		_, err := kiotaLoader.MethodChain(client, chain(seg("Tags"), seg("ByTagId"), seg("Get", argCtx, argNil)))
		if err == nil || !strings.Contains(err.Error(), "declares 0 argument(s) but the method takes 1") {
			t.Errorf("want the arity refusal, got: %v", err)
		}
	})

	t.Run("a two-result builder hop cannot anchor a chain", func(t *testing.T) {
		t.Parallel()
		_, err := kiotaLoader.MethodChain(client, chain(seg("Tags"), seg("TwoResults"), seg("Get", argCtx, argNil)))
		if err == nil || !strings.Contains(err.Error(), "must return exactly one") {
			t.Errorf("want the builder-hop refusal, got: %v", err)
		}
	})
}

func TestUnit_SDKBind_AccessorPairs(t *testing.T) {
	t.Parallel()

	const models = "example.com/kiotasdk/models"

	t.Run("a get and set pair on the concrete model", func(t *testing.T) {
		t.Parallel()
		if _, err := kiotaLoader.LookupAccessorPair(models, "Tag", "Name", true); err != nil {
			t.Errorf("LookupAccessorPair: %v", err)
		}
	})

	t.Run("a getter-only interface passes without the setter", func(t *testing.T) {
		t.Parallel()
		if _, err := kiotaLoader.LookupAccessorPair(models, "Tagable", "Name", false); err != nil {
			t.Errorf("read-side pair: %v", err)
		}
	})

	t.Run("a read-only field fails when a setter is demanded", func(t *testing.T) {
		t.Parallel()
		_, err := kiotaLoader.LookupAccessorPair(models, "Tag", "Id", true)
		if err == nil || !strings.Contains(err.Error(), "SetId") {
			t.Errorf("want the missing setter named, got: %v", err)
		}
	})

	t.Run("a mangling miss names the real spelling", func(t *testing.T) {
		t.Parallel()
		_, err := kiotaLoader.LookupAccessorPair(models, "Tag", "Error", true)
		if err == nil || !strings.Contains(err.Error(), "GetErrorEscaped") {
			t.Errorf("want the Escaped spelling suggested, got: %v", err)
		}
	})

	t.Run("the dispatcher routes struct style to fields", func(t *testing.T) {
		t.Parallel()
		// The concrete Tag has unexported fields only, so struct-style lookup
		// must fail while method-style succeeds -- proving the dispatch.
		if _, err := kiotaLoader.LookupFieldAccess(blueprint.AccessStructField, models, "Tag", "Name", true); err == nil {
			t.Error("struct-style lookup of a method-access model must fail")
		}
		if _, err := kiotaLoader.LookupFieldAccess(blueprint.AccessMethod, models, "Tag", "Name", true); err != nil {
			t.Errorf("method-style lookup: %v", err)
		}
	})
}

func TestUnit_SDKBind_FluentOperationVerifies(t *testing.T) {
	t.Parallel()

	client, err := kiotaLoader.LookupType("example.com/kiotasdk/client", "ApiClient")
	if err != nil {
		t.Fatalf("loading the client type: %v", err)
	}
	svc := *kiotaClientType(t)

	t.Run("a sound fluent read passes", func(t *testing.T) {
		t.Parallel()
		var r Report
		verifyOperation(kiotaLoader, client, "tag", svc, "read", blueprint.Operation{
			Style:      blueprint.CallStyleFluent,
			Chain:      chain(seg("Tags"), seg("ByTagId", argID), seg("Get", argCtx, argNil)),
			Return:     blueprint.ReturnResultError,
			ResultType: "models.Tagable",
		}, &r)
		if len(r.Problems) != 0 {
			t.Fatalf("unexpected problems: %v", r.Problems)
		}
	})

	t.Run("a wrong result type is named", func(t *testing.T) {
		t.Parallel()
		var r Report
		verifyOperation(kiotaLoader, client, "tag", svc, "read", blueprint.Operation{
			Style:      blueprint.CallStyleFluent,
			Chain:      chain(seg("Tags"), seg("ByTagId", argID), seg("Get", argCtx, argNil)),
			Return:     blueprint.ReturnResultError,
			ResultType: "models.Widget",
		}, &r)
		if len(r.Problems) != 1 || !strings.Contains(r.Problems[0].Detail, "Tagable") {
			t.Fatalf("want the real result type named, got: %v", r.Problems)
		}
	})

	t.Run("a transport arity cannot be satisfied", func(t *testing.T) {
		t.Parallel()
		var r Report
		verifyOperation(kiotaLoader, client, "tag", svc, "read", blueprint.Operation{
			Style:      blueprint.CallStyleFluent,
			Chain:      chain(seg("Tags"), seg("ByTagId", argID), seg("Get", argCtx, argNil)),
			Return:     blueprint.ReturnResultTransportError,
			ResultType: "models.Tagable",
		}, &r)
		if len(r.Problems) != 1 || !strings.Contains(r.Problems[0].Path, "return") {
			t.Fatalf("want the arity mismatch on .return, got: %v", r.Problems)
		}
	})
}

// TestUnit_SDKBind_ASelectorRecordsWhetherTheElementHoldsAnEnumeration.
//
// The list element is a different model from the by-id read's response and can
// carry the same field at a different type, so the attribute's own reconciled
// spelling says nothing about the selector. Whether the getter yields a kiota
// enumeration decides whether the generated comparison may read the value
// directly or has to go through String(), which is why it is settled against
// the loaded SDK rather than guessed from the document.
func TestUnit_SDKBind_ASelectorRecordsWhetherTheElementHoldsAnEnumeration(t *testing.T) {
	t.Parallel()

	const models = "example.com/kiotasdk/models"

	d := &blueprint.DataSource{
		Key: "tag",
		Binding: blueprint.DataSourceBinding{
			Service:     blueprint.ServiceRef{ImportPath: models, Alias: "models"},
			ElementType: "models.TagSummaryable",
			Response: blueprint.ResponseModel{
				Type: "models.Tagable", AccessStyle: blueprint.AccessMethod,
			},
			Selectors: []blueprint.Selector{
				{Attribute: "id", GoField: "ID", ViaRead: true},
				{Attribute: "name", GoField: "Name", SDKField: "Name"},
				{Attribute: "kind", GoField: "Kind", SDKField: "Kind"},
			},
		},
	}

	reconcileSelectors(kiotaLoader, d)

	sel := d.Binding.Selectors
	if sel[0].SDKEnum {
		t.Error("the identifier selector reaches no element getter, so it claims nothing")
	}
	if sel[1].SDKEnum {
		t.Error("a *string getter is not an enumeration")
	}
	if !sel[2].SDKEnum {
		t.Error("a pointer to an int-backed named type with a Parse function is an enumeration")
	}
}
