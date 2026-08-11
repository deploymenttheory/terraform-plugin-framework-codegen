package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/quirkserver"
)

// auditSpec is the smallest lifecycle the quirk server serves.
const auditSpec = `openapi: 3.0.3
info:
  title: Audit CLI fixture
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
    delete:
      operationId: deleteThing
      responses:
        "204":
          description: gone
components:
  schemas:
    ThingCreate:
      type: object
      required: [name]
      properties:
        name:
          type: string
        mode:
          type: string
          enum: [basic, advanced]
    Thing:
      allOf:
        - $ref: '#/components/schemas/ThingCreate'
        - type: object
          properties:
            id:
              type: string
              readOnly: true
`

const auditConfig = `version: 1
provider:
  name: fixture
  registry_namespace: deploymenttheory
generator:
  version: v1.0.0
spec:
  document_url: https://example.invalid/openapi.yaml
sdk:
  backend: kiota
  backend_version: "1.0.0"
auth:
  method: bearer_token
audit:
  rate_limit_rps: 500
`

// auditRepo lays out a provider-repo working directory: config, pinned
// spec, revised spec. Returns after chdir-ing into it.
func auditRepo(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)

	if err := os.WriteFile("tfpfgen.yaml", []byte(auditConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("spec", 0o750); err != nil {
		t.Fatal(err)
	}
	doc := []byte(auditSpec)
	if err := os.WriteFile(filepath.Join("spec", "upstream.yaml"), doc, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(doc)
	lock, _ := json.Marshal(map[string]any{
		"source":  "https://example.invalid/openapi.yaml",
		"sha256":  hex.EncodeToString(digest[:]),
		"format":  "yaml",
		"openapi": "3.0.3",
	})
	if err := os.WriteFile(filepath.Join("spec", "upstream.lock.json"), lock, 0o600); err != nil {
		t.Fatal(err)
	}
	// The revised spec is the upstream document verbatim: no corrections.
	if err := os.WriteFile(filepath.Join("spec", "revised.yaml"), doc, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUnit_AuditRun_FullRunWritesObservationsAndATable(t *testing.T) {
	s := quirkserver.New(t, quirkserver.Quirks{
		ClosedEnum: map[string][]string{"mode": {"basic", "advanced"}},
	})
	auditRepo(t)
	t.Setenv("TFPFGEN_AUTH_TOKEN", "cli-test-token-123456")

	code, stdout, stderr := run(t, "audit", "run", "--base-url", s.BaseURL())
	if code != ExitOK {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, want := range []string{
		"audit run", "audited", "observations:", "budget:", "cleanup:",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("summary table misses %q:\n%s", want, stdout)
		}
	}
	raw, err := os.ReadFile(filepath.Join("audit", "observations", "thing.observations.json"))
	if err != nil {
		t.Fatalf("no observation file: %v", err)
	}
	if !strings.Contains(string(raw), `"values"`) {
		t.Errorf("the closed enum did not surface in the committed file:\n%s", raw)
	}
	if strings.Contains(string(raw), "cli-test-token-123456") {
		t.Fatal("the committed observations carry the bearer token")
	}
	if left := s.Objects(); len(left) != 0 {
		t.Errorf("the run left objects: %v", left)
	}
}

func TestUnit_AuditRun_FallsBackToUpstreamWithoutRevised(t *testing.T) {
	s := quirkserver.New(t, quirkserver.Quirks{})
	auditRepo(t)
	// A first run: `spec revise` has not materialised revised.yaml yet, so
	// the audit interrogates against the pinned upstream document instead.
	if err := os.Remove(filepath.Join("spec", "revised.yaml")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TFPFGEN_AUTH_TOKEN", "cli-test-token-123456")

	code, stdout, stderr := run(t, "audit", "run", "--base-url", s.BaseURL())
	if code != ExitOK {
		t.Fatalf("the audit should fall back to the pinned upstream; exit = %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "audit run") {
		t.Errorf("no summary table:\n%s", stdout)
	}
}

func TestUnit_AuditRun_RefusesWithoutAnySpec(t *testing.T) {
	auditRepo(t)
	t.Setenv("TFPFGEN_AUTH_TOKEN", "cli-test-token-123456")
	for _, f := range []string{"revised.yaml", "upstream.yaml"} {
		if err := os.Remove(filepath.Join("spec", f)); err != nil {
			t.Fatal(err)
		}
	}
	code, _, stderr := run(t, "audit", "run", "--base-url", "https://api.example.invalid")
	if code != ExitFailure || !strings.Contains(stderr, "spec import") {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
}

func TestUnit_AuditRun_RefusesWithoutTheSecrets(t *testing.T) {
	auditRepo(t)
	t.Setenv("TFPFGEN_AUTH_TOKEN", "")
	code, _, stderr := run(t, "audit", "run", "--base-url", "https://api.example.invalid")
	if code != ExitFailure || !strings.Contains(stderr, "TFPFGEN_AUTH_TOKEN") {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
}

func TestUnit_AuditRun_NeedsABaseURLFromSomewhere(t *testing.T) {
	auditRepo(t)
	t.Setenv("TFPFGEN_AUTH_TOKEN", "cli-test-token-123456")
	code, _, stderr := run(t, "audit", "run")
	if code != ExitFailure || !strings.Contains(stderr, "--base-url") {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
}

func TestUnit_AuditCleanup_RemovesThePrefix(t *testing.T) {
	s := quirkserver.New(t, quirkserver.Quirks{})
	s.Seed(map[string]any{"name": "tfpfgen-old1-thing-name"})
	foreign := s.Seed(map[string]any{"name": "production"})
	auditRepo(t)
	t.Setenv("TFPFGEN_AUTH_TOKEN", "cli-test-token-123456")

	code, stdout, stderr := run(t, "audit", "cleanup", "--base-url", s.BaseURL())
	if code != ExitOK {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `deleted by prefix "tfpfgen"`) {
		t.Errorf("stdout does not report the cleanup pass: %s", stdout)
	}
	left := s.Objects()
	if len(left) != 1 {
		t.Fatalf("objects = %v, want only the foreign one", left)
	}
	if _, ok := left[foreign]; !ok {
		t.Fatal("the foreign object was deleted")
	}
}

// TestIntegration_AuditToRevise_UndocumentedFieldLandsInTheRevisedSpec is
// the undocumentedFieldInSpec loop end to end: the quirk server answers a
// "serial" field no schema in the fixture spec declares; the audit run
// observes it from the read-backs; spec revise compiles the observation
// into a proposed correction; accepting it and materializing produces a
// revised spec that declares the field with its observed type.
func TestIntegration_AuditToRevise_UndocumentedFieldLandsInTheRevisedSpec(t *testing.T) {
	s := quirkserver.New(t, quirkserver.Quirks{
		ConstantDefaults: map[string]any{"serial": "sn-100"},
	})
	auditRepo(t)
	t.Setenv("TFPFGEN_AUTH_TOKEN", "cli-test-token-123456")

	if code, stdout, stderr := run(t, "audit", "run", "--base-url", s.BaseURL()); code != ExitOK {
		t.Fatalf("audit run exit = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	raw, err := os.ReadFile(filepath.Join("audit", "observations", "thing.observations.json"))
	if err != nil {
		t.Fatalf("no observation file: %v", err)
	}
	if !strings.Contains(string(raw), `"undocumentedFieldInSpec"`) || !strings.Contains(string(raw), `"serial"`) {
		t.Fatalf("the run did not observe the undocumented serial field:\n%s", raw)
	}

	code, stdout, stderr := run(t, "spec", "revise", "--propose-only")
	if code != ExitOK {
		t.Fatalf("spec revise --propose-only exit = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "undocumentedFieldInSpec") {
		t.Fatalf("the proposal report does not name the kind:\n%s", stdout)
	}

	// Accept every proposal — the human decision, made by moving the files
	// — and re-revise until the loop converges. It takes two passes here by
	// design: only once the accepted correction declares the serial
	// property does the serverDefault observed on it have a place to land.
	proposedDir := filepath.Join("spec", "corrections", "proposed")
	converged := false
	for pass := 1; pass <= 5; pass++ {
		if code, _, _ := run(t, "spec", "revise"); code == ExitOK {
			converged = true
			break
		}
		entries, err := os.ReadDir(proposedDir)
		if err != nil || len(entries) == 0 {
			t.Fatalf("revision failed with nothing to accept: %v", err)
		}
		for _, e := range entries {
			// The pass prefix keeps a later pass's 001 from clobbering an
			// earlier accepted 001, and keeps application in acceptance
			// order.
			accepted := filepath.Join("spec", "corrections", fmt.Sprintf("pass%d-%s", pass, e.Name()))
			if err := os.Rename(filepath.Join(proposedDir, e.Name()), accepted); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !converged {
		t.Fatal("spec revise did not converge after accepting every proposal")
	}

	revised, err := os.ReadFile(filepath.Join("spec", "revised.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(revised, &doc); err != nil {
		t.Fatalf("revised.yaml is not usable YAML: %v", err)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	props := schemas["ThingCreate"].(map[string]any)["properties"].(map[string]any)
	serial, ok := props["serial"].(map[string]any)
	if !ok {
		t.Fatalf("the revised spec does not declare serial: %v", props)
	}
	if serial["type"] != "string" {
		t.Errorf("serial.type = %v, want string", serial["type"])
	}
}

func TestUnit_Audit_UsageListsBothVerbs(t *testing.T) {
	code, stdout, _ := run(t, "audit")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"run", "cleanup", "live API"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("audit usage misses %q: %s", want, stdout)
		}
	}
}
