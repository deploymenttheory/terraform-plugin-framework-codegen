// Package providergen orchestrates `provider generate` and `provider
// verify`: the verbs that turn the revised spec plus the generated SDK into
// the complete provider tree.
//
// The pipeline is fixed: load the revised document, derive the intermediate
// representation, draft SDK bindings and resolve them against the real SDK
// with go/types (prune, then verify), render the provider core and every
// entity, register the registrations into the registry files, and land the
// whole set through a staging directory so a failing run cannot leave a
// half-written tree. The manifest records every file under the empty
// origin — `provider generate`'s own, by the manifest package's convention —
// carrying the sdk-origin and authored entries forward untouched.
package providergen

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/emit"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/revise"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/version"
)

// Options is one `provider generate` or `provider verify` run.
type Options struct {
	// Config is the loaded tfpfgen.yaml.
	Config *config.Config
	// SpecDir holds the revised document (SpecDir/revised.yaml).
	SpecDir string
	// SDKDir is the committed SDK tree, relative to Root unless absolute.
	SDKDir string
	// Root is the provider repo root — where manifest.json lives, what
	// manifest paths are relative to, and where the generated tree lands.
	Root string
	// Postcheck holds the installed tree to `go mod tidy`, `go build` and
	// `go vet` after a generate. Skipped cleanly when the toolchain is
	// absent or the module proxy is unreachable.
	Postcheck bool
}

// Result reports one `provider generate` run.
type Result struct {
	// RevisedPath is the document the generation was taken from.
	RevisedPath string
	// Files is how many files the run produced.
	Files int
	// Root is where the tree landed.
	Root string
	// Resources, Datasources, ListResources and Actions count the entities
	// that survived pruning and were emitted.
	Resources, Datasources, ListResources, Actions int
	// Removals is the prune report: everything the SDK could not carry,
	// sorted, with the SDK's reason for each deletion.
	Removals []sdkbind.Removal
	// Excluded is every entity that yielded nothing, with the reason: the
	// classification and configuration exclusions the model already carried,
	// plus the shapes emission itself refused.
	Excluded []ir.UnsupportedEntity
	// Unsupported is everything unsupported.json records — the entity
	// exclusions above plus the attribute refusals, which surface nowhere
	// else.
	Unsupported []emit.Unsupported
	// Postcheck reports the toolchain gate.
	Postcheck PostcheckReport
}

