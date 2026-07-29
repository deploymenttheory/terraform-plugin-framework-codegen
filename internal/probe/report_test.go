package probe

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
)

// TestUnit_Probe_ReportSummary: a run that found nothing and a run that was never
// attempted must not read the same way in a log.
func TestUnit_Probe_ReportSummary(t *testing.T) {
	t.Parallel()

	r := Report{
		Probes: []ProbeOutcome{
			{Name: "read.volatile", Kind: KindRead, Status: "ok", Requests: 3, Facts: 1},
			{Name: "read.list-shape", Kind: KindRead, Status: "skipped", Reason: "empty collection"},
			{Name: "write.immutability", Kind: KindMutating, Status: "abandoned", Reason: "control request failed"},
			{Name: "write.enum", Kind: KindMutating, Status: "failed", Reason: "429"},
		},
		Facts:  []Fact{validFact()},
		Notes:  []Note{{Resource: "tag", Message: "no fixtures"}},
		Budget: BudgetReport{Requests: 12, Creates: 3},
	}

	got := r.Summary()

	// All four statuses counted separately, because they mean different things: skipped
	// is "does not apply here", abandoned is "could not be established honestly" -- which
	// is a correct outcome, not a failure.
	for _, want := range []string{
		"4 probe(s)", "1 ok", "1 skipped", "1 abandoned", "1 failed",
		"1 fact(s)", "1 note(s)", "12 request(s)", "3 object(s) created",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q is missing %q", got, want)
		}
	}

	// An orphan has to be shouted about, because it is somebody else's problem now.
	r.Orphans = []Orphan{{ID: "42", Path: "/tags/42"}}
	if !strings.Contains(r.Summary(), "1 ORPHANED") {
		t.Errorf("orphans must be prominent: %q", r.Summary())
	}

	// A clean empty report says so rather than saying nothing.
	var empty Report
	if !strings.Contains(empty.Summary(), "0 probe(s)") {
		t.Errorf("empty summary = %q", empty.Summary())
	}
}

// TestUnit_Probe_ReportSortIsDeterministic: the report is committed, so its order must not
// depend on which order probes happened to finish in.
func TestUnit_Probe_ReportSortIsDeterministic(t *testing.T) {
	t.Parallel()

	r := Report{
		Facts: []Fact{
			{Resource: "tag", JSONPath: "value", Field: FactWritable},
			{Resource: "tag", JSONPath: "colour", Field: FactWritable},
		},
		Notes: []Note{
			{Resource: "tag", JSONPath: "value", Message: "b"},
			{Resource: "tag", JSONPath: "colour", Message: "a"},
			{Resource: "agent", Message: "z"},
		},
		Probes: []ProbeOutcome{
			{Name: "write.enum", Kind: KindMutating},
			{Name: "read.volatile", Kind: KindRead},
			{Name: "read.list-shape", Kind: KindRead},
		},
		Orphans: []Orphan{{ID: "9"}, {ID: "1"}},
	}

	r.Sort()

	if r.Facts[0].JSONPath != "colour" {
		t.Errorf("facts are not sorted: %v", r.Facts)
	}
	if r.Notes[0].Resource != "agent" {
		t.Errorf("notes are not sorted: %v", r.Notes)
	}
	// Read probes first, matching the catalogue and the order they run in.
	if r.Probes[0].Kind != KindRead || r.Probes[0].Name != "read.list-shape" {
		t.Errorf("probes are not sorted read-first by name: %v", r.Probes)
	}
	if r.Orphans[0].ID != "1" {
		t.Errorf("orphans are not sorted: %v", r.Orphans)
	}
}

// TestUnit_Probe_FactsAtLeast: the confidence floor lives next to the levels it refers to,
// so merge cannot implement a subtly different rule.
func TestUnit_Probe_FactsAtLeast(t *testing.T) {
	t.Parallel()

	r := Report{Facts: []Fact{
		{Resource: "a", Confidence: Corroborated},
		{Resource: "b", Confidence: Observed},
		{Resource: "c", Confidence: Inferred},
		{Resource: "d", Confidence: Suspected},
	}}

	if got := len(r.FactsAtLeast(Corroborated)); got != 1 {
		t.Errorf("corroborated floor gave %d facts, want 1", got)
	}
	if got := len(r.FactsAtLeast(Observed)); got != 2 {
		t.Errorf("observed floor gave %d facts, want 2", got)
	}
	if got := len(r.FactsAtLeast(Suspected)); got != 4 {
		t.Errorf("suspected floor gave %d facts, want 4", got)
	}
}

