package quirkserver

// The v2 shape resources -- monitor, assignment and agent -- are the ground
// truth the v2 audit's harder machinery is asserted against. They are a
// different kind of fixture from the /things quirks: a quirk is one
// misbehaviour toggled on by a Quirks field, exercised in isolation; a shape
// resource is a whole small surface whose behaviour is *unconditional*, so the
// adaptive executor and the triangulating inference meet it exactly as they
// would meet a real vendor API.
//
//   - monitor is the multi-variant resource. A required `kind` discriminator
//     (ping | web | dns) gates which other fields are valid, which are
//     required, and one genuinely value-conditional co-requirement. Its spec
//     is an honest-but-partial oracle: it declares the fields flat and marks
//     `interval` optional, so the audit must *discover* both the variant
//     structure and that `interval` is really required.
//   - assignment is the reference-requiring resource. Its `agent_id` must be
//     the id of an object served by the separate /agents collection, so the
//     executor has to GET /agents and borrow a real id rather than synthesise
//     one.
//   - agent is the referenced collection: a small fixed set, list + get only.
//
// The exact 400 grammar these emit is documented on validateMonitor and
// validateAssignment; every sentence begins `field <name> ...` so a parser can
// pull the offending field out with one expression.

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// collection is an id-keyed store for one shape resource. It carries its own
// lock so the shape surfaces never contend with the /things state, and its own
// id sequence so ids are stable and human-legible ("monitor-1").
type collection struct {
	prefix string
	nextID int
	items  map[string]map[string]any
}

func newCollection(prefix string) *collection {
	return &collection{prefix: prefix, nextID: 1, items: map[string]map[string]any{}}
}

// create stores a new object under a freshly minted id and answers a copy of
// what was stored.
func (c *collection) create(body map[string]any) map[string]any {
	id := fmt.Sprintf("%s%d", c.prefix, c.nextID)
	c.nextID++
	return c.store(id, body)
}

// store writes body under id, stamping the id in, and answers a copy. It is
// the one place a shape object is persisted, so id-stamping never diverges.
func (c *collection) store(id string, body map[string]any) map[string]any {
	obj := cloneMap(body)
	obj["id"] = id
	c.items[id] = obj
	return cloneMap(obj)
}

func (c *collection) get(id string) (map[string]any, bool) {
	obj, ok := c.items[id]
	if !ok {
		return nil, false
	}
	return cloneMap(obj), true
}

func (c *collection) has(id string) bool {
	_, ok := c.items[id]
	return ok
}

func (c *collection) remove(id string) bool {
	if _, ok := c.items[id]; !ok {
		return false
	}
	delete(c.items, id)
	return true
}

// all answers every stored object, sorted by id so a list response is stable.
func (c *collection) all() []any {
	ids := make([]string, 0, len(c.items))
	for id := range c.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneMap(c.items[id]))
	}
	return out
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// seededAgents is the fixed set the /agents collection serves. assignment
// borrows one of these ids; a create naming anything else is refused. The
// names and regions are deliberately generic -- no value here is a claim about
// any real API.
var seededAgents = []struct {
	id     string
	fields map[string]any
}{
	{"agent-1", map[string]any{"name": "alpha", "region": "us-east"}},
	{"agent-2", map[string]any{"name": "bravo", "region": "eu-west"}},
	{"agent-3", map[string]any{"name": "charlie", "region": "ap-south"}},
}

// initShapes builds the shape stores and seeds the fixed agent set. Called by
// both constructors so the standalone process and the in-test server serve an
// identical surface.
func (s *Server) initShapes() {
	s.monitors = newCollection("monitor-")
	s.assignments = newCollection("assignment-")
	s.agents = newCollection("agent-")
	for _, a := range seededAgents {
		s.agents.store(a.id, a.fields)
	}
}

