package probe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempLedger(t *testing.T) (*Ledger, string) {
	t.Helper()

	path := LedgerPath(t.TempDir(), "example", "thing")

	l, err := OpenLedger(path)
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	return l, path
}

// TestUnit_Probe_LedgerIntentIsDurableBeforeItReturns is the guarantee the whole file exists
// for.
//
// The failure that strands an object is a create that *succeeds* and whose response is never
// seen. The only defence is that the intent is on disk before the request goes out — so this
// asserts the line is readable by a separate reader the moment Intent returns, not merely
// buffered somewhere.
func TestUnit_Probe_LedgerIntentIsDurableBeforeItReturns(t *testing.T) {
	t.Parallel()

	l, path := tempLedger(t)

	seq, err := l.Intent("write.required", "/things", "tfpfgen-probe-write-required-1")
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if seq != 1 {
		t.Errorf("seq = %d, want 1", seq)
	}

	// Read through the filesystem, not through the Ledger, so a buffered write would fail this.
	onDisk, err := ReadLedger(path)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}
	if len(onDisk) != 1 {
		t.Fatalf("the intent is not on disk yet: %+v", onDisk)
	}

	got := onDisk[0]
	if got.Kind != KindIntent || got.Probe != "write.required" || got.Name == "" {
		t.Errorf("entry = %+v", got)
	}
	// The name is what the prefix pass matches on, so its absence would make the whole
	// intent-only orphan story unworkable.
	if !strings.HasPrefix(got.Name, "tfpfgen-probe-") {
		t.Errorf("Name = %q, want the stamped prefix", got.Name)
	}
}

// TestUnit_Probe_LedgerReconcilesRatherThanEmptying.
//
// Cleanliness is reconciliation. A ledger recording forty creates and forty deletes is clean;
// treating "empty" as the test would make every successful run look dirty.
func TestUnit_Probe_LedgerReconcilesRatherThanEmptying(t *testing.T) {
	t.Parallel()

	l, _ := tempLedger(t)

	created, _ := l.Intent("p", "/things", "n1")
	if err := l.Resolve(created, KindCreated, "1", 201, ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := l.Resolve(created, KindDeleted, "1", 204, ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if !Clean(l.Entries()) {
		t.Errorf("a created-then-deleted object should be clean: %+v", Unresolved(l.Entries()))
	}
	if len(l.Entries()) != 3 {
		t.Errorf("the file is append-only: got %d entries, want 3", len(l.Entries()))
	}
}

// TestUnit_Probe_LedgerOutstandingCases covers every way an intent resolves, and the direction
// each ambiguity is resolved in.
func TestUnit_Probe_LedgerOutstandingCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// resolve is nil for an intent that was never resolved at all.
		resolve     *EntryKind
		id          string
		outstanding bool
		wantID      string
		wantReason  string
	}{
		{
			// A 4xx is reliable evidence that nothing was created.
			name: "rejected", resolve: kind(KindRejected), outstanding: false,
		},
		{
			name: "deleted", resolve: kind(KindDeleted), id: "1", outstanding: false,
		},
		{
			name: "created and not deleted", resolve: kind(KindCreated), id: "1",
			outstanding: true, wantID: "1", wantReason: "created and not deleted",
		},
		{
			// A 5xx, or a transport error. The object may well exist.
			name: "failed", resolve: kind(KindFailed), outstanding: true,
		},
		{
			// The signature of a crash between sending and reading. No id was ever learned,
			// which is exactly why the prefix pass exists.
			name: "never resolved", resolve: nil, outstanding: true,
			wantReason: "never recorded",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l, _ := tempLedger(t)

			seq, _ := l.Intent("p", "/things", "tfpfgen-probe-n")
			if tc.resolve != nil {
				if err := l.Resolve(seq, *tc.resolve, tc.id, 0, ""); err != nil {
					t.Fatalf("Resolve: %v", err)
				}
			}

			got := Unresolved(l.Entries())

			if tc.outstanding && len(got) != 1 {
				t.Fatalf("expected one outstanding, got %+v", got)
			}
			if !tc.outstanding {
				if len(got) != 0 {
					t.Fatalf("expected nothing outstanding, got %+v", got)
				}
				return
			}

			if got[0].ID != tc.wantID {
				t.Errorf("ID = %q, want %q", got[0].ID, tc.wantID)
			}
			// The name is always carried, because the prefix pass needs it whether or not an
			// id was learned.
			if got[0].Name == "" {
				t.Error("an outstanding entry must carry the name")
			}
			if tc.wantReason != "" && !strings.Contains(got[0].Reason, tc.wantReason) {
				t.Errorf("Reason = %q, want it to mention %q", got[0].Reason, tc.wantReason)
			}
		})
	}
}

func kind(k EntryKind) *EntryKind { return &k }

