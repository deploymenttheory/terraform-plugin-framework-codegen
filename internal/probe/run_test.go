package probe

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe/quirkserver"
)

// TestMain denies the default transport.
//
// A probe that reached the network by any route other than its session would fail here rather
// than quietly succeeding, which is what makes the "only session.go builds a client"
// structural test more than a style rule.
func TestMain(m *testing.M) {
	http.DefaultTransport = cassette.DenyTransport{}
	os.Exit(m.Run())
}

// quirkSubject builds a subject pointed at a quirk server.
//
// The paths are the server's, and the fields are the ones its objects carry, so a probe sees
// the same shape it would see against a real API driven by a blueprint.
func quirkSubject() Subject {
	return Subject{
		Resource:           "tag",
		CollectionTemplate: "/things",
		ItemTemplate:       "/things/{id}",
		Create:             &Op{Method: "POST", PathTemplate: "/things", SuccessCodes: []int{201}},
		Read:               &Op{Method: "GET", PathTemplate: "/things/{id}", SuccessCodes: []int{200}},
		Update:             &Op{Method: "PUT", PathTemplate: "/things/{id}", SuccessCodes: []int{200}},
		Delete:             &Op{Method: "DELETE", PathTemplate: "/things/{id}", SuccessCodes: []int{204}},
		NameField:          "key",
		Fields: []Field{
			{JSONPath: "id", Kind: blueprint.KindString, Presence: blueprint.Computed},
			{JSONPath: "key", Kind: blueprint.KindString, Presence: blueprint.Required, Writable: true},
			{JSONPath: "value", Kind: blueprint.KindString, Presence: blueprint.Optional, Writable: true},
			{JSONPath: "modifiedDate", Kind: blueprint.KindString, Presence: blueprint.Computed},
		},
	}
}

func testRedactor(t *testing.T) *cassette.Redactor {
	t.Helper()

	r, err := cassette.NewRedactor(nil, nil)
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}
	return r
}

// recordAgainst runs the read-only tier against a quirk server and returns the result.
//
// A real transport is used here, which is the one place in this package that legitimately
// reaches the network -- to localhost, at a server the test just started.
func recordAgainst(t *testing.T, s *quirkserver.Server, only string) RunResult {
	t.Helper()

	rec, err := cassette.NewRecordingTransport(&http.Transport{}, testRedactor(t), nil)
	if err != nil {
		t.Fatalf("NewRecordingTransport: %v", err)
	}

	session, err := newHTTPSession(SessionConfig{
		Transport:          rec,
		BaseURL:            s.URL,
		CollectionTemplate: "/things",
		ItemTemplate:       "/things/{id}",
	})
	if err != nil {
		t.Fatalf("newHTTPSession: %v", err)
	}

	var report Report
	runReadProbes(context.Background(), session, RunOptions{Subject: quirkSubject(), Only: only}, &report)
	report.Budget = session.budget.report()

	interactions, err := rec.Interactions()
	if err != nil {
		t.Fatalf("Interactions: %v", err)
	}

	attachEvidence(&report, interactions)
	report.Sort()

	return RunResult{Report: report, Interactions: interactions}
}

