package run

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/quirkserver"
)

// TestUnit_Ledger_IntentIsDurableBeforeTheRequestIsSent is the ledger's
// reason to exist: at the moment any create request arrives at the
// server, its intent line must already be on disk — so a crash between
// sending and reading the response cannot strand an unrecorded object.
func TestUnit_Ledger_IntentIsDurableBeforeTheRequestIsSent(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})

	opts := testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil)
	ledgerPath := filepath.Join(opts.RunsDir, "testrun1"+activityFileSuffix)

	creates := 0
	opts.beforeSend = func(req *http.Request) {
		if req.Method != http.MethodPost {
			return
		}
		creates++
		raw, err := os.ReadFile(ledgerPath)
		if err != nil {
			t.Errorf("create %d: the ledger does not exist when the request goes out: %v", creates, err)
			return
		}
		entries := decodeLedger(t, raw)
		open := unresolvedOf(entries)
		if len(open) == 0 {
			t.Errorf("create %d: no unresolved intent is on disk when the request goes out", creates)
			return
		}
		last := open[0]
		if last.Entity != "thing" || !strings.HasPrefix(last.Name, "tfpfgen-testrun1") {
			t.Errorf("create %d: the outstanding intent is not this create's: %+v", creates, last)
		}
	}

	mustRun(t, opts)
	if creates == 0 {
		t.Fatal("the hook never saw a create")
	}
	if _, err := os.Stat(ledgerPath); !os.IsNotExist(err) {
		t.Errorf("a fully reconciled ledger should be removed at the end of the run: %v", err)
	}
}

func decodeLedger(t *testing.T, raw []byte) []activityEntry {
	t.Helper()
	var out []activityEntry
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var e activityEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("ledger line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

// TestUnit_Ledger_UnresolvedIsNewestFirst: deletion order must reverse
// creation order, so children go before parents.
func TestUnit_Ledger_UnresolvedIsNewestFirst(t *testing.T) {
	t.Parallel()
	l := &activityLedger{}
	seqA, _ := l.intent("parent", "tfpfgen-a", "/parents/{id}")
	seqB, _ := l.intent("child", "tfpfgen-b", "/parents/1/children/{id}")
	l.resolve(seqA, activityCreated, "1", 201)
	l.resolve(seqB, activityCreated, "2", 201)

	open := l.unresolved()
	if len(open) != 2 || open[0].Entity != "child" || open[1].Entity != "parent" {
		t.Fatalf("unresolved = %+v, want the child first", open)
	}

	l.resolve(seqB, activityDeleted, "2", 204)
	if open := l.unresolved(); len(open) != 1 || open[0].Entity != "parent" {
		t.Fatalf("after deleting the child: %+v", open)
	}
}

// TestUnit_Ledger_TruncatedFinalLineReadsAsAnIntent: a torn last line is
// what a crash mid-write looks like, and it must widen the cleanup pass rather
// than break the read.
func TestUnit_Ledger_TruncatedFinalLineReadsAsAnIntent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "old"+activityFileSuffix)
	content := `{"kind":"intent","seq":1,"entity":"thing","name":"tfpfgen-x","itemPath":"/things/{thingId}"}
{"kind":"created","seq":1,"entity":"thing","id":"9"}
{"kind":"intent","seq":2,"ent` // torn mid-write
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := readActivityFile(path)
	if err != nil {
		t.Fatalf("readActivityFile: %v", err)
	}
	open := unresolvedOf(entries)
	if len(open) != 2 {
		t.Fatalf("unresolved = %+v, want the created object and the torn intent", open)
	}

	// A malformed line anywhere else is a real error.
	bad := filepath.Join(dir, "bad"+activityFileSuffix)
	if err := os.WriteFile(bad, []byte("not json\n{\"kind\":\"intent\",\"seq\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readActivityFile(bad); err == nil {
		t.Fatal("a malformed middle line must be an error")
	}
}