// TestUnit_Probe_LedgerSurvivesATruncatedFinalLine.
//
// A half-written final line is what a SIGKILL mid-fsync looks like. It must read as an
// unresolved intent — the safe direction, because it makes the sweeper look for something that
// may exist. Refusing to read the file would leave the operator unable to do the one thing
// they need to.
func TestUnit_Probe_LedgerSurvivesATruncatedFinalLine(t *testing.T) {
	t.Parallel()

	path := LedgerPath(t.TempDir(), "example", "thing")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	good, err := json.Marshal(LedgerEntry{Kind: KindIntent, Seq: 1, Probe: "p", Name: "n1"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	resolved, err := json.Marshal(LedgerEntry{Kind: KindDeleted, Seq: 1, Probe: "p"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// A complete, reconciled pair, then a line cut off mid-write.
	content := string(good) + "\n" + string(resolved) + "\n" + `{"kind":"intent","seq":2,"pro`

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	entries, err := ReadLedger(path)
	if err != nil {
		t.Fatalf("a truncated final line must not fail the read: %v", err)
	}

	outstanding := Unresolved(entries)
	if len(outstanding) != 1 {
		t.Fatalf("the truncated line should read as one unresolved intent, got %+v", outstanding)
	}
	if !strings.Contains(outstanding[0].Reason, "truncated") {
		t.Errorf("the reason should say the line was truncated: %q", outstanding[0].Reason)
	}

	// A malformed line that is *not* last is a real corruption, not a crash, and must fail.
	bad := LedgerPath(t.TempDir(), "example", "thing")
	if err := os.MkdirAll(filepath.Dir(bad), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(bad, []byte("{not json\n"+string(good)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := ReadLedger(bad); !errors.Is(err, ErrLedger) {
		t.Errorf("error = %v, want ErrLedger", err)
	}
}

// TestUnit_Probe_OpenLedgerResumesAPreviousRun: -mode sweep continues a run that did not
// finish, so it must see what that run recorded.
func TestUnit_Probe_OpenLedgerResumesAPreviousRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := LedgerPath(dir, "example", "thing")

	first, err := OpenLedger(path)
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	seq, _ := first.Intent("p", "/things", "n1")
	if err := first.Resolve(seq, KindCreated, "42", 201, ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := OpenLedger(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	outstanding := Unresolved(second.Entries())
	if len(outstanding) != 1 || outstanding[0].ID != "42" {
		t.Fatalf("the previous run's object was not carried over: %+v", outstanding)
	}

	// A new intent must not reuse a sequence number, or two different intents would be
	// indistinguishable in a file whose whole purpose is to be a record.
	next, err := second.Intent("p", "/things", "n2")
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if next <= seq {
		t.Errorf("the new sequence is %d, which collides with the resumed %d", next, seq)
	}

	// And resolving the resumed object works, which is what a sweep does.
	if err := second.Resolve(42, KindDeleted, "42", 204, ""); err == nil {
		t.Error("resolving a sequence that is not an intent should fail")
	}
	if err := second.Resolve(seq, KindDeleted, "42", 204, ""); err != nil {
		t.Errorf("resolving the resumed intent: %v", err)
	}
	if !Clean(second.Entries()) {
		remaining := Unresolved(second.Entries())
		if len(remaining) != 1 || remaining[0].Seq != next {
			t.Errorf("only the new intent should remain: %+v", remaining)
		}
	}
}

// TestUnit_Probe_MemoryLedgerWritesNothing: replay must not touch the filesystem, and it needs a
// ledger so a mutating replay can reproduce the recorded sweep.
func TestUnit_Probe_MemoryLedgerWritesNothing(t *testing.T) {
	t.Parallel()

	l := MemoryLedger()

	seq, err := l.Intent("p", "/things", "n1")
	if err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if err := l.Resolve(seq, KindCreated, "1", 201, ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if len(l.Entries()) != 2 {
		t.Errorf("a memory ledger should still accumulate: %+v", l.Entries())
	}
	if err := l.Close(); err != nil {
		t.Errorf("closing a memory ledger: %v", err)
	}
}

// TestUnit_Probe_LedgerRefusesWhenItCannotWrite.
//
// The caller must not issue the request when Intent fails. This asserts the error rather than
// the caller's behaviour, which is asserted where Create lives — but the error has to carry
// ErrLedger for that to be possible.
func TestUnit_Probe_LedgerRefusesWhenItCannotWrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := LedgerPath(dir, "example", "thing")

	l, err := OpenLedger(path)
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}

	// Closing behind its back is the cheapest way to make the next write fail.
	if err := l.f.Close(); err != nil {
		t.Fatalf("closing the file: %v", err)
	}

	if _, err := l.Intent("p", "/things", "n1"); !errors.Is(err, ErrLedger) {
		t.Errorf("error = %v, want ErrLedger", err)
	}
}

func TestUnit_Probe_DirtyErrorNamesTheFix(t *testing.T) {
	t.Parallel()

	err := DirtyError("/tmp/x/ledger.jsonl", "tag", []Outstanding{
		{Seq: 1, Probe: "write.required", Name: "tfpfgen-probe-1", ID: "42", Reason: "created and not deleted"},
		{Seq: 2, Probe: "write.required", Name: "tfpfgen-probe-2", Reason: "never recorded"},
	})

	if !errors.Is(err, ErrDirtyLedger) {
		t.Errorf("error = %v, want ErrDirtyLedger", err)
	}

	msg := err.Error()
	for _, want := range []string{"2 object(s)", "42", "identifier never recorded",
		"-mode sweep", "-resource tag", "/tmp/x/ledger.jsonl"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message omits %q:\n%s", want, msg)
		}
	}
}

func TestUnit_Probe_LedgerPath(t *testing.T) {
	t.Parallel()

	got := LedgerPath(".tfpluginframeworkgen/probe", "thousandeyes", "tag")
	want := filepath.Join(".tfpluginframeworkgen/probe", "thousandeyes", "tag", "ledger.jsonl")

	if got != want {
		t.Errorf("LedgerPath = %q, want %q", got, want)
	}
}

func TestUnit_Probe_ReadLedgerOnAMissingFile(t *testing.T) {
	t.Parallel()

	// No ledger means no outstanding objects, which is the normal state and not an error.
	entries, err := ReadLedger(LedgerPath(t.TempDir(), "example", "absent"))
	if err != nil {
		t.Fatalf("a missing ledger must not be an error: %v", err)
	}
	if len(entries) != 0 || !Clean(entries) {
		t.Errorf("entries = %+v", entries)
	}
}
