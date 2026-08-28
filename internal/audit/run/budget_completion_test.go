package run

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/quirkserver"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// TestUnit_Run_MonitorAndAssignmentCompleteWithoutExhaustion is the budget
// regression the live quirkserver rehearsal surfaced: under the program-summed
// budget, both the multi-variant monitor and the flat assignment run their whole
// generated program to completion — status audited, never timeoutExhausted. The
// old base+fields×variants budget (38 for the monitor, 12 for the assignment)
// exhausted mid-program.
func TestUnit_Run_MonitorAndAssignmentCompleteWithoutExhaustion(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	_, summary := mustRun(t, strategyOptions(t, s, nil))

	for _, entity := range []string{"monitor", "assignment"} {
		got := entityStatus(t, summary, entity)
		if got.Status == StatusTimeoutExhausted {
			t.Errorf("%s = %+v, want audited (budget exhausted mid-program)", entity, got)
		}
		if got.Status != StatusAudited {
			t.Errorf("%s = %+v, want audited", entity, got)
		}
	}
	if summary.Requests > summary.RequestBudget {
		t.Errorf("run spent %d of a %d budget", summary.Requests, summary.RequestBudget)
	}
}

// noUpdateSpec is a full lifecycle without an update operation: get, post and
// delete only. A resource like this must not be probed for field updates —
// there is no method to send one with.
const noUpdateSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /things:
    get:
      operationId: listThings
      responses: {"200": {description: ok, content: {application/json: {schema: {type: array, items: {$ref: '#/components/schemas/Thing'}}}}}}
    post:
      operationId: createThing
      requestBody: {content: {application/json: {schema: {$ref: '#/components/schemas/ThingCreate'}}}}
      responses: {"201": {description: made, content: {application/json: {schema: {$ref: '#/components/schemas/Thing'}}}}}
  /things/{thingId}:
    parameters: [{name: thingId, in: path, schema: {type: string}}]
    get:
      operationId: getThing
      responses: {"200": {description: ok, content: {application/json: {schema: {$ref: '#/components/schemas/Thing'}}}}}
    delete:
      operationId: deleteThing
      responses: {"204": {description: gone}}
components:
  schemas:
    ThingCreate:
      type: object
      required: [name]
      properties:
        name: {type: string}
        mode: {type: string, enum: [basic, advanced]}
    Thing:
      allOf:
        - $ref: '#/components/schemas/ThingCreate'
        - {type: object, properties: {id: {type: string, readOnly: true}}}
