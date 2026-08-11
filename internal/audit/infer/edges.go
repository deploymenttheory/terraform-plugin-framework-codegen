package infer

import (
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
)

// keyOf identifies an edge for confirmed/inconclusive reconciliation by the
// same identity ComputeID hashes, so two observations of one edge with
// different outcomes reconcile to the stronger.
func keyOf(o observe.Observation) string { return o.ID }

// edge builds one inferred observation with its ID already computed, leaving
// the run stamps (run id, spec hash, observed-at) for the caller. Excerpts are
// never attached: an inferred edge's proof is the convergence of many probes,
// not one request, and an excerpt-free observation is trivially redacted.
func (m *model) edge(attribute string, kind observe.Kind, value any, cond *observe.Condition, prov observe.Provenance, outcome observe.Outcome) observe.Observation {
	return m.edgeAttr(attribute, kind, value, cond, prov, outcome)
}

// edgeAttr is edge with an explicit attribute, used for entity-level edges
// where the attribute is empty or is the gate field rather than a subject.
func (m *model) edgeAttr(attribute string, kind observe.Kind, value any, cond *observe.Condition, prov observe.Provenance, outcome observe.Outcome) observe.Observation {
	o := observe.Observation{
		ID:         observe.ComputeID(m.ev.Entity, attribute, kind, cond),
		Entity:     m.ev.Entity,
		Attribute:  attribute,
		Kind:       kind,
		Condition:  cond,
		Provenance: prov,
		Outcome:    outcome,
	}
	if outcome == observe.OutcomeConfirmed {
		o.Value = value
	}
	return o
}

// dedup collapses observations sharing an ID, keeping the most-informed
// outcome — a confirmed edge is never displaced by an inconclusive one for the
// same edge — then sorts for a byte-stable result.
func dedup(obs []observe.Observation) []observe.Observation {
	best := map[string]int{}
	for i, o := range obs {
		if j, ok := best[o.ID]; !ok || rank(o.Outcome) >= rank(obs[j].Outcome) {
			best[o.ID] = i
		}
	}
	idx := make([]int, 0, len(best))
	for _, i := range best {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	out := make([]observe.Observation, 0, len(idx))
	for _, i := range idx {
		out = append(out, obs[i])
	}
	observe.Sort(out)
	return out
}

// rank orders outcomes by how much they know, so dedup keeps the strongest.
func rank(o observe.Outcome) int {
	switch o {
	case observe.OutcomeConfirmed:
		return 3
	case observe.OutcomeInconclusive:
		return 2
	case observe.OutcomeBlocked:
		return 1
	default:
		return 0
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func joinSorted(list []string) string {
	s := append([]string(nil), list...)
	sort.Strings(s)
	return strings.Join(s, ",")
}
