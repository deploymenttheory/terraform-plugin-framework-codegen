package probe

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
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

// defaultsPlan omits two fields from every fixture, so both are in the omitted set the
// server-default protocol observes, and varies one field between fixtures so a derived value can
// be told from a constant.
func defaultsPlan() Plan {
	return Plan{
		Fixtures: []Fixture{
			{Name: "first", Body: map[string]any{"key": "stamped", "value": "a"}},
			{Name: "second", Body: map[string]any{"key": "stamped", "value": "b"}},
		},
		DefaultInfluencers: []string{"value"},
		Budget:             Budget{MaxRequests: 80, MaxCreates: 30},
	}
}

// defaultSubject adds the fields the default protocol needs: two writable ones no fixture sets.
func defaultSubject() Subject {
	subj := quirkSubject()

	subj.Fields = append(subj.Fields,
		Field{
			JSONPath: "colour", Attribute: "colour",
			Kind: blueprint.KindString, ComputedOptionalRequired: blueprint.Optional, Writable: true,
		},
		Field{
			JSONPath: "rank", Attribute: "rank",
			Kind: blueprint.KindInt64, ComputedOptionalRequired: blueprint.Optional, Writable: true,
		},
	)

	return subj
}

// runAgainst runs the mutating tier against a quirk server with a chosen subject.
func runAgainst(
	t *testing.T,
	srv *quirkserver.Server,
	subj Subject,
	plan Plan,
	only string,
) Report {
	t.Helper()

	out, err := Run(context.Background(), RunOptions{
		Mode:      ModeRecord,
		Subject:   subj,
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

	if n := len(srv.Objects()); n != 0 {
		t.Errorf("%d object(s) left in the tenant", n)
	}

	return out.Report
}

// TestUnit_Probe_RequirednessIsAsymmetric.
//
// A 2xx on omission is unambiguous: the API accepted a body without the field. A 4xx is not -- the
// request may have failed for an unrelated reason -- so it is only Observed when the error names
// the field, and Inferred otherwise. Getting that backwards would let one unrelated 400 mark a
// field Required and break every configuration that omits it.
func TestUnit_Probe_RequirednessIsAsymmetric(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		quirks   quirkserver.Quirks
		want     bool
		wantConf Confidence
	}{
		{
			// The pilot's live case: two attributes marked required although the request schema
			// declares no required list.
			name:   "a field the API does not enforce",
			quirks: quirkserver.Quirks{},
			want:   false, wantConf: Corroborated,
		},
		{
			// The quirk server names the offending field, which is what earns Observed.
			name:   "a field the API enforces and names",
			quirks: quirkserver.Quirks{RequiredButUndeclared: []string{"value"}},
			want:   true, wantConf: Corroborated,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, tc.quirks)

			report := runAgainst(t, srv, quirkSubject(), writePlan(), "write.required")

			fact, ok := factFor(t, report, "value", FactRequiredByAPI)
			if !ok {
				t.Fatalf("no requiredByApi fact: %v (notes %v)", report.Facts, report.Notes)
			}

			if got := fact.Value.Bool != nil && *fact.Value.Bool; got != tc.want {
				t.Errorf("requiredByApi = %v, want %v (%s)", got, tc.want, fact.Rationale)
			}
			if fact.Confidence != tc.wantConf {
				t.Errorf("confidence = %s, want %s (%s)",
					fact.Confidence, tc.wantConf, fact.Rationale)
			}
		})
	}
}

// TestUnit_Probe_ARefusalThatNamesNothingIsOnlyInferred.
//
// The failure mode this asymmetry exists for: an API that rejects the whole body for its own
// reasons would otherwise have every omitted field recorded as Required at Observed confidence.
func TestUnit_Probe_ARefusalThatNamesNothingIsOnlyInferred(t *testing.T) {
	t.Parallel()

	// A closed enum on a field the fixture sets: omitting *any other* field still sends the
	// invalid value, so every omission is refused and none of the refusals names the omitted
	// field.
	srv := quirkserver.New(t, quirkserver.Quirks{
		ClosedEnum: map[string][]string{"value": {"only-this"}},
	})

	report := runAgainst(t, srv, quirkSubject(), writePlan(), "write.required")

	// The baseline itself is refused, so the honest outcome is a note rather than a pile of
	// Required facts derived from a body the API never accepted.
	if len(report.Facts) != 0 {
		t.Errorf("a refused baseline settles nothing, but produced %v", report.Facts)
	}
	if _, ok := noteMentioning(report, "no omission from it can be attributed"); !ok {
		t.Errorf("the run must say why it concluded nothing: %v", report.Notes)
	}
}

// TestUnit_Probe_ConditionalRequirementIsANoteNotAFact.
//
// Hand-maintained fixup tables in existing providers are full of these -- a port field that
// matters only when a protocol field says tcp. One-field-at-a-time omission from a single fixture
// reports half a truth either way, so disagreement between fixtures produces a note.
func TestUnit_Probe_ConditionalRequirementIsANoteNotAFact(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		ConditionallyRequired: &quirkserver.Conditional{
			WhenField: "objectType", WhenValue: "conditional", Then: "value",
		},
	})

	subj := quirkSubject()
	subj.Fields = append(subj.Fields, Field{
		JSONPath: "objectType", Attribute: "object_type",
		Kind: blueprint.KindString, ComputedOptionalRequired: blueprint.Required, Writable: true,
	})

	plan := Plan{
		Fixtures: []Fixture{
			{Name: "plain", Body: map[string]any{
				"key": "stamped", "value": "a", "objectType": "ordinary",
			}},
			{Name: "conditional", Body: map[string]any{
				"key": "stamped", "value": "b", "objectType": "conditional",
			}},
		},
		Budget: Budget{MaxRequests: 80, MaxCreates: 30},
	}

	report := runAgainst(t, srv, subj, plan, "write.required")

	if _, ok := factFor(t, report, "value", FactRequiredByAPI); ok {
		t.Error("a conditionally required field must not be recorded as a fact either way")
	}

	if _, ok := noteMentioning(report, "requiredness is conditional"); !ok {
		t.Errorf("the disagreement must be reported: %v", report.Notes)
	}
}

