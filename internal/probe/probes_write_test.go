package probe

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe/quirkserver"
)

// writePlan is the two-fixture plan the write probes are exercised against.
//
// Two fixtures because that is what separates "the server stored what I sent" from "the server
// returned its own value" -- the second fixture's differing value is the whole basis of every
// writability conclusion below.
func writePlan() Plan {
	return Plan{
		Fixtures: []Fixture{
			{Name: "first", Body: map[string]any{"key": "stamped", "value": "a"}},
			{Name: "second", Body: map[string]any{"key": "stamped", "value": "b"}},
		},
		Budget: Budget{MaxRequests: 60, MaxCreates: 20},
	}
}

// runWriteProbes runs the mutating tier against a quirk server and returns the report plus the
// recorded transcript.
//
// Through Run rather than by calling a probe directly, because that is what exercises the whole
// apparatus: the gate's grant, the ledger, per-probe release and the sweep all sit between a probe
// and the tenant, and a test that bypassed them would prove the protocol works while leaving the
// machinery around it unexercised.
func runWriteProbes(
	t *testing.T,
	srv *quirkserver.Server,
	plan Plan,
	only string,
) (Report, []cassette.Interaction) {
	t.Helper()

	out, err := Run(context.Background(), RunOptions{
		Mode:     ModeRecord,
		Subject:  quirkSubject(),
		Plan:     plan,
		Only:     only,
		BaseURL:  srv.BaseURL(),
		Redactor: testRedactor(t),
		Grant:    &Grant{namePrefix: testPrefix},
		Ledger:   MemoryLedger(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	return out.Report, out.Interactions
}

// factFor finds one fact by path and field.
func factFor(t *testing.T, report Report, path string, field FactField) (Fact, bool) {
	t.Helper()

	for _, f := range report.Facts {
		if f.JSONPath == path && f.Field == field {
			return f, true
		}
	}

	return Fact{}, false
}

func noteMentioning(report Report, needle string) (Note, bool) {
	for _, n := range report.Notes {
		if strings.Contains(n.Message, needle) {
			return n, true
		}
	}

	return Note{}, false
}

// TestUnit_Probe_WritableIsSettledByTwoDistinctValues.
//
// The pair of facts that decides between Computed and Optional+Computed, in both directions
// against the quirk that produces each.
//
// SilentlyDiscards is the ground truth for the hard case: the field was demonstrably sent, the
// create answered 201, and the object never had it. A probe that read back once and saw its own
// value echoed would call that writable.
func TestUnit_Probe_WritableIsSettledByTwoDistinctValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		quirks quirkserver.Quirks
		// wantWritable is the value expected, and found says whether a fact should exist.
		found        bool
		wantWritable bool
		wantAtLeast  Confidence
	}{
		{
			name:   "a field the API stores",
			quirks: quirkserver.Quirks{},
			found:  true, wantWritable: true, wantAtLeast: Corroborated,
		},
		{
			// Accepted and thrown away -- and deliberately *no* writability fact. From outside,
			// "discarded" and "stored but never returned" are the same observation, and merge
			// would act on the first by making the attribute Computed. The honest outcome is
			// returnedOnRead=false plus a note saying what cannot be seen from here.
			name:   "a field the API accepts and discards",
			quirks: quirkserver.Quirks{SilentlyDiscards: []string{"value"}},
			found:  false,
		},
		{
			// Stored and transformed. Writable, and the transform is another probe's business --
			// conflating the two would mark a perfectly writable field as computed.
			name:   "a field the API normalises",
			quirks: quirkserver.Quirks{NormalisesCase: []string{"value"}},
			found:  true, wantWritable: true, wantAtLeast: Corroborated,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, tc.quirks)

			report, _ := runWriteProbes(t, srv, writePlan(), "write.writable-returned")

			fact, ok := factFor(t, report, "value", FactWritable)
			if ok != tc.found {
				t.Fatalf("writable fact found = %v, want %v (facts: %v)", ok, tc.found, report.Facts)
			}
			if !ok {
				// The field was sent and never returned, which is recorded, and the reason
				// writability is unobservable is recorded too.
				if fact, found := factFor(t, report, "value", FactReturnedOnRead); !found {
					t.Error("returnedOnRead is observable and must be recorded")
				} else if fact.Value.Bool == nil || *fact.Value.Bool {
					t.Errorf("returnedOnRead = true for a field that never came back: %v", fact)
				}

				if _, found := noteMentioning(report, "look identical from here"); !found {
					t.Errorf("the run must say why writability could not be settled: %v",
						report.Notes)
				}

				if n := len(srv.Objects()); n != 0 {
					t.Errorf("%d object(s) left in the tenant", n)
				}

				return
			}

			if got := fact.Value.Bool != nil && *fact.Value.Bool; got != tc.wantWritable {
				t.Errorf("writable = %v, want %v (%s)", got, tc.wantWritable, fact.Rationale)
			}
			if !fact.Confidence.AtLeast(tc.wantAtLeast) {
				t.Errorf("confidence = %s, want at least %s", fact.Confidence, tc.wantAtLeast)
			}

			// Every object created is removed, whatever the fact turned out to be.
			if n := len(srv.Objects()); n != 0 {
				t.Errorf("%d object(s) left in the tenant", n)
			}
		})
	}
}

