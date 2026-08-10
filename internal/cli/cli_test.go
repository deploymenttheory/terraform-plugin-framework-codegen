package cli

import (
	"strings"
	"testing"
)

// run captures both streams and the exit code for one invocation.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, err strings.Builder
	code = Run(args, &out, &err)
	return code, out.String(), err.String()
}

func TestUnit_CLI_NoArgumentsExplainsAndExitsUsage(t *testing.T) {
	code, stdout, stderr := run(t)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "a command is required") {
		t.Fatalf("stderr does not explain the problem: %q", stderr)
	}
	if !strings.Contains(stderr, "usage: tfpfgen <noun> <verb>") {
		t.Fatalf("stderr missing the grammar line: %q", stderr)
	}
}

func TestUnit_CLI_UnknownCommandNamesItselfAndExitsUsage(t *testing.T) {
	code, _, stderr := run(t, "conjure", "provider")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, `unknown command "conjure`) {
		t.Fatalf("stderr does not name the unknown command: %q", stderr)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Fatalf("stderr missing usage after the error: %q", stderr)
	}
}

func TestUnit_CLI_UsageListsEveryCommandGroup(t *testing.T) {
	_, _, stderr := run(t)
	for _, want := range []string{
		"config", "work with tfpfgen.yaml",
		"version", "report the toolkit version",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("usage does not mention %q:\n%s", want, stderr)
		}
	}
}

func TestUnit_Version_PrintsTheVersion(t *testing.T) {
	code, stdout, stderr := run(t, "version")
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, ExitOK, stderr)
	}
	if strings.TrimSpace(stdout) != "dev" {
		t.Fatalf("stdout = %q, want the unstamped version %q", stdout, "dev")
	}
}

func TestUnit_Version_RefusesTrailingArguments(t *testing.T) {
	code, _, stderr := run(t, "version", "extra")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "usage: tfpfgen version") {
		t.Fatalf("stderr missing the verb usage line: %q", stderr)
	}
}