// TestUnit_Probe_TheNameFieldsRequirednessIsUnprobed.
//
// An object created without the stamped prefix could not be found by the sweeper, so the session
// refuses that body before it is sent. Silence would leave the field looking probed and
// unremarkable, which for a field the blueprint currently marks Required on an assumption is
// exactly the wrong impression.
func TestUnit_Probe_TheNameFieldsRequirednessIsUnprobed(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	report := runAgainst(t, srv, quirkSubject(), writePlan(), "write.required")

	if _, ok := factFor(t, report, "key", FactRequiredByAPI); ok {
		t.Error("the name field cannot be omitted, so nothing about it can be established")
	}

	note, ok := noteMentioning(report, "its requiredness is therefore unprobed")
	if !ok {
		t.Fatalf("the gap must be reported: %v", report.Notes)
	}
	if note.JSONPath != "key" {
		t.Errorf("the note should name the field, got %q", note.JSONPath)
	}
}

// TestUnit_Probe_AConstantDefaultIsDistinguishedFromADerivedOne.
//
// The dominant false positive in this whole package: treating a derived value as a constant and
// writing it into the blueprint as stringdefault.StaticString, which is then a permanent lie. Each
// row is a different mechanism producing a value for an omitted field, and only the first may
// become a static default.
func TestUnit_Probe_AConstantDefaultIsDistinguishedFromADerivedOne(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		quirks quirkserver.Quirks
		// wantDefault is the literal expected, empty when the value must not become a default.
		wantDefault string
		wantDerived bool
	}{
		{
			name:        "a constant",
			quirks:      quirkserver.Quirks{ConstantDefaults: map[string]any{"colour": "blue"}},
			wantDefault: `"blue"`,
		},
		{
			// A counter: two byte-identical creates disagree, so nothing constant can be
			// claimed. This is the case a single create would get confidently wrong.
			name:        "a counter",
			quirks:      quirkserver.Quirks{CounterDefault: "colour"},
			wantDerived: true,
		},
		{
			// Derived from another field. Identical across the byte-identical pair and different
			// for the third create, which is exactly what the third create is for.
			name:        "derived from the request",
			quirks:      quirkserver.Quirks{DerivedDefaults: map[string]string{"colour": "value"}},
			wantDerived: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, tc.quirks)

			report := runAgainst(t, srv, defaultSubject(), defaultsPlan(), "write.server-default")

			derived, hasDerived := factFor(t, report, "colour", FactDefaultIsDerived)
			def, hasDefault := factFor(t, report, "colour", FactServerDefault)

			if tc.wantDerived {
				if !hasDerived {
					t.Fatalf("no defaultIsDerived fact: %v (notes %v)", report.Facts, report.Notes)
				}
				if derived.Value.Bool == nil || !*derived.Value.Bool {
					t.Errorf("defaultIsDerived = %v", derived.Value)
				}
				if hasDefault {
					t.Errorf("a derived value must not also be recorded as a static default: %v",
						def)
				}

				return
			}

			if !hasDefault {
				t.Fatalf("no serverDefault fact: %v (notes %v)", report.Facts, report.Notes)
			}
			if def.Value.Literal == nil || def.Value.Literal.Raw != tc.wantDefault {
				t.Errorf("serverDefault = %v, want %s", def.Value, tc.wantDefault)
			}
			if hasDerived {
				t.Error("a constant must not also be recorded as derived")
			}

			// The alternatives no number of creates in one tenant can rule out.
			if len(def.Alternatives) < 2 {
				t.Errorf("a default fact must state what it could not rule out: %v",
					def.Alternatives)
			}
		})
	}
}

// TestUnit_Probe_ADefaultIsALiteralNotAString.
//
// Merge writes this into a generated Default, and the difference between the string "3" and the
// number 3 decides whether the emitted code compiles.
func TestUnit_Probe_ADefaultIsALiteralNotAString(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		ConstantDefaults: map[string]any{"rank": 7, "colour": "blue"},
	})

	report := runAgainst(t, srv, defaultSubject(), defaultsPlan(), "write.server-default")

	number, ok := factFor(t, report, "rank", FactServerDefault)
	if !ok {
		t.Fatalf("no fact for rank: %v (notes %v)", report.Facts, report.Notes)
	}
	if number.Value.Literal == nil || number.Value.Literal.Raw != "7" {
		t.Errorf("rank default = %v, want the bare literal 7", number.Value)
	}

	text, _ := factFor(t, report, "colour", FactServerDefault)
	if text.Value.Literal == nil || text.Value.Literal.Raw != `"blue"` {
		t.Errorf("colour default = %v, want a quoted string literal", text.Value)
	}
}

