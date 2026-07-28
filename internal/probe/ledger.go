package probe

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// The ledger is the record of every object a mutating run brought into existence.
//
// # Why it exists, and why the ordering is the whole design
//
// The failure that strands an object is not a create that fails. It is a create that
// *succeeds* and whose response is never seen -- a timeout, a network drop, a SIGKILL between
// sending and reading. The object exists, and nothing in the process knows its identifier.
//
// So the intent line is written and fsynced **before the request is issued**. An intent with
// no resolution is the signature of exactly that failure, and it is what tells the sweeper to
// hunt by name prefix rather than by id -- because an intent that never became a `created`
// line never learned one.
//
// Everything else here follows from that: the file is append-only, cleanliness is
// reconciliation rather than emptiness, and a half-written final line is read as an
// unresolved intent rather than as a parse error.
//
// # Not committed
//
// A ledger records live objects in somebody's tenant. It is per-run rather than
// per-snapshot, and it is gitignored. Its summary reaches the committed report; the file does
// not.

// EntryKind is what one ledger line records.
type EntryKind string

const (
	// KindIntent is written before a create request is sent.
	KindIntent EntryKind = "intent"
	// KindCreated resolves an intent: the object exists and we know its identifier.
	KindCreated EntryKind = "created"
	// KindRejected resolves an intent with no object having been made.
	//
	// A 4xx is reliable evidence of that. A 5xx is not, which is why it resolves to nothing
	// at all -- see MutatingSession.Create.
	KindRejected EntryKind = "rejected"
	// KindDeleted resolves a created object: it is gone.
	KindDeleted EntryKind = "deleted"
	// KindFailed records an attempt that neither created nor cleanly failed. The intent it
	// belongs to stays outstanding, deliberately.
	KindFailed EntryKind = "failed"
)

