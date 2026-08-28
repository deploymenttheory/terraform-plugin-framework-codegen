package run

import (
	"sort"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
)

// inferEdges runs the triangulating inference over every entity's gathered
// evidence and folds the conditional-edge and list-shape observations it
// asserts into the run's own. It runs only on a strategy-driven run: without a
// compiled strategy there are no claims to confirm and no variants to
// diff, so the executor's per-probe findings stand alone.
//
// The inferred observations carry no excerpts — an edge's proof is the
// convergence of many probes, not one request — so the redaction that guards
// per-probe excerpts has nothing to remove here. They are stamped with the
// run's identity and left for finishObservations to dedup against the
// per-probe findings, where a confirmed finding with excerpt proof always wins
// over one derived without.
func (r *runner) inferEdges() {
	if len(r.strategies) == 0 {
		return
	}
	entities := make([]string, 0, len(r.evidence))
	for k := range r.evidence {
		entities = append(entities, k)
	}
	sort.Strings(entities)

	for _, entity := range entities {
		compiled := r.strategies[entity]
		ev := r.evidence[entity]
		if compiled == nil || ev == nil {
			continue
		}
		ev.Adjustments = r.adjustmentsFor(entity)
		ev.RejectsUnknownFields = r.summary.RejectsUnknownFields[entity]

		for _, o := range infer.Infer(*ev, compiled) {
			r.adoptInferred(o)
		}
	}
}

// adjustmentsFor is every adjustment the run recorded for one entity, in
// recorded order — the inference does its own deduping.
func (r *runner) adjustmentsFor(entity string) []infer.RequestAdjustment {
	var out []infer.RequestAdjustment
	for _, a := range r.adjustments {
		if a.Entity == entity {
			out = append(out, a)
		}
	}
	return out
}

// adoptInferred stamps the run's identity onto an inferred observation and
// records it. The inference computed the stable ID and set the finding; only
// the run stamps (run id, spec hash, observed-at) are the run's to add.
func (r *runner) adoptInferred(o observe.Observation) {
	o.RunID = r.runID
	o.SpecHash = r.opts.SpecHash
	o.ObservedAt = time.Now().UTC()
	r.obs = append(r.obs, o)
}
