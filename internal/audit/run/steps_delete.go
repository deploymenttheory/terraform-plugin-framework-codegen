package run

import (
	"context"
	"net/http"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/plan"
)

// runDeleteWithConfirmation deletes the minimal object, confirms it is
// gone, and deletes again. The second delete's 404 is the finding:
// generated delete logic should treat not-found as already done.
func (r *runner) runDeleteWithConfirmation(ctx context.Context, ent *entityState, step *plan.Step) error {
	entity := ent.plan.Entity
	obj := r.registry[entity]
	if obj == nil || obj.id == "" {
		return blockedError{reason: "no created object to delete"}
	}

	del, err := r.deleteObject(ctx, ent, ent.recipe, obj)
	if err != nil {
		return err
	}
	if del == nil || !del.ok() {
		status := 0
		if del != nil {
			status = del.status
		}
		r.record(entity, "", observe.KindDeleteNotFoundOK, nil, nil, observe.OutcomeInconclusive, excerptsOf(del)...)
		r.log.Warn().Str("entity", entity).Int("status", status).Msg("the delete did not succeed; the object stays in the ledger for cleanup")
		return nil
	}

	// The confirm and the second delete address the object by its learned
	// id, never through $created resolution — the registry entry is gone,
	// and resolving it would re-create the very object this step just
	// removed.
	values := itemValuesFor(ent.recipe, obj.id)
	confirm, err := r.do(ctx, ent, reqSpec{method: "GET", path: step.Path, pathValues: values})
	if err != nil {
		return err
	}
	if confirm.ok() {
		// Deleted with a success status and still readable: nothing about
		// the 404 semantics can be claimed, and that is itself worth the
		// excerpt.
		r.record(entity, "", observe.KindDeleteNotFoundOK, nil, nil, observe.OutcomeInconclusive, del.excerpt, confirm.excerpt)
		return nil
	}

	again, err := r.do(ctx, ent, reqSpec{method: step.Method, path: step.Path, pathValues: values})
	if err != nil {
		return err
	}
	switch again.status {
	case http.StatusNotFound:
		r.record(entity, "", observe.KindDeleteNotFoundOK, true, nil, observe.OutcomeConfirmed, again.excerpt)
	default:
		// A 2xx here means the API never answers 404 for a gone object; a
		// 4xx other than 404 means it answers something else. Either way
		// the specific claim — 404 is safe to treat as done — was not
		// observed.
		r.record(entity, "", observe.KindDeleteNotFoundOK, nil, nil, observe.OutcomeInconclusive, again.excerpt)
	}
	return nil
}

// runCleanupDelete ends the entity at zero live objects: every ledger
// entry of this entity still unresolved is deleted, newest first.
func (r *runner) runCleanupDelete(ctx context.Context, ent *entityState, step *plan.Step) error {
	for _, e := range r.ledger.unresolved() {
		if e.Entity != ent.plan.Entity || e.ID == "" {
			continue
		}
		obj := &createdObject{entity: e.Entity, id: e.ID, seq: e.Seq}
		if _, err := r.deleteObject(ctx, ent, ent.recipe, obj); err != nil {
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
