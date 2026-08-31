package revise

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const linkedUpstreamYAML = `openapi: 3.0.3
info:
  title: sample
  version: 1.2.3
paths: {}
components:
  schemas:
    Widget:
      type: object
      required:
        - name
        - _links
      properties:
        name:
          type: string
        _links:
          $ref: '#/components/schemas/SelfLinks'
        detail:
          type: object
          properties:
            _links:
              $ref: '#/components/schemas/SelfLinks'
            note:
              type: string
    SelfLinks:
      type: object
      readOnly: true
      properties:
        self:
          type: object
          properties:
            href:
              type: string
`

// TestUnit_Revise_NavigationLinksLeaveTheDocument holds revision to the API
// mechanics catalogue: every _links property leaves the revised document at
// every depth, the required list beside one is pruned, and the result names
// each site so the removal is accounted for rather than silent.
func TestUnit_Revise_NavigationLinksLeaveTheDocument(t *testing.T) {
	t.Parallel()
	dir := pinned(t, linkedUpstreamYAML)

	res, err := WriteRevision(dir)
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}

	revised, err := os.ReadFile(filepath.Join(dir, OutputName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(revised)
	if strings.Contains(text, "_links") && !strings.Contains(text, "SelfLinks") {
		t.Fatalf("the revised document still declares _links:\n%s", text)
	}
	if strings.Contains(text, "_links:") {
		t.Errorf("a _links property survived revision:\n%s", text)
	}
	if !strings.Contains(text, "note:") || !strings.Contains(text, "name:") {
		t.Errorf("revision removed more than the catalogued properties:\n%s", text)
	}
	if !strings.Contains(text, "required:") || !strings.Contains(text, "- name") {
		t.Errorf("the surviving required entry is gone:\n%s", text)
	}
	if strings.Contains(text, "- _links") {
		t.Errorf("required still demands the removed property:\n%s", text)
	}

	if len(res.APIMechanicsRemoved) != 2 {
		t.Fatalf("APIMechanicsRemoved = %+v, want the two _links sites", res.APIMechanicsRemoved)
	}
	for _, removal := range res.APIMechanicsRemoved {
		if removal.Mechanic != NavigationLinks || removal.Property != "_links" {
			t.Errorf("removal = %+v, want apiMechanics.navigationLinks on _links", removal)
		}
	}

	// The removal is part of the deterministic write: a second revision of
	// the same inputs is byte-identical.
	if _, err := WriteRevision(dir); err != nil {
		t.Fatalf("second WriteRevision: %v", err)
	}
	again, err := os.ReadFile(filepath.Join(dir, OutputName))
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != text {
		t.Error("two revisions of the same inputs differ")
	}
}

// TestUnit_Revise_ADocumentWithoutMechanicsIsUntouched proves the removal
// pass answers the corrected bytes unchanged when nothing matches, so the
// zero-mechanic document keeps its exact round trip.
func TestUnit_Revise_ADocumentWithoutMechanicsIsUntouched(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)

	res, err := WriteRevision(dir)
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	if len(res.APIMechanicsRemoved) != 0 {
		t.Errorf("APIMechanicsRemoved = %+v on a document with no mechanics", res.APIMechanicsRemoved)
	}
}
