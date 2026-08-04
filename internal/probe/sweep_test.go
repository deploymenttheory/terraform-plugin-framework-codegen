package probe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe/quirkserver"
)

// sweepOpts builds options with retries that do not sleep.
//
// The delay is what a real sweep needs and what a test must not wait for. Configurable rather
// than hard-coded for exactly this reason.
func sweepOpts(ms *MutatingSession) SweepOptions {
	return SweepOptions{
		Session:    ms,
		NamePrefix: testPrefix,
		NameField:  "key",
		RetryDelay: time.Nanosecond,
		MaxSeconds: 5,
	}
}

// TestUnit_Probe_SweepLedgerPassDeletesByIdentifier is the ordinary case: everything the run
// created is known and goes away.
func TestUnit_Probe_SweepLedgerPassDeletesByIdentifier(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	l := MemoryLedger()
	ms := mutatingAgainst(t, srv.BaseURL(), l)

	ctx := context.Background()

	for i := range 3 {
		if _, _, err := ms.Create(ctx, "p", map[string]any{"key": ms.NameValue("p", i+1)}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	summary, err := Sweep(ctx, sweepOpts(ms))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if summary.LedgerDeletes != 3 {
		t.Errorf("LedgerDeletes = %d, want 3", summary.LedgerDeletes)
	}
	if summary.PrefixDeletes != 0 {
		t.Errorf("the prefix pass had nothing to do, but claims %d", summary.PrefixDeletes)
	}
	if !summary.Complete {
		t.Error("a small collection read in full should be Complete")
	}
	if len(summary.Orphans) != 0 {
		t.Errorf("orphans = %+v", summary.Orphans)
	}
	if len(srv.Objects()) != 0 {
		t.Errorf("%d object(s) left in the tenant", len(srv.Objects()))
	}
}

// TestUnit_Probe_SweepPrefixPassCatchesAnObjectWithNoIdentifier.
//
// This is the case the second pass exists for, and it is the case that actually happens: a
// create that succeeded and whose response was never seen. The intent has a name and nothing
// else, so deleting by identifier is impossible.
func TestUnit_Probe_SweepPrefixPassCatchesAnObjectWithNoIdentifier(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	l := MemoryLedger()

	// The object exists in the tenant carrying the run's prefix, and the ledger holds an
	// unresolved intent with no id -- exactly the state a SIGKILL between sending and reading
	// leaves behind.
	name := testPrefix + "-write-stranded-1"
	srv.Seed(map[string]any{"key": name})

	seq, err := l.Intent("write.stranded", "/things", name)
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	_ = seq

	ms := mutatingAgainst(t, srv.BaseURL(), l)

	summary, err := Sweep(context.Background(), sweepOpts(ms))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if summary.LedgerDeletes != 0 {
		t.Errorf("the ledger pass cannot delete what it has no identifier for, claims %d",
			summary.LedgerDeletes)
	}
	if summary.PrefixDeletes != 1 {
		t.Errorf("PrefixDeletes = %d, want 1", summary.PrefixDeletes)
	}
	if len(srv.Objects()) != 0 {
		t.Errorf("the stranded object survived: %+v", srv.Objects())
	}
}

// TestUnit_Probe_SweepIgnoresObjectsItDidNotCreate.
//
// The prefix is the only thing bounding the second pass, so this is the assertion that stops it
// being a tenant-emptying tool.
func TestUnit_Probe_SweepIgnoresObjectsItDidNotCreate(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})

	real1 := srv.Seed(map[string]any{"key": "production-tag"})
	real2 := srv.Seed(map[string]any{"key": "another-real-one"})
	mine := srv.Seed(map[string]any{"key": testPrefix + "-write-p-1"})

	l := MemoryLedger()
	ms := mutatingAgainst(t, srv.BaseURL(), l)

	summary, err := Sweep(context.Background(), sweepOpts(ms))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if summary.PrefixDeletes != 1 {
		t.Errorf("PrefixDeletes = %d, want 1", summary.PrefixDeletes)
	}

	remaining := srv.Objects()
	if len(remaining) != 2 {
		t.Fatalf("the sweep touched an object it did not create: %+v", remaining)
	}
	for _, id := range []string{real1, real2} {
		if _, ok := remaining[id]; !ok {
			t.Errorf("object %s was deleted and should not have been", id)
		}
	}
	if _, ok := remaining[mine]; ok {
		t.Error("the prefixed object survived")
	}
}

