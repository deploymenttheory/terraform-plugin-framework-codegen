// Package draft turns a loaded OpenAPI document into a set of blueprints on disk.
//
// This is the body of `tfpfgen blueprint draft`: candidate selection, inference of
// every selected family into a resource and its lookup companion, pruning the
// drafted bindings against the pinned SDK, and writing what survives. The command
// keeps flag parsing, path resolution and the tabular presentation; everything that
// decides what a blueprint contains lives here.
//
// It is a package rather than a block of `package main` because the drafting run is
// itself a test fixture. Tests that want a blueprint want the blueprint the pipeline
// actually produces, derived at test time from a committed document, and nothing
// under internal/ can reach into package main to get one. The alternative -- a second
// deriver written for tests -- would drift from this one, and tests would then be
// asserting against output the pipeline never emits.
//
// The progress lines are part of the contract. A drafting run reports what it
// excluded, what it skipped and what it wrote, and those lines are the only account
// of the families the document offered and this did not take; a caller that wants
// them quiet silences the log package, as the CLI's -q does.
package draft

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/openapi"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// ErrNothingInferred reports a run whose inputs were valid but produced no
// blueprint at all. It is an error rather than a silent success because it almost
// always means the document, the filter or the SDK being pruned against is not the
// one the caller meant.
var ErrNothingInferred = errors.New("nothing could be inferred")

// Options names everything a drafting run needs beyond the document and the
// candidates chosen from it.
//
// A struct rather than a parameter list: the inference knobs, the output roots and
// the prune module are unrelated to each other, and seven positional arguments of
// which three are strings is a call site nobody can read and a signature every new
// knob makes worse.
type Options struct {
	// Infer carries the naming and binding conventions inference works to.
	Infer openapi.InferOptions

	// Out is the blueprint tree's root; resources and data sources are written to
	// its resources/ and datasources/ subdirectories.
	Out string

	// ScenarioDrafts, when set, is the directory a probe-plan worksheet is
	// scaffolded into for each resource written.
	ScenarioDrafts string

	// PruneModule, when set, is the module root holding the pinned SDK. Every
	// drafted binding is resolved against it before anything is written, and what
	// the SDK cannot carry is pruned by name. It requires a provider block in Out,
	// because pruning resolves fluent chains from the client type it declares.
	PruneModule string

	// Excluded is the curated sidecar: a candidate it matches is skipped by name
	// with the reason the sidecar records.
	Excluded openapi.Exclusions

	// Notes, when set, is handed everything inference could not express, once the
	// writing is done and before the run's verdict. It is a hook rather than a
	// return value because where it is called relative to the progress lines is
	// itself the reporting order, and because a caller deriving blueprints in a
	// test wants the blueprints without the report.
	Notes func([]openapi.Caveat)
}

