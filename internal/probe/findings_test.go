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

	f := NewFindings([]Fact{
		boolFact("value", FactWritable, true, Observed),
		boolFact("color", FactWritable, false, Observed),
	})

	if !f.True("value", FactWritable, Observed) {
		t.Error("value was established as writable")
	}
	if f.True("color", FactWritable, Observed) {
		t.Error("color was established as NOT writable, which is not the same as unknown")
	}
	if f.True("absent", FactWritable, Observed) {
		t.Error("a field nothing was established about must not read as true")
	}
}

// TestUnit_Probe_FindingsRespectsTheCallersConfidenceFloor.
//
// The floor is the caller's deliberately: a probe about to create objects on the strength of an
// earlier conclusion should demand more of it than one merely deciding whether to emit a note.
func TestUnit_Probe_FindingsRespectsTheCallersConfidenceFloor(t *testing.T) {
	t.Parallel()

	f := NewFindings([]Fact{boolFact("value", FactWritable, true, Suspected)})

	if _, ok := f.Settled("value", FactWritable, Suspected); !ok {
		t.Error("a suspected fact should satisfy a suspected floor")
	}
	if _, ok := f.Settled("value", FactWritable, Observed); ok {
		t.Error("a suspected fact must not satisfy an observed floor")
	}
}

// TestUnit_Probe_FindingsPrefersTheStrongestFact.
//
// A run that observed something and then inferred it again must not end up reporting the weaker of
// the two, whichever order they happened to be added in.
func TestUnit_Probe_FindingsPrefersTheStrongestFact(t *testing.T) {
	t.Parallel()

	weakFirst := NewFindings([]Fact{
		boolFact("value", FactWritable, true, Inferred),
		boolFact("value", FactWritable, true, Corroborated),
	})
	strongFirst := NewFindings([]Fact{
		boolFact("value", FactWritable, true, Corroborated),
		boolFact("value", FactWritable, true, Inferred),
	})

	for name, f := range map[string]*Findings{"weak first": weakFirst, "strong first": strongFirst} {
		got, ok := f.Settled("value", FactWritable, Inferred)
		if !ok {
			t.Fatalf("%s: nothing settled", name)
		}
		if got.Confidence != Corroborated {
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

	f := NewFindings([]Fact{
		boolFact("value", FactWritable, true, Observed),
		boolFact("value", FactReturnedOnRead, true, Observed),
		boolFact("color", FactWritable, true, Observed),
	})

	if got := f.Retract("value", FactWritable); got != 1 {
		t.Errorf("Retract removed %d, want 1", got)
	}

	if f.True("value", FactWritable, Suspected) {
		t.Error("the retracted fact is still there")
	}
	// Only that claim about that path. A retraction that took the neighbours with it would
	// quietly discard evidence nobody disproved.
	if !f.True("value", FactReturnedOnRead, Suspected) {
		t.Error("a different claim about the same path was removed")
	}
	if !f.True("color", FactWritable, Suspected) {
		t.Error("the same claim about a different path was removed")
	}

	if got := f.Retract("value", FactWritable); got != 0 {
		t.Errorf("retracting twice removed %d the second time", got)
	}
}

// TestUnit_Probe_FindingsIsTheRunnersToWrite.
//
// A probe adds facts by returning them, so the runner stays the only writer. Otherwise two probes
// could disagree about what is in the report and the last to run would win silently.
func TestUnit_Probe_FindingsIsTheRunnersToWrite(t *testing.T) {
	t.Parallel()

	f := NewFindings(nil)

	if len(f.Facts()) != 0 {
		t.Error("a fresh accumulator holds nothing")
	}

	f.Add(boolFact("value", FactWritable, true, Observed))

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
	source := []Fact{boolFact("value", FactWritable, true, Observed)}
	built := NewFindings(source)
	source[0].JSONPath = "mutated"

	if built.Facts()[0].JSONPath != "value" {
		t.Error("NewFindings aliased its argument")
	}

	var none *Findings
	if none.Facts() != nil {
		t.Error("a nil accumulator must answer safely")
	}
}