// TestUnit_Probe_OneFixtureCannotSettleWritability.
//
// With a single value sent, "the server stored what I sent" and "the server returned its own
// value" are equally good explanations. The honest outcome is a note, not a weak fact -- a
// Suspected fact in the store is a claim nobody can act on.
func TestUnit_Probe_OneFixtureCannotSettleWritability(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	plan := writePlan()
	plan.Fixtures = plan.Fixtures[:1]

	report, _ := runWriteProbes(t, srv, plan, "write.writable-returned")

	if _, ok := factFor(t, report, "value", FactWritable); ok {
		t.Error("one fixture must not produce a writability fact")
	}

	// But what the read *did* show is still recorded: the field came back.
	if fact, ok := factFor(t, report, "value", FactReturnedOnRead); !ok {
		t.Error("returnedOnRead is observable from one round and should still be recorded")
	} else if fact.Confidence != Observed {
		t.Errorf("confidence = %s, want observed from a single round", fact.Confidence)
	}

	if _, ok := noteMentioning(report, "a second fixture sending a different value"); !ok {
		t.Errorf("the run should say what would settle it: %v", report.Notes)
	}
}

// TestUnit_Probe_AnExpansionGatedFieldIsNotAbsent.
//
// The stated failure mode of this probe, as a test. Any API with an expand, include or fields
// parameter may withhold a field until asked; a probe that read back once would conclude
// ReturnedOnRead=false, and the generated state mapper would then blank a real value on every
// refresh.
func TestUnit_Probe_AnExpansionGatedFieldIsNotAbsent(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		ExpansionGated: map[string]string{"value": "all"},
	})

	plan := writePlan()
	plan.Expansions = []string{"expand=all"}

	report, _ := runWriteProbes(t, srv, plan, "write.writable-returned")

	fact, ok := factFor(t, report, "value", FactReturnedOnRead)
	if !ok {
		t.Fatalf("no returnedOnRead fact: %v", report.Facts)
	}

	if fact.Value.Bool == nil || !*fact.Value.Bool {
		t.Errorf("returnedOnRead = false for a field that is returned under expansion; the "+
			"generated state mapper would blank it on every refresh (%s)", fact.Rationale)
	}

	if _, ok := noteMentioning(report, "only when an expansion was requested"); !ok {
		t.Errorf("the gating must be reported, or the generated read will not ask for it: %v",
			report.Notes)
	}

	// And with the expansion *not* declared, the same API looks like one that does not return
	// the field at all. That is the trap, and it is worth pinning: the difference between the
	// two runs is one line of plan.
	bare := quirkserver.New(t, quirkserver.Quirks{
		ExpansionGated: map[string]string{"value": "all"},
	})

	blind, _ := runWriteProbes(t, bare, writePlan(), "write.writable-returned")

	if fact, ok := factFor(t, blind, "value", FactReturnedOnRead); !ok {
		t.Error("expected a fact")
	} else if fact.Value.Bool != nil && *fact.Value.Bool {
		t.Error("without the expansion declared, the field cannot be seen; the run must not " +
			"claim otherwise")
	}
}

