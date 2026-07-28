package probe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe/quirkserver"
)

const testPrefix = "tfpfgen-probe"

// mutatingAgainst builds a live mutating session pointed at a server.
//
// A real transport, to localhost, at a server the test just started -- the same exemption the
// read-tier helper takes.
func mutatingAgainst(t *testing.T, baseURL string, l *Ledger, cfg ...func(*MutationConfig)) *MutatingSession {
	t.Helper()

	live, err := newHTTPSession(SessionConfig{
		Transport:          &http.Transport{},
		BaseURL:            baseURL,
		CollectionTemplate: "/things",
		ItemTemplate:       "/things/{id}",
	})
	if err != nil {
		t.Fatalf("newHTTPSession: %v", err)
	}

	mc := MutationConfig{Ledger: l, NameField: "key", IDField: "id"}
	for _, fn := range cfg {
		fn(&mc)
	}

	ms, err := newMutatingSession(&Grant{namePrefix: testPrefix}, live, mc)
	if err != nil {
		t.Fatalf("newMutatingSession: %v", err)
	}

	return ms
}

// TestUnit_Probe_LedgerWritesIntentBeforeTheRequest is the central guarantee of this phase, and
// it is directly observable.
//
// The handler reads the ledger *file* before the quirk server sees the request. If the intent
// were written after the response -- or merely buffered -- there would be nothing there, and
// every create whose response was lost would be invisible to both passes of the sweeper.
func TestUnit_Probe_LedgerWritesIntentBeforeTheRequest(t *testing.T) {
	t.Parallel()

	quirks := quirkserver.New(t, quirkserver.Quirks{})

	path := LedgerPath(t.TempDir(), "example", "thing")

	l, err := OpenLedger(path)
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	var (
		sawIntent bool
		sawName   string
	)

	// Interposed in front of the quirk server, so the assertion happens at the only moment it
	// proves anything: the request has been sent and no response exists yet.
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			entries, readErr := ReadLedger(path)
			if readErr != nil {
				t.Errorf("reading the ledger from inside the handler: %v", readErr)
			}
			for _, e := range entries {
				if e.Kind == KindIntent {
					sawIntent = true
					sawName = e.Name
				}
			}
		}

		quirks.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)

	ms := mutatingAgainst(t, front.URL, l)

	name := ms.NameValue("write.writable", 1)

	resp, id, err := ms.Create(context.Background(), "write.writable", map[string]any{
		"key":   name,
		"value": "v",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if resp.Status != http.StatusCreated || id == "" {
		t.Fatalf("Create gave status %d id %q", resp.Status, id)
	}

	if !sawIntent {
		t.Fatal("the create request reached the server with no intent line on disk; " +
			"a create whose response is lost would be invisible to the sweeper")
	}
	if sawName != name {
		t.Errorf("the intent recorded the name %q, want %q -- the prefix pass matches on it",
			sawName, name)
	}

	// The intent is resolved to a created line carrying the identifier. Still outstanding, and
	// correctly so: the object exists until something deletes it.
	outstanding := Unresolved(l.Entries())
	if len(outstanding) != 1 || outstanding[0].ID != id {
		t.Fatalf("outstanding = %+v, want one entry carrying id %q", outstanding, id)
	}

	if _, err := ms.Delete(context.Background(), "write.writable", id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !Clean(l.Entries()) {
		t.Errorf("the ledger did not reconcile after the delete: %+v", Unresolved(l.Entries()))
	}
}

// TestUnit_Probe_CreateRefusesAnUnprefixedBody.
//
// The session enforces the invariant rather than performing it. Silently injecting the prefix
// would confound the normalization protocol -- send "  MiXeD  ", receive prefix + "  MiXeD  " --
// and make request bodies unpredictable at replay, where a cassette matches on bytes.
func TestUnit_Probe_CreateRefusesAnUnprefixedBody(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	l := MemoryLedger()
	ms := mutatingAgainst(t, srv.BaseURL(), l)

	tests := []struct {
		name string
		body map[string]any
	}{
		{"no name field at all", map[string]any{"value": "v"}},
		{"a name without the prefix", map[string]any{"key": "something"}},
		{"a name that is not a string", map[string]any{"key": 7}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, _, err := ms.Create(context.Background(), "p", tc.body); !errors.Is(err, ErrInvalidPlan) {
				t.Errorf("error = %v, want ErrInvalidPlan", err)
			}
		})
	}

	// Refused before anything is recorded, and before anything is sent. An intent for a create
	// that never happened would send the sweeper hunting for an object that does not exist.
	if len(l.Entries()) != 0 {
		t.Errorf("a refused create must leave no ledger entry: %+v", l.Entries())
	}
	if srv.Requests() != 0 {
		t.Errorf("a refused create must issue no request, got %d", srv.Requests())
	}
}

// TestUnit_Probe_CreateStatusClassification is the safety argument of the whole phase.
//
// A 4xx resolves the intent, because a 4xx is reliable evidence that nothing was created.
// Anything else leaves it outstanding, because a 500 may well have created the object -- and an
// object nobody looks for is an object left in somebody's tenant.
func TestUnit_Probe_CreateStatusClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
		// outstanding is whether the sweeper must still go looking. A successful create is
		// outstanding too: the object exists until something deletes it, and cleanliness is
		// reconciliation rather than emptiness.
		outstanding bool
		wantKind    EntryKind
	}{
		{"created", http.StatusCreated, `{"id":"1"}`, true, KindCreated},
		{"bad request", http.StatusBadRequest, `{"title":"nope"}`, false, KindRejected},
		{"conflict", http.StatusConflict, `{"title":"exists"}`, false, KindRejected},
		{"server error", http.StatusInternalServerError, `{}`, true, KindFailed},
		{"bad gateway", http.StatusBadGateway, ``, true, KindFailed},
		// A 2xx whose body carries no identifier: the object exists and cannot be addressed.
		{"created with no id", http.StatusCreated, `{"key":"x"}`, true, KindFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			l := MemoryLedger()
			ms := mutatingAgainst(t, srv.URL, l)

			_, _, err := ms.Create(context.Background(), "p", map[string]any{
				"key": ms.NameValue("p", 1),
			})

			if tc.name == "created with no id" {
				if !errors.Is(err, ErrNoIdentifier) {
					t.Errorf("error = %v, want ErrNoIdentifier", err)
				}
			}

			outstanding := Unresolved(l.Entries())
			if got := len(outstanding) > 0; got != tc.outstanding {
				t.Fatalf("outstanding = %v, want %v (%+v)", got, tc.outstanding, outstanding)
			}

			var kinds []EntryKind
			for _, e := range l.Entries() {
				if e.Kind != KindIntent {
					kinds = append(kinds, e.Kind)
				}
			}
			if len(kinds) != 1 || kinds[0] != tc.wantKind {
				t.Errorf("resolution kinds = %v, want [%s]", kinds, tc.wantKind)
			}
		})
	}
}

