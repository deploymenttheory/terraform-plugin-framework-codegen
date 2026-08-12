package revise

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/store"
)

// proposedFiles lists the compiled proposals currently on disk.
func proposedFiles(t *testing.T, specDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(specDir, correction.DirName, ProposedDirName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// acceptAll moves every proposed correction into the accepted directory, the
// way a human accepts. Only the corrections move: the report describing them
// is not one, and the pull-request job leaves it where it is.
func acceptAll(t *testing.T, specDir string) {
	t.Helper()
	correctionsDir := filepath.Join(specDir, correction.DirName)
	for _, name := range proposedFiles(t, specDir) {
		if !strings.HasSuffix(name, correction.Suffix) {
			continue
		}
		from := filepath.Join(correctionsDir, ProposedDirName, name)
		if err := os.Rename(from, filepath.Join(correctionsDir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnit_Propose_IsByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256),
		confirmedObs("color", observe.KindValues,
			observe.Values{Accepted: []string{"red", "green"}, Rejected: []string{"blue"}, Closed: boolPtr(false)}, nil, lock.SHA256),
		confirmedObs("", observe.KindUpdateStyle, "patch-merge", nil, lock.SHA256),
	)

	read := func() map[string][]byte {
		p, err := Propose(specDir)
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		out := map[string][]byte{}
		for _, w := range p.Proposed {
			raw, err := os.ReadFile(w.Path)
			if err != nil {
				t.Fatal(err)
			}
			out[w.Path] = raw
		}
		return out
	}

	first := read()
	second := read()
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("proposal counts differ: %d then %d", len(first), len(second))
	}
	for path, raw := range first {
		if !bytes.Equal(raw, second[path]) {
			t.Errorf("%s differs between runs:\nfirst:\n%s\nsecond:\n%s", path, raw, second[path])
		}
	}
}

func TestUnit_Propose_ARejectedMarkerSuppressesReProposal(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	o := confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256)
	commitObs(t, root, o)

	id := observe.ComputeID("tag", "name", observe.KindRequiredByAPI, nil)
	marker := `{
  "observationID": "` + id + `",
  "reason": "the requirement was a transient API bug the vendor has acknowledged",
  "rejectedAt": "2026-08-01T12:00:00Z"
}`
	markerPath := filepath.Join(specDir, correction.DirName, RejectedDirName, "requiredByAPI-name.json")
	writeFile(t, markerPath, marker)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 0 {
		t.Errorf("Proposed = %+v; a rejected marker must suppress re-proposal", p.Proposed)
	}
	if len(p.Suppressed) != 1 || p.Suppressed[0].ObservationID != id ||
		!strings.Contains(p.Suppressed[0].Reason, "transient API bug") {
		t.Fatalf("Suppressed = %+v, want the marker's reason on the observation", p.Suppressed)
	}
	if files := proposedFiles(t, specDir); len(files) != 0 {
		t.Errorf("proposed/ holds %v despite the marker", files)
	}
}

func TestUnit_Propose_RefusesAMarkerNamingNoObservation(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256))
	writeFile(t, filepath.Join(specDir, correction.DirName, RejectedDirName, "empty.json"),
		`{"reason": "no", "rejectedAt": "2026-08-01T12:00:00Z"}`)

	_, err := Propose(specDir)
	if err == nil || !strings.Contains(err.Error(), "names no observationID") {
		t.Fatalf("a marker naming no observation must refuse: %v", err)
	}
}

func TestUnit_Propose_RefusesAMarkerGivingNoReason(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256))
	writeFile(t, filepath.Join(specDir, correction.DirName, RejectedDirName, "mute.json"),
		`{"observationID": "abc123"}`)

	_, err := Propose(specDir)
	if err == nil || !strings.Contains(err.Error(), "gives no reason") {
		t.Fatalf("a marker giving no reason must refuse: %v", err)
	}
}

func TestUnit_Propose_RefusesAMarkerWithUnknownKeys(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256))
	writeFile(t, filepath.Join(specDir, correction.DirName, RejectedDirName, "odd.json"),
		`{"observationID": "abc", "reason": "no", "observation": "abc"}`)

	_, err := Propose(specDir)
	if err == nil || !strings.Contains(err.Error(), "not a usable rejected marker") {
		t.Fatalf("a marker with unknown keys must refuse: %v", err)
	}
}