// TestUnit_Probe_UpdateStyleIsSettledByTheInterstitialRead.
//
// Both directions of the quirk that decides whether a generated update may send a partial body.
// Getting it wrong means the provider "silently erases attributes the practitioner never
// mentioned", which is the IR's own description of the cost.
func TestUnit_Probe_UpdateStyleIsSettledByTheInterstitialRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		quirks quirkserver.Quirks
		want   string
	}{
		{"an API that merges", quirkserver.Quirks{}, "patchMerge"},
		{"an API that clears omitted fields", quirkserver.Quirks{PutClearsOmitted: true}, "putFull"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, tc.quirks)

			report, _ := runWriteProbes(t, srv, writePlan(), "write.update-style")

			fact, ok := factFor(t, report, "", FactUpdateStyle)
			if !ok {
				t.Fatalf("no update-style fact: %v (notes %v)", report.Facts, report.Notes)
			}

			if fact.Value.Text != tc.want {
				t.Errorf("updateStyle = %q, want %q (%s)", fact.Value.Text, tc.want, fact.Rationale)
			}
			if fact.Confidence != Observed {
				t.Errorf("confidence = %s, want observed", fact.Confidence)
			}

			// The alternatives are the honest half: this was observed for one field and is
			// assumed to hold for the rest.
			if len(fact.Alternatives) == 0 {
				t.Error("an update-style fact must state what its sequence did not rule out")
			}

			if n := len(srv.Objects()); n != 0 {
				t.Errorf("%d object(s) left behind", n)
			}
		})
	}
}

// TestUnit_Probe_NoUpdateOperationMeansReplaceOnly: a resource the API only lets you create and
// delete needs RequiresReplace on every writable attribute, and that is observable without
// sending anything.
func TestUnit_Probe_NoUpdateOperationMeansReplaceOnly(t *testing.T) {
	t.Parallel()

	subj := quirkSubject()
	subj.Update = nil

	sc, err := NewScope(subj, writePlan())
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	result, err := (updateStyle{}).Exercise(context.Background(), &MutatingSession{}, sc)
	if err != nil {
		t.Fatalf("Exercise: %v", err)
	}

	if len(result.Facts) != 1 || result.Facts[0].Value.Text != "replaceOnly" {
		t.Fatalf("facts = %v", result.Facts)
	}
	if result.Requests != 0 {
		t.Errorf("%d request(s) issued to learn something the blueprint already says",
			result.Requests)
	}
}

// TestUnit_Probe_SilentlyIgnoredOnUpdateIsNotImmutability.
//
// An API that refuses a change says so; one that silently drops it does not. Only the second
// produces a perpetual diff, and conflating them is the classic error.
func TestUnit_Probe_SilentlyIgnoredOnUpdateIsNotImmutability(t *testing.T) {
	t.Parallel()

	// Create stores the name; the update accepts a new one, answers 200, and does not apply it.
	// SilentlyDiscards would be the wrong quirk here -- it never stores the field at all, so
	// there would be nothing on the read to compare against.
	srv := quirkserver.New(t, quirkserver.Quirks{SilentlyDiscardsOnUpdate: []string{"key"}})

	report, _ := runWriteProbes(t, srv, writePlan(), "write.update-style")

	fact, ok := factFor(t, report, "key", FactSilentlyIgnoredOnUpdate)
	if !ok {
		t.Fatalf("no silentlyIgnoredOnUpdate fact: %v (notes %v)", report.Facts, report.Notes)
	}

	if fact.Value.Bool == nil || !*fact.Value.Bool {
		t.Errorf("fact = %v", fact)
	}
	if _, ok := factFor(t, report, "key", FactImmutable); ok {
		t.Error("a silently ignored update must not be recorded as immutability")
	}

	// An API that stores it produces no such fact.
	clean := quirkserver.New(t, quirkserver.Quirks{})

	ok2, _ := runWriteProbes(t, clean, writePlan(), "write.update-style")
	if _, found := factFor(t, ok2, "key", FactSilentlyIgnoredOnUpdate); found {
		t.Error("an API that applies the update must not be reported as ignoring it")
	}
}