// TestUnit_Probe_ReplayReproducesFactsWithNoNetwork is the Phase 4.4 milestone.
//
// Record against a quirk server, then re-derive the facts from the transcript alone with the
// network denied, and get exactly the same facts. This is the purity test: if derivation
// depended on anything outside the transcript -- a clock, an environment variable, map
// iteration order -- the two would differ, and every committed fact would be unreproducible.
func TestUnit_Probe_ReplayReproducesFactsWithNoNetwork(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		VolatileFields:            []string{"modifiedDate"},
		IgnoresUnknownQueryParams: true,
		TypedQueryParams:          []string{"limit"},
	})
	srv.Seed(map[string]any{"key": "existing", "value": "v"})

	live := recordAgainst(t, srv, "")

	if len(live.Report.Facts) == 0 {
		t.Fatal("recording produced no facts, so the replay comparison would be vacuous")
	}
	if len(live.Interactions) == 0 {
		t.Fatal("recording produced no interactions")
	}

	// Replayed with a cassette-backed transport. http.DefaultTransport is DenyTransport, so
	// anything escaping the cassette fails.
	replayed, err := Run(context.Background(), RunOptions{
		Mode:         ModeReplay,
		Subject:      quirkSubject(),
		Interactions: live.Interactions,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	if err := VerifyFacts(replayed.Report.Facts, live.Report.Facts); err != nil {
		t.Errorf("replay did not reproduce the recorded facts: %v", err)
	}
}

// TestUnit_Probe_ReplayIsRepeatable: replaying the same transcript twice must give the same
// facts, or "derivation is a pure function of the transcript" is false in a way the milestone
// test above would not catch.
func TestUnit_Probe_ReplayIsRepeatable(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		VolatileFields:   []string{"modifiedDate"},
		TypedQueryParams: []string{"limit"},
	})
	srv.Seed(map[string]any{"key": "existing"})

	live := recordAgainst(t, srv, "")

	first, err := Run(context.Background(), RunOptions{
		Mode: ModeReplay, Subject: quirkSubject(), Interactions: live.Interactions,
	})
	if err != nil {
		t.Fatalf("first replay: %v", err)
	}

	second, err := Run(context.Background(), RunOptions{
		Mode: ModeReplay, Subject: quirkSubject(), Interactions: live.Interactions,
	})
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}

	if err := VerifyFacts(first.Report.Facts, second.Report.Facts); err != nil {
		t.Errorf("two replays of one transcript differ: %v", err)
	}
}

// TestUnit_Probe_UnknownParamTolerance covers both directions against known ground truth.
func TestUnit_Probe_UnknownParamTolerance(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		quirks quirkserver.Quirks
		want   bool
	}{
		"tolerant": {quirkserver.Quirks{IgnoresUnknownQueryParams: true}, true},
		"strict":   {quirkserver.Quirks{}, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, tc.quirks)

			got := recordAgainst(t, srv, "read.unknown-param")

			fact := findFact(t, got.Report.Facts, "", FactUnknownParamTolerated)
			if fact.Value.Bool == nil || *fact.Value.Bool != tc.want {
				t.Errorf("tolerated = %v, want %v", fact.Value, tc.want)
			}
			// The gateway caveat has to be stated, because tolerance of query parameters is
			// only weak evidence about request bodies.
			if len(fact.Alternatives) == 0 {
				t.Error("the fact must name what it did not rule out")
			}
		})
	}
}

// TestUnit_Probe_NotFoundShape covers the case that matters most: internal/ingest currently
// hardcodes NotFoundIsSuccess to true with no evidence, and an API answering 403 would make
// the generated delete swallow a real failure.
func TestUnit_Probe_NotFoundShape(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		quirks quirkserver.Quirks
		want   bool
	}{
		"404": {quirkserver.Quirks{}, true},
		"403": {quirkserver.Quirks{NotFoundStatus: http.StatusForbidden}, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, tc.quirks)

			got := recordAgainst(t, srv, "read.not-found-shape")

			fact := findFact(t, got.Report.Facts, "", FactNotFoundIsSuccess)
			if fact.Value.Bool == nil || *fact.Value.Bool != tc.want {
				t.Errorf("notFoundIsSuccess = %v, want %v", fact.Value, tc.want)
			}

			if name == "403" {
				// The genuinely ambiguous case has to say so: a 403 may mean the object
				// exists in another tenant.
				joined := strings.Join(fact.Alternatives, " ")
				if !strings.Contains(joined, "another tenant") {
					t.Errorf("the 403 ambiguity must be stated: %v", fact.Alternatives)
				}
			}
		})
	}
}

