package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/emit"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/version"
)

type emitOptions struct {
	blueprintPath string
	out           string
	only          string
	dryRun        bool
	list          bool
	force         bool
	clean         bool
}

// planOnly reports whether the run inspects output without writing it.
func (o emitOptions) planOnly() bool { return o.list || o.dryRun }

// wholeProvider reports whether the run covers every resource. A -only run must
// not touch the manifest or the registration files, because a partial inventory
// would make verify report every unlisted file as an orphan.
func (o emitOptions) wholeProvider() bool { return o.only == "" }

func parseEmitFlags(args []string) (emitOptions, error) {
	fs, _ := newFlagSet("emit", "emit -blueprint DIR -out DIR [-only NAME] [-dry-run]")

	var o emitOptions
	fs.StringVar(&o.blueprintPath, "blueprint", "", "blueprint file or directory (required)")
	fs.StringVar(&o.out, "out", "", "provider root to write into (required)")
	fs.StringVar(&o.only, "only", "", "generate a single resource by key, for inspecting output")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the write plan and touch nothing")
	fs.BoolVar(&o.list, "list", false, "list the files that would be written and exit")
	fs.BoolVar(&o.force, "force", false, "overwrite files that are not marked as generated")
	fs.BoolVar(&o.clean, "clean", false, "delete files the blueprints no longer produce")

	if err := parse(fs, args); err != nil {
		return emitOptions{}, err
	}

	if o.blueprintPath == "" {
		return emitOptions{}, usagef("-blueprint is required")
	}
	if o.out == "" && !o.planOnly() {
		return emitOptions{}, usagef("-out is required unless -list or -dry-run is given")
	}

	return o, nil
}

func runEmit(args []string) error {
	o, err := parseEmitFlags(args)
	if err != nil {
		return err
	}

	bp, err := blueprint.LoadDir(o.blueprintPath)
	if err != nil {
		return err
	}

	log.Printf("blueprint: %s (%d resource(s), %d data source(s))",
		o.blueprintPath, len(bp.Resources), len(bp.DataSources))

	gen, err := emit.New()
	if err != nil {
		return err
	}

	plan, err := gen.Build(bp, emit.Options{BlueprintPath: o.blueprintPath, Only: o.only})
	if err != nil {
		return err
	}

	if len(plan.Files) == 0 {
		return fmt.Errorf("%w: nothing matched", errNothingToDo)
	}

	if o.planOnly() {
		printPlan(plan)
		return nil
	}

	return writePlan(plan, o)
}

// printPlan lists what would be written, for -list and -dry-run.
//
// Building a plan touches nothing on disk, so these flags exercise the same code
// path as a real run rather than approximating it.
func printPlan(plan emit.Plan) {
	for _, f := range plan.Files {
		fmt.Fprintf(os.Stdout, "%-72s %6d bytes  sha256:%s\n", f.Path, len(f.Content), f.SHA256()[:12])
	}
	log.Printf("%d file(s) would be written; nothing was changed", len(plan.Files))
}

func writePlan(plan emit.Plan, o emitOptions) error {
	res, err := emit.Write(plan, emit.WriteOptions{Root: o.out, Force: o.force})
	if err != nil {
		return err
	}

	if o.wholeProvider() {
		// Orphans are found before the manifest is rewritten, because rewriting it
		// is what destroys the record of them. Emitting after renaming a resource
		// would otherwise leave the old files with nothing able to notice.
		remaining, err := handleOrphans(o.out, plan, o.clean)
		if err != nil {
			return err
		}

		// The manifest is written after the files, so a failed run does not leave
		// an inventory claiming output that was never produced.
		entries := manifestEntries(plan, o.blueprintPath)

		// Orphans that were reported but not deleted stay in the inventory.
		// Dropping them would make the advice this command just printed -- re-run
		// with -clean -- impossible to follow, because the second run would have
		// no record of what to delete.
		entries = append(entries, remaining...)

		if err := manifest.Save(o.out, manifest.New(version.Version, entries)); err != nil {
			return err
		}
	} else {
		log.Printf("note: -only was given, so the manifest was left unchanged")
	}

	for _, p := range res.Written {
		log.Printf("wrote     %s", p)
	}
	// Unchanged files are reported rather than silently skipped, so a run that
	// produced nothing new is visibly a no-op rather than ambiguously a failure.
	for _, p := range res.Unchanged {
		log.Printf("unchanged %s", p)
	}
	log.Printf("%d written, %d unchanged", len(res.Written), len(res.Unchanged))

	return nil
}

// handleOrphans reports files the blueprints no longer produce, and removes them
// when asked.
//
// Reporting rather than deleting by default: an orphan is usually a rename, but it
// might equally be a blueprint that failed to load a resource, and silently
// deleting a working resource's files on the strength of that would be worse than
// leaving them.
// handleOrphans returns the manifest entries for orphans that were reported but
// not deleted, so the caller can carry them forward.
func handleOrphans(root string, plan emit.Plan, clean bool) ([]manifest.Entry, error) {
	m, ok, err := manifest.Load(root)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	produced := make(map[string]bool, len(plan.Files))
	for _, f := range plan.Files {
		produced[filepath.ToSlash(f.Path)] = true
	}

	orphans, err := m.Orphans(root, produced)
	if err != nil {
		return nil, err
	}
	if len(orphans) == 0 {
		return nil, nil
	}

	if !clean {
		carried := make([]manifest.Entry, 0, len(orphans))
		for _, p := range orphans {
			log.Printf("orphaned %s", p)
			carried = append(carried, manifest.Entry{Path: p, Blueprint: "orphaned"})
		}
		log.Printf("%d file(s) are no longer produced by the blueprints. "+
			"Re-run with -clean to delete them, or remove them by hand.", len(orphans))
		return carried, nil
	}

	for _, p := range orphans {
		if err := os.Remove(filepath.Join(root, p)); err != nil {
			return nil, fmt.Errorf("removing orphaned %s: %w", p, err)
		}
		log.Printf("removed   %s", p)
	}

	return nil, nil
}

// manifestEntries records what the run produced, so a later run can tell which
// files it used to produce and no longer does.
func manifestEntries(plan emit.Plan, blueprintPath string) []manifest.Entry {
	out := make([]manifest.Entry, 0, len(plan.Files))
	for _, f := range plan.Files {
		out = append(out, manifest.Entry{
			Path:      filepath.ToSlash(f.Path),
			SHA256:    f.SHA256(),
			Blueprint: blueprintPath,
		})
	}
	return out
}
