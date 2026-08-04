package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/openapi"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/snapshot"
)

const usageBlueprintDraft = "blueprint draft [-openapi-dir DIR] [-snapshot NAME] [-tag TAG] [-out DIR] [-dry-run]"

func runBlueprintDraft(args []string) error {
	fs, _ := newFlagSet("blueprint draft", usageBlueprintDraft)

	var (
		openapiDir  = fs.String("openapi-dir", "openapi/thousandeyes", "directory holding pinned OpenAPI snapshots")
		snapshot    = fs.String("snapshot", "", "snapshot to read (default: the newest)")
		openapiPath = fs.String("openapi", "", "read this OpenAPI document directly, bypassing the snapshot store")
		tag         = fs.String("tag", "",
			"restrict to candidates whose tag or key contains this; comma-separate several")
		dryRun          = fs.Bool("dry-run", false, "list what the document offers and write nothing")
		includeUnusable = fs.Bool("include-unusable", false, "include candidates that cannot become resources or data sources")
		out             = fs.String("out", "", "write inferred blueprints under this directory")
		provider        = fs.String("provider", "thousandeyes", "provider name, which prefixes every resource type")
		sdkRoot         = fs.String("sdk-service-root",
			"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/thousandeyes_api",
			"import prefix the SDK's service packages live under")
		accessor       = fs.String("sdk-accessor", "r.client.API", "expression reaching a service from the resource receiver")
		apiVersionDir  = fs.String("api-version-dir", "v7", "version directory generated packages live under")
		scenarioDrafts = fs.String("scenario-drafts", "",
			"also scaffold a KEY.scenario.draft.json scenario worksheet per resource under this directory")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	path, err := resolveOpenAPIPath(*openapiPath, *openapiDir, *snapshot)
	if err != nil {
		return err
	}

	doc, err := openapi.Load(path)
	if err != nil {
		return err
	}

	log.Printf("specification: %s (%s %s)", path, doc.Title, doc.Version)

	candidates := filterCandidates(doc.Discover(), *tag, *includeUnusable)
	if len(candidates) == 0 {
		return fmt.Errorf("%w: no candidates matched", errNothingToDo)
	}

	if *dryRun {
		printCandidates(candidates)
		return nil
	}

	if *out == "" {
		return usagef("-out is required unless -dry-run is given")
	}

	opts := openapi.InferOptions{
		Provider:          *provider,
		SDKServiceRoot:    *sdkRoot,
		SDKAccessorPrefix: *accessor,
		APIVersionDir:     *apiVersionDir,
	}

	return inferAll(doc, candidates, opts, *out, *scenarioDrafts)
}

func inferAll(
	doc *openapi.Document,
	candidates []openapi.Candidate,
	opts openapi.InferOptions,
	out string,
	planDrafts string,
) error {
	var (
		notes   []openapi.Caveat
		written int
		skipped int
	)

	for _, c := range candidates {
		if kind, why := c.Classify(); kind != openapi.CandidateKindResource {
			// Said out loud rather than silently skipped: silence reads as agreement,
			// and a data source or action the spec offers deserves at least a line
			// saying inference does not reach it yet.
			log.Printf("skipped   %s: %s inference is not implemented (%s)", c.Key, kind, why)
			skipped++
			continue
		}

		res, resNotes, err := doc.Infer(c, opts)
		notes = append(notes, resNotes...)
		if err != nil {
			// One resource that cannot be inferred must not stop the rest: the
			// output is a starting point for curation, not an all-or-nothing build.
			log.Printf("skipped   %s: %v", c.Key, err)
			skipped++
			continue
		}

		bp := blueprint.Blueprint{FormatVersion: blueprint.FormatVersion, Resources: []blueprint.Resource{res}}

		path := filepath.Join(out, "resources", res.Key+blueprint.Ext)
		if err := blueprint.Save(path, bp); err != nil {
			return err
		}

		log.Printf("wrote     %s (%d attributes)", path, len(res.Schema.Attributes))
		written++

		if planDrafts != "" {
			if err := writeScenarioDraft(planDrafts, bp, res); err != nil {
				return err
			}
		}
	}

	printNotes(notes)

	if written == 0 {
		return fmt.Errorf("%w: nothing could be inferred", errNothingToDo)
	}

	log.Printf("%d blueprint(s) written, %d candidate(s) skipped, %d note(s)", written, skipped, len(notes))

	return nil
}

// writeScenarioDraft scaffolds one resource's probe-plan worksheet.
//
// The .draft.json suffix is the whole mechanism, borrowed from interop's drafts: no
// loader resolves it -- the bulk record driver reads only KEY.scenario.json -- so a
// scaffold nobody has curated can never be recorded against by accident. Promotion is
// a rename, which is a diff a reviewer sees. An existing draft is never overwritten:
// a worksheet somebody has started marking up is theirs.
func writeScenarioDraft(dir string, bp blueprint.Blueprint, res blueprint.Resource) error {
	subj, err := probe.SubjectOf(bp, res)
	if err != nil {
		// A resource the prober cannot subject at all gets no worksheet; the probe
		// run will state the same refusal in full when it matters.
		log.Printf("no draft  %s: %v", res.Key, err)
		return nil
	}

	path := filepath.Join(dir, res.Key+".scenario.draft.json")
	if _, err := os.Stat(path); err == nil {
		log.Printf("kept      %s (already exists; drafts are never overwritten)", path)
		return nil
	}

	data, err := json.MarshalIndent(probe.DraftScenario(subj), "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling the %s plan draft: %w", res.Key, err)
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return err
	}

	log.Printf("drafted   %s (curate every %q, then rename away the .draft)",
		path, probe.CurateMe)

	return nil
}

