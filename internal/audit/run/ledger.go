package run

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// The ledger records every object the run brings into existence, durably,
// before the request that creates it is sent.
//
// The failure that strands an object is not a create that fails — it is a
// create that succeeds and whose response is never seen: a timeout, a
// SIGKILL between sending and reading. The object exists and nothing in
// the process knows its identifier. So the intent line is written and
// fsynced before the request goes out; an intent that never resolves is
// the signature of exactly that failure, and it is what tells cleanup to
// hunt by name prefix rather than by id. The file is append-only, one
// JSON object per line, in the work directory — never committed, because
// it records live objects in somebody's tenant.

// ledgerKind is what one ledger line records.
type ledgerKind string

const (
	// ledgerIntent is written before a create request is sent.
	ledgerIntent ledgerKind = "intent"
	// ledgerCreated resolves an intent: the object exists, id known.
	ledgerCreated ledgerKind = "created"
	// ledgerRejected resolves an intent with no object made: a 4xx is
	// reliable evidence of that; a 5xx or a network error is not, and
	// leaves the intent outstanding on purpose.
	ledgerRejected ledgerKind = "rejected"
	// ledgerDeleted resolves a created object: it is gone.
	ledgerDeleted ledgerKind = "deleted"
)

// ledgerEntry is one line. It carries enough to delete the object with no
// other state: the item path template with every parent already
// substituted, so cleanup after a crash needs only this file.
type ledgerEntry struct {
	Kind   ledgerKind `json:"kind"`
	Seq    int        `json:"seq"`
	Entity string     `json:"entity"`
	// Name is the value stamped into the object's name-bearing field, the
	// prefix pass's only handle on an object whose id was never learned.
	Name string `json:"name,omitempty"`
	// ItemPath is the item path with parent parameters substituted and
	// the object's own parameter still templated, e.g.
	// "/projects/42/tags/{tagId}".
	ItemPath string `json:"itemPath,omitempty"`
	ID       string `json:"id,omitempty"`
	Status   int    `json:"status,omitempty"`
}

// ledgerFileSuffix names ledger files in the work directory, one per run.
const ledgerFileSuffix = ".created.jsonl"

// ledger appends entries durably.
type ledger struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	seq     int
	entries []ledgerEntry
}

// openLedger opens this run's ledger for appending. An empty dir returns
// a memory-only ledger, which newRunner allows only for plans that never
// mutate.
func openLedger(dir, runID string) (*ledger, error) {
	if dir == "" {
		return &ledger{}, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("audit run: creating the work directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, runID+ledgerFileSuffix)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // operator-supplied work dir by design
	if err != nil {
		return nil, fmt.Errorf("audit run: opening the ledger %s: %w", path, err)
	}
	return &ledger{path: path, f: f}, nil
}

// intent records the intention to create and does not return until it is
// durable. The caller must not send the request when this errors: a
// create that cannot be recorded is a create that cannot be cleaned up.
func (l *ledger) intent(entity, name, itemPath string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	return l.seq, l.append(ledgerEntry{
		Kind: ledgerIntent, Seq: l.seq, Entity: entity, Name: name, ItemPath: itemPath,
	})
}

// resolve records what became of an intent.
func (l *ledger) resolve(seq int, kind ledgerKind, id string, status int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.entries {
		if e.Seq == seq && e.Kind == ledgerIntent {
			// Best-effort by design: a resolution that cannot be written
			// leaves the intent outstanding, which errs toward removing.
			_ = l.append(ledgerEntry{
				Kind: kind, Seq: seq, Entity: e.Entity, Name: e.Name,
				ItemPath: e.ItemPath, ID: id, Status: status,
			})
			return
		}
	}
}

// append writes one line and fsyncs it. Caller holds the lock.
func (l *ledger) append(e ledgerEntry) error {
	l.entries = append(l.entries, e)
	if l.f == nil {
		return nil
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("audit run: encoding a ledger entry: %w", err)
	}
	if _, err := l.f.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("audit run: writing %s: %w", l.path, err)
	}
	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("audit run: syncing %s: %w", l.path, err)
	}
	return nil
}

// unresolved reconciles the ledger into the objects that may still exist,
// newest first — children were created after the parents whose paths
// embed them, so deleting in reverse creation order deletes children
// before parents.
func (l *ledger) unresolved() []ledgerEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return unresolvedOf(l.entries)
}

func unresolvedOf(entries []ledgerEntry) []ledgerEntry {
	type state struct {
		intent ledgerEntry
		id     string
		closed bool
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
		case ledgerIntent:
			s.intent = e
		case ledgerCreated:
			s.id = e.ID
		case ledgerDeleted, ledgerRejected:
			s.closed = true
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(order)))
	var out []ledgerEntry
	for _, seq := range order {
		s := bySeq[seq]
		if s.closed {
			continue
		}
		e := s.intent
		e.ID = s.id
		out = append(out, e)
	}
	return out
}

// close releases the file.
func (l *ledger) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
}

// remove deletes a fully reconciled ledger file; a file with outstanding
// entries is kept, because it is the only record of what may still exist.
func (l *ledger) remove() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.path == "" || len(unresolvedOf(l.entries)) > 0 {
		return
	}
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
	_ = os.Remove(l.path)
}

// readLedgerFile loads a previous run's ledger. Only the final line may
// be malformed — that is what a crash mid-write looks like — and it is
// read as an unresolved intent rather than a parse error, because making
// cleanup impossible over a torn line would strand the very objects the
// ledger exists to find.
func readLedgerFile(path string) ([]ledgerEntry, error) {
	f, err := os.Open(path) //nolint:gosec // operator-supplied work dir by design
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var out []ledgerEntry
	for i, line := range lines {
		var e ledgerEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			if i == len(lines)-1 {
				out = append(out, ledgerEntry{Kind: ledgerIntent, Seq: maxSeq(out) + 1})
				break
			}
			return nil, fmt.Errorf("%s line %d: %w", path, i+1, err)
		}
		out = append(out, e)
	}
	return out, nil
}

func maxSeq(entries []ledgerEntry) int {
	m := 0
	for _, e := range entries {
		if e.Seq > m {
			m = e.Seq
		}
	}
	return m
}
