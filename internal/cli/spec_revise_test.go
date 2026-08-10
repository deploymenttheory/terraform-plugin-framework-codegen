package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const enumCorrection = `{
  "justification": "the live API accepts and echoes this value",
  "operations": [
    {"op": "add", "path": "/paths/x-tfpfgen-note", "value": "corrected"}
  ]
}`

// importedSpecDir pins the sample document into a fresh spec directory.
func importedSpecDir(t *testing.T) string {
	t.Helper()
	source := writeSpecSource(t, specSample)
	dir := filepath.Join(t.TempDir(), "spec")
	if code, _, stderr := run(t, "spec", "import", source, "--dir", dir); code != ExitOK {
		t.Fatalf("import failed: %s", stderr)
	}
	return dir
}

// writeUnder writes content, creating every parent directory first.
func writeUnder(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUnit_SpecRevise_WritesRevisedYAMLWithNoCorrections(t *testing.T) {
	dir := importedSpecDir(t)

	code, stdout, stderr := run(t, "spec", "revise", "--dir", dir)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitOK, stderr)
	}
	for _, want := range []string{"wrote", "revised.yaml", "no corrections", "upstream document", "sha256"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "revised.yaml")); err != nil {
		t.Errorf("revised.yaml was not written: %v", err)
	}
}

func TestUnit_SpecRevise_ListsEachAppliedCorrection(t *testing.T) {
	dir := importedSpecDir(t)
	writeUnder(t, filepath.Join(dir, "corrections", "note.correction.json"), enumCorrection)

	code, stdout, stderr := run(t, "spec", "revise", "--dir", dir)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitOK, stderr)
	}
	for _, want := range []string{"1 correction applied", "note.correction.json"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestUnit_SpecRevise_FailsWhileProposedCorrectionsAwaitADecision(t *testing.T) {
	dir := importedSpecDir(t)
	writeUnder(t, filepath.Join(dir, "corrections", "proposed", "note.correction.json"), enumCorrection)

	code, _, stderr := run(t, "spec", "revise", "--dir", dir)
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	for _, want := range []string{"note.correction.json", "accept", "reject", "no flag skips this"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestUnit_SpecRevise_FailsWhenNothingIsPinned(t *testing.T) {
	code, _, stderr := run(t, "spec", "revise", "--dir", filepath.Join(t.TempDir(), "spec"))
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "tfpfgen spec import") {
		t.Fatalf("stderr does not say what to run: %q", stderr)
	}
}

func TestUnit_SpecRevise_RefusesTrailingArguments(t *testing.T) {
	code, _, stderr := run(t, "spec", "revise", "extra")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "usage: tfpfgen spec revise") {
		t.Fatalf("stderr missing the verb usage line: %q", stderr)
	}
}
