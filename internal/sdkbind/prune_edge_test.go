package sdkbind

import (
	"go/types"
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// pruneKiotaVariant binds one hand-built model and prunes it against the
// kiota fake, for edge shapes the main fixtures do not carry.
func pruneKiotaVariant(t *testing.T, m *ir.Model) (*Bindings, []Removal) {
	t.Helper()
	b, err := kiotaBinder{}.Bind(m, kiotaInfo())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	removed, err := Prune(b, testdataDir(t, "kiotasdk"))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	return b, removed
}

// TestPruneKiotaShapes covers the settled shapes the main walk asserts
// only in passing: time, bool, enum slices, and the named string that is
// not an enumeration.
func TestPruneKiotaShapes(t *testing.T) {
	b, removed := prunedKiota(t)
	rb := b.Resources["tags"]
	if rb == nil {
		t.Fatal("resource tags did not survive")
	}

	created := findField(t, rb.Fields, "created_at")
	if created.Access.SDKType != "*time.Time" || created.Access.ConvertGet != "FromPtrTime" {
		t.Errorf("created_at access = %+v", created.Access)
	}
	if created.Access.Set != "" {
		t.Error("computed created_at drafted a setter")
	}

	enabled := findField(t, rb.Fields, "enabled")
	if enabled.Access.SDKType != "*bool" || enabled.Access.ConvertGet != "FromPtrBool" {
		t.Errorf("enabled access = %+v", enabled.Access)
	}

	kinds := findField(t, rb.Fields, "kinds")
	if kinds.Access.SDKType != "[]models.Tag_kind" || kinds.Access.ConvertGet != "FromEnumSlice" ||
		kinds.Access.ParseFunc != "models.ParseTag_kind" {
		t.Errorf("kinds access = %+v", kinds.Access)
	}

	if fieldNamed(rb.Fields, "slug") {
		t.Error("slug survived: a named string without a companion is not bridgeable")
	}
	r := findRemoval(t, removed, "resource", "tags", "slug")
	if !strings.Contains(r.Reason, "models.Slug") {
		t.Errorf("slug reason = %q, want the SDK's named type", r.Reason)
	}

	if fieldNamed(rb.Fields, "alias") {
		t.Error("alias survived a per-direction shape disagreement")
	}
	r = findRemoval(t, removed, "resource", "tags", "alias")
	if !strings.Contains(r.Reason, "read as *string but written as []string") {
		t.Errorf("alias reason = %q, want both directions named", r.Reason)
	}
}

// TestLoadSDKRefusesBrokenTree reports a tree that does not type-check
// as a load error carrying the tool's own reason.
func TestLoadSDKRefusesBrokenTree(t *testing.T) {
	_, err := loadSDK(testdataDir(t, "brokensdk"))
	if err == nil {
		t.Fatal("loadSDK accepted a tree that does not type-check")
	}
	if !strings.Contains(err.Error(), "does not type-check") {
		t.Errorf("error = %v", err)
	}
}

// TestPruneLookupDatasource resolves a lookup-by-key datasource, whose
// fields bind against the read payload rather than a list element.
func TestPruneLookupDatasource(t *testing.T) {
	m := &ir.Model{
		Provider: ir.Provider{Name: "example"},
		Datasources: []ir.Datasource{{
			Names: names("tags", "tags"),
			Operations: ir.Operations{
				Read: op(ir.OperationRead, "GET", "/tags/{tagId}", "", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
			},
			Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
				attr("id", "id", ir.TypeString, ir.Required),
				attr("name", "name", ir.TypeString, ir.Computed),
			}},
			LookupByKey:  true,
			KeyParameter: "tagId",
		}},
	}
	b, removed := pruneKiotaVariant(t, m)
	db := b.Datasources["tags"]
	if db == nil {
		t.Fatalf("lookup datasource did not survive: %v", removed)
	}
	if db.ReadModel != "models.Tagable" || db.ElementType != "" {
		t.Errorf("lookup binding = read %q element %q", db.ReadModel, db.ElementType)
	}
	if name := findField(t, db.Fields, "name"); name.Access.Get != "GetName" {
		t.Errorf("lookup field access = %+v", name.Access)
	}
}