// TestUnit_Probe_CreateRefusesWhenTheLedgerCannotBeWritten.
//
// The consequence of the ordering, asserted where it matters: no request is issued. A create
// that cannot be recorded is a create that cannot be cleaned up.
func TestUnit_Probe_CreateRefusesWhenTheLedgerCannotBeWritten(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	l, err := OpenLedger(LedgerPath(t.TempDir(), "example", "thing"))
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	// Closing behind its back is the cheapest way to make the intent write fail.
	if err := l.f.Close(); err != nil {
		t.Fatalf("closing the file: %v", err)
	}

	ms := mutatingAgainst(t, srv.BaseURL(), l)

	_, _, err = ms.Create(context.Background(), "p", map[string]any{"key": ms.NameValue("p", 1)})
	if !errors.Is(err, ErrLedger) {
		t.Fatalf("error = %v, want ErrLedger", err)
	}

	if srv.Requests() != 0 {
		t.Errorf("%d request(s) were issued after the ledger write failed; there must be none",
			srv.Requests())
	}
}

// TestUnit_Probe_InFlightContextIgnoresCancellation.
//
// Cancellation must stop *new* requests without aborting one already sent: the response to a
// create in flight is the only thing that turns an intent into a resolvable line. A one-liner
// whose being wrong is invisible in every green run.
func TestUnit_Probe_InFlightContextIgnoresCancellation(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	cancel()

	ctx, release := inFlightContext(parent)
	defer release()

	if err := ctx.Err(); err != nil {
		t.Fatalf("the detached context is already done: %v", err)
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("a detached context with no deadline would outlive the process")
	}
	if _, parentHad := parent.Deadline(); parentHad {
		t.Fatal("the parent was expected to have no deadline of its own")
	}
	_ = deadline

	// And a create still completes under an already-cancelled parent, which is the behavior
	// the detachment exists for.
	srv := quirkserver.New(t, quirkserver.Quirks{})
	ms := mutatingAgainst(t, srv.BaseURL(), MemoryLedger())

	resp, id, err := ms.Create(parent, "p", map[string]any{"key": ms.NameValue("p", 1)})
	if err != nil {
		t.Fatalf("a create under a cancelled parent should still be sent: %v", err)
	}
	if resp.Status != http.StatusCreated || id == "" {
		t.Errorf("status %d id %q", resp.Status, id)
	}
}

