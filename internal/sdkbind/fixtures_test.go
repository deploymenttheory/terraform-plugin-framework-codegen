package sdkbind

import (
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
)

// minimalConfig builds just enough config to select a backend.
func minimalConfig(backend string) *config.Config {
	return &config.Config{SDK: config.SDK{Backend: backend, ClientTypeName: "APIClient"}}
}

// testdataDir resolves one fake SDK tree.
func testdataDir(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("resolving testdata dir: %v", err)
	}
	return abs
}

func kiotaInfo() SDKInfo {
	return SDKInfo{
		ImportPath:       "example.com/kiotasdk",
		ModelsImportPath: "example.com/kiotasdk/models",
		ClientTypeName:   "APIClient",
	}
}

func oagInfo() SDKInfo {
	return SDKInfo{
		ImportPath:       "example.com/oagsdk",
		ModelsImportPath: "example.com/oagsdk",
		ClientTypeName:   "APIClient",
	}
}

// names builds the naming block by hand, the way derivation would.
func names(key, service string) ir.Names {
	return ir.Names{
		Key: key, Pascal: exportedName(key), Camel: key,
		TerraformType: "example_" + key, Package: key,
		Service: service, APIVersionDir: "v1",
	}
}

func op(kind ir.OpKind, method, path, opID string, params ...ir.Param) *ir.Op {
	return &ir.Op{Kind: kind, Method: method, PathTemplate: path, OperationID: opID, PathParams: params}
}

func attr(name, wire string, kind ir.TypeKind, presence ir.Presence) ir.Attribute {
	return ir.Attribute{Name: name, WireName: wire, Kind: kind, Presence: presence}
}

// tagSchema is the shared attribute tree both fake SDKs carry variants
// of: an identifier, scalars at several widths, a keyword-mangled name,
// an inline enumeration, a scalar slice, and a nested object.
func tagSchema() *ir.AttributeTree {
	detail := &ir.AttributeTree{Attributes: []ir.Attribute{
		attr("note", "note", ir.TypeString, ir.PresenceOptional),
		attr("weight", "weight", ir.TypeFloat64, ir.PresenceOptional),
	}}
	nested := attr("detail", "detail", ir.TypeObject, ir.PresenceOptional)
	nested.Nested = detail

	labels := attr("labels", "labels", ir.TypeList, ir.PresenceOptional)
	labels.ElemKind = ir.TypeString

	kindAttr := attr("kind", "kind", ir.TypeString, ir.PresenceOptional)
	kindAttr.OneOf = []string{"SIMPLE"}

	kinds := attr("kinds", "kinds", ir.TypeList, ir.PresenceOptional)
	kinds.ElemKind = ir.TypeString

	unsupported := attr("free", "free", "", ir.PresenceOptional)
	unsupported.Unsupported = true
	unsupported.UnsupportedReason = "free-form object"

	return &ir.AttributeTree{Attributes: []ir.Attribute{
		attr("id", "id", ir.TypeString, ir.PresenceComputed),
		attr("name", "name", ir.TypeString, ir.PresenceRequired),
		attr("error", "error", ir.TypeString, ir.PresenceOptional),
		attr("vendor", "vendor", ir.TypeString, ir.PresenceOptional),
		attr("count", "count", ir.TypeInt64, ir.PresenceOptional),
		attr("enabled", "enabled", ir.TypeBool, ir.PresenceOptional),
		attr("created_at", "createdAt", ir.TypeString, ir.PresenceComputed),
		kindAttr,
		kinds,
		attr("slug", "slug", ir.TypeString, ir.PresenceOptional),
		attr("alias", "alias", ir.TypeString, ir.PresenceOptional),
		labels,
		nested,
		attr("weird", "weird", ir.TypeString, ir.PresenceOptional),
		attr("legacy", "legacy", ir.TypeString, ir.PresenceOptional),
		unsupported,
	}}
}

