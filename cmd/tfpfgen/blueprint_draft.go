package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/openapi"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/snapshot"
)

const usageBlueprintDraft = "blueprint draft [-openapi-dir DIR] [-snapshot NAME] [-tag TAG] " +
	"[-sdk-dialect restyService|kiotaFluent] [-exclusions FILE] [-out DIR] [-dry-run]"

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
		sdkDialect = fs.String("sdk-dialect", string(blueprint.DialectRestyService),
			"binding shape to infer: restyService, or kiotaFluent for a kiota-generated SDK")
		sdkModels = fs.String("sdk-models-package", "",
			"import path of the kiota SDK's models package (required with -sdk-dialect kiotaFluent)")
		exclusions = fs.String("exclusions", "",
			"exclusions sidecar; defaults to <openapi-dir>/"+openapi.ExclusionsFileName+" when present")
		pruneModule = fs.String("prune-module", "",
			"module root holding the pinned SDK; drafted bindings the SDK cannot carry are "+
				"pruned by name instead of written (requires a provider block in -out)")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	dialect := blueprint.SDKDialect(*sdkDialect)
	switch dialect {
	case blueprint.DialectRestyService, blueprint.DialectKiotaFluent:
	default:
		return usagef("-sdk-dialect %q is not restyService or kiotaFluent", *sdkDialect)
	}

	if dialect == blueprint.DialectKiotaFluent {
		if *sdkModels == "" {
			return usagef("-sdk-models-package is required with -sdk-dialect kiotaFluent: " +
				"it names where the generated models live")
		}
		// The resty knobs mean nothing to a fluent binding; a value somebody
		// typed deserves a refusal, not silence.
		var misused []string
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "sdk-service-root" || f.Name == "sdk-accessor" {
				misused = append(misused, "-"+f.Name)
			}
		})
		if len(misused) > 0 {
			return usagef("%s do(es) not apply under -sdk-dialect kiotaFluent; a fluent "+
				"binding resolves from the client and the models package", strings.Join(misused, ", "))
		}
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

	exclusionsPath := *exclusions
	if exclusionsPath == "" {
		exclusionsPath = filepath.Join(*openapiDir, openapi.ExclusionsFileName)
	}
	excluded, err := openapi.LoadExclusions(exclusionsPath)
	if err != nil {
		return err
	}

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
		SDKDialect:        dialect,
		SDKModelsImport:   *sdkModels,
	}

	return inferAll(doc, candidates, excluded, opts, *out, *scenarioDrafts, *pruneModule)
}

func inferAll(
	doc *openapi.Document,
	candidates []openapi.Candidate,
	excluded openapi.Exclusions,
	opts openapi.InferOptions,
	out string,
	planDrafts string,
	pruneModule string,
) error {
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
		if reason, is := excluded.Match(c); is {
			log.Printf("excluded  %s: %s", c.Key, reason)
			skipped++
			continue
		}

		kind, why := c.Classify()

		if kind == openapi.CandidateKindDataSource {
			ds, dsNotes, err := doc.InferDataSource(c, opts)
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

		res, resNotes, err := doc.Infer(c, opts)
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
		ds, dsNotes, err := doc.InferDataSource(c, opts)
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
	if pruneModule != "" {
		pruned, err := pruneAgainstSDK(out, pruneModule, &resources, &dataSources)
		if err != nil {
			return err
		}
		skipped += pruned
	}

	for _, ds := range dataSources {
		bp := blueprint.Blueprint{FormatVersion: blueprint.FormatVersion, DataSources: []blueprint.DataSource{ds}}
		path := filepath.Join(out, "datasources", ds.Key+blueprint.Ext)
		if err := blueprint.Save(path, bp); err != nil {
			return err
		}
		log.Printf("wrote     %s (dataSource, %d attributes, %d selector(s))",
			path, len(ds.Schema.Attributes), len(ds.Binding.Selectors))
		written++
	}

	for _, res := range resources {
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
