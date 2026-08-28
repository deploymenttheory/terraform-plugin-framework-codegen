package revise

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/store"
)

// compositeSpec exercises the document shapes the flat fixture cannot:
// component request bodies and responses, allOf composition over a $ref
// base, a property that is itself a $ref, media types beyond plain
// application/json, and a bodiless action entity.
const compositeSpec = `openapi: 3.0.3
info:
  title: composite
  version: 1.0.0
paths:
  /widgets:
    post:
      requestBody:
        $ref: '#/components/requestBodies/WidgetBody'
      responses:
        "201":
          $ref: '#/components/responses/WidgetResponse'
  /widgets/{widgetId}:
    get:
      responses:
        "200":
          $ref: '#/components/responses/WidgetResponse'
    delete:
      responses:
        "204":
          description: deleted
  /pings:
    post:
      requestBody:
        content:
          application/json:
            schema:
              type: object
      responses:
        "200":
          description: ponged
components:
  requestBodies:
    WidgetBody:
      content:
        text/plain:
          schema:
            type: string
        application/hal+json:
          schema:
            $ref: '#/components/schemas/Widget'
  responses:
    WidgetResponse:
      description: one widget
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/Widget'
  schemas:
    WidgetBase:
      type: object
      properties:
        kind:
          $ref: '#/components/schemas/KindValue'
    KindValue:
      type: string
      enum:
        - basic
    Widget:
      allOf:
        - $ref: '#/components/schemas/WidgetBase'
        - type: object
          properties:
            label:
              type: string
`

// pinnedComposite imports compositeSpec into <root>/spec.
func pinnedComposite(t *testing.T) (root, specDir string, lock store.Lock) {
	t.Helper()
	root = t.TempDir()
	specDir = filepath.Join(root, "spec")
	res, err := store.Import(specDir, []byte(compositeSpec), "published.yaml")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return root, specDir, res.Lock
}

func TestUnit_Propose_PlacesCorrectionsThroughComposedSchemas(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedComposite(t)
	widget := func(attribute string, kind observe.Kind, value any) observe.Observation {
		o := confirmedObs(attribute, kind, value, nil, lock.SHA256)
		o.Entity = "widget"
		return o
	}
	commitObs(t, root,
		// Declared in the inline allOf branch: required lands there.
		widget("label", observe.KindRequiredByAPI, true),
		// The property is a $ref: readOnly must land on the resolved
		// component, an extension beside the reference.
		widget("kind", observe.KindWritable, false),
		widget("kind", observe.KindImmutable, true),
		// The enum lives on the referenced component schema.
		widget("kind", observe.KindValues, observe.Values{Accepted: []string{"basic", "extra"}}),
	)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 4 {
		t.Fatalf("Proposed = %+v, want 4", p.Proposed)
	}

	wantPaths := map[string]string{
		"001": `"path": "/components/schemas/WidgetBase/properties/kind/x-tfpfgen-immutable"`,
		"002": `"path": "/components/schemas/KindValue/enum/-"`,
		"003": `"path": "/components/schemas/KindValue/readOnly"`,
		"004": `"path": "/components/schemas/Widget/allOf/1/required"`,
	}
	for _, w := range p.Proposed {
		raw, err := os.ReadFile(w.Path)
		if err != nil {
			t.Fatal(err)
		}
		ordinal := filepath.Base(w.Path)[:3]
		if want := wantPaths[ordinal]; !strings.Contains(string(raw), want) {
			t.Errorf("%s missing %s:\n%s", filepath.Base(w.Path), want, raw)
		}
	}

	// The corrections apply cleanly to the composed document.
	acceptAll(t, specDir)
	if _, err := WriteRevision(specDir); err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	p2, err := Propose(specDir)
	if err != nil {
		t.Fatalf("re-Propose: %v", err)
	}
	if len(p2.Proposed) != 0 || len(p2.AlreadyStated) != 4 {
		t.Errorf("re-propose: Proposed = %+v, AlreadyStated = %+v; want convergence", p2.Proposed, p2.AlreadyStated)
	}
}

