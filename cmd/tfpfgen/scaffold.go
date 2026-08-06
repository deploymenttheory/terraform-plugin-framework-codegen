package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/generate"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/version"
)

const usageProviderScaffold = "provider scaffold -blueprint DIR -out DIR [-check]"

// runProviderScaffold is `provider scaffold`: emit the provider shell -- the
// support packages generated code calls into -- from the embedded templates,
// parameterised by the provider block alone.
//
// It is the counterpart of `provider generate`, and the two share the output
// manifest: scaffold records its files under its own origin, so each verb's
// drift check polices its own inventory without reporting the other's files as
// orphans.
func runProviderScaffold(args []string) error {
	fs, _ := newFlagSet("provider scaffold", usageProviderScaffold)

	var (
		blueprintDir string
		out          string
		check        bool
	)
	fs.StringVar(&blueprintDir, "blueprint", "",
		"blueprint directory holding provider.blueprint.json (required)")
	fs.StringVar(&out, "out", "", "provider root to write into (required)")
	fs.BoolVar(&check, "check", false,
		"write nothing and exit 1 if the committed shell has drifted from the templates")

	if err := parse(fs, args); err != nil {
		return err
	}

	if blueprintDir == "" {
		return usagef("-blueprint is required")
	}
	if out == "" {
		return usagef("-out is required")
	}

	// Only the provider block is loaded, not the whole blueprint directory: the
	// shell depends on nothing a resource declares, and loading less is what
	// keeps a resource edit from rewriting every shell file's header digest.
	blockPath := filepath.Join(blueprintDir, "provider.blueprint.json")

	bp, err := blueprint.Load(blockPath)
	if err != nil {
		return err
	}
	if bp.Provider.Name == "" {
		return fmt.Errorf("%s carries no provider block", blockPath)
	}

	params, err := generate.ShellParamsFrom(bp.Provider)
	if err != nil {
		return err
	}

	plan, err := generate.BuildShell(
		params, filepath.ToSlash(blockPath), generate.ProviderBlockDigest(bp),
	)
	if err != nil {
		return err
	}
	if len(plan.Files) == 0 {
		return fmt.Errorf("%w: no shell templates found", errNothingToDo)
	}

	if check {
		return runScaffoldCheck(plan, blueprintDir, out)
	}

	// Force, always: the shell files exist first as a provider's hand-written
	// originals, carrying no generated marker, and adopting them is the whole
	// point of this verb. `provider generate` keeps its refusal semantics --
	// only the scaffold transition is exempted.
	res, err := generate.Write(plan, generate.WriteOptions{Root: out, Force: true})
	if err != nil {
		return err
	}

	if err := saveScaffoldManifest(plan, blockPath, out); err != nil {
		return err
	}

	for _, p := range res.Written {
		log.Printf("wrote     %s", p)
	}
	for _, p := range res.Unchanged {
		log.Printf("unchanged %s", p)
	}
	log.Printf("%d written, %d unchanged", len(res.Written), len(res.Unchanged))

	return nil
}

// saveScaffoldManifest replaces the manifest's scaffold inventory, carrying
// every other verb's entries forward unchanged.
//
// A prior scaffold entry the templates no longer produce is reported and kept,
// the same treatment `provider generate` gives its orphans: a file with a
// generated header that nothing regenerates should stay visible to the drift
// check until somebody removes it.
func saveScaffoldManifest(plan generate.Fileset, blockPath, out string) error {
	entries := make([]manifest.Entry, 0, len(plan.Files))
	produced := make(map[string]bool, len(plan.Files))

	for _, f := range plan.Files {
		p := filepath.ToSlash(f.Path)
		produced[p] = true
		entries = append(entries, manifest.Entry{
			Path:      p,
			SHA256:    f.SHA256(),
			Blueprint: filepath.ToSlash(blockPath),
			Origin:    manifest.OriginScaffold,
		})
	}

	prior, havePrior, err := manifest.Load(out)
	if err != nil {
		return err
	}
	if havePrior {
		orphans, err := prior.OrphansOf(out, manifest.OriginScaffold, produced)
		if err != nil {
			return err
		}
		for _, p := range orphans {
			log.Printf("orphaned  %s (no longer produced by the shell templates; remove it by hand)", p)
			entries = append(entries, manifest.Entry{
				Path: p, Blueprint: "orphaned", Origin: manifest.OriginScaffold,
			})
		}

		entries = append(entries, prior.EntriesNotOf(manifest.OriginScaffold)...)
	}

	return manifest.Save(out, manifest.New(version.Version, entries))
}

// runScaffoldCheck is `provider scaffold -check`: compare the shell the
// templates produce against the committed tree and fail on drift, writing
// nothing. The failure classes mirror `provider generate -check`'s.
func runScaffoldCheck(plan generate.Fileset, blueprintDir, out string) error {
	res, err := compareAgainstDisk(plan, out, manifest.OriginScaffold)
	if err != nil {
		return err
	}

	if res.clean() {
		if !res.checkedOrphans {
			log.Printf("✅ %d shell file(s) match the templates "+
				"(no manifest present, so orphans were not checked)", len(plan.Files))
			return nil
		}
		log.Printf("✅ %d shell file(s) match the templates, with no orphans", len(plan.Files))
		return nil
	}

	for _, p := range res.drifted {
		fmt.Fprintf(os.Stderr, "drifted  %s\n", p)
	}
	for _, p := range res.missing {
		fmt.Fprintf(os.Stderr, "missing  %s\n", p)
	}
	for _, p := range res.orphaned {
		fmt.Fprintf(os.Stderr, "orphaned %s\n", p)
	}
	fmt.Fprintf(os.Stderr,
		"::error::The provider shell is out of date. Run: tfpfgen provider scaffold -blueprint %s -out %s\n",
		blueprintDir, out)

	return &driftError{n: res.count()}
}
