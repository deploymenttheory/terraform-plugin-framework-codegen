package main

import (
	"errors"
	"strings"
	"testing"
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

// TestUnit_CLI_Probe_UnbuiltModesRefuseExplicitly.
//
// A mode whose implementation has not landed must return an error, not exit zero having
// done nothing. A scripted run that treated an unbuilt mode as a clean one would report
// success for a probe run that never happened -- which in a CI job is indistinguishable
// from a passing gate.
func TestUnit_CLI_Probe_UnbuiltModesRefuseExplicitly(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"record", "replay", "verify", "sweep"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			err := runProbe([]string{"-blueprint", blueprintDir(), "-resource", "tag", "-mode", mode})
			if !errors.Is(err, errNotImplemented) {
				t.Errorf("probe -mode %s = %v, want errNotImplemented", mode, err)
			}
		})
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
// The mode that can change somebody's tenant has to be spelled out; the one that cannot is
// what you get by typing less. If this ever flips, a bare `probe -blueprint …` becomes a
// command that reaches for the network.
func TestUnit_CLI_Probe_DefaultModeIsReplay(t *testing.T) {
	t.Parallel()

	err := runProbe([]string{"-blueprint", blueprintDir(), "-resource", "tag"})
	if err == nil {
		t.Fatal("expected the unbuilt-mode error")
	}
	if !strings.Contains(err.Error(), modeReplay) {
		t.Errorf("the default mode should be %q, got error %v", modeReplay, err)
	}
}