// TestUnit_Probe_ErrorEnvelopeIsIdentified covers all four shapes.
//
// Every later probe's classifier depends on this: telling "rejected because immutable" from
// "rejected because the token expired" is what makes the immutability protocol possible.
func TestUnit_Probe_ErrorEnvelopeIsIdentified(t *testing.T) {
	t.Parallel()

	for _, kind := range []quirkserver.EnvelopeKind{
		quirkserver.EnvelopeProblem,
		quirkserver.EnvelopeLegacy,
		quirkserver.EnvelopeEmpty,
	} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			srv := quirkserver.New(t, quirkserver.Quirks{
				ErrorEnvelope:    kind,
				TypedQueryParams: []string{"limit"},
			})

			got := recordAgainst(t, srv, "read.error-envelope")

			var found bool
			for _, f := range got.Report.Facts {
				if f.Field == FactErrorEnvelope && strings.Contains(f.Value.Text, string(kind)) {
					found = true
				}
			}
			if !found {
				t.Errorf("the %s envelope was not identified: %v", kind, got.Report.Facts)
			}
		})
	}

	// The OAuth envelope is returned for auth failures, which must stop the run rather than
	// be recorded as an observation -- every later response would be about the token.
	t.Run("oauth stops the run", func(t *testing.T) {
		t.Parallel()

		srv := quirkserver.New(t, quirkserver.Quirks{
			ErrorEnvelope:  quirkserver.EnvelopeOAuth,
			NotFoundStatus: http.StatusUnauthorized,
		})

		got := recordAgainst(t, srv, "read.not-found-shape")

		var failed bool
		for _, p := range got.Report.Probes {
			if p.Status == "failed" && strings.Contains(p.Reason, "credential") {
				failed = true
			}
		}
		if !failed {
			t.Errorf("a credential failure must stop the run: %+v", got.Report.Probes)
		}
	})
}

// TestUnit_Probe_VolatileOnRead: a diff across three reads is Observed; no diff produces no
// fact at all, because stability over milliseconds is not evidence of stability.
func TestUnit_Probe_VolatileOnRead(t *testing.T) {
	t.Parallel()

	t.Run("volatile field is found", func(t *testing.T) {
		t.Parallel()

		srv := quirkserver.New(t, quirkserver.Quirks{VolatileFields: []string{"modifiedDate"}})
		srv.Seed(map[string]any{"key": "k"})

		got := recordAgainst(t, srv, "read.volatile")

		fact := findFact(t, got.Report.Facts, "modifiedDate", FactVolatile)
		if fact.Value.Bool == nil || !*fact.Value.Bool {
			t.Errorf("modifiedDate should be volatile: %v", fact.Value)
		}
		if fact.Confidence != Observed {
			t.Errorf("confidence = %s, want observed", fact.Confidence)
		}
	})

	t.Run("a stable object yields no fact", func(t *testing.T) {
		t.Parallel()

		srv := quirkserver.New(t, quirkserver.Quirks{})
		srv.Seed(map[string]any{"key": "k"})

		got := recordAgainst(t, srv, "read.volatile")

		for _, f := range got.Report.Facts {
			if f.Field == FactVolatile {
				t.Errorf("a stable object must produce no volatility fact, got %v", f)
			}
		}
		// And it says so, rather than being silent.
		if len(got.Report.Notes) == 0 {
			t.Error("the absence of a fact should be explained in a note")
		}
	})

	t.Run("an empty tenant yields no fact", func(t *testing.T) {
		t.Parallel()

		// An empty collection is the normal state of a good sandbox, so this must degrade
		// cleanly rather than erroring.
		srv := quirkserver.New(t, quirkserver.Quirks{VolatileFields: []string{"modifiedDate"}})

		got := recordAgainst(t, srv, "read.volatile")

		for _, p := range got.Report.Probes {
			if p.Status == "failed" {
				t.Errorf("an empty tenant must not fail the probe: %+v", p)
			}
		}
	})
}

