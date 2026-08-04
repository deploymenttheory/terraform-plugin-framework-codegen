package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	hcspec "github.com/hashicorp/terraform-plugin-codegen-spec/spec"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec"
)

// The usage sketches are constants rather than fields read back off the table,
// because a run function that reads interopVerbs while interopVerbs is being
// initialised with that same function is an initialisation cycle the compiler
// rejects.
const (
	usageInteropExport = "interop export -blueprint DIR [-out FILE] [-only KEY] [-strict] [-report FILE]"
	usageInteropImport = "interop import -spec FILE -out DIR -provider NAME [flags]"
)

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
	{
		name:    "import",
		summary: "read codegen-spec v0.1 JSON into draft blueprints",
		usage:   usageInteropImport,
		run:     runInteropImport,
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

	s, report, err := spec.FromBlueprint(bp)
	if err != nil {
		return err
	}

	data, err := spec.Marshal(s)
	if err != nil {
		return err
	}

	// Validated against upstream's own embedded JSON schema before it is written,
	// not after. A document that fails that schema is a bug in this package, and
	// writing it first would leave a bad artefact on disk for CI to diff against.
	// This is also the conformance check that justifies the whole package.
	if err := spec.Validate(context.Background(), data); err != nil {
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

func runInteropImport(args []string) error {
	fs, _ := newFlagSet("interop import", usageInteropImport)

	var (
		specPath = fs.String("spec", "", "codegen-spec v0.1 JSON to read (required)")
		out      = fs.String("out", "", "directory to write draft blueprints under (required)")
		provider = fs.String("provider", "", "registry provider name, which prefixes every Terraform type (required)")

		typePrefix    = fs.String("type-prefix", "", "type prefix; defaults to -provider")
		goModule      = fs.String("go-module", "", "the generated provider's Go module path")
		apiVersionDir = fs.String("api-version-dir", "", "version directory generated packages live under, e.g. v7")
		serviceGroup  = fs.String("service-group", "", "service grouping for the on-disk layout")

		list = fs.Bool("list", false, "report what the document offers and exit")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	if *specPath == "" {
		return usagef("-spec is required")
	}

	data, err := os.ReadFile(*specPath) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return fmt.Errorf("reading %s: %w", *specPath, err)
	}

	s, err := spec.Parse(context.Background(), data)
	if err != nil {
		return err
	}

	if *list {
		return listSpec(s)
	}

	if *provider == "" {
		return usagef("-provider is required: it names the registry provider and prefixes every Terraform type")
	}
	if *out == "" {
		return usagef("-out is required")
	}

	bp, report, err := spec.ToBlueprint(s, spec.Options{
		Provider:      *provider,
		TypePrefix:    *typePrefix,
		GoModule:      *goModule,
		APIVersionDir: *apiVersionDir,
		ServiceGroup:  *serviceGroup,
	})
	if err != nil {
		return err
	}

	printReport(report)

	written, err := writeDrafts(*out, bp)
	if err != nil {
		return err
	}

	printUnauthored(bp, written)

	return nil
}

// writeDrafts writes one draft per resource, plus the provider block.
//
// blueprint.Save marshals without validating, which is what makes this possible: a
// draft is by definition not yet valid, and a Save that validated would leave the
// operator with nothing to edit.
func writeDrafts(root string, bp blueprint.Blueprint) ([]string, error) {
	var written []string

	// The provider part carries no resources, so it is a draft too -- its SDK block
	// is empty and has to be authored.
	providerPart := blueprint.Blueprint{FormatVersion: bp.FormatVersion, Provider: bp.Provider}
	providerPath := filepath.Join(root, "provider"+spec.DraftExt)

	if err := blueprint.Save(providerPath, providerPart); err != nil {
		return nil, err
	}
	written = append(written, providerPath)

	for _, res := range bp.Resources {
		part := blueprint.Blueprint{FormatVersion: bp.FormatVersion, Resources: []blueprint.Resource{res}}
		path := filepath.Join(root, "resources", res.Key+spec.DraftExt)

		if err := blueprint.Save(path, part); err != nil {
			return nil, err
		}
		written = append(written, path)
	}

	return written, nil
}

// printUnauthored tells the operator what to write and what to do next.
func printUnauthored(bp blueprint.Blueprint, written []string) {
	fields := spec.Unauthored(bp)

	fmt.Fprintf(os.Stderr, "\n%d draft(s) written. %d field group(s) must be authored before emission:\n\n",
		len(written), len(fields))

	for _, f := range fields {
		fmt.Fprintf(os.Stderr, "  %s\n", f)
	}

	fmt.Fprintf(os.Stderr, `
A draft is deliberately invisible to emit and verify: %q does not end in %q, so
LoadDir cannot open it. Author the fields above, rename the drafts, then check the
bindings against the SDK:

  go run ./cmd/tfpluginframeworkgen bindings -blueprint <dir> -module <provider dir>

`, spec.DraftExt, blueprint.Ext)
}

// listSpec reports what a document offers without writing anything.
func listSpec(s hcspec.Specification) error {
	name := "(none)"
	if s.Provider != nil {
		name = s.Provider.Name
	}

	log.Printf("provider %s, version %s", name, s.Version)
	log.Printf("%d resource(s), %d data source(s)", len(s.Resources), len(s.DataSources))

	for _, r := range s.Resources {
		attrs, blocks := 0, 0
		if r.Schema != nil {
			attrs = len(r.Schema.Attributes)
			blocks = len(r.Schema.Blocks)
		}
		log.Printf("  resource %-30s %2d attribute(s), %d block(s)", r.Name, attrs, blocks)
	}

	for _, d := range s.DataSources {
		attrs := 0
		if d.Schema != nil {
			attrs = len(d.Schema.Attributes)
		}
		log.Printf("  data source %-27s %2d attribute(s)   (import not implemented)", d.Name, attrs)
	}

	return nil
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
func printReport(r spec.Report) {
	fmt.Fprintln(os.Stderr, r.Summary())

	if len(r.Notes) == 0 {
		return
	}

	current := spec.Severity("")

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
	if n := r.Count(spec.SeverityDropped); n > 0 {
		fmt.Fprintf(os.Stderr, "::warning::%d blueprint value(s) have no counterpart in the exported specification\n", n)
	}
	if n := r.Count(spec.SeverityLossy); n > 0 {
		fmt.Fprintf(os.Stderr, "::warning::%d blueprint value(s) were coarsened on export\n", n)
	}
}

func writeReport(path string, r spec.Report) error {
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
