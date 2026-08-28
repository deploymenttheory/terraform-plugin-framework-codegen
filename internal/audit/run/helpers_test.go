package run

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/testapiserver"
)

// testToken is the bearer credential every test run authenticates with —
// distinctive on purpose, so the redaction test can grep for it.
const testToken = "sekret-bearer-token-value-12345"

// thingSpec matches the test API server's fixed surface: a /things
// collection with the full lifecycle. The schema exercises the evidence
// paths: required fields for the minimal body, optional ones for the
// maximal, an enum, a conditional requirement, and a field with a
// documented default.
const thingSpec = `openapi: 3.0.3
info:
  title: Quirk fixture
  version: "1.0"
paths:
  /things:
    get:
      operationId: listThings
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Thing'
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
      required: [name, mode, code, notes]
      properties:
        name:
          type: string
        mode:
          type: string
          enum: [basic, advanced]
        query:
          type: string
          x-tfpfgen-required-when:
            property: mode
            equals: advanced
        code:
          type: string
          example: MiXeD
        notes:
          type: string
          example: some notes
        color:
          type: string
        label:
          type: string
        enabled:
          type: boolean
        retention:
          type: integer
          default: 30
    Thing:
      allOf:
        - $ref: '#/components/schemas/ThingCreate'
        - type: object
          properties:
            id:
              type: string
              readOnly: true
`

// testEnv is the environment every run test starts from.
func testEnv() map[string]string {
	return map[string]string{
		config.SecretToken:       testToken,
		"TFPFGEN_TEST_PARENT_ID": "seeded-parent",
	}
}

func lookupOf(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}

// derivedPlan derives the thing plan the way the CLI would.
func derivedPlan(t *testing.T) *plan.Plan {
	t.Helper()
	document, err := specmodel.Load([]byte(thingSpec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	configuration := &config.Config{
		Audit: config.Audit{NamePrefix: "tfpfgen", MaxObjects: 25, RateLimitRPS: 2},
	}
	p, err := plan.Derive(document, configuration, nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(p.Entities) == 0 {
		t.Fatal("the derived plan has no entities")
	}
	shrinkPolls(p)
	return p
}

// shrinkPolls makes readWithRetry polling test-speed.
func shrinkPolls(p *plan.Plan) {
	for ei := range p.Entities {
		for si := range p.Entities[ei].Steps {
			if p.Entities[ei].Steps[si].Poll != nil {
				p.Entities[ei].Steps[si].Poll = &plan.Poll{Interval: "10ms", Timeout: "2s"}
			}
		}
	}
}

// testOptions is the baseline options against a test API server.
func testOptions(t *testing.T, s *testapiserver.Server, p *plan.Plan, env map[string]string, logs *bytes.Buffer) Options {
	t.Helper()
	if logs == nil {
		logs = &bytes.Buffer{}
	}
	return Options{
		Plan:         p,
		BaseURL:      s.BaseURL(),
		Auth:         Auth{Method: config.AuthBearerToken},
		NamePrefix:   "tfpfgen",
		RateLimitRPS: 500,
		RunsDir:      t.TempDir(),
		SpecHash:     "testspechash",
		Logger:       zerolog.New(logs).Level(zerolog.DebugLevel),
		Lookup:       lookupOf(env),
		RunID:        "testrun1",
	}
}

func mustRun(t *testing.T, opts Options) ([]observe.Observation, Summary) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	obs, summary, err := Run(ctx, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return obs, summary
}

// findObs locates one observation by its identity.
func findObs(obs []observe.Observation, entity, attribute string, kind observe.Kind) *observe.Observation {
	for i := range obs {
		o := &obs[i]
		if o.Entity == entity && o.Attribute == attribute && o.Kind == kind {
			return o
		}
	}
	return nil
}

// wantConfirmed asserts one confirmed observation and returns it.
func wantConfirmed(t *testing.T, obs []observe.Observation, entity, attribute string, kind observe.Kind) *observe.Observation {
	t.Helper()
	o := findObs(obs, entity, attribute, kind)
	if o == nil {
		t.Fatalf("no %s observation for %s.%s; have %v", kind, entity, attribute, observationIndex(obs))
	}
	if o.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("%s %s.%s outcome = %s, want confirmed", kind, entity, attribute, o.Outcome)
	}
	return o
}

func observationIndex(obs []observe.Observation) []string {
	var out []string
	for _, o := range obs {
		out = append(out, o.Entity+"."+o.Attribute+":"+string(o.Kind)+"="+string(o.Outcome))
	}
	return out
}

// entityStatus finds one entity's result in a summary.
func entityStatus(t *testing.T, summary Summary, entity string) EntityResult {
	t.Helper()
	for _, e := range summary.Entities {
		if e.Entity == entity {
			return e
		}
	}
	t.Fatalf("no entity %q in the summary: %+v", entity, summary.Entities)
	return EntityResult{}
}

// thingPlan hand-builds a minimal resource plan for the test API server,
// for tests that need exact step control.
func thingPlan(steps []plan.Step, budget int) *plan.Plan {
	return &plan.Plan{
		Entities: []plan.EntityPlan{{
			Entity:     "thing",
			AuditShape: "resource",
			Budget:     plan.Budget{Requests: budget},
			Steps:      steps,
		}},
		Budget: plan.RunBudget{Requests: 200, Objects: 10, Duration: "1m"},
	}
}

// resourceSteps is the short lifecycle hand-built plans reuse.
func resourceSteps() []plan.Step {
	item := map[string]string{"thingId": "$created:thing"}
	return []plan.Step{
		{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/things",
			Body: map[string]any{"name": "tfpfgen-<runid>-thing-name", "mode": "basic"}},
		{Kind: plan.StepReadWithRetry, Method: "GET", Path: "/things/{thingId}", PathValues: item,
			Poll: &plan.Poll{Interval: "10ms", Timeout: "2s"}},
		{Kind: plan.StepReadConsecutive, Method: "GET", Path: "/things/{thingId}", PathValues: item},
		{Kind: plan.StepDeleteWithConfirmation, Method: "DELETE", Path: "/things/{thingId}", PathValues: item},
		{Kind: plan.StepCleanupDelete, Method: "DELETE", Path: "/things/{thingId}", PathValues: item},
	}
}
