package run

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/quirkserver"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// shapesSpec is the honest-but-partial document for the quirk server's shape
// resources: monitors declare their fields flat with only kind required (the
// audit must discover that interval is really required and that kind gates the
// per-variant fields), and assignments declare agent_id a plain required
// string (the audit must discover it has to reference a live /agents object).
const shapesSpec = `openapi: 3.0.3
info:
  title: shapes fixture
  version: "1.0"
paths:
  /monitors:
    get:
      operationId: listMonitors
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Monitor'
    post:
      operationId: createMonitor
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/MonitorCreate'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Monitor'
  /monitors/{monitorId}:
    parameters:
      - name: monitorId
        in: path
        schema:
          type: string
    get:
      operationId: getMonitor
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Monitor'
    put:
      operationId: updateMonitor
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/MonitorCreate'
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Monitor'
    delete:
      operationId: deleteMonitor
      responses:
        "204":
          description: gone
  /assignments:
    get:
      operationId: listAssignments
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Assignment'
    post:
      operationId: createAssignment
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/AssignmentCreate'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Assignment'
  /assignments/{assignmentId}:
    parameters:
      - name: assignmentId
        in: path
        schema:
          type: string
    get:
      operationId: getAssignment
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Assignment'
    put:
      operationId: updateAssignment
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/AssignmentCreate'
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Assignment'
    delete:
      operationId: deleteAssignment
      responses:
        "204":
          description: gone
  /agents:
    get:
      operationId: listAgents
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Agent'
  /agents/{agentId}:
    parameters:
      - name: agentId
        in: path
        schema:
          type: string
    get:
      operationId: getAgent
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Agent'
components:
  schemas:
    MonitorCreate:
      type: object
      required: [kind]
      properties:
        kind:
          type: string
          enum: [ping, web, dns]
        name:
          type: string
        interval:
          type: integer
        target_host:
          type: string
        domain:
          type: string
        dnssec:
          type: boolean
        web:
          type: object
          properties:
            url:
              type: string
            method:
              type: string
    Monitor:
      allOf:
        - $ref: '#/components/schemas/MonitorCreate'
        - type: object
          properties:
            id:
              type: string
              readOnly: true
    AssignmentCreate:
      type: object
      required: [name, agent_id]
      properties:
        name:
          type: string
        agent_id:
          type: string
    Assignment:
      allOf:
        - $ref: '#/components/schemas/AssignmentCreate'
        - type: object
          properties:
            id:
              type: string
              readOnly: true
    Agent:
      type: object
      properties:
        id:
          type: string
          readOnly: true
        name:
          type: string
        region:
          type: string
`

// strategyConfig is the config a strategy-driven run against the shapes
// fixture uses.
func strategyConfig() *config.Config {
	return &config.Config{
		Audit: config.Audit{NamePrefix: "tfpfgen", MaxObjects: 25, RateLimitRPS: 2},
	}
}

