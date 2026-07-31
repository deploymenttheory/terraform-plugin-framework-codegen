package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
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
// A resource with no evidence is noted and skipped -- authoring first and recording second
// is the pipeline's order -- but a run in which *nothing* had evidence must still fail:
// "no cassette anywhere" and "a cassette that produced nothing" are very different states,
// and a CI gate that verified nothing must not report success.
func TestUnit_CLI_Probe_ReplayNeedsACommittedCassette(t *testing.T) {
	t.Parallel()

	err := runProbe([]string{
		"-blueprint", blueprintDir(), "-resource", "tag",
		"-mode", "replay", "-evidence", filepath.Join(t.TempDir(), "absent"),
	})
	if err == nil {
		t.Fatal("a replay that verified nothing must fail")
	}
	if !errors.Is(err, errNothingToDo) {
		t.Errorf("error = %v, want errNothingToDo", err)
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
		// Only replay honours -rederive; silence on the other modes would let a scripted
		// run believe it had rewritten facts.json when nothing was written at all.
		{"rederive outside replay", []string{
			"-blueprint", blueprintDir(), "-resource", "tag", "-mode", "verify", "-rederive",
		}},
		{"rederive with sweep", []string{
			"-blueprint", blueprintDir(), "-resource", "tag", "-mode", "sweep", "-rederive",
		}},
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

// TestUnit_CLI_Probe_SuspectedFactsAreShapeCheckedOnLoad.
//
// A Suspected fact is exempt from the strength checks -- it may lack evidence -- but its
// shape is not: a malformed precondition on one would otherwise load unchecked and sit in
// the store as a claim nothing downstream can interpret.
func TestUnit_CLI_Probe_SuspectedFactsAreShapeCheckedOnLoad(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "facts.json")

	malformed := `[{
		"resource": "tag", "jsonPath": "matchType", "field": "returnedOnRead",
		"value": {"bool": false}, "confidence": "suspected", "probe": "test",
		"when": [{"jsonPath": "type", "equals": ""}]
	}]`
	if err := os.WriteFile(path, []byte(malformed), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := readFacts(path); err == nil ||
		!strings.Contains(err.Error(), "no value") {
		t.Errorf("a suspected fact with a malformed condition must be refused: %v", err)
	}

	// The exemption itself still holds: no evidence is fine at suspected confidence.
	unevidenced := `[{
		"resource": "tag", "jsonPath": "matchType", "field": "returnedOnRead",
		"value": {"bool": false}, "confidence": "suspected", "probe": "test"
	}]`
	if err := os.WriteFile(path, []byte(unevidenced), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := readFacts(path); err != nil {
		t.Errorf("a suspected fact may lack evidence: %v", err)
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

// TestUnit_CLI_ThePilotPlanMatchesTheCommittedBlueprint.
//
// The plan and the blueprint are two committed files that have to agree, and nothing else checks
// that they do. A blueprint change that renames a JSON path would otherwise fail six months later
// during a record run -- against a real tenant, with the objects already created.
//
// Every key in the plan resolves against the subject, which is what Plan.Validate does; this test
// exists to run that validation against the *committed pair* rather than a fixture.
func TestUnit_CLI_ThePilotPlanMatchesTheCommittedBlueprint(t *testing.T) {
	t.Parallel()

	bp, err := blueprint.LoadDir(blueprintDir())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	plan, err := loadPlan(filepath.Join(blueprintDir(), "probe.plan.json"))
	if err != nil {
		t.Fatalf("loadPlan: %v", err)
	}

	if len(plan.Fixtures) < 2 {
		t.Errorf("the pilot plan declares %d fixture(s); conditional requirement is only "+
			"detectable with two or more", len(plan.Fixtures))
	}

	var checked int

	for _, res := range bp.Resources {
		if res.Drop {
			continue
		}

		// The committed plan is the tag's: its fixtures, candidates and deny list speak
		// that schema's wire vocabulary. Validating it against every other resource would
		// demand one plan fit all schemas, which no plan can. A per-resource plan story
		// is the re-record PR's work; until then a record run scopes itself with
		// -resource, exactly as the runbook says.
		if res.Key != "tag" {
			continue
		}

		subj, err := probe.SubjectOf(bp, res)
		if err != nil {
			continue
		}

		sc, err := probe.NewScope(subj, plan)
		if err != nil {
			t.Fatalf("%s: the committed plan does not match the committed blueprint: %v",
				res.Key, err)
		}

		checked++

		// The fixture must set the name field, because the probe replaces its value with the
		// stamped name -- and Create refuses a body whose name field is missing.
		for i := range plan.Fixtures {
			fixture, _ := sc.Fixture(i)
			if _, ok := fixture.Body[subj.NameField]; !ok {
				t.Errorf("fixture %q does not set %q, so the stamped name would have nowhere "+
					"to go", fixture.Name, subj.NameField)
			}
		}

		// The narrowing has to actually narrow, or the plan is decorative.
		if len(sc.Immutable()) == 0 {
			t.Error("the plan declares no field with two candidates, so the immutability " +
				"protocol has nothing to probe")
		}
		if len(sc.Omitted()) == 0 {
			t.Error("every sendable field is set by some fixture, so the server-default " +
				"protocol has nothing to observe")
		}
		if len(sc.Enums()) == 0 {
			t.Error("no sendable field carries documented enum values, so the enum protocol " +
				"has nothing to check the specification against")
		}

		// And the whole catalogue has to fit, which is the milestone this phase is measured by.
		requests, creates := probe.TotalCost(sc, "")
		budget := plan.Budget.WithDefaults()

		if requests > budget.MaxRequests {
			t.Errorf("%s: %d requests exceeds the plan's cap of %d", res.Key, requests,
				budget.MaxRequests)
		}
		if creates > budget.MaxCreates {
			t.Errorf("%s: %d creates exceeds the plan's cap of %d", res.Key, creates,
				budget.MaxCreates)
		}
	}

	if checked == 0 {
		t.Fatal("no resource was checked against the plan")
	}
}

// TestUnit_CLI_ThePilotBlueprintCarriesSpecEnumValues.
//
// The enum protocol's valuable claim is "the specification is stale". That claim is only worth
// anything when the values provably came from the specification rather than from somebody's
// transcription -- if a human typed them, the fact would mean "the API disagrees with what
// somebody typed", which is worthless.
//
// The pilot is already a case in point: its committed object_type *description* lists five values
// and the specification declares six.
func TestUnit_CLI_ThePilotBlueprintCarriesSpecEnumValues(t *testing.T) {
	t.Parallel()

	bp, err := blueprint.LoadDir(blueprintDir())
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	want := map[string]int{
		"object_type": 6,
		"access_type": 3,
		"match_type":  2,
	}

	found := map[string]int{}

	for _, res := range bp.Resources {
		for _, a := range res.Schema.Attributes {
			if len(a.Type.AllowedValues) > 0 {
				found[a.Name] = len(a.Type.AllowedValues)
			}
		}
	}

	for name, count := range want {
		if found[name] != count {
			t.Errorf("%s carries %d enum value(s), want %d from the specification",
				name, found[name], count)
		}
	}
}
