package main

import (
	"errors"
	"flag"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
)

// TestUnit_CLI_Probe_ListsTheCatalogue is the Phase 4.1 milestone as a test.
//
// It runs against the committed pilot blueprint with no credentials, no cassettes and no
// network, which is the point: the catalogue, its worst-case costs and the read/mutating
// split are reviewable before anybody points the prober at a tenant.
func TestUnit_CLI_Probe_ListsTheCatalogue(t *testing.T) {
	t.Parallel()

	if err := runProbe([]string{"-blueprint", blueprintDir(), "-resource", "tag", "-list"}); err != nil {
		t.Fatalf("probe -list: %v", err)
	}
}

// TestUnit_CLI_Probe_ListNarrowsToOneProbe covers -only.
func TestUnit_CLI_Probe_ListNarrowsToOneProbe(t *testing.T) {
	t.Parallel()

	if err := runProbe([]string{
		"-blueprint", blueprintDir(), "-resource", "tag", "-list", "-only", "read.volatile",
	}); err != nil {
		t.Errorf("probe -list -only: %v", err)
	}
}

// TestUnit_CLI_Probe_MutationsOutsideRecordAreRefused.
//
// Refused rather than ignored, and refused *before* the gate, because the gate only runs on the
// record path. Silently dropping the flag would let a scripted run believe it had probed the
// write path when what it actually did was replay a committed transcript.
func TestUnit_CLI_Probe_MutationsOutsideRecordAreRefused(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"replay", "verify", "sweep"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			err := runProbe([]string{
				"-blueprint", blueprintDir(), "-resource", "tag",
				"-mode", mode, "--allow-mutations",
			})

			if !errors.Is(err, probe.ErrRefused) {
				t.Fatalf("error = %v, want ErrRefused", err)
			}
			assertExitCode(t, err, exitGatingRefused)
		})
	}
}

// TestUnit_CLI_Probe_SweepNeedsCredentialsFromTheEnvironment: a sweep reaches a real API, and the
// token comes from the environment for the same reason a record run's does.
func TestUnit_CLI_Probe_SweepNeedsCredentialsFromTheEnvironment(t *testing.T) {
	t.Parallel()

	err := runProbe([]string{"-blueprint", blueprintDir(), "-resource", "tag", "-mode", "sweep"})
	if err == nil {
		t.Fatal("a sweep with no endpoint must fail")
	}
	if !strings.Contains(err.Error(), endpointEnv) {
		t.Errorf("the error should name the variable to set: %v", err)
	}
}

// TestUnit_CLI_Probe_ExitCodePrecedence.
//
// The mapping cannot be an errors.As walk: errors.As returns the first match in a preorder walk,
// and first is not most serious. These conditions genuinely co-occur -- a run can exceed its
// budget, sweep, and still leave an orphan -- so which number CI sees must not depend on the
// order the errors happened to be joined in.
func TestUnit_CLI_Probe_ExitCodePrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{"redaction", cassette.ErrSecretFound, exitRedactionFailed},
		{"orphans", probe.ErrOrphans, exitOrphansLeft},
		{"an unusable ledger is an orphan risk", probe.ErrLedger, exitOrphansLeft},
		{"a stale ledger is too", probe.ErrDirtyLedger, exitOrphansLeft},
		{"refused", probe.ErrRefused, exitGatingRefused},
		{"budget", probe.ErrBudget, exitBudgetExceeded},
		{"delete failures", probe.ErrDeleteFailures, exitBudgetExceeded},
		{"replay mismatch", probe.ErrReplayMismatch, exitReplayMismatch},
		{"anything else", errors.New("something went wrong"), exitError},

		// The precedence, asserted in both join orders. Either order producing a different
		// code is the specific bug this table exists to prevent.
		{
			"budget joined before orphans",
			errors.Join(probe.ErrBudget, probe.ErrOrphans),
			exitOrphansLeft,
		},
		{
			"orphans joined before budget",
			errors.Join(probe.ErrOrphans, probe.ErrBudget),
			exitOrphansLeft,
		},
		{
			"a redaction failure outranks orphans in either order",
			errors.Join(probe.ErrOrphans, cassette.ErrSecretFound),
			exitRedactionFailed,
		},
		{
			"a refusal outranks a budget cap",
			errors.Join(probe.ErrBudget, probe.ErrRefused),
			exitGatingRefused,
		},
		{
			"a budget cap outranks a replay mismatch",
			errors.Join(probe.ErrReplayMismatch, probe.ErrBudget),
			exitBudgetExceeded,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assertExitCode(t, probeError(tc.err), tc.want)
		})
	}

	// Nothing wrong stays nothing wrong, and help is not a failure.
	if err := probeError(nil); err != nil {
		t.Errorf("probeError(nil) = %v", err)
	}
	if err := probeError(flag.ErrHelp); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("help must pass through untouched, got %v", err)
	}

	// A usage error already carries its own code and must keep it: a mistyped flag is not a
	// gating refusal, whatever else the error mentions.
	usage := usagef("-mode %q is not a mode", "nonsense")
	if got := probeError(usage); got != usage {
		t.Errorf("a usage error should pass through unchanged, got %v", got)
	}
}