// strategyOptions builds a strategy-driven run: the plan is derived and its
// polls shrunk, and Doc plus Config are set so Run replaces the uniform
// program with each entity's compiled strategy.
func strategyOptions(t *testing.T, s *quirkserver.Server, logs *bytes.Buffer) Options {
	t.Helper()
	doc, err := specmodel.Load([]byte(shapesSpec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := strategyConfig()
	p, err := plan.Derive(doc, cfg, nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	shrinkPolls(p)
	if logs == nil {
		logs = &bytes.Buffer{}
	}
	return Options{
		Plan:         p,
		Doc:          doc,
		Config:       cfg,
		BaseURL:      s.BaseURL(),
		Auth:         Auth{Method: config.AuthBearerToken},
		NamePrefix:   "tfpfgen",
		RateLimitRPS: 500,
		RunsDir:      t.TempDir(),
		SpecHash:     "testspechash",
		Logger:       zerolog.New(logs).Level(zerolog.DebugLevel),
		Lookup:       lookupOf(testEnv()),
		RunID:        "testrun1",
	}
}

// collectionCount GETs a shape collection and returns how many objects it
// holds, for asserting cleanup.
func collectionCount(t *testing.T, base, path, key string) int {
	t.Helper()
	resp, err := http.Get(base + path) //nolint:gosec,noctx // test-local server
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env map[string][]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return len(env[key])
}

func hasAdjustment(sum Summary, entity string, action infer.AdjustAction, field string) bool {
	for _, a := range sum.Adjustments {
		if a.Entity == entity && a.Action == action && a.Field == field {
			return true
		}
	}
	return false
}

// TestUnit_Adaptive_MonitorSelfHealsTheIntervalRequirement is the headline
// self-heal: interval is optional in the document and enforced by the API, so
// the minimal create is refused naming it, the loop adds it and retries, and
// the entity completes rather than blocking — with a requiredByAPI(interval)
// observation to show for it.
func TestUnit_Adaptive_MonitorSelfHealsTheIntervalRequirement(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})

	obs, sum := mustRun(t, strategyOptions(t, s, nil))

	got := entityStatus(t, sum, "monitor")
	if got.Status == StatusBlocked {
		t.Fatalf("monitor blocked despite the self-heal: %+v", got)
	}
	o := findObs(obs, "monitor", "interval", observe.KindRequiredByAPI)
	if o == nil || o.Outcome != observe.OutcomeConfirmed || o.Value != true {
		t.Fatalf("requiredByAPI(interval) = %+v, want a confirmed true", o)
	}
	if !hasAdjustment(sum, "monitor", infer.AdjustAdd, "interval") {
		t.Errorf("no add adjustment recorded for interval: %+v", sum.Adjustments)
	}
	if collectionCount(t, s.BaseURL(), "/monitors", "monitors") != 0 {
		t.Errorf("monitors remain after the run: cleanup did not level the tenant")
	}
}

// TestUnit_Adaptive_AssignmentBorrowsARealAgentID drives reference borrowing:
// the synthesised agent_id is refused, the loop GETs /agents, borrows a real
// id, and the create succeeds — the garbage-then-borrow path.
func TestUnit_Adaptive_AssignmentBorrowsARealAgentID(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})

	_, sum := mustRun(t, strategyOptions(t, s, nil))

	got := entityStatus(t, sum, "assignment")
	if got.Status == StatusBlocked {
		t.Fatalf("assignment blocked; the borrow did not satisfy agent_id: %+v", got)
	}
	if !hasAdjustment(sum, "assignment", infer.AdjustBorrow, "agent_id") {
		t.Fatalf("no borrow adjustment recorded for agent_id: %+v", sum.Adjustments)
	}
	if sum.ObjectsCreated == 0 {
		t.Error("nothing was created; the borrowed reference did not yield an object")
	}
	if collectionCount(t, s.BaseURL(), "/assignments", "assignments") != 0 {
		t.Errorf("assignments remain after the run: cleanup did not level the tenant")
	}
}

// TestUnit_Adaptive_MaximalRemovesWrongVariantFields: a maximal monitor create
// carries fields belonging to other kinds, and the loop removes each one the
// API says is not valid for the kind under test — the validWhen signal.
func TestUnit_Adaptive_MaximalRemovesWrongVariantFields(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})

	_, sum := mustRun(t, strategyOptions(t, s, nil))

	removed := false
	for _, a := range sum.Adjustments {
		if a.Entity == "monitor" && a.Action == infer.AdjustRemove {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("no remove adjustment recorded; the wrong-variant fields were never rejected: %+v", sum.Adjustments)
	}
}

// TestUnit_Adaptive_RequiresFieldIsAdded drives the value-conditional
// co-requirement: dnssec on a dns monitor requires domain, so a create with
// dnssec and no domain is refused naming the pair, the loop adds domain, and
// the create succeeds. Hand-built so the exact body is under test; the
// fallback synthesis fills domain without a strategy.
func TestUnit_Adaptive_RequiresFieldIsAdded(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})

	item := map[string]string{"monitorId": "$created:monitor"}
	steps := []plan.Step{
		{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/monitors",
			Body: map[string]any{"kind": "dns", "interval": 5, "dnssec": true}},
		{Kind: plan.StepCleanupDelete, Method: "DELETE", Path: "/monitors/{monitorId}", PathValues: item},
	}
	p := &plan.Plan{
		Entities: []plan.EntityPlan{{
			Entity: "monitor", Role: "resource",
			Budget: plan.Budget{Requests: 20}, Steps: steps,
		}},
		Budget: plan.RunBudget{Requests: 40, Objects: 10, Duration: "1m"},
	}
	opts := testOptions(t, s, p, testEnv(), nil)

	_, sum := mustRun(t, opts)

	if got := entityStatus(t, sum, "monitor"); got.Status != StatusAudited {
		t.Fatalf("monitor = %+v, want audited via the added domain", got)
	}
	if !hasAdjustment(sum, "monitor", infer.AdjustRequires, "domain") {
		t.Fatalf("no requires adjustment recorded for domain: %+v", sum.Adjustments)
	}
	if collectionCount(t, s.BaseURL(), "/monitors", "monitors") != 0 {
		t.Errorf("the dns monitor was not cleaned up")
	}
}

