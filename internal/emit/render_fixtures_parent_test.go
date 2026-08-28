package emit

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/fixtures"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// settingUnder is a singleton addressed by the fictional http_server's id,
// named after that parent because the document spells the parameter id.
func settingUnder(parentParam string) ir.Resource {
	return ir.Resource{
		Names:        names("http_server_setting", "HTTPServerSetting", "servers"),
		Singleton:    true,
		ParentEntity: "http_server",
		Operations: ir.Operations{
			Read: &ir.Operation{Kind: ir.OperationRead, Method: "GET", PathTemplate: "/v7/http-servers/{" + parentParam + "}/setting",
				PathParameters: []ir.Parameter{{Name: parentParam, Type: ir.TypeString}}, SuccessCode: 200},
		},
		Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
			{Name: "http_server_id", WireName: parentParam, Kind: ir.TypeString, ComputedOptionalRequired: ir.Required},
			{Name: "id", WireName: "id", Kind: ir.TypeString, ComputedOptionalRequired: ir.Computed},
			{Name: "scope", WireName: "scope", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required},
		}},
	}
}

func TestUnit_DependencyBlocks_CarryTheParentsMinimalConfiguration(t *testing.T) {
	m, b := fictionalModel(), fictionalBindings()
	pc := fictionalProviderCore()
	pc.AcceptedRequestBodies = map[string]observe.RequestBodies{"http_server": {
		Entity:  "http_server",
		Minimal: &observe.AcceptedRequestBody{Status: 201, Request: map[string]any{"name": "tfpfgen-run1-http_server-name"}, Response: map[string]any{"name": "tfpfgen-run1-http_server-name", "id": "s1"}},
	}}
	e := &serviceRenderer{pc: pc, bindings: b, resources: map[string]*ir.Resource{"http_server": &m.Resources[0]}}
	child := settingUnder("id")
	fixture := fixtures.Fixture{Entries: []fixtures.Entry{
		{Name: "http_server_id", Wire: "id", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required, Scalar: "tfpfgen-test-http-server-id"},
		{Name: "scope", Wire: "scope", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required, Scalar: "default"},
	}}

	blocks, with := e.dependencyBlocks(&child, fixture, nil, 0, true)
	if with.Entries[0].Expression != "petstore_http_server.http_server.id" {
		t.Fatalf("parent attribute = %+v, want the parent's id expression", with.Entries[0])
	}
	if !strings.HasPrefix(blocks, `resource "petstore_http_server" "http_server" {`) || !strings.Contains(blocks, "name") {
		t.Errorf("live block = %q, want the parent's minimal block", blocks)
	}
	if !strings.Contains(blocks, "${random_string.tfpfgen_run.result}") {
		t.Errorf("live block = %q, want the run suffix on the invented name", blocks)
	}
	if e.dependencyTypes == nil || e.dependencyTypes[0] != "petstore_http_server" {
		t.Errorf("dependencyTypes = %v, want the parent's type recorded", e.dependencyTypes)
	}

	unit, _ := e.dependencyBlocks(&child, fixture, nil, 0, false)
	if strings.Contains(unit, "random_string") || !strings.HasPrefix(unit, `resource "petstore_http_server" "http_server" {`) {
		t.Errorf("unit block = %q, want the derived minimal block without a run suffix", unit)
	}

	// No parent the provider emits: the fixture keeps its invented value.
	orphan := settingUnder("id")
	orphan.ParentEntity = "ghost"
	if blocks, with := e.dependencyBlocks(&orphan, fixture, nil, 0, true); blocks != "" || with.Entries[0].Expression != "" {
		t.Errorf("a parent the provider does not emit produced a block: %q", blocks)
	}
	// Past the depth bound: nothing.
	if blocks, _ := e.dependencyBlocks(&child, fixture, nil, dependencyDepth, true); blocks != "" {
		t.Errorf("a block past the depth bound: %q", blocks)
	}
}

