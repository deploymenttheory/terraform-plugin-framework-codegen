package emit

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
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

func TestUnit_ParentBlocks_CarryTheParentsMinimalConfiguration(t *testing.T) {
	m, b := fictionalModel(), fictionalBindings()
	pc := fictionalProviderCore()
	pc.AcceptedRequestBodies = map[string]observe.RequestBodies{"http_server": {
		Entity:  "http_server",
		Minimal: &observe.AcceptedRequestBody{Status: 201, Request: map[string]any{"name": "tfpfgen-run1-http_server-name"}, Response: map[string]any{"name": "tfpfgen-run1-http_server-name", "id": "s1"}},
	}}
	e := &serviceRenderer{pc: pc, bindings: b, resources: map[string]*ir.Resource{"http_server": &m.Resources[0]}}
	child := settingUnder("id")

	blocks, attribute, reference := e.parentBlocks(&child, 0, true)
	if attribute != "http_server_id" || reference != "petstore_http_server.http_server.id" {
		t.Fatalf("parentBlocks = %q, %q; want the parent attribute and its id expression", attribute, reference)
	}
	if !strings.HasPrefix(blocks, `resource "petstore_http_server" "http_server" {`) || !strings.Contains(blocks, "name") {
		t.Errorf("live block = %q, want the parent's minimal block", blocks)
	}
	if !strings.Contains(blocks, "${random_string.tfpfgen_run.result}") {
		t.Errorf("live block = %q, want the run suffix on the invented name", blocks)
	}
	if e.parentTypes == nil || e.parentTypes[0] != "petstore_http_server" {
		t.Errorf("parentTypes = %v, want the parent's type recorded", e.parentTypes)
	}

	unit, _, _ := e.parentBlocks(&child, 0, false)
	if strings.Contains(unit, "random_string") || !strings.HasPrefix(unit, `resource "petstore_http_server" "http_server" {`) {
		t.Errorf("unit block = %q, want the derived minimal block without a run suffix", unit)
	}

	// No parent the provider emits: the fixture keeps its invented value.
	orphan := settingUnder("id")
	orphan.ParentEntity = "ghost"
	if blocks, _, _ := e.parentBlocks(&orphan, 0, true); blocks != "" {
		t.Errorf("a parent the provider does not emit produced a block: %q", blocks)
	}
	// Past the depth bound: nothing.
	if blocks, _, _ := e.parentBlocks(&child, parentBlockDepth, true); blocks != "" {
		t.Errorf("a block past the depth bound: %q", blocks)
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