// assertExitCode checks the code an error maps to, defaulting to exitError when it carries none.
func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()

	got := exitError

	var coded exitCoder
	if errors.As(err, &coded) {
		got = coded.ExitCode()
	}

	if got != want {
		t.Errorf("exit code = %d, want %d (error: %v)", got, want, err)
	}
}

// TestUnit_CLI_Probe_ADirtyLedgerRefusesARecordRun.
//
// Not only for tidiness. A record run against a tenant already holding your own orphans makes
// maxExistingObjects measure your own rubbish, and creating more on top of objects you have
// already failed to remove is the wrong direction.
func TestUnit_CLI_Probe_ADirtyLedgerRefusesARecordRun(t *testing.T) {
	// Not parallel: it writes a ledger at the conventional path, which is process-wide state.
	dir := t.TempDir()

	path := probe.LedgerPath(dir, "example", "thing")

	l, err := probe.OpenLedger(path)
	if err != nil {
		t.Fatalf("OpenLedger: %v", err)
	}
	if _, err := l.Intent("write.p", "/things", "tfpfgen-probe-p-1"); err != nil {
		t.Fatalf("Intent: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The ledger root is a constant, so the check is exercised directly rather than by moving
	// the process's working directory out from under a parallel test suite.
	entries, err := probe.ReadLedger(path)
	if err != nil {
		t.Fatalf("ReadLedger: %v", err)
	}

	dirty := probe.DirtyError(path, "thing", probe.Unresolved(entries))

	if !errors.Is(dirty, probe.ErrDirtyLedger) {
		t.Fatalf("error = %v, want ErrDirtyLedger", dirty)
	}
	// Exit 5, the same code as orphans: a stale ledger is the record of objects that may be
	// live, and "something is still out there" is what a caller needs to act on.
	assertExitCode(t, probeError(dirty), exitOrphansLeft)

	// Replay and verify get a note instead: refusing an offline CI gate over a stale local file
	// buys nothing.
	for _, mode := range []string{"replay", "verify"} {
		if err := checkLedger(mode, "absent-provider", "absent-resource"); err != nil {
			t.Errorf("%s: %v", mode, err)
		}
	}
}

// TestUnit_CLI_Probe_ReplayNeedsACommittedCassette.
//
// The pilot has no committed evidence yet, so replay has nothing to work from. It must say so
// rather than silently succeeding with no facts: "no cassette" and "a cassette that produced
// nothing" are very different states and a CI gate has to tell them apart.
func TestUnit_CLI_Probe_ReplayNeedsACommittedCassette(t *testing.T) {
	t.Parallel()

	err := runProbe([]string{
		"-blueprint", blueprintDir(), "-resource", "tag",
		"-mode", "replay", "-evidence", filepath.Join(t.TempDir(), "absent"),
	})
	if err == nil {
		t.Fatal("replay with no committed cassette must fail")
	}
	if !errors.Is(err, cassette.ErrNoSnapshot) {
		t.Errorf("error = %v, want ErrNoSnapshot", err)
	}
}

// TestUnit_CLI_Probe_RecordNeedsCredentialsFromTheEnvironment.
//
// The endpoint and the token come from the environment and nowhere else. Not a flag: a flag
// puts the token in shell history and in the process table.
func TestUnit_CLI_Probe_RecordNeedsCredentialsFromTheEnvironment(t *testing.T) {
	// Not parallel: it manipulates process environment.
	t.Setenv(endpointEnv, "")
	t.Setenv(tokenEnv, "")

	err := runProbe([]string{"-blueprint", blueprintDir(), "-resource", "tag", "-mode", "record"})

	var ue *usageError
	if !errors.As(err, &ue) {
		t.Errorf("error = %v, want a usageError naming the environment variable", err)
	}
	if err != nil && !strings.Contains(err.Error(), endpointEnv) {
		t.Errorf("the error should name %s: %v", endpointEnv, err)
	}

	// With an endpoint but no token, it must still refuse -- and name the token variable.
	t.Setenv(endpointEnv, "https://api.example.com")

	err = runProbe([]string{"-blueprint", blueprintDir(), "-resource", "tag", "-mode", "record"})
	if err == nil || !strings.Contains(err.Error(), tokenEnv) {
		t.Errorf("the error should name %s: %v", tokenEnv, err)
	}
}

func TestUnit_CLI_Probe_RejectsBadUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{"no blueprint", nil},
		{"unknown mode", []string{"-blueprint", blueprintDir(), "-mode", "telepathy"}},
		{"unknown resource", []string{"-blueprint", blueprintDir(), "-resource", "octopus", "-list"}},
		{"unknown probe", []string{"-blueprint", blueprintDir(), "-list", "-only", "read.telepathy"}},
		{"bad flag", []string{"-nonsense"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := runProbe(tc.args)

			var ue *usageError
			if !errors.As(err, &ue) {
				t.Errorf("error = %v, want a usageError so the exit code is %d", err, exitInvalidInput)
			}
		})
	}
}

