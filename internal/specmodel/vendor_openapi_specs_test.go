package specmodel

import (
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/vendor_openapi_specs"
)

// The inline fixtures above prove each rule in isolation; this proves the
// loader against a real vendor document, where the shapes were not chosen
// to pass.
//
// The version and path count are stated here rather than read from the
// document, so replacing it fails this test rather than passing quietly
// against whatever the new one happens to contain.
func TestIntegration_Specmodel_LoadsAPinnedVendorDocument(t *testing.T) {
	doc, err := Load(vendor_openapi_specs.ThousandEyes())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	const (
		version   = "7.0.102"
		pathCount = 208
	)
	if doc.Info.Version != version {
		t.Errorf("info.version = %q, want %q", doc.Info.Version, version)
	}
	if got := len(doc.Paths); got != pathCount {
		t.Errorf("loaded %d paths, want %d", got, pathCount)
	}
	if got := len(doc.Operations()); got == 0 {
		t.Errorf("loaded no operations")
	}

	cls := Classify(doc)
	if len(cls.Entities) == 0 {
		t.Errorf("classified no entities out of %d paths", len(doc.Paths))
	}
	// Every entity and exclusion carries a key and a collection path;
	// nothing about a real document may come out anonymous.
	for _, c := range cls.Entities {
		if c.Key == "" || c.CollectionPath == "" || len(c.Kinds) == 0 {
			t.Errorf("anonymous classification: %+v", c)
		}
	}
	for _, e := range cls.Excluded {
		if e.Key == "" || e.Reason == "" {
			t.Errorf("anonymous exclusion: %+v", e)
		}
	}
}
