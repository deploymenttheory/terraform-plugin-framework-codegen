package testapiserver

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	if !s.quirks.IgnoresUnknownQueryParams {
		if bad := s.badQueryParameter(r); bad != "" {
			s.fail(w, http.StatusBadRequest, "unknown query parameter", bad)
			return
		}
	}
	if bad := s.badTypedParameter(r); bad != "" {
		s.fail(w, http.StatusBadRequest, "invalid value for query parameter", bad)
		return
	}

	s.mu.Lock()
	ids := make([]string, 0, len(s.objects))
	for id := range s.objects {
		ids = append(ids, id)
	}
	s.mu.Unlock()

	sort.Strings(ids)

	items := make([]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, s.project(id, r))
	}

	writeJSON(w, http.StatusOK, map[string]any{listWrapperKey: items})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	body, err := readJSON(r)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "malformed body", "")
		return
	}

	if missing := s.missingRequired(body); missing != "" {
		// Naming the field is what lets the auditor write the requirement down
		// as observed rather than guessed: an error that does not name it
		// could have been about anything.
		s.fail(w, http.StatusBadRequest, "missing required field", s.refusedFieldDetail(missing))
		return
	}

	if field, value := s.rejectedEnumValue(body); field != "" {
		s.fail(w, http.StatusBadRequest, "invalid value for "+field, fmt.Sprint(value))
		return
	}

	s.mu.Lock()

	id := strconv.Itoa(s.nextID)
	s.nextID++

	obj := map[string]any{"id": id}
	for k, v := range body {
		if contains(s.quirks.SilentlyDiscards, k) {
			// Accepted and thrown away. The response says 201 and the object
			// never has it.
			continue
		}
		if contains(s.quirks.NullsInWriteResponse, k) {
			// Accepted, stored nowhere, and answered as explicit null by
			// project.
			continue
		}
		if dw := s.quirks.DiscardsWhen; dw != nil && k == dw.Then &&
			fmt.Sprint(body[dw.WhenField]) == fmt.Sprint(dw.WhenValue) {
			// The conditional variant: dropped on this branch, stored on every
			// other.
			continue
		}
		if sw := s.quirks.SuppressWhenSibling; sw != nil && k == sw.Then &&
			fmt.Sprint(body[sw.WhenField]) == fmt.Sprint(sw.WhenValue) {
			// The field interaction: fine alone, stripped when the sibling
			// rides along.
			continue
		}
		if forced, ok := s.quirks.Forces[k]; ok {
			// Whatever was sent, the server's own value wins.
			obj[k] = forced
			continue
		}
		obj[k] = s.normalise(k, v)
	}

	s.applyDefaults(obj, body)
	s.applySideEffects(obj, body)

	s.objects[id] = obj
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, s.project(id, r))
}

func (s *Server) read(w http.ResponseWriter, r *http.Request, path string) {
	id := strings.TrimPrefix(path, itemPrefix)

	s.mu.Lock()
	_, exists := s.objects[id]
	s.reads[id]++
	seen := s.reads[id]
	s.mu.Unlock()

	// Eventual consistency: the object exists but the first N reads deny it.
	if exists && seen <= s.quirks.EventuallyConsistentReads {
		s.notFound(w)
		return
	}

	if !exists {
		s.notFound(w)
		return
	}

	writeJSON(w, http.StatusOK, s.project(id, r))
}

