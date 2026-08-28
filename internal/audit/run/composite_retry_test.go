package run

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// labelSpec declares a label whose filter elements have no required member,
// beside a windows collection that references a tests collection nothing
// serves.
const labelSpec = `openapi: 3.0.3
info:
  title: label fixture
  version: "1.0"
paths:
  /labels:
    get:
      operationId: listLabels
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  labels:
                    type: array
                    items:
                      $ref: '#/components/schemas/Label'
    post:
      operationId: createLabel
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Label'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Label'
  /labels/{id}:
    parameters:
      - name: id
        in: path
        schema:
          type: string
    get:
      operationId: getLabel
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Label'
    delete:
      operationId: deleteLabel
      responses:
        "204":
          description: gone
  /tests:
    get:
      operationId: listTests
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  tests:
                    type: array
                    items:
                      type: object
                      properties:
                        testId:
                          type: string
  /windows:
    get:
      operationId: listWindows
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  windows:
                    type: array
                    items:
                      $ref: '#/components/schemas/Window'
    post:
      operationId: createWindow
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Window'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Window'
  /windows/{id}:
    parameters:
      - name: id
        in: path
        schema:
          type: string
    get:
      operationId: getWindow
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Window'
    delete:
      operationId: deleteWindow
      responses:
        "204":
          description: gone
components:
  schemas:
    Label:
      type: object
      required: [name, filters]
      properties:
        id:
          type: string
          readOnly: true
        name:
          type: string
        filters:
          type: array
          items:
            type: object
            properties:
              key:
                type: string
                enum: [network, agent]
              values:
                type: array
                example: ["10.1.1.0/24"]
                items:
                  type: string
    Window:
      type: object
      required: [name]
      properties:
        id:
          type: string
          readOnly: true
        name:
          type: string
        testIds:
          type: array
          example: ["281474976710706"]
          items:
            type: string
`

// labelAPI refuses a filter element carrying no key, in the words of a
// framework that names nothing, and serves no tests at all.
type labelAPI struct {
	mu      sync.Mutex
	objects map[string]map[string]any
	next    int
	// windowBodies records every window create body the API took.
	windowBodies []map[string]any
}

func (a *labelAPI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	collection := strings.Trim(req.URL.Path, "/")
	parts := strings.SplitN(collection, "/", 2)
	switch {
	case req.Method == http.MethodGet && collection == "tests":
		_ = json.NewEncoder(w).Encode(map[string]any{"tests": []any{}})
	case req.Method == http.MethodGet && len(parts) == 1:
		list := make([]any, 0)
		for _, o := range a.objects {
			if o["_collection"] == parts[0] {
				list = append(list, o)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{parts[0]: list})
	case req.Method == http.MethodPost && len(parts) == 1:
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		if parts[0] == "labels" {
			filters, _ := body["filters"].([]any)
			for _, f := range filters {
				if element, ok := f.(map[string]any); ok {
					if _, has := element["key"]; !has {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]any{"title": "Request validation failed", "detail": "Required request body is missing mandatory field"})
						return
					}
				}
			}
		}
		if parts[0] == "windows" {
			a.windowBodies = append(a.windowBodies, body)
		}
		a.next++
		id := "o" + strings.Repeat("0", 3-len(string(rune('0'+a.next)))) + string(rune('0'+a.next))
		body["id"] = id
		body["_collection"] = parts[0]
		a.objects[id] = body
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(body)
	case len(parts) == 2:
		o, ok := a.objects[parts[1]]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if req.Method == http.MethodDelete {
			delete(a.objects, parts[1])
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(o)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func labelRun(t *testing.T) (*labelAPI, []observe.Observation, Summary) {
	t.Helper()
	api := &labelAPI{objects: map[string]map[string]any{}}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)

	document, err := specmodel.Load([]byte(labelSpec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	configuration := strategyConfig()
	p, err := plan.Derive(document, configuration, nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	shrinkPolls(p)
	var logs bytes.Buffer
	opts := Options{
		Plan: p, Doc: document, Config: configuration,
		BaseURL:      server.URL,
		Auth:         Auth{Method: config.AuthBearerToken},
		NamePrefix:   "tfpfgen",
		RateLimitRPS: 500,
		RunsDir:      t.TempDir(),
		SpecHash:     "testspechash",
		Logger:       zerolog.New(&logs).Level(zerolog.DebugLevel),
		Lookup:       lookupOf(testEnv()),
		RunID:        "testrun1",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	obs, summary, err := Run(ctx, opts)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, logs.String())
	}
	return api, obs, summary
}

func TestUnit_Adaptive_ARefusedElementIsWidenedToTheDocumentedMembers(t *testing.T) {
	t.Parallel()
	_, _, summary := labelRun(t)
	got := entityStatus(t, summary, "label")
	if got.Outcome == observe.OutcomeBlocked {
		t.Fatalf("label blocked; the filter element was never widened: %+v", got)
	}
	var minimal map[string]any
	for _, rb := range summary.RequestBodies {
		if rb.Entity == "label" && rb.Minimal != nil {
			minimal = rb.Minimal.Request
		}
	}
	filters, _ := minimal["filters"].([]any)
	if len(filters) != 1 {
		t.Fatalf("accepted minimal filters = %#v, want one element", minimal["filters"])
	}
	if element, _ := filters[0].(map[string]any); element["key"] != "network" {
		t.Errorf("accepted element = %#v, want the documented members", element)
	}
}

func TestUnit_Adaptive_AnOptionalReferenceNothingServesIsLeftOut(t *testing.T) {
	t.Parallel()
	api, _, summary := labelRun(t)
	got := entityStatus(t, summary, "window")
	if got.Outcome == observe.OutcomeBlocked {
		t.Fatalf("window blocked on an optional reference: %+v", got)
	}
	if len(api.windowBodies) == 0 {
		t.Fatal("no window was created")
	}
	for _, body := range api.windowBodies {
		if ids, present := body["testIds"]; present {
			if list, _ := ids.([]any); len(list) != 0 {
				t.Errorf("a window create carried an unsatisfiable reference: %#v", ids)
			}
		}
	}
}