// TestPruneKiotaListResource resolves a list-only entity through the
// collection response's value accessor.
func TestPruneKiotaListResource(t *testing.T) {
	m := &ir.Model{
		Provider: ir.Provider{Name: "example"},
		ListResources: []ir.ListResource{
			{
				Names:         names("tags", "tags"),
				ListOperation: *op(ir.OperationList, "GET", "/tags", ""),
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("name", "name", ir.TypeString, ir.Computed),
				}},
			},
			{
				Names:         names("widgets", "widgets"),
				ListOperation: *op(ir.OperationList, "GET", "/widgets", ""),
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("name", "name", ir.TypeString, ir.Computed),
				}},
			},
		},
	}
	b, removed := pruneKiotaVariant(t, m)

	lb := b.ListResources["tags"]
	if lb == nil {
		t.Fatalf("list resource tags did not survive: %v", removed)
	}
	if lb.ElementType != "models.Tagable" || lb.CollectionAccess != "GetValue()" {
		t.Errorf("element = %q via %q", lb.ElementType, lb.CollectionAccess)
	}

	if _, ok := b.ListResources["widgets"]; ok {
		t.Error("widgets list resource survived against an SDK that does not carry it")
	}
	findRemoval(t, removed, "list_resource", "widgets", "")
}

// TestPruneDatasourceRemovals covers the datasource-level failures.
func TestPruneDatasourceRemovals(t *testing.T) {
	m := &ir.Model{
		Provider: ir.Provider{Name: "example"},
		Datasources: []ir.Datasource{{
			Names: names("widgets", "widgets"),
			Operations: ir.Operations{
				Read: op(ir.OperationRead, "GET", "/widgets/{widgetId}", "", ir.Parameter{Name: "widgetId", Type: ir.TypeString}),
			},
			Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
				attr("id", "id", ir.TypeString, ir.Required),
			}},
			LookupByKey:  true,
			KeyParameter: "widgetId",
		}},
	}
	b, removed := pruneKiotaVariant(t, m)
	if len(b.Datasources) != 0 {
		t.Error("widgets datasource survived")
	}
	r := findRemoval(t, removed, "datasource", "widgets", "")
	if !strings.Contains(r.Reason, "read call cannot be made") {
		t.Errorf("reason = %q", r.Reason)
	}
}

// TestPruneKiotaWrappedList resolves a wrapped-list collection whose kiota
// envelope is an interface with a slice getter named for the wire key —
// the ThousandEyes Tagsable shape (GET /gizmos → {"gizmos":[...]}) that
// used to be pruned for carrying "no single way to reach its elements".
// The observed envelope key names GetGizmos directly, so the entity now
// survives with its element reached through GetGizmos().
func TestPruneKiotaWrappedList(t *testing.T) {
	itemTree := &ir.AttributeTree{Attributes: []ir.Attribute{
		attr("id", "id", ir.TypeString, ir.Computed),
		attr("name", "name", ir.TypeString, ir.Computed),
	}}
	items := attr("items", "items", ir.TypeList, ir.Computed)
	items.ElementType = ir.TypeObject
	items.Nested = itemTree

	t.Run("the envelope key names the getter", func(t *testing.T) {
		m := &ir.Model{
			Provider: ir.Provider{Name: "example"},
			Datasources: []ir.Datasource{{
				Names:      names("gizmos", "gizmos"),
				Operations: ir.Operations{List: op(ir.OperationList, "GET", "/gizmos", "")},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("filter_type", "filter_type", ir.TypeString, ir.Required),
					attr("filter_value", "filter_value", ir.TypeString, ir.Optional),
					items,
				}},
				ListEnvelopeKey: "gizmos",
			}},
		}
		b, removed := pruneKiotaVariant(t, m)
		db := b.Datasources["gizmos"]
		if db == nil {
			t.Fatalf("wrapped-list datasource gizmos did not survive: %v", removed)
		}
		if db.CollectionAccess != "GetGizmos()" || db.ElementType != "models.Gizmoable" {
			t.Errorf("element = %q via %q, want models.Gizmoable via GetGizmos()", db.ElementType, db.CollectionAccess)
		}
		if !fieldNamed(db.Fields, "id") || !fieldNamed(db.Fields, "name") {
			t.Errorf("wrapped element lost its fields: %+v", db.Fields)
		}
	})

	t.Run("a lone getter needs no envelope key", func(t *testing.T) {
		// The same wrapper, reached as a list resource with no observed
		// key: exactly one slice getter is unambiguous on its own.
		m := &ir.Model{
			Provider: ir.Provider{Name: "example"},
			ListResources: []ir.ListResource{{
				Names:         names("gizmos", "gizmos"),
				ListOperation: *op(ir.OperationList, "GET", "/gizmos", ""),
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("name", "name", ir.TypeString, ir.Computed),
				}},
			}},
		}
		b, removed := pruneKiotaVariant(t, m)
		lb := b.ListResources["gizmos"]
		if lb == nil {
			t.Fatalf("gizmos list resource did not survive: %v", removed)
		}
		if lb.CollectionAccess != "GetGizmos()" || lb.ElementType != "models.Gizmoable" {
			t.Errorf("element = %q via %q, want models.Gizmoable via GetGizmos()", lb.ElementType, lb.CollectionAccess)
		}
	})
}