// TestUnit_Probe_AValueOnAFieldTheAPIDiscardsIsNotADefault.
//
// The catalogue's third outcome, and the one real dependency in the whole catalogue: a field the
// API does not store is plain Computed, and the value it reports is not a default a practitioner
// could override. Writing it as a static default would make the provider plan a change it cannot
// apply.
//
// The precondition is a *fact*, so it is satisfied by whatever established it -- here
// write.writable-returned, running earlier in the same tier.
func TestUnit_Probe_AValueOnAFieldTheAPIDiscardsIsNotADefault(t *testing.T) {
	t.Parallel()

	// colour is discarded on write and defaulted when absent, so a read always shows "blue"
	// however the field was sent. Every observation a default probe can make says "constant".
	srv := quirkserver.New(t, quirkserver.Quirks{
		SilentlyDiscards: []string{"colour"},
		ConstantDefaults: map[string]any{"colour": "blue"},
	})

	// Both probes, in registry order, so the dependency is exercised rather than simulated.
	report := runAgainst(t, srv, defaultSubject(), defaultsPlan(), "")

	if fact, ok := factFor(t, report, "colour", FactServerDefault); ok {
		t.Errorf("a value on a field the API discards was recorded as a default: %v", fact)
	}

	if _, ok := noteMentioning(report, "a computed value rather than a default"); !ok {
		t.Errorf("the run must say why no default was recorded: %v", report.Notes)
	}
}

// TestUnit_Probe_OneFixtureCannotRuleOutDerivation.
//
// With one fixture the third create cannot be built, so a value stable across two identical
// creates is Observed rather than Corroborated -- and merge treats the two differently.
func TestUnit_Probe_OneFixtureCannotRuleOutDerivation(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		ConstantDefaults: map[string]any{"colour": "blue"},
	})

	plan := defaultsPlan()
	plan.Fixtures = plan.Fixtures[:1]

	report := runAgainst(t, srv, defaultSubject(), plan, "write.server-default")

	fact, ok := factFor(t, report, "colour", FactServerDefault)
	if !ok {
		t.Fatalf("no fact: %v (notes %v)", report.Facts, report.Notes)
	}

	if fact.Confidence != Observed {
		t.Errorf("confidence = %s, want observed: one fixture cannot rule out derivation from "+
			"the request", fact.Confidence)
	}
}

// TestUnit_Probe_TheFiveOpenPilotGuessesAreSettled is the Phase 4.7c milestone.
//
// The pilot blueprint carries five decisions nothing has tested: colour, accessType and matchType
// are computed_optional on an assumption, and key and objectType are required although the request
// schema declares no required list.
//
// Against a fixture that models each of them, every one either produces a fact or produces a note
// saying precisely why it cannot. Both outcomes are progress; a guess that stays silent is not.
func TestUnit_Probe_TheFiveOpenPilotGuessesAreSettled(t *testing.T) {
	t.Parallel()

	subj := quirkSubject()
	subj.Fields = append(subj.Fields,
		Field{
			JSONPath: "colour", Attribute: "colour", Kind: blueprint.KindString,
			ComputedOptionalRequired: blueprint.ComputedOptional, Writable: true,
		},
		Field{
			JSONPath: "accessType", Attribute: "access_type", Kind: blueprint.KindString,
			ComputedOptionalRequired: blueprint.ComputedOptional, Writable: true,
		},
		Field{
			JSONPath: "matchType", Attribute: "match_type", Kind: blueprint.KindString,
			ComputedOptionalRequired: blueprint.ComputedOptional, Writable: true,
		},
		Field{
			JSONPath: "objectType", Attribute: "object_type", Kind: blueprint.KindString,
			ComputedOptionalRequired: blueprint.Required, Writable: true,
		},
	)

	// The fixtures set key and objectType and omit the three computed_optional fields, which is
	// what makes the two groups observable at all -- exactly how the committed pilot plan is
	// built.
	plan := Plan{
		Fixtures: []Fixture{
			{Name: "first", Body: map[string]any{
				"key": "stamped", "value": "a", "objectType": "test",
			}},
			{Name: "second", Body: map[string]any{
				"key": "stamped", "value": "b", "objectType": "dashboard",
			}},
		},
		DefaultInfluencers: []string{"objectType"},
		Budget:             Budget{MaxRequests: 120, MaxCreates: 40},
	}

	// An API that defaults two of the three unset fields, derives the third from the request, and
	// enforces neither of the two the blueprint marks required.
	srv := quirkserver.New(t, quirkserver.Quirks{
		ConstantDefaults: map[string]any{"accessType": "all"},
		DerivedDefaults:  map[string]string{"colour": "objectType"},
	})

	report := runAgainst(t, srv, subj, plan, "")

	// Each guess, and what settling it looks like.
	settled := []struct {
		path  string
		field FactField
		// note is a substring accepted in place of a fact, for a guess this protocol cannot
		// reach.
		note string
	}{
		{path: "accessType", field: FactServerDefault},
		{path: "colour", field: FactDefaultIsDerived},
		{path: "matchType", note: "assigns no value"},
		{path: "objectType", field: FactRequiredByAPI},
		{path: "key", note: "its requiredness is therefore unprobed"},
	}

	for _, want := range settled {
		if want.field != "" {
			if _, ok := factFor(t, report, want.path, want.field); !ok {
				t.Errorf("%s: no %s fact\nfacts: %v\nnotes: %v",
					want.path, want.field, report.Facts, report.Notes)
			}

			continue
		}

		found := false
		for _, n := range report.Notes {
			if n.JSONPath == want.path && strings.Contains(n.Message, want.note) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: nothing said about it; a guess that stays silent is not progress\n"+
				"notes: %v", want.path, report.Notes)
		}
	}

	// objectType is required in the blueprint and not enforced by this API, which is the finding
	// that matters: the guess was wrong in the direction that breaks configurations.
	if fact, ok := factFor(t, report, "objectType", FactRequiredByAPI); ok {
		if fact.Value.Bool != nil && *fact.Value.Bool {
			t.Error("this fixture does not enforce objectType, so the fact should say so")
		}
	}
}

