package strategy_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/strategy"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// defaultCfg is a config with the live-object budget the ceiling derives from.
func defaultCfg() *config.Config {
	return &config.Config{Audit: config.Audit{MaxObjects: 25}}
}

// compile loads a spec, classifies it, and compiles the named entity.
func compile(t *testing.T, spec, key string, cfg *config.Config) *strategy.Strategy {
	t.Helper()
	doc, err := specmodel.Load([]byte(spec))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cls := specmodel.Classify(doc)
	for _, c := range cls.Entities {
		if c.Key == key {
			s, err := strategy.Compile(doc, c, cfg)
			if err != nil {
				t.Fatalf("compile %q: %v", key, err)
			}
			return s
		}
	}
	var got []string
	for _, c := range cls.Entities {
		got = append(got, c.Key)
	}
	t.Fatalf("entity %q not classified; classified: %v", key, got)
	return nil
}

// ---- specs -----------------------------------------------------------------

// flatSpec: a resource with no enum or boolean field — no gates, one baseline.
const flatSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /widgets:
    post:
      operationId: createWidget
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
                color: {type: string}
      responses: {"201": {description: made, content: {application/json: {schema: {$ref: '#/components/schemas/Widget'}}}}}
  /widgets/{id}:
    get:
      operationId: getWidget
      responses: {"200": {description: ok, content: {application/json: {schema: {$ref: '#/components/schemas/Widget'}}}}}
    delete:
      operationId: deleteWidget
      responses: {"204": {description: gone}}
components:
  schemas:
    Widget:
      type: object
      properties:
        id: {type: string, readOnly: true}
        name: {type: string}
        color: {type: string}
`

// oneOfSpec: a resource gated by a `kind` enum whose value selects a oneOf
// branch's distinct field set. No discriminator — branches pin the enum.
const oneOfSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /gadgets:
    post:
      operationId: createGadget
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/GadgetCreate'}
      responses: {"201": {description: made, content: {application/json: {schema: {$ref: '#/components/schemas/Gadget'}}}}}
  /gadgets/{id}:
    get:
      operationId: getGadget
      responses: {"200": {description: ok, content: {application/json: {schema: {$ref: '#/components/schemas/Gadget'}}}}}
    delete:
      operationId: deleteGadget
      responses: {"204": {description: gone}}
components:
  schemas:
    Gadget:
      type: object
      properties: {id: {type: string, readOnly: true}}
    GadgetCreate:
      type: object
      required: [name, kind]
      properties:
        name: {type: string}
        kind: {type: string, enum: [x, y]}
      oneOf:
        - type: object
          properties:
            kind: {type: string, enum: [x]}
            xField: {type: string}
        - type: object
          properties:
            kind: {type: string, enum: [y]}
            yField: {type: string}
`

// discriminatorSpec: a resource gated by `type`, mapped to named branches by
// an explicit discriminator.
const discriminatorSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /tests:
    post:
      operationId: createTest
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/TestCreate'}
      responses: {"201": {description: made, content: {application/json: {schema: {$ref: '#/components/schemas/TestObj'}}}}}
  /tests/{id}:
    get:
      operationId: getTest
      responses: {"200": {description: ok, content: {application/json: {schema: {$ref: '#/components/schemas/TestObj'}}}}}
    delete:
      operationId: deleteTest
      responses: {"204": {description: gone}}
components:
  schemas:
    TestObj:
      type: object
      properties: {id: {type: string, readOnly: true}}
    Http:
      type: object
      properties: {url: {type: string}}
    Dns:
      type: object
      properties: {domain: {type: string}}
    TestCreate:
      type: object
      required: [name, type]
      properties:
        name: {type: string}
        type: {type: string, enum: [http, dns]}
      oneOf:
        - {$ref: '#/components/schemas/Http'}
        - {$ref: '#/components/schemas/Dns'}
      discriminator:
        propertyName: type
        mapping:
          http: '#/components/schemas/Http'
          dns: '#/components/schemas/Dns'
`

// dependentSpec: a resource with dependentRequired and dependentSchemas.
const dependentSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /alarms:
    post:
      operationId: createAlarm
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
                a: {type: string}
                b: {type: string}
                c: {type: string}
                d: {type: string}
              dependentRequired:
                a: [b]
              dependentSchemas:
                c:
                  required: [d]
                  properties:
                    d: {type: string}
      responses: {"201": {description: made, content: {application/json: {schema: {$ref: '#/components/schemas/Alarm'}}}}}
  /alarms/{id}:
    get:
      operationId: getAlarm
      responses: {"200": {description: ok, content: {application/json: {schema: {$ref: '#/components/schemas/Alarm'}}}}}
    delete:
      operationId: deleteAlarm
      responses: {"204": {description: gone}}
components:
  schemas:
    Alarm:
      type: object
      properties: {id: {type: string, readOnly: true}}
`