// kiotaModel is the intermediate representation the kiota fake SDK
// implements — plus one entity ("widgets") the SDK does not carry at all.
func kiotaModel() *ir.Model {
	tagID := ir.Param{Name: "tagId", Type: ir.TypeString}

	itemTree := &ir.AttributeTree{Attributes: []ir.Attribute{
		attr("id", "id", ir.TypeString, ir.PresenceComputed),
		attr("name", "name", ir.TypeString, ir.PresenceComputed),
	}}
	items := attr("items", "items", ir.TypeList, ir.PresenceComputed)
	items.ElemKind = ir.TypeObject
	items.Nested = itemTree

	assignTree := &ir.AttributeTree{Attributes: []ir.Attribute{
		attr("name", "name", ir.TypeString, ir.PresenceRequired),
	}}

	return &ir.Model{
		Provider: ir.Provider{Name: "example"},
		Resources: []ir.Resource{
			{
				Names: names("tags", "tags"),
				Ops: ir.Ops{
					Create: op(ir.OpCreate, "POST", "/tags", ""),
					Read:   op(ir.OpRead, "GET", "/tags/{tagId}", "", tagID),
					Update: op(ir.OpUpdate, "PATCH", "/tags/{tagId}", "", tagID),
					Delete: op(ir.OpDelete, "DELETE", "/tags/{tagId}", "", tagID),
				},
				Schema: tagSchema(),
			},
			{
				Names: names("widgets", "widgets"),
				Ops: ir.Ops{
					Create: op(ir.OpCreate, "POST", "/widgets", ""),
					Read:   op(ir.OpRead, "GET", "/widgets/{widgetId}", "", ir.Param{Name: "widgetId", Type: ir.TypeString}),
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("id", "id", ir.TypeString, ir.PresenceComputed),
					attr("name", "name", ir.TypeString, ir.PresenceRequired),
				}},
			},
		},
		Datasources: []ir.Datasource{
			{
				Names: names("tags", "tags"),
				Ops: ir.Ops{
					Read: op(ir.OpRead, "GET", "/tags/{tagId}", "", tagID),
					List: op(ir.OpList, "GET", "/tags", ""),
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("filter_type", "filter_type", ir.TypeString, ir.PresenceRequired),
					attr("filter_value", "filter_value", ir.TypeString, ir.PresenceOptional),
					items,
				}},
			},
		},
		Actions: []ir.Action{
			{
				Names:         names("tags_assign", "tags"),
				InvokeOp:      *op(ir.OpInvoke, "POST", "/tags/{tagId}/assign", "", tagID),
				RequestSchema: assignTree,
				ParentEntity:  "tags",
			},
		},
	}
}

// oagModel is the intermediate representation the openapi-generator fake
// SDK implements. The tags service area is deliberately misspelled
// ("tag") so pruning has to repair the service field off the client.
func oagModel() *ir.Model {
	tagID := ir.Param{Name: "tagId", Type: ir.TypeString}

	itemTree := &ir.AttributeTree{Attributes: []ir.Attribute{
		attr("id", "id", ir.TypeString, ir.PresenceComputed),
		attr("name", "name", ir.TypeString, ir.PresenceComputed),
	}}
	items := attr("items", "items", ir.TypeList, ir.PresenceComputed)
	items.ElemKind = ir.TypeObject
	items.Nested = itemTree

	schema := tagSchema()
	// The oag fake carries no keyword-mangled, mispromised or
	// kiota-shaped properties. Keep the shared tree's core.
	kept := schema.Attributes[:0]
	for _, a := range schema.Attributes {
		switch a.Name {
		case "error", "vendor", "weird", "legacy", "enabled", "created_at", "kinds", "slug", "alias":
			continue
		}
		kept = append(kept, a)
	}
	schema.Attributes = kept

	return &ir.Model{
		Provider: ir.Provider{Name: "example"},
		Resources: []ir.Resource{
			{
				Names: names("tags", "tag"),
				Ops: ir.Ops{
					Create: op(ir.OpCreate, "POST", "/tags", "createTag"),
					Read:   op(ir.OpRead, "GET", "/tags/{tagId}", "getTag", tagID),
					Update: op(ir.OpUpdate, "PATCH", "/tags/{tagId}", "updateTag", tagID),
					Delete: op(ir.OpDelete, "DELETE", "/tags/{tagId}", "deleteTag", tagID),
				},
				Schema: schema,
			},
		},
		Datasources: []ir.Datasource{
			{
				Names: names("tags", "tag"),
				Ops: ir.Ops{
					Read: op(ir.OpRead, "GET", "/tags/{tagId}", "getTag", tagID),
					List: op(ir.OpList, "GET", "/tags", "listTags"),
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("filter_type", "filter_type", ir.TypeString, ir.PresenceRequired),
					attr("filter_value", "filter_value", ir.TypeString, ir.PresenceOptional),
					items,
				}},
			},
		},
		ListResources: []ir.ListResource{
			{
				Names:  names("groups", "groups"),
				ListOp: *op(ir.OpList, "GET", "/groups", "listGroups"),
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attr("name", "name", ir.TypeString, ir.PresenceComputed),
				}},
			},
		},
	}
}

// prunedKiota binds and prunes the kiota fixtures against the fake SDK.
func prunedKiota(t *testing.T) (*Bindings, []Removal) {
	t.Helper()
	b, err := kiotaBinder{}.Bind(kiotaModel(), kiotaInfo())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	removed, err := Prune(b, testdataDir(t, "kiotasdk"))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	return b, removed
}

// prunedOAG binds and prunes the openapi-generator fixtures.
func prunedOAG(t *testing.T) (*Bindings, []Removal) {
	t.Helper()
	b, err := openAPIGeneratorBinder{}.Bind(oagModel(), oagInfo())
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	removed, err := Prune(b, testdataDir(t, "oagsdk"))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	return b, removed
}
