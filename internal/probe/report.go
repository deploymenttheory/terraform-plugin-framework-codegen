package probe

import (
	"fmt"
	"sort"
	"strings"
)

// Report is the committed record of one probe run.
//
// It carries no timestamps and no durations. That is not an oversight: the report is
// committed and diffed by CI, and any value that changes without an input changing
// would make the drift check useless -- the same rule internal/blueprint and
// internal/emit follow. Latency belongs in the run log, and a measured consistency
// window belongs in a fact with its provenance attached, where nobody can mistake it
// for a property of the real system.
type Report struct {
	// Profile summarises where this ran, and never carries the token.
	Profile ProfileSummary `json:"profile"`

	Facts []Fact `json:"facts,omitempty"`
	Notes []Note `json:"notes,omitempty"`

	Probes []ProbeOutcome `json:"probes"`
	Budget BudgetReport   `json:"budget"`

	// Sweep is what cleaning up did, nil for a read-only run. A pointer so a read-only
	// report does not carry a zero summary claiming a complete sweep of nothing.
	Sweep *SweepSummary `json:"sweep,omitempty"`

	// Orphans are objects the sweeper could not remove. Non-empty means the run
	// failed, whatever else it achieved.
	Orphans []Orphan `json:"orphans,omitempty"`

	// Rehearsal is what write.rehearsal actually sent, per fixpoint round. Written
	// beside the cassette as rehearsal.json so replay sends the same bodies rather
	// than re-deriving from a working tree that has moved on.
	Rehearsal []RehearsalRound `json:"rehearsal,omitempty"`
}

// ProfileSummary is what may safely be written into a committed artefact.
//
// The host and the assertions that passed, and nothing else. Not the token, obviously,
// but also not the account-group id or the tenant name: a committed report is as public
// as the repository, and a tenant identifier is the kind of thing that turns a
// vulnerability into a targeted one.
type ProfileSummary struct {
	// Host is the API host, which is not a secret and is the one thing a reader needs
	// in order to know what these facts describe.
	Host string `json:"host"`
	// Mode is record, replay, verify or sweep.
	Mode string `json:"mode"`
	// Sandbox records whether the profile claimed to be a sandbox.
	Sandbox bool `json:"sandbox"`
	// AssertionsPassed names the gating assertions that were checked and passed, so a
	// reader can see what evidence stood behind a mutating run.
	AssertionsPassed []string `json:"assertionsPassed,omitempty"`
}

// ProbeOutcome is what one probe did.
type ProbeOutcome struct {
	Name string `json:"name"`
	Kind Kind   `json:"kind"`

	// Status is one of "ok", "skipped", "abandoned" or "failed".
	//
	// Four states rather than pass/fail, because they mean different things to a
	// reader. Skipped is "this probe does not apply here"; abandoned is "the protocol
	// could not be completed honestly, so no fact was emitted" -- which is a correct
	// outcome, not a failure.
	Status string `json:"status"`

	Requests int `json:"requests"`
	Facts    int `json:"facts"`

	// Reason explains a status other than ok.
	Reason string `json:"reason,omitempty"`
}

// BudgetReport is the accounting.
type BudgetReport struct {
	Requests    int `json:"requests"`
	MaxRequests int `json:"maxRequests"`
	Creates     int `json:"creates"`
	MaxCreates  int `json:"maxCreates"`
	Deletes     int `json:"deletes"`
	// DeleteFailures being non-zero is why a run may stop early.
	DeleteFailures int `json:"deleteFailures,omitempty"`
	// SweepRequests is what cleaning up cost, counted against the sweep's own reserve
	// rather than the run's cap. Separate because a reader needs to be able to tell a run
	// that spent its budget probing from one that spent it cleaning up.
	SweepRequests int `json:"sweepRequests,omitempty"`
}

// Orphan is an object the prober created and could not remove.
type Orphan struct {
	ID   string `json:"id,omitempty"`
	Path string `json:"path"`
	// Probe names what created it.
	Probe string `json:"probe,omitempty"`
	// Curl is the exact command to remove it by hand. Printed because the person who
	// has to clean up should not have to reconstruct the request from the path, and
	// because a run that leaves rubbish owes them that much.
	Curl string `json:"curl,omitempty"`
}

// Summary is the one-line count, always printed so a silent run is distinguishable
// from a run that had nothing to say.
func (r Report) Summary() string {
	var b strings.Builder

	ok, skipped, abandoned, failed := 0, 0, 0, 0
	for _, p := range r.Probes {
		switch p.Status {
		case "ok":
			ok++
		case "skipped":
			skipped++
		case "abandoned":
			abandoned++
		default:
			failed++
		}
	}

	fmt.Fprintf(&b, "%d probe(s): %d ok, %d skipped, %d abandoned, %d failed. ",
		len(r.Probes), ok, skipped, abandoned, failed)
	fmt.Fprintf(&b, "%d fact(s), %d note(s), %d request(s)",
		len(r.Facts), len(r.Notes), r.Budget.Requests)

	if r.Budget.Creates > 0 {
		fmt.Fprintf(&b, ", %d object(s) created", r.Budget.Creates)
	}
	if len(r.Orphans) > 0 {
		fmt.Fprintf(&b, ", %d ORPHANED", len(r.Orphans))
	}

	return b.String()
}

// Sort orders everything in the report so a committed document is byte-stable.
func (r *Report) Sort() {
	SortFacts(r.Facts)

	sort.SliceStable(r.Notes, func(i, j int) bool {
		a, b := r.Notes[i], r.Notes[j]
		if a.Resource != b.Resource {
			return a.Resource < b.Resource
		}
		if a.JSONPath != b.JSONPath {
			return a.JSONPath < b.JSONPath
		}
		return a.Message < b.Message
	})

	sort.SliceStable(r.Probes, func(i, j int) bool {
		if r.Probes[i].Kind != r.Probes[j].Kind {
			return r.Probes[i].Kind == KindRead
		}
		return r.Probes[i].Name < r.Probes[j].Name
	})

	sort.SliceStable(r.Orphans, func(i, j int) bool { return r.Orphans[i].ID < r.Orphans[j].ID })
}

// FactsAtLeast returns the facts meeting a confidence floor.
//
// Merge uses this rather than filtering itself, so the confidence rules live in one
// place next to the levels they refer to.
func (r Report) FactsAtLeast(want Confidence) []Fact {
	var out []Fact
	for _, f := range r.Facts {
		if f.Confidence.AtLeast(want) {
			out = append(out, f)
		}
	}
	return out
}