// TestUnit_Probe_SweepReportsOrphansItCannotRemove.
//
// A run that leaves rubbish in somebody's tenant has failed at the thing that matters most,
// whatever else it achieved -- so ErrOrphans, and a table a human can act on.
func TestUnit_Probe_SweepReportsOrphansItCannotRemove(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{DeleteFails: true})
	l := MemoryLedger()
	ms := mutatingAgainst(t, srv.BaseURL(), l)

	if _, _, err := ms.Create(context.Background(), "write.p", map[string]any{
		"key": ms.NameValue("write.p", 1),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	summary, err := Sweep(context.Background(), sweepOpts(ms))
	if !errors.Is(err, ErrOrphans) {
		t.Fatalf("error = %v, want ErrOrphans", err)
	}

	if len(summary.Orphans) != 1 {
		t.Fatalf("orphans = %+v", summary.Orphans)
	}

	orphan := summary.Orphans[0]
	if orphan.Probe != "write.p" {
		t.Errorf("the orphan should name what created it, got %q", orphan.Probe)
	}
	if !strings.Contains(orphan.Curl, "-X DELETE") || !strings.Contains(orphan.Curl, orphan.ID) {
		t.Errorf("the curl line should be runnable as-is: %q", orphan.Curl)
	}

	// The curl line goes into a committed report and a CI step summary. A token in either is a
	// token that has to be rotated.
	if !strings.Contains(orphan.Curl, "$TFPFGEN_PROBE_TOKEN") {
		t.Errorf("the curl line must reference the environment variable: %q", orphan.Curl)
	}
	if strings.Contains(orphan.Curl, "Bearer secret") {
		t.Errorf("the curl line carries a credential value: %q", orphan.Curl)
	}
}

// TestUnit_Probe_SweepRetriesAFlakyDelete: two retries with constant backoff, because a single
// attempt turns a transient failure into a permanent orphan.
func TestUnit_Probe_SweepRetriesAFlakyDelete(t *testing.T) {
	t.Parallel()

	// Every second delete fails, so each object needs one retry.
	srv := quirkserver.New(t, quirkserver.Quirks{DeleteFlakyEvery: 2})
	l := MemoryLedger()
	ms := mutatingAgainst(t, srv.BaseURL(), l)

	ctx := context.Background()

	for i := range 2 {
		if _, _, err := ms.Create(ctx, "p", map[string]any{"key": ms.NameValue("p", i+1)}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	summary, err := Sweep(ctx, sweepOpts(ms))
	if err != nil {
		t.Fatalf("a flaky delete should be retried, not reported: %v", err)
	}
	if len(summary.Orphans) != 0 {
		t.Errorf("orphans = %+v", summary.Orphans)
	}
	if len(srv.Objects()) != 0 {
		t.Errorf("%d object(s) left behind", len(srv.Objects()))
	}
}

// TestUnit_Probe_SweepConfirmsA404BeforeBelievingIt.
//
// On an eventually-consistent API a 404 may mean "not visible yet" rather than "absent", and
// treating that as success is how an orphan gets reported as cleaned up. So the collection is
// consulted: it is the API's own answer to what exists.
func TestUnit_Probe_SweepConfirmsA404BeforeBelievingIt(t *testing.T) {
	t.Parallel()

	var (
		deletes int
		lists   int
	)

	// The object is in the collection but the item endpoint 404s -- the shape of a read replica
	// that has not caught up. A sweeper that trusted the 404 would call this cleaned up.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodDelete:
			deletes++
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"title":"not found"}`))

		default:
			lists++
			_, _ = w.Write([]byte(`{"things":[{"id":"1","key":"` + testPrefix + `-p-1"}]}`))
		}
	}))
	t.Cleanup(srv.Close)

	l := MemoryLedger()
	seq, err := l.Intent("write.p", "/things", testPrefix+"-p-1")
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if err := l.Resolve(seq, EntryKindCreated, "1", 201, ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	ms := mutatingAgainst(t, srv.URL, l)

	summary, err := Sweep(context.Background(), sweepOpts(ms))
	if !errors.Is(err, ErrOrphans) {
		t.Fatalf("error = %v, want ErrOrphans -- the object is still listed", err)
	}
	if len(summary.Orphans) != 1 {
		t.Errorf("orphans = %+v", summary.Orphans)
	}
	if lists == 0 {
		t.Error("the 404 was believed without consulting the collection")
	}
	if deletes < sweepDeleteAttempts {
		t.Errorf("%d delete attempts, want %d", deletes, sweepDeleteAttempts)
	}
}

// TestUnit_Probe_SweepDoesNotClaimACompleteSweepItCannotVouchFor.
//
// Paging is not generically implementable, so the sweeper reports what it can stand behind. A
// round item count is far likelier to be a page limit than a collection size, and saying so is
// better than a clean bill of health that is a guess.
func TestUnit_Probe_SweepDoesNotClaimACompleteSweepItCannotVouchFor(t *testing.T) {
	t.Parallel()

	var body strings.Builder
	body.WriteString(`{"things":[`)
	for i := range 100 {
		if i > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `{"id":"%d","key":"unrelated-%d"}`, i, i)
	}
	body.WriteString(`]}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body.String()))
	}))
	t.Cleanup(srv.Close)

	ms := mutatingAgainst(t, srv.URL, MemoryLedger())

	summary, err := Sweep(context.Background(), sweepOpts(ms))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if summary.Complete {
		t.Error("exactly 100 items should not be reported as a complete collection read")
	}
	if len(summary.Notes) != 1 || !strings.Contains(summary.Notes[0].Message, "page limit") {
		t.Errorf("notes = %+v", summary.Notes)
	}
}