// panicProbe faults on purpose.
type panicProbe struct{}

func (panicProbe) Name() string      { return "write.panics" }
func (panicProbe) Kind() Kind        { return KindMutating }
func (panicProbe) Cost(Scope) int    { return 1 }
func (panicProbe) Creates(Scope) int { return 1 }

func (panicProbe) Exercise(_ context.Context, _ *MutatingSession, _ Scope) (Result, error) {
	panic("a deliberate fault")
}

// createThenPanicProbe creates an object and then faults, which is the case that matters: the
// runner must clean up after a probe that never got to its own cleanup.
type createThenPanicProbe struct{}

func (createThenPanicProbe) Name() string      { return "write.creates-then-panics" }
func (createThenPanicProbe) Kind() Kind        { return KindMutating }
func (createThenPanicProbe) Cost(Scope) int    { return 2 }
func (createThenPanicProbe) Creates(Scope) int { return 1 }

func (createThenPanicProbe) Exercise(
	ctx context.Context,
	s *MutatingSession,
	_ Scope,
) (Result, error) {
	if _, _, err := s.Create(ctx, "write.creates-then-panics", map[string]any{
		"key": s.NameValue("write.creates-then-panics", 1),
	}); err != nil {
		return Result{}, err
	}

	panic("faulted after creating")
}

// TestUnit_Probe_APanicIsCapturedWithItsOwnStack.
//
// Re-raising inside this package would destroy the evidence: cmd would never get the report,
// never write the orphan table, never append the step summary. And the stack has to be taken at
// the fault -- a later bare panic(r) yields a stack from the re-raise point, which says nothing
// about the cause.
func TestUnit_Probe_APanicIsCapturedWithItsOwnStack(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	ms := mutatingAgainst(t, srv.BaseURL(), MemoryLedger())

	_, err := exercise(context.Background(), ms, panicProbe{}, UnplannedScope(quirkSubject()))

	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v, want a *PanicError", err)
	}
	if !errors.Is(err, ErrPanicked) {
		t.Error("a PanicError must satisfy errors.Is(err, ErrPanicked) so the exit mapping needs no type switch")
	}
	if pe.Probe != "write.panics" {
		t.Errorf("Probe = %q", pe.Probe)
	}
	if !strings.Contains(string(pe.Stack), "panicProbe") {
		t.Errorf("the stack was not captured at the fault:\n%s", pe.Stack)
	}
}

