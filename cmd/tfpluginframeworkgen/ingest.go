package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/ingest/openapi"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specstore"
)

func runIngest(args []string) error {
	fs, _ := newFlagSet("ingest", "ingest [-spec-root DIR] [-snapshot NAME] [-only TAG] [-list]")

	var (
		specRoot = fs.String("spec-root", "openapi-specs/thousandeyes", "directory holding pinned snapshots")
		snapshot = fs.String("snapshot", "", "snapshot to read (default: the newest)")
		specPath = fs.String("spec", "", "read this document directly, bypassing the snapshot store")
		only     = fs.String("only", "", "restrict to candidates whose tag or key contains this")
		list     = fs.Bool("list", false, "list what the document offers and exit")
		all      = fs.Bool("all", false, "include candidates that cannot become resources or data sources")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	path, err := resolveSpecPath(*specPath, *specRoot, *snapshot)
	if err != nil {
		return err
	}

	doc, err := openapi.Load(path)
	if err != nil {
		return err
	}

	log.Printf("specification: %s (%s %s)", path, doc.Title, doc.Version)

	candidates := filterCandidates(doc.Discover(), *only, *all)
	if len(candidates) == 0 {
		return fmt.Errorf("%w: no candidates matched", errNothingToDo)
	}

	if *list {
		printCandidates(candidates)
		return nil
	}

	// Inference is the next increment. Saying so beats a subcommand that appears
	// to work and writes nothing.
	return fmt.Errorf("ingest: blueprint inference is %w; -list works today", errNotImplemented)
}

// resolveSpecPath picks the document to read.
func resolveSpecPath(specPath, specRoot, snapshotName string) (string, error) {
	// An explicit path is for inspecting a document that is not pinned yet.
	// Everything reproducible goes through the snapshot store.
	if specPath != "" {
		return specPath, nil
	}

	snap, err := specstore.Find(specRoot, snapshotName)
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

func filterCandidates(in []openapi.Candidate, only string, includeUnusable bool) []openapi.Candidate {
	out := make([]openapi.Candidate, 0, len(in))

	for _, c := range in {
		kind, _ := c.Classify()
		if kind == openapi.KindNeither && !includeUnusable {
			continue
		}
		if only != "" && !matches(c, only) {
			continue
		}
		out = append(out, c)
	}

	return out
}

func matches(c openapi.Candidate, want string) bool {
	want = strings.ToLower(want)
	return strings.Contains(strings.ToLower(c.Tag), want) ||
		strings.Contains(strings.ToLower(c.Key), want) ||
		strings.Contains(strings.ToLower(c.CollectionPath), want)
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
		case openapi.KindResource:
			resources++
		case openapi.KindDataSource:
			dataSources++
		case openapi.KindNeither:
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