// TestUnit_Probe_MutatingSessionNeedsAGrant is the type-level safety property, asserted.
//
// The constructor is unexported and takes a *Grant that only the gate can produce. This checks
// the remaining hole: passing nil.
func TestUnit_Probe_MutatingSessionNeedsAGrant(t *testing.T) {
	t.Parallel()

	live, err := newHTTPSession(SessionConfig{
		Transport:          cassette.DenyTransport{},
		BaseURL:            "https://example.invalid",
		CollectionTemplate: "/things",
		ItemTemplate:       "/things/{id}",
	})
	if err != nil {
		t.Fatalf("newHTTPSession: %v", err)
	}

	cfg := MutationConfig{Ledger: MemoryLedger(), NameField: "name", IDField: "id"}

	if _, err := newMutatingSession(nil, live, cfg); !errors.Is(err, ErrNoGrant) {
		t.Errorf("error = %v, want ErrNoGrant", err)
	}

	// A grant is constructible only from inside this package, which is the point: no external
	// caller can reach this path at all.
	s, err := newMutatingSession(&Grant{namePrefix: "tfpfgen-probe"}, live, cfg)
	if err != nil {
		t.Fatalf("newMutatingSession: %v", err)
	}
	if s.CollectionPath() != "/things" {
		t.Errorf("the session did not inherit its read half: %q", s.CollectionPath())
	}

	// Every remaining refusal is about being able to clean up afterwards.
	for _, tc := range []struct {
		name string
		cfg  MutationConfig
		want error
	}{
		{"no ledger", MutationConfig{NameField: "name", IDField: "id"}, ErrLedger},
		{"no name field", MutationConfig{Ledger: MemoryLedger(), IDField: "id"}, ErrInvalidPlan},
		{"no id field", MutationConfig{Ledger: MemoryLedger(), NameField: "name"}, ErrInvalidPlan},
	} {
		if _, err := newMutatingSession(&Grant{}, live, tc.cfg); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", tc.name, err, tc.want)
		}
	}

	if _, err := newMutatingSession(&Grant{}, nil, cfg); !errors.Is(err, ErrInvalidPlan) {
		t.Errorf("a session with nothing to write through should be refused, got %v", err)
	}
}

// TestUnit_Probe_NameValueIsDeterministic.
//
// ReplayTransport matches request bodies, so a create whose name carried a timestamp or a
// random suffix would make every mutating cassette unreplayable -- the same total, silent
// failure class as a base path that does not reproduce.
func TestUnit_Probe_NameValueIsDeterministic(t *testing.T) {
	t.Parallel()

	s := &MutatingSession{grant: &Grant{namePrefix: "tfpfgen-probe"}}

	got := s.NameValue("write.required", 3)
	if got != "tfpfgen-probe-write-required-3" {
		t.Errorf("NameValue = %q", got)
	}
	if again := s.NameValue("write.required", 3); again != got {
		t.Errorf("NameValue is not deterministic: %q then %q", got, again)
	}
	if s.NamePrefix() != "tfpfgen-probe" {
		t.Errorf("NamePrefix = %q", s.NamePrefix())
	}
}

// TestUnit_Probe_ReplayGrantIsMarked: the exported constructor exists so cmd can replay a
// mutating cassette, and the flag is what lets Run refuse it in record mode.
func TestUnit_Probe_ReplayGrantIsMarked(t *testing.T) {
	t.Parallel()

	g := ReplayGrant("tfpfgen-probe")

	if !g.IsReplay() {
		t.Error("a replay grant must be marked as one")
	}
	if g.NamePrefix() != "tfpfgen-probe" {
		t.Errorf("NamePrefix = %q", g.NamePrefix())
	}

	// The nil receiver is reachable: Run holds a *Grant that is nil for a read-only run.
	var none *Grant
	if none.IsReplay() || none.NamePrefix() != "" {
		t.Error("a nil grant must answer safely")
	}
}

func TestUnit_Probe_ReadOnlySession(t *testing.T) {
	t.Parallel()

	s := readOnly{collection: "/v7/tags", item: "/v7/tags/{testId}"}

	if got := s.CollectionPath(); got != "/v7/tags" {
		t.Errorf("CollectionPath = %q", got)
	}
	// The parameter is not named "id", which is the case a literal match would get wrong.
	if got := s.ItemPath("42"); got != "/v7/tags/42" {
		t.Errorf("ItemPath = %q, want /v7/tags/42", got)
	}
	if _, err := s.Get(context.Background(), "/v7/tags", url.Values{}); !errors.Is(err, errNotImplemented) {
		t.Errorf("Get = %v, want errNotImplemented", err)
	}
}

func TestUnit_Probe_ReadableFields(t *testing.T) {
	t.Parallel()

	subj := testSubject()

	// Everything is assumed readable until a probe observes otherwise, which is exactly
	// the assumption read.returned-weak and write.writable-returned exist to test.
	if got := len(subj.ReadableFields()); got != len(subj.Fields) {
		t.Errorf("ReadableFields gave %d of %d fields", got, len(subj.Fields))
	}
	if got := len(subj.WritableFields()); got != 2 {
		t.Errorf("WritableFields gave %d, want 2", got)
	}
}