`

// TestUnit_Run_NoUpdateOperationSkipsUpdateProbing: a resource with no update
// operation must run its whole program without issuing a method-less "update"
// (which defaults to GET and mislabels the read as an accepted-but-ignored
// update, then fails to commit for want of an excerpt method). The budget
// increase that lets such a resource reach its update steps is what first
// surfaced this — so the entity completes audited, every observation it emits is
// committable, and no immutable/ignored/update-style claim appears at all.
func TestUnit_Run_NoUpdateOperationSkipsUpdateProbing(t *testing.T) {
	t.Parallel()
	document, err := specmodel.Load([]byte(noUpdateSpec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	configuration := &config.Config{Audit: config.Audit{NamePrefix: "tfpfgen", MaxObjects: 25, RateLimitRPS: 2}}
	s := quirkserver.New(t, quirkserver.Quirks{})
	p, err := plan.Derive(document, configuration, nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	shrinkPolls(p)
	opts := testOptions(t, s, p, testEnv(), nil)
	opts.Doc = document
	opts.Config = configuration
	obs, summary := mustRun(t, opts)

	if got := entityStatus(t, summary, "thing"); got.Status != StatusAudited {
		t.Fatalf("thing = %+v, want audited", got)
	}
	for i := range obs {
		if err := obs[i].Validate(); err != nil {
			t.Errorf("observation %s.%s (%s) is not committable: %v", obs[i].Entity, obs[i].Attribute, obs[i].Kind, err)
		}
		switch obs[i].Kind {
		case observe.KindImmutable, observe.KindIgnoredOnUpdate, observe.KindUpdateStyle:
			t.Errorf("update-derived observation %s emitted for a resource with no update operation: %+v", obs[i].Kind, obs[i])
		}
	}
}

// TestUnit_Run_IdUnknownRecordsInconclusiveAndCleansByPrefix drives FIX 2's
// fallback: a create that answers 2xx with no id in any known shape and no
// Location header leaves the object addressable only by the cleanup prefix pass.
// The entity still completes (audited), its id-derived claims are recorded
// inconclusive rather than driven at an empty id, and the boundary prefix pass
// still deletes the object it created.
func TestUnit_Run_IdUnknownRecordsInconclusiveAndCleansByPrefix(t *testing.T) {
	t.Parallel()
	srv := newNoIDServer()
	s := httptest.NewServer(srv)
	t.Cleanup(s.Close)

	item := map[string]string{"widgetId": "$created:widget"}
	p := &plan.Plan{
		Entities: []plan.EntityPlan{{
			Entity: "widget",
			Role:   "resource",
			Budget: plan.Budget{Requests: 50},
			Steps: []plan.Step{
				{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/widgets",
					Body: map[string]any{"name": "tfpfgen-<runid>-widget-name"}},
				{Kind: plan.StepReadWithRetry, Method: "GET", Path: "/widgets/{widgetId}", PathValues: item,
					Poll: &plan.Poll{Interval: "10ms", Timeout: "200ms"}},
				{Kind: plan.StepReadConsecutive, Method: "GET", Path: "/widgets/{widgetId}", PathValues: item},
				{Kind: plan.StepUpdateField, Method: "PUT", Path: "/widgets/{widgetId}", PathValues: item,
					Attribute: "name", Body: map[string]any{"name": "tfpfgen-<runid>-widget-name-2"}},
				{Kind: plan.StepDeleteWithConfirmation, Method: "DELETE", Path: "/widgets/{widgetId}", PathValues: item},
				{Kind: plan.StepCleanupDelete, Method: "DELETE", Path: "/widgets/{widgetId}", PathValues: item},
			},
		}},
		Budget: plan.RunBudget{Requests: 200, Objects: 10, Duration: "1m"},
	}

	opts := Options{
		Plan:         p,
		BaseURL:      s.URL,
		Auth:         Auth{Method: config.AuthBearerToken},
		NamePrefix:   "tfpfgen",
		RateLimitRPS: 500,
		RunsDir:      t.TempDir(),
		SpecHash:     "testspechash",
		Logger:       zerolog.New(nil).Level(zerolog.Disabled),
		Lookup:       lookupOf(map[string]string{config.SecretToken: testToken}),
		RunID:        "testrun1",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	obs, summary, err := Run(ctx, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := entityStatus(t, summary, "widget")
	if got.Status != StatusAudited {
		t.Fatalf("widget = %+v, want audited despite the unlearnable id", got)
	}

	// The id-derived claims are inconclusive, not silently absent.
	for _, kind := range []observe.Kind{observe.KindReadAfterWrite, observe.KindImmutable, observe.KindDeleteNotFoundOK} {
		o := findObs(obs, "widget", "", kind)
		if kind == observe.KindImmutable {
			o = findObs(obs, "widget", "name", kind)
		}
		if o == nil || o.Outcome != observe.OutcomeInconclusive {
			t.Errorf("%s(widget) = %+v, want an inconclusive record", kind, o)
		}
	}

	// The prefix pass still leveled the tenant: nothing the run created survives.
	if n := srv.count(); n != 0 {
		t.Errorf("%d widget(s) survived the boundary prefix cleanup", n)
	}
}

// noIDServer is a stub collection whose create answers 2xx with no id and no
// Location, but which lists and deletes its objects by an id it assigns
// internally — so the prefix cleanup can find and remove them even though the
// create response never named one.
type noIDServer struct {
	mu      sync.Mutex
	seq     int
	objects map[string]map[string]any
}

func newNoIDServer() *noIDServer {
	return &noIDServer{objects: map[string]map[string]any{}}
}

func (n *noIDServer) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.objects)
}

func (n *noIDServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/widgets" {
		n.serveCollection(w, r)
		return
	}
	if id, ok := strings.CutPrefix(r.URL.Path, "/widgets/"); ok && id != "" {
		n.serveItem(w, r, id)
		return
	}
	http.NotFound(w, r)
}

func (n *noIDServer) serveCollection(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	switch r.Method {
	case http.MethodPost:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		n.seq++
		id := fmt.Sprintf("w%d", n.seq)
		body["id"] = id
		n.objects[id] = body
		// The create deliberately reveals no id and sets no Location.
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"created"}`))
	case http.MethodGet:
		list := make([]any, 0, len(n.objects))
		for _, o := range n.objects {
			list = append(list, o)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"widgets": list})
	default:
		http.Error(w, "no", http.StatusMethodNotAllowed)
	}
}

func (n *noIDServer) serveItem(w http.ResponseWriter, r *http.Request, id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	obj, ok := n.objects[id]
	switch r.Method {
	case http.MethodGet:
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(obj)
	case http.MethodDelete:
		if !ok {
			http.NotFound(w, r)
			return
		}
		delete(n.objects, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "no", http.StatusMethodNotAllowed)
	}
}