// Run generates the provider tree: derive, bind, prune, verify, render,
// register, then land everything through a staging directory and record it in
// the manifest under the empty origin.
//
// The write to the repo is deliberately last: every failure mode — a missing
// input, an unresolvable binding, a template error — happens before the
// first byte moves, so the previous tree survives any error. The install
// itself is per-file renames from a staging directory beside the tree;
// unlike the SDK's single output directory, the provider tree spans the
// repo root, so there is no one directory to swap.
func Run(ctx context.Context, opts Options) (Result, error) {
	g, err := generate(opts)
	if err != nil {
		return Result{}, err
	}

	staging, err := os.MkdirTemp(opts.Root, ".tfpfgen-provider-staging-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(staging) //nolint:errcheck // best-effort: emptied by the install on success

	entries, err := emit.Write(staging, g.files)
	if err != nil {
		return Result{}, err
	}

	current, _, err := manifest.Load(opts.Root)
	if err != nil {
		return Result{}, err
	}

	// The authored contract is enforced here, not by convention: a manifest
	// that marks a produced path as authored refuses before the install
	// would overwrite it.
	paths := make([]string, len(entries))
	produced := make(map[string]bool, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
		produced[e.Path] = true
	}
	if refused := current.RefusesWrites(paths); len(refused) > 0 {
		return Result{}, fmt.Errorf("generation refuses to write authored paths: %s", strings.Join(refused, ", "))
	}

	unproduced, err := current.UnproducedFilesOf(opts.Root, "", produced)
	if err != nil {
		return Result{}, err
	}

	if err := install(staging, opts.Root, paths); err != nil {
		return Result{}, err
	}
	removeUnproducedFiles(opts.Root, unproduced)

	next := manifest.New(version.Version(), append(current.EntriesNotOf(""), entries...))
	if err := manifest.Save(opts.Root, next); err != nil {
		return Result{}, err
	}

	g.res.Files = len(entries)
	g.res.Postcheck, err = postcheck(ctx, opts.Root, opts.Postcheck)
	if err != nil {
		// The tree is already on disk — postcheck is the gate after the
		// install, not before it — so the result travels with the error. A
		// caller that discards it reports nothing about a run that wrote a
		// thousand files, and the exclusions explaining what is missing are
		// exactly what the operator needs when the build then fails.
		return g.res, err
	}

	// Postcheck's `go mod tidy` rewrites go.mod and writes go.sum after the
	// manifest recorded the emitted bytes; refreshing their entries makes
	// the manifest describe the committed truth, which is what `provider
	// verify` holds those files to.
	if err := recordPostcheckOwned(opts.Root); err != nil {
		return Result{}, err
	}
	return g.res, nil
}

// postcheckOwned are the paths the toolchain finalises after install:
// `go mod tidy` rewrites go.mod (the go directive, the indirect require
// closure) and writes go.sum, which no template emits. No regeneration
// reproduces them without running the toolchain, so the drift gate holds
// them to the manifest digests recorded here instead.
var postcheckOwned = []string{"go.mod", "go.sum"}

// recordPostcheckOwned re-records the toolchain-finalised files in the
// manifest, as they stand on disk after postcheck. Idempotent, and cheap
// enough to run whether or not postcheck did: a digest recomputed from
// unchanged bytes changes nothing.
func recordPostcheckOwned(root string) error {
	m, _, err := manifest.Load(root)
	if err != nil {
		return err
	}

	index := make(map[string]int, len(m.Files))
	for i, e := range m.Files {
		index[e.Path] = i
	}

	changed := false
	for _, path := range postcheckOwned {
		sum, there, err := fileDigest(root, path)
		if err != nil {
			return err
		}
		if !there {
			continue
		}
		if i, ok := index[path]; ok {
			if m.Files[i].SHA256 != sum {
				m.Files[i].SHA256 = sum
				changed = true
			}
			continue
		}
		m.Files = append(m.Files, manifest.Entry{
			Path:   path,
			SHA256: sum,
			Source: "go mod tidy",
			Origin: manifest.OriginPostcheck,
		})
		changed = true
	}
	if !changed {
		return nil
	}
	return manifest.Save(root, manifest.New(m.ToolVersion, m.Files))
}

// IR derives the intermediate representation for the given inputs and
// renders it as indented JSON — what `provider generate --print-ir` dumps.
// It reads the config and the revised document only: no SDK is required to
// look at the derivation.
func IR(opts Options) ([]byte, error) {
	_, model, err := loadModel(opts)
	if err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(model, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding the intermediate representation: %w", err)
	}
	return append(out, '\n'), nil
}

// generation is what the shared pipeline stages produce: the fully rendered
// file set — registry files registered — and the run report so far.
type generation struct {
	res   Result
	files []emit.File
}

// generate runs the stages `provider generate` and `provider verify` share:
// load the revised document, require the SDK, derive the intermediate
// representation, bind-prune-verify against the real SDK, render the
// provider core and the entities, and register the registrations. Nothing
// under Root is written; what becomes of the files is the caller's
// question — Run installs them, Verify compares and discards them.
func generate(opts Options) (*generation, error) {
	doc, model, err := loadModel(opts)
	if err != nil {
		return nil, err
	}

	sdkAbs, err := resolveSDK(opts)
	if err != nil {
		return nil, err
	}

	pc, err := emit.FromConfig(opts.Config, firstServerURL(doc))
	if err != nil {
		return nil, err
	}
	// What a probe run recorded of the API's accepted creates. Absent before
	// any run, which leaves every acceptance fixture derived from the
	// document as it was.
	pc.AcceptedRequestBodies, err = observe.ReadRequestBodies(filepath.Join(opts.Root, "audit", "request_bodies"))
	if err != nil {
		return nil, err
	}

	info, err := sdkbind.InfoFor(opts.Config, pc.SDKImport)
	if err != nil {
		return nil, err
	}
	binder, err := sdkbind.For(opts.Config)
	if err != nil {
		return nil, err
	}
	bindings, err := binder.Bind(model, info)
	if err != nil {
		return nil, err
	}

	loadDir, cleanup, err := bindContext(opts.Root, pc.Module, sdkAbs)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	removals, err := sdkbind.Prune(bindings, loadDir)
	if err != nil {
		return nil, err
	}
	rep, err := sdkbind.Verify(bindings, loadDir)
	if err != nil {
		return nil, err
	}
	// A binding the SDK cannot answer is a fact about one entity, not a
	// defect in the toolkit, so it must not stop the run: one entity naming
	// a read model the backend never emitted would otherwise take every
	// other entity in the document with it. The entity is dropped from the
	// bindings — which is exactly what emission already does with an entity
	// the bindings lack — and reported as excluded.
	excluded := bindings.DropProblems(rep.Problems)

	core, err := emit.RenderProviderCore(pc)
	if err != nil {
		return nil, err
	}
	entities, err := emit.RenderServices(pc, model, bindings)
	if err != nil {
		return nil, err
	}
	core, err = registerServices(core, entities.Registrations)
	if err != nil {
		return nil, err
	}

	// The refusal report is derived like everything else: it goes through
	// staging, the manifest and the drift gate, so a refusal that appears
	// or disappears is a line in the generation pull request rather than a
	// line in a CI log nobody reads.
	unsupported, refusals, err := emit.RenderUnsupported(model, removals, excluded, entities.Excluded, entities.KeptUnbound)
	if err != nil {
		return nil, err
	}

	g := &generation{
		res: Result{
			RevisedPath: filepath.Join(opts.SpecDir, revise.OutputName),
			Root:        opts.Root,
			// Counted from what was registered, not from what the bindings
			// held: an entity emission refused is bound but not emitted, and
			// reporting it as generated would be a lie the operator cannot see
			// through.
			Resources:     len(entities.Registrations.Resources.Registrations),
			Datasources:   len(entities.Registrations.Datasources.Registrations),
			ListResources: len(entities.Registrations.ListResources.Registrations),
			Actions:       len(entities.Registrations.Actions.Registrations),
			Removals:      removals,
			Excluded:      allExclusions(model.Excluded, excluded, entities.Excluded),
			Unsupported:   refusals,
		},
		files: append(append(core, entities.Files...), unsupported),
	}
	sort.Slice(g.files, func(i, j int) bool { return g.files[i].Path < g.files[j].Path })
	return g, nil
}

// loadModel reads the revised document and derives the intermediate
// representation from it and the config.
func loadModel(opts Options) (*specmodel.Document, *ir.Model, error) {
	revisedPath := filepath.Join(opts.SpecDir, revise.OutputName)
	data, err := os.ReadFile(revisedPath) //nolint:gosec // the fixed name under the operator-supplied dir
	if os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("%s does not exist; generation reads the revised spec — run `tfpfgen spec revise` first", revisedPath)
	}
	if err != nil {
		return nil, nil, err
	}

	doc, err := specmodel.Load(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", revisedPath, err)
	}
	model, err := ir.Derive(doc, opts.Config)
	if err != nil {
		return nil, nil, err
	}
	return doc, model, nil
}