// Run infers every candidate and writes what it can.
//
// One candidate that cannot be inferred must not stop the rest: the output is a
// starting point for curation, not an all-or-nothing build. A run that inferred
// nothing at all is the one failure, and it returns ErrNothingInferred.
func Run(doc *openapi.Document, candidates []openapi.Candidate, opts Options) error {
	var (
		notes       []openapi.Caveat
		resources   []blueprint.Resource
		dataSources []blueprint.DataSource
		written     int
		skipped     int
	)

	// Import aliases share one namespace across every block kind, because all of
	// them register into the same generated provider package -- validation refuses
	// a duplicate. Nested identifiers are already deduplicated inside each
	// resource; this is the same rule one level up, across the whole drafted set.
	takenAliases := map[string]bool{}

	for _, c := range candidates {
		// The sidecar speaks first: a curated exclusion is a decision already
		// made, and the run repeats its reason as a named skip.
		if reason, is := opts.Excluded.Match(c); is {
			log.Printf("excluded  %s: %s", c.Key, reason)
			skipped++
			continue
		}

		kind, why := c.Classify()

		if kind == openapi.CandidateKindDataSource {
			ds, dsNotes, err := doc.InferDataSource(c, opts.Infer)
			notes = append(notes, dsNotes...)
			if err != nil {
				log.Printf("skipped   %s: %v", c.Key, err)
				skipped++
				continue
			}
			ds.GoPackageAlias = naming.Unique(takenAliases, ds.GoPackageAlias)
			dataSources = append(dataSources, ds)
			continue
		}

		if kind != openapi.CandidateKindResource {
			// Said out loud rather than silently skipped: silence reads as agreement,
			// and an action the spec offers deserves at least a line saying inference
			// does not reach it yet.
			log.Printf("skipped   %s: %s inference is not implemented (%s)", c.Key, kind, why)
			skipped++
			continue
		}

		res, resNotes, err := doc.Infer(c, opts.Infer)
		notes = append(notes, resNotes...)
		if err != nil {
			// One resource that cannot be inferred must not stop the rest: the
			// output is a starting point for curation, not an all-or-nothing build.
			log.Printf("skipped   %s: %v", c.Key, err)
			skipped++
			continue
		}

		res.GoPackageAlias = naming.Unique(takenAliases, res.GoPackageAlias)
		resources = append(resources, res)

		// A manageable family gets a lookup companion as well. Looking up an
		// object somebody else created is the most useful data source there
		// is, and the two live in separate Terraform namespaces, so the family
		// name carries both. A family the data-source shape cannot serve --
		// no list to resolve through, nothing selectable -- says so once and
		// keeps its resource.
		ds, dsNotes, err := doc.InferDataSource(c, opts.Infer)
		notes = append(notes, dsNotes...)
		if err != nil {
			log.Printf("no lookup %s: %v", c.Key, err)
			continue
		}
		ds.GoPackageAlias = naming.Unique(takenAliases, ds.GoPackageAlias)
		dataSources = append(dataSources, ds)
	}

	// The document promises; the SDK disposes. With a module to check against,
	// every drafted binding is resolved against the real SDK before anything is
	// written, and what does not resolve is pruned by name -- the computed form
	// of the drop a curator used to write by hand.
	if opts.PruneModule != "" {
		pruned, err := pruneAgainstSDK(opts.Out, opts.PruneModule, &resources, &dataSources)
		if err != nil {
			return err
		}
		skipped += pruned
	}

	for _, ds := range dataSources {
		bp := blueprint.Blueprint{
			FormatVersion: blueprint.FormatVersion,
			DataSources:   []blueprint.DataSource{ds},
		}
		path := filepath.Join(opts.Out, "datasources", ds.Key+blueprint.Ext)
		if err := blueprint.Save(path, bp); err != nil {
			return err //nolint:wrapcheck // Save already names the path it could not write
		}
		log.Printf("wrote     %s (dataSource, %d attributes, %d selector(s))",
			path, len(ds.Schema.Attributes), len(ds.Binding.Selectors))
		written++
	}

	for _, res := range resources {
		bp := blueprint.Blueprint{
			FormatVersion: blueprint.FormatVersion,
			Resources:     []blueprint.Resource{res},
		}

		path := filepath.Join(opts.Out, "resources", res.Key+blueprint.Ext)
		if err := blueprint.Save(path, bp); err != nil {
			return err //nolint:wrapcheck // Save already names the path it could not write
		}

		log.Printf("wrote     %s (%d attributes)", path, len(res.Schema.Attributes))
		written++

		if opts.ScenarioDrafts != "" {
			if err := writeScenarioDraft(opts.ScenarioDrafts, bp, res); err != nil {
				return err
			}
		}
	}

	if opts.Notes != nil {
		opts.Notes(notes)
	}

	if written == 0 {
		return ErrNothingInferred
	}

	log.Printf(
		"%d blueprint(s) written, %d candidate(s) skipped, %d note(s)",
		written,
		skipped,
		len(notes),
	)

	return nil
}

// pruneAgainstSDK verifies drafted bindings against the pinned SDK and removes
// what cannot be generated, reporting each removal. The provider block is read
// from the output directory, because pruning resolves fluent chains from the
// client type it declares.
func pruneAgainstSDK(
	out, module string,
	resources *[]blueprint.Resource,
	dataSources *[]blueprint.DataSource,
) (skipped int, err error) {
	providerPath := filepath.Join(out, "provider"+blueprint.Ext)
	pbp, err := blueprint.Load(providerPath)
	if err != nil {
		return 0, fmt.Errorf("-prune-module needs the provider block at %s: %w", providerPath, err)
	}

	combined := blueprint.Blueprint{
		FormatVersion: blueprint.FormatVersion,
		Provider:      pbp.Provider,
		Resources:     *resources,
		DataSources:   *dataSources,
	}

	removals := sdkbind.Prune(sdkbind.NewLoader(module), &combined)
	for _, p := range removals {
		log.Printf("pruned    %s", p)
	}

	*resources = nil
	for _, r := range combined.Resources {
		if !r.Drop {
			*resources = append(*resources, r)
		} else {
			skipped++
		}
	}
	*dataSources = nil
	for _, d := range combined.DataSources {
		if !d.Drop {
			*dataSources = append(*dataSources, d)
		} else {
			skipped++
		}
	}

	return skipped, nil
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
		return err //nolint:wrapcheck // the os error already names the directory
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return err //nolint:wrapcheck // the os error already names the file
	}

	log.Printf("drafted   %s (curate every %q, then rename away the .draft)",
		path, probe.CurateMe)

	return nil
}

// FilterCandidates narrows what the document discovered to what a run should
// consider: a candidate no block kind can be built from is dropped unless
// includeUnusable says otherwise, and a non-empty tag selects by name.
func FilterCandidates(
	in []openapi.Candidate,
	tag string,
	includeUnusable bool,
) []openapi.Candidate {
	out := make([]openapi.Candidate, 0, len(in))

	for _, c := range in {
		kind, _ := c.Classify()
		if kind == openapi.CandidateKindNeither && !includeUnusable {
			continue
		}
		if tag != "" && !Matches(c, tag) {
			continue
		}
		out = append(out, c)
	}

	return out
}

// Matches reports whether a candidate matches any of the comma-separated terms.
//
// Any-of rather than all-of, because the flag's job is selecting a batch: `-tag
// tests,alerts,dashboards` names three areas, and no candidate belongs to all three.
func Matches(c openapi.Candidate, want string) bool {
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
