package main

import (
	"fmt"
	"log"
	"os"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/emit"
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
