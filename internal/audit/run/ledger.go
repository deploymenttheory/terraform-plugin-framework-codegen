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

// The audit activity ledger records every object the run brings into
// existence, durably, before the request that creates it is sent.
//
// The failure that strands an object is not a create that fails — it is a
// create that succeeds and whose response is never seen: a timeout, a
// SIGKILL between sending and reading. The object exists and nothing in
// the process knows its identifier. So the intent line is written and
// fsynced before the request goes out; an intent that never resolves is
// the signature of exactly that failure, and it is what tells cleanup to
// hunt by name prefix rather than by id. The file is append-only, one
// JSON object per line, in the runs directory — never committed, because
// it records live objects in somebody's tenant.

// activityKind is what one activity-ledger line records.
type activityKind string

const (
	// activityIntent is written before a create request is sent.
	activityIntent activityKind = "intent"
	// activityCreated resolves an intent: the object exists, id known.
	activityCreated activityKind = "created"
	// activityRejected resolves an intent with no object made: a 4xx is
	// reliable evidence of that; a 5xx or a network error is not, and
	// leaves the intent outstanding on purpose.
	activityRejected activityKind = "rejected"
	// activityDeleted resolves a created object: it is gone.
	activityDeleted activityKind = "deleted"
)

// activityEntry is one line. It carries enough to delete the object with no
// other state: the item path template with every parent already
// substituted, so cleanup after a crash needs only this file.
type activityEntry struct {
	Kind   activityKind `json:"kind"`
	Seq    int          `json:"seq"`
	Entity string       `json:"entity"`
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

// activityFileSuffix names activity-ledger files in the runs directory:
// <runid>.activity.jsonl, one per run.
const activityFileSuffix = ".activity.jsonl"

// activityLedger appends entries durably.
type activityLedger struct {
	mu      sync.Mutex
	path    string
	f       *os.File
	seq     int
	entries []activityEntry
}

// openActivityLedger opens this run's activity ledger for appending. An
// empty dir returns a memory-only ledger, which newRunner allows only for
// plans that never mutate.
func openActivityLedger(dir, runID string) (*activityLedger, error) {
	if dir == "" {
		return &activityLedger{}, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("audit run: creating the runs directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, runID+activityFileSuffix)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // operator-supplied runs dir by design
	if err != nil {
		return nil, fmt.Errorf("audit run: opening the ledger %s: %w", path, err)
	}
	return &activityLedger{path: path, f: f}, nil
}

// intent records the intention to create and does not return until it is
// durable. The caller must not send the request when this errors: a
// create that cannot be recorded is a create that cannot be cleaned up.
func (l *activityLedger) intent(entity, name, itemPath string) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	return l.seq, l.append(activityEntry{
		Kind: activityIntent, Seq: l.seq, Entity: entity, Name: name, ItemPath: itemPath,
	})
}

// resolve records what became of an intent.
func (l *activityLedger) resolve(seq int, kind activityKind, id string, status int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.entries {
		if e.Seq == seq && e.Kind == activityIntent {
			// Best-effort by design: a resolution that cannot be written
			// leaves the intent outstanding, which errs toward removing.
			_ = l.append(activityEntry{
				Kind: kind, Seq: seq, Entity: e.Entity, Name: e.Name,
				ItemPath: e.ItemPath, ID: id, Status: status,
			})
			return
		}
	}
}

// append writes one line and fsyncs it. Caller holds the lock.
func (l *activityLedger) append(e activityEntry) error {
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
func (l *activityLedger) unresolved() []activityEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return unresolvedOf(l.entries)
}

func unresolvedOf(entries []activityEntry) []activityEntry {
	type state struct {
		intent activityEntry
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
		case activityIntent:
			s.intent = e
		case activityCreated:
			s.id = e.ID
		case activityDeleted, activityRejected:
			s.closed = true
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(order)))
	var out []activityEntry
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
func (l *activityLedger) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		_ = l.f.Close()
		l.f = nil
	}
}

// remove deletes a fully reconciled ledger file; a file with outstanding
// entries is kept, because it is the only record of what may still exist.
func (l *activityLedger) remove() {
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

// readActivityFile loads a previous run's activity ledger. Only the final line may
// be malformed — that is what a crash mid-write looks like — and it is
// read as an unresolved intent rather than a parse error, because making
// cleanup impossible over a torn line would strand the very objects the
// ledger exists to find.
func readActivityFile(path string) ([]activityEntry, error) {
	f, err := os.Open(path) //nolint:gosec // operator-supplied runs dir by design
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

	var out []activityEntry
	for i, line := range lines {
		var e activityEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			if i == len(lines)-1 {
				out = append(out, activityEntry{Kind: activityIntent, Seq: maxSeq(out) + 1})
				break
			}
			return nil, fmt.Errorf("%s line %d: %w", path, i+1, err)
		}
		out = append(out, e)
	}
	return out, nil
}

func maxSeq(entries []activityEntry) int {
	m := 0
	for _, e := range entries {
		if e.Seq > m {
			m = e.Seq
		}
	}
	return m
}