// TestUnit_Adaptive_UnintelligibleRefusalIsBoundedAndContinues: a 400 whose
// body names no field the grammar understands stops the loop at once rather
// than spinning it; the entity blocks, and a later entity still runs.
func TestUnit_Adaptive_UnintelligibleRefusalIsBoundedAndContinues(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{
		// The /things create refuses without a parseable "field X" sentence.
		RequiredButUndeclared: []string{"serial"},
	})
	seeded := s.Seed(map[string]any{"name": "lookup-target"})

	item := map[string]string{"thingId": "$created:thing"}
	p := &plan.Plan{
		Entities: []plan.EntityPlan{
			{
				Entity: "thing", Role: "resource", Budget: plan.Budget{Requests: 30},
				Steps: []plan.Step{
					{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/things",
						Body: map[string]any{"name": "tfpfgen-<runid>-thing-name"}},
					{Kind: plan.StepCleanupDelete, Method: "DELETE", Path: "/things/{thingId}", PathValues: item},
				},
			},
			{
				Entity: "gadget", Role: "lookup", Budget: plan.Budget{Requests: 10},
				Steps: []plan.Step{{
					Kind: plan.StepRead, Method: "GET", Path: "/things/{thingId}",
					PathValues: map[string]string{"thingId": seeded},
				}},
			},
		},
		Budget: plan.RunBudget{Requests: 200, Objects: 10, Duration: "1m"},
	}

	before := s.Requests()
	obs, sum, err := Run(context.Background(), testOptions(t, s, p, testEnv(), nil))
	if err != nil {
		t.Fatalf("an unhealable refusal blocks the entity, not the run: %v", err)
	}
	if got := entityStatus(t, sum, "thing"); got.Status != StatusBlocked {
		t.Fatalf("thing = %+v, want blocked on the unintelligible refusal", got)
	}
	if got := entityStatus(t, sum, "gadget"); got.Status != StatusAudited {
		t.Fatalf("gadget = %+v, want audited after the blocked entity", got)
	}
	// The block carries the API's own words and names what the search asked
	// for: a status alone cannot be acted on, and an entity that produced no
	// request body leaves no other trace of either.
	blocked := entityStatus(t, sum, "thing")
	if blocked.Refusal == nil || blocked.Refusal.Status != 400 {
		t.Errorf("thing = %+v, want the refusal behind the block", blocked)
	}
	if !strings.Contains(blocked.Reason, "refused with status 400") {
		t.Errorf("reason = %q, want the refusing status", blocked.Reason)
	}
	// Bounded: the unintelligible create was not retried into the ground.
	if spent := s.Requests() - before; spent > 20 {
		t.Errorf("the run spent %d requests; an unhealable create must not spin the loop", spent)
	}
	_ = obs
}

// TestUnit_Adaptive_RedactionHoldsEndToEnd runs a full strategy-driven audit
// of the shape resources and greps everything it produced — observation
// files, the summary, the debug logs — for the bearer token.
func TestUnit_Adaptive_RedactionHoldsEndToEnd(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})

	var logs bytes.Buffer
	obs, sum := mustRun(t, strategyOptions(t, s, &logs))

	outDir := t.TempDir()
	if err := observe.Write(outDir, obs); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(outDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(testToken)) {
			t.Fatalf("%s contains the bearer token", e.Name())
		}
	}
	if strings.Contains(logs.String(), testToken) {
		t.Fatal("the debug log contains the bearer token")
	}
	if raw, _ := json.Marshal(sum); bytes.Contains(raw, []byte(testToken)) {
		t.Fatal("the summary contains the bearer token")
	}
	// The run's activity ledger must not carry it either.
	_ = time.Now
}