// immutablePlan declares two candidate values for one field, which is the minimum the
// immutability fact requires: two distinct values, both refused.
func immutablePlan() Plan {
	return Plan{
		Fixtures: []Fixture{
			{Name: "first", Body: map[string]any{"key": "stamped", "value": "original"}},
		},
		Candidates: map[string][]any{
			"value": {"changed-one", "changed-two"},
		},
		Budget: Budget{MaxRequests: 80, MaxCreates: 30},
	}
}

// TestUnit_Probe_ImmutabilityRequiresTwoRefusals.
//
// One refusal is consistent with the value simply being invalid in a way its acceptance on create
// did not reveal. Two distinct values, both refused, is what earns Corroborated -- and merge
// refuses Immutable=true on anything weaker.
func TestUnit_Probe_ImmutabilityRequiresTwoRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		quirks quirkserver.Quirks
		// wantImmutable is the value expected; found says whether the fact should exist at all.
		found         bool
		wantImmutable bool
		wantConf      Confidence
	}{
		{
			name:   "a field that cannot be changed",
			quirks: quirkserver.Quirks{ImmutableAfterCreate: []string{"value"}},
			found:  true, wantImmutable: true, wantConf: Corroborated,
		},
		{
			// One demonstration is enough for false: it recommends nothing.
			name:   "a field that can be changed",
			quirks: quirkserver.Quirks{},
			found:  true, wantImmutable: false, wantConf: Observed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, tc.quirks)

			report := runAgainst(t, srv, quirkSubject(), immutablePlan(), "write.immutability")

			fact, ok := factFor(t, report, "value", FactImmutable)
			if ok != tc.found {
				t.Fatalf("immutable fact found = %v, want %v (facts %v, notes %v)",
					ok, tc.found, report.Facts, report.Notes)
			}
			if !ok {
				return
			}

			if got := fact.Value.Bool != nil && *fact.Value.Bool; got != tc.wantImmutable {
				t.Errorf("immutable = %v, want %v (%s)", got, tc.wantImmutable, fact.Rationale)
			}
			if fact.Confidence != tc.wantConf {
				t.Errorf("confidence = %s, want %s (%s)",
					fact.Confidence, tc.wantConf, fact.Rationale)
			}

			if tc.wantImmutable {
				// Whatever the finding, this probe never recommends a plan modifier: whether
				// Terraform should destroy and recreate is a decision about somebody's
				// infrastructure.
				if _, ok := noteMentioning(report, "no plan modifier is recommended"); !ok {
					t.Errorf("the run must say it recommends nothing: %v", report.Notes)
				}
			}
		})
	}
}

// TestUnit_Probe_TheControlRequestIsLoadBearing.
//
// The step that separates "this field cannot be changed" from "this update request is malformed".
// RequiresExtraFieldOnUpdate is the quirk that exists for exactly this: an update omitting a field
// create never needed is refused, and without the control a probe reads that 4xx as immutability
// and marks a perfectly mutable field as requiring replacement.
func TestUnit_Probe_TheControlRequestIsLoadBearing(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		RequiresExtraFieldOnUpdate: "revision",
	})

	report := runAgainst(t, srv, quirkSubject(), immutablePlan(), "write.immutability")

	// No fact either way. The control failed, so nothing after it means anything.
	if fact, ok := factFor(t, report, "value", FactImmutable); ok {
		t.Errorf("a failing control must produce no immutability fact, got %v", fact)
	}

	note, ok := noteMentioning(report, "the control update")
	if !ok {
		t.Fatalf("the run must say the control failed: %v", report.Notes)
	}
	if !strings.Contains(note.Message, "would say nothing about immutability") {
		t.Errorf("the note should say what the failure invalidates: %q", note.Message)
	}
}

// TestUnit_Probe_AnUnacceptableCandidateProvesNothing.
//
// Before concluding a field cannot be *changed* to a value, the value has to be acceptable to the
// API at all. Without that step, "the field is immutable" and "that value is invalid" are the same
// observation.
func TestUnit_Probe_AnUnacceptableCandidateProvesNothing(t *testing.T) {
	t.Parallel()

	// The candidate values are outside the closed set, so the create that would prove them
	// acceptable is refused.
	srv := quirkserver.New(t, quirkserver.Quirks{
		ClosedEnum: map[string][]string{"value": {"original"}},
	})

	report := runAgainst(t, srv, quirkSubject(), immutablePlan(), "write.immutability")

	if fact, ok := factFor(t, report, "value", FactImmutable); ok {
		t.Errorf("an unproven candidate must produce no immutability fact, got %v", fact)
	}
	if _, ok := noteMentioning(report, "is not an acceptable value"); !ok {
		t.Errorf("the run must say the candidate itself was refused: %v", report.Notes)
	}
}

