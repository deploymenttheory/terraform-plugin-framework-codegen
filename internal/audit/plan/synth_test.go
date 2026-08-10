package plan

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// resourceSpec renders a one-entity document around the given create
// schema properties and required list, so each test states only the shape
// it is about.
func resourceSpec(required string, properties string) string {
	return fmt.Sprintf(`openapi: 3.0.3
info:
  title: Synthesis fixture
  version: "1.0"
paths:
  /things:
    post:
      operationId: createThing
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ThingCreate'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
  /things/{thingId}:
    parameters:
      - name: thingId
        in: path
        schema:
          type: string
    get:
      operationId: getThing
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
    put:
      operationId: updateThing
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ThingCreate'
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
    delete:
      operationId: deleteThing
      responses:
        "204":
          description: gone
components:
  schemas:
    ThingCreate:
      type: object
      required: [%s]
      properties:
%s
    Thing:
      type: object
      properties:
        id:
          type: string
          readOnly: true
`, required, properties)
}

func TestUnit_Plan_SynthesisPriorityOrder(t *testing.T) {
	spec := resourceSpec(
		"name, mode, contact, website, ident, when, day, count, ratio, flag, labels, meta, freeform, exampled, defaulted",
		`        name:
          type: string
        mode:
          type: string
          enum: [alpha, beta]
        contact:
          type: string
          format: email
        website:
          type: string
          format: uri
        ident:
          type: string
          format: uuid
        when:
          type: string
          format: date-time
        day:
          type: string
          format: date
        count:
          type: integer
        ratio:
          type: number
        flag:
          type: boolean
        labels:
          type: array
          items:
            type: string
        meta:
          type: object
          required: [key]
          properties:
            key:
              type: string
            optional_extra:
              type: string
        freeform:
          type: string
        exampled:
          type: string
          example: shown
        defaulted:
          type: integer
          default: 7
`)
	in, err := ParseInputs([]byte(`{"thing": {"values": {"name": "given-name"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	p := mustDerive(t, loadDoc(t, spec), testConfig(), in)
	body := entityByKey(t, p, "thing").Steps[0].Body

	want := map[string]any{
		"name":      "given-name", // inputs outrank everything
		"exampled":  "shown",      // example outranks enum/format/type
		"defaulted": 7,            // default likewise
		"mode":      "alpha",      // first enum value
		"contact":   "tfpfgen-" + RunIDToken + "@example.invalid",
		"website":   "https://example.invalid/tfpfgen",
		"ident":     "00000000-0000-4000-8000-000000000000",
		"when":      "2026-01-01T00:00:00Z",
		"day":       "2026-01-01",
		"count":     1,
		"ratio":     1.5,
		"flag":      true,
		"labels":    []any{"sample-labels"},
		"meta":      map[string]any{"key": "sample-key"}, // required nested only
		"freeform":  "sample-freeform",
	}
	if !reflect.DeepEqual(body, want) {
		for k, v := range want {
			if !reflect.DeepEqual(body[k], v) {
				t.Errorf("%s = %#v, want %#v", k, body[k], v)
			}
		}
		for k := range body {
			if _, ok := want[k]; !ok {
				t.Errorf("unexpected field %s = %#v", k, body[k])
			}
		}
	}
}

func TestUnit_Plan_UpdateVariantsMoveEveryScalar(t *testing.T) {
	spec := resourceSpec(
		"name",
		`        name:
          type: string
        mode:
          type: string
          enum: [alpha, beta]
        lonely:
          type: string
          enum: [only]
        contact:
          type: string
          format: email
        count:
          type: integer
        ratio:
          type: number
        flag:
          type: boolean
        labels:
          type: array
          items:
            type: string
`)
	p := mustDerive(t, loadDoc(t, spec), testConfig(), nil)
	steps := entityByKey(t, p, "thing").Steps

	variants := map[string]any{}
	for _, s := range steps {
		if s.Kind == StepUpdateField {
			variants[s.Attribute] = s.Body[s.Attribute]
		}
	}
	want := map[string]any{
		"name":    "tfpfgen-" + RunIDToken + "-thing-name-2",
		"mode":    "beta",
		"contact": "tfpfgen-" + RunIDToken + "-2@example.invalid",
		"count":   2,
		"ratio":   2.5,
		"flag":    false,
	}
	if !reflect.DeepEqual(variants, want) {
		t.Errorf("variants = %#v, want %#v", variants, want)
	}
	// A single-value enum has no variant, and arrays are not update
	// targets: neither yields a step.
	for _, absent := range []string{"lonely", "labels"} {
		if _, ok := variants[absent]; ok {
			t.Errorf("%s should yield no update step", absent)
		}
	}
}

func TestUnit_Plan_CapsBoundWideSchemas(t *testing.T) {
	var props, required strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&required, "r%d, ", i)
		fmt.Fprintf(&props, "        r%d:\n          type: string\n", i)
	}
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&props, "        o%d:\n          type: string\n", i)
	}
	props.WriteString("        pick:\n          type: string\n          enum: [e0, e1, e2, e3, e4, e5, e6, e7, e8, e9]\n")

	spec := resourceSpec(strings.TrimSuffix(required.String(), ", "), props.String())
	p := mustDerive(t, loadDoc(t, spec), testConfig(), nil)
	steps := entityByKey(t, p, "thing").Steps

	counts := map[StepKind]int{}
	for _, s := range steps {
		counts[s.Kind]++
	}
	if counts[StepUpdateField] != maxUpdateFields {
		t.Errorf("updateField steps = %d, want the %d cap", counts[StepUpdateField], maxUpdateFields)
	}
	if counts[StepOmitRequired] != maxOmitRequired {
		t.Errorf("omitRequired steps = %d, want the %d cap", counts[StepOmitRequired], maxOmitRequired)
	}
	if counts[StepConditionalCreate] != maxConditionalValues {
		t.Errorf("conditionalCreate steps = %d, want the %d cap", counts[StepConditionalCreate], maxConditionalValues)
	}

	// 21 writable fields, 11 optional and derivable: allowance is
	// ceil(log2(11)) + 1.
	for _, s := range steps {
		if s.Kind == StepCreateMaximal && s.BisectionAllowance != 5 {
			t.Errorf("bisection allowance = %d, want 5 for 11 optionals", s.BisectionAllowance)
		}
	}
}

func TestUnit_Plan_MissingUpdateYieldsNoUpdateSteps(t *testing.T) {
	spec := strings.Replace(resourceSpec("name", "        name:\n          type: string\n"),
		`    put:
      operationId: updateThing
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ThingCreate'
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
`, "", 1)
	p := mustDerive(t, loadDoc(t, spec), testConfig(), nil)
	for _, s := range entityByKey(t, p, "thing").Steps {
		if s.Kind == StepUpdateField {
			t.Fatalf("an update step without an update operation: %+v", s)
		}
	}
}

func TestUnit_Plan_UnderivableRequiredFieldSkipsTheEntity(t *testing.T) {
	spec := resourceSpec("payload", "        payload: {}\n")
	p := mustDerive(t, loadDoc(t, spec), testConfig(), nil)
	if len(p.Entities) != 0 || len(p.Skipped) != 1 {
		t.Fatalf("plan = %d entities, %d skipped", len(p.Entities), len(p.Skipped))
	}
	s := p.Skipped[0]
	if s.Entity != "thing" || !strings.Contains(s.Reason, `"payload"`) || !strings.Contains(s.Reason, InputsPath) {
		t.Errorf("skip = %+v", s)
	}

	// The same field with an operator value derives fine.
	in, err := ParseInputs([]byte(`{"thing": {"values": {"payload": {"free": "form"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	p = mustDerive(t, loadDoc(t, spec), testConfig(), in)
	body := entityByKey(t, p, "thing").Steps[0].Body
	if !reflect.DeepEqual(body["payload"], map[string]any{"free": "form"}) {
		t.Errorf("payload = %#v", body["payload"])
	}
}

func TestUnit_Plan_AllOfAndSelfReferenceSynthesis(t *testing.T) {
	// A create schema composed of allOf branches, one of which requires a
	// self-referencing property: composition flattens, the cycle bounds.
	spec := `openapi: 3.0.3
info:
  title: Composition fixture
  version: "1.0"
paths:
  /nodes:
    post:
      operationId: createNode
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/NodeCreate'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Node'
  /nodes/{nodeId}:
    parameters:
      - name: nodeId
        in: path
        schema:
          type: string
    get:
      operationId: getNode
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Node'
    delete:
      operationId: deleteNode
      responses:
        "204":
          description: gone
components:
  schemas:
    NodeCreate:
      allOf:
        - type: object
          required: [name]
          properties:
            name:
              type: string
        - type: object
          required: [kind]
          properties:
            kind:
              type: string
              enum: [leaf, branch]
            parent:
              $ref: '#/components/schemas/NodeCreate'
    Node:
      type: object
      properties:
        id:
          type: string
          readOnly: true
`
	p := mustDerive(t, loadDoc(t, spec), testConfig(), nil)
	body := entityByKey(t, p, "node").Steps[0].Body
	if body["name"] != "tfpfgen-"+RunIDToken+"-node-name" || body["kind"] != "leaf" {
		t.Errorf("allOf minimal body = %#v", body)
	}
	// The optional self-reference stays out of the minimal body; the
	// maximal body includes it, bounded rather than infinite.
	if _, ok := body["parent"]; ok {
		t.Errorf("minimal body carries the optional self-reference: %#v", body)
	}
}
