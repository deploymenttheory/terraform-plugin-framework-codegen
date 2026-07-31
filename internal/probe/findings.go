package probe

// Findings is what earlier probes established, readable by later ones.
//
// # Why this and not a dependency graph
//
// Exactly one hard edge exists in the catalogue: write.server-default cannot tell "the server
// assigned this" from "the field is not writable at all" without knowing the field is writable,
// which write.writable-returned settles. The registry already runs them in that order.
//
// Declared dependencies would encode that one edge and then force a bad choice about
// `-only write.server-default`: either the runner silently also runs write.writable-returned --
// widening what -only means, and creating objects nobody asked for -- or the declaration is
// decorative. Neither is worth a graph.
//
// # The precondition is a fact, not a probe
//
// What server-default needs is FactWritable=true for a path. That might come from a probe earlier
// in this run, or from a committed facts document a month ago, and the protocol is identical
// either way. So a probe asks about facts rather than about probes, which also means the
// dependency survives a run that skipped the earlier probe but loaded its evidence.
//
// Read-only plus Retract. A probe adds facts by *returning* them, so the runner stays the only
// writer -- otherwise two probes could disagree about what is in the report and the last one to
// run would win silently.
type Findings struct {
	facts []Fact
}

// NewFindings builds an accumulator over facts already established.
func NewFindings(facts []Fact) *Findings {
	return &Findings{facts: append([]Fact(nil), facts...)}
}

// Add records facts a probe returned. Called by the runner, not by a probe.
func (f *Findings) Add(facts ...Fact) {
	f.facts = append(f.facts, facts...)
}

// Settled reports whether a claim about a path was established at or above a confidence floor.
//
// The floor is the caller's, deliberately. A probe that will create objects on the strength of an
// earlier conclusion should demand more of it than one merely deciding whether to emit a note.
func (f *Findings) Settled(jsonPath string, claim FactField, floor Confidence) (Fact, bool) {
	var (
		best  Fact
		found bool
	)

	for _, fact := range f.facts {
		if fact.JSONPath != jsonPath || fact.Field != claim {
			continue
		}
		// A conditional fact holds only while its gate does, and a caller of Settled is
		// asking an unconditional question -- "is this field writable" so I may create
		// objects on the answer. Letting `writable=true when type is dynamic` answer it
		// would authorise a protocol against every other branch on evidence about one.
		if fact.Conditional() {
			continue
		}
		if !fact.Confidence.AtLeast(floor) {
			continue
		}
		// The strongest wins rather than the last. A run that observed something and then
		// inferred it again must not end up reporting the weaker of the two.
		if !found || fact.Confidence.AtLeast(best.Confidence) {
			best, found = fact, true
		}
	}

	return best, found
}

// True reports whether a boolean claim about a path was established as true.
//
// The convenience the one real dependency needs: server-default asking "is this field writable"
// wants a yes or a no, not a Fact.
func (f *Findings) True(jsonPath string, claim FactField, floor Confidence) bool {
	fact, ok := f.Settled(jsonPath, claim, floor)
	if !ok {
		return false
	}

	return fact.Value.Bool != nil && *fact.Value.Bool
}

// Retract removes facts about a path, and reports how many went.
//
// The one mutation a probe may make, and it exists because a later protocol can *disprove* an
// earlier one. A field that looked writable because a create echoed it back, and is then shown to
// be silently discarded on update, was never writable -- and leaving both facts in the report
// would hand merge a contradiction to resolve with no basis for choosing.
//
// Retracting rather than overwriting keeps the runner the only writer, and keeps the retraction
// visible: the probe that retracts is expected to say so in a note.
//
// Retraction removes every variant of the claim, conditional ones included: a disproof of the
// path's claim disproves each branch of it, and keeping a branch alive after its parent claim
// fell would leave merge a fact whose foundation is gone.
func (f *Findings) Retract(jsonPath string, claim FactField) int {
	kept := make([]Fact, 0, len(f.facts))
	removed := 0

	for _, fact := range f.facts {
		if fact.JSONPath == jsonPath && fact.Field == claim {
			removed++
			continue
		}

		kept = append(kept, fact)
	}

	f.facts = kept

	return removed
}

// Facts returns everything accumulated, for the report.
func (f *Findings) Facts() []Fact {
	if f == nil {
		return nil
	}

	return append([]Fact(nil), f.facts...)
}