// TestUnit_Probe_ReturnedOnReadWeakIsAlwaysSuspected.
//
// Deliberately capped, because an absent field could mean never-returned, null-for-this-object
// or expansion-gated, and one read distinguishes none of them. Suspected facts are never
// merged, so this probe can only ever prompt a human to look.
func TestUnit_Probe_ReturnedOnReadWeakIsAlwaysSuspected(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	// Seeded without "value", so the schema knows a field the object does not carry.
	srv.Seed(map[string]any{"key": "k"})

	got := recordAgainst(t, srv, "read.returned-weak")

	var seen int
	for _, f := range got.Report.Facts {
		if f.Field != FactReturnedOnRead {
			continue
		}
		seen++
		if f.Confidence != Suspected {
			t.Errorf("%s: confidence = %s, want suspected", f.JSONPath, f.Confidence)
		}
		if len(f.Alternatives) < 2 {
			t.Errorf("%s: must name the explanations it did not rule out, got %v",
				f.JSONPath, f.Alternatives)
		}
	}

	if seen == 0 {
		t.Error("an absent field should have been noticed")
	}
}

// TestUnit_Probe_BudgetStopsTheRun: the cap is enforced in the session, before the request,
// and hitting it stops the run rather than continuing past a limit somebody set deliberately.
func TestUnit_Probe_BudgetStopsTheRun(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	srv.Seed(map[string]any{"key": "k"})

	rec, err := cassette.NewRecordingTransport(&http.Transport{}, testRedactor(t), nil)
	if err != nil {
		t.Fatalf("NewRecordingTransport: %v", err)
	}

	session, err := newHTTPSession(SessionConfig{
		Transport:          rec,
		BaseURL:            srv.URL,
		CollectionTemplate: "/things",
		ItemTemplate:       "/things/{id}",
		Budget:             Budget{MaxRequests: 2},
	})
	if err != nil {
		t.Fatalf("newHTTPSession: %v", err)
	}

	var report Report
	runReadProbes(context.Background(), session, RunOptions{Subject: quirkSubject()}, &report)

	if session.budget.report().Requests > 3 {
		t.Errorf("the run spent %d requests against a cap of 2", session.budget.report().Requests)
	}

	var stopped bool
	for _, n := range report.Notes {
		if strings.Contains(n.Message, "stopped early") {
			stopped = true
		}
	}
	if !stopped {
		t.Errorf("hitting the budget must be reported: %+v", report.Notes)
	}
}

// TestUnit_Probe_MutatingTierIsReportedAsSkipped.
//
// Explicitly, not by omission. A read-only run and a full run must not produce reports that
// look alike, or a reader cannot tell that two thirds of the catalogue never executed.
func TestUnit_Probe_MutatingTierIsReportedAsSkipped(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	live := recordAgainst(t, srv, "")

	got, err := Run(context.Background(), RunOptions{
		Mode: ModeReplay, Subject: quirkSubject(), Interactions: live.Interactions,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	var skipped int
	for _, p := range got.Report.Probes {
		if p.Kind == KindMutating {
			if p.Status != "skipped" {
				t.Errorf("%s: status = %s, want skipped", p.Name, p.Status)
			}
			if p.Reason == "" {
				t.Errorf("%s: a skip must say why", p.Name)
			}
			skipped++
		}
	}

	if skipped != len(MutatingProbes("")) {
		t.Errorf("%d mutating probes reported, want %d", skipped, len(MutatingProbes("")))
	}
}

// TestUnit_Probe_EvidenceCitesRealInteractions.
//
// A fact whose citation names a file that does not exist looks checkable and is not, which is
// worse than a weaker fact. So citations are rewritten against the transcript, and anything
// unresolvable downgrades the fact rather than being left dangling.
func TestUnit_Probe_EvidenceCitesRealInteractions(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		VolatileFields:   []string{"modifiedDate"},
		TypedQueryParams: []string{"limit"},
	})
	srv.Seed(map[string]any{"key": "k"})

	got := recordAgainst(t, srv, "")

	real := map[string]bool{}
	for _, i := range got.Interactions {
		real[i.ID] = true
	}

	for _, f := range got.Report.Facts {
		for _, cited := range f.Evidence {
			if !real[cited] {
				t.Errorf("%s cites %q, which is not in the transcript", factKey(f), cited)
			}
		}
		// An observed-or-better fact with no evidence would fail Fact.Validate, so the
		// downgrade has to have happened.
		if len(f.Evidence) == 0 && f.Confidence.AtLeast(Observed) {
			t.Errorf("%s has no evidence but claims %s", factKey(f), f.Confidence)
		}
	}
}