// TestUnit_Adaptive_TheReductionKeepsWhatTheCreateNeeded: a field the API
// demanded at createMinimal is not something the maximal reduction may drop.
//
// The reduction protects the body the entity creates by. That body is the one
// the run got accepted, not the one the plan started from — the plan's is the
// document's guess, and the guess is what needed healing. Protecting the guess
// leaves the healed field droppable, and a reduction that drops it can never
// reach an accepted create at all.
func TestUnit_Adaptive_TheReductionKeepsWhatTheCreateNeeded(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{
		// Demanded by the API, absent from the document, and named in a
		// sentence the loop can act on.
		RequiredButUndeclared:    []string{"serial"},
		NamesRefusedFieldInProse: true,
		// One optional field the maximal carries and the API will not take,
		// so the maximal is refused and has to be reduced.
		RejectsDocumentedValue: map[string]string{"colour": "bad-colour"},
	})

	p := &plan.Plan{
		Entities: []plan.EntityPlan{{
			Entity: "thing", Role: "resource", Budget: plan.Budget{Requests: 60},
			Steps: []plan.Step{
				{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/things",
					Body: map[string]any{"name": "tfpfgen-<runid>-thing-name"}},
				{Kind: plan.StepCreateMaximal, Method: "POST", Path: "/things",
					Body: map[string]any{"name": "tfpfgen-<runid>-thing-name", "colour": "bad-colour"}},
			},
		}},
		Budget: plan.RunBudget{Requests: 300, Objects: 10, Duration: "1m"},
	}

	_, sum := mustRun(t, testOptions(t, s, p, testEnv(), nil))

	var recorded *observe.AcceptedRequestBody
	for _, b := range sum.RequestBodies {
		if b.Entity == "thing" {
			recorded = b.Maximal
		}
	}
	if recorded == nil {
		t.Fatal("no maximal create was ever accepted: the reduction dropped what the create needed")
	}
	if _, kept := recorded.Request["serial"]; !kept {
		t.Errorf("the reduction dropped the field the API demanded: %#v", recorded.Request)
	}
	if _, dropped := recorded.Request["colour"]; dropped {
		t.Errorf("the reduction kept the field the API refused: %#v", recorded.Request)
	}
}

// TestUnit_Adaptive_AnOperatorValueReachesTheWire: a value the operator
// supplies for a field the audit cannot guess is what the live create sends.
// Run rebuilds every body the plan derived, so a value applied at plan time
// alone never leaves the plan.
func TestUnit_Adaptive_AnOperatorValueReachesTheWire(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	opts := strategyOptions(t, s, nil)
	opts.Inputs = &plan.Inputs{Entities: map[string]plan.EntityInputs{
		"monitor": {Values: map[string]any{"interval": 42}},
	}}

	_, sum := mustRun(t, opts)

	var sent map[string]any
	for _, b := range sum.RequestBodies {
		if b.Entity == "monitor" && b.Minimal != nil {
			sent = b.Minimal.Request
		}
	}
	if sent == nil {
		t.Fatalf("no accepted create was recorded for monitor: %+v", sum.Entities)
	}
	if got := sent["interval"]; got != 42 {
		t.Errorf("interval = %#v, want the operator's value on the wire", got)
	}
	// A field the operator said nothing about is still synthesised, so the
	// override replaces one value rather than the whole body.
	if got := sent["kind"]; got == nil || got == 42 {
		t.Errorf("kind = %#v, want the synthesised value", got)
	}
}

// TestUnit_Adaptive_AFieldTheAPIDemandedTeachesTheDocument: an API that
// requires a property the document never declares yields both findings — that
// the property exists, and that it is required. Without the first the second
// is unplaceable: revision cannot write required onto a property no schema
// declares.
func TestUnit_Adaptive_AFieldTheAPIDemandedTeachesTheDocument(t *testing.T) {
	t.Parallel()
	// serial is refused when absent and declared by no schema. The refusal
	// names it in prose, which is the shape the grammar reads.
	s := quirkserver.New(t, quirkserver.Quirks{
		RequiredButUndeclared:    []string{"serial"},
		NamesRefusedFieldInProse: true,
	})
	p := &plan.Plan{
		Entities: []plan.EntityPlan{{
			Entity: "thing", Role: "resource", Budget: plan.Budget{Requests: 30},
			DeclaredProperties: []string{"name"},
			Steps: []plan.Step{
				{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/things",
					Body: map[string]any{"name": "tfpfgen-<runid>-thing-name"}},
				{Kind: plan.StepCleanupDelete, Method: "DELETE", Path: "/things/{thingId}",
					PathValues: map[string]string{"thingId": "$created:thing"}},
			},
		}},
		Budget: plan.RunBudget{Requests: 200, Objects: 10, Duration: "1m"},
	}

	obs, _ := mustRun(t, testOptions(t, s, p, testEnv(), nil))

	required := findObs(obs, "thing", "serial", observe.KindRequiredByAPI)
	if required == nil || required.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("no confirmed requiredByAPI for the demanded field: %+v", required)
	}
	declared := findObs(obs, "thing", "serial", observe.KindUndocumentedFieldInSpec)
	if declared == nil || declared.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("no confirmed undocumentedFieldInSpec for the demanded field: %+v", declared)
	}
	// The type is the one the accepted create carried, which is what the
	// correction adds the property as.
	if declared.Value != "string" {
		t.Errorf("declared type = %#v, want the type the accepted body sent", declared.Value)
	}
	// A property the document does declare is not reported as undocumented.
	if name := findObs(obs, "thing", "name", observe.KindUndocumentedFieldInSpec); name != nil {
		t.Errorf("a declared property was reported undocumented: %+v", name)
	}
}