// TestUnit_Probe_SweepRefusesWithoutAPrefix: a prefix sweep with no prefix would delete a
// tenant.
func TestUnit_Probe_SweepRefusesWithoutAPrefix(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	ms := mutatingAgainst(t, srv.BaseURL(), MemoryLedger())

	tests := []struct {
		name string
		opts SweepOptions
	}{
		{"no session", SweepOptions{NamePrefix: testPrefix, NameField: "key"}},
		{"no prefix", SweepOptions{Session: ms, NameField: "key"}},
		{"no name field", SweepOptions{Session: ms, NamePrefix: testPrefix}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := Sweep(context.Background(), tc.opts); !errors.Is(err, ErrInvalidPlan) {
				t.Errorf("error = %v, want ErrInvalidPlan", err)
			}
		})
	}

	if srv.Requests() != 0 {
		t.Errorf("a refused sweep must issue no request, got %d", srv.Requests())
	}
}

// TestUnit_Probe_SweepContextSurvivesACancelledRun.
//
// The commonest reason to be sweeping is that the run was cancelled or timed out. A sweep
// inheriting that context would be dead before its first request, which would guarantee exactly
// the orphans the sweeper exists to prevent.
func TestUnit_Probe_SweepContextSurvivesACancelledRun(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	cancel()

	ctx, release := SweepContext(parent, 5)
	defer release()

	if err := ctx.Err(); err != nil {
		t.Fatalf("the sweep context is already done: %v", err)
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Error("a sweep with no deadline could hang a CI job indefinitely")
	}

	// And an end-to-end sweep works under a cancelled parent, which is the behaviour that
	// matters rather than the context's fields.
	srv := quirkserver.New(t, quirkserver.Quirks{})
	l := MemoryLedger()
	ms := mutatingAgainst(t, srv.BaseURL(), l)

	if _, _, err := ms.Create(parent, "p", map[string]any{"key": ms.NameValue("p", 1)}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := Sweep(ctx, sweepOpts(ms)); err != nil {
		t.Fatalf("Sweep under a cancelled parent: %v", err)
	}
	if len(srv.Objects()) != 0 {
		t.Errorf("%d object(s) left behind", len(srv.Objects()))
	}
}

// TestUnit_Probe_SweepIsIdempotent: `-mode sweep` exists to be run again after one fails
// halfway, so running it twice must not be an error and must not delete anything the second
// time.
func TestUnit_Probe_SweepIsIdempotent(t *testing.T) {
	t.Parallel()

	srv := quirkserver.New(t, quirkserver.Quirks{})
	l := MemoryLedger()
	ms := mutatingAgainst(t, srv.BaseURL(), l)

	ctx := context.Background()

	if _, _, err := ms.Create(ctx, "p", map[string]any{"key": ms.NameValue("p", 1)}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := Sweep(ctx, sweepOpts(ms)); err != nil {
		t.Fatalf("first sweep: %v", err)
	}

	second, err := Sweep(ctx, sweepOpts(ms))
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if second.LedgerDeletes != 0 || second.PrefixDeletes != 0 {
		t.Errorf("the second sweep deleted something: %+v", second)
	}
}

// TestUnit_Probe_TheSweepSpendsFromItsOwnReserve.
//
// The contradiction this fixes: budgetCounter.spend refuses everything once MaxRequests is
// reached, so "exceed the budget, then sweep, then exit 4" was unimplementable -- the sweeper's
// own deletes would have been refused, and the cap meant to bound the blast radius would have
// manufactured exactly the orphans it exists to prevent.
func TestUnit_Probe_TheSweepSpendsFromItsOwnReserve(t *testing.T) {
	t.Parallel()

	b := &budgetCounter{limits: Budget{MaxRequests: 2, MaxCreates: 1}}

	if err := b.spend(http.MethodGet); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if err := b.spend(http.MethodPost); err != nil {
		t.Fatalf("second request: %v", err)
	}

	// The cap is reached, and it latches: a runner that ignored one refusal must not get a
	// different error naming a different probe on the next request.
	first := b.spend(http.MethodGet)
	if !errors.Is(first, ErrBudget) {
		t.Fatalf("error = %v, want ErrBudget", first)
	}
	if second := b.spend(http.MethodDelete); second == nil || second.Error() != first.Error() {
		t.Errorf("the refusal is not latched: %v then %v", first, second)
	}

	b.enterSweep()

	// Deletes now come from the reserve, which is what makes cleanup possible after the cap.
	for i := range sweepReserveRequests(b.limits) {
		if err := b.spend(http.MethodDelete); err != nil {
			t.Fatalf("sweep request %d was refused: %v", i, err)
		}
	}
	if err := b.spend(http.MethodDelete); !errors.Is(err, ErrBudget) {
		t.Errorf("the reserve is unbounded: %v", err)
	}

	report := b.report()
	if report.SweepRequests == 0 {
		t.Error("the sweep's spending must be reported separately from the run's")
	}
	if report.Requests != 2 {
		t.Errorf("Requests = %d, want the run's own 2 -- the sweep must not inflate it",
			report.Requests)
	}
}

// TestUnit_Probe_TheSweepMayNotCreate: the sweeper's whole contract is that it only removes, so
// a create from inside one is a bug in the sweeper rather than a budget matter.
func TestUnit_Probe_TheSweepMayNotCreate(t *testing.T) {
	t.Parallel()

	b := &budgetCounter{limits: Budget{MaxRequests: 10, MaxCreates: 2}}
	b.enterSweep()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		if err := b.spend(method); !errors.Is(err, ErrLedger) {
			t.Errorf("%s during a sweep: error = %v, want ErrLedger", method, err)
		}
	}

	if err := b.spend(http.MethodGet); err != nil {
		t.Errorf("a read during a sweep is legitimate -- the prefix pass needs it: %v", err)
	}
}

func TestUnit_Probe_IsProbablyAPageLimit(t *testing.T) {
	t.Parallel()

	for _, n := range []int{50, 100, 200, 250, 500, 1000} {
		if !isProbablyAPageLimit(n) {
			t.Errorf("%d should be treated as a possible page limit", n)
		}
	}
	for _, n := range []int{0, 1, 7, 99, 101, 999} {
		if isProbablyAPageLimit(n) {
			t.Errorf("%d should not be", n)
		}
	}
}

// TestUnit_Probe_TrimFloatBuildsAUsableIdentifier: every JSON number decodes to float64, so an
// id of 42 arrives as 42.0 and the obvious formatting sends a DELETE to /things/42.000000.
func TestUnit_Probe_TrimFloatBuildsAUsableIdentifier(t *testing.T) {
	t.Parallel()

	if got := trimFloat(42); got != "42" {
		t.Errorf("trimFloat(42) = %q", got)
	}
	if got := identifierIn(map[string]any{"id": float64(7)}, "id"); got != "7" {
		t.Errorf("identifierIn = %q, want 7", got)
	}
	if got := identifierIn(map[string]any{"id": true}, "id"); got != "" {
		t.Errorf("a non-identifier value should yield nothing, got %q", got)
	}
}
