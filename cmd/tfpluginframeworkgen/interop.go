package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/interop"
)

// The usage sketches are constants rather than fields read back off the table,
// because a run function that reads interopVerbs while interopVerbs is being
// initialised with that same function is an initialisation cycle the compiler
// rejects.
const usageInteropExport = "interop export -blueprint DIR [-out FILE] [-only KEY] [-strict] [-report FILE]"

// interopVerbs is the local verb table.
//
// interop is the first subcommand with two levels, and the table is deliberately
// the same shape as the top-level one so that `blueprint <validate|diff|list>` can
// adopt it unchanged rather than inventing a second convention.
var interopVerbs = []command{
	{
		name:    "export",
		summary: "write codegen-spec v0.1 JSON from blueprints",
		usage:   usageInteropExport,
		run:     runInteropExport,
	},
}

func runInterop(args []string) error {
	if len(args) == 0 {
		printInteropVerbs()
		return usagef("interop needs a verb")
	}

	// -h before the verb is a request for the verb list, not a malformed flag.
	if args[0] == "-h" || args[0] == "-help" || args[0] == "--help" {
		printInteropVerbs()
		return nil
	}

	for _, v := range interopVerbs {
		if v.name == args[0] {
			return v.run(args[1:])
		}
	}

	printInteropVerbs()
	return usagef("interop: unknown verb %q", args[0])
}

func printInteropVerbs() {
	fmt.Fprintf(os.Stderr, "Usage: tfpluginframeworkgen interop <verb> [flags]\n\nVerbs:\n")
	for _, v := range interopVerbs {
		fmt.Fprintf(os.Stderr, "  %-8s %s\n", v.name, v.summary)
	}
	fmt.Fprintln(os.Stderr)
}

func runInteropExport(args []string) error {
	fs, _ := newFlagSet("interop export", usageInteropExport)

	var (
		blueprintPath = fs.String("blueprint", "", "blueprint file or directory (required)")
		out           = fs.String("out", "", "file to write; stdout when omitted")
		only          = fs.String("only", "", "restrict the export to one resource, by blueprint key")
		strict        = fs.Bool("strict", false, "exit non-zero if anything was coarsened or dropped")
		reportPath    = fs.String("report", "", "write the downgrade notes to this file as JSON")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	if *blueprintPath == "" {
		return usagef("-blueprint is required")
	}

	// LoadDir validates, so the export is always of a blueprint that is internally
	// consistent. Exporting an invalid blueprint would produce a document whose
	// problems look like interop bugs.
	bp, err := blueprint.LoadDir(*blueprintPath)
	if err != nil {
		return err
	}

	if *only != "" {
		if err := keepOnly(&bp, *only); err != nil {
			return err
		}
	}

	s, report, err := interop.FromBlueprint(bp)
	if err != nil {
		return err
	}

	data, err := interop.Marshal(s)
	if err != nil {
		return err
	}

	// Validated against upstream's own embedded JSON schema before it is written,
	// not after. A document that fails that schema is a bug in this package, and
	// writing it first would leave a bad artefact on disk for CI to diff against.
	// This is also the conformance check that justifies the whole package.
	if err := interop.Validate(context.Background(), data); err != nil {
		return err
	}

	printReport(report)

	if *reportPath != "" {
		if err := writeReport(*reportPath, report); err != nil {
			return err
		}
	}

	if *out == "" {
		if _, err := os.Stdout.Write(data); err != nil {
			return fmt.Errorf("writing to stdout: %w", err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(*out), 0o750); err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(*out), err)
		}
		if err := os.WriteFile(*out, data, 0o600); err != nil {
			return fmt.Errorf("writing %s: %w", *out, err)
		}
		log.Printf("wrote %s", *out)
	}

	return report.Err(*strict)
}

// keepOnly narrows the blueprint to one resource.
//
// It matches on Key rather than on the Terraform type, because Key is the stable
// merge identifier every other subcommand's -only already uses.
func keepOnly(bp *blueprint.Blueprint, key string) error {
	for _, r := range bp.Resources {
		if r.Key == key {
			bp.Resources = []blueprint.Resource{r}
			bp.DataSources = nil
			return nil
		}
	}

	for _, d := range bp.DataSources {
		if d.Key == key {
			bp.DataSources = []blueprint.DataSource{d}
			bp.Resources = nil
			return nil
		}
	}

	return usagef("-only %q matches no resource or data source key", key)
}

// printReport writes the downgrade notes to stderr.
//
// Stderr with fmt rather than log, following runIngest's printNotes: -q silences
// the log package, and a loss report is not progress chatter. A user who silences
// progress has not asked to be kept in the dark about what the export could not
// carry.
func printReport(r interop.Report) {
	fmt.Fprintln(os.Stderr, r.Summary())

	if len(r.Notes) == 0 {
		return
	}

	current := interop.Severity("")

	for _, n := range r.Sorted() {
		if n.Severity != current {
			fmt.Fprintf(os.Stderr, "\n%s:\n", n.Severity)
			current = n.Severity
		}
		fmt.Fprintf(os.Stderr, "  %s: %s\n", n.Path, n.Message)
	}

	fmt.Fprintln(os.Stderr)

	// One annotation per severity for the checks UI, matching runBindings' single
	// ::error:: line rather than annotating every note.
	if n := r.Count(interop.SeverityDropped); n > 0 {
		fmt.Fprintf(os.Stderr, "::warning::%d blueprint value(s) have no counterpart in the exported specification\n", n)
	}
	if n := r.Count(interop.SeverityLossy); n > 0 {
		fmt.Fprintf(os.Stderr, "::warning::%d blueprint value(s) were coarsened on export\n", n)
	}
}

func writeReport(path string, r interop.Report) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the report: %w", err)
	}

	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	log.Printf("wrote %s", path)

	return nil
}
