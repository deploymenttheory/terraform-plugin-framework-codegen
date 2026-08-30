package intermediate_representation

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/vendor_openapi_specs"
)

// The inline fixtures prove each rule in isolation; this proves derivation
// against a real vendor document, where the shapes were not chosen to pass.
func TestIntegration_IntermediateRepresentation_DerivesAPinnedVendorDocument(t *testing.T) {
	document, err := specmodel.Load(vendor_openapi_specs.ThousandEyes())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	model, err := Derive(document, testConfig())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(model.Resources)+len(model.Datasources)+len(model.ListResources)+len(model.Actions) == 0 {
		t.Fatalf("a real document derived nothing")
	}

	// Nothing about a real document may come out anonymous or untyped at
	// the top level of its naming.
	for _, resource := range model.Resources {
		if resource.Names.Key == "" || resource.Names.TerraformType == "" || resource.Attributes == nil {
			t.Errorf("anonymous resource: %+v", resource.Names)
		}
		// A singleton is one object at a fixed path: read and updated,
		// never created or destroyed. Every other resource carries the
		// full lifecycle.
		if resource.Singleton {
			if resource.Operations.Read == nil || resource.Operations.Update == nil {
				t.Errorf("singleton %s lacks its read or update", resource.Names.Key)
			}
			if resource.Operations.Create != nil || resource.Operations.Delete != nil {
				t.Errorf("singleton %s must have neither create nor delete: %+v", resource.Names.Key, resource.Operations)
			}
			continue
		}
		if resource.Operations.Create == nil || resource.Operations.Read == nil || resource.Operations.Delete == nil {
			t.Errorf("resource %s lacks lifecycle ops", resource.Names.Key)
		}
	}
	for _, excludedEntity := range append(append([]UnsupportedEntity{}, model.ExcludedByConfiguration...), model.ExcludedByClassification...) {
		if excludedEntity.Key == "" || excludedEntity.Reason == "" {
			t.Errorf("anonymous exclusion: %+v", excludedEntity)
		}
		if strings.Contains(excludedEntity.Reason, "collide") {
			t.Errorf("a key collision still excludes: %+v", excludedEntity)
		}
	}

	// The document's colliding path families both generate now: distinct
	// keys, and a co-management note on each side of the overlap.
	actionByKey := map[string]Action{}
	for _, candidate := range model.Actions {
		actionByKey[candidate.Names.Key] = candidate
	}
	for _, family := range [][2]string{
		{"tags_assign", "tags_assign_by_id"},
		{"tags_unassign", "tags_unassign_by_id"},
		{"endpoint_test_results_scheduled_tests_http_server_filter",
			"endpoint_test_results_scheduled_tests_http_server_filter_by_test_id"},
		{"endpoint_test_results_scheduled_tests_network_filter",
			"endpoint_test_results_scheduled_tests_network_filter_by_test_id"},
	} {
		for index, key := range family {
			candidate, ok := actionByKey[key]
			if !ok {
				t.Errorf("colliding entity %s is not in the model", key)
				continue
			}
			sibling := "acme_" + family[1-index]
			if !strings.Contains(candidate.CoManagementNote, sibling) {
				t.Errorf("%s co-management note does not name %s: %q", key, sibling, candidate.CoManagementNote)
			}
		}
	}

	// Purity at scale: a second derivation of the same loaded document is
	// equal in value and in bytes.
	again, err := Derive(document, testConfig())
	if err != nil {
		t.Fatalf("second Derive: %v", err)
	}
	if !reflect.DeepEqual(model, again) {
		t.Fatalf("two derivations of the pinned document differ")
	}
	mj, err := json.Marshal(model)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	firstJSON, err := json.Marshal(again)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(mj, firstJSON) {
		t.Fatalf("two derivations of the pinned document marshal differently")
	}
}
