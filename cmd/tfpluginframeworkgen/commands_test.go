package main

import (
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/ingest/openapi"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
)

// repoRoot is where the committed blueprints, snapshot and pilot live.
const repoRoot = "../.."

func blueprintDir() string { return filepath.Join(repoRoot, "blueprints", "thousandeyes") }

// quiet silences the log output subcommands produce, so a failing test's output
// is its own assertions rather than a page of progress lines.
func quiet(t *testing.T) {
	t.Helper()

	prev := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(prev) })
}

// captureStdout redirects os.Stdout for the duration of fn.
//
// Several subcommands report to stdout by design -- a plan listing is output, not
// a diagnostic -- so testing them means capturing it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}

	prev := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		if _, err := io.Copy(&sb, r); err != nil {
			sb.WriteString("copy failed: " + err.Error())
		}
		done <- sb.String()
	}()

	fn()

	os.Stdout = prev
	_ = w.Close()

	return <-done
}

// emitInto generates the pilot provider into a scratch directory.
//
// It copies the hand-written support packages first, because generated code calls
// into them and the point of most of these tests is that the result compiles
// conceptually, not that it is generated in isolation.
func emitInto(t *testing.T) string {
	t.Helper()
	quiet(t)

	out := t.TempDir()
	if err := runEmit([]string{"-blueprint", blueprintDir(), "-out", out}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	return out
}

func TestUnit_CLI_Emit_WritesAndIsIdempotent(t *testing.T) {
	quiet(t)

	out := t.TempDir()

	if err := runEmit([]string{"-blueprint", blueprintDir(), "-out", out}); err != nil {
		t.Fatalf("first emit: %v", err)
	}

	// The manifest records what the run produced, so a later run can tell what it
	// used to produce and no longer does.
	if _, ok, err := manifest.Load(out); err != nil || !ok {
		t.Fatalf("emit should write a manifest: ok=%v err=%v", ok, err)
	}

	// A second run must change nothing, or the drift gate would fire on a no-op.
	if err := runEmit([]string{"-blueprint", blueprintDir(), "-out", out}); err != nil {
		t.Fatalf("second emit: %v", err)
	}
	if err := runVerify([]string{"-blueprint", blueprintDir(), "-out", out, "-github-summary", ""}); err != nil {
		t.Errorf("verify after a clean emit: %v", err)
	}
}

func TestUnit_CLI_Emit_DryRunAndListTouchNothing(t *testing.T) {
	quiet(t)

	out := t.TempDir()

	got := captureStdout(t, func() {
		if err := runEmit([]string{"-blueprint", blueprintDir(), "-out", out, "-dry-run"}); err != nil {
			t.Errorf("dry run: %v", err)
		}
	})

	if !strings.Contains(got, "sha256:") {
		t.Errorf("a dry run should list the files it would write:\n%s", got)
	}

	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a dry run must write nothing, found %d entries", len(entries))
	}

	// -list works without -out at all, for inspecting a blueprint.
	if err := runEmit([]string{"-blueprint", blueprintDir(), "-list"}); err != nil {
		t.Errorf("list: %v", err)
	}
}

func TestUnit_CLI_Emit_RequiredFlags(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"no blueprint":          {},
		"no out without a plan": {"-blueprint", blueprintDir()},
	}

	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := runEmit(args)
			if err == nil {
				t.Fatal("expected a usage error")
			}
			var coded exitCoder
			if !errors.As(err, &coded) || coded.ExitCode() != exitInvalidInput {
				t.Errorf("a missing required flag should be a usage error, got %v", err)
			}
		})
	}
}

// TestUnit_CLI_Emit_OnlyLeavesTheManifestAlone: a partial run must not record a
// partial inventory, or verify reports every unlisted file as an orphan.
func TestUnit_CLI_Emit_OnlyLeavesTheManifestAlone(t *testing.T) {
	quiet(t)

	out := t.TempDir()

	if err := runEmit([]string{"-blueprint", blueprintDir(), "-out", out, "-only", "tag"}); err != nil {
		t.Fatalf("emit -only: %v", err)
	}

	if _, ok, err := manifest.Load(out); err != nil {
		t.Fatalf("Load: %v", err)
	} else if ok {
		t.Error("-only must not write a manifest")
	}

	// It must also skip the provider-wide registration files, which would not
	// compile against a tree containing the rest.
	if _, err := os.Stat(filepath.Join(out, "internal", "provider", "resources.go")); !os.IsNotExist(err) {
		t.Error("-only should not emit the registration files")
	}
}

