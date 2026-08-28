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

// vaultSpec declares a delete that confirms itself through a required query
// parameter whose default is the value the API refuses.
const vaultSpec = `openapi: 3.0.3
info:
  title: vault fixture
  version: "1.0"
paths:
  /vaults:
    get:
      operationId: listVaults
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  vaults:
                    type: array
                    items:
                      $ref: '#/components/schemas/Vault'
    post:
      operationId: createVault
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Vault'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Vault'
  /vaults/{id}:
    parameters:
      - name: id
        in: path
        schema:
          type: string
    get:
      operationId: getVault
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Vault'
    delete:
      operationId: deleteVault
      parameters:
        - name: confirmDisabledObjects
          in: query
          required: true
          schema:
            type: boolean
            default: false
          example: true
      responses:
        "204":
          description: gone
components:
  schemas:
    Vault:
      type: object
      required: [name]
      properties:
        id:
          type: string
          readOnly: true
        name:
          type: string
`

// vaultAPI refuses a delete that does not confirm itself.
type vaultAPI struct {
	mu      sync.Mutex
	objects map[string]map[string]any
	next    int
	refused int
}

func (a *vaultAPI) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case req.Method == http.MethodGet && req.URL.Path == "/vaults":
		list := make([]any, 0, len(a.objects))
		for _, o := range a.objects {
			list = append(list, o)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"vaults": list})
	case req.Method == http.MethodPost && req.URL.Path == "/vaults":
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		a.next++
		id := "v" + strings.Repeat("0", 2) + string(rune('0'+a.next))
		body["id"] = id
		a.objects[id] = body
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(body)
	case strings.HasPrefix(req.URL.Path, "/vaults/"):
		id := strings.TrimPrefix(req.URL.Path, "/vaults/")
		o, ok := a.objects[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if req.Method == http.MethodDelete {
			if req.URL.Query().Get("confirmDisabledObjects") != "true" {
				a.refused++
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "Required parameter 'confirmDisabledObjects' is not present."})
				return
			}
			delete(a.objects, id)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(o)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestUnit_Run_ADeleteSendsTheQueryParametersItRequires(t *testing.T) {
	t.Parallel()
	api := &vaultAPI{objects: map[string]map[string]any{}}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)

	document, err := specmodel.Load([]byte(vaultSpec))
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
	_, summary, err := Run(ctx, opts)
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, logs.String())
	}
	if got := entityStatus(t, summary, "vault"); got.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("vault = %+v, want audited", got)
	}
	if api.refused != 0 {
		t.Errorf("%d delete(s) were sent without the confirmation the document requires", api.refused)
	}
	if len(api.objects) != 0 {
		t.Errorf("objects remain after the run: %v", api.objects)
	}
}