// resolveSDK renders the SDK directory absolute — relative to Root unless
// absolute — and requires the tree to exist, because binding resolves
// against real type information, not against hope.
func resolveSDK(opts Options) (string, error) {
	dir := opts.SDKDir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(opts.Root, dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		return "", fmt.Errorf("%s does not exist; generation binds against the generated SDK — run `tfpfgen sdk generate` first", abs)
	} else if err != nil {
		return "", err
	}
	return abs, nil
}

// firstServerURL is the document's default endpoint: the first declared
// server, empty when the document declares none.
func firstServerURL(doc *specmodel.Document) string {
	if len(doc.Servers) == 0 {
		return ""
	}
	return doc.Servers[0].URL
}

// registryFiles maps each sentinel kind to the provider-core registry file
// carrying its sentinels.
var registryFiles = map[string]string{
	"resources":      "internal/provider/resources.go",
	"datasources":    "internal/provider/datasources.go",
	"list_resources": "internal/provider/list_resources.go",
	"actions":        "internal/provider/actions.go",
}

// registerServices replaces the machine-maintained lines in every registry
// file with the rendered registrations, before anything is written.
func registerServices(core []emit.File, regs emit.Registry) ([]emit.File, error) {
	byPath := map[string]int{}
	for i, f := range core {
		byPath[f.Path] = i
	}

	for _, kind := range emit.RegistrySlots {
		path := registryFiles[kind]
		i, ok := byPath[path]
		if !ok {
			return nil, fmt.Errorf("the provider core rendered no %s to register %s registrations into", path, kind)
		}
		set, err := regs.ByKind(kind)
		if err != nil {
			return nil, err
		}
		registered, err := emit.Register(core[i].Content, kind, set)
		if err != nil {
			return nil, fmt.Errorf("registering %s: %w", path, err)
		}
		core[i].Content = registered
	}
	return core, nil
}

// install moves every staged file into place. All rendering and validation
// is already spent by the time this runs, so the only failures left are the
// filesystem's own; a rename within one filesystem cannot half-write a file.
func install(staging, root string, paths []string) error {
	for _, p := range paths {
		source := filepath.Join(staging, filepath.FromSlash(p))
		destination := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return fmt.Errorf("creating the directory for %s: %w", p, err)
		}
		if err := os.Rename(source, destination); err != nil {
			return fmt.Errorf("installing %s: %w", p, err)
		}
	}
	return nil
}

// removeUnproducedFiles deletes the files a previous run produced that this one no
// longer does, then sweeps any directories the deletions emptied. Both are
// best-effort: a file that will not delete is left for `provider verify`
// to report, not a reason to fail a run that already installed correctly.
func removeUnproducedFiles(root string, paths []string) {
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.Remove(full); err != nil {
			continue
		}
		for dir := filepath.Dir(full); dir != filepath.Clean(root); dir = filepath.Dir(dir) {
			if os.Remove(dir) != nil {
				break // non-empty or gone; either way the sweep is done
			}
		}
	}
}

// allExclusions gathers every reason an entity yielded nothing, in pipeline
// order: refused by classification or configuration, then dropped because the
// SDK could not answer its binding, then refused by emission.
func allExclusions(derived []ir.UnsupportedEntity, dropped []sdkbind.Dropped, refused []ir.UnsupportedEntity) []ir.UnsupportedEntity {
	out := make([]ir.UnsupportedEntity, 0, len(derived)+len(dropped)+len(refused))
	out = append(out, derived...)
	for _, d := range dropped {
		out = append(out, ir.UnsupportedEntity{Key: d.Key, Reason: d.Reason})
	}
	return append(out, refused...)
}
