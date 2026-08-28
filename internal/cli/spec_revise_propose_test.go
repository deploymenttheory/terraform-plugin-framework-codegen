package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/store"
)

// thingSpec is one classifiable entity: enough lifecycle for a resource, so
// observations about it have somewhere to land.
const thingSpec = `openapi: 3.0.3
info:
  title: sample
  version: 1.2.3
paths:
  /things:
    post:
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Thing'
      responses:
        "201":
          description: created
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
  /things/{thingId}:
    get:
      responses:
        "200":
          description: read
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
    delete:
      responses:
        "204":
          description: deleted
components:
  schemas:
    Thing:
      type: object
      properties:
        name:
          type: string
`

// observedTree pins thingSpec into <root>/spec and commits two confirmed
// observations beside it, returning the spec directory.
func observedTree(t *testing.T) (root, specDir string) {
	t.Helper()
	root = t.TempDir()
	specDir = filepath.Join(root, "spec")
	res, err := store.Import(specDir, []byte(thingSpec), "published.yaml")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	obs := []observe.Observation{
		{
			Entity: "thing", Attribute: "name", Kind: observe.KindRequiredByAPI, Value: true,
			Outcome: observe.OutcomeConfirmed, RunID: "run1", SpecHash: res.Lock.SHA256,
		},
		{
			Entity: "thing", Kind: observe.KindDeleteNotFoundOK, Value: true,
			Outcome: observe.OutcomeConfirmed, RunID: "run1", SpecHash: res.Lock.SHA256,
		},
	}
	if err := observe.Write(filepath.Join(root, "audit", "observations"), obs); err != nil {
		t.Fatalf("observe.Write: %v", err)
	}
	return root, specDir
}

func TestUnit_SpecRevise_ProposeOnlyCompilesAndStops(t *testing.T) {
	_, specDir := observedTree(t)

	code, stdout, stderr := run(t, "spec", "revise", "--dir", specDir, "--propose-only")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitOK, stderr)
	}
	for _, want := range []string{
		"compiled 2 observations: 2 proposed, 0 auto-accepted, 0 suppressed by rejection markers, 0 stale",
		"proposed 001-thing.correction.json — thing (deleteNotFoundOK)",
		"proposed 002-thing.correction.json — thing.name (requiredByAPI)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "wrote") {
		t.Errorf("propose-only must stop before materializing:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(specDir, "revised.yaml")); !os.IsNotExist(err) {
		t.Errorf("revised.yaml exists after --propose-only (stat: %v)", err)
	}
}

func TestUnit_SpecRevise_FreshObservationsProposeThenFailTheGate(t *testing.T) {
	_, specDir := observedTree(t)

	code, stdout, stderr := run(t, "spec", "revise", "--dir", specDir)
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d (stdout: %s)", code, ExitFailure, stdout)
	}
	if !strings.Contains(stdout, "2 proposed") {
		t.Errorf("stdout does not summarise the compile:\n%s", stdout)
	}
	for _, want := range []string{"001-thing.correction.json", "002-thing.correction.json", "await a decision"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestUnit_SpecRevise_AutoAcceptedKindsMaterializeInOneRun(t *testing.T) {
	root, specDir := observedTree(t)
	configuration := filepath.Join(root, "tfpfgen.yaml")
	writeUnder(t, configuration, `version: 1
provider:
  name: example
  registry_namespace: example-org
generator:
  version: v0.1.0
spec:
  document_url: https://example.com/openapi.yaml
sdk:
  backend: kiota
  backend_version: "1.0.0"
auth:
  method: bearer_token
audit:
  auto_accept:
    - requiredByAPI
    - deleteNotFoundOK
`)

	code, stdout, stderr := run(t, "spec", "revise", "--dir", specDir, "--config", configuration)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitOK, stderr)
	}
	for _, want := range []string{
		"compiled 2 observations: 0 proposed, 2 auto-accepted",
		"auto-accepted auto-001-thing.correction.json — thing (deleteNotFoundOK)",
		"auto-accepted auto-002-thing.correction.json — thing.name (requiredByAPI)",
		"2 corrections applied",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(specDir, "revised.yaml")); err != nil {
		t.Errorf("revised.yaml was not written: %v", err)
	}
}

func TestUnit_SpecRevise_RefusesAnExplicitConfigThatDoesNotExist(t *testing.T) {
	_, specDir := observedTree(t)

	code, _, stderr := run(t, "spec", "revise", "--dir", specDir, "--config", filepath.Join(t.TempDir(), "nope.yaml"))
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "does not exist") {
		t.Fatalf("stderr does not name the missing config: %q", stderr)
	}
}

func TestUnit_SpecRevise_RefusesAnUnknownAutoAcceptKindFromConfig(t *testing.T) {
	root, specDir := observedTree(t)
	configuration := filepath.Join(root, "tfpfgen.yaml")
	writeUnder(t, configuration, `version: 1
provider:
  name: example
  registry_namespace: example-org
generator:
  version: v0.1.0
spec:
  document_url: https://example.com/openapi.yaml
sdk:
  backend: kiota
  backend_version: "1.0.0"
auth:
  method: bearer_token
audit:
  auto_accept:
    - deleteNotFound
`)

	code, _, stderr := run(t, "spec", "revise", "--dir", specDir, "--config", configuration)
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, `"deleteNotFound" is not an observation kind`) {
		t.Fatalf("stderr does not refuse the unknown kind: %q", stderr)
	}
}
