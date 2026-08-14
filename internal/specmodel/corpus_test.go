package specmodel

import (
	"os"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/corpus"
)

// The inline fixtures above prove each rule in isolation; this proves the
// loader against a real vendor document, where the shapes were not chosen
// to pass. Skips when the pinned document is not cached and cannot be
// fetched, unless TFPFGEN_CORPUS_REQUIRED says the machine must be honest —
// the same split every corpus-backed test in this repo follows.
func TestIntegration_Specmodel_LoadsAPinnedVendorDocument(t *testing.T) {
	path := corpus.SpecPath(t, "thousandeyes")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	doc, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	pin := corpus.MustPin(t, "thousandeyes")
	if doc.Info.Version != pin.Version {
		t.Errorf("info.version = %q, want the pinned %q", doc.Info.Version, pin.Version)
	}
	if got := len(doc.Paths); got != pin.PathCount {
		t.Errorf("loaded %d paths, the pin says %d", got, pin.PathCount)
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
