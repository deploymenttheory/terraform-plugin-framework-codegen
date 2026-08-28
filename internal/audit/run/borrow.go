package run

// Reference borrowing is how the executor satisfies a field whose value must
// be the id of a live object in another collection — the assignment.agent_id
// case, where a synthesised id is refused by construction and the only valid
// value is one the API itself already serves. The refusal names the
// collection; borrow reads it once, takes a real id, and caches it for the
// rest of the run so a second create needing the same reference costs no extra
// request.
//
// It is strictly read-only: it lists another collection and never mutates it.
// The GET goes through the same paced, redacted, logged path every other
// request does.

import (
	"context"
	"strings"
)

// borrow returns a real id from the named collection, reading it live the
// first time and serving it from the run cache thereafter. The second return
// is false when the collection is empty or unreadable — in which case the
// field it would have filled stays unsatisfiable and the caller records the
// attempt as inconclusive rather than guessing.
func (r *runner) borrow(ctx context.Context, entity *entityState, collection string) (string, bool) {
	if id, ok := r.borrowed[collection]; ok {
		return id, true
	}
	for _, path := range collectionPaths(collection) {
		if id, ok := r.borrowFromPath(ctx, entity, path); ok {
			r.borrowed[collection] = id
			return id, true
		}
	}
	return "", false
}

// borrowFromPath reads one collection path and answers the first identifier
// it serves, cached per path for the rest of the run. False when the
// collection is empty or unreadable.
func (r *runner) borrowFromPath(ctx context.Context, entity *entityState, path string) (string, bool) {
	if id, ok := r.borrowed[path]; ok {
		return id, true
	}
	res, err := r.do(ctx, entity, reqSpec{method: "GET", path: path})
	if err != nil || !res.ok() {
		return "", false
	}
	for _, item := range items(res.body) {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if id := identifierOf(obj); id != "" {
			r.borrowed[path] = id
			return id, true
		}
	}
	return "", false
}

// collectionPaths turns the noun a refusal named into the candidate paths its
// collection might live at. A refusal says "an existing agent"; the collection
// is almost always the plural at the root, but the singular is tried too so a
// singular-collection API is not missed.
func collectionPaths(noun string) []string {
	noun = strings.ToLower(noun)
	paths := []string{"/" + noun}
	if !strings.HasSuffix(noun, "s") {
		paths = append(paths, "/"+noun+"s")
	}
	return paths
}