// TestUnit_Probe_ASilentlyIgnoredUpdateIsNotImmutability.
//
// A 200 that leaves the value alone is a different fact that happens to want similar handling, and
// conflating the two is the classic error. Only this one produces a perpetual diff.
func TestUnit_Probe_ASilentlyIgnoredUpdateIsNotImmutability(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		SilentlyDiscardsOnUpdate: []string{"value"},
	})

	report := runAgainst(t, srv, quirkSubject(), immutablePlan(), "write.immutability")

	if _, ok := factFor(t, report, "value", FactImmutable); ok {
		t.Error("an accepted-and-ignored update must not be recorded as immutability")
	}

	fact, ok := factFor(t, report, "value", FactSilentlyIgnoredOnUpdate)
	if !ok {
		t.Fatalf("no silentlyIgnoredOnUpdate fact: %v (notes %v)", report.Facts, report.Notes)
	}
	if fact.Value.Bool == nil || !*fact.Value.Bool {
		t.Errorf("fact = %v", fact)
	}
}

// TestUnit_Probe_ImmutabilityKeepsTheObjectSweepable.
//
// The name field is a candidate field in the committed pilot plan, and its declared candidates are
// unprefixed. An update that replaced the stamped name with one of them would leave an object the
// prefix sweep could not find -- so a crash between that update and the delete would strand it
// permanently.
func TestUnit_Probe_ImmutabilityKeepsTheObjectSweepable(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	plan := immutablePlan()
	plan.Candidates = map[string][]any{
		// Unprefixed, exactly as the committed pilot plan declares them.
		"key": {"candidate-key-one", "candidate-key-two"},
	}

	report := runAgainst(t, srv, quirkSubject(), plan, "write.immutability")

	// The substitution is reported, because the transcript will not match what the plan declares.
	note, ok := noteMentioning(report, "replaced with two stamped names")
	if !ok {
		t.Fatalf("the substitution must be reported: %v", report.Notes)
	}
	if note.JSONPath != "key" {
		t.Errorf("the note should name the field, got %q", note.JSONPath)
	}

	// And the field is still probed rather than skipped: a stamped name is a distinct value,
	// which is all the protocol needs.
	if _, ok := factFor(t, report, "key", FactImmutable); !ok {
		t.Errorf("the name field should still be probeable: %v", report.Facts)
	}
}

// enumSubject gives the enum probe a field with documented values.
func enumSubject() Subject {
	subj := quirkSubject()

	subj.Fields = append(subj.Fields, Field{
		JSONPath: "mode", Attribute: "mode",
		Kind: blueprint.KindString, ComputedOptionalRequired: blueprint.Optional, Writable: true,
		AllowedValues: []string{"and", "or"},
	})

	return subj
}

func enumPlan() Plan {
	return Plan{
		Fixtures: []Fixture{
			{Name: "first", Body: map[string]any{"key": "stamped", "value": "a", "mode": "and"}},
		},
		Budget: Budget{MaxRequests: 80, MaxCreates: 30},
	}
}

// TestUnit_Probe_AClosedEnumNeedsBothNegativesRefused.
//
// One rejection is not evidence of a closed set: an API can reject a value for a reason that has
// nothing to do with enum membership. Two generated negatives, both refused, is the rule.
func TestUnit_Probe_AClosedEnumNeedsBothNegativesRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		quirks     quirkserver.Quirks
		wantClosed bool
	}{
		{
			name:       "an API that enforces the set",
			quirks:     quirkserver.Quirks{ClosedEnum: map[string][]string{"mode": {"and", "or"}}},
			wantClosed: true,
		},
		{
			// Generated SDKs frequently model enums as open strings, and a routine upstream
			// addition must not become a plan failure.
			name:       "an API that takes anything",
			quirks:     quirkserver.Quirks{},
			wantClosed: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, tc.quirks)

			report := runAgainst(t, srv, enumSubject(), enumPlan(), "write.enum")

			fact, ok := factFor(t, report, "mode", FactValuesClosed)
			if !ok {
				t.Fatalf("no valuesClosed fact: %v (notes %v)", report.Facts, report.Notes)
			}

			if got := fact.Value.Bool != nil && *fact.Value.Bool; got != tc.wantClosed {
				t.Errorf("valuesClosed = %v, want %v (%s)", got, tc.wantClosed, fact.Rationale)
			}

			// The note says which set a validator would come from, and that this probe's
			// answer is what decides whether there is one. Asserted because the old text
			// claimed no validator was ever generated, which stopped being true.
			if _, ok := noteMentioning(report, "comes from the documented set"); !ok {
				t.Errorf("the run must say which set the validator comes from: %v", report.Notes)
			}
		})
	}
}

// TestUnit_Probe_ADocumentedValueTheAPIRejectsIsTheValuableResult.
//
// It means the specification is stale, and a spec-derived validator would have been actively
// harmful. The pilot is already a case in point.
func TestUnit_Probe_ADocumentedValueTheAPIRejectsIsTheValuableResult(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		RejectsDocumentedValue: map[string]string{"mode": "or"},
	})

	report := runAgainst(t, srv, enumSubject(), enumPlan(), "write.enum")

	rejected, ok := factFor(t, report, "mode", FactRejectedValues)
	if !ok {
		t.Fatalf("no enumRejectedDocumented fact: %v (notes %v)", report.Facts, report.Notes)
	}

	if len(rejected.Value.List) != 1 || rejected.Value.List[0] != "or" {
		t.Errorf("rejected = %v, want just [or]", rejected.Value.List)
	}

	// And the one it took is recorded separately, because merge writes it into the description.
	accepted, ok := factFor(t, report, "mode", FactAcceptedValues)
	if !ok {
		t.Fatalf("no enumAccepted fact: %v", report.Facts)
	}
	if len(accepted.Value.List) != 1 || accepted.Value.List[0] != "and" {
		t.Errorf("accepted = %v, want just [and]", accepted.Value.List)
	}

	// A value this tenant refuses may be licence-gated rather than nonexistent.
	if len(rejected.Alternatives) == 0 {
		t.Error("the fact must not claim the value does not exist")
	}
}