// TestPruneKiotaAmbiguousWrapper keeps pruning a wrapped list whose
// envelope offers several slice getters with no wire key to choose among
// them: guessing would be invention, so the entity is dropped with the
// SDK's shape in the reason.
func TestPruneKiotaAmbiguousWrapper(t *testing.T) {
	m := &ir.Model{
		Provider: ir.Provider{Name: "example"},
		ListResources: []ir.ListResource{{
			Names:         names("blobs", "blobs"),
			ListOperation: *op(ir.OperationList, "GET", "/blobs", ""),
			Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
				attr("name", "name", ir.TypeString, ir.Computed),
			}},
			// No envelope key, and the envelope carries two slice getters.
		}},
	}
	b, removed := pruneKiotaVariant(t, m)
	if _, ok := b.ListResources["blobs"]; ok {
		t.Error("blobs survived against an ambiguous envelope")
	}
	r := findRemoval(t, removed, "list_resource", "blobs", "")
	if !strings.Contains(r.Reason, "no single way to reach its elements") {
		t.Errorf("reason = %q, want the ambiguity reason", r.Reason)
	}
}

// TestPruneActionRemovals covers an invocation whose call the SDK
// refuses: a bodyless draft against a verb that takes one.
func TestPruneActionRemovals(t *testing.T) {
	m := &ir.Model{
		Provider: ir.Provider{Name: "example"},
		Actions: []ir.Action{{
			Names:           names("tags_assign", "tags"),
			InvokeOperation: *op(ir.OperationInvoke, "POST", "/tags/{tagId}/assign", "", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
			// No request schema: the drafted call passes (ctx, nil) where
			// the SDK's Post takes three arguments.
		}},
	}
	b, removed := pruneKiotaVariant(t, m)
	if len(b.Actions) != 0 {
		t.Error("the bodyless action survived an arity the SDK refuses")
	}
	r := findRemoval(t, removed, "action", "tags_assign", "")
	if !strings.Contains(r.Reason, "argument") {
		t.Errorf("reason = %q, want the arity mismatch", r.Reason)
	}
}

// TestPruneUnconstructibleBody removes an entity whose write interface
// has no constructor behind it — a body nothing can build.
func TestPruneUnconstructibleBody(t *testing.T) {
	m := &ir.Model{
		Provider: ir.Provider{Name: "example"},
		Resources: []ir.Resource{{
			Names: names("orphans", "orphans"),
			Operations: ir.Operations{
				Create: op(ir.OperationCreate, "POST", "/orphans", ""),
				Read:   op(ir.OperationRead, "GET", "/orphans/{orphanId}", "", ir.Parameter{Name: "orphanId", Type: ir.TypeString}),
			},
			Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
				attr("name", "name", ir.TypeString, ir.Required),
			}},
		}},
	}
	b, removed := pruneKiotaVariant(t, m)
	if len(b.Resources) != 0 {
		t.Error("orphans survived without a constructible body")
	}
	r := findRemoval(t, removed, "resource", "orphans", "")
	if !strings.Contains(r.Reason, "NewOrphan") {
		t.Errorf("reason = %q, want the missing constructor", r.Reason)
	}
}

// TestPruneUnbuildableEntities removes what survives with nothing left
// to generate in one direction.
func TestPruneUnbuildableEntities(t *testing.T) {
	t.Run("nothing readable", func(t *testing.T) {
		m := &ir.Model{
			Provider: ir.Provider{Name: "example"},
			Resources: []ir.Resource{{
				Names: names("tags", "tags"),
				Operations: ir.Operations{
					Create: op(ir.OperationCreate, "POST", "/tags", ""),
					Read:   op(ir.OperationRead, "GET", "/tags/{tagId}", "", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("legacy", "legacy", ir.TypeString, ir.Optional),
				}},
			}},
		}
		b, removed := pruneKiotaVariant(t, m)
		if len(b.Resources) != 0 {
			t.Error("a resource with no readable attribute survived")
		}
		r := findRemoval(t, removed, "resource", "tags", "")
		if !strings.Contains(r.Reason, "nothing to map into state") {
			t.Errorf("reason = %q", r.Reason)
		}
	})

	t.Run("nothing settable", func(t *testing.T) {
		m := &ir.Model{
			Provider: ir.Provider{Name: "example"},
			Resources: []ir.Resource{{
				Names: names("tags", "tags"),
				Operations: ir.Operations{
					Create: op(ir.OperationCreate, "POST", "/tags", ""),
					Read:   op(ir.OperationRead, "GET", "/tags/{tagId}", "", ir.Parameter{Name: "tagId", Type: ir.TypeString}),
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("id", "id", ir.TypeString, ir.Computed),
				}},
			}},
		}
		b, removed := pruneKiotaVariant(t, m)
		if len(b.Resources) != 0 {
			t.Error("a resource with nothing settable survived its create")
		}
		r := findRemoval(t, removed, "resource", "tags", "")
		if !strings.Contains(r.Reason, "nothing an operator could configure") {
			t.Errorf("reason = %q", r.Reason)
		}
	})
}