func TestUnit_Propose_NonConfirmedOutcomesNeverCompile(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	blocked := confirmedObs("name", observe.KindRequiredByAPI, nil, nil, lock.SHA256)
	blocked.Outcome = observe.OutcomeBlocked
	inconclusive := confirmedObs("mode", observe.KindServerDefault, nil, nil, lock.SHA256)
	inconclusive.Outcome = observe.OutcomeInconclusive
	exhausted := confirmedObs("", observe.KindUpdateStyle, nil, nil, lock.SHA256)
	exhausted.Outcome = observe.OutcomeTimeoutExhausted
	commitObs(t, root, blocked, inconclusive, exhausted)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed)+len(p.AutoAccepted) != 0 {
		t.Errorf("non-confirmed observations compiled: %+v %+v", p.Proposed, p.AutoAccepted)
	}
	if len(p.NotConfirmed) != 3 {
		t.Fatalf("NotConfirmed = %+v, want all three", p.NotConfirmed)
	}
	for _, n := range p.NotConfirmed {
		if !strings.Contains(n.Reason, "never compiles") {
			t.Errorf("reason %q does not say the outcome never compiles", n.Reason)
		}
	}
}

func TestUnit_Propose_FlagsAStaleObservationAndStillCompilesIt(t *testing.T) {
	t.Parallel()
	root, specDir, _ := pinnedTree(t)
	commitObs(t, root, confirmedObs("name", observe.KindRequiredByAPI, true, nil, "0000superseded"))

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Stale) != 1 || p.Stale[0].Reason != "observed against a superseded document" {
		t.Fatalf("Stale = %+v, want the superseded-document wording", p.Stale)
	}
	if len(p.Proposed) != 1 || !p.Proposed[0].Stale {
		t.Fatalf("Proposed = %+v; a stale observation still compiles, flagged", p.Proposed)
	}
}

func TestUnit_Propose_AutoAcceptedKindsSkipProposed(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		confirmedObs("", observe.KindDeleteNotFoundOK, true, nil, lock.SHA256),
		confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256),
	)

	p, err := ProposeWith(specDir, Options{AutoAccept: []string{"deleteNotFoundOK"}})
	if err != nil {
		t.Fatalf("ProposeWith: %v", err)
	}
	if len(p.AutoAccepted) != 1 || p.AutoAccepted[0].Kind != observe.KindDeleteNotFoundOK {
		t.Fatalf("AutoAccepted = %+v, want the deleteNotFoundOK correction", p.AutoAccepted)
	}
	// The entity-level observation sorts first, so it takes ordinal 001 of
	// the shared sequence and the proposal takes 002.
	if got := filepath.Base(p.AutoAccepted[0].Path); got != "auto-001-tag.correction.json" {
		t.Errorf("auto file name = %s, want the auto- prefix on the shared ordinal", got)
	}
	if dir := filepath.Dir(p.AutoAccepted[0].Path); dir != filepath.Join(specDir, correction.DirName) {
		t.Errorf("auto correction landed in %s, not the accepted directory", dir)
	}
	if len(p.Proposed) != 1 || p.Proposed[0].Kind != observe.KindRequiredByAPI ||
		filepath.Base(p.Proposed[0].Path) != "002-tag.correction.json" {
		t.Fatalf("Proposed = %+v, want only the non-auto kind at ordinal 002", p.Proposed)
	}

	// The auto-accepted correction is already accepted: once the proposal is
	// decided too, Materialize applies both.
	acceptAll(t, specDir)
	res, err := Materialize(specDir)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(res.Applied) != 2 {
		t.Errorf("Applied = %v, want the accepted and the auto-accepted correction", res.Applied)
	}
}

func TestUnit_Propose_RefusesAnUnknownAutoAcceptKind(t *testing.T) {
	t.Parallel()
	_, specDir, _ := pinnedTree(t)
	_, err := ProposeWith(specDir, Options{AutoAccept: []string{"deleteNotFound"}})
	if err == nil || !strings.Contains(err.Error(), `"deleteNotFound" is not an observation kind`) {
		t.Fatalf("an unknown auto_accept kind must refuse before anything is written: %v", err)
	}
}

func TestUnit_Propose_AnAutoNameCollisionBumpsRatherThanClobbers(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	// A previous run's auto file for a different observation already sits at
	// the ordinal this run would use.
	other := `{
  "justification": "an earlier run's evidence",
  "evidence": "audit/observations/tag.observations.json#ffffffffffffffff",
  "operations": [{"op": "add", "path": "/paths/~1tags/post/x-tfpfgen-eventual-consistency", "value": "1s"}]
}`
	writeFile(t, filepath.Join(specDir, correction.DirName, "auto-001-tag.correction.json"), other)
	commitObs(t, root, confirmedObs("", observe.KindDeleteNotFoundOK, true, nil, lock.SHA256))

	p, err := ProposeWith(specDir, Options{AutoAccept: []string{"deleteNotFoundOK"}})
	if err != nil {
		t.Fatalf("ProposeWith: %v", err)
	}
	if len(p.AutoAccepted) != 1 || filepath.Base(p.AutoAccepted[0].Path) != "auto-002-tag.correction.json" {
		t.Fatalf("AutoAccepted = %+v, want the ordinal bumped past the occupied name", p.AutoAccepted)
	}
	if raw, err := os.ReadFile(filepath.Join(specDir, correction.DirName, "auto-001-tag.correction.json")); err != nil ||
		!strings.Contains(string(raw), "an earlier run's evidence") {
		t.Errorf("the earlier run's file was clobbered: %v\n%s", err, raw)
	}
}