// routeShape dispatches a request the /things routes did not claim to one of
// the shape resources, answering whether it handled it. Every route holds the
// shared lock: the shape stores are plain maps, and the audit exercises them
// concurrently.
func (s *Server) routeShape(w http.ResponseWriter, r *http.Request, path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case path == "/monitors" && r.Method == http.MethodPost:
		s.shapeCreate(w, r, s.monitors, s.validateMonitor)
	case path == "/monitors" && r.Method == http.MethodGet:
		s.shapeList(w, s.monitors, "monitors")
	case strings.HasPrefix(path, "/monitors/") && r.Method == http.MethodGet:
		s.shapeRead(w, s.monitors, path, "/monitors/")
	case strings.HasPrefix(path, "/monitors/") &&
		(r.Method == http.MethodPut || r.Method == http.MethodPatch):
		s.shapeUpdate(w, r, s.monitors, path, "/monitors/", s.validateMonitor)
	case strings.HasPrefix(path, "/monitors/") && r.Method == http.MethodDelete:
		s.shapeDelete(w, s.monitors, path, "/monitors/")

	case path == "/assignments" && r.Method == http.MethodPost:
		s.shapeCreate(w, r, s.assignments, s.validateAssignment)
	case path == "/assignments" && r.Method == http.MethodGet:
		s.shapeList(w, s.assignments, "assignments")
	case strings.HasPrefix(path, "/assignments/") && r.Method == http.MethodGet:
		s.shapeRead(w, s.assignments, path, "/assignments/")
	case strings.HasPrefix(path, "/assignments/") &&
		(r.Method == http.MethodPut || r.Method == http.MethodPatch):
		s.shapeUpdate(w, r, s.assignments, path, "/assignments/", s.validateAssignment)
	case strings.HasPrefix(path, "/assignments/") && r.Method == http.MethodDelete:
		s.shapeDelete(w, s.assignments, path, "/assignments/")

	// agents are read-only: a fixed set to be listed and looked up, the pool
	// assignment.agent_id draws from.
	case path == "/agents" && r.Method == http.MethodGet:
		s.shapeList(w, s.agents, "agents")
	case strings.HasPrefix(path, "/agents/") && r.Method == http.MethodGet:
		s.shapeRead(w, s.agents, path, "/agents/")

	default:
		return false
	}
	return true
}

// validator inspects a create/update body and answers "" when it is
// acceptable, or a regular sentence naming the offending field otherwise. The
// sentence is the whole contract with the executor, so it is documented on
// each validator rather than here.
type validator func(body map[string]any) string

// shapeInvalid is the single 400 path every shape validator funnels through:
// the honest problem+json envelope the rest of the server already speaks, with
// the parseable sentence in `detail`.
func (s *Server) shapeInvalid(w http.ResponseWriter, detail string) {
	s.fail(w, http.StatusBadRequest, "invalid configuration", detail)
}

func (s *Server) shapeCreate(w http.ResponseWriter, r *http.Request, c *collection, validate validator) {
	body, err := readJSON(r)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "malformed body", "")
		return
	}
	if detail := validate(body); detail != "" {
		s.shapeInvalid(w, detail)
		return
	}
	writeJSON(w, http.StatusCreated, c.create(body))
}

func (s *Server) shapeList(w http.ResponseWriter, c *collection, key string) {
	writeJSON(w, http.StatusOK, map[string]any{key: c.all()})
}

func (s *Server) shapeRead(w http.ResponseWriter, c *collection, path, prefix string) {
	id := strings.TrimPrefix(path, prefix)
	obj, ok := c.get(id)
	if !ok {
		s.notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, obj)
}

func (s *Server) shapeUpdate(w http.ResponseWriter, r *http.Request, c *collection, path, prefix string, validate validator) {
	id := strings.TrimPrefix(path, prefix)

	body, err := readJSON(r)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "malformed body", "")
		return
	}
	if !c.has(id) {
		s.notFound(w)
		return
	}
	// Full-replace semantics: the body must be a complete, valid object, so
	// update re-runs exactly the create-time validation. An audit that learned
	// the shape from create meets the same rules on update.
	if detail := validate(body); detail != "" {
		s.shapeInvalid(w, detail)
		return
	}
	writeJSON(w, http.StatusOK, c.store(id, body))
}

func (s *Server) shapeDelete(w http.ResponseWriter, c *collection, path, prefix string) {
	id := strings.TrimPrefix(path, prefix)
	if !c.remove(id) {
		s.notFound(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