func TestUnit_Propose_ReportsEntityLevelKindsTheEntityCannotCarry(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedComposite(t)
	obs := []observe.Observation{}
	for _, kind := range []observe.Kind{observe.KindReadAfterWrite, observe.KindDeleteNotFoundOK, observe.KindUpdateStyle} {
		o := confirmedObs("", kind, map[observe.Kind]any{
			observe.KindReadAfterWrite:   "2s",
			observe.KindDeleteNotFoundOK: true,
			observe.KindUpdateStyle:      "patch-merge",
		}[kind], nil, lock.SHA256)
		o.Entity = "ping" // an action: no read, no update, no delete
		obs = append(obs, o)
	}
	// And a per-attribute observation naming no attribute.
	anon := confirmedObs("", observe.KindVolatile, true, nil, lock.SHA256)
	anon.Entity = "widget"
	obs = append(obs, anon)
	commitObs(t, root, obs...)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 0 {
		t.Errorf("Proposed = %+v, want nothing placeable", p.Proposed)
	}
	if len(p.Unplaceable) != 4 {
		t.Fatalf("Unplaceable = %+v, want all four", p.Unplaceable)
	}
	for _, n := range p.Unplaceable {
		switch n.Kind {
		case observe.KindReadAfterWrite:
			if !strings.Contains(n.Reason, "no read operation") {
				t.Errorf("readAfterWrite reason = %q", n.Reason)
			}
		case observe.KindDeleteNotFoundOK:
			if !strings.Contains(n.Reason, "no delete operation") {
				t.Errorf("deleteNotFoundOK reason = %q", n.Reason)
			}
		case observe.KindUpdateStyle:
			if !strings.Contains(n.Reason, "no update operation") {
				t.Errorf("updateStyle reason = %q", n.Reason)
			}
		case observe.KindVolatile:
			if !strings.Contains(n.Reason, "names no attribute") {
				t.Errorf("volatile reason = %q", n.Reason)
			}
		}
	}
}

// annotatedSpec is the flat fixture with every fact already stated, so every
// observation about it must compile to nothing.
const annotatedSpec = `openapi: 3.0.3
info:
  title: annotated
  version: 1.0.0
paths:
  /tags:
    post:
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Tag'
      responses:
        "201":
          description: created
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Tag'
  /tags/{tagId}:
    get:
      x-tfpfgen-read-after-write: 5s
      responses:
        "200":
          description: read
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Tag'
    put:
      x-tfpfgen-update-style: put-full
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Tag'
      responses:
        "200":
          description: updated
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Tag'
    delete:
      x-tfpfgen-delete-not-found-ok: true
      responses:
        "204":
          description: deleted
components:
  schemas:
    Tag:
      type: object
      required:
        - name
      properties:
        name:
          type: string
          x-tfpfgen-immutable: true
        color:
          type: string
          x-tfpfgen-values: true
          enum:
            - red
            - green
        size:
          type: string
          readOnly: true
        mode:
          type: string
          x-tfpfgen-server-default: auto
          x-tfpfgen-volatile: true
        port:
          type: integer
          x-tfpfgen-required-when:
            property: protocol
            equals: tcp
        protocol:
          type: string
`