// structNamed builds a named struct type in a package, which is the shape
// kiota's DateOnly and the standard library's time.Time both have.
func structNamed(path, pkgName, typeName string) *types.Named {
	pkg := types.NewPackage(path, pkgName)
	obj := types.NewTypeName(0, pkg, typeName, nil)
	return types.NewNamed(obj, types.NewStruct(nil, nil), nil)
}

// TestUnit_SettleScalar_BridgesDateOnlyTimeSliceAndBytes proves the three
// SDK shapes the catalog can carry but the pruner used to refuse settle to
// their catalog shorthands.
func TestUnit_SettleScalar_BridgesDateOnlyTimeSliceAndBytes(t *testing.T) {
	dateOnly := structNamed("github.com/microsoft/kiota-abstractions-go/serialization", "serialization", "DateOnly")
	instant := structNamed("time", "time", "Time")

	for _, testCase := range []struct {
		name     string
		kind     ir.AttributeType
		element  ir.AttributeType
		sdkType  types.Type
		wantGet  string
		wantSet  string
		wantType string
	}{
		{"kiota date-only", ir.TypeString, "", types.NewPointer(dateOnly), "FromPtrDateOnly", "ToPtrDateOnly", "*serialization.DateOnly"},
		{"slice of instants", ir.TypeList, ir.TypeString, types.NewSlice(instant), "FromTimeSlice", "ToTimeSlice", "[]time.Time"},
		{"byte slice", ir.TypeString, "", types.NewSlice(types.Typ[types.Byte]), "FromBytesBase64", "ToBytesBase64", "[]byte"},
	} {
		fb := FieldBinding{
			Attr: "x", Wire: "x", Kind: testCase.kind, ElementType: testCase.element,
			Access: FieldAccess{Get: "GetX", Set: "SetX"},
		}
		p := &pruner{}
		if why := p.settleScalar(&fb, testCase.sdkType); why != "" {
			t.Errorf("%s: settleScalar refused it: %s", testCase.name, why)
			continue
		}
		if fb.Access.ConvertGet != testCase.wantGet || fb.Access.ConvertSet != testCase.wantSet {
			t.Errorf("%s: conversions = %q/%q, want %q/%q",
				testCase.name, fb.Access.ConvertGet, fb.Access.ConvertSet, testCase.wantGet, testCase.wantSet)
		}
		if fb.Access.SDKType != testCase.wantType {
			t.Errorf("%s: SDKType = %q, want %q", testCase.name, fb.Access.SDKType, testCase.wantType)
		}
	}
}

// TestUnit_IsKiotaDateOnly_MatchesOnThePackagePath proves another SDK's
// DateOnly is not bridged through kiota's parser, which would not compile.
func TestUnit_IsKiotaDateOnly_MatchesOnThePackagePath(t *testing.T) {
	if !isKiotaDateOnly(structNamed("github.com/microsoft/kiota-abstractions-go/serialization", "serialization", "DateOnly")) {
		t.Error("kiota's DateOnly must be recognised")
	}
	if isKiotaDateOnly(structNamed("example.com/somesdk/serialization", "serialization", "DateOnly")) {
		t.Error("another SDK's DateOnly must not be bridged through kiota's parser")
	}
	if isKiotaDateOnly(structNamed("github.com/microsoft/kiota-abstractions-go/serialization", "serialization", "TimeOnly")) {
		t.Error("only DateOnly is bridged")
	}
}