func (s *Server) update(w http.ResponseWriter, r *http.Request, path string) {
	id := strings.TrimPrefix(path, itemPrefix)

	body, err := readJSON(r)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "malformed body", "")
		return
	}

	s.mu.Lock()
	current, exists := s.objects[id]
	s.mu.Unlock()

	if !exists {
		s.notFound(w)
		return
	}

	// Checked before immutability, deliberately: this is the quirk that makes
	// a naive audit read a 4xx as immutability when the request shape was
	// simply wrong.
	if f := s.quirks.RequiresExtraFieldOnUpdate; f != "" {
		if _, ok := body[f]; !ok {
			s.fail(w, http.StatusBadRequest, "missing required field", f)
			return
		}
	}

	for _, field := range s.quirks.ImmutableAfterCreate {
		sent, ok := body[field]
		if !ok {
			continue
		}
		if !equalJSON(sent, current[field]) {
			s.fail(w, http.StatusBadRequest, "field cannot be modified after creation", field)
			return
		}
	}

	s.mu.Lock()

	updated := map[string]any{"id": id}
	if !s.quirks.PutClearsOmitted {
		// Everything not mentioned survives.
		for k, v := range current {
			updated[k] = v
		}
	}
	for k, v := range body {
		if contains(s.quirks.SilentlyDiscards, k) {
			continue
		}
		if contains(s.quirks.NullsInWriteResponse, k) {
			continue
		}
		// Accepted, answered 2xx, and not applied. Distinct from
		// SilentlyDiscards, which never stores the field at all: here a create
		// stored it and the update is the thing that quietly does nothing.
		if contains(s.quirks.SilentlyDiscardsOnUpdate, k) {
			continue
		}
		if sw := s.quirks.SuppressWhenSibling; sw != nil && k == sw.Then &&
			fmt.Sprint(body[sw.WhenField]) == fmt.Sprint(sw.WhenValue) {
			// The interaction holds on update too: carrying the create-stored
			// value through would hide the suppression from a PUT-based audit.
			delete(updated, k)
			continue
		}
		if forced, ok := s.quirks.Forces[k]; ok {
			updated[k] = forced
			continue
		}
		updated[k] = s.normalise(k, v)
	}

	// An omitted field reverts to the update-path constant, whatever the
	// object held -- after the body loop, so sending the field still wins the
	// usual way.
	for k, v := range s.quirks.UpdateDefaults {
		if _, sent := body[k]; !sent {
			updated[k] = v
		}
	}

	s.applySideEffects(updated, body)

	s.objects[id] = updated
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, s.project(id, r))
}

func (s *Server) delete(w http.ResponseWriter, _ *http.Request, path string) {
	id := strings.TrimPrefix(path, itemPrefix)

	s.mu.Lock()
	s.deletes++
	attempt := s.deletes
	s.mu.Unlock()

	if s.quirks.DeleteFails {
		s.fail(w, http.StatusInternalServerError, "delete failed", id)
		return
	}
	if n := s.quirks.DeleteFlakyEvery; n > 0 && attempt%n == 0 {
		s.fail(w, http.StatusInternalServerError, "delete failed", id)
		return
	}

	s.mu.Lock()
	_, exists := s.objects[id]
	delete(s.objects, id)
	s.mu.Unlock()

	if !exists {
		s.notFound(w)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// project renders an object as the API would return it, applying the quirks
// that affect what a read sees.
func (s *Server) project(id string, r *http.Request) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()

	obj := s.objects[id]
	out := make(map[string]any, len(obj))

	for k, v := range obj {
		// Expansion-gated fields are withheld unless asked for.
		if want, gated := s.quirks.ExpansionGated[k]; gated {
			if !expansionRequested(r, want) {
				continue
			}
		}
		out[k] = v
	}

	// Volatile fields differ on every read, which is what makes a perpetual
	// diff.
	for _, field := range s.quirks.VolatileFields {
		s.counter++
		out[field] = fmt.Sprintf("v%d", s.counter)
	}

	// Explicit null, everywhere the object is rendered: present-and-null is
	// the observation this quirk exists to produce.
	for _, field := range s.quirks.NullsInWriteResponse {
		out[field] = nil
	}

	return out
}

func expansionRequested(r *http.Request, want string) bool {
	if r == nil {
		return false
	}
	for _, v := range r.URL.Query()["expand"] {
		if v == want {
			return true
		}
	}
	return false
}