func TestUnit_CLI_Emit_UnmatchedOnlyIsAnError(t *testing.T) {
	quiet(t)

	err := runEmit([]string{"-blueprint", blueprintDir(), "-out", t.TempDir(), "-only", "nonexistent"})
	if !errors.Is(err, errNothingToDo) {
		t.Errorf("error = %v, want errNothingToDo: a mistyped filter must not look like success", err)
	}
}

// TestUnit_CLI_Verify_DetectsEachDriftClass is the gate's contract. The three
// classes have different causes and different fixes, so they are reported apart.
func TestUnit_CLI_Verify_DetectsEachDriftClass(t *testing.T) {
	quiet(t)

	t.Run("drifted", func(t *testing.T) {
		out := emitInto(t)
		target := filepath.Join(out, "internal", "provider", "resources.go")

		body, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		edited := make([]byte, 0, len(body)+16)
		edited = append(edited, body...)
		edited = append(edited, []byte("\n// edited\n")...)

		if wErr := os.WriteFile(target, edited, 0o600); wErr != nil {
			t.Fatalf("WriteFile: %v", wErr)
		}

		err = runVerify([]string{"-blueprint", blueprintDir(), "-out", out, "-github-summary", ""})
		if err == nil {
			t.Fatal("an edited generated file must be detected")
		}
		var coded exitCoder
		if !errors.As(err, &coded) || coded.ExitCode() != exitError {
			t.Errorf("drift should exit %d, got %v", exitError, err)
		}
	})

	t.Run("missing", func(t *testing.T) {
		out := emitInto(t)
		if err := os.Remove(filepath.Join(out, "internal", "provider", "datasources.go")); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if err := runVerify([]string{"-blueprint", blueprintDir(), "-out", out, "-github-summary", ""}); err == nil {
			t.Error("a deleted generated file must be detected")
		}
	})

	t.Run("orphaned", func(t *testing.T) {
		out := emitInto(t)

		// Record a file the blueprints do not produce, and leave it on disk. This
		// is what renaming a resource leaves behind.
		m, _, err := manifest.Load(out)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		stray := filepath.Join("internal", "services", "resources", "old", "gone.go")
		if err := os.MkdirAll(filepath.Join(out, filepath.Dir(stray)), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(out, stray), []byte("package old\n"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		entries := make([]manifest.Entry, 0, len(m.Files)+1)
		entries = append(entries, m.Files...)
		entries = append(entries, manifest.Entry{Path: filepath.ToSlash(stray)})
		if err := manifest.Save(out, manifest.New("dev", entries)); err != nil {
			t.Fatalf("Save: %v", err)
		}

		if err := runVerify([]string{"-blueprint", blueprintDir(), "-out", out, "-github-summary", ""}); err == nil {
			t.Error("an orphaned file must be detected")
		}
	})
}

func TestUnit_CLI_Verify_WritesAStepSummary(t *testing.T) {
	quiet(t)

	out := emitInto(t)
	target := filepath.Join(out, "internal", "provider", "resources.go")
	if err := os.WriteFile(target, []byte("package provider // clobbered\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	summary := filepath.Join(t.TempDir(), "summary.md")
	// The error is the point of the call, but the assertion is on the summary file.
	if err := runVerify([]string{"-blueprint", blueprintDir(), "-out", out, "-github-summary", summary}); err == nil {
		t.Fatal("expected drift to be reported")
	}

	body, err := os.ReadFile(summary)
	if err != nil {
		t.Fatalf("the summary should have been written: %v", err)
	}
	for _, want := range []string{"out of date", "tfpluginframeworkgen emit"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("summary omits %q:\n%s", want, body)
		}
	}
}

func TestUnit_CLI_Verify_RequiredFlags(t *testing.T) {
	t.Parallel()

	for name, args := range map[string][]string{
		"no blueprint": {},
		"no out":       {"-blueprint", blueprintDir()},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := runVerify(args); err == nil {
				t.Error("expected a usage error")
			}
		})
	}
}

func TestUnit_CLI_Verify_ToleratesATreeWithNoManifest(t *testing.T) {
	quiet(t)

	out := emitInto(t)
	if err := os.Remove(filepath.Join(out, manifest.Name)); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// "no orphans" and "could not check" are different answers, and neither is a
	// failure when everything else matches.
	if err := runVerify([]string{"-blueprint", blueprintDir(), "-out", out, "-github-summary", ""}); err != nil {
		t.Errorf("a tree with no manifest should still verify: %v", err)
	}
}

func TestUnit_CLI_Bindings(t *testing.T) {
	quiet(t)

	t.Run("the committed blueprint matches the pinned SDK", func(t *testing.T) {
		err := runBindings([]string{
			"-blueprint", blueprintDir(),
			"-module", filepath.Join(repoRoot, "pilot", "thousandeyes"),
		})
		if err != nil {
			t.Errorf("bindings: %v", err)
		}
	})

	t.Run("required flags", func(t *testing.T) {
		t.Parallel()
		if err := runBindings(nil); err == nil {
			t.Error("expected -blueprint to be required")
		}
		if err := runBindings([]string{"-blueprint", blueprintDir()}); err == nil {
			t.Error("expected -module to be required")
		}
	})
}

func TestUnit_CLI_Ingest_ListsCandidates(t *testing.T) {
	quiet(t)

	got := captureStdout(t, func() {
		err := runIngest([]string{
			"-spec-root", filepath.Join(repoRoot, "openapi-specs", "thousandeyes"),
			"-only", "tag", "-list",
		})
		if err != nil {
			t.Errorf("ingest -list: %v", err)
		}
	})

	for _, want := range []string{"KEY", "VERDICT", "tag", "resource:"} {
		if !strings.Contains(got, want) {
			t.Errorf("listing omits %q:\n%s", want, got)
		}
	}
}

func TestUnit_CLI_Ingest_InfersBlueprints(t *testing.T) {
	quiet(t)

	out := t.TempDir()

	err := runIngest([]string{
		"-spec-root", filepath.Join(repoRoot, "openapi-specs", "thousandeyes"),
		"-only", "tag", "-out", out,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	written := filepath.Join(out, "resources", "tag.blueprint.json")
	if _, err := os.Stat(written); err != nil {
		t.Fatalf("the blueprint should have been written: %v", err)
	}
}

func TestUnit_CLI_Ingest_RequiresAnOutputUnlessListing(t *testing.T) {
	quiet(t)

	err := runIngest([]string{
		"-spec-root", filepath.Join(repoRoot, "openapi-specs", "thousandeyes"), "-only", "tag",
	})
	if err == nil {
		t.Fatal("expected -out to be required")
	}
	var coded exitCoder
	if !errors.As(err, &coded) || coded.ExitCode() != exitInvalidInput {
		t.Errorf("error = %v, want a usage error", err)
	}
}

func TestUnit_CLI_Ingest_UnmatchedFilterIsAnError(t *testing.T) {
	quiet(t)

	err := runIngest([]string{
		"-spec-root", filepath.Join(repoRoot, "openapi-specs", "thousandeyes"),
		"-only", "definitelynotathing", "-list",
	})
	if !errors.Is(err, errNothingToDo) {
		t.Errorf("error = %v, want errNothingToDo", err)
	}
}

func TestUnit_CLI_Ingest_MissingSnapshotIsReported(t *testing.T) {
	quiet(t)

	err := runIngest([]string{"-spec-root", t.TempDir(), "-list"})
	if err == nil {
		t.Fatal("expected a missing snapshot to be reported")
	}
	if !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("error should mention the snapshot: %v", err)
	}
}

// TestUnit_CLI_Ingest_ReadsAnExplicitSpec covers the escape hatch for a document
// that is not pinned yet.
func TestUnit_CLI_Ingest_ReadsAnExplicitSpec(t *testing.T) {
	quiet(t)

	spec := filepath.Join(repoRoot, "openapi-specs", "thousandeyes",
		"7.0.97-t1785152261691", "api.yaml")

	captureStdout(t, func() {
		if err := runIngest([]string{"-spec", spec, "-only", "tag", "-list"}); err != nil {
			t.Errorf("ingest -spec: %v", err)
		}
	})
}

func TestUnit_CLI_CrudFlags(t *testing.T) {
	t.Parallel()

	op := &openapi.Operation{}

	tests := []struct {
		name string
		c    Candidate
		want string
	}{
		{"full lifecycle", Candidate{Create: op, Read: op, Update: op, Delete: op, List: op}, "CRUDL"},
		{"read only", Candidate{Read: op}, "-R---"},
		{"nothing", Candidate{}, "-----"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := crudFlags(tc.c); got != tc.want {
				t.Errorf("crudFlags = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUnit_CLI_Matches(t *testing.T) {
	t.Parallel()

	c := Candidate{Key: "tag", Tag: "Tags", CollectionPath: "/tags"}

	for _, want := range []string{"tag", "TAG", "Tags", "/tags"} {
		if !matches(c, want) {
			t.Errorf("matches(%q) = false", want)
		}
	}
	if matches(c, "widget") {
		t.Error("matches should be false for an unrelated term")
	}
}

func TestUnit_CLI_Version(t *testing.T) {
	t.Parallel()

	if err := runVersion(nil); err != nil {
		t.Errorf("version: %v", err)
	}
	if err := runVersion([]string{"-short"}); err != nil {
		t.Errorf("version -short: %v", err)
	}
}

func TestUnit_CLI_GlobalChdirIsApplied(t *testing.T) {
	// Not parallel: it changes the process working directory.
	quiet(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Errorf("restoring the working directory: %v", err)
		}
	})

	if rErr := runVersion([]string{"-C", repoRoot}); rErr != nil {
		t.Fatalf("version -C: %v", rErr)
	}

	now, nErr := os.Getwd()
	if nErr != nil {
		t.Fatalf("Getwd: %v", nErr)
	}
	if now == wd {
		t.Error("-C should have changed the working directory")
	}
}

func TestUnit_CLI_Emit_CleanRemovesOrphans(t *testing.T) {
	quiet(t)

	out := emitInto(t)

	// Record a file the blueprints no longer produce, and leave it on disk: what
	// renaming a resource leaves behind.
	m, _, err := manifest.Load(out)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	stray := filepath.Join("internal", "services", "resources", "gone", "old.go")
	if err := os.MkdirAll(filepath.Join(out, filepath.Dir(stray)), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(out, stray), []byte("package gone\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	entries := make([]manifest.Entry, 0, len(m.Files)+1)
	entries = append(entries, m.Files...)
	entries = append(entries, manifest.Entry{Path: filepath.ToSlash(stray)})

	if err := manifest.Save(out, manifest.New("dev", entries)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Without -clean the file is reported but kept: an orphan is usually a
	// rename, but it might be a blueprint that failed to load, and deleting a
	// working resource's files on that basis would be worse.
	if err := runEmit([]string{"-blueprint", blueprintDir(), "-out", out}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, stray)); err != nil {
		t.Error("without -clean the orphan should be kept")
	}

	if err := runEmit([]string{"-blueprint", blueprintDir(), "-out", out, "-clean"}); err != nil {
		t.Fatalf("emit -clean: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, stray)); !os.IsNotExist(err) {
		t.Error("-clean should have removed the orphan")
	}
}

// TestUnit_CLI_Emit_RefusesToClobberHandWrittenFiles is the guard that stops a
// mistyped -out destroying work with no way to recover it.
func TestUnit_CLI_Emit_RefusesToClobberHandWrittenFiles(t *testing.T) {
	quiet(t)

	out := t.TempDir()
	target := filepath.Join(out, "internal", "provider", "resources.go")

	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(target, []byte("package provider // by a person\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := runEmit([]string{"-blueprint", blueprintDir(), "-out", out}); err == nil {
		t.Fatal("overwriting a hand-written file must be refused")
	}

	// -force is the deliberate escape hatch for adopting an existing tree.
	if err := runEmit([]string{"-blueprint", blueprintDir(), "-out", out, "-force"}); err != nil {
		t.Errorf("-force should allow it: %v", err)
	}
}