func TestUnit_Propose_AnAlreadyStatedFactProposesNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	res, err := store.Import(specDir, []byte(annotatedSpec), "published.yaml")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	hash := res.Lock.SHA256
	commitObs(t, root,
		confirmedObs("name", observe.KindRequiredByAPI, true, nil, hash),
		confirmedObs("name", observe.KindImmutable, true, nil, hash),
		confirmedObs("size", observe.KindWritable, false, nil, hash),
		confirmedObs("mode", observe.KindServerDefault, "auto", nil, hash),
		confirmedObs("mode", observe.KindVolatile, true, nil, hash),
		confirmedObs("port", observe.KindRequiredWhen, true,
			&observe.Condition{Attribute: "protocol", Equals: "tcp"}, hash),
		confirmedObs("color", observe.KindValues,
			observe.Values{Accepted: []string{"red", "green"}, Rejected: []string{"blue"}, Closed: boolPtr(false)}, nil, hash),
		confirmedObs("", observe.KindUpdateStyle, "put-full", nil, hash),
		confirmedObs("", observe.KindDeleteNotFoundOK, true, nil, hash),
		confirmedObs("", observe.KindReadAfterWrite, "2s", nil, hash),
	)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed)+len(p.AutoAccepted) != 0 {
		t.Errorf("wrote corrections for stated facts: %+v %+v", p.Proposed, p.AutoAccepted)
	}
	if len(p.AlreadyStated) != 10 {
		t.Fatalf("AlreadyStated = %d (%+v), want all 10", len(p.AlreadyStated), p.AlreadyStated)
	}
}

func TestUnit_Propose_AConfirmationOfTheDocumentProposesNothing(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		confirmedObs("name", observe.KindWritable, true, nil, lock.SHA256),
		confirmedObs("name", observe.KindImmutable, false, nil, lock.SHA256),
		confirmedObs("name", observe.KindRequiredByAPI, false, nil, lock.SHA256),
		confirmedObs("port", observe.KindRequiredWhen, false,
			&observe.Condition{Attribute: "protocol", Equals: "tcp"}, lock.SHA256),
		confirmedObs("", observe.KindDeleteNotFoundOK, false, nil, lock.SHA256),
		confirmedObs("mode", observe.KindVolatile, false, nil, lock.SHA256),
		// A values record that agrees with the document, about a property
		// with no enum at all.
		confirmedObs("size", observe.KindValues, observe.Values{Closed: boolPtr(false)}, nil, lock.SHA256),
	)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed)+len(p.AutoAccepted) != 0 {
		t.Errorf("wrote corrections for confirmations: %+v %+v", p.Proposed, p.AutoAccepted)
	}
	if len(p.AlreadyStated) != 7 {
		t.Fatalf("AlreadyStated = %d (%+v), want all 7", len(p.AlreadyStated), p.AlreadyStated)
	}
}

func TestUnit_Propose_RendersANonStringDefaultInItsJSONForm(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, confirmedObs("port", observe.KindServerDefault, 8080, nil, lock.SHA256))

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	got := readProposed(t, p)
	for _, want := range []string{
		"stores 8080",
		// The reading lands on the property as an extension, not in OpenAPI's
		// own `default` — which says what the document declares, is read by
		// nothing in the generation path, and on a $ref'd property would be
		// written onto a schema every other use of that type shares.
		`"path": "/components/schemas/Tag/properties/port/x-tfpfgen-server-default"`,
		`"value": 8080`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("proposal missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `/properties/port/default"`) {
		t.Errorf("proposal still writes OpenAPI's own default:\n%s", got)
	}
}

func TestUnit_Propose_RefusesWhenAnAcceptedCorrectionIsBroken(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256))
	writeFile(t, filepath.Join(specDir, "corrections", "broken.correction.json"), "not json")

	_, err := Propose(specDir)
	if err == nil || !strings.Contains(err.Error(), "not a usable correction") {
		t.Fatalf("a broken accepted correction must refuse before compiling: %v", err)
	}
}

func TestUnit_Propose_ReplacesADefaultTheDocumentGetsWrong(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	res, err := store.Import(specDir, []byte(annotatedSpec), "published.yaml")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	commitObs(t, root, confirmedObs("mode", observe.KindServerDefault, "manual", nil, res.Lock.SHA256))

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	got := readProposed(t, p)
	if !strings.Contains(got, `"value": "manual"`) {
		t.Errorf("proposal does not replace the wrong default:\n%s", got)
	}
}
