package sdkbind

import (
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// TestPruneKiota resolves the kiota drafts against the fake SDK: types
// settle to what the SDK carries, repairable spellings are repaired, and
// what the SDK cannot express goes with a recorded reason.
func TestPruneKiota(t *testing.T) {
	b, removed := prunedKiota(t)

	t.Run("the unreachable entity is removed with the chain's reason", func(t *testing.T) {
		if _, ok := b.Resources["widgets"]; ok {
			t.Fatal("widgets survived pruning against an SDK that does not carry it")
		}
		r := findRemoval(t, removed, "resource", "widgets", "")
		if !strings.Contains(r.Reason, "has no method Widgets") {
			t.Errorf("reason = %q, want it to name the missing chain hop", r.Reason)
		}
	})

	rb := b.Resources["tags"]
	if rb == nil {
		t.Fatal("resource tags did not survive pruning")
	}

	t.Run("payload types settle to the SDK's", func(t *testing.T) {
		if rb.Read.Expr != "client.Tags().ByTagId(tagId).Get(ctx, nil)" {
			t.Errorf("read Expr = %q", rb.Read.Expr)
		}
		if rb.Read.ResponseType != "models.Tagable" {
			t.Errorf("read ResponseType = %q, want models.Tagable (drafted Tagsable repaired)", rb.Read.ResponseType)
		}
		if rb.ReadModel != "models.Tagable" || rb.WriteModel != "models.Tag" {
			t.Errorf("models = %q / %q, want models.Tagable / models.Tag", rb.ReadModel, rb.WriteModel)
		}
		if rb.WriteConstructor != "models.NewTag()" {
			t.Errorf("WriteConstructor = %q", rb.WriteConstructor)
		}
		if rb.Create.RequestType != "models.Tagable" {
			t.Errorf("create RequestType = %q", rb.Create.RequestType)
		}
		if len(rb.Delete.Results) != 1 || rb.Delete.Results[0] != "error" {
			t.Errorf("delete Results = %v", rb.Delete.Results)
		}
	})

	t.Run("a delete sends the query parameters it requires through the SDK's types", func(t *testing.T) {
		want := "client.Tags().ByTagId(tagId).Delete(ctx, &abstractions.RequestConfiguration[sdk.TagsItemRequestBuilderDeleteQueryParameters]" +
			`{QueryParameters: &sdk.TagsItemRequestBuilderDeleteQueryParameters{Confirm: convert.PointerTo(true), Reason: convert.PointerTo("retired")}})`
		if rb.Delete.Expr != want {
			t.Errorf("delete Expr = %q\nwant %q", rb.Delete.Expr, want)
		}
		if b.OperationPackages["abstractions"] != "example.com/kiotasdk/abstractions" {
			t.Errorf("the request configuration's package was not recorded: %v", b.OperationPackages)
		}
	})

	t.Run("scalar widths settle to the SDK's", func(t *testing.T) {
		count := findField(t, rb.Fields, "count")
		if count.Access.SDKType != "*int32" || count.Access.ConvertGet != "FromPtrInt32" ||
			count.Access.ConvertSet != "ToPtrInt32" {
			t.Errorf("count access = %+v, want the SDK's int32 width", count.Access)
		}
	})

	t.Run("an inline enumeration gains its parse companion", func(t *testing.T) {
		kind := findField(t, rb.Fields, "kind")
		if kind.Access.SDKType != "*models.Tag_kind" {
			t.Errorf("kind SDKType = %q", kind.Access.SDKType)
		}
		if kind.Access.ConvertGet != "FromPtrEnum" || kind.Access.ConvertSet != "ToPtrEnum" {
			t.Errorf("kind converts = %q/%q", kind.Access.ConvertGet, kind.Access.ConvertSet)
		}
		if kind.Access.ParseFunc != "models.ParseTag_kind" {
			t.Errorf("kind ParseFunc = %q", kind.Access.ParseFunc)
		}
	})

	t.Run("a mangling miss is repaired off the SDK", func(t *testing.T) {
		vendor := findField(t, rb.Fields, "vendor")
		if vendor.Access.Get != "GetVendorEscaped" || vendor.Access.Set != "SetVendorEscaped" {
			t.Errorf("vendor accessors = %q/%q, want the Escaped spelling", vendor.Access.Get, vendor.Access.Set)
		}
		errField := findField(t, rb.Fields, "error")
		if errField.Access.Get != "GetErrorEscaped" {
			t.Errorf("error accessor = %q", errField.Access.Get)
		}
	})

	t.Run("the nested object settles both models", func(t *testing.T) {
		detail := findField(t, rb.Fields, "detail")
		if detail.NestedModel != "models.TagDetailable" {
			t.Errorf("NestedModel = %q", detail.NestedModel)
		}
		if detail.NestedWriteModel != "models.TagDetail" || detail.NestedConstructor != "models.NewTagDetail()" {
			t.Errorf("nested write = %q / %q", detail.NestedWriteModel, detail.NestedConstructor)
		}
		weight := findField(t, detail.Nested, "weight")
		if weight.Access.SDKType != "*float32" || weight.Access.ConvertGet != "FromPtrFloat32" {
			t.Errorf("nested weight access = %+v, want the SDK's float32 width", weight.Access)
		}
	})

	t.Run("a field the response never answers is kept from the plan", func(t *testing.T) {
		// Read as objects and written as identifiers: the write side stays.
		owners := findField(t, rb.Fields, "owners")
		if !owners.KeptFromPlan || owners.Access.Get != "" || owners.Access.Set != "SetOwners" {
			t.Errorf("owners = %+v, want kept from the plan with its setter alone", owners)
		}
		if owners.Access.SDKType != "[]string" || owners.Access.ConvertSet != "ToStringSlice" || owners.Access.ConvertGet != "" {
			t.Errorf("owners settles = %+v, want the setter's shape", owners.Access)
		}
		// Declared by the request model and absent from the read interface.
		owner := findField(t, rb.Fields, "owner_id")
		if !owner.KeptFromPlan || owner.Access.Get != "" || owner.Access.Set != "SetOwnerId" || owner.Access.ConvertSet != "ToPtrString" {
			t.Errorf("owner_id = %+v, want kept from the plan with a settled setter", owner)
		}
		for _, r := range removed {
			if r.Key == "tags" && (r.Attribute == "owners" || r.Attribute == "owner_id") {
				t.Errorf("a kept field was also recorded as removed: %v", r)
			}
		}
	})

	t.Run("what the SDK cannot carry goes with a reason", func(t *testing.T) {
		if fieldNamed(rb.Fields, "legacy") {
			t.Error("legacy survived against an SDK that does not carry it")
		}
		r := findRemoval(t, removed, "resource", "tags", "legacy")
		if !strings.Contains(r.Reason, "GetLegacy") {
			t.Errorf("legacy reason = %q, want it to name the missing accessor", r.Reason)
		}

		if fieldNamed(rb.Fields, "weird") {
			t.Error("weird survived with an unbridgeable carried type")
		}
		r = findRemoval(t, removed, "resource", "tags", "weird")
		if !strings.Contains(r.Reason, "*models.TagDetail") || !strings.Contains(r.Reason, "string attribute") {
			t.Errorf("weird reason = %q, want the SDK's shape beside the attribute kind", r.Reason)
		}
	})

	t.Run("the companion datasource resolves its element", func(t *testing.T) {
		db := b.Datasources["tags"]
		if db == nil {
			t.Fatal("datasource tags did not survive")
		}
		if db.ElementType != "models.Tagable" || db.CollectionAccess != "GetValue()" {
			t.Errorf("element = %q via %q", db.ElementType, db.CollectionAccess)
		}
		if db.ReadModel != "models.Tagable" {
			t.Errorf("ReadModel = %q", db.ReadModel)
		}
	})

	t.Run("the action settles its body", func(t *testing.T) {
		ab := b.Actions["tags_assign"]
		if ab == nil {
			t.Fatal("action tags_assign did not survive")
		}
		if ab.WriteModel != "models.Tag" || ab.WriteConstructor != "models.NewTag()" {
			t.Errorf("action write = %q / %q", ab.WriteModel, ab.WriteConstructor)
		}
		name := findField(t, ab.Fields, "name")
		if name.Access.Set != "SetName" || name.Access.Get != "" {
			t.Errorf("action field access = %+v, want write-only", name.Access)
		}
	})

	t.Run("removals are recorded on the binding set, sorted", func(t *testing.T) {
		if len(b.Removed) != len(removed) {
			t.Errorf("Removed carries %d entries, Prune returned %d", len(b.Removed), len(removed))
		}
		for i := 1; i < len(removed); i++ {
			a, z := removed[i-1], removed[i]
			if a.Kind > z.Kind || (a.Kind == z.Kind && a.Key > z.Key) ||
				(a.Kind == z.Kind && a.Key == z.Key && a.Attribute > z.Attribute) {
				t.Errorf("removals unsorted at %d: %v then %v", i, a, z)
			}
		}
	})
}

// TestPruneOAG resolves the openapi-generator drafts: the service field
// and body setter repair off the client, and payloads settle from
// Execute's signature.
func TestPruneOAG(t *testing.T) {
	b, removed := prunedOAG(t)

	rb := b.Resources["tags"]
	if rb == nil {
		t.Fatal("resource tags did not survive pruning")
	}

	t.Run("the drafted service field repairs to the client's", func(t *testing.T) {
		if want := "client.TagsAPI.GetTag(ctx, tagId).Execute()"; rb.Read.Expr != want {
			t.Errorf("read Expr = %q, want %q", rb.Read.Expr, want)
		}
	})

	t.Run("the drafted body setter repairs to the builder's", func(t *testing.T) {
		if want := "client.TagsAPI.CreateTag(ctx).Tag(*body).Execute()"; rb.Create.Expr != want {
			t.Errorf("create Expr = %q, want %q", rb.Create.Expr, want)
		}
		if rb.Create.RequestType != "sdk.Tag" {
			t.Errorf("create RequestType = %q", rb.Create.RequestType)
		}
		if rb.WriteModel != "sdk.Tag" || rb.WriteConstructor != "sdk.NewTagWithDefaults()" {
			t.Errorf("write = %q / %q", rb.WriteModel, rb.WriteConstructor)
		}
	})

	t.Run("payloads settle from Execute", func(t *testing.T) {
		if rb.Read.ResponseType != "*sdk.Tag" {
			t.Errorf("read ResponseType = %q", rb.Read.ResponseType)
		}
		want := []string{"*sdk.Tag", "*http.Response", "error"}
		for i, w := range want {
			if rb.Read.Results[i] != w {
				t.Errorf("read Results = %v, want %v", rb.Read.Results, want)
				break
			}
		}
		if rb.Delete.ResponseType != "" || len(rb.Delete.Results) != 2 {
			t.Errorf("delete = response %q results %v", rb.Delete.ResponseType, rb.Delete.Results)
		}
	})

	t.Run("value scalars and the string enum settle", func(t *testing.T) {
		count := findField(t, rb.Fields, "count")
		if count.Access.SDKType != "int32" || count.Access.ConvertGet != "FromInt32" {
			t.Errorf("count access = %+v", count.Access)
		}
		kind := findField(t, rb.Fields, "kind")
		if kind.Access.SDKType != "sdk.TagKind" || kind.Access.ParseFunc != "sdk.NewTagKindFromValue" {
			t.Errorf("kind access = %+v", kind.Access)
		}
		if kind.Access.ConvertGet != "FromEnum" || kind.Access.ConvertSet != "ToEnum" {
			t.Errorf("kind converts = %q/%q", kind.Access.ConvertGet, kind.Access.ConvertSet)
		}
	})

	t.Run("the nested object settles to the flat struct", func(t *testing.T) {
		detail := findField(t, rb.Fields, "detail")
		if detail.NestedModel != "sdk.TagDetail" || detail.NestedWriteModel != "sdk.TagDetail" {
			t.Errorf("nested = %q / %q", detail.NestedModel, detail.NestedWriteModel)
		}
		if detail.NestedConstructor != "sdk.NewTagDetailWithDefaults()" {
			t.Errorf("NestedConstructor = %q", detail.NestedConstructor)
		}
		// The oag fake's TagDetail carries no weight property.
		if fieldNamed(detail.Nested, "weight") {
			t.Error("detail.weight survived against a model that does not carry it")
		}
		findRemoval(t, removed, "resource", "tags", "detail.weight")
	})

	t.Run("the bare-slice list settles directly", func(t *testing.T) {
		db := b.Datasources["tags"]
		if db == nil {
			t.Fatal("datasource tags did not survive")
		}
		if db.ElementType != "sdk.Tag" || db.CollectionAccess != "" {
			t.Errorf("element = %q via %q, want the slice itself", db.ElementType, db.CollectionAccess)
		}
	})

	t.Run("the envelope list settles through its single slice field", func(t *testing.T) {
		lb := b.ListResources["groups"]
		if lb == nil {
			t.Fatal("list resource groups did not survive")
		}
		if lb.ElementType != "sdk.Group" || lb.CollectionAccess != "Items" {
			t.Errorf("element = %q via %q", lb.ElementType, lb.CollectionAccess)
		}
		if want := "client.GroupsAPI.ListGroups(ctx).Execute()"; lb.List.Expr != want {
			t.Errorf("list Expr = %q, want %q", lb.List.Expr, want)
		}
	})
}

// TestPruneErrors covers the loads that cannot even start.
func TestPruneErrors(t *testing.T) {
	b, err := kiotaBinder{}.Bind(kiotaModel(), kiotaInfo())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if _, err := Prune(b, t.TempDir()); err == nil {
		t.Error("Prune accepted a directory with no Go packages")
	}

	b.SDK.ClientTypeName = "NoSuchClient"
	if _, err := Prune(b, testdataDir(t, "kiotasdk")); err == nil {
		t.Error("Prune accepted a client type the SDK does not declare")
	}
}

func findField(t *testing.T, fbs []FieldBinding, attribute string) FieldBinding {
	t.Helper()
	for _, fb := range fbs {
		if fb.Attr == attribute {
			return fb
		}
	}
	t.Fatalf("no field binding for %q (have %v)", attribute, fieldAttrs(fbs))
	return FieldBinding{}
}

func fieldNamed(fbs []FieldBinding, attribute string) bool {
	for _, fb := range fbs {
		if fb.Attr == attribute {
			return true
		}
	}
	return false
}

func fieldAttrs(fbs []FieldBinding) []string {
	out := make([]string, 0, len(fbs))
	for _, fb := range fbs {
		out = append(out, fb.Attr)
	}
	return out
}

func findRemoval(t *testing.T, removed []Removal, kind, key, attribute string) Removal {
	t.Helper()
	for _, r := range removed {
		if r.Kind == kind && r.Key == key && r.Attribute == attribute {
			return r
		}
	}
	t.Fatalf("no removal for %s %s %q (have %v)", kind, key, attribute, removed)
	return Removal{}
}

// indexerReceiver builds a type carrying exactly the named methods, each
// taking one string and returning one value — the shape of a generated
// collection builder's by-identifier hop.
func indexerReceiver(t *testing.T, methods ...string) types.Type {
	t.Helper()
	goPackage := types.NewPackage("example.com/sdk/things", "things")
	named := types.NewNamed(
		types.NewTypeName(token.NoPos, goPackage, "ThingsRequestBuilder", nil),
		types.NewStruct(nil, nil), nil)

	result := types.NewNamed(
		types.NewTypeName(token.NoPos, goPackage, "ItemRequestBuilder", nil),
		types.NewStruct(nil, nil), nil)

	for _, name := range methods {
		sig := types.NewSignatureType(
			types.NewVar(token.NoPos, goPackage, "m", types.NewPointer(named)), nil, nil,
			types.NewTuple(types.NewVar(token.NoPos, goPackage, "id", types.Typ[types.String])),
			types.NewTuple(types.NewVar(token.NoPos, goPackage, "", types.NewPointer(result))),
			false)
		named.AddMethod(types.NewFunc(token.NoPos, goPackage, name, sig))
	}
	// Every generated builder also carries hops that are not indexers; they
	// must not be considered, whatever their arity.
	getSig := types.NewSignatureType(
		types.NewVar(token.NoPos, goPackage, "m", types.NewPointer(named)), nil, nil,
		types.NewTuple(types.NewVar(token.NoPos, goPackage, "id", types.Typ[types.String])),
		types.NewTuple(types.NewVar(token.NoPos, goPackage, "", types.NewPointer(result))),
		false)
	named.AddMethod(types.NewFunc(token.NoPos, goPackage, "Get", getSig))

	return types.NewPointer(named)
}

func TestUnit_Prune_RepairsAnIndexerTheGeneratorSpeltDifferently(t *testing.T) {
	// A generator does not spell the by-identifier hop the way the document
	// implies: it keeps the parameter's punctuation, appends a suffix, or
	// renames wholesale. Where the builder declares exactly one such method,
	// there is nothing to guess.
	for _, drafted := range []string{"ByGistId", "ByOwner", "ByTeamSlug"} {
		t.Run(drafted, func(t *testing.T) {
			seg := &Segment{Name: drafted, Call: true, Args: []string{"id"}}
			p := &pruner{}
			if _, ok := p.repairIndexer(indexerReceiver(t, "ByGist_id"), seg); !ok {
				t.Fatalf("%s must resolve to the builder's only indexer", drafted)
			}
			if seg.Name != "ByGist_id" {
				t.Fatalf("the segment must be respelled, got %q", seg.Name)
			}
		})
	}
}

func TestUnit_Prune_RefusesAnAmbiguousIndexer(t *testing.T) {
	// Two indexers is a guess, and a guess is what this must never make.
	seg := &Segment{Name: "ByThingId", Call: true, Args: []string{"id"}}
	p := &pruner{}
	if _, ok := p.repairIndexer(indexerReceiver(t, "ByOne", "ByTwo"), seg); ok {
		t.Fatal("two candidate indexers must refuse rather than pick one")
	}
	if seg.Name != "ByThingId" {
		t.Fatalf("a refused repair must leave the draft alone, got %q", seg.Name)
	}
}

func TestUnit_Prune_LeavesANonIndexerHopAlone(t *testing.T) {
	seg := &Segment{Name: "Teams", Call: true, Args: []string{"id"}}
	p := &pruner{}
	if _, ok := p.repairIndexer(indexerReceiver(t, "ByGist_id"), seg); ok {
		t.Fatal("only a by-identifier hop may be repaired this way")
	}
}

func TestUnit_CopyFieldBindings_IsDeepEnoughToResolveTwice(t *testing.T) {
	// The update body is resolved from a copy of the create's fields, so
	// resolving against a second model must not disturb the first.
	original := []FieldBinding{{
		Attr: "settings", Wire: "settings",
		Nested: []FieldBinding{{Attr: "name", Wire: "name"}},
	}}
	clone := copyFieldBindings(original)
	clone[0].Attr = "changed"
	clone[0].Nested[0].Attr = "changed_too"

	if original[0].Attr != "settings" {
		t.Fatalf("the original was disturbed: %q", original[0].Attr)
	}
	if original[0].Nested[0].Attr != "name" {
		t.Fatalf("the original's nested field was disturbed: %q", original[0].Nested[0].Attr)
	}
	if copyFieldBindings(nil) != nil {
		t.Fatal("copying nothing must answer nothing")
	}
}

func TestUnit_Bindings_RecordPackageIgnoresWhatEveryEmitterKnows(t *testing.T) {
	b := &Bindings{SDK: SDKInfo{ImportPath: "example.com/sdk", ModelsImportPath: "example.com/sdk/models"}}
	b.recordPackage("sdk", "example.com/sdk")
	b.recordPackage("models", "example.com/sdk/models")
	if len(b.OperationPackages) != 0 {
		t.Fatalf("the root and models packages need no recording, got %v", b.OperationPackages)
	}

	b.recordPackage("orgs", "example.com/sdk/orgs")
	if b.OperationPackages["orgs"] != "example.com/sdk/orgs" {
		t.Fatalf("an operation package must be recorded, got %v", b.OperationPackages)
	}
	b.recordPackage("", "example.com/sdk/x")
	b.recordPackage("x", "")
	if len(b.OperationPackages) != 1 {
		t.Fatalf("a half-named package must not be recorded, got %v", b.OperationPackages)
	}
}