// LedgerEntry is one ledger line.
//
// Deliberately no timestamp. A ledger answers "which intent has no resolution", never "when",
// and omitting the clock makes assertions about a ledger exact rather than approximate. Seq
// carries the ordering.
type LedgerEntry struct {
	Kind EntryKind `json:"kind"`
	Seq  int       `json:"seq"`

	Probe string `json:"probe"`
	Path  string `json:"path"`

	// Name is the value stamped into the object's name field, so the prefix pass of the
	// sweeper can match an object whose identifier was never learned.
	Name string `json:"name,omitempty"`
	// ID is empty until a create resolves, and stays empty forever when it never does.
	ID string `json:"id,omitempty"`

	Status int    `json:"status,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Ledger appends entries, durably.
type Ledger struct {
	mu sync.Mutex

	// path and f are empty and nil for a memory ledger.
	path string
	f    *os.File

	seq int
	// entries holds everything, read plus written, so the sweeper needs no re-parse.
	entries []LedgerEntry
}

// LedgerPath is where a resource's ledger lives.
//
// The convention lives here rather than in cmd so that the sweeper, the runner and the
// startup check cannot disagree about it.
func LedgerPath(root, provider, resource string) string {
	return filepath.Join(root, provider, resource, "ledger.jsonl")
}

// OpenLedger reads any existing entries and then opens the file for appending.
//
// Reading first is what lets `probe -mode sweep` resume a previous run's ledger through the
// same constructor: a sweep is not a fresh start, it is the continuation of a run that did
// not finish.
func OpenLedger(path string) (*Ledger, error) {
	existing, err := ReadLedger(path)
	if err != nil {
		return nil, err
	}

	if mkErr := os.MkdirAll(filepath.Dir(path), 0o750); mkErr != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(path), mkErr)
	}

	f, err := os.OpenFile(
		path,
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0o600,
	) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	l := &Ledger{path: path, f: f, entries: existing}
	for _, e := range existing {
		if e.Seq > l.seq {
			l.seq = e.Seq
		}
	}

	return l, nil
}

// MemoryLedger is a ledger that writes nothing.
//
// Used by replay and verify, and it is what makes a mutating replay reproduce the recorded
// sweep: the replayed creates rebuild the same in-memory ledger from the recorded responses,
// so the ledger pass emits the same DELETEs in the same order -- which a strictly ordered
// cassette requires.
func MemoryLedger() *Ledger { return &Ledger{} }

// ReadLedger reads a ledger without opening it for writing.
//
// A missing file is not an error: no ledger means no outstanding objects, which is the normal
// state.
func ReadLedger(path string) ([]LedgerEntry, error) {
	f, err := os.Open(path) //nolint:gosec // operator-supplied path by design
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var (
		out   []LedgerEntry
		lines []string
	)

	scanner := bufio.NewScanner(f)
	// A ledger line is small, but a pathological Reason should not break the read.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, fmt.Errorf("reading %s: %w", path, scanErr)
	}

	for i, line := range lines {
		var e LedgerEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			// Only the final line may be malformed, and when it is, it is a partial write --
			// which is precisely what a SIGKILL mid-fsync looks like. Treating it as an
			// unresolved intent is the safe reading: it makes the sweeper look for something
			// that may exist. Refusing to read the file at all would leave the operator
			// unable to sweep, which is the one thing they need to be able to do.
			if i == len(lines)-1 {
				out = append(out, LedgerEntry{
					Kind: KindIntent, Seq: nextSeq(out), Probe: "unknown",
					Reason: "the final ledger line was truncated, so this run may have created " +
						"an object it never recorded",
				})
				break
			}
			return nil, fmt.Errorf("%w: %s line %d: %w", ErrLedger, path, i+1, err)
		}
		out = append(out, e)
	}

	return out, nil
}

func nextSeq(entries []LedgerEntry) int {
	max := 0
	for _, e := range entries {
		if e.Seq > max {
			max = e.Seq
		}
	}
	return max + 1
}

// Intent records the intention to create, and does not return until it is durable.
//
// The fsync is the point. An intent still in a page cache when the machine dies is an intent
// that never existed, and the object it was about is then invisible to both passes of the
// sweeper.
//
// The caller must not issue the request when this returns an error: a create that cannot be
// recorded is a create that cannot be cleaned up.
func (l *Ledger) Intent(probe, path, name string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.seq++

	e := LedgerEntry{Kind: KindIntent, Seq: l.seq, Probe: probe, Path: path, Name: name}

	if err := l.append(e); err != nil {
		// The sequence is not rolled back. A gap is harmless; a reused number would make two
		// different intents indistinguishable in a file that is meant to be a record.
		return 0, err
	}

	return e.Seq, nil
}

// Resolve records what became of an intent.
func (l *Ledger) Resolve(seq int, kind EntryKind, id string, status int, reason string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	prev, ok := l.findLocked(seq)
	if !ok {
		return fmt.Errorf("%w: no intent with sequence %d", ErrLedger, seq)
	}

	return l.append(LedgerEntry{
		Kind: kind, Seq: seq,
		Probe: prev.Probe, Path: prev.Path, Name: prev.Name,
		ID: id, Status: status, Reason: reason,
	})
}

// append writes one entry. The caller holds the lock.
func (l *Ledger) append(e LedgerEntry) error {
	l.entries = append(l.entries, e)

	if l.f == nil {
		return nil
	}

	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("%w: encoding a ledger entry: %w", ErrLedger, err)
	}

	if _, err := l.f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("%w: writing %s: %w", ErrLedger, l.path, err)
	}

	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("%w: syncing %s: %w", ErrLedger, l.path, err)
	}

	return nil
}

func (l *Ledger) findLocked(seq int) (LedgerEntry, bool) {
	for _, e := range l.entries {
		if e.Seq == seq && e.Kind == KindIntent {
			return e, true
		}
	}
	return LedgerEntry{}, false
}

// Entries returns a copy of everything recorded.
func (l *Ledger) Entries() []LedgerEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]LedgerEntry, len(l.entries))
	copy(out, l.entries)

	return out
}

// Close releases the file.
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.f == nil {
		return nil
	}

	err := l.f.Close()
	l.f = nil

	if err != nil {
		return fmt.Errorf("%w: closing %s: %w", ErrLedger, l.path, err)
	}

	return nil
}

// Outstanding is an intent that may correspond to a live object.
type Outstanding struct {
	Seq   int
	Probe string
	Path  string
	// Name is always set, because the sweeper's prefix pass depends on it.
	Name string
	// ID is frequently empty, and that is the whole reason the prefix pass exists: an intent
	// that never resolved never learned an identifier, so the name is the only handle on it.
	ID string
	// Reason explains why this is outstanding, for the orphan report.
	Reason string
}

// Unresolved reconciles a ledger into the objects that may still exist.
//
// An intent resolved by `deleted` or `rejected` is clean -- the first because the object is
// gone, the second because there never was one. An intent resolved by `created`, resolved by
// `failed`, or not resolved at all is outstanding.
//
// Note which way the ambiguity is resolved: an unresolved intent is treated as *possibly
// live* rather than possibly absent. Sweeping something that does not exist costs a 404;
// failing to sweep something that does costs somebody else an afternoon.
func Unresolved(entries []LedgerEntry) []Outstanding {
	type state struct {
		intent   LedgerEntry
		resolved EntryKind
		id       string
		reason   string
	}

	bySeq := map[int]*state{}
	order := []int{}

	for _, e := range entries {
		s, ok := bySeq[e.Seq]
		if !ok {
			s = &state{}
			bySeq[e.Seq] = s
			order = append(order, e.Seq)
		}

		switch e.Kind {
		case KindIntent:
			s.intent = e
			// An intent almost never carries a reason of its own. The exception is the
			// synthetic one ReadLedger manufactures for a truncated final line, and that
			// reason is worth more to the operator than the generic wording below: it tells
			// them the process died mid-write rather than mid-flight.
			if e.Reason != "" {
				s.reason = e.Reason
			}
		case KindCreated:
			s.resolved = KindCreated
			s.id = e.ID
		case KindDeleted, KindRejected:
			s.resolved = e.Kind
		case KindFailed:
			s.resolved = KindFailed
			s.reason = e.Reason
			if e.ID != "" {
				s.id = e.ID
			}
		}
	}

	sort.Ints(order)

	out := make([]Outstanding, 0, len(order))

	for _, seq := range order {
		s := bySeq[seq]

		switch s.resolved {
		case KindDeleted, KindRejected:
			continue
		}

		reason := s.reason
		switch {
		case reason != "":
		case s.resolved == KindCreated:
			reason = "created and not deleted"
		default:
			reason = "the create was issued and its outcome was never recorded, so an object " +
				"may exist that this run never learned the identifier of"
		}

		out = append(out, Outstanding{
			Seq:    seq,
			Probe:  s.intent.Probe,
			Path:   s.intent.Path,
			Name:   s.intent.Name,
			ID:     s.id,
			Reason: reason,
		})
	}

	return out
}

// Clean reports whether a ledger has nothing outstanding.
//
// Cleanliness is reconciliation, not emptiness. A ledger recording forty creates and forty
// deletes is clean; an empty one that was truncated is not.
func Clean(entries []LedgerEntry) bool { return len(Unresolved(entries)) == 0 }

// ErrDirtyLedger marks a ledger with outstanding intents, which must be swept before another
// recording run.
//
// Refused rather than warned about, and for a reason beyond tidiness: a record run against a
// tenant holding the previous run's orphans makes the gate's maxExistingObjects assertion
// measure our own rubbish, so the safety check stops meaning what it says.
var ErrDirtyLedger = errors.New("a previous probe run left objects behind")

// DirtyError describes an unswept ledger, naming the command that fixes it.
func DirtyError(path, resource string, outstanding []Outstanding) error {
	var b strings.Builder

	fmt.Fprintf(&b, "%d object(s) may still exist from a previous run:\n", len(outstanding))
	for _, o := range outstanding {
		id := o.ID
		if id == "" {
			id = "(identifier never recorded)"
		}
		fmt.Fprintf(&b, "  %s  %s  %s\n", id, o.Name, o.Reason)
	}
	fmt.Fprintf(&b, "\nsweep them first:\n  tfpluginframeworkgen probe -mode sweep -resource %s "+
		"-profile <your profile>\n\nthe ledger is at %s", resource, path)

	return fmt.Errorf("%w: %s", ErrDirtyLedger, b.String())
}
