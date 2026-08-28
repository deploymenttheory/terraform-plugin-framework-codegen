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

// connectorSpec declares one enum for a connector's type — "generic" — under
// a collection whose path spells the value the API actually wants.
const connectorSpec = `openapi: 3.0.3
info:
  title: connector fixture
  version: "1.0"
paths:
  /connectors/panorama:
    get:
      operationId: listPanorama
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  connectors:
                    type: array
                    items:
                      $ref: '#/components/schemas/Connector'
    post:
      operationId: createPanorama
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Connector'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Connector'
  /connectors/panorama/{id}:
    parameters:
      - name: id
        in: path
        schema:
          type: string
    get:
      operationId: getPanorama
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Connector'
    delete:
      operationId: deletePanorama
      responses:
        "204":
          description: gone
components:
  schemas:
    Connector:
      type: object
      required: [type, name, target]
      properties:
        id:
          type: string
          readOnly: true
        type:
          type: string
          enum: [generic]
        name:
          type: string
        target:
          type: string
          example: https://panorama.example.invalid
`

// connectorAPI is a fake that refuses the documented type and takes its own
// collection name, phrasing the refusal the way a deserialiser does.
type connectorAPI struct {
	mu      sync.Mutex
	objects map[string]map[string]any
	next    int
}

func (a *connectorAPI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case req.Method == http.MethodGet && req.URL.Path == "/connectors/panorama":
		list := make([]any, 0, len(a.objects))
		for _, o := range a.objects {
			list = append(list, o)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"connectors": list})
	case req.Method == http.MethodPost && req.URL.Path == "/connectors/panorama":
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		kind, _ := body["type"].(string)
		if kind != "panorama" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": "JSON parse error: Could not resolve type id '" + kind + "' as a subtype of `PanoramaConnector`",
			})
			return
		}
		a.next++
		id := "c" + strings.Repeat("0", 3) + string(rune('0'+a.next))
		body["id"] = id
		a.objects[id] = body
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(body)
	case strings.HasPrefix(req.URL.Path, "/connectors/panorama/"):
		id := strings.TrimPrefix(req.URL.Path, "/connectors/panorama/")
		o, ok := a.objects[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if req.Method == http.MethodDelete {
			delete(a.objects, id)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(o)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestUnit_Adaptive_ARefusedDiscriminatorTakesTheEntitysOwnName(t *testing.T) {
	t.Parallel()
	api := &connectorAPI{objects: map[string]map[string]any{}}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)

	document, err := specmodel.Load([]byte(connectorSpec))
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
		t.Fatalf("Run: %v", err)
	}

	got := entityStatus(t, summary, "connectors_panorama")
	if got.Outcome == observe.OutcomeBlocked {
		t.Fatalf("the entity blocked; the collection name was never tried: %+v\n%s", got, logs.String())
	}
	values := findObs(obs, "connectors_panorama", "type", observe.KindValues)
	if values == nil {
		t.Fatalf("no values observation on type:\n%s", logs.String())
	}
	raw, _ := json.Marshal(values.Value)
	var record observe.Values
	_ = json.Unmarshal(raw, &record)
	if !contains(record.Accepted, "panorama") || !contains(record.Rejected, "generic") {
		t.Errorf("values = %+v, want panorama accepted and generic rejected", record)
	}
	if len(api.objects) != 0 {
		t.Errorf("objects remain after the run: %v", api.objects)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