// proseSpec: a resource whose descriptions carry conditional language — a
// value-gated requirement, a validity edge, an exclusion, and one hint that
// names nothing and must be discarded.
const proseSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /rules:
    post:
      operationId: createRule
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
                mode: {type: string, enum: [dynamic, static]}
                query:
                  type: string
                  description: "Required when mode is dynamic."
                region:
                  type: string
                  description: "Only applies when mode is static."
                primary:
                  type: string
                  description: "Cannot be used with secondary."
                secondary: {type: string}
                notes:
                  type: string
                  description: "Free-form operator notes with no special behaviour."
      responses: {"201": {description: made, content: {application/json: {schema: {$ref: '#/components/schemas/Rule'}}}}}
  /rules/{id}:
    get:
      operationId: getRule
      responses: {"200": {description: ok, content: {application/json: {schema: {$ref: '#/components/schemas/Rule'}}}}}
    delete:
      operationId: deleteRule
      responses: {"204": {description: gone}}
components:
  schemas:
    Rule:
      type: object
      properties: {id: {type: string, readOnly: true}}
`

// boolSpec: a resource gated by a boolean.
const boolSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /flags:
    post:
      operationId: createFlag
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
                enabled: {type: boolean}
      responses: {"201": {description: made, content: {application/json: {schema: {$ref: '#/components/schemas/Flag'}}}}}
  /flags/{id}:
    get:
      operationId: getFlag
      responses: {"200": {description: ok, content: {application/json: {schema: {$ref: '#/components/schemas/Flag'}}}}}
    delete:
      operationId: deleteFlag
      responses: {"204": {description: gone}}
components:
  schemas:
    Flag:
      type: object
      properties: {id: {type: string, readOnly: true}}
`

// lookupSpec: a datasource whose only access is the item read (no list) —
// yields a read-only lookup strategy.
const lookupSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /agents/{name}:
    get:
      operationId: getAgent
      responses: {"200": {description: ok, content: {application/json: {schema: {$ref: '#/components/schemas/Agent'}}}}}
components:
  schemas:
    Agent:
      type: object
      properties: {name: {type: string}}
`

// listDatasourceSpec: a list-plus-read datasource (no create/delete) — a
// read-only strategy in the datasource role.
const listDatasourceSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /zones:
    get:
      operationId: listZones
      responses: {"200": {description: ok, content: {application/json: {schema: {type: array, items: {$ref: '#/components/schemas/Zone'}}}}}}
  /zones/{id}:
    get:
      operationId: getZone
      responses: {"200": {description: ok, content: {application/json: {schema: {$ref: '#/components/schemas/Zone'}}}}}
components:
  schemas:
    Zone:
      type: object
      properties: {id: {type: string}}
`

// ---- helpers ---------------------------------------------------------------

func findVariant(s *strategy.Strategy, gateField, gateValue string) *strategy.Variant {
	for i := range s.Variants {
		if s.Variants[i].GateField == gateField && s.Variants[i].GateValue == gateValue {
			return &s.Variants[i]
		}
	}
	return nil
}

func findHypothesis(s *strategy.Strategy, kind strategy.HypothesisKind, subject string) *strategy.Hypothesis {
	for i := range s.Hypotheses {
		for _, sub := range s.Hypotheses[i].Subjects {
			if s.Hypotheses[i].Kind == kind && sub == subject {
				return &s.Hypotheses[i]
			}
		}
	}
	return nil
}