// printNotes reports what inference could not do.
//
// This is as much the output as the blueprints are. A generator that silently
// drops what it cannot express produces a provider that looks complete and is
// not, so every skipped field is named.
func printNotes(notes []openapi.Caveat) {
	if len(notes) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "\n%d field(s) were not inferred:\n", len(notes))
	for _, n := range notes {
		fmt.Fprintf(os.Stderr, "  %s\n", n)
	}
}

// resolveOpenAPIPath picks the document to read.
func resolveOpenAPIPath(openapiPath, openapiDir, snapshotName string) (string, error) {
	// An explicit path is for inspecting a document that is not pinned yet.
	// Everything reproducible goes through the snapshot store.
	if openapiPath != "" {
		return openapiPath, nil
	}

	snap, err := snapshot.Find(openapiDir, snapshotName)
	if err != nil {
		return "", err
	}

	// A snapshot is meant to be immutable, so a mismatch means somebody edited
	// one in place -- which would make the generated provider unreproducible
	// from anything committed.
	if err := snap.Verify(); err != nil {
		return "", err
	}

	return snap.SpecPath(), nil
}

func filterCandidates(in []openapi.Candidate, tag string, includeUnusable bool) []openapi.Candidate {
	out := make([]openapi.Candidate, 0, len(in))

	for _, c := range in {
		kind, _ := c.Classify()
		if kind == openapi.CandidateKindNeither && !includeUnusable {
			continue
		}
		if tag != "" && !matches(c, tag) {
			continue
		}
		out = append(out, c)
	}

	return out
}

// matches reports whether a candidate matches any of the comma-separated terms.
//
// Any-of rather than all-of, because the flag's job is selecting a batch: `-tag
// tests,alerts,dashboards` names three areas, and no candidate belongs to all three.
func matches(c openapi.Candidate, want string) bool {
	for _, term := range strings.Split(want, ",") {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}

		if strings.Contains(strings.ToLower(c.Tag), term) ||
			strings.Contains(strings.ToLower(c.Key), term) ||
			strings.Contains(strings.ToLower(c.CollectionPath), term) {
			return true
		}
	}

	return false
}

// printCandidates reports what the document offers.
//
// The verdict column is the point. 315 operations is not 315 resources, and the
// useful output is a curation list -- what could be managed, what can only be
// read, and what the API offers that this does not cover.
func printCandidates(candidates []openapi.Candidate) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	fmt.Fprintln(tw, "KEY\tTAG\tCRUD\tEXTRA\tVERDICT")

	var resources, dataSources int

	for _, c := range candidates {
		kind, why := c.Classify()
		switch kind {
		case openapi.CandidateKindResource:
			resources++
		case openapi.CandidateKindDataSource:
			dataSources++
		case openapi.CandidateKindNeither:
		}

		extra := ""
		if n := len(c.Extra); n > 0 {
			extra = fmt.Sprintf("+%d", n)
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s: %s\n", c.Key, c.Tag, crudFlags(c), extra, kind, why)
	}

	_ = tw.Flush()

	fmt.Fprintf(os.Stdout, "\n%d candidate(s): %d could be resources, %d read-only\n",
		len(candidates), resources, dataSources)
}

// crudFlags renders which operations exist as a fixed-width mask, so the column
// scans vertically rather than having to be read.
func crudFlags(c Candidate) string {
	flag := func(op *openapi.Operation, letter string) string {
		if op == nil {
			return "-"
		}
		return letter
	}
	return flag(c.Create, "C") + flag(c.Read, "R") + flag(c.Update, "U") + flag(c.Delete, "D") + flag(c.List, "L")
}

// Candidate is aliased so this file reads without the package qualifier on every
// use.
type Candidate = openapi.Candidate