// TestUnit_Probe_ReadYourWritesIsAsymmetric.
//
// enabled=true is Observed because the failure was seen; enabled=false is only Inferred, because
// one fast success does not prove consistency. So merge may add a read-back and never remove one:
// a needless re-read costs a request, a missing one costs a failed apply.
func TestUnit_Probe_ReadYourWritesIsAsymmetric(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		quirks      quirkserver.Quirks
		wantEnabled bool
		wantConf    Confidence
	}{
		{
			name:   "a consistent API",
			quirks: quirkserver.Quirks{},
			// Inferred, deliberately: nothing here proves the next write will be visible.
			wantEnabled: false, wantConf: Inferred,
		},
		{
			name:   "an eventually consistent API",
			quirks: quirkserver.Quirks{EventuallyConsistentReads: 1},
			// Observed: the failure was seen.
			wantEnabled: true, wantConf: Observed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, tc.quirks)

			plan := writePlan()
			// The retries must not sleep for half a second each in a unit test.
			plan.Budget.MaxWallClockSeconds = 30

			report, _ := runWriteProbesFast(t, srv, plan, "")

			fact, ok := factFor(t, report, "", FactReadBack)
			if !ok {
				t.Fatalf("no readBack fact: %v (notes %v)", report.Facts, report.Notes)
			}

			if fact.Value.ReadBack == nil {
				t.Fatalf("the fact carries no read-back value: %+v", fact)
			}
			if fact.Value.ReadBack.Enabled != tc.wantEnabled {
				t.Errorf("enabled = %v, want %v (%s)",
					fact.Value.ReadBack.Enabled, tc.wantEnabled, fact.Rationale)
			}
			if fact.Confidence != tc.wantConf {
				t.Errorf("confidence = %s, want %s", fact.Confidence, tc.wantConf)
			}

			if tc.wantEnabled {
				// A floor rather than the measurement: the worst case on an idle sandbox is
				// not the worst case in production.
				if fact.Value.ReadBack.MaxRetries < 1 {
					t.Errorf("MaxRetries = %d, want at least one more than was needed",
						fact.Value.ReadBack.MaxRetries)
				}
				if fact.Value.ReadBack.Reason == "" {
					t.Error("a retry loop with no stated reason is indistinguishable from " +
						"cargo cult")
				}
			}

			if len(fact.Alternatives) == 0 {
				t.Error("a consistency window measured on an idle sandbox is not production's, " +
					"and the fact must say so")
			}
		})
	}
}