func countSteps(s *strategy.Strategy, kind string) int {
	n := 0
	for _, st := range s.Program {
		if string(st.Kind) == kind {
			n++
		}
	}
	return n
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// ---- tests -----------------------------------------------------------------

func TestFlatResourceHasSingleBaseline(t *testing.T) {
	s := compile(t, flatSpec, "widget", defaultCfg())

	if s.Role != "resource" || s.ReadOnly {
		t.Fatalf("role=%q readOnly=%v, want resource/false", s.Role, s.ReadOnly)
	}
	if len(s.Gates) != 0 {
		t.Fatalf("gates=%v, want none", s.Gates)
	}
	if len(s.Variants) != 1 {
		t.Fatalf("variants=%d, want 1 baseline", len(s.Variants))
	}
	base := s.Variants[0]
	if base.GateField != "" || base.GateValue != "" {
		t.Fatalf("baseline carries a gate: %+v", base)
	}
	if !reflect.DeepEqual(base.Minimal.Fields, []string{"name"}) {
		t.Fatalf("minimal=%v, want [name]", base.Minimal.Fields)
	}
	if !reflect.DeepEqual(base.Maximal.Fields, []string{"color", "name"}) {
		t.Fatalf("maximal=%v, want [color name]", base.Maximal.Fields)
	}
	if len(s.Hypotheses) != 0 {
		t.Fatalf("hypotheses=%v, want none", s.Hypotheses)
	}
	// The program covers the full lifecycle and ends with cleanup.
	if s.Program[len(s.Program)-1].Kind != "cleanupDelete" {
		t.Fatalf("last step=%q, want cleanupDelete", s.Program[len(s.Program)-1].Kind)
	}
	if countSteps(s, "createMinimal") != 1 {
		t.Fatalf("createMinimal count=%d, want 1", countSteps(s, "createMinimal"))
	}
}

func TestOneOfResourceYieldsVariantPerValue(t *testing.T) {
	s := compile(t, oneOfSpec, "gadget", defaultCfg())

	if len(s.Gates) != 1 || s.Gates[0].Field != "kind" || s.Gates[0].Kind != strategy.GateRequiredEnum {
		t.Fatalf("gates=%+v, want one required-enum gate on kind", s.Gates)
	}
	// baseline + x + y.
	if len(s.Variants) != 3 {
		t.Fatalf("variants=%d, want 3", len(s.Variants))
	}
	vx := findVariant(s, "kind", "x")
	vy := findVariant(s, "kind", "y")
	if vx == nil || vy == nil {
		t.Fatalf("missing a gate variant: %+v", s.Variants)
	}
	if vx.Provenance != strategy.ProvenanceStructural {
		t.Fatalf("x provenance=%q, want structural", vx.Provenance)
	}
	if !contains(vx.Maximal.Fields, "xField") || contains(vx.Maximal.Fields, "yField") {
		t.Fatalf("x maximal=%v, want xField without yField", vx.Maximal.Fields)
	}
	if !contains(vy.Maximal.Fields, "yField") || contains(vy.Maximal.Fields, "xField") {
		t.Fatalf("y maximal=%v, want yField without xField", vy.Maximal.Fields)
	}
	// Each branch's extra field is a structural variant hypothesis.
	h := findHypothesis(s, strategy.HypothesisVariant, "xField")
	if h == nil || h.GateValue != "x" || h.Provenance != strategy.ProvenanceStructural {
		t.Fatalf("missing structural variant hypothesis for xField: %+v", s.Hypotheses)
	}
	// A per-value create is scheduled for each gate value.
	if countSteps(s, "createPerEnumValue") < 2 {
		t.Fatalf("createPerEnumValue count=%d, want >=2", countSteps(s, "createPerEnumValue"))
	}
}

func TestDiscriminatorMappingResolvesBranches(t *testing.T) {
	s := compile(t, discriminatorSpec, "test", defaultCfg())

	vhttp := findVariant(s, "type", "http")
	vdns := findVariant(s, "type", "dns")
	if vhttp == nil || vdns == nil {
		t.Fatalf("variants=%+v, want http and dns", s.Variants)
	}
	if !contains(vhttp.Maximal.Fields, "url") || contains(vhttp.Maximal.Fields, "domain") {
		t.Fatalf("http maximal=%v, want url without domain", vhttp.Maximal.Fields)
	}
	if !contains(vdns.Maximal.Fields, "domain") || contains(vdns.Maximal.Fields, "url") {
		t.Fatalf("dns maximal=%v, want domain without url", vdns.Maximal.Fields)
	}
	if vhttp.Provenance != strategy.ProvenanceStructural {
		t.Fatalf("http provenance=%q, want structural", vhttp.Provenance)
	}
}

func TestDependentRequiredYieldsRequiresField(t *testing.T) {
	s := compile(t, dependentSpec, "alarm", defaultCfg())

	// dependentRequired a:[b] -> requiresField {a,b}, structural.
	h := findHypothesis(s, strategy.HypothesisRequiresField, "a")
	if h == nil {
		t.Fatalf("no requiresField hypothesis for a: %+v", s.Hypotheses)
	}
	if h.Provenance != strategy.ProvenanceStructural {
		t.Fatalf("provenance=%q, want structural", h.Provenance)
	}
	if !contains(h.Subjects, "a") || !contains(h.Subjects, "b") {
		t.Fatalf("subjects=%v, want a and b", h.Subjects)
	}
	// dependentSchemas c -> requires d.
	hc := findHypothesis(s, strategy.HypothesisRequiresField, "c")
	if hc == nil || !contains(hc.Subjects, "d") {
		t.Fatalf("no requiresField hypothesis for c requiring d: %+v", s.Hypotheses)
	}
}

func TestProseRequiredWhenAndDiscard(t *testing.T) {
	s := compile(t, proseSpec, "rule", defaultCfg())

	// "Required when mode is dynamic." -> requiredWhen(query, mode=dynamic), prose.
	h := findHypothesis(s, strategy.HypothesisRequiredWhen, "query")
	if h == nil {
		t.Fatalf("no requiredWhen hypothesis for query: %+v", s.Hypotheses)
	}
	if h.Provenance != strategy.ProvenanceProse || h.GateField != "mode" || h.GateValue != "dynamic" {
		t.Fatalf("query hypothesis=%+v, want prose mode=dynamic", *h)
	}
	// "Only applies when mode is static." -> validWhen(region, mode=static), prose.
	hr := findHypothesis(s, strategy.HypothesisValidWhen, "region")
	if hr == nil || hr.GateValue != "static" || hr.Provenance != strategy.ProvenanceProse {
		t.Fatalf("region validWhen hypothesis wrong: %+v", s.Hypotheses)
	}
	// "Cannot be used with secondary." -> mutuallyExclusive{primary,secondary}, prose.
	he := findHypothesis(s, strategy.HypothesisMutuallyExclusive, "primary")
	if he == nil || !contains(he.Subjects, "secondary") || he.Provenance != strategy.ProvenanceProse {
		t.Fatalf("primary mutuallyExclusive hypothesis wrong: %+v", s.Hypotheses)
	}
	// The notes field names nothing -> discarded, no hypothesis mentions it.
	if findHypothesis(s, strategy.HypothesisRequiredWhen, "notes") != nil ||
		findHypothesis(s, strategy.HypothesisValidWhen, "notes") != nil ||
		findHypothesis(s, strategy.HypothesisRequiresField, "notes") != nil {
		t.Fatalf("a hint naming nothing was not discarded: %+v", s.Hypotheses)
	}
}

func TestBoolGate(t *testing.T) {
	s := compile(t, boolSpec, "flag", defaultCfg())

	if len(s.Gates) != 1 || s.Gates[0].Kind != strategy.GateBool || s.Gates[0].Field != "enabled" {
		t.Fatalf("gates=%+v, want one bool gate on enabled", s.Gates)
	}
	if !reflect.DeepEqual(s.Gates[0].Values, []string{"false", "true"}) {
		t.Fatalf("bool values=%v, want [false true]", s.Gates[0].Values)
	}
	// baseline + false + true, both derived (a bool declares no branch fields).
	if len(s.Variants) != 3 {
		t.Fatalf("variants=%d, want 3", len(s.Variants))
	}
	vt := findVariant(s, "enabled", "true")
	if vt == nil || vt.Provenance != strategy.ProvenanceDerived {
		t.Fatalf("enabled=true variant wrong: %+v", s.Variants)
	}
	// A bool gate gets no undocumented-value step.
	if countSteps(s, "undocumentedEnumValue") != 0 {
		t.Fatalf("undocumentedEnumValue count=%d, want 0 for a bool gate", countSteps(s, "undocumentedEnumValue"))
	}
}

func TestLookupDatasourceIsReadOnly(t *testing.T) {
	s := compile(t, lookupSpec, "agent", defaultCfg())

	if !s.ReadOnly || s.Role != "lookup" {
		t.Fatalf("role=%q readOnly=%v, want lookup/true", s.Role, s.ReadOnly)
	}
	if len(s.Gates) != 0 || len(s.Variants) != 0 {
		t.Fatalf("read-only strategy carries gates/variants: %+v", s)
	}
	kinds := []string{}
	for _, st := range s.Program {
		kinds = append(kinds, string(st.Kind))
	}
	if !reflect.DeepEqual(kinds, []string{"read", "readConsecutive"}) {
		t.Fatalf("program=%v, want [read readConsecutive]", kinds)
	}
}

func TestListDatasourceIsReadOnly(t *testing.T) {
	s := compile(t, listDatasourceSpec, "zone", defaultCfg())
	if !s.ReadOnly || s.Role != "datasource" {
		t.Fatalf("role=%q readOnly=%v, want datasource/true", s.Role, s.ReadOnly)
	}
}

func TestBudgetScalesWithComplexity(t *testing.T) {
	flat := compile(t, flatSpec, "widget", defaultCfg())
	disc := compile(t, discriminatorSpec, "test", defaultCfg())

	if disc.Budget.Requests <= flat.Budget.Requests {
		t.Fatalf("discriminated budget %d not greater than flat %d", disc.Budget.Requests, flat.Budget.Requests)
	}
	if flat.Budget.Formula == "" || disc.Budget.Formula == "" {
		t.Fatalf("budget formula missing: flat=%q disc=%q", flat.Budget.Formula, disc.Budget.Formula)
	}
	// The read-only budget is small and fixed.
	ro := compile(t, lookupSpec, "agent", defaultCfg())
	if ro.Budget.Requests != 2 {
		t.Fatalf("read-only budget=%d, want 2", ro.Budget.Requests)
	}
}

func TestBudgetCeilingCaps(t *testing.T) {
	// A tiny object budget forces the ceiling to bind.
	cfg := &config.Config{Audit: config.Audit{MaxObjects: 1}}
	s := compile(t, discriminatorSpec, "test", cfg)
	if s.Budget.Requests != 12 { // maxObjects(1) × perObjectCost(12)
		t.Fatalf("capped budget=%d, want 12", s.Budget.Requests)
	}
	if !strings.Contains(s.Budget.Formula, "capped") {
		t.Fatalf("formula %q should record the cap", s.Budget.Formula)
	}
}

func TestBudgetDefaultsMaxObjects(t *testing.T) {
	// MaxObjects unset (0) falls back to 25, so the ceiling does not bind.
	cfg := &config.Config{}
	s := compile(t, flatSpec, "widget", cfg)
	if s.Budget.Requests == 0 {
		t.Fatalf("budget should be non-zero with defaulted maxObjects")
	}
}

func TestDeterministic(t *testing.T) {
	doc, err := specmodel.Load([]byte(discriminatorSpec))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cls := specmodel.Classify(doc)
	var c specmodel.Classification
	for _, e := range cls.Entities {
		if e.Key == "test" {
			c = e
		}
	}

	s1, err := strategy.Compile(doc, c, defaultCfg())
	if err != nil {
		t.Fatalf("compile 1: %v", err)
	}
	s2, err := strategy.Compile(doc, c, defaultCfg())
	if err != nil {
		t.Fatalf("compile 2: %v", err)
	}
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("Compile is not deterministic under DeepEqual")
	}
	j1, err := s1.JSON()
	if err != nil {
		t.Fatalf("json 1: %v", err)
	}
	j2, err := s2.JSON()
	if err != nil {
		t.Fatalf("json 2: %v", err)
	}
	if string(j1) != string(j2) {
		t.Fatalf("JSON is not deterministic:\n%s\n---\n%s", j1, j2)
	}
	if len(j1) == 0 || j1[len(j1)-1] != '\n' {
		t.Fatalf("JSON should end with a newline")
	}
}

