package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const specSample = `openapi: 3.0.3
info:
  title: sample
  version: 1.2.3
paths: {}
`

// writeSpecSource writes a document a test imports from.
func writeSpecSource(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "published.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnit_SpecImport_PinsADocumentFromAFile(t *testing.T) {
	source := writeSpecSource(t, specSample)
	dir := filepath.Join(t.TempDir(), "spec")

	code, stdout, stderr := run(t, "spec", "import", source, "--dir", dir)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitOK, stderr)
	}
	for _, want := range []string{"pinned", "openapi 3.0.3", "version 1.2.3", "sha256"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "upstream.yaml")); err != nil {
		t.Errorf("upstream.yaml was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "upstream.lock.json")); err != nil {
		t.Errorf("upstream.lock.json was not written: %v", err)
	}
}

func TestUnit_SpecImport_SaysNothingChangedOnAnIdenticalReimport(t *testing.T) {
	source := writeSpecSource(t, specSample)
	dir := filepath.Join(t.TempDir(), "spec")

	if code, _, stderr := run(t, "spec", "import", source, "--dir", dir); code != ExitOK {
		t.Fatalf("first import failed: %s", stderr)
	}
	code, stdout, stderr := run(t, "spec", "import", source, "--dir", dir)
	if code != ExitOK {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "nothing changed") {
		t.Fatalf("stdout does not say the re-import was a no-op:\n%s", stdout)
	}
}

func TestUnit_SpecImport_ReportsOldAndNewPinsOnAChangedReimport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spec")

	first := writeSpecSource(t, specSample)
	if code, _, stderr := run(t, "spec", "import", first, "--dir", dir); code != ExitOK {
		t.Fatalf("first import failed: %s", stderr)
	}

	second := writeSpecSource(t, strings.ReplaceAll(specSample, "1.2.3", "2.0.0"))
	code, stdout, stderr := run(t, "spec", "import", second, "--dir", dir)
	if code != ExitOK {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr)
	}
	for _, want := range []string{"re-imported", "was:", "now:", "version 1.2.3", "version 2.0.0"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestUnit_SpecImport_OmitsTheDocumentVersionWhenNoneIsDeclared(t *testing.T) {
	source := writeSpecSource(t, "openapi: 3.1.0\npaths: {}\n")
	code, stdout, stderr := run(t, "spec", "import", source, "--dir", filepath.Join(t.TempDir(), "spec"))
	if code != ExitOK {
		t.Fatalf("exit code = %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "openapi 3.1.0") {
		t.Errorf("stdout missing the dialect version:\n%s", stdout)
	}
	if strings.Contains(stdout, "version ,") || strings.Contains(stdout, "version )") {
		t.Errorf("stdout renders an empty document version:\n%s", stdout)
	}
}

func TestUnit_SpecImport_RefusesANonOpenAPIDocument(t *testing.T) {
	source := writeSpecSource(t, "just: prose\n")
	code, _, stderr := run(t, "spec", "import", source, "--dir", filepath.Join(t.TempDir(), "spec"))
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "openapi") {
		t.Fatalf("stderr does not name the missing openapi key: %q", stderr)
	}
}

func TestUnit_SpecImport_RefusesAMissingSourceArgument(t *testing.T) {
	code, _, stderr := run(t, "spec", "import")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "usage: tfpfgen spec import <url|file>") {
		t.Fatalf("stderr missing the verb usage line: %q", stderr)
	}
}

func TestUnit_SpecImport_RefusesTrailingArguments(t *testing.T) {
	code, _, stderr := run(t, "spec", "import", "one.yaml", "two.yaml")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "usage: tfpfgen spec import <url|file>") {
		t.Fatalf("stderr missing the verb usage line: %q", stderr)
	}
}

func TestUnit_SpecVerify_PassesOnACleanPin(t *testing.T) {
	source := writeSpecSource(t, specSample)
	dir := filepath.Join(t.TempDir(), "spec")
	if code, _, stderr := run(t, "spec", "import", source, "--dir", dir); code != ExitOK {
		t.Fatalf("import failed: %s", stderr)
	}

	code, stdout, stderr := run(t, "spec", "verify", "--dir", dir)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitOK, stderr)
	}
	if !strings.Contains(stdout, "matches its lock") {
		t.Fatalf("stdout does not confirm the match:\n%s", stdout)
	}
}

func TestUnit_SpecVerify_FailsWhenNothingIsPinned(t *testing.T) {
	code, _, stderr := run(t, "spec", "verify", "--dir", filepath.Join(t.TempDir(), "spec"))
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "tfpfgen spec import") {
		t.Fatalf("stderr does not say what to run: %q", stderr)
	}
}

func TestUnit_SpecVerify_FailsOnATamperedDocument(t *testing.T) {
	source := writeSpecSource(t, specSample)
	dir := filepath.Join(t.TempDir(), "spec")
	if code, _, stderr := run(t, "spec", "import", source, "--dir", dir); code != ExitOK {
		t.Fatalf("import failed: %s", stderr)
	}
	if err := os.WriteFile(filepath.Join(dir, "upstream.yaml"), []byte("openapi: 3.0.3\nedited: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := run(t, "spec", "verify", "--dir", dir)
	if code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "does not match its lock") {
		t.Fatalf("stderr does not explain the mismatch: %q", stderr)
	}
}

func TestUnit_SpecVerify_RefusesTrailingArguments(t *testing.T) {
	code, _, stderr := run(t, "spec", "verify", "extra")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "usage: tfpfgen spec verify") {
		t.Fatalf("stderr missing the verb usage line: %q", stderr)
	}
}

func TestUnit_Spec_UsageListsEveryVerb(t *testing.T) {
	_, stdout, _ := run(t, "spec", "--help")
	for _, want := range []string{
		"import", "pin the upstream OpenAPI document",
		"verify", "check the pinned document",
		"revise", "materialize the revised spec",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("spec --help missing %q:\n%s", want, stdout)
		}
	}
}
