package probe

import "testing"

func boolFact(path string, field FactField, value bool, conf Confidence) Fact {
	return Fact{
		Resource:   "thing",
		JSONPath:   path,
		Field:      field,
		Value:      BoolValue(value),
		Confidence: conf,
		Probe:      "write.writable-returned",
		Evidence:   []string{"001-post-things"},
		Rationale:  "for the test",
	}
}

// TestUnit_Probe_FindingsAnswersTheOneRealDependency.
//
// write.server-default cannot tell "the server assigned this" from "the field is not writable at
// all" without knowing the field is writable. It asks about the *fact*, not about the probe -- so
// the dependency is satisfied identically whether the answer came from a probe earlier in this run
// or from a committed facts document a month ago.
func TestUnit_Probe_FindingsAnswersTheOneRealDependency(t *testing.T) {
	t.Parallel()

	f := NewFactSet([]Fact{
		boolFact("value", FactWritable, true, ConfidenceObserved),
		boolFact("colour", FactWritable, false, ConfidenceObserved),
	})

	if !f.True("value", FactWritable, ConfidenceObserved) {
		t.Error("value was established as writable")
	}
	if f.True("colour", FactWritable, ConfidenceObserved) {
		t.Error("colour was established as NOT writable, which is not the same as unknown")
	}
	if f.True("absent", FactWritable, ConfidenceObserved) {
		t.Error("a field nothing was established about must not read as true")
	}
}

// TestUnit_Probe_FindingsRespectsTheCallersConfidenceFloor.
//
// The floor is the caller's deliberately: a probe about to create objects on the strength of an
// earlier conclusion should demand more of it than one merely deciding whether to emit a note.
func TestUnit_Probe_FindingsRespectsTheCallersConfidenceFloor(t *testing.T) {
	t.Parallel()

	f := NewFactSet([]Fact{boolFact("value", FactWritable, true, ConfidenceSuspected)})

	if _, ok := f.Settled("value", FactWritable, ConfidenceSuspected); !ok {
		t.Error("a suspected fact should satisfy a suspected floor")
	}
	if _, ok := f.Settled("value", FactWritable, ConfidenceObserved); ok {
		t.Error("a suspected fact must not satisfy an observed floor")
	}
}

// TestUnit_Probe_FindingsPrefersTheStrongestFact.
//
// A run that observed something and then inferred it again must not end up reporting the weaker of
// the two, whichever order they happened to be added in.
func TestUnit_Probe_FindingsPrefersTheStrongestFact(t *testing.T) {
	t.Parallel()

	weakFirst := NewFactSet([]Fact{
		boolFact("value", FactWritable, true, ConfidenceInferred),
		boolFact("value", FactWritable, true, ConfidenceCorroborated),
	})
	strongFirst := NewFactSet([]Fact{
		boolFact("value", FactWritable, true, ConfidenceCorroborated),
		boolFact("value", FactWritable, true, ConfidenceInferred),
	})

	for name, f := range map[string]*FactSet{"weak first": weakFirst, "strong first": strongFirst} {
		got, ok := f.Settled("value", FactWritable, ConfidenceInferred)
		if !ok {
			t.Fatalf("%s: nothing settled", name)
		}
		if got.Confidence != ConfidenceCorroborated {
			t.Errorf("%s: confidence = %s, want corroborated", name, got.Confidence)
		}
	}
}

// TestUnit_Probe_RetractRemovesADisprovedFact.
//
// The one mutation a probe may make. A field that looked writable because a create echoed it back,
// and is then shown to be silently discarded on update, was never writable -- and leaving both
// facts in would hand merge a contradiction with no basis for resolving it.
func TestUnit_Probe_RetractRemovesADisprovedFact(t *testing.T) {
	t.Parallel()

	f := NewFactSet([]Fact{
		boolFact("value", FactWritable, true, ConfidenceObserved),
		boolFact("value", FactReturnedOnRead, true, ConfidenceObserved),
		boolFact("colour", FactWritable, true, ConfidenceObserved),
	})

	if got := f.Retract("value", FactWritable); got != 1 {
		t.Errorf("Retract removed %d, want 1", got)
	}

	if f.True("value", FactWritable, ConfidenceSuspected) {
		t.Error("the retracted fact is still there")
	}
	// Only that claim about that path. A retraction that took the neighbours with it would
	// quietly discard evidence nobody disproved.
	if !f.True("value", FactReturnedOnRead, ConfidenceSuspected) {
		t.Error("a different claim about the same path was removed")
	}
	if !f.True("colour", FactWritable, ConfidenceSuspected) {
		t.Error("the same claim about a different path was removed")
	}

	if got := f.Retract("value", FactWritable); got != 0 {
		t.Errorf("retracting twice removed %d the second time", got)
	}
}

// TestUnit_Probe_AConditionalFactDoesNotSettleAnUnconditionalQuestion.
//
// A caller of Settled is asking an unconditional question -- "is this field writable" so I
// may create objects on the answer. `writable=true when type is dynamic` would authorise a
// protocol against every branch on evidence about one, which is exactly the half-truth the
// precondition exists to prevent.
func TestUnit_Probe_AConditionalFactDoesNotSettleAnUnconditionalQuestion(t *testing.T) {
	t.Parallel()

	conditional := boolFact("value", FactWritable, true, ConfidenceCorroborated)
	conditional.When = []Condition{{JSONPath: "type", Equals: "dynamic"}}

	f := NewFactSet([]Fact{conditional})

	if _, ok := f.Settled("value", FactWritable, ConfidenceInferred); ok {
		t.Error("a conditional fact must not settle an unconditional question")
	}
	if f.True("value", FactWritable, ConfidenceInferred) {
		t.Error("True answered an unconditional question from a conditional fact")
	}

	// Retract still removes every variant: a disproof of the path's claim disproves each
	// branch, and a surviving branch would hand merge a fact whose foundation is gone.
	other := boolFact("value", FactWritable, true, ConfidenceCorroborated)
	other.When = []Condition{{JSONPath: "type", Equals: "static"}}
	f.Add(other)

	if got := f.Retract("value", FactWritable); got != 2 {
		t.Errorf("Retract removed %d fact(s), want both branch variants", got)
	}
}

// TestUnit_Probe_FindingsIsTheRunnersToWrite.
//
// A probe adds facts by returning them, so the runner stays the only writer. Otherwise two probes
// could disagree about what is in the report and the last to run would win silently.
func TestUnit_Probe_FindingsIsTheRunnersToWrite(t *testing.T) {
	t.Parallel()

	f := NewFactSet(nil)

	if len(f.Facts()) != 0 {
		t.Error("a fresh accumulator holds nothing")
	}

	f.Add(boolFact("value", FactWritable, true, ConfidenceObserved))

	if len(f.Facts()) != 1 {
		t.Errorf("Facts = %v", f.Facts())
	}

	// The returned slice is a copy: a probe that appended to it would be writing to the report
	// without the runner's knowledge.
	got := f.Facts()
	got[0].JSONPath = "mutated"

	if f.Facts()[0].JSONPath != "value" {
		t.Error("Facts returns the accumulator's own slice")
	}

	// And the constructor copies its input, so the caller's slice is not aliased either.
	source := []Fact{boolFact("value", FactWritable, true, ConfidenceObserved)}
	built := NewFactSet(source)
	source[0].JSONPath = "mutated"

	if built.Facts()[0].JSONPath != "value" {
		t.Error("NewFindings aliased its argument")
	}

	var none *FactSet
	if none.Facts() != nil {
		t.Error("a nil accumulator must answer safely")
	}
}