// TestUnit_Probe_TheNegativesAreShapedLikeTheDocumentedValues.
//
// The whole reason the negatives are generated rather than fixed. A forty-character string would be
// refused by a length check, and the probe would conclude "closed" having learned nothing about the
// set. This asserts the values that actually went over the wire.
func TestUnit_Probe_TheNegativesAreShapedLikeTheDocumentedValues(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	out, err := Run(context.Background(), RunOptions{
		Mode:      ModeRecord,
		Subject:   enumSubject(),
		Plan:      enumPlan(),
		Only:      "write.enum",
		BaseURL:   srv.BaseURL(),
		Redactor:  testRedactor(t),
		Grant:     &Grant{namePrefix: testPrefix},
		Ledger:    MemoryLedger(),
		ReadDelay: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	documented := map[string]bool{"and": true, "or": true}

	var sent []string

	for _, i := range out.Interactions {
		if i.Request.Method != http.MethodPost {
			continue
		}

		body, ok := i.Request.Body.(map[string]any)
		if !ok {
			continue
		}

		if mode, ok := body["mode"].(string); ok {
			sent = append(sent, mode)
		}
	}

	if len(sent) < len(documented)+negativeEnumCandidates {
		t.Fatalf("only %d value(s) were sent: %v", len(sent), sent)
	}

	for _, v := range sent {
		if documented[v] || strings.EqualFold(v, "and") || strings.EqualFold(v, "or") {
			continue
		}

		// Every negative has to be close enough in shape that a refusal is attributable to
		// membership rather than to length or character class.
		if len(v) > maxCandidateLength {
			t.Errorf("the negative %q is %d characters; a length check would answer instead of "+
				"enum membership", v, len(v))
		}
		if len(v) < 2 {
			t.Errorf("the negative %q is too short to be refused for a reason about the set", v)
		}
	}
}

// TestUnit_Probe_CaseHandlingIsReportedNotAsserted.
//
// The contract's third question, and it has no fact field: recording one would need merge to act on
// it, and the only sound action -- describing the behaviour -- is what a note already does.
func TestUnit_Probe_CaseHandlingIsReportedNotAsserted(t *testing.T) {
	t.Parallel()

	// Case-insensitive: the API lower-cases whatever it is given, so AND is accepted.
	loose := quirkserver.New(t, quirkserver.Quirks{NormalisesCase: []string{"mode"}})

	report := runAgainst(t, loose, enumSubject(), enumPlan(), "write.enum")

	if _, ok := noteMentioning(report, "is not the whole of what it takes"); !ok {
		t.Errorf("an API that accepts a case variant must be reported: %v", report.Notes)
	}

	// Case-sensitive: only the documented spellings are taken.
	strict := quirkserver.New(t, quirkserver.Quirks{
		ClosedEnum: map[string][]string{"mode": {"and", "or"}},
	})

	report = runAgainst(t, strict, enumSubject(), enumPlan(), "write.enum")

	if _, ok := noteMentioning(report, "case-sensitively"); !ok {
		t.Errorf("an API that refuses a case variant must be reported: %v", report.Notes)
	}
}

// TestUnit_Probe_NoDocumentedValuesMeansNothingToCheck: a field the specification says nothing
// about is not a field this protocol has anything to say about, and it says so rather than
// producing an empty result that reads like a clean bill of health.
func TestUnit_Probe_NoDocumentedValuesMeansNothingToCheck(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	report := runAgainst(t, srv, quirkSubject(), writePlan(), "write.enum")

	if len(report.Facts) != 0 {
		t.Errorf("facts = %v", report.Facts)
	}
	if _, ok := noteMentioning(report, "no specification claim to check"); !ok {
		t.Errorf("the gap must be reported: %v", report.Notes)
	}
	if srv.Requests() > 1 {
		t.Errorf("%d request(s) issued with nothing to check", srv.Requests())
	}
}

// normaliseSubject has a string field and a collection field, which are the two types the
// transforms apply to.
func normaliseSubject() Subject {
	subj := quirkSubject()

	subj.Fields = append(subj.Fields, Field{
		JSONPath: "tags", Attribute: "tags",
		Kind: blueprint.KindList, ComputedOptionalRequired: blueprint.Optional, Writable: true,
	})

	return subj
}

// TestUnit_Probe_AnIdentifiedTransformIsObservedAndAVagueOneIsSuspected.
//
// The highest-value class of fact in the catalogue: server normalisation is the direct cause of a
// perpetual diff. A specific named transform is Observed; "changed somehow" is only Suspected,
// because merge writes the first into a description a human acts on.
func TestUnit_Probe_AnIdentifiedTransformIsObservedAndAVagueOneIsSuspected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		quirks  quirkserver.Quirks
		path    string
		wantSay string
	}{
		{
			name:    "whitespace",
			quirks:  quirkserver.Quirks{TrimsWhitespace: []string{"value"}},
			path:    "value",
			wantSay: "surrounding whitespace is stripped",
		},
		{
			name:    "case",
			quirks:  quirkserver.Quirks{NormalisesCase: []string{"value"}},
			path:    "value",
			wantSay: "lower-cased",
		},
		{
			// The one existing providers carry runtime helpers for, which is the wrong layer.
			name:    "collection order",
			quirks:  quirkserver.Quirks{SortsLists: []string{"tags"}},
			path:    "tags",
			wantSay: "re-sorted",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, tc.quirks)

			report := runAgainst(t, srv, normaliseSubject(), writePlan(), "write.normalisation")

			fact, ok := factFor(t, report, tc.path, FactNormalisation)
			if !ok {
				t.Fatalf("no normalisation fact for %s: %v (notes %v)",
					tc.path, report.Facts, report.Notes)
			}

			if !strings.Contains(fact.Value.Text, tc.wantSay) {
				t.Errorf("fact says %q, want it to mention %q", fact.Value.Text, tc.wantSay)
			}
			if fact.Confidence != Observed {
				t.Errorf("confidence = %s, want observed for an identified transform",
					fact.Confidence)
			}

			// Named in the framework's own terms, because that is what a provider author reaches
			// for.
			if _, ok := noteMentioning(report, "semantic-equality"); !ok {
				t.Errorf("an identified transform should point at where it is suppressed "+
					"properly: %v", report.Notes)
			}
		})
	}
}