// TestUnit_Probe_EveryDerivedFactValidates: the facts a run produces must satisfy the same
// gate a hand-edited facts document does, or Validate is only checking other people's work.
func TestUnit_Probe_EveryDerivedFactValidates(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		VolatileFields:            []string{"modifiedDate"},
		IgnoresUnknownQueryParams: true,
		TypedQueryParams:          []string{"limit"},
	})
	srv.Seed(map[string]any{"key": "k"})

	got := recordAgainst(t, srv, "")

	for _, f := range got.Report.Facts {
		if f.Confidence == Suspected {
			// Suspected facts are report-only and may legitimately lack evidence after a
			// downgrade.
			continue
		}
		if err := f.Validate(); err != nil {
			t.Errorf("a derived fact does not validate: %v", err)
		}
	}
}

func TestUnit_Probe_RunRejectsBadOptions(t *testing.T) {
	t.Parallel()

	tests := map[string]RunOptions{
		"record with no redactor": {Mode: ModeRecord, Subject: quirkSubject(), BaseURL: "https://x"},
		"replay with no cassette": {Mode: ModeReplay, Subject: quirkSubject()},
		"sweep is not a run":      {Mode: ModeSweep, Subject: quirkSubject()},
		"unknown mode":            {Mode: "telepathy", Subject: quirkSubject()},
	}

	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := Run(context.Background(), opts); !errors.Is(err, ErrInvalidPlan) {
				t.Errorf("error = %v, want ErrInvalidPlan", err)
			}
		})
	}
}

func TestUnit_Probe_VerifyFactsDetectsDrift(t *testing.T) {
	t.Parallel()

	base := []Fact{{
		Resource: "tag", JSONPath: "colour", Field: FactWritable,
		Value: BoolValue(true), Confidence: Observed,
	}}

	if err := VerifyFacts(base, base); err != nil {
		t.Errorf("identical facts should verify: %v", err)
	}

	tests := map[string][]Fact{
		"a missing fact": {},
		"a changed value": {{
			Resource: "tag", JSONPath: "colour", Field: FactWritable,
			Value: BoolValue(false), Confidence: Observed,
		}},
		"a changed confidence": {{
			Resource: "tag", JSONPath: "colour", Field: FactWritable,
			Value: BoolValue(true), Confidence: Suspected,
		}},
		"a different field": {{
			Resource: "tag", JSONPath: "colour", Field: FactImmutable,
			Value: BoolValue(true), Confidence: Observed,
		}},
	}

	for name, derived := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := VerifyFacts(derived, base); !errors.Is(err, ErrReplayMismatch) {
				t.Errorf("error = %v, want ErrReplayMismatch", err)
			}
		})
	}
}

