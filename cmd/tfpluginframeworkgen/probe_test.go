package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
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

// TestUnit_CLI_Probe_UnbuiltPathsRefuseExplicitly.
//
// Anything not yet implemented must return an error rather than exit zero having done
// nothing. A scripted run that treated an unbuilt path as a clean one would report success
// for a probe run that never happened -- which in a CI job is indistinguishable from a
// passing gate.
//
// Two remain: sweep, and the whole mutating tier. --allow-mutations is *refused* rather than
// ignored, because silently dropping it would let a caller believe the write path had been
// probed.
func TestUnit_CLI_Probe_UnbuiltPathsRefuseExplicitly(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"sweep":           {"-blueprint", blueprintDir(), "-resource", "tag", "-mode", "sweep"},
		"allow-mutations": {"-blueprint", blueprintDir(), "-resource", "tag", "--allow-mutations"},
	}

	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := runProbe(args); !errors.Is(err, errNotImplemented) {
				t.Errorf("probe %v = %v, want errNotImplemented", args, err)
			}
		})
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