func TestUnit_Propose_ClearsItsOwnStaleProposalsAndLeavesHandAuthoredOnes(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	proposedDir := filepath.Join(specDir, correction.DirName, ProposedDirName)
	writeFile(t, filepath.Join(proposedDir, "005-tag.correction.json"), `{"stale": "from an earlier run"}`)
	writeFile(t, filepath.Join(proposedDir, "hand-authored.correction.json"), `{"justification": "mine"}`)
	commitObs(t, root, confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256))

	if _, err := Propose(specDir); err != nil {
		t.Fatalf("Propose: %v", err)
	}
	files := proposedFiles(t, specDir)
	// The report describing this run's proposals lands beside them.
	want := []string{"001-tag.correction.json", "hand-authored.correction.json", ReportName}
	if len(files) != 3 || files[0] != want[0] || files[1] != want[1] || files[2] != want[2] {
		t.Errorf("proposed/ = %v, want %v — compiled files replaced, hand-authored kept", files, want)
	}
}

func TestUnit_Propose_WithNoObservationsReportsNothingAndWritesNothing(t *testing.T) {
	t.Parallel()
	_, specDir, _ := pinnedTree(t)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if p.Observations != 0 || len(p.Proposed)+len(p.AutoAccepted) != 0 {
		t.Errorf("Proposals = %+v, want an empty report", p)
	}
	if files := proposedFiles(t, specDir); len(files) != 0 {
		t.Errorf("proposed/ = %v, want nothing", files)
	}
}

func TestUnit_Propose_ReadsObservationsFromAnOverrideDirectory(t *testing.T) {
	t.Parallel()
	_, specDir, lock := pinnedTree(t)
	elsewhere := filepath.Join(t.TempDir(), "obs")
	if err := observe.Write(elsewhere, []observe.Observation{
		confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256),
	}); err != nil {
		t.Fatal(err)
	}

	p, err := ProposeWith(specDir, Options{ObservationsDir: elsewhere})
	if err != nil {
		t.Fatalf("ProposeWith: %v", err)
	}
	if len(p.Proposed) != 1 {
		t.Fatalf("Proposed = %+v, want the override directory's observation", p.Proposed)
	}
}

func TestUnit_Propose_RefusesWhenNothingIsPinned(t *testing.T) {
	t.Parallel()
	_, err := Propose(filepath.Join(t.TempDir(), "spec"))
	if err == nil || !strings.Contains(err.Error(), "tfpfgen spec import") {
		t.Fatalf("error does not say what to run: %v", err)
	}
}

