package sdkbind

import (
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
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
		Service: service, APIVersionDirectory: "v1",
	}
}

func op(kind ir.OperationKind, method, path, opID string, parameters ...ir.Parameter) *ir.Operation {
	return &ir.Operation{Kind: kind, Method: method, PathTemplate: path, OperationID: opID, PathParameters: parameters}
}

func attribute(name, wire string, kind ir.AttributeType, participation ir.ComputedOptionalRequired) ir.Attribute {
	return ir.Attribute{Name: name, WireName: wire, Kind: kind, ComputedOptionalRequired: participation}
}

// filterAttr is one companion datasource filter: toolkit vocabulary with no
// SDK side, so binding must leave it alone rather than look for a field.
func filterAttr(name string) ir.Attribute {
	return ir.Attribute{Name: name, WireName: name, Kind: ir.TypeString,
		ComputedOptionalRequired: ir.Optional, Filter: true}
}

// tagSchema is the shared attribute tree both fake SDKs carry variants
// of: an identifier, scalars at several widths, a keyword-mangled name,
// an inline enumeration, a scalar slice, and a nested object.
func tagSchema() *ir.AttributeTree {
	detail := &ir.AttributeTree{Attributes: []ir.Attribute{
		attribute("note", "note", ir.TypeString, ir.Optional),
		attribute("weight", "weight", ir.TypeFloat64, ir.Optional),
	}}
	nested := attribute("detail", "detail", ir.TypeObject, ir.Optional)
	nested.Nested = detail

	labels := attribute("labels", "labels", ir.TypeList, ir.Optional)
	labels.ElementType = ir.TypeString

	kindAttr := attribute("kind", "kind", ir.TypeString, ir.Optional)
	kindAttr.OneOf = []string{"SIMPLE"}

	kinds := attribute("kinds", "kinds", ir.TypeList, ir.Optional)
	kinds.ElementType = ir.TypeString

	unsupported := attribute("free", "free", "", ir.Optional)
	unsupported.Unsupported = true
	unsupported.UnsupportedReason = "free-form object"

	return &ir.AttributeTree{Attributes: []ir.Attribute{
		attribute("id", "id", ir.TypeString, ir.Computed),
		attribute("name", "name", ir.TypeString, ir.Required),
		attribute("error", "error", ir.TypeString, ir.Optional),
		attribute("vendor", "vendor", ir.TypeString, ir.Optional),
		attribute("count", "count", ir.TypeInt64, ir.Optional),
		attribute("enabled", "enabled", ir.TypeBool, ir.Optional),
		attribute("created_at", "createdAt", ir.TypeString, ir.Computed),
		kindAttr,
		kinds,
		attribute("slug", "slug", ir.TypeString, ir.Optional),
		attribute("alias", "alias", ir.TypeString, ir.Optional),
		labels,
		nested,
		attribute("weird", "weird", ir.TypeString, ir.Optional),
		attribute("legacy", "legacy", ir.TypeString, ir.Optional),
		unsupported,
	}}
}

// kiotaModel is the intermediate representation the kiota fake SDK
// implements — plus one entity ("widgets") the SDK does not carry at all.
func kiotaModel() *ir.Model {
	tagID := ir.Parameter{Name: "tagId", Type: ir.TypeString}

	itemTree := &ir.AttributeTree{Attributes: []ir.Attribute{
		attribute("id", "id", ir.TypeString, ir.Computed),
		attribute("name", "name", ir.TypeString, ir.Computed),
	}}
	items := attribute("items", "items", ir.TypeList, ir.Computed)
	items.ElementType = ir.TypeObject
	items.Nested = itemTree

	assignTree := &ir.AttributeTree{Attributes: []ir.Attribute{
		attribute("name", "name", ir.TypeString, ir.Required),
	}}

	return &ir.Model{
		Provider: ir.Provider{Name: "example"},
		Resources: []ir.Resource{
			{
				Names: names("tags", "tags"),
				Operations: ir.Operations{
					Create: op(ir.OperationCreate, "POST", "/tags", ""),
					Read:   op(ir.OperationRead, "GET", "/tags/{tagId}", "", tagID),
					Update: op(ir.OperationUpdate, "PATCH", "/tags/{tagId}", "", tagID),
					Delete: op(ir.OperationDelete, "DELETE", "/tags/{tagId}", "", tagID),
				},
				Schema: tagSchema(),
			},
			{
				Names: names("widgets", "widgets"),
				Operations: ir.Operations{
					Create: op(ir.OperationCreate, "POST", "/widgets", ""),
					Read:   op(ir.OperationRead, "GET", "/widgets/{widgetId}", "", ir.Parameter{Name: "widgetId", Type: ir.TypeString}),
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attribute("id", "id", ir.TypeString, ir.Computed),
					attribute("name", "name", ir.TypeString, ir.Required),
				}},
			},
		},
		Datasources: []ir.Datasource{
			{
				Names: names("tags", "tags"),
				Operations: ir.Operations{
					Read: op(ir.OperationRead, "GET", "/tags/{tagId}", "", tagID),
					List: op(ir.OperationList, "GET", "/tags", ""),
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					filterAttr("id"),
					filterAttr("name"),
					items,
				}},
			},
		},
		Actions: []ir.Action{
			{
				Names:           names("tags_assign", "tags"),
				InvokeOperation: *op(ir.OperationInvoke, "POST", "/tags/{tagId}/assign", "", tagID),
				RequestSchema:   assignTree,
				ParentEntity:    "tags",
			},
		},
	}
}

// oagModel is the intermediate representation the openapi-generator fake
// SDK implements. The tags service area is deliberately misspelled
// ("tag") so pruning has to repair the service field off the client.
func oagModel() *ir.Model {
	tagID := ir.Parameter{Name: "tagId", Type: ir.TypeString}

	itemTree := &ir.AttributeTree{Attributes: []ir.Attribute{
		attribute("id", "id", ir.TypeString, ir.Computed),
		attribute("name", "name", ir.TypeString, ir.Computed),
	}}
	items := attribute("items", "items", ir.TypeList, ir.Computed)
	items.ElementType = ir.TypeObject
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
				Operations: ir.Operations{
					Create: op(ir.OperationCreate, "POST", "/tags", "createTag"),
					Read:   op(ir.OperationRead, "GET", "/tags/{tagId}", "getTag", tagID),
					Update: op(ir.OperationUpdate, "PATCH", "/tags/{tagId}", "updateTag", tagID),
					Delete: op(ir.OperationDelete, "DELETE", "/tags/{tagId}", "deleteTag", tagID),
				},
				Schema: schema,
			},
		},
		Datasources: []ir.Datasource{
			{
				Names: names("tags", "tag"),
				Operations: ir.Operations{
					Read: op(ir.OperationRead, "GET", "/tags/{tagId}", "getTag", tagID),
					List: op(ir.OperationList, "GET", "/tags", "listTags"),
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					filterAttr("id"),
					filterAttr("name"),
					items,
				}},
			},
		},
		ListResources: []ir.ListResource{
			{
				Names:         names("groups", "groups"),
				ListOperation: *op(ir.OperationList, "GET", "/groups", "listGroups"),
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					attribute("name", "name", ir.TypeString, ir.Computed),
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
