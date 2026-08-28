package revise

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/correction"
)

// readReport loads the report a Propose run wrote, failing when there is none.
func readReport(t *testing.T, specDir string) Report {
	t.Helper()
	path := filepath.Join(specDir, correction.DirName, ProposedDirName, ReportName)
	raw, err := os.ReadFile(path) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatalf("reading the report: %v", err)
	}
	var rep Report
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rep); err != nil {
		t.Fatalf("decoding the report: %v", err)
	}
	return rep
}

// withExcerpt attaches one excerpt to an observation, the proof a narratable
// finding is built from.
func withExcerpt(o observe.Observation, method, path string, status int, request, response string) observe.Observation {
	o.Excerpts = append(o.Excerpts, observe.Excerpt{
		Method: method, PathTemplate: path, Status: status,
		RequestFragment: json.RawMessage(request), ResponseFragment: json.RawMessage(response),
	})
	return o
}

func TestUnit_Report_GroupsByEntityAndKind(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		confirmedObs("color", observe.KindServerDefault, "red", nil, lock.SHA256),
		confirmedObs("size", observe.KindServerDefault, "large", nil, lock.SHA256),
		confirmedObs("mode", observe.KindServerDefault, "fast", nil, lock.SHA256),
		confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256),
	)
	if _, err := Propose(specDir); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	rep := readReport(t, specDir)
	if len(rep.Groups) != 2 {
		t.Fatalf("groups = %d, want 2 (one per kind); got %+v", len(rep.Groups), rep.Groups)
	}
	// Sorted by entity then kind: requiredByAPI before serverDefault.
	if rep.Groups[0].Kind != observe.KindRequiredByAPI || rep.Groups[1].Kind != observe.KindServerDefault {
		t.Fatalf("groups are not in kind order: %s then %s", rep.Groups[0].Kind, rep.Groups[1].Kind)
	}

	defaults := rep.Groups[1]
	if len(defaults.Findings) != 3 {
		t.Fatalf("serverDefault findings = %d, want 3", len(defaults.Findings))
	}
	if got, want := defaults.Summary, "3 server-assigned defaults"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
	if got, want := defaults.Branch, "tfpfgen/correction-tag-server-default"; got != want {
		t.Errorf("Branch = %q, want %q", got, want)
	}
	// Findings sort by attribute, and every one names its own file and ID.
	var attributes []string
	for _, f := range defaults.Findings {
		attributes = append(attributes, f.Attribute)
		if f.File == "" || f.ObservationID == "" || f.Evidence == "" {
			t.Errorf("finding %+v is missing its identity", f)
		}
		if len(f.Operations) == 0 {
			t.Errorf("finding %s carries no operations", f.Attribute)
		}
	}
	if got, want := strings.Join(attributes, ","), "color,mode,size"; got != want {
		t.Errorf("finding order = %q, want %q", got, want)
	}
	if len(defaults.ObservationIDs) != 3 || len(defaults.Files) != 3 {
		t.Errorf("group carries %d ids and %d files, want 3 of each", len(defaults.ObservationIDs), len(defaults.Files))
	}
}

func TestUnit_Report_NarratesEachFindingInPlainEnglish(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, withExcerpt(
		confirmedObs("color", observe.KindServerDefault, "#A7EB10", nil, lock.SHA256),
		"POST", "/tags", 201, `{"name":"tfpfgen-test-1"}`, `{"name":"tfpfgen-test-1","color":"#A7EB10"}`))
	if _, err := Propose(specDir); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	rep := readReport(t, specDir)
	if len(rep.Groups) != 1 || len(rep.Groups[0].Findings) != 1 {
		t.Fatalf("want one group of one finding, got %+v", rep.Groups)
	}
	g := rep.Groups[0]
	f := g.Findings[0]

	if !strings.Contains(f.Observed, "`color`") || !strings.Contains(f.Observed, "`#A7EB10`") {
		t.Errorf("Observed names neither the attribute nor the value: %q", f.Observed)
	}
	if !strings.Contains(f.Expected, "declares no default") {
		t.Errorf("Expected does not say what the document led us to expect: %q", f.Expected)
	}
	if !strings.Contains(f.Means, "drift") {
		t.Errorf("Means does not say what it costs: %q", f.Means)
	}
	if !strings.HasPrefix(g.Merging, "Merging ") || !strings.HasPrefix(g.Closing, "Closing ") {
		t.Errorf("the group does not say what each decision does: %q / %q", g.Merging, g.Closing)
	}
	if len(f.Excerpts) != 1 {
		t.Fatalf("excerpts = %d, want the one that was recorded", len(f.Excerpts))
	}
	e := f.Excerpts[0]
	if e.Method != "POST" || e.PathTemplate != "/tags" || e.Status != 201 {
		t.Errorf("the excerpt lost the request it proves: %+v", e)
	}
	if !strings.Contains(string(e.ResponseFragment), "#A7EB10") {
		t.Errorf("the response fragment lost the value it proves: %s", e.ResponseFragment)
	}
}

