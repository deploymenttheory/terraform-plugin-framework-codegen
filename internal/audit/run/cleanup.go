package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CleanupSummary is what a cleanup pass did. Orphans non-empty means live
// objects may remain in the tenant, and a human has to look.
type CleanupSummary struct {
	// LedgerDeletes removed objects a ledger knew the id of; a
	// PrefixDelete means something got past a ledger, which is worth
	// distinguishing.
	LedgerDeletes int      `json:"ledgerDeletes"`
	PrefixDeletes int      `json:"prefixDeletes"`
	Orphans       []string `json:"orphans,omitempty"`
}

// Cleanup clears the tenant of audit debris: every unresolved entry of
// every activity ledger in the runs directory is deleted by id, then every
// resource collection the plan names is read and every object carrying
// the name prefix is deleted. Standalone counterpart of the cleanup passes a run
// performs at both of its boundaries; `tfpfgen audit cleanup` calls it.
//
// Deletes are mutating requests, so the host allowlist applies here
// exactly as in a run.
func Cleanup(ctx context.Context, opts Options) (CleanupSummary, error) {
	r, err := newRunner(opts)
	if err != nil {
		return CleanupSummary{}, err
	}
	defer r.ledger.close()
	r.inCleanup = true
	summary := r.cleanupDebris(ctx)
	r.ledger.remove()
	if len(summary.Orphans) > 0 {
		return summary, fmt.Errorf("audit cleanup: %d object(s) could not be removed; see the orphan list", len(summary.Orphans))
	}
	return summary, nil
}

// cleanupDebris is the shared removal pass: prior activity ledgers first (delete by id, newest
// first), then the prefix pass over every reachable collection. At the
// start of a run it must not fail the run — leftovers are reported and
// the run proceeds on a tenant it has done its best to level.
func (r *runner) cleanupDebris(ctx context.Context) CleanupSummary {
	wasCleanup := r.inCleanup
	r.inCleanup = true
	defer func() { r.inCleanup = wasCleanup }()

	var summary CleanupSummary
	r.cleanupLedgers(ctx, &summary)
	r.cleanupByPrefix(ctx, &summary)
	return summary
}

// cleanupOwn is the end-of-run pass: this run's own ledger, newest first
// so children go before the parents whose paths embed them, then the
// prefix pass for anything that got past it.
func (r *runner) cleanupOwn(ctx context.Context) CleanupSummary {
	var summary CleanupSummary
	for _, e := range r.ledger.unresolved() {
		r.cleanupLedgerEntry(ctx, e, &summary)
	}
	r.cleanupByPrefix(ctx, &summary)
	for _, e := range r.ledger.unresolved() {
		summary.Orphans = append(summary.Orphans, orphanLine(e))
	}
	r.ledger.remove()
	return summary
}

// cleanupLedgers replays every previous run's activity ledger in the runs
// directory: unresolved entries with ids are deleted, and a file whose
// entries all reconcile is removed. A file that still names possible
// objects is kept — it is the only record of them.
func (r *runner) cleanupLedgers(ctx context.Context, summary *CleanupSummary) {
	if r.opts.RunsDir == "" {
		return
	}
	entries, err := os.ReadDir(r.opts.RunsDir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), activityFileSuffix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(r.opts.RunsDir, name)
		if path == r.ledger.path {
			continue
		}
		recorded, err := readActivityFile(path)
		if err != nil {
			summary.Orphans = append(summary.Orphans, fmt.Sprintf("ledger %s could not be read: %v", path, err))
			continue
		}
		clean := true
		for _, e := range unresolvedOf(recorded) {
			if !r.cleanupLedgerEntry(ctx, e, summary) {
				clean = false
			}
		}
		if clean {
			_ = os.Remove(path)
		} else {
			summary.Orphans = append(summary.Orphans, fmt.Sprintf("ledger %s still names objects that may exist", path))
		}
	}
}

