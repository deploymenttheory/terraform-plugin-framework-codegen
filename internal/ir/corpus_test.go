package ir

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/corpus"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// The inline fixtures prove each rule in isolation; this proves derivation
// against a real vendor document, where the shapes were not chosen to pass.
// Skips when the pinned document is not cached and cannot be fetched,
// unless TFPFGEN_CORPUS_REQUIRED says the machine must be honest — the
// same split every corpus-backed test in this repo follows.
func TestIntegration_IR_DerivesAPinnedVendorDocument(t *testing.T) {
	path := corpus.SpecPath(t, "thousandeyes")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	doc, err := specmodel.Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	m, err := Derive(doc, testConfig())
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
		if r.Ops.Create == nil || r.Ops.Read == nil || r.Ops.Delete == nil {
			t.Errorf("resource %s lacks lifecycle ops", r.Names.Key)
		}
	}
	for _, e := range m.Excluded {
		if e.Key == "" || e.Reason == "" {
			t.Errorf("anonymous exclusion: %+v", e)
		}
	}

	// Purity at scale: a second derivation of the same loaded document is
	// equal in value and in bytes.
	again, err := Derive(doc, testConfig())
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