// TestUnit_Probe_AnApiThatChangesNothingProducesNoNormalisationFact.
//
// Absence of a fact is the correct outcome, and it has to be distinguishable from a probe that did
// not run: the run still costs its creates and still reports them.
func TestUnit_Probe_AnApiThatChangesNothingProducesNoNormalisationFact(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	report := runAgainst(t, srv, normaliseSubject(), writePlan(), "write.normalisation")

	for _, f := range report.Facts {
		if f.Field == FactNormalisation {
			t.Errorf("an API that stores values verbatim produced a normalisation fact: %v", f)
		}
	}

	var outcome ProbeOutcome
	for _, o := range report.Probes {
		if o.Name == "write.normalisation" {
			outcome = o
		}
	}

	if outcome.Requests == 0 {
		t.Error("the probe must have actually sent the awkward values")
	}
}

// TestUnit_Probe_ARefusedAwkwardValueIsNoObservation.
//
// A rejected value tells you nothing about what the server would have done with an accepted one, so
// it is a note rather than evidence either way.
func TestUnit_Probe_ARefusedAwkwardValueIsNoObservation(t *testing.T) {
	t.Parallel()

	// A closed enum on the field the transforms target, so every awkward value is refused.
	srv := quirkserver.New(t, quirkserver.Quirks{
		ClosedEnum: map[string][]string{"value": {"a", "b"}},
	})

	report := runAgainst(t, srv, normaliseSubject(), writePlan(), "write.normalisation")

	for _, f := range report.Facts {
		if f.Field == FactNormalisation {
			t.Errorf("a refused value produced a fact: %v", f)
		}
	}

	if _, ok := noteMentioning(report, "so nothing was observed about that transform"); !ok {
		t.Errorf("the refusal must be reported: %v", report.Notes)
	}
}

// TestUnit_Probe_TheNameFieldCannotCarryEveryAwkwardShape.
//
// Leading whitespace and mixed case both destroy the sweeper's prefix, and a body whose name has
// lost it is refused before it is sent -- correctly, since the object could not then be found. The
// gap is reported rather than left looking probed.
func TestUnit_Probe_TheNameFieldCannotCarryEveryAwkwardShape(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{TrimsWhitespace: []string{"key"}})

	report := runAgainst(t, srv, normaliseSubject(), writePlan(), "write.normalisation")

	note, ok := noteMentioning(report, "the awkward shape would destroy it")
	if !ok {
		t.Fatalf("the gap must be reported: %v", report.Notes)
	}
	if note.JSONPath != "key" {
		t.Errorf("the note should name the field, got %q", note.JSONPath)
	}
}

// sideEffectSubject adds a boolean trigger and a field the server can couple to it.
func sideEffectSubject() Subject {
	subj := quirkSubject()

	subj.Fields = append(subj.Fields,
		Field{
			JSONPath: "enabled", Attribute: "enabled",
			Kind: blueprint.KindBool, ComputedOptionalRequired: blueprint.Optional, Writable: true,
		},
		Field{
			JSONPath: "alsoEnabled", Attribute: "also_enabled",
			Kind: blueprint.KindBool, ComputedOptionalRequired: blueprint.Computed,
		},
	)

	return subj
}

func sideEffectPlan() Plan {
	return Plan{
		Fixtures: []Fixture{
			{Name: "first", Body: map[string]any{"key": "stamped", "value": "a"}},
		},
		DefaultInfluencers: []string{"enabled"},
		Budget:             Budget{MaxRequests: 80, MaxCreates: 30},
	}
}