// cleanupLedgerEntry deletes one recorded object by id, reporting whether
// the entry is settled. An entry with no id cannot be addressed — it is
// the prefix pass's job, and settles only if that pass can run.
func (r *runner) cleanupLedgerEntry(ctx context.Context, e activityEntry, summary *CleanupSummary) bool {
	if e.ID == "" || e.ItemPath == "" {
		// No identifier was ever learned; the prefix pass is this
		// object's only chance.
		return e.Name != ""
	}
	path := e.ItemPath
	if p := selfParameter(path); p != "" {
		path = strings.ReplaceAll(path, "{"+p+"}", e.ID)
	}
	res, err := r.do(ctx, nil, reqSpec{method: "DELETE", path: path})
	if err != nil {
		summary.Orphans = append(summary.Orphans, orphanLine(e)+": "+err.Error())
		return false
	}
	if res.ok() || res.status == 404 {
		r.ledger.resolve(e.Seq, activityDeleted, e.ID, res.status)
		if res.ok() {
			summary.LedgerDeletes++
		}
		return true
	}
	summary.Orphans = append(summary.Orphans, fmt.Sprintf("%s: the delete answered %d", orphanLine(e), res.status))
	return false
}

// cleanupByPrefix reads every reachable resource collection and deletes
// each object whose name-bearing fields carry the run prefix — the pass
// that catches an object whose create response was never seen. Bounded by
// the prefix, so it can never touch an object the audit did not make.
func (r *runner) cleanupByPrefix(ctx context.Context, summary *CleanupSummary) {
	for _, ep := range r.opts.Plan.Entities {
		rec := recipeOf(&ep)
		if rec.minimalBody == nil || rec.collectionPath == "" || rec.itemPath == "" {
			continue
		}
		// The self parameter is pinned to each found id, so a $created
		// token there is fine; a parent parameter still tokened means the
		// child collection cannot even be listed — its objects cascade
		// with the parent's own deletion or are caught while it exists.
		parentValues := map[string]string{}
		self := selfParameter(rec.itemPath)
		for k, v := range rec.itemValues {
			if k != self {
				parentValues[k] = v
			}
		}
		if referencesCreated(rec.collectionValues) || referencesCreated(parentValues) {
			continue
		}
		r.cleanupCollection(ctx, rec, summary)
	}
}

func (r *runner) cleanupCollection(ctx context.Context, rec *entityRecipe, summary *CleanupSummary) {
	res, err := r.do(ctx, nil, reqSpec{method: "GET", path: rec.collectionPath, pathValues: rec.collectionValues})
	if err != nil || !res.ok() {
		return
	}
	type found struct{ id, name string }
	var matches []found
	for _, item := range items(res.body) {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := nameOf(obj, r.opts.NamePrefix)
		if name == "" {
			continue
		}
		if id := identifierOf(obj); id != "" {
			matches = append(matches, found{id: id, name: name})
		} else {
			summary.Orphans = append(summary.Orphans, fmt.Sprintf("%s %q carries the prefix but no recognisable id", rec.entity, name))
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].id < matches[j].id })
	for _, m := range matches {
		del, err := r.do(ctx, nil, reqSpec{
			method: rec.deleteMethod, path: rec.itemPath, pathValues: itemValuesFor(rec, m.id),
		})
		if err != nil {
			summary.Orphans = append(summary.Orphans, fmt.Sprintf("%s %s (%s): %v", rec.entity, m.id, m.name, err))
			continue
		}
		if del.ok() || del.status == 404 {
			if del.ok() {
				summary.PrefixDeletes++
			}
			r.resolveByName(m.name, m.id, del.status)
			continue
		}
		summary.Orphans = append(summary.Orphans, fmt.Sprintf("%s %s (%s): the delete answered %d", rec.entity, m.id, m.name, del.status))
	}
}

// resolveByName settles the ledger intent behind a prefix-deleted object:
// this is the pass that cleans up an intent that never learned an id, and
// matching by id would leave exactly those intents outstanding.
func (r *runner) resolveByName(name, id string, status int) {
	for _, e := range r.ledger.unresolved() {
		if e.Name == name {
			r.ledger.resolve(e.Seq, activityDeleted, id, status)
			return
		}
	}
}

// referencesCreated reports whether any path value depends on an object
// the current process has not created.
func referencesCreated(values map[string]string) bool {
	for _, v := range values {
		if strings.HasPrefix(v, "$created:") {
			return true
		}
	}
	return false
}

func orphanLine(e activityEntry) string {
	id := e.ID
	if id == "" {
		id = "(id never learned)"
	}
	return fmt.Sprintf("%s %s (name %q, path %s)", e.Entity, id, e.Name, e.ItemPath)
}