func TestUnit_DependencyBlocks_CarryABorrowedObjectsBlock(t *testing.T) {
	m, b := fictionalModel(), fictionalBindings()
	pc := fictionalProviderCore()
	e := &serviceRenderer{pc: pc, bindings: b, resources: map[string]*ir.Resource{"http_server": &m.Resources[0]}}
	rule := ir.Resource{Names: names("alert_rule", "AlertRule", "alerts"), Operations: ir.Operations{
		Create: &ir.Operation{Kind: ir.OperationCreate, Method: "POST", PathTemplate: "/v7/alert-rules", SuccessCode: 201},
		Read:   &ir.Operation{Kind: ir.OperationRead, Method: "GET", PathTemplate: "/v7/alert-rules/{ruleId}", PathParameters: []ir.Parameter{{Name: "ruleId"}}},
	}}
	fixture := fixtures.Fixture{Entries: []fixtures.Entry{
		{Name: "name", Wire: "name", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required, Scalar: "r"},
		{Name: "server_ids", Wire: "serverIds", Kind: ir.TypeList, ElementType: ir.TypeString, ComputedOptionalRequired: ir.Optional, Scalar: "s1"},
		{Name: "targets", Wire: "targets", Kind: ir.TypeList, ElementType: ir.TypeObject, ComputedOptionalRequired: ir.Optional, Nested: []fixtures.Entry{
			{Name: "server_id", Wire: "serverId", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required, Scalar: "s1"},
		}},
	}}
	references := map[string]string{"serverIds": "/v7/http-servers", "serverId": "/v7/http-servers", "ghostId": "/v7/ghosts"}
	blocks, with := e.dependencyBlocks(&rule, fixture, references, 0, false)
	if !strings.HasPrefix(blocks, `resource "petstore_http_server" "http_server" {`) || strings.Count(blocks, "resource ") != 1 {
		t.Fatalf("blocks = %q, want the borrowed object's block once", blocks)
	}
	if with.Entries[1].Expression != "petstore_http_server.http_server.id" {
		t.Errorf("list of ids = %+v, want the block's id expression", with.Entries[1])
	}
	if with.Entries[2].Nested[0].Expression != "petstore_http_server.http_server.id" {
		t.Errorf("nested id = %+v, want the block's id expression", with.Entries[2].Nested[0])
	}
	if got := with.HCL(fixtures.ConfigMaximal); !strings.Contains(got, "server_ids = [petstore_http_server.http_server.id]") {
		t.Errorf("HCL = %q, want the list expression in brackets", got)
	}
	if e.resourceByCollection("/v7/ghosts") != nil {
		t.Error("a collection no resource is addressed to resolved")
	}
}

func TestUnit_ParentAttribute_NamesTheImmediateParentsAttribute(t *testing.T) {
	singleton := settingUnder("httpServerId")
	if got := parentAttribute(&singleton); got != "http_server_id" {
		t.Errorf("a singleton's parent attribute = %q, want http_server_id", got)
	}
	keyed := settingUnder("httpServerId")
	keyed.Singleton = false
	keyed.Operations.Read.PathParameters = []ir.Parameter{{Name: "httpServerId"}, {Name: "settingId"}}
	if got := parentAttribute(&keyed); got != "http_server_id" {
		t.Errorf("a keyed child's parent attribute = %q, want the parameter above the key", got)
	}
	keyed.Operations.Read.PathParameters = []ir.Parameter{{Name: "settingId"}}
	if got := parentAttribute(&keyed); got != "" {
		t.Errorf("a top-level resource has a parent attribute: %q", got)
	}
	unanswered := settingUnder("ownerId")
	unanswered.Schema.Attributes = unanswered.Schema.Attributes[1:]
	if got := parentAttribute(&unanswered); got != "" {
		t.Errorf("a parameter no attribute answers = %q, want none", got)
	}
	if got := parentAttribute(&ir.Resource{}); got != "" {
		t.Errorf("a resource with no read = %q, want none", got)
	}
}