// TestUnit_SettleScalar_BridgesUUID proves a uuid the SDK types as
// uuid.UUID settles to the catalog's string bridge, scalar and slice alike.
func TestUnit_SettleScalar_BridgesUUID(t *testing.T) {
	uuidType := structNamed("github.com/google/uuid", "uuid", "UUID")

	scalar := FieldBinding{Attr: "x", Wire: "x", Kind: ir.TypeString, Access: FieldAccess{Get: "GetX", Set: "SetX"}}
	if why := (&pruner{}).settleScalar(&scalar, types.NewPointer(uuidType)); why != "" {
		t.Fatalf("a uuid scalar was refused: %s", why)
	}
	if scalar.Access.ConvertGet != "FromPtrUUID" || scalar.Access.ConvertSet != "ToPtrUUID" {
		t.Errorf("scalar conversions = %q/%q, want FromPtrUUID/ToPtrUUID",
			scalar.Access.ConvertGet, scalar.Access.ConvertSet)
	}

	list := FieldBinding{Attr: "x", Wire: "x", Kind: ir.TypeList, ElementType: ir.TypeString,
		Access: FieldAccess{Get: "GetX", Set: "SetX"}}
	if why := (&pruner{}).settleScalar(&list, types.NewSlice(uuidType)); why != "" {
		t.Fatalf("a uuid slice was refused: %s", why)
	}
	if list.Access.ConvertGet != "FromUUIDSlice" || list.Access.ConvertSet != "ToUUIDSlice" {
		t.Errorf("slice conversions = %q/%q, want FromUUIDSlice/ToUUIDSlice",
			list.Access.ConvertGet, list.Access.ConvertSet)
	}
}

// TestUnit_IsGoogleUUID_MatchesOnThePackagePath proves another package's
// UUID type is not bridged through github.com/google/uuid's parser.
func TestUnit_IsGoogleUUID_MatchesOnThePackagePath(t *testing.T) {
	if !isGoogleUUID(structNamed("github.com/google/uuid", "uuid", "UUID")) {
		t.Error("google/uuid's UUID must be recognised")
	}
	if isGoogleUUID(structNamed("example.com/other/uuid", "uuid", "UUID")) {
		t.Error("another package's UUID must not be bridged through google/uuid")
	}
}

// TestUnit_SettleScalar_MapThroughAdditionalData proves a map attribute
// whose SDK type is a model with an additionalData bag settles to the bag
// bridge rather than being refused. kiota emits no typed accessor for
// additionalProperties, so the bag is the only route to the values.
func TestUnit_SettleScalar_MapThroughAdditionalData(t *testing.T) {
	pkg := types.NewPackage("example.com/sdk/models", "models")
	obj := types.NewTypeName(0, pkg, "Headers", nil)
	named := types.NewNamed(obj, types.NewStruct(nil, nil), nil)

	// The bag accessor is what the pruner recognises.
	sig := types.NewSignatureType(types.NewVar(0, nil, "m", named), nil, nil, nil,
		types.NewTuple(types.NewVar(0, nil, "", types.NewMap(types.Typ[types.String], types.NewInterfaceType(nil, nil)))), false)
	named.AddMethod(types.NewFunc(0, pkg, "GetAdditionalData", sig))

	fb := FieldBinding{Attr: "headers", Wire: "headers", Kind: ir.TypeMap, ElementType: ir.TypeString,
		Access: FieldAccess{Get: "GetHeaders"}}
	if why := (&pruner{}).settleScalar(&fb, named); why != "" {
		t.Fatalf("a map carried through additionalData was refused: %s", why)
	}
	if fb.Access.ConvertGet != "FromStringMapAdditionalData" {
		t.Errorf("ConvertGet = %q, want FromStringMapAdditionalData", fb.Access.ConvertGet)
	}
	if fb.NestedModel != "models.Headers" {
		t.Errorf("NestedModel = %q, want models.Headers", fb.NestedModel)
	}
}

// TestUnit_SettleScalar_MapWithoutABagIsRefused proves a map attribute whose
// SDK type carries neither a Go map nor an additionalData bag still names
// itself in the reason rather than settling to something that will not
// compile.
func TestUnit_SettleScalar_MapWithoutABagIsRefused(t *testing.T) {
	named := structNamed("example.com/sdk/models", "models", "Opaque")
	fb := FieldBinding{Attr: "headers", Wire: "headers", Kind: ir.TypeMap, ElementType: ir.TypeString,
		Access: FieldAccess{Get: "GetHeaders"}}

	why := (&pruner{}).settleScalar(&fb, named)
	if why == "" {
		t.Fatal("a model with no bag and no map settled anyway")
	}
	if !strings.Contains(why, "cannot be bridged to a map attribute") {
		t.Errorf("reason = %q", why)
	}
}
