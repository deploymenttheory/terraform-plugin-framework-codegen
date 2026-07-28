package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/version"
)

// The environment is the only place a credential or an endpoint comes from.
//
// Not a flag: a flag puts the token in shell history and in the process table. Not a profile
// file: that gets committed. See probe.Profile's doc comment.
const (
	tokenEnv    = "TFPFGEN_PROBE_TOKEN"
	endpointEnv = "TFPFGEN_PROBE_ENDPOINT"
)

// defaultEvidenceRoot is where committed cassettes live, matching the repository layout.
const defaultEvidenceRoot = "probe-evidence"

// replayBaseURL is the unresolvable host replay requests are addressed to. The recorded base path
// is appended, so a cassette made against an endpoint with a prefix replays against the same
// paths it holds.
const replayBaseURL = "https://replay.invalid"

// basePathOf extracts the path prefix from an endpoint, e.g. "/v7" from
// "https://api.example.com/v7".
func basePathOf(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(u.Path, "/")
}

const usageProbe = "probe [-mode record|replay|verify|sweep] -blueprint DIR " +
	"[-resource KEY] [-only PROBE] [-list] [-plan FILE] [--allow-mutations] " +
	"[-profile FILE] [-force]"

// Probe modes. The default is replay, deliberately: the safe mode is what you get by
// typing less, and the mode that can change somebody's tenant has to be spelled out.
const (
	modeReplay = "replay"
	modeRecord = "record"
	modeVerify = "verify"
	modeSweep  = "sweep"
)

// runProbe is a two-line funnel over probeMain, so no return path escapes the exit-code mapping.
//
// The mapping cannot live in main.go's errors.As walk: errors.As returns the *first* match in a
// preorder walk, and first is not most serious. These conditions genuinely co-occur -- a run can
// exceed its budget, sweep, and still leave an orphan -- and which number CI sees must not depend
// on the order the errors were joined in.
func runProbe(args []string) error {
	return probeError(probeMain(args))
}

// probeExitPrecedence is the order in which co-occurring failures are reported.
//
// 7 > 5 > 3 > 4 > 6 > 1, and each step is an argument about what a human needs to see first:
//
//   - 7, a redaction failure, outranks everything: nothing was written, and a secret nearly was.
//   - 5, orphans, outranks the rest because objects are still live in somebody's tenant.
//   - 3, a refused gate, before 4 because a refusal means nothing ran at all.
//   - 4, a budget cap, before 6 because it explains why a replay might not match.
//   - 6, a replay mismatch, last of the specific codes.
var probeExitPrecedence = []struct {
	err  error
	code int
}{
	{cassette.ErrSecretFound, exitRedactionFailed},
	{probe.ErrRedaction, exitRedactionFailed},
	{probe.ErrOrphans, exitOrphansLeft},
	{probe.ErrLedger, exitOrphansLeft},
	// A stale ledger is not itself an orphan, but it is the record of objects that may be
	// live -- so it gets the code that means "something is still out there".
	{probe.ErrDirtyLedger, exitOrphansLeft},
	{probe.ErrRefused, exitGatingRefused},
	{probe.ErrBudget, exitBudgetExceeded},
	{probe.ErrDeleteFailures, exitBudgetExceeded},
	{probe.ErrReplayMismatch, exitReplayMismatch},
}

// probeError maps a probe failure onto a documented exit code.
func probeError(err error) error {
	if err == nil {
		return nil
	}

	// Help is not a failure, and a usage error already carries its own code.
	var coded exitCoder
	if errors.Is(err, flag.ErrHelp) || errors.As(err, &coded) {
		return err
	}

	for _, entry := range probeExitPrecedence {
		if errors.Is(err, entry.err) {
			return &codedError{err: err, code: entry.code}
		}
	}

	return err
}

// codedError attaches an exit code without losing the original error.
type codedError struct {
	err  error
	code int
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }
func (e *codedError) ExitCode() int { return e.code }