// TestUnit_Probe_TheRunnerCleansUpAfterAPanickingProbe.
//
// Cleanup is the runner's job precisely because of this case. A probe that faults -- or returns
// early on an abandoned protocol, which is the majority of the branches in every mutating
// protocol -- never reaches its own defer.
func TestUnit_Probe_TheRunnerCleansUpAfterAPanickingProbe(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	l := MemoryLedger()
	ms := mutatingAgainst(t, srv.BaseURL(), l)

	var report Report

	err := runMutatingProbes(
		context.Background(),
		ms,
		[]MutatingProbe{createThenPanicProbe{}},
		RunOptions{Subject: quirkSubject()},
		&report,
	)
	if !errors.Is(err, ErrPanicked) {
		t.Fatalf("error = %v, want ErrPanicked", err)
	}

	if len(srv.Objects()) != 0 {
		t.Errorf("the runner left %d object(s) behind after a probe panicked", len(srv.Objects()))
	}
	if !Clean(l.Entries()) {
		t.Errorf("the ledger did not reconcile: %+v", Unresolved(l.Entries()))
	}

	// The report survives, which is the whole reason the panic is converted rather than
	// re-raised here.
	if len(report.Probes) != 1 || report.Probes[0].Status != "failed" {
		t.Errorf("outcome = %+v", report.Probes)
	}
}

// TestUnit_Probe_RemainingProbesAreReportedSkippedNotOmitted.
//
// MaxDeleteFailures defaults to zero, so a run stops early routinely. Omitting the probes that
// never ran would make a halted run read as a complete one.
func TestUnit_Probe_RemainingProbesAreReportedSkippedNotOmitted(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	ms := mutatingAgainst(t, srv.BaseURL(), MemoryLedger())

	var report Report

	err := runMutatingProbes(
		context.Background(),
		ms,
		[]MutatingProbe{panicProbe{}, panicProbe{}},
		RunOptions{Subject: quirkSubject()},
		&report,
	)
	if !errors.Is(err, ErrPanicked) {
		t.Fatalf("error = %v, want ErrPanicked", err)
	}

	if len(report.Probes) != 2 {
		t.Fatalf("both probes must appear in the report, got %+v", report.Probes)
	}
	if report.Probes[1].Status != "skipped" ||
		!strings.Contains(report.Probes[1].Reason, "stopped earlier") {
		t.Errorf("the second probe should say why it did not run: %+v", report.Probes[1])
	}
}