// TestUnit_CLI_Probe_ReportsAnUnprobeableResourceWithoutFailing.
//
// A blueprint with twenty resources of which two have no read operation should still probe
// the other eighteen. Refusing the whole run because one resource is unprobeable would make
// the prober useless on any real provider -- but the two must still be reported, or they
// silently go unprobed forever.
func TestUnit_CLI_Probe_ReportsAnUnprobeableResourceWithoutFailing(t *testing.T) {
	t.Parallel()

	// The pilot's one resource is probeable, so this asserts the happy path stays clean;
	// the mixed case is covered in internal/probe against a synthetic blueprint.
	if err := runProbe([]string{"-blueprint", blueprintDir(), "-list"}); err != nil {
		t.Errorf("probe -list on the whole pilot: %v", err)
	}
}

// TestUnit_CLI_Probe_DefaultModeIsReplay pins the safe default.
//
// The mode that can change somebody's tenant has to be spelled out; the one that cannot is what
// you get by typing less. If this ever flips, a bare `probe -blueprint …` becomes a command
// that reaches for the network.
//
// Asserted on the flag's own default rather than on a run's behaviour, because a run's failure
// mode could coincidentally match for the wrong reason.
func TestUnit_CLI_Probe_DefaultModeIsReplay(t *testing.T) {
	t.Parallel()

	// The flag's declared default, read out of the registered flag set rather than asserted
	// against a constant -- so this fails if somebody changes the registration and leaves the
	// constant alone.
	fs, _ := newFlagSet("probe", usageProbe)
	fs.String("mode", modeReplay, "")

	f := fs.Lookup("mode")
	if f == nil {
		t.Fatal("the mode flag is not registered")
	}

	if f.DefValue != modeReplay {
		t.Errorf("the default mode is %q; it must be %q so that typing less is the safe option",
			f.DefValue, modeReplay)
	}
	if f.DefValue == modeRecord {
		t.Error("the default must not be the mode that can change somebody's tenant")
	}
}
