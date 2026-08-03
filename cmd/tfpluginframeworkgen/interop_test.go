package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/interop"
)

// committedSpec is the exported document CI diffs against.
func committedSpec() string {
	return filepath.Join(repoRoot, "interop-specs", "thousandeyes", "provider-code-spec.json")
}

// TestUnit_CLI_Interop_ExportMatchesTheCommittedSpec is the drift gate as a test.
//
// It runs before CI does, so a mapping change that alters the export fails here with
// a diff rather than in a workflow whose output nobody reads until the pull request
// is already open.
func TestUnit_CLI_Interop_ExportMatchesTheCommittedSpec(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile(committedSpec())
	if err != nil {
		t.Fatalf("the committed specification must exist: %v", err)
	}

	out := filepath.Join(t.TempDir(), "spec.json")

	if err := runInterop([]string{"export", "-blueprint", blueprintDir(), "-out", out, "-q"}); err != nil {
		t.Fatalf("interop export: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the export: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("the export has drifted from %s; regenerate it and review the diff", committedSpec())
	}
}

// TestUnit_CLI_Interop_TheCommittedSpecIsValid re-checks the artefact on disk rather
// than the value in memory.
//
// Those are different claims: a committed file could have been hand-edited, or
// written by an older build. This is the assertion that the whole package exists to
// support, so it is made against the thing that is actually committed.
func TestUnit_CLI_Interop_TheCommittedSpecIsValid(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(committedSpec())
	if err != nil {
		t.Fatalf("reading the committed specification: %v", err)
	}

	if err := interop.Validate(context.Background(), data); err != nil {
		t.Fatalf("the committed specification does not satisfy the upstream schema: %v", err)
	}

	var doc struct {
		Version   string                 `json:"version"`
		Provider  *struct{ Name string } `json:"provider"`
		Resources []struct {
			Name   string `json:"name"`
			Schema struct {
				Attributes []map[string]json.RawMessage `json:"attributes"`
				Blocks     []map[string]json.RawMessage `json:"blocks"`
			} `json:"schema"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing the committed specification: %v", err)
	}

	// The literal version, asserted on the artefact. Upstream switches on it
	// exactly, and the published documentation shows a form it rejects.
	if doc.Version != "0.1" {
		t.Errorf("version = %q, want \"0.1\"", doc.Version)
	}
	if doc.Provider == nil || doc.Provider.Name != "thousandeyes" {
		t.Errorf("provider = %+v, want a name-only block for thousandeyes", doc.Provider)
	}
	// Sorted by name in the export, so the alerts pair precedes credential, the
	// tests_* family and tag.
	if len(doc.Resources) != 21 || doc.Resources[0].Name != "account_group" {
		t.Fatalf("got %d resources starting %q, want 21 starting with account_group",
			len(doc.Resources), firstName(doc.Resources))
	}
	byName := map[string]int{}
	for _, r := range doc.Resources {
		byName[r.Name] = len(r.Schema.Attributes)
	}
	if got := byName["tag"]; got != 17 {
		t.Errorf("tag has %d attributes, want 17", got)
	}
	if got := byName["credential"]; got != 3 {
		t.Errorf("credential has %d attributes, want 3", got)
	}
	// Blocks are never written: the nested-attributes choice is deliberate and
	// permanent for a published provider.
	for _, r := range doc.Resources {
		if got := len(r.Schema.Blocks); got != 0 {
			t.Errorf("the export must not contain blocks, got %d on %s", got, r.Name)
		}
	}
}

func TestUnit_CLI_Interop_ExportToStdout(t *testing.T) {
	t.Parallel()

	// -out omitted writes to stdout, so `interop export | jq` is the natural way to
	// look at one. Only the exit status is asserted here; the content is covered by
	// the drift test above.
	if err := runInterop([]string{"export", "-blueprint", blueprintDir(), "-q"}); err != nil {
		t.Errorf("export to stdout: %v", err)
	}
}

func TestUnit_CLI_Interop_StrictFailsOnThePilot(t *testing.T) {
	t.Parallel()

	err := runInterop([]string{
		"export", "-blueprint", blueprintDir(), "-out", filepath.Join(t.TempDir(), "s.json"), "-strict", "-q",
	})

	// The pilot has CRUD bindings, and the format cannot carry them, so strict must
	// fail. If this ever passes, either the taxonomy stopped reporting or the
	// blueprint stopped binding -- both worth knowing about.
	if !errors.Is(err, interop.ErrDowngraded) {
		t.Errorf("error = %v, want ErrDowngraded", err)
	}
}

// TestUnit_CLI_Interop_PilotHasNoLossyNotes pins the milestone criterion.
//
// The pilot uses no int32 and no float32, so nothing in it needs coarsening. A lossy
// note appearing here means a mapping regressed into widening something it used to
// carry exactly -- a failure that would otherwise be invisible, because the export
// would still be valid and still round-trip.
func TestUnit_CLI_Interop_PilotHasNoLossyNotes(t *testing.T) {
	t.Parallel()

	reportPath := filepath.Join(t.TempDir(), "report.json")

	err := runInterop([]string{
		"export", "-blueprint", blueprintDir(),
		"-out", filepath.Join(t.TempDir(), "s.json"),
		"-report", reportPath, "-q",
	})
	if err != nil {
		t.Fatalf("interop export: %v", err)
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("reading the report: %v", err)
	}

	var report interop.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("parsing the report: %v", err)
	}

	if got := report.Count(interop.SeverityLossy); got != 0 {
		t.Errorf("the pilot should need no coarsening, got %d lossy note(s):\n%v", got, report.Sorted())
	}
	if report.Count(interop.SeverityDropped) == 0 {
		t.Error("the pilot's CRUD bindings must be reported as dropped")
	}

	// The binding note is the one a reader most needs, so it is asserted by path.
	found := false
	for _, n := range report.Notes {
		if strings.HasSuffix(n.Path, "].binding") {
			found = true
		}
	}
	if !found {
		t.Errorf("no note addressed the resource binding:\n%v", report.Sorted())
	}
}

func TestUnit_CLI_Interop_Only(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "s.json")

	if err := runInterop([]string{"export", "-blueprint", blueprintDir(), "-only", "tag", "-out", out, "-q"}); err != nil {
		t.Fatalf("interop export -only tag: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the export: %v", err)
	}
	if !strings.Contains(string(data), `"name": "tag"`) {
		t.Errorf("-only tag did not export the tag resource:\n%s", data)
	}

	// An unmatched -only is a caller mistake, not an empty success.
	err = runInterop([]string{"export", "-blueprint", blueprintDir(), "-only", "octopus", "-q"})

	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error = %v, want a usageError", err)
	}
}

// TestUnit_CLI_Interop_ImportWritesInvisibleDrafts is the empirical half of the draft
// design.
//
// TestUnit_Interop_Drafts asserts the extension does not match; this asserts the
// consequence, which is the thing actually promised: a directory of drafts is not
// something emit refuses to process, it is something emit cannot see at all. The
// distinction matters because the failure mode being designed against is a
// half-authored blueprint reaching the emitter.
func TestUnit_CLI_Interop_ImportWritesInvisibleDrafts(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "drafts")

	err := runInterop([]string{
		"import", "-spec", committedSpec(), "-provider", "thousandeyes",
		"-api-version-dir", "v7", "-service-group", "tags", "-out", out, "-q",
	})
	if err != nil {
		t.Fatalf("interop import: %v", err)
	}

	var found []string
	err = filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		found = append(found, filepath.Base(path))
		if !strings.HasSuffix(path, interop.DraftExt) {
			t.Errorf("%s is not a draft; an imported blueprint must not be loadable by emit", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the output: %v", err)
	}

	if len(found) != 22 {
		t.Errorf("wrote %d drafts (%v), want a provider draft and one per resource", len(found), found)
	}

	// The load path itself must not see them. This is the assertion that would fail
	// if findBlueprints' suffix test were ever loosened.
	if _, err := blueprint.LoadDir(out); !errors.Is(err, blueprint.ErrNoBlueprint) {
		t.Errorf("LoadDir on a draft directory = %v, want ErrNoBlueprint", err)
	}
}

// TestUnit_CLI_Interop_PromotedDraftsLoadAndFailOnBindings shows the other half: once
// renamed, a draft is a blueprint, and it fails validation for the reason the import
// report said it would.
func TestUnit_CLI_Interop_PromotedDraftsLoadAndFailOnBindings(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "drafts")

	err := runInterop([]string{
		"import", "-spec", committedSpec(), "-provider", "thousandeyes", "-out", out, "-q",
	})
	if err != nil {
		t.Fatalf("interop import: %v", err)
	}

	err = filepath.WalkDir(out, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		promoted := strings.TrimSuffix(path, interop.DraftExt) + blueprint.Ext
		return os.Rename(path, promoted)
	})
	if err != nil {
		t.Fatalf("promoting the drafts: %v", err)
	}

	_, err = blueprint.LoadDir(out)
	if !errors.Is(err, blueprint.ErrInvalid) {
		t.Fatalf("a promoted draft has no bindings, so it must not validate; got %v", err)
	}
	if !strings.Contains(err.Error(), "wire") && !strings.Contains(err.Error(), "sdk") {
		t.Errorf("the refusal should be about bindings:\n%v", err)
	}
}

func TestUnit_CLI_Interop_ImportList(t *testing.T) {
	t.Parallel()

	// -list reports what the document offers and writes nothing, so it needs neither
	// -out nor -provider.
	if err := runInterop([]string{"import", "-spec", committedSpec(), "-list"}); err != nil {
		t.Errorf("interop import -list: %v", err)
	}
}

func TestUnit_CLI_Interop_ImportRejectsBadInput(t *testing.T) {
	t.Parallel()

	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{"version":"0.1.0"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// The documented-but-rejected version, arriving as input rather than output.
	if err := runInterop([]string{"import", "-spec", bad, "-provider", "x", "-out", t.TempDir(), "-q"}); err == nil {
		t.Error(`a document declaring "0.1.0" must be refused`)
	}

	if err := runInterop([]string{
		"import", "-spec", filepath.Join(t.TempDir(), "absent.json"), "-provider", "x", "-out", t.TempDir(), "-q",
	}); err == nil {
		t.Error("a missing spec file must fail")
	}
}

func TestUnit_CLI_Interop_RejectsBadUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{"no verb", nil},
		{"unknown verb", []string{"telepathy"}},
		{"export with no blueprint", []string{"export"}},
		{"export with a bad flag", []string{"export", "-nonsense"}},
		{"import with no spec", []string{"import"}},
		{"import with no provider", []string{"import", "-spec", committedSpec(), "-out", "/tmp/x"}},
		{"import with no out", []string{"import", "-spec", committedSpec(), "-provider", "x"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := runInterop(tc.args)

			var ue *usageError
			if !errors.As(err, &ue) {
				t.Errorf("error = %v, want a usageError so the exit code is %d", err, exitInvalidInput)
			}
		})
	}
}

func TestUnit_CLI_Interop_HelpIsNotAnError(t *testing.T) {
	t.Parallel()

	// -h before the verb asks for the verb list, which is output rather than a
	// diagnostic.
	for _, arg := range []string{"-h", "-help", "--help"} {
		if err := runInterop([]string{arg}); err != nil {
			t.Errorf("interop %s = %v, want nil", arg, err)
		}
	}
}

func TestUnit_CLI_Interop_ExportReportsAMissingBlueprint(t *testing.T) {
	t.Parallel()

	err := runInterop([]string{"export", "-blueprint", filepath.Join(t.TempDir(), "absent"), "-q"})
	if err == nil {
		t.Error("a missing blueprint directory must fail")
	}
}

func TestUnit_CLI_Interop_ExportReportsAnUnwritablePath(t *testing.T) {
	t.Parallel()

	// A path whose parent is a file, not a directory.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := runInterop([]string{
		"export", "-blueprint", blueprintDir(), "-out", filepath.Join(blocker, "nested", "s.json"), "-q",
	})
	if err == nil {
		t.Error("writing under a file must fail")
	}

	// Same for the report path.
	err = runInterop([]string{
		"export", "-blueprint", blueprintDir(),
		"-out", filepath.Join(dir, "ok.json"),
		"-report", filepath.Join(blocker, "nested", "r.json"), "-q",
	})
	if err == nil {
		t.Error("writing the report under a file must fail")
	}
}

// firstName names the first resource for a failure message without dumping raw schema bytes.
func firstName(resources []struct {
	Name   string `json:"name"`
	Schema struct {
		Attributes []map[string]json.RawMessage `json:"attributes"`
		Blocks     []map[string]json.RawMessage `json:"blocks"`
	} `json:"schema"`
},
) string {
	if len(resources) == 0 {
		return "<none>"
	}
	return resources[0].Name
}
