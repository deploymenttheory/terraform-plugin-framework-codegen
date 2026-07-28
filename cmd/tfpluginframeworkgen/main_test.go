package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

// exitCodeOf reports the exit code main would use for err, mirroring main's
// logic. Asserting on the code rather than on the message is what makes these
// tests a contract: the codes are published in the CLI's documentation, and
// scripts branch on them.
func exitCodeOf(t *testing.T, err error) int {
	t.Helper()

	if err == nil {
		return exitOK
	}
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}

	var coded exitCoder
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return exitError
}

func TestUnit_CLI_Dispatch_ExitCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no arguments is a usage error", nil, exitInvalidInput},
		{"unknown command is a usage error", []string{"bogus"}, exitInvalidInput},
		{"help is not a failure", []string{"help"}, exitOK},
		{"short help flag is not a failure", []string{"-h"}, exitOK},
		{"long help flag is not a failure", []string{"--help"}, exitOK},
		{"version succeeds", []string{"version"}, exitOK},
		{"version short flag succeeds", []string{"version", "-short"}, exitOK},
		{"subcommand help is not a failure", []string{"version", "-h"}, exitOK},
		// A malformed flag must not be reported as an internal failure: exit 1
		// would tell a caller the tool broke rather than that they mistyped.
		{"undefined flag is a usage error", []string{"version", "-nope"}, exitInvalidInput},
		{"unbuilt subcommand reports a plain failure", []string{"scaffold"}, exitError},
		// A built subcommand missing a required flag is the caller's mistake, and
		// must not be reported the same way as a subcommand that does not exist yet.
		{"built subcommand missing a required flag is a usage error", []string{"emit"}, exitInvalidInput},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := exitCodeOf(t, run(tc.args, io.Discard))
			if got != tc.want {
				t.Errorf("run(%q) exit code = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

// builtCommands are the subcommands that are actually implemented. Adding one
// here is part of implementing it, and the test below checks the claim in both
// directions so the list cannot quietly go stale.
var builtCommands = map[string]bool{
	"version":  true,
	"emit":     true,
	"verify":   true,
	"bindings": true,
	// ingest is partially built: -list discovers candidates from a pinned
	// snapshot, while blueprint inference is not written yet. It counts as built
	// because invoking it does real work and fails for real reasons, rather than
	// reporting that it does not exist.
	"ingest": true,
	// interop has its export verb; import is not written yet. Same reasoning as
	// ingest: `interop` with no verb reports that it needs one, which is a real
	// answer rather than a claim that the subcommand does not exist.
	"interop": true,
	// probe has its catalogue and -list; the transports and the gate are not written
	// yet. It counts as built because it loads a real blueprint, flattens it and
	// reports real costs -- and because an unbuilt *mode* returns errNotImplemented
	// rather than exiting zero, which is asserted separately.
	"probe": true,
}

// TestUnit_CLI_Dispatch_ImplementationClaimsAreTrue guards two failure modes at
// once.
//
// An unbuilt subcommand that silently succeeds is worse than one that is missing,
// because CI would go green on a no-op. And a built subcommand still reporting
// "not implemented" means somebody wired the registry to the wrong function,
// which is easy to do and invisible until someone tries to use it.
func TestUnit_CLI_Dispatch_ImplementationClaimsAreTrue(t *testing.T) {
	t.Parallel()

	for _, c := range commands {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			// Invoked with no arguments, so a built subcommand fails on its
			// required flags rather than doing any work.
			err := run([]string{c.name}, io.Discard)

			if builtCommands[c.name] {
				if errors.Is(err, errNotImplemented) {
					t.Errorf("%q is listed as built but reports errNotImplemented; the registry entry is wrong", c.name)
				}
				return
			}

			if !errors.Is(err, errNotImplemented) {
				t.Errorf("run(%q) error = %v, want it to wrap errNotImplemented", c.name, err)
			}
		})
	}
}

// TestUnit_CLI_Registry_IsWellFormed catches the copy-paste mistakes that a
// hand-maintained dispatch table invites: a duplicated name silently shadows a
// subcommand, and an empty summary leaves a blank line in the help output.
func TestUnit_CLI_Registry_IsWellFormed(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, len(commands))

	for _, c := range commands {
		switch {
		case c.name == "":
			t.Error("a command has an empty name")
		case seen[c.name]:
			t.Errorf("command %q is registered twice", c.name)
		case c.summary == "":
			t.Errorf("command %q has no summary, so help output has a blank line", c.name)
		case c.run == nil:
			t.Errorf("command %q has no run function and would panic on dispatch", c.name)
		}
		seen[c.name] = true
	}

	// "help" is handled by run's switch, so registering it as a command too
	// would create two code paths for the same word.
	if seen["help"] {
		t.Error(`"help" is handled by run and must not also be a registered command`)
	}
}

func TestUnit_CLI_Usage_ListsEveryCommand(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	printUsage(&buf)
	out := buf.String()

	for _, c := range commands {
		if !strings.Contains(out, c.name) {
			t.Errorf("usage output omits command %q", c.name)
		}
		if !strings.Contains(out, c.summary) {
			t.Errorf("usage output omits the summary for %q", c.name)
		}
	}
}