// TestUnit_Report_ATokenPlantedInAnExcerptCannotReachTheReport is the
// redaction assertion. Excerpts are redacted at capture, but
// audit/observations/ is committed and hand-editable, and a pull request body
// is far more public than a repository file.
func TestUnit_Report_ATokenPlantedInAnExcerptCannotReachTheReport(t *testing.T) {
	t.Parallel()
	const planted = "sk-live-000102030405060708090a0b"
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, withExcerpt(
		confirmedObs("color", observe.KindServerDefault, "red", nil, lock.SHA256),
		"POST", "/tags", 201,
		`{"authorization":"Bearer `+planted+`","name":"t"}`,
		`{"apiToken":"`+planted+`","color":"red"}`))
	if _, err := Propose(specDir); err != nil {
		t.Fatalf("Propose: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(specDir, correction.DirName, ProposedDirName, ReportName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(planted)) {
		t.Fatalf("the planted token reached the report:\n%s", raw)
	}
	if !bytes.Contains(raw, []byte("[redacted]")) {
		t.Errorf("nothing was redacted, so the excerpts were not passed through Redact:\n%s", raw)
	}
	// The finding itself must survive: redaction removes the secret, not the
	// evidence around it.
	if !bytes.Contains(raw, []byte(`"color"`)) || !bytes.Contains(raw, []byte(`"red"`)) {
		t.Errorf("redaction took the evidence with it:\n%s", raw)
	}
}

func TestUnit_Report_IsByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		confirmedObs("size", observe.KindServerDefault, "large", nil, lock.SHA256),
		confirmedObs("color", observe.KindServerDefault, "red", nil, lock.SHA256),
		confirmedObs("", observe.KindUpdateStyle, "patch-merge", nil, lock.SHA256),
	)
	path := filepath.Join(specDir, correction.DirName, ProposedDirName, ReportName)

	read := func() []byte {
		if _, err := Propose(specDir); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		raw, err := os.ReadFile(path) //nolint:gosec // a path this test built
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	first, second := read(), read()
	if !bytes.Equal(first, second) {
		t.Errorf("the report is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestUnit_Report_IsAbsentWhenNothingIsProposed(t *testing.T) {
	t.Parallel()
	_, specDir, _ := pinnedTree(t)
	if _, err := Propose(specDir); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	path := filepath.Join(specDir, correction.DirName, ProposedDirName, ReportName)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a run that proposed nothing left a report (stat: %v)", err)
	}
}

// TestUnit_Report_LastRunsReportDoesNotOutliveItsProposals is what stops the
// pull-request job chasing files a later run no longer wrote.
func TestUnit_Report_LastRunsReportDoesNotOutliveItsProposals(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, confirmedObs("color", observe.KindServerDefault, "red", nil, lock.SHA256))
	if _, err := Propose(specDir); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	path := filepath.Join(specDir, correction.DirName, ProposedDirName, ReportName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the first run wrote no report: %v", err)
	}

	// Accept it, then re-propose: the fact is now stated, so nothing is
	// proposed and no report may remain.
	acceptAll(t, specDir)
	if _, err := Propose(specDir); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the stale report survived a run that proposed nothing (stat: %v)", err)
	}
}

// TestUnit_Report_AutoAcceptedCorrectionsAreNotNarrated: the report describes
// decisions a human is being asked to make, and an auto-accepted correction
// is not one.
func TestUnit_Report_AutoAcceptedCorrectionsAreNotNarrated(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		confirmedObs("color", observe.KindServerDefault, "red", nil, lock.SHA256),
		confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256),
	)
	if _, err := ProposeWith(specDir, Options{AutoAccept: []string{string(observe.KindServerDefault)}}); err != nil {
		t.Fatalf("ProposeWith: %v", err)
	}
	rep := readReport(t, specDir)
	for _, g := range rep.Groups {
		if g.Kind == observe.KindServerDefault {
			t.Errorf("the auto-accepted kind was narrated as a pending decision: %+v", g)
		}
	}
	if len(rep.Groups) != 1 || rep.Groups[0].Kind != observe.KindRequiredByAPI {
		t.Errorf("want only the requiredByAPI group, got %+v", rep.Groups)
	}
}

func TestUnit_Report_GroupBranchIsStableAndSanitised(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		entity string
		kind   observe.Kind
		want   string
	}{
		{"tag", observe.KindServerDefault, "tfpfgen/correction-tag-server-default"},
		{"endpoint_tests_http_server", observe.KindReadAfterWrite,
			"tfpfgen/correction-endpoint_tests_http_server-read-after-write"},
		{"tag", observe.KindUndocumentedFieldInSpec,
			"tfpfgen/correction-tag-undocumented-field-in-spec"},
		// An acronym stays whole rather than becoming required-by-a-p-i.
		{"tag", observe.KindRequiredByAPI, "tfpfgen/correction-tag-required-by-api"},
		{"a b/c..d", observe.KindValues, "tfpfgen/correction-a-b-c-d-values"},
	} {
		if got := GroupBranch(tc.entity, tc.kind); got != tc.want {
			t.Errorf("GroupBranch(%q, %s) = %q, want %q", tc.entity, tc.kind, got, tc.want)
		}
		if got := GroupBranch(tc.entity, tc.kind); got != GroupBranch(tc.entity, tc.kind) {
			t.Errorf("GroupBranch is not stable for %q/%s", tc.entity, tc.kind)
		}
	}
}

