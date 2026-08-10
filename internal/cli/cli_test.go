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

func TestUnit_CLI_NoArgumentsPrintsUsageAndExitsUsage(t *testing.T) {
	code, stdout, stderr := run(t)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "usage: tfpfgen <noun> <verb>") {
		t.Fatalf("stderr missing usage line: %q", stderr)
	}
}

func TestUnit_CLI_UnknownCommandNamesItselfAndExitsUsage(t *testing.T) {
	code, _, stderr := run(t, "conjure", "provider")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, `unknown command "conjure provider"`) {
		t.Fatalf("stderr does not name the unknown command: %q", stderr)
	}
	if !strings.Contains(stderr, "usage: tfpfgen") {
		t.Fatalf("stderr missing usage after the error: %q", stderr)
	}
}

func TestUnit_CLI_UsageListsEveryRegisteredCommand(t *testing.T) {
	_, _, stderr := run(t)
	for _, c := range commands() {
		if !strings.Contains(stderr, c.Name) {
			t.Errorf("usage does not list %q", c.Name)
		}
		if !strings.Contains(stderr, c.Summary) {
			t.Errorf("usage does not carry the summary for %q", c.Name)
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