func probeMain(args []string) error {
	fs, _ := newFlagSet("probe", usageProbe)

	var (
		mode          = fs.String("mode", modeReplay, "record, replay, verify or sweep")
		blueprintPath = fs.String("blueprint", "", "blueprint file or directory (required)")
		resource      = fs.String("resource", "", "probe one resource, by blueprint key")
		only          = fs.String("only", "", "run one probe, by name")
		list          = fs.Bool("list", false, "print the probe catalogue and exit")
		evidenceRoot  = fs.String("evidence", defaultEvidenceRoot, "root of the committed probe evidence")
		providerName  = fs.String("provider", "", "provider name for the evidence path; defaults to the blueprint's")

		allowMutations = fs.Bool("allow-mutations", false,
			"permit probes that create, update and delete; requires -mode record and a sandbox profile")
		profilePath = fs.String("profile", "",
			"sandbox profile for a mutating run; defaults to "+defaultProfileDir+"/PROVIDER.json")
		planPath = fs.String("plan", "", "probe plan: fixtures and candidates a probe cannot discover")
		force    = fs.Bool("force", false, "record over evidence that is already committed")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	if *blueprintPath == "" {
		return usagef("-blueprint is required")
	}

	switch *mode {
	case modeRecord, modeReplay, modeVerify, modeSweep:
	default:
		return usagef("-mode %q is not one of record, replay, verify or sweep", *mode)
	}

	bp, err := blueprint.LoadDir(*blueprintPath)
	if err != nil {
		return err
	}

	if *resource != "" {
		if err := keepOnlyResource(&bp, *resource); err != nil {
			return err
		}
	}

	if *providerName == "" {
		*providerName = bp.Provider.Name
	}

	subjects, notes, err := subjectsOf(bp)
	if err != nil {
		return err
	}

	// Printed before anything else: a resource the prober cannot work on at all is
	// something the operator needs to know regardless of what the run then does.
	for _, n := range notes {
		fmt.Fprintf(os.Stderr, "  %s\n", n)
	}

	if len(subjects) == 0 {
		return fmt.Errorf("%w: no resource in %s can be probed", errNothingToDo, *blueprintPath)
	}

	if *only != "" {
		if _, ok := probe.Lookup(*only); !ok {
			return usagef("-only %q is not a registered probe; run `probe -list` to see them", *only)
		}
	}

	// Refused here rather than inside the gate, because the gate only runs on the record path
	// and this combination would otherwise be silently ignored -- letting a scripted run
	// believe it had probed the write path when it replayed a cassette instead.
	if *allowMutations && *mode != modeRecord {
		return fmt.Errorf("%w: --allow-mutations needs -mode record, and this is %s; replay and "+
			"verify derive facts from a committed transcript and must never write",
			probe.ErrRefused, *mode)
	}

	plan, err := loadPlan(*planPath)
	if err != nil {
		return err
	}

	if *list {
		return listProbes(subjects, plan, *only)
	}

	opts := probeRun{
		mode:         *mode,
		subjects:     subjects,
		only:         *only,
		evidenceRoot: *evidenceRoot,
		provider:     *providerName,
		apiVersion:   bp.Source.SpecVersion,
		plan:         plan,
		profilePath:  profilePathFor(*profilePath, *providerName),
		allowMutate:  *allowMutations,
		force:        *force,
	}

	if *mode == modeSweep {
		return sweepEverything(opts)
	}

	return runProbeMode(opts)
}

// probeRun is one invocation's settings, gathered so the mode functions take one argument
// instead of nine positional strings nobody can read at a call site.
type probeRun struct {
	mode         string
	subjects     []probe.Subject
	only         string
	evidenceRoot string
	provider     string
	apiVersion   string

	plan        probe.Plan
	profilePath string
	allowMutate bool
	force       bool
}

// ledgerRoot is where per-run ledgers live. Gitignored: a ledger records live objects in
// somebody's tenant and is per-run rather than per-snapshot.
const ledgerRoot = ".tfpluginframeworkgen/probe"

// loadPlan reads a probe plan, or returns the zero plan when none is named.
//
// The zero plan is usable for a read-only run and is refused by the gate for a mutating one,
// which is the right split: -list reports the unnarrowed worst case, and a mutating run needs a
// fixture.
// loadPlanIfPresent is loadPlan for a path that may legitimately not exist.
//
// A read-only recording carries no plan, and a snapshot from before plans were frozen carries none
// either. Neither is an error: the run simply has no fixtures, and the mutating tier is reported
// skipped. An explicit -plan pointing at a missing file *is* an error, which is why the strict
// version stays.
func loadPlanIfPresent(path string) (probe.Plan, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return probe.Plan{}, nil
	}

	return loadPlan(path)
}