// TestIntegration_Propose_FullLoopConverges walks the whole evidence loop:
// quirkserver-flavoured observations compile into proposals, a human accepts
// them, Materialize folds them into the revised spec, and a re-propose
// against that spec yields nothing — the fixed point re-auditing relies on.
func TestIntegration_Propose_FullLoopConverges(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		// The key/object_type quirk: required but undeclared.
		confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256),
		// SilentlyDiscards: sent on create, never stored.
		confirmedObs("size", observe.KindWritable, false, nil, lock.SHA256),
		// ImmutableAfterCreate.
		confirmedObs("name", observe.KindImmutable, true, nil, lock.SHA256),
		// ConstantDefaults.
		confirmedObs("mode", observe.KindServerDefault, "auto", nil, lock.SHA256),
		// ConditionallyRequired: port matters only when protocol is tcp.
		confirmedObs("port", observe.KindRequiredWhen, true,
			&observe.Condition{Attribute: "protocol", Equals: "tcp"}, lock.SHA256),
		// The documented enum disagrees with the live API.
		confirmedObs("color", observe.KindValues,
			observe.Values{Accepted: []string{"red", "green"}, Rejected: []string{"blue"}, Closed: boolPtr(false)},
			nil, lock.SHA256),
		confirmedObs("", observe.KindUpdateStyle, "put-full", nil, lock.SHA256),
		confirmedObs("", observe.KindDeleteNotFoundOK, true, nil, lock.SHA256),
		confirmedObs("", observe.KindReadAfterWrite, "2s", nil, lock.SHA256),
	)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 9 {
		t.Fatalf("Proposed %d corrections, want 9: %+v", len(p.Proposed), p.Proposed)
	}

	// The gate holds while they await a decision.
	if _, err := Materialize(specDir); err == nil {
		t.Fatal("Materialize succeeded with proposals pending")
	}

	acceptAll(t, specDir)
	res, err := Materialize(specDir)
	if err != nil {
		t.Fatalf("Materialize after accepting: %v", err)
	}
	if len(res.Applied) != 9 {
		t.Errorf("Applied = %d corrections, want 9", len(res.Applied))
	}

	revised, err := os.ReadFile(filepath.Join(specDir, OutputName))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(revised, &doc); err != nil {
		t.Fatalf("revised.yaml is not usable YAML: %v", err)
	}

	tag := doc["components"].(map[string]any)["schemas"].(map[string]any)["Tag"].(map[string]any)
	props := tag["properties"].(map[string]any)
	if req, ok := tag["required"].([]any); !ok || len(req) != 1 || req[0] != "name" {
		t.Errorf("required = %v, want [name]", tag["required"])
	}
	if ro := props["size"].(map[string]any)["readOnly"]; ro != true {
		t.Errorf("size.readOnly = %v, want true", ro)
	}
	if co := props["name"].(map[string]any)["x-tfpfgen-create-only"]; co != true {
		t.Errorf("name create-only = %v, want true", co)
	}
	if def := props["mode"].(map[string]any)["x-tfpfgen-server-default"]; def != "auto" {
		t.Errorf("mode server-default = %v, want auto", def)
	}
	rw, ok := props["port"].(map[string]any)["x-tfpfgen-required-when"].(map[string]any)
	if !ok || rw["property"] != "protocol" || rw["equals"] != "tcp" {
		t.Errorf("port required-when = %v, want protocol=tcp", props["port"])
	}
	color := props["color"].(map[string]any)
	if enum, ok := color["enum"].([]any); !ok || len(enum) != 2 || enum[0] != "red" || enum[1] != "green" {
		t.Errorf("color.enum = %v, want [red green]", color["enum"])
	}
	if open := color["x-tfpfgen-values-open"]; open != true {
		t.Errorf("color values-open = %v, want true", open)
	}
	item := doc["paths"].(map[string]any)["/tags/{tagId}"].(map[string]any)
	if style := item["put"].(map[string]any)["x-tfpfgen-update-style"]; style != "put-full" {
		t.Errorf("update style = %v, want put-full", style)
	}
	if dnf := item["delete"].(map[string]any)["x-tfpfgen-delete-not-found-ok"]; dnf != true {
		t.Errorf("delete-not-found-ok = %v, want true", dnf)
	}
	if ec := item["get"].(map[string]any)["x-tfpfgen-eventual-consistency"]; ec != "2s" {
		t.Errorf("eventual-consistency = %v, want 2s", ec)
	}

	// Convergence: the accepted corrections now state every fact, so a
	// re-propose yields nothing new and the gate stays open.
	p2, err := Propose(specDir)
	if err != nil {
		t.Fatalf("re-Propose: %v", err)
	}
	if len(p2.Proposed)+len(p2.AutoAccepted) != 0 {
		t.Errorf("re-propose wrote %+v %+v; the loop must converge", p2.Proposed, p2.AutoAccepted)
	}
	if len(p2.AlreadyStated) != 9 {
		t.Errorf("AlreadyStated = %d, want all 9 facts recognised", len(p2.AlreadyStated))
	}
	if _, err := Materialize(specDir); err != nil {
		t.Errorf("Materialize after convergence: %v", err)
	}

	// And the second materialization is byte-identical to the first.
	second, err := os.ReadFile(filepath.Join(specDir, OutputName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(revised, second) {
		t.Error("revised.yaml changed across the converged loop")
	}
}

// A superseded pin plus the observation's own hash is the pairing the stale
// wording is about; the marker keeps its suppression across pin moves too.
func TestUnit_Propose_MarkerSurvivesAPinMove(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	o := confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256)
	commitObs(t, root, o)
	id := observe.ComputeID("tag", "name", observe.KindRequiredByAPI, nil)
	writeFile(t, filepath.Join(specDir, correction.DirName, RejectedDirName, "m.json"),
		`{"observationID": "`+id+`", "reason": "vendor bug", "rejectedAt": "2026-08-01T00:00:00Z"}`)

	// Move the pin: same entity surface, new document bytes.
	if _, err := store.Import(specDir, []byte(tagSpec+"# moved\n"), "published.yaml"); err != nil {
		t.Fatal(err)
	}

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Suppressed) != 1 || len(p.Proposed) != 0 {
		t.Fatalf("Suppressed = %+v, Proposed = %+v; the marker must hold across a pin move", p.Suppressed, p.Proposed)
	}
	if len(p.Stale) != 0 {
		t.Errorf("Stale = %+v; a suppressed observation is not reported stale", p.Stale)
	}
}
