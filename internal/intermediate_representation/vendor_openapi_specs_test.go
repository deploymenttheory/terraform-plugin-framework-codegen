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

	m, err := Derive(document, testConfig())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(m.Resources)+len(m.Datasources)+len(m.ListResources)+len(m.Actions) == 0 {
		t.Fatalf("a real document derived nothing")
	}

	// Nothing about a real document may come out anonymous or untyped at
	// the top level of its naming.
	for _, r := range m.Resources {
		if r.Names.Key == "" || r.Names.TerraformType == "" || r.Schema == nil {
			t.Errorf("anonymous resource: %+v", r.Names)
		}
		// A singleton is one object at a fixed path: read and updated,
		// never created or destroyed. Every other resource carries the
		// full lifecycle.
		if r.Singleton {
			if r.Operations.Read == nil || r.Operations.Update == nil {
				t.Errorf("singleton %s lacks its read or update", r.Names.Key)
			}
			if r.Operations.Create != nil || r.Operations.Delete != nil {
				t.Errorf("singleton %s must have neither create nor delete: %+v", r.Names.Key, r.Operations)
			}
			continue
		}
		if r.Operations.Create == nil || r.Operations.Read == nil || r.Operations.Delete == nil {
			t.Errorf("resource %s lacks lifecycle ops", r.Names.Key)
		}
	}
	for _, e := range m.Excluded {
		if e.Key == "" || e.Reason == "" {
			t.Errorf("anonymous exclusion: %+v", e)
		}
		if strings.Contains(e.Reason, "collide") {
			t.Errorf("a key collision still excludes: %+v", e)
		}
	}

	// The document's colliding path families both generate now: distinct
	// keys, and a co-management note on each side of the overlap.
	actionByKey := map[string]Action{}
	for _, a := range m.Actions {
		actionByKey[a.Names.Key] = a
	}
	for _, family := range [][2]string{
		{"tags_assign", "tags_assign_by_id"},
		{"tags_unassign", "tags_unassign_by_id"},
		{"endpoint_test_results_scheduled_tests_http_server_filter",
			"endpoint_test_results_scheduled_tests_http_server_filter_by_test_id"},
		{"endpoint_test_results_scheduled_tests_network_filter",
			"endpoint_test_results_scheduled_tests_network_filter_by_test_id"},
	} {
		for i, key := range family {
			a, ok := actionByKey[key]
			if !ok {
				t.Errorf("colliding entity %s is not in the model", key)
				continue
			}
			sibling := "acme_" + family[1-i]
			if !strings.Contains(a.CoManagementNote, sibling) {
				t.Errorf("%s co-management note does not name %s: %q", key, sibling, a.CoManagementNote)
			}
		}
	}

	// Purity at scale: a second derivation of the same loaded document is
	// equal in value and in bytes.
	again, err := Derive(document, testConfig())
	if err != nil {
		t.Fatalf("second Derive: %v", err)
	}
	if !reflect.DeepEqual(m, again) {
		t.Fatalf("two derivations of the pinned document differ")
	}
	mj, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	aj, err := json.Marshal(again)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(mj, aj) {
		t.Fatalf("two derivations of the pinned document marshal differently")
	}
}
