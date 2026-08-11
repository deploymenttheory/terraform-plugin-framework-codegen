package quirkserver

import (
	"net/http"
	"strings"
	"testing"
)

// The shape resources are unconditional ground truth, so unlike the quirks
// they are not driven by a Quirks field and are not walked by
// EachQuirkIsExhibited. They get the same discipline in their own right: one
// table naming every 400 the multi-variant monitor can emit, asserting both
// the status and the exact detail sentence, because the executor parses those
// sentences and a fixture whose phrasing drifted would make the Wave 2/3 tests
// pass for the wrong reason.

func monitorsURL(s *Server) string    { return s.BaseURL() + "/monitors" }
func assignmentsURL(s *Server) string { return s.BaseURL() + "/assignments" }
func agentsURL(s *Server) string      { return s.BaseURL() + "/agents" }

// TestUnit_Quirkserver_Monitor_VariantGrammar exercises every refusal the
// multi-variant monitor makes, pinning the exact detail each carries: the
// discriminator, the learn-from-400 requirement, the wrong-kind conflict, the
// per-kind requirement, and the value-conditional dnssec co-requirement.
func TestUnit_Quirkserver_Monitor_VariantGrammar(t *testing.T) {
	t.Parallel()
	s := New(t, Quirks{})

	cases := []struct {
		name   string
		body   map[string]any
		status int
		detail string // the parseable problem+json detail; "" means expect 201
	}{
		{
			name:   "kind is the required discriminator",
			body:   map[string]any{"interval": 60},
			status: http.StatusBadRequest,
			detail: "field kind is required",
		},
		{
			name:   "kind is a closed set",
			body:   map[string]any{"kind": "traceroute", "interval": 60},
			status: http.StatusBadRequest,
			detail: "field kind must be one of ping, web, dns",
		},
		{
			name:   "interval is optional in the document but really required",
			body:   map[string]any{"kind": "ping", "target_host": "h"},
			status: http.StatusBadRequest,
			detail: "field interval is required",
		},
		{
			name:   "a field from another variant is a named conflict",
			body:   map[string]any{"kind": "web", "interval": 60, "domain": "example.test", "web": map[string]any{"url": "u"}},
			status: http.StatusBadRequest,
			detail: "field domain is not valid when kind=web",
		},
		{
			name:   "ping requires target_host",
			body:   map[string]any{"kind": "ping", "interval": 60},
			status: http.StatusBadRequest,
			detail: "field target_host is required when kind=ping",
		},
		{
			name:   "web requires the web block",
			body:   map[string]any{"kind": "web", "interval": 60},
			status: http.StatusBadRequest,
			detail: "field web is required when kind=web",
		},
		{
			name:   "web block requires a url",
			body:   map[string]any{"kind": "web", "interval": 60, "web": map[string]any{"method": "GET"}},
			status: http.StatusBadRequest,
			detail: "field web.url is required when kind=web",
		},
		{
			name:   "dns requires domain",
			body:   map[string]any{"kind": "dns", "interval": 60},
			status: http.StatusBadRequest,
			detail: "field domain is required when kind=dns",
		},
		{
			name:   "dnssec is not valid on a non-dns kind",
			body:   map[string]any{"kind": "ping", "interval": 60, "target_host": "h", "dnssec": true},
			status: http.StatusBadRequest,
			detail: "field dnssec is not valid when kind=ping",
		},
		{
			name:   "dnssec on dns needs domain set (the value-conditional edge)",
			body:   map[string]any{"kind": "dns", "interval": 60, "dnssec": true},
			status: http.StatusBadRequest,
			detail: "field dnssec requires field domain to be set",
		},
		{
			name:   "ping is accepted with its field",
			body:   map[string]any{"kind": "ping", "interval": 60, "target_host": "h"},
			status: http.StatusCreated,
		},
		{
			name:   "web is accepted with its block",
			body:   map[string]any{"kind": "web", "interval": 60, "web": map[string]any{"url": "https://example.test", "method": "GET"}},
			status: http.StatusCreated,
		},
		{
			name:   "dns is accepted, and dnssec is fine once domain is set",
			body:   map[string]any{"kind": "dns", "interval": 60, "domain": "example.test", "dnssec": true},
			status: http.StatusCreated,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := post(t, monitorsURL(s), tc.body)
			if status != tc.status {
				t.Fatalf("status = %d, want %d (%v)", status, tc.status, body)
			}
			if tc.detail == "" {
				return
			}
			if got, _ := body["detail"].(string); got != tc.detail {
				t.Errorf("detail = %q, want %q", got, tc.detail)
			}
		})
	}
}

