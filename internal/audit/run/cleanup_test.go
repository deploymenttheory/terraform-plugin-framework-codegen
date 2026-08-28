package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/quirkserver"
)

// TestUnit_Cleanup_RemovesPrefixedLeftoversOnly: the standalone cleanup pass
// deletes exactly the objects carrying the prefix — prior-run debris —
// and never anything else.
func TestUnit_Cleanup_RemovesPrefixedLeftoversOnly(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	s.Seed(map[string]any{"name": "tfpfgen-oldrun1-thing-name"})
	s.Seed(map[string]any{"name": "tfpfgen-oldrun2-thing-name"})
	foreign := s.Seed(map[string]any{"name": "production-object"})

	summary, err := Cleanup(context.Background(), testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if summary.PrefixDeletes != 2 {
		t.Errorf("prefix deletes = %d, want 2", summary.PrefixDeletes)
	}
	left := s.Objects()
	if len(left) != 1 {
		t.Fatalf("objects left = %v, want only the foreign one", left)
	}
	if _, ok := left[foreign]; !ok {
		t.Fatal("the foreign object was deleted")
	}
}

// TestUnit_Cleanup_ReplaysACrashedRunsLedger: a prior run's ledger file
// still naming a created object is enough to delete it by id, and the
// reconciled file is removed.
func TestUnit_Cleanup_ReplaysACrashedRunsLedger(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	orphan := s.Seed(map[string]any{"name": "unprefixed-but-ledgered"})

	opts := testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil)
	old := filepath.Join(opts.RunsDir, "crashed1"+activityFileSuffix)
	lines := []string{
		`{"kind":"intent","seq":1,"entity":"thing","name":"unprefixed-but-ledgered","itemPath":"/things/{thingId}"}`,
		`{"kind":"created","seq":1,"entity":"thing","id":"` + orphan + `"}`,
	}
	if err := os.WriteFile(old, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	summary, err := Cleanup(context.Background(), opts)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if summary.LedgerDeletes != 1 {
		t.Errorf("ledger deletes = %d, want 1", summary.LedgerDeletes)
	}
	if len(s.Objects()) != 0 {
		t.Errorf("the ledgered orphan survived: %v", s.Objects())
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the reconciled crash ledger was not removed")
	}
}

// TestUnit_Cleanup_RunBoundaryCleanupRemovesPriorDebris: the run's own
// start-of-run cleanup pass levels what a previous run left, before any step
// executes.
func TestUnit_Cleanup_RunBoundaryCleanupRemovesPriorDebris(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	s.Seed(map[string]any{"name": "tfpfgen-oldrun9-thing-name"})

	_, summary := mustRun(t, testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if summary.CleanupStart.PrefixDeletes != 1 {
		t.Errorf("start cleanup deletes = %d, want 1", summary.CleanupStart.PrefixDeletes)
	}
	if len(s.Objects()) != 0 {
		t.Errorf("objects remain: %v", s.Objects())
	}
}

// TestUnit_Cleanup_FailedDeletesReportOrphans: an API whose deletes fail
// leaves orphans, and the cleanup error names the count instead of
// claiming a clean removal.
func TestUnit_Cleanup_FailedDeletesReportOrphans(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{DeleteFails: true})
	s.Seed(map[string]any{"name": "tfpfgen-oldrun3-thing-name"})

	summary, err := Cleanup(context.Background(), testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err == nil {
		t.Fatal("orphans must fail the cleanup")
	}
	if len(summary.Orphans) == 0 {
		t.Fatal("no orphans were reported")
	}
	raw, _ := json.Marshal(summary)
	if !strings.Contains(string(raw), "answered 500") {
		t.Errorf("the orphan line does not carry the delete status: %s", raw)
	}
}

// TestUnit_Cleanup_ResolveByNameSettlesIdlessIntents: the prefix pass is
// the only handle on an intent that never learned an id, and it must
// settle exactly that intent.
func TestUnit_Cleanup_ResolveByNameSettlesIdlessIntents(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	r, err := newRunner(testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ledger.intent("thing", "tfpfgen-x-thing-name", "/things/{thingId}"); err != nil {
		t.Fatal(err)
	}
	r.resolveByName("no-such-name", "1", 204)
	if len(r.ledger.unresolved()) != 1 {
		t.Fatal("an unrelated name settled the intent")
	}
	r.resolveByName("tfpfgen-x-thing-name", "9", 204)
	if open := r.ledger.unresolved(); len(open) != 0 {
		t.Fatalf("the intent did not settle by name: %+v", open)
	}

	if !referencesCreated(map[string]string{"a": "$created:thing"}) || referencesCreated(map[string]string{"a": "literal"}) {
		t.Error("referencesCreated")
	}
	if got := orphanLine(activityEntry{Entity: "thing", Name: "n", ItemPath: "/things/{id}"}); !strings.Contains(got, "id never learned") {
		t.Errorf("orphanLine without an id = %q", got)
	}
	if excerptsOf(nil) != nil {
		t.Error("excerptsOf(nil)")
	}
	if (&httpResult{body: []byte("not json")}).object() != nil {
		t.Error("object() of a non-JSON body")
	}
}
