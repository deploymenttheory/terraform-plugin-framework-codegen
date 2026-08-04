package probe

import (
	"context"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe/quirkserver"
)

// rehearsalSubject is the quirk subject with the fields the rehearsal rounds carry.
func rehearsalSubject() Subject {
	subj := quirkSubject()
	subj.EvidenceRev = CurrentEvidenceRev
	subj.Fields = append(subj.Fields,
		Field{
			JSONPath: "colour", Kind: blueprint.KindString,
			ComputedOptionalRequired: blueprint.Optional, Writable: true,
		},
		Field{
			JSONPath: "enabled", Kind: blueprint.KindBool,
			ComputedOptionalRequired: blueprint.Optional, Writable: true,
		},
		Field{
			JSONPath: "postBody", Kind: blueprint.KindString,
			ComputedOptionalRequired: blueprint.Optional, Writable: true,
		},
		Field{
			JSONPath: "requestMethod", Kind: blueprint.KindString,
			ComputedOptionalRequired: blueprint.Optional, Writable: true,
		},
	)
	return subj
}

// rehearsalRound is a frozen round over the quirk subject: minimal is the required
// field alone, maximal carries every optional the tests below observe.
func rehearsalRound() RehearsalRound {
	return RehearsalRound{
		Minimal: map[string]any{"key": "stamped"},
		Maximal: map[string]any{
			"key":           "stamped",
			"value":         "a",
			"colour":        "blue",
			"enabled":       false,
			"postBody":      "b",
			"requestMethod": "get",
		},
	}
}

func runRehearsal(t *testing.T, srv *quirkserver.Server, cfg *RehearsalConfig) Report {
	t.Helper()

	out, err := Run(context.Background(), RunOptions{
		Mode:      ModeRecord,
		Subject:   rehearsalSubject(),
		Plan:      writePlan(),
		Only:      "write.rehearsal",
		BaseURL:   srv.BaseURL(),
		Redactor:  testRedactor(t),
		Grant:     &Grant{namePrefix: testPrefix},
		Ledger:    MemoryLedger(),
		Rehearsal: cfg,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	return out.Report
}

// TestUnit_Probe_RehearsalObservesTheForcedValue: send false, the server stores true
// on both write paths -- the networkMeasurements class, Corroborated because both
// directions agreed.
func TestUnit_Probe_RehearsalObservesTheForcedValue(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{Forces: map[string]any{"enabled": true}})

	report := runRehearsal(t, srv, &RehearsalConfig{Rounds: []RehearsalRound{rehearsalRound()}})

	fact, ok := factFor(t, report, "enabled", FactServerForced)
	if !ok {
		t.Fatalf("no serverForced fact: %v", report.Facts)
	}
	if fact.Value.Literal == nil || fact.Value.Literal.Raw != "true" {
		t.Errorf("forced value = %v, want true", fact.Value)
	}
	if fact.Confidence != ConfidenceCorroborated {
		t.Errorf("both write paths stored the same value; want Corroborated, got %s",
			fact.Confidence)
	}

	if n := len(srv.Objects()); n != 0 {
		t.Errorf("%d object(s) left in the tenant", n)
	}
}

// TestUnit_Probe_RehearsalObservesTheUpdateReset: omitted on the downgrade, the field
// reverts to a constant that is not the stored value.
func TestUnit_Probe_RehearsalObservesTheUpdateReset(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		UpdateDefaults: map[string]any{"colour": "grey"},
	})

	report := runRehearsal(t, srv, &RehearsalConfig{Rounds: []RehearsalRound{rehearsalRound()}})

	if fact, ok := factFor(t, report, "colour", FactUpdateResets); !ok {
		t.Fatalf("no updateResets fact: %v", report.Facts)
	} else if fact.Value.Bool == nil || !*fact.Value.Bool {
		t.Errorf("updateResets = %v, want true", fact.Value)
	}

	fact, ok := factFor(t, report, "colour", FactUpdateDefault)
	if !ok {
		t.Fatalf("no updateDefault fact: %v", report.Facts)
	}
	if fact.Value.Literal == nil || fact.Value.Literal.Raw != `"grey"` {
		t.Errorf("updateDefault = %v, want \"grey\"", fact.Value)
	}
	if fact.Confidence != ConfidenceCorroborated {
		t.Errorf("both directions' downgrades agreed; want Corroborated, got %s", fact.Confidence)
	}
}

