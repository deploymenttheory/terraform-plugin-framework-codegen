package sdkbind

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
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
			Ops: ir.Ops{
				Read: op(ir.OpRead, "GET", "/tags/{tagId}", "", ir.Param{Name: "tagId", Type: ir.TypeString}),
			},
			Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
				attr("id", "id", ir.TypeString, ir.PresenceRequired),
				attr("name", "name", ir.TypeString, ir.PresenceComputed),
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
				Names:  names("tags", "tags"),
				ListOp: *op(ir.OpList, "GET", "/tags", ""),
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("name", "name", ir.TypeString, ir.PresenceComputed),
				}},
			},
			{
				Names:  names("widgets", "widgets"),
				ListOp: *op(ir.OpList, "GET", "/widgets", ""),
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("name", "name", ir.TypeString, ir.PresenceComputed),
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
			Ops: ir.Ops{
				Read: op(ir.OpRead, "GET", "/widgets/{widgetId}", "", ir.Param{Name: "widgetId", Type: ir.TypeString}),
			},
			Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
				attr("id", "id", ir.TypeString, ir.PresenceRequired),
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

// TestPruneActionRemovals covers an invocation whose call the SDK
// refuses: a bodyless draft against a verb that takes one.
func TestPruneActionRemovals(t *testing.T) {
	m := &ir.Model{
		Provider: ir.Provider{Name: "example"},
		Actions: []ir.Action{{
			Names:    names("tags_assign", "tags"),
			InvokeOp: *op(ir.OpInvoke, "POST", "/tags/{tagId}/assign", "", ir.Param{Name: "tagId", Type: ir.TypeString}),
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
			Ops: ir.Ops{
				Create: op(ir.OpCreate, "POST", "/orphans", ""),
				Read:   op(ir.OpRead, "GET", "/orphans/{orphanId}", "", ir.Param{Name: "orphanId", Type: ir.TypeString}),
			},
			Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
				attr("name", "name", ir.TypeString, ir.PresenceRequired),
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
				Ops: ir.Ops{
					Create: op(ir.OpCreate, "POST", "/tags", ""),
					Read:   op(ir.OpRead, "GET", "/tags/{tagId}", "", ir.Param{Name: "tagId", Type: ir.TypeString}),
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("legacy", "legacy", ir.TypeString, ir.PresenceOptional),
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
				Ops: ir.Ops{
					Create: op(ir.OpCreate, "POST", "/tags", ""),
					Read:   op(ir.OpRead, "GET", "/tags/{tagId}", "", ir.Param{Name: "tagId", Type: ir.TypeString}),
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("id", "id", ir.TypeString, ir.PresenceComputed),
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
