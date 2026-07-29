package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint/merge"
)

// copyBlueprints copies the committed pilot into a temp dir.
//
// Merge writes in place, and a test must not mutate the committed blueprint -- a failing test
// would otherwise leave the repository dirty and the next drift check would fail for an
// unrelated reason.
func copyBlueprints(t *testing.T) string {
	t.Helper()

	dst := filepath.Join(t.TempDir(), "blueprints")

	err := filepath.WalkDir(blueprintDir(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(blueprintDir(), path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}

		data, readErr := os.ReadFile(path) //nolint:gosec // a committed fixture
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("copying the blueprints: %v", err)
	}

	return dst
}

func factsFile(name string) string {
	return filepath.Join("testdata", "facts", name)
}

// TestUnit_CLI_Merge_NoServerDefaultConflicts is the Phase 4.5 milestone, run against the real
// committed blueprint.
//
// `color` is computed_optional on an explicitly unprobed assumption. A facts file saying the
// probe found no default must produce a conflict, change nothing, and exit non-zero -- because
// narrowing presence can break existing state, and that is a human's call.
func TestUnit_CLI_Merge_NoServerDefaultConflicts(t *testing.T) {
	t.Parallel()

	dir := copyBlueprints(t)
	before := readFile(t, filepath.Join(dir, "resources", "tag.blueprint.json"))

	err := runMerge([]string{
		"-blueprint", dir, "-facts", factsFile("color-no-default.json"), "-strategy", "apply", "-q",
	})

	if !errors.Is(err, merge.ErrConflicts) {
		t.Fatalf("error = %v, want ErrConflicts", err)
	}

	// Nothing written, even under apply.
	after := readFile(t, filepath.Join(dir, "resources", "tag.blueprint.json"))
	if after != before {
		t.Error("a conflicting merge must not change the blueprint")
	}
}

// TestUnit_CLI_Merge_CorroboratedDefaultApplies is the other half.
//
// A corroborated constant default confirms the guess, so it applies: the behaviour is recorded
// and the description gains a probed block. A *static* default is only recommended, because
// adding one changes plan output for every existing configuration.
func TestUnit_CLI_Merge_CorroboratedDefaultApplies(t *testing.T) {
	t.Parallel()

	dir := copyBlueprints(t)

	if err := runMerge([]string{
		"-blueprint", dir, "-facts", factsFile("access-type-default.json"),
		"-strategy", "apply", "-q",
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	got := readFile(t, filepath.Join(dir, "resources", "tag.blueprint.json"))

	for _, want := range []string{"serverDefault", `\"all\"`, "<!-- probed:"} {
		if !strings.Contains(got, want) {
			t.Errorf("the merged blueprint is missing %q", want)
		}
	}
	// The curated prose survives: a human wrote it and merge has no business editing it.
	if !strings.Contains(got, "The tag's access level.") {
		t.Error("the curated description was lost")
	}
	// No static default, no validator.
	if strings.Contains(got, `"default"`) {
		t.Error("merge must not add a static default: it changes plan output for every configuration")
	}

	// And the result still loads and validates, which is the thing that would break if merge
	// wrote a malformed fragment.
	if _, err := blueprint.LoadDir(dir); err != nil {
		t.Errorf("the merged blueprint does not load: %v", err)
	}
}

// TestUnit_CLI_Merge_ResourceFragmentsCarryNoProviderBlock.
//
// A regression test for a latent bug this phase surfaced: Blueprint.Provider had no omitzero,
// so every resource-only fragment written by ingest, merge or interop import carried an empty
// provider block. Noise in a committed file, and a fragment quietly claiming a provider it does
// not describe.
func TestUnit_CLI_Merge_ResourceFragmentsCarryNoProviderBlock(t *testing.T) {
	t.Parallel()

	dir := copyBlueprints(t)

	if err := runMerge([]string{
		"-blueprint", dir, "-facts", factsFile("access-type-default.json"),
		"-strategy", "apply", "-q",
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	resource := readFile(t, filepath.Join(dir, "resources", "tag.blueprint.json"))
	if strings.Contains(resource, `"provider"`) {
		t.Errorf("a resource fragment must not declare a provider block:\n%s",
			firstLines(resource, 12))
	}

	// The provider file still has one, or the blueprint would not load at all.
	provider := readFile(t, filepath.Join(dir, "provider.blueprint.json"))
	if !strings.Contains(provider, `"provider"`) {
		t.Error("the provider fragment must keep its provider block")
	}
}

// TestUnit_CLI_Merge_CheckIsTheCIGate.
//
// -check writes nothing and fails if merging would change anything, exactly parallel to verify.
// A blueprint that would change means somebody recorded evidence and did not fold it in.
func TestUnit_CLI_Merge_CheckIsTheCIGate(t *testing.T) {
	t.Parallel()

	dir := copyBlueprints(t)
	before := readFile(t, filepath.Join(dir, "resources", "tag.blueprint.json"))

	err := runMerge([]string{
		"-blueprint", dir, "-facts", factsFile("access-type-default.json"),
		"-strategy", "apply", "-check", "-q",
	})
	if err == nil {
		t.Fatal("-check must fail when merging would change the blueprint")
	}

	if after := readFile(t, filepath.Join(dir, "resources", "tag.blueprint.json")); after != before {
		t.Error("-check must write nothing")
	}

	// Once folded in, -check passes -- which is what makes it usable as a gate rather than a
	// permanent failure.
	if err := runMerge([]string{
		"-blueprint", dir, "-facts", factsFile("access-type-default.json"),
		"-strategy", "apply", "-q",
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	if err := runMerge([]string{
		"-blueprint", dir, "-facts", factsFile("access-type-default.json"),
		"-strategy", "apply", "-check", "-q",
	}); err != nil {
		t.Errorf("-check should pass once the facts are folded in: %v", err)
	}
}

// TestUnit_CLI_Merge_IsIdempotentThroughTheCLI: merging twice must leave the files byte-identical,
// or the drift gate fires on a no-op and reviewers stop trusting it.
func TestUnit_CLI_Merge_IsIdempotentThroughTheCLI(t *testing.T) {
	t.Parallel()

	dir := copyBlueprints(t)
	args := []string{
		"-blueprint", dir, "-facts", factsFile("access-type-default.json"),
		"-strategy", "apply", "-q",
	}

	if err := runMerge(args); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	first := readFile(t, filepath.Join(dir, "resources", "tag.blueprint.json"))

	if err := runMerge(args); err != nil {
		t.Fatalf("second merge: %v", err)
	}
	second := readFile(t, filepath.Join(dir, "resources", "tag.blueprint.json"))

	if first != second {
		t.Error("merging the same facts twice changed the blueprint the second time")
	}
}

// TestUnit_CLI_Merge_AcceptConflictsSuppressesOnlyTheExitCode.
//
// It never applies anything. A flag that made merge overrule a curated decision would defeat
// the whole precedence design.
func TestUnit_CLI_Merge_AcceptConflictsSuppressesOnlyTheExitCode(t *testing.T) {
	t.Parallel()

	dir := copyBlueprints(t)
	before := readFile(t, filepath.Join(dir, "resources", "tag.blueprint.json"))

	if err := runMerge([]string{
		"-blueprint", dir, "-facts", factsFile("color-no-default.json"),
		"-strategy", "apply", "-accept-conflicts", "-q",
	}); err != nil {
		t.Errorf("-accept-conflicts should suppress the exit code: %v", err)
	}

	if after := readFile(t, filepath.Join(dir, "resources", "tag.blueprint.json")); after != before {
		t.Error("-accept-conflicts must not apply the conflicting change")
	}
}

func TestUnit_CLI_Merge_WritesAStepSummary(t *testing.T) {
	t.Parallel()

	dir := copyBlueprints(t)
	summary := filepath.Join(t.TempDir(), "summary.md")

	_ = runMerge([]string{
		"-blueprint", dir, "-facts", factsFile("color-no-default.json"),
		"-github-summary", summary, "-q",
	})

	got := readFile(t, summary)
	for _, want := range []string{"Probe facts merged", "conflict(s)", "computed_optional"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary is missing %q:\n%s", want, got)
		}
	}
}

func TestUnit_CLI_Merge_RejectsBadUsage(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"no blueprint":     nil,
		"no facts":         {"-blueprint", blueprintDir()},
		"unknown strategy": {"-blueprint", blueprintDir(), "-facts", factsFile("color-no-default.json"), "-strategy", "telepathy"},
		"bad flag":         {"-nonsense"},
	}

	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var ue *usageError
			if err := runMerge(args); !errors.As(err, &ue) {
				t.Errorf("error = %v, want a usageError", err)
			}
		})
	}

	// A facts file that does not exist, and one holding an invalid fact, are input errors rather
	// than usage errors -- and the second matters: a fact with no evidence must not reach merge.
	if err := runMerge([]string{
		"-blueprint", blueprintDir(), "-facts", filepath.Join(t.TempDir(), "absent.json"), "-q",
	}); err == nil {
		t.Error("a missing facts file must fail")
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`[{"resource":"tag","field":"writable"}]`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := runMerge([]string{"-blueprint", blueprintDir(), "-facts", bad, "-q"}); err == nil {
		t.Error("a fact with no evidence must be refused on load")
	}

	empty := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(empty, []byte(`[]`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := runMerge([]string{"-blueprint", blueprintDir(), "-facts", empty, "-q"}); !errors.Is(err, errNothingToDo) {
		t.Errorf("an empty facts file = %v, want errNothingToDo", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // a temp dir this test wrote
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