// TestUnit_Probe_RehearsalBisectsTheSuppressingSibling: postBody round-trips alone and
// vanishes when requestMethod rides along; the bisection must name the culprit.
func TestUnit_Probe_RehearsalBisectsTheSuppressingSibling(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		SuppressWhenSibling: &quirkserver.Conditional{
			WhenField: "requestMethod", WhenValue: "get", Then: "postBody",
		},
	})

	report := runRehearsal(t, srv, &RehearsalConfig{Rounds: []RehearsalRound{rehearsalRound()}})

	fact, ok := factFor(t, report, "postBody", FactInteractionSuppressed)
	if !ok {
		t.Fatalf("no interactionSuppressed fact: %v\nnotes: %v", report.Facts, report.Notes)
	}
	if len(fact.When) != 1 || fact.When[0].JSONPath != "requestMethod" {
		t.Errorf("the fact must name the culprit as a precondition: %+v", fact.When)
	}
	if fact.Confidence != ConfidenceObserved {
		t.Errorf("a bisected culprit is Observed, got %s", fact.Confidence)
	}

	if n := len(srv.Objects()); n != 0 {
		t.Errorf("%d object(s) left in the tenant", n)
	}
}

// TestUnit_Probe_RehearsalEchoesFeedReturnedOnUpdate: a well-behaved server echoes
// everything; a nulling one does not, and both must be recorded from the update
// response itself.
func TestUnit_Probe_RehearsalEchoesFeedReturnedOnUpdate(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{NullsInWriteResponse: []string{"postBody"}})

	report := runRehearsal(t, srv, &RehearsalConfig{Rounds: []RehearsalRound{rehearsalRound()}})

	if fact, ok := factFor(t, report, "postBody", FactReturnedOnUpdate); !ok {
		t.Fatalf("no returnedOnUpdate fact for the nulled field: %v", report.Facts)
	} else if fact.Value.Bool == nil || *fact.Value.Bool {
		t.Errorf("a nulled field is not echoed; got %v", fact.Value)
	}

	if fact, ok := factFor(t, report, "colour", FactReturnedOnUpdate); !ok {
		t.Fatalf("no returnedOnUpdate fact for the ordinary field: %v", report.Facts)
	} else if fact.Value.Bool == nil || !*fact.Value.Bool {
		t.Errorf("an ordinary field is echoed; got %v", fact.Value)
	}

	// The null-aware read conclusion: the API answers explicit null on every read,
	// which presence-only observation calls "returned" -- and flattening it is what
	// blanks the configured value. Both directions saw it, so Corroborated.
	fact, ok := factFor(t, report, "postBody", FactReturnedOnRead)
	if !ok {
		t.Fatalf("no returnedOnRead fact for the nulled field: %v", report.Facts)
	}
	if fact.Value.Bool == nil || *fact.Value.Bool {
		t.Errorf("explicit null for a sent value is not returned; got %v", fact.Value)
	}
	if fact.Confidence != ConfidenceCorroborated {
		t.Errorf("both directions read null; want Corroborated, got %s", fact.Confidence)
	}
}

// TestUnit_Probe_RehearsalFixpointStopsWhenTheBodiesStopChanging: the derive closure
// is consulted once per round and the loop ends the moment it returns what was just
// sent.
func TestUnit_Probe_RehearsalFixpointStopsWhenTheBodiesStopChanging(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	derivations := 0
	cfg := &RehearsalConfig{
		Derive: func(facts []Fact) (RehearsalRound, error) {
			derivations++
			return rehearsalRound(), nil
		},
	}

	report := runRehearsal(t, srv, cfg)

	// Round one runs; the second derivation returns the same bodies, so no second
	// lifecycle is spent on it.
	if len(report.Rehearsal) != 1 {
		t.Fatalf("executed %d round(s), want 1: the fixpoint had converged", len(report.Rehearsal))
	}
	if derivations != 2 {
		t.Errorf("derive was consulted %d time(s), want 2 (once to run, once to converge)",
			derivations)
	}

	if n := len(srv.Objects()); n != 0 {
		t.Errorf("%d object(s) left in the tenant", n)
	}
}

// TestUnit_Probe_RehearsalWithoutBodiesSaysSo: no config means a stated note, never a
// silent pass -- and replaying an old snapshot is exactly this case.
func TestUnit_Probe_RehearsalWithoutBodiesSaysSo(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	report := runRehearsal(t, srv, nil)

	if len(report.Facts) != 0 {
		t.Errorf("no bodies were supplied, so nothing can have been observed: %v", report.Facts)
	}
	if _, ok := noteMentioning(report, "no rehearsal bodies"); !ok {
		t.Errorf("the skip must be stated: %v", report.Notes)
	}
}
