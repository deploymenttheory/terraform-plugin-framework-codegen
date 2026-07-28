package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"[-resource KEY] [-only PROBE] [-list] [--allow-mutations]"

// Probe modes. The default is replay, deliberately: the safe mode is what you get by
// typing less, and the mode that can change somebody's tenant has to be spelled out.
const (
	modeReplay = "replay"
	modeRecord = "record"
	modeVerify = "verify"
	modeSweep  = "sweep"
)

func runProbe(args []string) error {
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

	if *list {
		return listProbes(subjects, *only)
	}

	// The mutating tier needs the gating conjunction, which lands in Phase 4.6. Until
	// then --allow-mutations is accepted and refused rather than ignored: silently
	// dropping it would let a scripted run believe it had probed the write path.
	if *allowMutations {
		return fmt.Errorf("--allow-mutations: %w (the gate is not built yet, so no mutating "+
			"probe can be authorised)", errNotImplemented)
	}

	if *mode == modeSweep {
		return fmt.Errorf("probe -mode sweep: %w", errNotImplemented)
	}

	return runProbeMode(*mode, subjects, *only, *evidenceRoot, *providerName, bp.Source.SpecVersion)
}

// runProbeMode records, replays or verifies the read-only tier.
func runProbeMode(
	mode string,
	subjects []probe.Subject,
	only, evidenceRoot, providerName, apiVersion string,
) error {
	failures := 0

	for _, subj := range subjects {
		root := filepath.Join(evidenceRoot, providerName, subj.Resource)

		switch mode {
		case modeRecord:
			if err := recordProbe(subj, only, root, providerName, apiVersion); err != nil {
				return err
			}

		case modeReplay, modeVerify:
			if err := replayProbe(mode, subj, only, root); err != nil {
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
func recordProbe(subj probe.Subject, only, root, providerName, apiVersion string) error {
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

	redactor, err := cassette.NewRedactor(map[string]string{"bearer": token}, nil)
	if err != nil {
		return err
	}

	ctx, cancel := probe.DeadlineFor(context.Background(), probe.Budget{})
	defer cancel()

	result, err := probe.Run(ctx, probe.RunOptions{
		Mode:     probe.ModeRecord,
		Subject:  subj,
		Only:     only,
		BaseURL:  endpoint,
		Token:    token,
		Redactor: redactor,
		Secrets:  map[string]string{"bearer": token},
	})
	if err != nil {
		return err
	}

	printProbeReport(subj.Resource, result.Report)

	meta := probe.RecordingMetadata(providerName, subj, result.Report.Profile.Host, version.Version)
	meta.BasePath = basePathOf(endpoint)
	// The API version the blueprint was inferred from, so the snapshot directory names the
	// specification the facts were recorded against rather than reading "unknown". A cassette
	// whose directory does not say which API version produced it is far less useful a year later.
	meta.APIVersion = apiVersion

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

	log.Printf("wrote %s", snap.Dir)

	return nil
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

	result, err := probe.Run(context.Background(), probe.RunOptions{
		Mode:    probe.ModeReplay,
		Subject: subj,
		Only:    only,
		// The recorded prefix, reproduced. A cassette stores full request paths, so replaying
		// a recording made against an endpoint with a prefix needs that prefix back.
		BaseURL:      replayBaseURL + meta.BasePath,
		Interactions: interactions,
	})
	if err != nil {
		return err
	}

	printProbeReport(subj.Resource, result.Report)

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
func listProbes(subjects []probe.Subject, only string) error {
	for _, subj := range subjects {
		canMutate, why := subj.CanMutate()

		fmt.Printf("\n%s  (%s)\n", subj.Resource, subj.CollectionTemplate)
		fmt.Printf("  %d field(s), %d writable", len(subj.Fields), len(subj.WritableFields()))
		if subj.NameField != "" {
			fmt.Printf(", name prefix goes in %q", subj.NameField)
		}
		fmt.Println()

		if !canMutate {
			// Stated per subject rather than per probe, because it is a property of
			// the resource and would otherwise be repeated nine times.
			fmt.Printf("  mutating probes refused: %s\n", why)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\n  PROBE\tKIND\tREQUESTS\tCREATES")

		var totalRequests, totalCreates int

		for _, e := range probe.Catalogue(subj) {
			if only != "" && e.Name != only {
				continue
			}

			// A mutating probe against a subject that cannot be mutated costs nothing,
			// because it will not run. Showing its nominal cost would overstate the
			// budget by an order of magnitude.
			requests, creates := e.Cost, e.Creates
			if e.Kind == probe.KindMutating && !canMutate {
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

		printBudgetVerdict(totalRequests, totalCreates)
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
func printBudgetVerdict(requests, creates int) {
	budget := probe.Budget{}.WithDefaults()

	overRequests := requests > budget.MaxRequests
	overCreates := creates > budget.MaxCreates

	if !overRequests && !overCreates {
		fmt.Printf("\n  Fits the default budget (%d/%d requests, %d/%d creates).\n",
			requests, budget.MaxRequests, creates, budget.MaxCreates)
		return
	}

	fmt.Printf("\n  Does NOT fit the default budget: %d/%d requests, %d/%d creates.\n",
		requests, budget.MaxRequests, creates, budget.MaxCreates)
	fmt.Println("  The per-field probes scale with the number of writable fields, so a full run")
	fmt.Println("  needs a plan that narrows them -- candidates for the fields worth probing, and")
	fmt.Println("  a deny list for the rest. Without one, a record run would stop at exit 4.")
}