// TestUnit_Probe_ASideEffectIsConfirmedByPerturbingTheTrigger.
//
// The class of quirk a human would never guess from a specification and a prober genuinely can
// find: enabling one measurement silently enables another. Confirmation requires perturbing the
// trigger, because a field that merely changes could be a server default.
func TestUnit_Probe_ASideEffectIsConfirmedByPerturbingTheTrigger(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		WriteSideEffects: map[string]string{"enabled": "alsoEnabled"},
	})

	report := runAgainst(t, srv, sideEffectSubject(), sideEffectPlan(), "write.side-effect")

	fact, ok := factFor(t, report, "enabled", FactSideEffect)
	if !ok {
		t.Fatalf("no sideEffect fact: %v (notes %v)", report.Facts, report.Notes)
	}

	if !strings.Contains(fact.Value.Text, "alsoEnabled") {
		t.Errorf("the fact should name the coupled field, got %q", fact.Value.Text)
	}

	// Inferred even after confirmation: "the server set it in response to this request" and "the
	// server always sets it" are different claims, and one perturbation establishes only the first.
	if fact.Confidence != Inferred {
		t.Errorf("confidence = %s, want inferred", fact.Confidence)
	}
	if len(fact.Alternatives) < 2 {
		t.Errorf("the fact must state what one perturbation did not establish: %v",
			fact.Alternatives)
	}
}

// TestUnit_Probe_NoCouplingIsReportedAsNone: an API with no coupling must produce a note, not
// silence -- silence is indistinguishable from a probe that did not run.
func TestUnit_Probe_NoCouplingIsReportedAsNone(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	report := runAgainst(t, srv, sideEffectSubject(), sideEffectPlan(), "write.side-effect")

	if _, ok := factFor(t, report, "enabled", FactSideEffect); ok {
		t.Error("an API with no coupling must not produce a side-effect fact")
	}
	if _, ok := noteMentioning(report, "no coupling was observed"); !ok {
		t.Errorf("the absence must be reported: %v", report.Notes)
	}
}

// TestUnit_Probe_NoDeclaredTriggerMeansNothingToPerturb.
//
// The plan is where knowledge a probe cannot discover comes from, and which fields might be coupled
// is exactly that. Without one there is no experiment to run, and the run says so.
func TestUnit_Probe_NoDeclaredTriggerMeansNothingToPerturb(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		WriteSideEffects: map[string]string{"enabled": "alsoEnabled"},
	})

	plan := sideEffectPlan()
	plan.DefaultInfluencers = nil

	report := runAgainst(t, srv, sideEffectSubject(), plan, "write.side-effect")

	if len(report.Facts) != 0 {
		t.Errorf("facts = %v", report.Facts)
	}
	if _, ok := noteMentioning(report, "declare one under defaultInfluencers"); !ok {
		t.Errorf("the run must say what would let it work: %v", report.Notes)
	}
}

// TestUnit_Probe_EveryProbeInTheCatalogueIsImplemented is the Phase 4.7e milestone.
//
// Nine mutating protocols and six read-only ones, all with bodies. From here the catalogue is
// complete and what remains is pointing it at a real tenant.
func TestUnit_Probe_EveryProbeInTheCatalogueIsImplemented(t *testing.T) {
	t.Parallel()

	for _, p := range MutatingProbes("") {
		if !builtMutatingProbes[p.Name()] {
			t.Errorf("%s is still unimplemented", p.Name())
		}
	}

	if len(builtMutatingProbes) != len(MutatingProbes("")) {
		t.Errorf("%d probes are recorded as built and %d are registered",
			len(builtMutatingProbes), len(MutatingProbes("")))
	}
}

// TestUnit_Probe_TheWholeCatalogueRunsAndSweepsClean.
//
// Every protocol against one fixture, then the tenant checked empty. This is the assertion that
// matters most before a live run: nine probes creating objects through the ledger, per-probe
// release, and the sweep, with nothing left behind.
func TestUnit_Probe_TheWholeCatalogueRunsAndSweepsClean(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		// A fixture that misbehaves in several ways at once, because that is what a real API
		// does.
		SilentlyDiscards:          []string{"modifiedDate"},
		NormalisesCase:            []string{"value"},
		ConstantDefaults:          map[string]any{"colour": "blue"},
		EventuallyConsistentReads: 1,
		PutClearsOmitted:          true,
	})

	subj := normaliseSubject()
	subj.Fields = append(subj.Fields, Field{
		JSONPath: "colour", Attribute: "colour",
		Kind: blueprint.KindString, ComputedOptionalRequired: blueprint.Optional, Writable: true,
	})

	plan := Plan{
		Fixtures: []Fixture{
			{Name: "first", Body: map[string]any{"key": "stamped", "value": "a"}},
			{Name: "second", Body: map[string]any{"key": "stamped", "value": "b"}},
		},
		Candidates:         map[string][]any{"value": {"one", "two"}},
		DefaultInfluencers: []string{"value"},
		Budget:             Budget{MaxRequests: 300, MaxCreates: 100},
	}

	report := runAgainst(t, srv, subj, plan, "")

	// Every mutating probe ran and reported an outcome. A probe missing from the report is the one
	// failure a reader cannot detect.
	seen := map[string]bool{}
	for _, o := range report.Probes {
		seen[o.Name] = true

		if o.Status == "failed" {
			t.Errorf("%s failed: %s", o.Name, o.Reason)
		}
	}

	for _, p := range MutatingProbes("") {
		if !seen[p.Name()] {
			t.Errorf("%s is missing from the report", p.Name())
		}
	}

	if report.Sweep == nil {
		t.Fatal("a mutating run must report what cleaning up did")
	}
	if len(report.Orphans) != 0 {
		t.Errorf("orphans = %+v", report.Orphans)
	}
	if len(report.Facts) == 0 {
		t.Error("the whole catalogue against a misbehaving fixture established nothing")
	}
}