// TestUnit_Probe_ReadYourWritesCreatesNothing.
//
// It reports what the other mutating probes already measured. A probe that created an object
// purely to time how long it took to appear would spend the scarcest budget there is to learn
// something the run already knows -- and run alone, it must say it has nothing to report rather
// than invent a measurement.
func TestUnit_Probe_ReadYourWritesCreatesNothing(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	report, _ := runWriteProbes(t, srv, writePlan(), "write.read-your-writes")

	if len(report.Facts) != 0 {
		t.Errorf("run alone it has nothing to measure, but produced %v", report.Facts)
	}
	if _, ok := noteMentioning(report, "no object was read back"); !ok {
		t.Errorf("it must say why it concluded nothing: %v", report.Notes)
	}
	if n := len(srv.Objects()); n != 0 {
		t.Errorf("%d object(s) created by a probe that creates nothing", n)
	}

	// Its own request count, not the server's: the sweep runs after every mutating tier and
	// spends from its own reserve, so the server's total is not this probe's.
	for _, outcome := range report.Probes {
		if outcome.Name == "write.read-your-writes" && outcome.Requests != 0 {
			t.Errorf("%d request(s) issued by a probe that costs nothing", outcome.Requests)
		}
	}
}

// runWriteProbesFast is runWriteProbes with the read-back retry interval collapsed.
//
// The interval is real and belongs in a recording; waiting it out in a unit test does not. It is
// configurable for exactly this reason.
func runWriteProbesFast(
	t *testing.T,
	srv *quirkserver.Server,
	plan Plan,
	only string,
) (Report, []cassette.Interaction) {
	t.Helper()

	out, err := Run(context.Background(), RunOptions{
		Mode:      ModeRecord,
		Subject:   quirkSubject(),
		Plan:      plan,
		Only:      only,
		BaseURL:   srv.BaseURL(),
		Redactor:  testRedactor(t),
		Grant:     &Grant{namePrefix: testPrefix},
		Ledger:    MemoryLedger(),
		ReadDelay: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	return out.Report, out.Interactions
}

// TestUnit_Probe_AMutatingRunReplaysToTheSameFacts is the Phase 4.7b milestone.
//
// Record a full mutating run against the quirk server, then re-derive every fact from the
// transcript alone with the network denied, and get exactly the same facts.
//
// This is a much stronger claim than the read tier's version of it. A mutating run's requests
// depend on what earlier responses said, its bodies carry stamped names, and its read-backs retry
// a number of times the API decides -- so anything non-deterministic in any of that would show up
// here as a mismatch. It is also what makes committed write-tier evidence worth having.
func TestUnit_Probe_AMutatingRunReplaysToTheSameFacts(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		SilentlyDiscards: []string{"modifiedDate"},
		NormalisesCase:   []string{"value"},
	})

	recorded, interactions := runWriteProbesFast(t, srv, writePlan(), "")

	if len(interactions) == 0 {
		t.Fatal("nothing was recorded")
	}
	if n := len(srv.Objects()); n != 0 {
		t.Fatalf("%d object(s) left in the tenant after a full run", n)
	}

	// Replayed with no network at all: http.DefaultTransport is DenyTransport in this package's
	// tests, so a code path that bypassed the cassette would fail loudly rather than quietly
	// reaching a real API.
	replayed, err := Run(context.Background(), RunOptions{
		Mode:         ModeReplay,
		Subject:      quirkSubject(),
		Plan:         writePlan(),
		BaseURL:      "https://replay.invalid",
		Interactions: interactions,
		// A replay grant: nothing is created, the transport answers from the cassette, and the
		// probes are the same code.
		Grant:     ReplayGrant(testPrefix),
		Ledger:    MemoryLedger(),
		ReadDelay: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	if err := VerifyFacts(replayed.Report.Facts, recorded.Facts); err != nil {
		t.Fatalf("the replayed facts differ from the recorded ones: %v", err)
	}

	if len(recorded.Facts) == 0 {
		t.Error("the run established nothing, so reproducing it proves nothing")
	}
}

// TestUnit_Probe_AMutatingReplayNeedsAGrant: the probes are the same code in replay, so they need
// the same authorisation type -- and a replay grant cannot be used to record.
func TestUnit_Probe_AMutatingReplayNeedsAGrant(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	_, interactions := runWriteProbesFast(t, srv, writePlan(), "write.writable-returned")

	// Without a grant the mutating tier is reported as skipped rather than run.
	out, err := Run(context.Background(), RunOptions{
		Mode:         ModeReplay,
		Subject:      quirkSubject(),
		Plan:         writePlan(),
		BaseURL:      "https://replay.invalid",
		Interactions: interactions,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, p := range out.Report.Probes {
		if p.Kind == KindMutating && p.Status != "skipped" {
			t.Errorf("%s ran without a grant, status %q", p.Name, p.Status)
		}
	}

	// And the replay grant is refused in record mode, which is the hole ReplayGrant would
	// otherwise open.
	_, err = Run(context.Background(), RunOptions{
		Mode:     ModeRecord,
		Subject:  quirkSubject(),
		Plan:     writePlan(),
		BaseURL:  srv.BaseURL(),
		Redactor: testRedactor(t),
		Grant:    ReplayGrant(testPrefix),
		Ledger:   MemoryLedger(),
	})
	if !errors.Is(err, ErrNoGrant) {
		t.Errorf("error = %v, want ErrNoGrant", err)
	}
}

// TestUnit_Probe_ADeniedFieldIsNotedRatherThanSkipped.
//
// Silence would read as agreement with whatever the blueprint already claims -- and the reason a
// field is denied, that it is writable and has consequences, is exactly the reason its existing
// guess deserves scrutiny.
func TestUnit_Probe_ADeniedFieldIsNotedRatherThanSkipped(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	plan := writePlan()
	plan.Deny = []string{"value"}
	plan.Fixtures = []Fixture{
		{Name: "first", Body: map[string]any{"key": "stamped"}},
		{Name: "second", Body: map[string]any{"key": "stamped"}},
	}

	report, _ := runWriteProbes(t, srv, plan, "write.writable-returned")

	if _, ok := factFor(t, report, "value", FactWritable); ok {
		t.Error("a denied field must not be probed")
	}

	note, ok := noteMentioning(report, "the plan denies this field")
	if !ok {
		t.Fatalf("a denied field must be reported: %v", report.Notes)
	}
	if note.JSONPath != "value" {
		t.Errorf("the note should name the field, got %q", note.JSONPath)
	}
}

// TestUnit_Probe_TheDeclaredCostIsNeverExceeded is the second of the cost model's two invariants.
//
// Cost is what a run is authorised against, so a probe that issues more requests than it declared
// has spent a budget nobody granted.
//
// Measured against the fixture's own request counter rather than the probe's self-report, because
// the self-report is the thing under test. The probes are invoked directly rather than through Run
// for the same reason of measurement hygiene: the sweep runs after every mutating tier and spends
// from its own reserve, so the server's total would otherwise include requests no probe declared.
func TestUnit_Probe_TheDeclaredCostIsNeverExceeded(t *testing.T) {
	t.Parallel()

	probes := map[string]MutatingProbe{
		"write.writable-returned": writableAndReturned{},
		"write.update-style":      updateStyle{},
		"write.read-your-writes":  readYourWrites{},
	}

	for name, probe := range probes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, quirkserver.Quirks{})

			sc, err := NewScope(quirkSubject(), writePlan())
			if err != nil {
				t.Fatalf("NewScope: %v", err)
			}

			declared := probe.Cost(sc)

			ms := mutatingAgainst(t, srv.BaseURL(), MemoryLedger(), func(mc *MutationConfig) {
				mc.ReadDelay = time.Nanosecond
			})

			if _, err := probe.Exercise(context.Background(), ms, sc); err != nil {
				t.Fatalf("Exercise: %v", err)
			}

			// The declared cost includes the delete the runner performs on the probe's behalf,
			// so releasing here is part of what is being counted.
			ms.ReleaseProbe(context.Background(), name)

			if srv.Requests() > declared {
				t.Errorf("%s issued %d request(s) against a declared cost of %d",
					name, srv.Requests(), declared)
			}
			if n := len(srv.Objects()); n != 0 {
				t.Errorf("%s left %d object(s) behind", name, n)
			}
		})
	}
}