func TestUnit_Report_ValueSpellings(t *testing.T) {
	t.Parallel()
	closed := false
	for _, tc := range []struct {
		name string
		obs  observe.Observation
		want string
	}{
		{"scalar", observe.Observation{Kind: observe.KindServerDefault, Value: "red"}, "`red`"},
		{"number", observe.Observation{Kind: observe.KindServerDefault, Value: 8080}, "`8080`"},
		{"duration", observe.Observation{Kind: observe.KindReadAfterWrite, Value: "2s"}, "`2s`"},
		{"boolean kinds say nothing", observe.Observation{Kind: observe.KindImmutable, Value: true}, ""},
		{"no value at all", observe.Observation{Kind: observe.KindServerDefault}, ""},
		{"field list", observe.Observation{Kind: observe.KindMutuallyExclusive, Value: []string{"b", "a"}}, "`a` and `b`"},
		{"three fields", observe.Observation{Kind: observe.KindMutuallyExclusive, Value: []string{"c", "b", "a"}},
			"`a`, `b` and `c`"},
		{"values record", observe.Observation{Kind: observe.KindValues,
			Value: observe.Values{Accepted: []string{"red"}, Rejected: []string{"blue"}, Closed: &closed}},
			"it refused the documented value `blue`, it took `red`, and it took a value outside the documented set entirely"},
		{"wrapped list", observe.Observation{Kind: observe.KindListResponseShape,
			Value: observe.ListResponseShape{Envelope: "wrapped", Key: "data", Pagination: "cursor"}},
			"the items arrived wrapped under `data`, paginated by cursor"},
		{"bare list", observe.Observation{Kind: observe.KindListResponseShape,
			Value: observe.ListResponseShape{Envelope: "bare", Pagination: "none"}},
			"the items arrived as a bare array, with no pagination"},
	} {
		if got := describeValue(tc.obs); got != tc.want {
			t.Errorf("%s: describeValue = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestUnit_Report_UnreadableCompoundValuesSpellAsNothing(t *testing.T) {
	t.Parallel()
	// A value that cannot be marshalled into the record its kind claims
	// leaves the sentence without a detail rather than crashing the run.
	for _, kind := range []observe.Kind{observe.KindValues, observe.KindListResponseShape} {
		if got := describeValue(observe.Observation{Kind: kind, Value: func() {}}); got != "" {
			t.Errorf("%s: describeValue = %q, want empty", kind, got)
		}
	}
	if got := describeValue(observe.Observation{Kind: observe.KindValues, Value: observe.Values{}}); got != "" {
		t.Errorf("an empty values record spelled as %q, want empty", got)
	}
	if got := describeValue(observe.Observation{Kind: observe.KindMutuallyExclusive, Value: "not a list"}); got != "`not a list`" {
		t.Errorf("a non-list mutuallyExclusive value spelled as %q", got)
	}
}

func TestUnit_Report_CapsTheExcerptsItCarries(t *testing.T) {
	t.Parallel()
	var o observe.Observation
	for range MaxReportExcerpts + 3 {
		o.Excerpts = append(o.Excerpts, observe.Excerpt{Method: "GET", PathTemplate: "/tags"})
	}
	if got := len(reportExcerpts(o.Excerpts)); got != MaxReportExcerpts {
		t.Errorf("reportExcerpts kept %d, want the %d cap", got, MaxReportExcerpts)
	}
	if reportExcerpts(nil) != nil {
		t.Error("reportExcerpts invented evidence from none")
	}
}

// TestUnit_Report_IsNotMistakenForACorrection: the report shares a directory
// with the proposals, so every scanner that reads that directory must pass it
// by — the gate's pending-decision scan above all.
func TestUnit_Report_IsNotMistakenForACorrection(t *testing.T) {
	t.Parallel()
	if strings.HasSuffix(ReportName, correction.Suffix) {
		t.Fatalf("%s ends in the correction suffix; the gate would count it as a pending decision", ReportName)
	}
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, confirmedObs("color", observe.KindServerDefault, "red", nil, lock.SHA256))
	if _, err := Propose(specDir); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	acceptAll(t, specDir)
	// The report is still there; Materialize must not read it as a pending
	// decision, and correction.Load must not read it as a correction.
	if _, err := os.Stat(filepath.Join(specDir, correction.DirName, ProposedDirName, ReportName)); err != nil {
		t.Fatalf("the report vanished: %v", err)
	}
	if _, err := Materialize(specDir); err != nil {
		t.Fatalf("the report blocked materialization: %v", err)
	}
}
