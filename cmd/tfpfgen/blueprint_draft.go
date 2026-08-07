package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/draft"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/openapi"
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

	candidates := draft.FilterCandidates(doc.Discover(), *tag, *includeUnusable)
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

	err = draft.Run(doc, candidates, draft.Options{
		Infer: openapi.InferOptions{
			Provider:          *provider,
			SDKServiceRoot:    *sdkRoot,
			SDKAccessorPrefix: *accessor,
			APIVersionDir:     *apiVersionDir,
			SDKDialect:        dialect,
			SDKModelsImport:   *sdkModels,
		},
		Out:            *out,
		ScenarioDrafts: *scenarioDrafts,
		PruneModule:    *pruneModule,
		Excluded:       excluded,
		Notes:          printNotes,
	})

	// A run that drafted nothing is this command's "nothing to do", and the CLI
	// owns that vocabulary: the package states the drafting fact, and the wrapping
	// gives it the exit code every other subcommand uses for the same situation.
	if errors.Is(err, draft.ErrNothingInferred) {
		return fmt.Errorf("%w: %w", errNothingToDo, err)
	}

	return err
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
