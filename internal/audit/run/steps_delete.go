package run

import (
	"context"
	"net/http"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
)

// runDeleteWithConfirmation deletes the minimal object, confirms it is
// gone, and deletes again. The second delete's 404 is the finding:
// generated delete logic should treat not-found as already done.
func (r *runner) runDeleteWithConfirmation(ctx context.Context, entity *entityState, step *plan.Step) error {
	entityKey := entity.plan.Entity
	obj := r.createdObjects[entityKey]
	if obj != nil && obj.id == "" {
		// The object was created but its id was never learned, so it cannot be
		// deleted by id here — the boundary prefix pass is what removes it. The
		// 404-on-second-delete claim is inconclusive rather than a blocked
		// entity.
		r.record(entityKey, "", observe.KindDeleteNotFoundOK, nil, nil, observe.OutcomeInconclusive)
		return nil
	}
	if obj == nil {
		return blockedError{reason: "no created object to delete"}
	}

	del, err := r.deleteObject(ctx, entity, entity.recipe, obj)
	if err != nil {
		return err
	}
	if del == nil || !del.ok() {
		status := 0
		if del != nil {
			status = del.status
		}
		r.record(entityKey, "", observe.KindDeleteNotFoundOK, nil, nil, observe.OutcomeInconclusive, excerptsOf(del)...)
		r.log.Warn().Str("entity", entityKey).Int("status", status).Msg("the delete did not succeed; the object stays in the ledger for cleanup")
		return nil
	}

	// The confirm and the second delete address the object by its learned
	// id, never through $created resolution — the registry entry is gone,
	// and resolving it would re-create the very object this step just
	// removed.
	values := itemValuesFor(entity.recipe, obj.id)
	confirm, err := r.do(ctx, entity, reqSpec{method: "GET", path: step.Path, pathValues: values})
	if err != nil {
		return err
	}
	if confirm.ok() {
		// Deleted with a success status and still readable: nothing about
		// the 404 semantics can be claimed, and that is itself worth the
		// excerpt.
		r.record(entityKey, "", observe.KindDeleteNotFoundOK, nil, nil, observe.OutcomeInconclusive, del.excerpt, confirm.excerpt)
		return nil
	}

	again, err := r.do(ctx, entity, reqSpec{method: step.Method, path: step.Path, pathValues: values})
	if err != nil {
		return err
	}
	switch again.status {
	case http.StatusNotFound:
		r.record(entityKey, "", observe.KindDeleteNotFoundOK, true, nil, observe.OutcomeConfirmed, again.excerpt)
	default:
		// A 2xx here means the API never answers 404 for a gone object; a
		// 4xx other than 404 means it answers something else. Either way
		// the specific claim — 404 is safe to treat as done — was not
		// observed.
		r.record(entityKey, "", observe.KindDeleteNotFoundOK, nil, nil, observe.OutcomeInconclusive, again.excerpt)
	}
	return nil
}

// runCleanupDelete ends the entityKey at zero live objects: every ledger
// entry of this entityKey still unresolved is deleted, newest first.
func (r *runner) runCleanupDelete(ctx context.Context, entity *entityState, step *plan.Step) error {
	for _, e := range r.ledger.unresolved() {
		if e.Entity != entity.plan.Entity || e.ID == "" {
			continue
		}
		obj := &createdObject{entity: e.Entity, id: e.ID, seq: e.Seq}
		if _, err := r.deleteObject(ctx, entity, entity.recipe, obj); err != nil {
			return err
		}
	}
	_ = step
	return nil
}

func excerptsOf(res *httpResult) []observe.Excerpt {
	if res == nil {
		return nil
	}
	return []observe.Excerpt{res.excerpt}
}
