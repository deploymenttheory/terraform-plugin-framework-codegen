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

func runEmit(args []string) error {
	fs, _ := newFlagSet("emit", "emit -blueprint DIR -out DIR [-only NAME] [-dry-run]")

	var (
		blueprintPath = fs.String("blueprint", "", "blueprint file or directory (required)")
		out           = fs.String("out", "", "provider root to write into (required)")
		only          = fs.String("only", "", "generate a single resource by key, for inspecting output")
		dryRun        = fs.Bool("dry-run", false, "print the write plan and touch nothing")
		list          = fs.Bool("list", false, "list the files that would be written and exit")
		force         = fs.Bool("force", false, "overwrite files that are not marked as generated")
		clean         = fs.Bool("clean", false, "delete files the blueprints no longer produce")
	)

	if err := parse(fs, args); err != nil {
		return err
	}

	if *blueprintPath == "" {
		return usagef("-blueprint is required")
	}
	if *out == "" && !*list && !*dryRun {
		return usagef("-out is required unless -list or -dry-run is given")
	}

	bp, err := blueprint.LoadDir(*blueprintPath)
	if err != nil {
		return err
	}

	log.Printf("blueprint: %s (%d resource(s), %d data source(s))",
		*blueprintPath, len(bp.Resources), len(bp.DataSources))

	gen, err := emit.New()
	if err != nil {
		return err
	}

	plan, err := gen.Build(bp, emit.Options{BlueprintPath: *blueprintPath, Only: *only})
	if err != nil {
		return err
	}

	if len(plan.Files) == 0 {
		return fmt.Errorf("%w: nothing matched", errNothingToDo)
	}

	if *list || *dryRun {
		for _, f := range plan.Files {
			fmt.Fprintf(os.Stdout, "%-72s %6d bytes  sha256:%s\n", f.Path, len(f.Content), f.SHA256()[:12])
		}
		log.Printf("%d file(s) would be written; nothing was changed", len(plan.Files))
		return nil
	}

	res, err := emit.Write(plan, emit.WriteOptions{Root: *out, Force: *force})
	if err != nil {
		return err
	}

	// Orphans are found before the manifest is rewritten, because rewriting it is
	// what destroys the record of them. Emitting after renaming a resource would
	// otherwise leave the old files on disk with nothing able to notice.
	if *only == "" {
		if err := handleOrphans(*out, plan, *clean); err != nil {
			return err
		}
	}

	// The manifest is written after the files, so a failed run does not leave an
	// inventory claiming output that was never produced.
	//
	// It is skipped under -only: a partial run would record a partial inventory,
	// and verify would then report every unlisted file as an orphan.
	if *only == "" {
		if err := manifest.Save(*out, manifest.New(version.Version, manifestEntries(plan, *blueprintPath))); err != nil {
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
func handleOrphans(root string, plan emit.Plan, clean bool) error {
	m, ok, err := manifest.Load(root)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	produced := make(map[string]bool, len(plan.Files))
	for _, f := range plan.Files {
		produced[filepath.ToSlash(f.Path)] = true
	}

	orphans, err := m.Orphans(root, produced)
	if err != nil {
		return err
	}
	if len(orphans) == 0 {
		return nil
	}

	if !clean {
		for _, p := range orphans {
			log.Printf("orphaned %s", p)
		}
		log.Printf("%d file(s) are no longer produced by the blueprints. "+
			"Re-run with -clean to delete them, or remove them by hand.", len(orphans))
		return nil
	}

	for _, p := range orphans {
		if err := os.Remove(filepath.Join(root, p)); err != nil {
			return fmt.Errorf("removing orphaned %s: %w", p, err)
		}
		log.Printf("removed   %s", p)
	}

	return nil
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