func TestCompileErrors(t *testing.T) {
	doc, _ := specmodel.Load([]byte(flatSpec))
	cls := specmodel.Classify(doc)
	var widget specmodel.Classification
	for _, e := range cls.Entities {
		if e.Key == "widget" {
			widget = e
		}
	}

	if _, err := strategy.Compile(nil, widget, defaultCfg()); err == nil {
		t.Fatal("nil doc should error")
	}
	if _, err := strategy.Compile(doc, widget, nil); err == nil {
		t.Fatal("nil cfg should error")
	}
	// An entity that is neither a resource nor readable.
	action := specmodel.Classification{
		Key:    "invoke",
		Kinds:  []specmodel.Kind{specmodel.KindAction},
		Create: &specmodel.Op{Method: "POST", Path: "/invoke"},
	}
	if _, err := strategy.Compile(doc, action, defaultCfg()); err == nil {
		t.Fatal("non-auditable entity should error")
	}
	// A resource whose create operation cannot be resolved to a body.
	bad := specmodel.Classification{
		Key:    "phantom",
		Kinds:  []specmodel.Kind{specmodel.KindResource},
		Create: &specmodel.Op{Method: "POST", Path: "/nowhere"},
	}
	if _, err := strategy.Compile(doc, bad, defaultCfg()); err == nil {
		t.Fatal("resource with no create body should error")
	}
}