// TestUnit_Quirkserver_Monitor_Lifecycle walks a monitor through the full
// CRUD the document declares, and holds update to the same variant rules as
// create.
func TestUnit_Quirkserver_Monitor_Lifecycle(t *testing.T) {
	t.Parallel()
	s := New(t, Quirks{})

	status, created := post(t, monitorsURL(s),
		map[string]any{"kind": "dns", "interval": 60, "domain": "example.test"})
	if status != http.StatusCreated {
		t.Fatalf("create = %d, want 201 (%v)", status, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id assigned: %v", created)
	}
	if created["domain"] != "example.test" {
		t.Errorf("create should echo what it stored: %v", created)
	}

	if status, read := get(t, monitorsURL(s)+"/"+id); status != http.StatusOK || read["domain"] != "example.test" {
		t.Fatalf("read = %d %v, want the stored monitor", status, read)
	}

	status, listed := get(t, monitorsURL(s))
	if status != http.StatusOK {
		t.Fatalf("list = %d", status)
	}
	if items, _ := listed["monitors"].([]any); len(items) != 1 {
		t.Errorf("list should hold one monitor: %v", listed)
	}

	// Update is full-replace and re-validated: an invalid body is refused.
	if status, _ := put(t, monitorsURL(s)+"/"+id, map[string]any{"kind": "dns", "domain": "d"}); status != http.StatusBadRequest {
		t.Errorf("an update omitting interval should 400 like create, got %d", status)
	}
	status, updated := put(t, monitorsURL(s)+"/"+id,
		map[string]any{"kind": "dns", "interval": 30, "domain": "changed.test"})
	if status != http.StatusOK || updated["domain"] != "changed.test" {
		t.Fatalf("update = %d %v, want the new domain", status, updated)
	}

	if status, _ := do(t, http.MethodDelete, monitorsURL(s)+"/"+id, nil); status != http.StatusNoContent {
		t.Errorf("delete = %d, want 204", status)
	}
	if status, _ := get(t, monitorsURL(s)+"/"+id); status != http.StatusNotFound {
		t.Errorf("a deleted monitor should be gone, got %d", status)
	}
}

// TestUnit_Quirkserver_Assignment_Reference is the reference-borrowing ground
// truth: a create must name an agent /agents actually serves.
func TestUnit_Quirkserver_Assignment_Reference(t *testing.T) {
	t.Parallel()
	s := New(t, Quirks{})

	// Absent reference, named.
	status, body := post(t, assignmentsURL(s), map[string]any{"name": "a"})
	if status != http.StatusBadRequest {
		t.Fatalf("a missing agent_id should 400, got %d", status)
	}
	if got, _ := body["detail"].(string); got != "field agent_id is required" {
		t.Errorf("detail = %q, want the missing-field sentence", got)
	}

	// A synthesised id is refused: the point is the executor cannot guess one.
	status, body = post(t, assignmentsURL(s), map[string]any{"name": "a", "agent_id": "agent-999"})
	if status != http.StatusBadRequest {
		t.Fatalf("a garbage agent_id should 400, got %d", status)
	}
	if got, _ := body["detail"].(string); got != "field agent_id must reference an existing agent" {
		t.Errorf("detail = %q, want the reference sentence", got)
	}

	// A real id borrowed from /agents succeeds — the executor's whole job here.
	_, listed := get(t, agentsURL(s))
	agents, _ := listed["agents"].([]any)
	first, _ := agents[0].(map[string]any)
	realID, _ := first["id"].(string)

	status, created := post(t, assignmentsURL(s), map[string]any{"name": "a", "agent_id": realID})
	if status != http.StatusCreated {
		t.Fatalf("a real agent_id should 201, got %d (%v)", status, created)
	}
	if created["agent_id"] != realID {
		t.Errorf("the assignment should carry the referenced id: %v", created)
	}
}

// TestUnit_Quirkserver_Assignment_Lifecycle walks the full CRUD.
func TestUnit_Quirkserver_Assignment_Lifecycle(t *testing.T) {
	t.Parallel()
	s := New(t, Quirks{})

	status, created := post(t, assignmentsURL(s), map[string]any{"name": "a", "agent_id": "agent-1"})
	if status != http.StatusCreated {
		t.Fatalf("create = %d, want 201 (%v)", status, created)
	}
	id, _ := created["id"].(string)

	if status, read := get(t, assignmentsURL(s)+"/"+id); status != http.StatusOK || read["agent_id"] != "agent-1" {
		t.Fatalf("read = %d %v", status, read)
	}
	if status, listed := get(t, assignmentsURL(s)); status != http.StatusOK || listed["assignments"] == nil {
		t.Fatalf("list = %d %v", status, listed)
	}
	// Update is re-validated: a bad reference is refused.
	if status, _ := put(t, assignmentsURL(s)+"/"+id, map[string]any{"name": "a", "agent_id": "agent-999"}); status != http.StatusBadRequest {
		t.Errorf("an update with a bad reference should 400, got %d", status)
	}
	if status, _ := put(t, assignmentsURL(s)+"/"+id, map[string]any{"name": "b", "agent_id": "agent-2"}); status != http.StatusOK {
		t.Errorf("a valid update should 200, got %d", status)
	}
	if status, _ := do(t, http.MethodDelete, assignmentsURL(s)+"/"+id, nil); status != http.StatusNoContent {
		t.Errorf("delete = %d, want 204", status)
	}
}

// TestUnit_Quirkserver_Agents_FixedPool asserts the referenced collection is a
// stable, readable set — what the reference borrowing depends on.
func TestUnit_Quirkserver_Agents_FixedPool(t *testing.T) {
	t.Parallel()
	s := New(t, Quirks{})

	status, listed := get(t, agentsURL(s))
	if status != http.StatusOK {
		t.Fatalf("list = %d, want 200", status)
	}
	agents, _ := listed["agents"].([]any)
	if len(agents) != len(seededAgents) {
		t.Fatalf("listed %d agents, want the fixed %d", len(agents), len(seededAgents))
	}

	// Every seeded id is individually readable.
	for _, a := range seededAgents {
		status, got := get(t, agentsURL(s)+"/"+a.id)
		if status != http.StatusOK {
			t.Errorf("get %s = %d, want 200", a.id, status)
		}
		if got["id"] != a.id {
			t.Errorf("get %s returned %v", a.id, got)
		}
	}

	// An unknown id is a 404, not an empty 200 — the executor can tell a real
	// id from a made-up one by reading it back.
	if status, _ := get(t, agentsURL(s)+"/agent-999"); status != http.StatusNotFound {
		t.Errorf("an unknown agent should 404, got %d", status)
	}

	// Agents are read-only: no create route.
	if status, _ := post(t, agentsURL(s), map[string]any{"name": "x"}); status != http.StatusMethodNotAllowed {
		t.Errorf("agents should reject a create, got %d", status)
	}
}

// TestUnit_Quirkserver_Shapes_ErrorPaths covers the mechanical failures every
// shape route shares: an absent id reads/updates/deletes as 404, and a
// malformed body is a 400 the same way the /things routes answer one.
func TestUnit_Quirkserver_Shapes_ErrorPaths(t *testing.T) {
	t.Parallel()
	s := New(t, Quirks{})

	// A missing item across read, update, delete.
	if status, _ := get(t, monitorsURL(s)+"/absent"); status != http.StatusNotFound {
		t.Errorf("read of an absent monitor = %d, want 404", status)
	}
	if status, _ := put(t, assignmentsURL(s)+"/absent", map[string]any{"name": "a", "agent_id": "agent-1"}); status != http.StatusNotFound {
		t.Errorf("update of an absent assignment = %d, want 404", status)
	}
	if status, _ := do(t, http.MethodDelete, monitorsURL(s)+"/absent", nil); status != http.StatusNotFound {
		t.Errorf("delete of an absent monitor = %d, want 404", status)
	}

	// A malformed body on create and on update.
	for _, url := range []string{monitorsURL(s), assignmentsURL(s)} {
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader("{not json")) //nolint:noctx // a test fixture
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("a malformed create at %s = %d, want 400", url, resp.StatusCode)
		}
	}

	// A malformed update against a real object, so the malformed-body branch is
	// reached rather than the not-found one.
	_, created := post(t, monitorsURL(s), map[string]any{"kind": "ping", "interval": 60, "target_host": "h"})
	id, _ := created["id"].(string)
	upReq, err := http.NewRequest(http.MethodPut, monitorsURL(s)+"/"+id, strings.NewReader("{not json")) //nolint:noctx // a test fixture
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	upResp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = upResp.Body.Close()
	if upResp.StatusCode != http.StatusBadRequest {
		t.Errorf("a malformed update = %d, want 400", upResp.StatusCode)
	}
}