func loadPlan(path string) (probe.Plan, error) {
	var p probe.Plan

	if path == "" {
		return p, nil
	}

	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return p, fmt.Errorf("reading %s: %w", path, err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	// Strict: a mistyped key in a plan silently omits the fixture or candidate it meant to
	// supply, and a missing fixture value looks exactly like a server default.
	dec.DisallowUnknownFields()

	if err := dec.Decode(&p); err != nil {
		return probe.Plan{}, usagef("%s is not a usable probe plan: %v", path, err)
	}

	return p, nil
}

// checkLedger reports what a stale ledger means for this mode.
//
// Refused in record: a run against a tenant already holding your own orphans makes
// maxExistingObjects measure your own rubbish, and creating more on top of objects you have
// already failed to clean up is the wrong direction. A note in replay and verify, because
// refusing an offline CI gate over a stale local file buys nothing. Proceeds in sweep, which is
// the whole point of it.
func checkLedger(mode, provider, resource string) error {
	path := probe.LedgerPath(ledgerRoot, provider, resource)

	entries, err := probe.ReadLedger(path)
	if err != nil {
		return err
	}

	outstanding := probe.Unresolved(entries)
	if len(outstanding) == 0 {
		return nil
	}

	if mode != modeRecord {
		fmt.Fprintf(os.Stderr,
			"  note  %d object(s) from a previous run are unaccounted for in %s; "+
				"run `probe -mode sweep -resource %s` when you next have credentials\n",
			len(outstanding), path, resource)

		return nil
	}

	return probe.DirtyError(path, resource, outstanding)
}

// runProbeMode records, replays or verifies.
func runProbeMode(opts probeRun) error {
	failures := 0

	for _, subj := range opts.subjects {
		root := filepath.Join(opts.evidenceRoot, opts.provider, subj.Resource)

		if err := checkLedger(opts.mode, opts.provider, subj.Resource); err != nil {
			return err
		}

		switch opts.mode {
		case modeRecord:
			if err := recordProbe(opts, subj, root); err != nil {
				return err
			}

		case modeReplay, modeVerify:
			if err := replayProbe(opts.mode, subj, opts.only, root); err != nil {
				if errors.Is(err, probe.ErrReplayMismatch) {
					// Reported and counted rather than returned, so a multi-resource run
					// reports every mismatch instead of one per invocation.
					fmt.Fprintf(os.Stderr, "::error::%s: %v\n", subj.Resource, err)
					failures++
					continue
				}
				return err
			}
		}
	}

	if failures > 0 {
		return fmt.Errorf("%w: %d resource(s) did not reproduce their committed facts",
			probe.ErrReplayMismatch, failures)
	}

	return nil
}

// recordProbe runs live and writes a snapshot.
func recordProbe(opts probeRun, subj probe.Subject, root string) error {
	endpoint := os.Getenv(endpointEnv)
	if endpoint == "" {
		return usagef("%s must be set for -mode record: it names the API to probe", endpointEnv)
	}

	// The token comes from the environment and nowhere else -- not a flag, not a profile,
	// not anything written to disk. A flag would put it in shell history and in the
	// process table.
	token := os.Getenv(tokenEnv)
	if token == "" {
		return usagef("%s must be set for -mode record", tokenEnv)
	}

	ctx, cancel := probe.DeadlineFor(context.Background(), opts.plan.Budget)
	defer cancel()

	// The gate, and the ledger it makes safe to open. Both only when mutations were asked for:
	// a read-only run needs no profile, no assertions and no ledger, and demanding one would
	// make the safe mode the inconvenient one.
	//
	// Before the redactor, so an operator with an unusable profile reads about the profile
	// rather than about a detail of how recordings are scrubbed.
	var (
		grant      *probe.Grant
		assertions []string
		ledger     *probe.Ledger
	)

	if opts.allowMutate {
		var err error

		grant, assertions, err = authorizeMutations(ctx, opts, subj, endpoint, token, root)
		if err != nil {
			return err
		}

		ledger, err = probe.OpenLedger(probe.LedgerPath(ledgerRoot, opts.provider, subj.Resource))
		if err != nil {
			return err
		}
		defer func() { _ = ledger.Close() }()
	}

	redactor, err := cassette.NewRedactor(map[string]string{"bearer": token}, nil)
	if err != nil {
		return err
	}

	runOpts := probe.RunOptions{
		Mode:     probe.ModeRecord,
		Subject:  subj,
		Plan:     opts.plan,
		Only:     opts.only,
		BaseURL:  endpoint,
		Token:    token,
		Redactor: redactor,
		Secrets:  map[string]string{"bearer": token},

		AllowMutations: opts.allowMutate,

		Grant:            grant,
		Ledger:           ledger,
		AssertionsPassed: assertions,
	}

	result, runErr := probe.Run(ctx, runOpts)

	// Everything below happens whatever Run returned. A run that left an orphan or panicked
	// owes the operator its report, its transcript and its orphan table more than a clean run
	// does, and returning early here is what would deny them.
	printProbeReport(subj.Resource, result.Report)
	reportOrphans(subj.Resource, result.Report)

	if runErr != nil && !worthWriting(runErr) {
		return runErr
	}

	meta := probe.RecordingMetadata(opts.provider, subj, result.Report.Profile.Host, version.Version)
	meta.BasePath = basePathOf(endpoint)
	// The API version the blueprint was inferred from, so the snapshot directory names the
	// specification the facts were recorded against rather than reading "unknown". A cassette
	// whose directory does not say which API version produced it is far less useful a year later.
	meta.APIVersion = opts.apiVersion
	// The prefix every created object carried. Without it a mutating replay cannot reproduce a
	// single create: ReplayTransport matches request bodies, and the name is in the body.
	meta.NamePrefix = runOpts.Grant.NamePrefix()

	snap, err := cassette.Write(root, meta, result.Interactions, map[string]string{"bearer": token}, time.Now())
	if err != nil {
		return err
	}

	if err := writeFacts(snap.FactsPath(), result.Report.Facts); err != nil {
		return err
	}
	if err := writeJSONFile(snap.ReportPath(), result.Report); err != nil {
		return err
	}

	// Frozen beside the transcript. A write-tier recording's request bodies *are* the plan's
	// fixtures, so a replay that read the plan from the working tree would fail on any later edit
	// with a body mismatch indistinguishable from a probe regression.
	if len(opts.plan.Fixtures) > 0 {
		if err := writeJSONFile(snap.PlanPath(), opts.plan); err != nil {
			return err
		}
	}

	log.Printf("wrote %s", snap.Dir)

	// Returned last, so the exit code reflects what went wrong while the evidence of it is
	// already on disk.
	return runErr
}

// worthWriting reports whether a snapshot should still be written after a failed run.
//
// A redaction failure is the one case where nothing may be written: the whole point is that the
// caller cannot proceed to commit something containing a secret. Everything else -- orphans, a
// budget cap, a panicking probe -- produced real observations, and a transcript of the run that
// went wrong is more useful than none.
func worthWriting(err error) bool {
	return !errors.Is(err, cassette.ErrSecretFound) && !errors.Is(err, probe.ErrRedaction)
}

// authorizeMutations runs the gate.
//
// The gate's tenant reads go through their own unrecorded transport, and that is not an
// oversight. Replay has no gate, so a cassette holding requests the replayed run will never
// issue is a cassette that cannot reproduce itself -- the same silent, total failure class as a
// base path that does not round-trip.
func authorizeMutations(
	ctx context.Context,
	opts probeRun,
	subj probe.Subject,
	endpoint, token, root string,
) (*probe.Grant, []string, error) {
	profile, err := loadProfile(opts.profilePath)
	if err != nil {
		return nil, nil, err
	}

	// The profile says where; the environment says with what. A profile pointing somewhere
	// other than TFPFGEN_PROBE_ENDPOINT is a mistake worth refusing rather than resolving,
	// because whichever one loses would be a surprise.
	if profile.Endpoint != "" && profile.Endpoint != endpoint {
		return nil, nil, fmt.Errorf("%w: the profile's endpoint %q is not %s (%q); one of the "+
			"two is pointed somewhere it was not meant to be",
			probe.ErrRefused, profile.Endpoint, endpointEnv, endpoint)
	}

	read, err := probe.NewReadSession(probe.SessionConfig{
		Transport:          probe.UnrecordedTransport(),
		BaseURL:            endpoint,
		Token:              token,
		CollectionTemplate: subj.CollectionTemplate,
		ItemTemplate:       subj.ItemTemplate,
		// Only the gate's own reads come through here, and there are at most two.
		Budget: probe.Budget{MaxRequests: 4, MaxCreates: 0},
	})
	if err != nil {
		return nil, nil, err
	}

	return probe.Authorize(ctx, read, profile, probe.GateOptions{
		Mode:                     probe.ModeRecord,
		AllowMutations:           opts.allowMutate,
		Subject:                  subj,
		Plan:                     opts.plan,
		EquivalentSnapshotExists: equivalentSnapshotExists(root, opts.plan),
		Force:                    opts.force,
	}, probe.OSEnviron{})
}

// equivalentSnapshotExists reports whether the newest committed snapshot was recorded with an
// identical plan.
//
// Compared by canonical JSON rather than by a stored hash: the plan is already frozen into the
// snapshot, so the comparison needs no second representation that could disagree with it.
//
// Any failure to read or compare answers false. Being unable to tell must not block a recording --
// the consequence of a wrong false is one extra snapshot, and of a wrong true is a run an operator
// cannot make at all.
func equivalentSnapshotExists(root string, plan probe.Plan) bool {
	existing, err := cassette.Latest(root)
	if err != nil {
		return false
	}

	frozen, err := loadPlanIfPresent(existing.PlanPath())
	if err != nil {
		return false
	}

	was, err := json.Marshal(frozen)
	if err != nil {
		return false
	}

	now, err := json.Marshal(plan)
	if err != nil {
		return false
	}

	return bytes.Equal(was, now)
}

// sweepEverything removes what previous runs left behind.
func sweepEverything(opts probeRun) error {
	endpoint := os.Getenv(endpointEnv)
	if endpoint == "" {
		return usagef("%s must be set for -mode sweep: it names the API to clean up in", endpointEnv)
	}

	token := os.Getenv(tokenEnv)
	if token == "" {
		return usagef("%s must be set for -mode sweep", tokenEnv)
	}

	profile, err := loadProfile(opts.profilePath)
	if err != nil {
		return err
	}

	grant, err := probe.AuthorizeSweep(profile, probe.GateOptions{Mode: probe.ModeSweep},
		probe.OSEnviron{})
	if err != nil {
		return err
	}

	var failed error

	for _, subj := range opts.subjects {
		if can, why := subj.CanMutate(); !can {
			// Nothing could have been created here, so there is nothing to sweep. Said out
			// loud rather than skipped silently, because an operator sweeping after a failure
			// needs to know which resources were even considered.
			fmt.Fprintf(os.Stderr, "  %s: nothing to sweep (%s)\n", subj.Resource, why)
			continue
		}

		ledger, err := probe.OpenLedger(probe.LedgerPath(ledgerRoot, opts.provider, subj.Resource))
		if err != nil {
			return err
		}

		ctx, cancel := probe.SweepContext(context.Background(), opts.plan.Budget.MaxSweepSeconds)

		summary, sweepErr := probe.RunSweep(ctx, probe.SweepRunOptions{
			Grant:      grant,
			Subject:    subj,
			BaseURL:    endpoint,
			Token:      token,
			Ledger:     ledger,
			MaxSeconds: opts.plan.Budget.MaxSweepSeconds,
		})

		cancel()
		_ = ledger.Close()

		printSweepSummary(subj.Resource, summary)

		if sweepErr != nil {
			// Every resource is attempted: stopping at the first would leave the rest of the
			// tenant holding objects for no reason but ordering.
			failed = errors.Join(failed, fmt.Errorf("%s: %w", subj.Resource, sweepErr))
		}
	}

	return failed
}

// printSweepSummary says what the sweep did, including when it did nothing.
func printSweepSummary(resource string, summary probe.SweepSummary) {
	fmt.Fprintf(os.Stderr, "\n%s: %d removed by ledger, %d by name prefix",
		resource, summary.LedgerDeletes, summary.PrefixDeletes)

	if !summary.Complete {
		fmt.Fprint(os.Stderr, " (INCOMPLETE)")
	}
	fmt.Fprintln(os.Stderr)

	for _, n := range summary.Notes {
		fmt.Fprintf(os.Stderr, "  note  %s\n", n)
	}

	printOrphanTable(summary.Orphans)
}

// reportOrphans prints the orphan table after a run.
func reportOrphans(resource string, report probe.Report) {
	if len(report.Orphans) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "\n%s: %d OBJECT(S) LEFT BEHIND\n", resource, len(report.Orphans))
	printOrphanTable(report.Orphans)
}

