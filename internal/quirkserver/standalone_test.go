package quirkserver

import (
	"net/http"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// TestUnit_Standalone_ServesTheDocumentedLifecycle boots the standalone
// server on a real listener and walks every operation the embedded document
// declares: create, list, read, update, delete. The fixture and its document
// must agree, or a pipeline exercised against the standalone server audits
// a surface the imported spec does not describe.
func TestUnit_Standalone_ServesTheDocumentedLifecycle(t *testing.T) {
	t.Parallel()
	s, err := Standalone("127.0.0.1:0", Quirks{})
	if err != nil {
		t.Fatalf("Standalone: %v", err)
	}
	t.Cleanup(s.Close)

	body := map[string]any{"name": "one", "mode": "basic", "code": "abc", "notes": "n"}
	status, created := post(t, s.CollectionURL(), body)
	if status != http.StatusCreated {
		t.Fatalf("POST %s = %d, want 201 (%v)", s.CollectionURL(), status, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("the created object carries no id: %v", created)
	}

	if status, listed := get(t, s.CollectionURL()); status != http.StatusOK || listed[envelopeKey] == nil {
		t.Fatalf("GET %s = %d %v, want 200 with %q", s.CollectionURL(), status, listed, envelopeKey)
	}
	if status, _ := get(t, s.ItemURL(id)); status != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", s.ItemURL(id), status)
	}
	if status, _ := put(t, s.ItemURL(id), map[string]any{"name": "two"}); status != http.StatusOK {
		t.Fatalf("PUT %s = %d, want 200", s.ItemURL(id), status)
	}
	if status, _ := do(t, http.MethodDelete, s.ItemURL(id), nil); status != http.StatusNoContent {
		t.Fatalf("DELETE %s = %d, want 204", s.ItemURL(id), status)
	}
}

// TestUnit_Standalone_RefusesAnUnusableAddress asserts the listen failure
// surfaces as an error naming the address rather than a panic.
func TestUnit_Standalone_RefusesAnUnusableAddress(t *testing.T) {
	t.Parallel()
	if _, err := Standalone("not-an-address", Quirks{}); err == nil {
		t.Fatal("Standalone accepted an address nothing can listen on")
	}
}

// TestUnit_Spec_ClassifiesAsAFullLifecycleEntity holds the embedded
// document to the promise its name makes: specmodel loads it and classifies
// the /things surface as one entity with every lifecycle role filled — the
// shape the audit planner and both generators need.
func TestUnit_Spec_ClassifiesAsAFullLifecycleEntity(t *testing.T) {
	t.Parallel()
	doc, err := specmodel.Load(Spec())
	if err != nil {
		t.Fatalf("the embedded document does not load: %v", err)
	}

	cls := specmodel.Classify(doc)
	if len(cls.Entities) != 1 {
		t.Fatalf("classified %d entities, want exactly 1: %+v", len(cls.Entities), cls.Entities)
	}
	e := cls.Entities[0]
	if e.CollectionPath != collectionPath {
		t.Errorf("collection path = %q, want %q", e.CollectionPath, collectionPath)
	}
	for role, op := range map[string]*specmodel.Op{
		"create": e.Create, "read": e.Read, "update": e.Update, "delete": e.Delete, "list": e.List,
	} {
		if op == nil {
			t.Errorf("the document declares no %s operation", role)
		}
	}
}

// TestUnit_Spec_ReturnsACopy asserts a caller scribbling on the returned
// bytes cannot corrupt what the next caller reads.
func TestUnit_Spec_ReturnsACopy(t *testing.T) {
	t.Parallel()
	a := Spec()
	a[0] = 'X'
	if b := Spec(); b[0] == 'X' {
		t.Fatal("Spec hands out the embedded backing array itself")
	}
}

// TestUnit_StandaloneQuirks_DeviatesFromTheDocument spot-checks that the
// standalone profile actually misbehaves in the ways its comments claim:
// the discarded field, the unserved documented default, and the volatile
// undeclared field. The audit's whole rehearsal value rests on these
// deviations existing.
func TestUnit_StandaloneQuirks_DeviatesFromTheDocument(t *testing.T) {
	t.Parallel()
	s, err := Standalone("127.0.0.1:0", StandaloneQuirks())
	if err != nil {
		t.Fatalf("Standalone: %v", err)
	}
	t.Cleanup(s.Close)

	status, created := post(t, s.CollectionURL(),
		map[string]any{"name": "one", "mode": "basic", "code": "MiXeD", "notes": "kept?"})
	if status != http.StatusCreated {
		t.Fatalf("POST = %d, want 201 (%v)", status, created)
	}
	id, _ := created["id"].(string)

	_, read := get(t, s.ItemURL(id))
	if _, present := read["notes"]; present {
		t.Errorf("notes survived a read; the profile says it is silently discarded: %v", read)
	}
	if got := read["retention"]; got != float64(45) {
		t.Errorf("retention = %v, want the profile's 45 (the document claims 30)", got)
	}
	if got := read["code"]; got != "mixed" {
		t.Errorf("code = %v, want the normalised %q", got, "mixed")
	}
	first, _ := read["etag"].(string)
	_, again := get(t, s.ItemURL(id))
	if second, _ := again["etag"].(string); first == "" || first == second {
		t.Errorf("etag = %q then %q, want a volatile undeclared field", first, second)
	}
}