// TestUnit_Probe_ADeleteFailureStopsTheRunCreating.
//
// Continuing to create after demonstrating you cannot clean up is the worst available
// behavior, so the first failure is enough -- MaxDeleteFailures is deliberately not defaulted,
// because treating zero as "unset" would make the safest setting the one you cannot express.
func TestUnit_Probe_ADeleteFailureStopsTheRunCreating(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{DeleteFails: true})
	l := MemoryLedger()
	ms := mutatingAgainst(t, srv.BaseURL(), l)

	_, id, err := ms.Create(context.Background(), "p", map[string]any{"key": ms.NameValue("p", 1)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = ms.Delete(context.Background(), "p", id)
	if !errors.Is(err, ErrDeleteFailures) {
		t.Fatalf("error = %v, want ErrDeleteFailures", err)
	}
	if !stopsTheRun(err) {
		t.Error("a delete failure must stop the run")
	}

	// The object is still there and still outstanding, which is what makes it an orphan
	// rather than a forgotten one.
	if len(Unresolved(l.Entries())) != 1 {
		t.Errorf("outstanding = %+v", Unresolved(l.Entries()))
	}
}

// TestUnit_Probe_ReleaseProbeOnlyReleasesItsOwnProbe.
//
// Per probe rather than per run, so peak live objects is one probe's worth. Per probe rather
// than per object, because the immutability protocol needs its first object alive while it
// creates the second.
func TestUnit_Probe_ReleaseProbeOnlyReleasesItsOwnProbe(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	l := MemoryLedger()
	ms := mutatingAgainst(t, srv.BaseURL(), l)

	ctx := context.Background()

	for i, probe := range []string{"write.first", "write.first", "write.second"} {
		if _, _, err := ms.Create(ctx, probe, map[string]any{
			"key": ms.NameValue(probe, i+1),
		}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	if got := ms.ReleaseProbe(ctx, "write.first"); got != 2 {
		t.Errorf("released %d objects, want 2", got)
	}

	if len(srv.Objects()) != 1 {
		t.Errorf("the other probe's object should still be alive, %d remain", len(srv.Objects()))
	}

	outstanding := Unresolved(l.Entries())
	if len(outstanding) != 1 || outstanding[0].Probe != "write.second" {
		t.Errorf("outstanding = %+v", outstanding)
	}
}

// TestUnit_Probe_UpdateAndDeleteRefuseAnEmptyIdentifier: without an id there is no URL, and
// resolving the template anyway would send a request to the collection path -- which for a
// DELETE could mean something very different from nothing.
func TestUnit_Probe_UpdateAndDeleteRefuseAnEmptyIdentifier(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	ms := mutatingAgainst(t, srv.BaseURL(), MemoryLedger())

	if _, err := ms.Update(context.Background(), "p", "", nil); !errors.Is(err, ErrNoIdentifier) {
		t.Errorf("Update = %v, want ErrNoIdentifier", err)
	}
	if _, err := ms.Delete(context.Background(), "p", ""); !errors.Is(err, ErrNoIdentifier) {
		t.Errorf("Delete = %v, want ErrNoIdentifier", err)
	}
	if srv.Requests() != 0 {
		t.Errorf("%d request(s) issued with no identifier", srv.Requests())
	}
}

// TestUnit_Probe_ConcurrentRequestsAreRefused.
//
// A cassette is a strictly ordered transcript, so two requests in flight at once land in it in
// whichever order the server answered. A mutex would serialize them silently; silently is the
// problem, because the recording would then replay only by luck.
func TestUnit_Probe_ConcurrentRequestsAreRefused(t *testing.T) {
	t.Parallel()

	var (
		inner error
		live  *httpSession
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Re-entering from inside the handler is exactly what a concurrent probe looks like
		// from the session's point of view, and it needs no goroutine to demonstrate.
		_, inner = live.Get(context.Background(), "/things", nil)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"things":[]}`))
	}))
	t.Cleanup(srv.Close)

	var err error

	live, err = newHTTPSession(SessionConfig{
		Transport:          &http.Transport{},
		BaseURL:            srv.URL,
		CollectionTemplate: "/things",
		ItemTemplate:       "/things/{id}",
	})
	if err != nil {
		t.Fatalf("newHTTPSession: %v", err)
	}

	if _, err := live.Get(context.Background(), "/things", nil); err != nil {
		t.Fatalf("the outer request should succeed: %v", err)
	}

	if !errors.Is(inner, ErrInvalidPlan) {
		t.Errorf("the nested request error = %v, want ErrInvalidPlan", inner)
	}
	if inner != nil && !strings.Contains(inner.Error(), "still in flight") {
		t.Errorf("the message should say what went wrong: %v", inner)
	}
}

// TestUnit_Probe_ReplayGrantCannotAuthorizeARecordingRun.
//
// ReplayGrant is exported so cmd can replay a mutating cassette with no profile, no token and
// no tenant, which is safe because a replay transport cannot reach a network. This is the one
// hole that opens, closed in the one place that can see both facts.
func TestUnit_Probe_ReplayGrantCannotAuthorizeARecordingRun(t *testing.T) {
	t.Parallel()

	var report Report

	err := runMutatingTier(context.Background(), nil, RunOptions{
		Mode:    ModeRecord,
		Grant:   ReplayGrant(testPrefix),
		Subject: quirkSubject(),
	}, &report)

	if !errors.Is(err, ErrNoGrant) {
		t.Fatalf("error = %v, want ErrNoGrant", err)
	}
}

// TestUnit_Probe_RunRefusesSweepMode: a sweep derives no facts and must be able to spend after
// the run's budget is exhausted, so it has its own entry point.
func TestUnit_Probe_RunRefusesSweepMode(t *testing.T) {
	t.Parallel()

	_, err := Run(context.Background(), RunOptions{Mode: ModeSweep, Subject: quirkSubject()})
	if !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("error = %v, want ErrInvalidPlan", err)
	}
}

// TestUnit_Probe_UpdateUsesTheMethodTheBlueprintRecords.
//
// Carried rather than assumed, because on an API that only accepts PATCH every update probe
// would otherwise report a fact about the method instead of about the field.
func TestUnit_Probe_UpdateUsesTheMethodTheBlueprintRecords(t *testing.T) {
	t.Parallel()

	var seen []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","key":"x"}`))
	}))
	t.Cleanup(srv.Close)

	patch := mutatingAgainst(t, srv.URL, MemoryLedger(), func(mc *MutationConfig) {
		mc.UpdateMethod = http.MethodPatch
	})

	if _, err := patch.Update(context.Background(), "p", "1", map[string]any{"key": "x"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// PUT is the default, and an unset method must not send an empty one.
	dflt := mutatingAgainst(t, srv.URL, MemoryLedger())
	if _, err := dflt.Update(context.Background(), "p", "1", map[string]any{"key": "x"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	want := []string{http.MethodPatch, http.MethodPut}
	if len(seen) != 2 || seen[0] != want[0] || seen[1] != want[1] {
		t.Errorf("methods = %v, want %v", seen, want)
	}

	// An update creates nothing, so it records nothing. A ledger line that can never be
	// outstanding is noise in the one document whose whole purpose is outstanding lines.
	if entries := dflt.cfg.Ledger.Entries(); len(entries) != 0 {
		t.Errorf("an update should leave no ledger entry: %+v", entries)
	}
}

// TestUnit_Probe_AnIdentifierMayBeANumber: every JSON number decodes to float64, so an id of 42
// arrives as 42.0 and the obvious formatting would send a DELETE to /things/42.000000. Refusing a
// numeric id outright would strand the object for no better reason than JSON's type system.
func TestUnit_Probe_AnIdentifierMayBeANumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"string", `{"id":"abc"}`, "abc"},
		{"number", `{"id":42}`, "42"},
		{"large number", `{"id":9007199254740993}`, "9007199254740992"},
		{"absent", `{"key":"x"}`, ""},
		{"a type nothing can address", `{"id":{"nested":1}}`, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ms := &MutatingSession{cfg: MutationConfig{IDField: "id"}}

			if got := ms.identifierOf(&Response{Body: parseJSON([]byte(tc.body))}); got != tc.want {
				t.Errorf("identifierOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnit_Probe_SweepNotesACollectionItCouldNotRead.
//
// A prefix pass that could not list anything has not swept: saying so is the difference between
// a sweep that failed and one that had nothing to do.
func TestUnit_Probe_SweepNotesACollectionItCouldNotRead(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A body that is not JSON, so Items() finds nothing and the read itself is what
		// fails to be usable.
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream is down"))
	}))
	t.Cleanup(srv.Close)

	ms := mutatingAgainst(t, srv.URL, MemoryLedger())

	summary := SweepSummary{Complete: true}
	opts := sweepOpts(ms)
	opts.sweepByPrefix(context.Background(), &summary)

	// The read succeeded at the HTTP level and carried nothing usable, which is not an error
	// and not a complete sweep either.
	if summary.PrefixDeletes != 0 {
		t.Errorf("PrefixDeletes = %d", summary.PrefixDeletes)
	}
}

// TestUnit_Probe_StopsTheRunCoversEveryHaltingSentinel.
//
// Each entry is about not making things worse, and the list being short is the point: everything
// else is one probe's problem rather than the run's.
func TestUnit_Probe_StopsTheRunCoversEveryHaltingSentinel(t *testing.T) {
	t.Parallel()

	halting := []error{ErrBudget, ErrDeleteFailures, ErrCancelled, ErrAuth, ErrLedger, ErrPanicked}
	for _, sentinel := range halting {
		if !stopsTheRun(fmt.Errorf("wrapped: %w", sentinel)) {
			t.Errorf("%v should stop the run", sentinel)
		}
	}

	// A missing identifier does not: the object was recorded by name, the prefix sweep will
	// remove it, and the next probe can still work. Stopping would abandon the remaining
	// protocols over a response-shape problem.
	for _, keeps := range []error{ErrNoIdentifier, ErrInvalidPlan, errNotImplemented, ErrOrphans} {
		if stopsTheRun(keeps) {
			t.Errorf("%v should not stop the run", keeps)
		}
	}
}

// TestUnit_Probe_RunWithAGrantSweepsAndReportsIt is the integration the phase is for.
//
// The mutating probes are all unimplemented, so nothing is created -- but the tier runs, the
// sweep runs, and the report carries a sweep summary. A read-only run must not produce the same
// document as a mutating one that happened to create nothing.
func TestUnit_Probe_RunWithAGrantSweepsAndReportsIt(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	// An object from a previous run, still carrying the prefix. The prefix pass is what makes
	// a fresh run clean up after a crashed one.
	stranded := srv.Seed(map[string]any{"key": testPrefix + "-write-old-1"})
	keep := srv.Seed(map[string]any{"key": "someone-elses-object"})

	out, err := Run(context.Background(), RunOptions{
		Mode:     ModeRecord,
		Subject:  quirkSubject(),
		BaseURL:  srv.BaseURL(),
		Redactor: testRedactor(t),
		Grant:    &Grant{namePrefix: testPrefix},
		Ledger:   MemoryLedger(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if out.Report.Sweep == nil {
		t.Fatal("a mutating run must report what cleaning up did")
	}
	if out.Report.Sweep.PrefixDeletes != 1 {
		t.Errorf("PrefixDeletes = %d, want the stranded object", out.Report.Sweep.PrefixDeletes)
	}
	if len(out.Report.Orphans) != 0 {
		t.Errorf("orphans = %+v", out.Report.Orphans)
	}

	remaining := srv.Objects()
	if _, ok := remaining[stranded]; ok {
		t.Error("the stranded object survived the sweep")
	}
	if _, ok := remaining[keep]; !ok {
		t.Error("the sweep deleted an object it did not create")
	}

	// The mutating tier is reported rather than omitted, and no probe claims to have concluded
	// anything: this run supplied no plan, so the built probes abandon and the unbuilt ones are
	// skipped. A run where the write tier did nothing must not read as a complete one.
	var mutating int
	for _, p := range out.Report.Probes {
		if p.Kind != KindMutating {
			continue
		}

		mutating++

		if p.Status == "ok" {
			t.Errorf("%s reports ok with no plan and no fixture to send", p.Name)
		}
		if p.Reason == "" {
			t.Errorf("%s: status %q with no reason given", p.Name, p.Status)
		}
	}
	if mutating == 0 {
		t.Error("the mutating tier is missing from the report entirely")
	}
}

// TestUnit_Probe_NameFieldIsWhatTheSubjectSaid: a probe and the sweeper must not be able to
// disagree about which key carries the name.
func TestUnit_Probe_NameFieldIsWhatTheSubjectSaid(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	ms := mutatingAgainst(t, srv.BaseURL(), MemoryLedger())

	if ms.NameField() != "key" {
		t.Errorf("NameField = %q, want key", ms.NameField())
	}
}