// printOrphanTable prints one row per object a human has to remove.
//
// To stderr and to $GITHUB_STEP_SUMMARY where there is one, because the person who has to clean
// up should not have to find this in a fold-out log. The curl lines carry
// "$TFPFGEN_PROBE_TOKEN" and never a value.
func printOrphanTable(orphans []probe.Orphan) {
	if len(orphans) == 0 {
		return
	}

	w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  ID\tPROBE\tPATH")

	for _, o := range orphans {
		fmt.Fprintf(w, "  %s\t%s\t%s\n", or(o.ID, "(unknown)"), o.Probe, o.Path)
	}
	_ = w.Flush()

	fmt.Fprintln(os.Stderr, "\n  remove them with:")
	for _, o := range orphans {
		if o.Curl != "" {
			fmt.Fprintf(os.Stderr, "    %s\n", o.Curl)
		}
	}

	appendStepSummary(orphans)
}

// appendStepSummary puts the orphan table where a CI reviewer will see it.
func appendStepSummary(orphans []probe.Orphan) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}

	var b strings.Builder

	fmt.Fprintf(&b, "\n### ⚠️ %d probe object(s) left behind\n\n", len(orphans))
	b.WriteString("| id | probe | remove with |\n|---|---|---|\n")

	for _, o := range orphans {
		fmt.Fprintf(&b, "| `%s` | %s | `%s` |\n", or(o.ID, "unknown"), o.Probe, o.Curl)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // CI-supplied path
	if err != nil {
		// Best effort by design: failing a run because a CI convenience file was unwritable
		// would replace a useful failure with a confusing one.
		return
	}
	defer func() { _ = f.Close() }()

	_, _ = f.WriteString(b.String())
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// replayProbe re-derives facts from a committed snapshot with no network.
func replayProbe(mode string, subj probe.Subject, only, root string) error {
	snap, err := cassette.Latest(root)
	if err != nil {
		return err
	}

	// The checksum first: replaying a tampered cassette would produce facts that look
	// derived from evidence which no longer matches what was committed.
	if err := snap.Verify(); err != nil {
		return err
	}

	interactions, err := snap.LoadInteractions()
	if err != nil {
		return err
	}

	meta, err := snap.LoadMetadata()
	if err != nil {
		return err
	}

	// The plan the *recording* was made with, never the working tree's. See Snapshot.PlanPath.
	plan, err := loadPlanIfPresent(snap.PlanPath())
	if err != nil {
		return err
	}

	grant := grantForReplay(plan, meta)

	result, runErr := probe.Run(context.Background(), probe.RunOptions{
		Mode:    probe.ModeReplay,
		Subject: subj,
		Plan:    plan,
		Grant:   grant,
		Ledger:  probe.MemoryLedger(),
		Only:    only,
		// The recorded prefix, reproduced. A cassette stores full request paths, so replaying
		// a recording made against an endpoint with a prefix needs that prefix back.
		BaseURL:      replayBaseURL + meta.BasePath,
		Interactions: interactions,
	})

	// Printed before the error is returned, for the same reason recordProbe does it: a replay that
	// leaves something unresolved is precisely the run whose report a reader needs, and returning
	// first means they are told a count and nothing else.
	printProbeReport(subj.Resource, result.Report)
	reportOrphans(subj.Resource, result.Report)

	if runErr != nil {
		return runErr
	}

	if mode != modeVerify {
		return nil
	}

	// Verify is the purity test: the committed facts must be exactly what replaying the
	// committed transcript produces. A difference means derivation depends on something
	// outside the transcript, and every fact in the store is then unreproducible.
	committed, err := readFacts(snap.FactsPath())
	if err != nil {
		return err
	}

	if err := probe.VerifyFacts(result.Report.Facts, committed); err != nil {
		return err
	}

	log.Printf("✅ %s: %d fact(s) reproduced from the committed cassette", subj.Resource, len(committed))

	return nil
}

// grantForReplay authorizes the mutating tier during a replay, and only when the recording had one.
//
// A read-only recording carries no plan and no name prefix, so no grant is issued and the mutating
// probes are reported skipped -- which is what they were. A mutating recording gets a replay grant
// carrying the prefix the run stamped, because ReplayTransport matches request bodies and the name
// is in the body.
func grantForReplay(plan probe.Plan, meta cassette.Metadata) *probe.Grant {
	if len(plan.Fixtures) == 0 || meta.NamePrefix == "" {
		return nil
	}

	return probe.ReplayGrant(meta.NamePrefix)
}

// printProbeReport writes the run summary to stderr.
//
// Stderr with fmt rather than the log package, following runIngest's printNotes: -q silences
// progress, and what a probe could not establish is not progress chatter.
func printProbeReport(resource string, report probe.Report) {
	fmt.Fprintf(os.Stderr, "\n%s: %s\n", resource, report.Summary())

	for _, p := range report.Probes {
		if p.Status == "ok" {
			continue
		}
		fmt.Fprintf(os.Stderr, "  %-24s %-10s %s\n", p.Name, p.Status, p.Reason)
	}

	for _, f := range report.Facts {
		fmt.Fprintf(os.Stderr, "  fact  %s\n", f)
	}
	for _, n := range report.Notes {
		fmt.Fprintf(os.Stderr, "  note  %s\n", n)
	}

	fmt.Fprintln(os.Stderr)
}

func writeFacts(path string, facts []probe.Fact) error {
	return writeJSONFile(path, facts)
}

func readFacts(path string) ([]probe.Fact, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var out []probe.Fact
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	// Validated on load, because a committed facts document is hand-editable and a fact with
	// no evidence would otherwise flow into merge and change a schema on the strength of
	// nothing.
	for _, f := range out {
		if f.Confidence == probe.Suspected {
			continue
		}
		if err := f.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}

	return out, nil
}

// writeJSONFile writes canonical JSON: two-space indent, no HTML escaping, trailing newline.
// Matching blueprint.Marshal, because these files are committed and diffed.
func writeJSONFile(path string, v any) error {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// keepOnlyResource narrows the blueprint to one resource.
//
// A separate flag from -only, which selects a probe. Both exist because the two axes are
// independent: probing one resource with the whole catalogue and probing every resource
// with one protocol are both things an operator wants, and conflating them into one flag
// would make the common case ambiguous.
//
// Naming a single resource *file* on -blueprint does not work, and deliberately: a
// resource file carries no provider block, so LoadDir refuses it rather than silently
// probing against a half-loaded document.
func keepOnlyResource(bp *blueprint.Blueprint, key string) error {
	for _, r := range bp.Resources {
		if r.Key == key {
			bp.Resources = []blueprint.Resource{r}
			return nil
		}
	}

	return usagef("-resource %q matches no resource key in the blueprint", key)
}

// subjectsOf flattens every probeable resource, collecting a note for each one that
// cannot be probed rather than failing the run.
//
// A blueprint with twenty resources of which two have no read operation should still
// probe the other eighteen. Refusing the whole run because one resource is unprobeable
// would make the prober useless on any real provider.
func subjectsOf(bp blueprint.Blueprint) ([]probe.Subject, []string, error) {
	var (
		subjects []probe.Subject
		notes    []string
	)

	for _, res := range bp.Resources {
		if res.Drop {
			continue
		}

		subj, err := probe.SubjectOf(bp, res)
		if err != nil {
			if errors.Is(err, probe.ErrNotProbeable) {
				notes = append(notes, err.Error())
				continue
			}
			return nil, nil, err
		}

		subjects = append(subjects, subj)
	}

	return subjects, notes, nil
}

// listProbes prints the catalogue with its worst-case costs.
//
// This works with no credentials, no cassettes and no network, which is what makes it
// the first useful thing the prober does: the costs and the mutating/read split are
// reviewable against a real blueprint before anybody points it at a tenant.
func listProbes(subjects []probe.Subject, plan probe.Plan, only string) error {
	for _, subj := range subjects {
		sc, err := probe.NewScope(subj, plan)
		if err != nil {
			return err
		}

		// Two different reasons a mutating probe might not run, and they must not be conflated.
		// A resource with no delete can never be probed, so its nominal cost is noise. A
		// resource with no *plan* could be probed the moment one exists, so its unnarrowed
		// worst case is exactly the figure an operator needs in order to write that plan.
		probeable, why := subj.CanMutate()
		_, planWhy := sc.CanMutate()

		fmt.Printf("\n%s  (%s)\n", subj.Resource, subj.CollectionTemplate)
		fmt.Printf("  %d field(s), %d writable", len(subj.Fields), len(subj.WritableFields()))
		if subj.NameField != "" {
			fmt.Printf(", name prefix goes in %q", subj.NameField)
		}
		fmt.Println()
		fmt.Printf("  %s\n", sc)

		switch {
		case !probeable:
			// Stated per subject rather than per probe, because it is a property of
			// the resource and would otherwise be repeated nine times.
			fmt.Printf("  mutating probes refused: %s\n", why)
		case planWhy != "":
			fmt.Printf("  mutating probes need a plan: %s\n", planWhy)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\n  PROBE\tKIND\tREQUESTS\tCREATES")

		var totalRequests, totalCreates int

		for _, e := range probe.Catalogue(sc) {
			if only != "" && e.Name != only {
				continue
			}

			// A mutating probe against a resource that cannot be mutated costs nothing,
			// because it will never run. Showing its nominal cost would overstate the
			// budget by an order of magnitude.
			requests, creates := e.Cost, e.Creates
			if e.Kind == probe.KindMutating && !probeable {
				requests, creates = 0, 0
			}

			fmt.Fprintf(w, "  %s\t%s\t%d\t%d\n", e.Name, e.Kind, requests, creates)

			totalRequests += requests
			totalCreates += creates
		}

		fmt.Fprintf(w, "  %s\t%s\t%d\t%d\n", "TOTAL", "", totalRequests, totalCreates)

		if err := w.Flush(); err != nil {
			return fmt.Errorf("writing the catalogue: %w", err)
		}

		printBudgetVerdict(sc, totalRequests, totalCreates)
	}

	fmt.Println()

	return nil
}

// printBudgetVerdict compares the catalogue's worst case against the default caps.
//
// This is the whole reason -list prints costs rather than just names. The unbounded
// catalogue against the pilot wants roughly 500 requests and 226 creates, against
// defaults of 200 and 25 -- so the full run does not fit, and an operator needs to know
// that *before* pointing it at a tenant rather than discovering it as an exit 4 partway
// through.
//
// It is not a defect in either number. The per-field probes scale with the count of
// writable fields, and the answer is to narrow them with a plan: candidates, fixtures
// and a deny list are how an operator says which fields are worth the requests. Saying
// so here is more useful than quietly showing a total nobody compares to anything.
func printBudgetVerdict(sc probe.Scope, requests, creates int) {
	budget := sc.Plan.Budget.WithDefaults()

	overRequests := requests > budget.MaxRequests
	overCreates := creates > budget.MaxCreates

	which := "default"
	if sc.Planned {
		which = "plan's"
	}

	if !overRequests && !overCreates {
		fmt.Printf("\n  Fits the %s budget (%d/%d requests, %d/%d creates).\n",
			which, requests, budget.MaxRequests, creates, budget.MaxCreates)
		return
	}

	fmt.Printf("\n  Does NOT fit the %s budget: %d/%d requests, %d/%d creates.\n",
		which, requests, budget.MaxRequests, creates, budget.MaxCreates)

	if !sc.Planned {
		fmt.Println("  This is the unnarrowed worst case: with no plan, the per-field probes are")
		fmt.Println("  costed over every writable field. Supply -plan with fixtures, candidates and")
		fmt.Println("  a deny list to narrow them -- that is what the plan is for.")

		return
	}

	fmt.Println("  Narrow the plan further: fewer candidate fields for the immutability protocol,")
	fmt.Println("  or a longer deny list. Without that, a record run would stop at exit 4.")
}