func TestUnit_Probe_HostOf(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"https://api.example.com":    "api.example.com",
		"https://api.example.com/v7": "api.example.com",
		"http://localhost:8080/tags": "localhost:8080",
		"api.example.com":            "api.example.com",
	}

	for in, want := range tests {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// findFact locates a fact, failing the test if it is absent.
func findFact(t *testing.T, facts []Fact, jsonPath string, field FactField) Fact {
	t.Helper()

	for _, f := range facts {
		if f.JSONPath == jsonPath && f.Field == field {
			return f
		}
	}

	t.Fatalf("no %s fact for %q in %v", field, jsonPath, facts)

	return Fact{}
}

// recordAgainstPrefixed records against a server serving under a base path.
//
// The distinction from recordAgainst matters: the session's base URL carries the prefix, so the
// paths a probe asks for and the paths a cassette records differ by exactly that prefix. Two bugs
// lived in that gap.
func recordAgainstPrefixed(t *testing.T, s *quirkserver.Server, prefix string) RunResult {
	t.Helper()

	rec, err := cassette.NewRecordingTransport(&http.Transport{}, testRedactor(t), nil)
	if err != nil {
		t.Fatalf("NewRecordingTransport: %v", err)
	}

	session, err := newHTTPSession(SessionConfig{
		Transport:          rec,
		BaseURL:            s.URL + prefix,
		CollectionTemplate: "/things",
		ItemTemplate:       "/things/{id}",
	})
	if err != nil {
		t.Fatalf("newHTTPSession: %v", err)
	}

	var report Report
	runReadProbes(context.Background(), session, RunOptions{Subject: quirkSubject()}, &report)

	interactions, err := rec.Interactions()
	if err != nil {
		t.Fatalf("Interactions: %v", err)
	}

	attachEvidence(&report, interactions)
	report.Sort()

	return RunResult{Report: report, Interactions: interactions}
}

// TestUnit_Probe_EvidenceResolvesAcrossABasePath is a regression test for a bug found only against
// a live API.
//
// A probe cites the path it asked for, which is relative to the base URL; a cassette records the
// full request path. When the endpoint carries a prefix the two differ by exactly that prefix, and
// equality matching resolved nothing -- so **every fact in the run was silently downgraded to
// Suspected**. Correct facts, all quietly marked unverifiable, and no test caught it because an
// httptest base URL has no path.
func TestUnit_Probe_EvidenceResolvesAcrossABasePath(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		BasePath:                  "/v7",
		IgnoresUnknownQueryParams: true,
		TypedQueryParams:          []string{"limit"},
	})
	srv.Seed(map[string]any{"key": "existing"})

	got := recordAgainstPrefixed(t, srv, "/v7")

	if len(got.Report.Facts) == 0 {
		t.Fatal("no facts were produced, so this test proves nothing")
	}

	real := map[string]bool{}
	for _, i := range got.Interactions {
		real[i.ID] = true
	}

	var observed int
	for _, f := range got.Report.Facts {
		for _, cited := range f.Evidence {
			if !real[cited] {
				t.Errorf("%s cites %q, which is not in the transcript", factKey(f), cited)
			}
		}
		if f.Confidence.AtLeast(Observed) {
			observed++
			if len(f.Evidence) == 0 {
				t.Errorf("%s claims %s with no evidence", factKey(f), f.Confidence)
			}
		}
	}

	// The bug's signature was zero observed facts: everything downgraded.
	if observed == 0 {
		t.Error("every fact was downgraded; evidence citations are not resolving across the base path")
	}
}

// TestUnit_Probe_ReplayReproducesFactsAcrossABasePath is the regression test for the second bug.
//
// A cassette stores full request paths. Replaying a recording made against a prefixed endpoint has
// to reproduce that prefix, or every request mismatches and no fact survives -- which is what
// happened live, with a report reading "0 ok, 6 failed" against a transcript that was perfectly
// good.
func TestUnit_Probe_ReplayReproducesFactsAcrossABasePath(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{
		BasePath:                  "/v7",
		IgnoresUnknownQueryParams: true,
		TypedQueryParams:          []string{"limit"},
	})
	srv.Seed(map[string]any{"key": "existing"})

	live := recordAgainstPrefixed(t, srv, "/v7")

	// The prefix supplied the way the CLI supplies it: from the cassette's recorded metadata.
	replayed, err := Run(context.Background(), RunOptions{
		Mode:         ModeReplay,
		Subject:      quirkSubject(),
		BaseURL:      replayHost + "/v7",
		Interactions: live.Interactions,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	if err := VerifyFacts(replayed.Report.Facts, live.Report.Facts); err != nil {
		t.Errorf("replay across a base path did not reproduce the facts: %v", err)
	}

	// And without the prefix it must fail loudly rather than quietly producing nothing -- the
	// failure mode that made the live run look like a broken probe rather than a broken base URL.
	bare, err := Run(context.Background(), RunOptions{
		Mode:         ModeReplay,
		Subject:      quirkSubject(),
		Interactions: live.Interactions,
	})
	if err == nil && len(bare.Report.Facts) == len(live.Report.Facts) {
		t.Error("replaying without the recorded prefix should not silently succeed")
	}
}
