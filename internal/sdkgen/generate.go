package sdkgen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/revise"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/version"
)

// Options is one `sdk generate` run.
type Options struct {
	// Config is the loaded tfpfgen.yaml.
	Config *config.Config
	// SpecDir holds the revised document (SpecDir/revised.yaml).
	SpecDir string
	// Out is where the SDK tree lands, relative to Root unless absolute.
	Out string
	// Root is the provider repo root — where manifest.json lives and what
	// manifest paths are relative to.
	Root string
}

// Result reports one run.
type Result struct {
	// Backend and Version name the tool that generated the tree.
	Backend, Version string
	// RevisedPath is the document the generation was taken from.
	RevisedPath string
	// Files is how many files the run produced.
	Files int
	// OutDir is where the tree landed.
	OutDir string
}

// Run generates the SDK tree: gate the pinned tool, pre-normalize the
// revised document into a temp copy, generate into a staging directory, scrub
// nondeterminism, swap the staging tree into place, and record every file in
// the manifest under the sdk origin — carrying every other origin's entries
// forward untouched.
//
// Generation is staged so a failing run cannot leave a half-written tree
// where a complete one stood: the previous output survives any error, and
// the swap at the end is two renames.
func Run(ctx context.Context, opts Options) (Result, error) {
	outAbs, err := resolveOut(opts)
	if err != nil {
		return Result{}, err
	}

	// Staging lives beside the final tree so the swap is a rename on one
	// filesystem, not a copy that can half-finish.
	res, staging, err := regenerate(ctx, opts, filepath.Dir(outAbs), ".tfpfgen-sdk-staging-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(staging) //nolint:errcheck // best-effort: gone already after a successful swap

	entries, err := inventory(staging, opts.Root, outAbs, recordedPath(opts.Root, res.RevisedPath))
	if err != nil {
		return Result{}, err
	}
	res.Files = len(entries)
	res.OutDir = outAbs

	current, _, err := manifest.Load(opts.Root)
	if err != nil {
		return Result{}, err
	}

	// The authored contract is enforced here, not by convention: a tree whose
	// manifest marks a path under Out as authored refuses before the swap
	// would overwrite it.
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	if refused := current.RefusesWrites(paths); len(refused) > 0 {
		return Result{}, fmt.Errorf("generation refuses to write authored paths: %s", strings.Join(refused, ", "))
	}

	if err := swap(staging, outAbs); err != nil {
		return Result{}, err
	}

	next := manifest.New(version.Version(), append(current.EntriesNotOf(manifest.OriginSDK), entries...))
	if err := manifest.Save(opts.Root, next); err != nil {
		return Result{}, err
	}

	return res, nil
}

// resolveOut renders opts.Out absolute: relative to Root unless already
// absolute.
func resolveOut(opts Options) (string, error) {
	out := opts.Out
	if !filepath.IsAbs(out) {
		out = filepath.Join(opts.Root, opts.Out)
	}
	return filepath.Abs(out)
}

// regenerate runs the pipeline stages `sdk generate` and `sdk verify` share —
// select the backend, read the revised document, gate the pinned tool,
// pre-normalize a copy, generate into a fresh temporary directory, scrub
// nondeterminism — and returns that directory. What becomes of the tree is
// the caller's question: Run swaps it into place, Verify compares and
// discards it.
//
// The temporary target is created under targetParent ("" means the system
// temp directory), and only after every gate has passed, so a refusal leaves
// no mark near the repo. The caller removes the returned directory.
func regenerate(ctx context.Context, opts Options, targetParent, targetPrefix string) (Result, string, error) {
	backend, err := For(opts.Config)
	if err != nil {
		return Result{}, "", err
	}

	res := Result{
		Backend:     backend.Name(),
		Version:     backend.RequiredVersion(opts.Config),
		RevisedPath: filepath.Join(opts.SpecDir, revise.OutputName),
	}

	revised, err := os.ReadFile(res.RevisedPath) //nolint:gosec // the fixed name under the operator-supplied dir
	if os.IsNotExist(err) {
		return Result{}, "", fmt.Errorf("%s does not exist; generation reads the revised spec — run `tfpfgen spec revise` first", res.RevisedPath)
	}
	if err != nil {
		return Result{}, "", err
	}

	if err := backend.CheckTool(ctx, opts.Config); err != nil {
		return Result{}, "", err
	}

	prenormalized, _, _, err := Prenormalize(revised)
	if err != nil {
		return Result{}, "", err
	}

	// The pre-normalized copy is an intermediate, not an output: it lives in
	// a temp directory the run removes, and only its durable source
	// (spec/revised.yaml) is ever recorded in generated files.
	workDir, err := os.MkdirTemp("", "tfpfgen-sdkgen-")
	if err != nil {
		return Result{}, "", err
	}
	defer os.RemoveAll(workDir) //nolint:errcheck // best-effort temp cleanup

	prePath := filepath.Join(workDir, "revised.prenormalized.yaml")
	if err := os.WriteFile(prePath, prenormalized, 0o600); err != nil {
		return Result{}, "", err
	}

	if targetParent != "" {
		if err := os.MkdirAll(targetParent, 0o750); err != nil {
			return Result{}, "", err
		}
	}
	target, err := os.MkdirTemp(targetParent, targetPrefix)
	if err != nil {
		return Result{}, "", err
	}

	if err := backend.Generate(ctx, prePath, opts.Config, target); err != nil {
		os.RemoveAll(target) //nolint:errcheck // best-effort temp cleanup; the generate error is the report
		return Result{}, "", err
	}
	if err := backend.Normalize(target, recordedPath(opts.Root, res.RevisedPath)); err != nil {
		os.RemoveAll(target) //nolint:errcheck // best-effort temp cleanup; the normalize error is the report
		return Result{}, "", err
	}

	return res, target, nil
}

// recordedPath renders the revised document's path the way durable output
// records it: relative to the repo root, forward slashes. A path outside the
// root is recorded as given — still durable, still not a temp file.
func recordedPath(root, revisedPath string) string {
	absRoot, err1 := filepath.Abs(root)
	absRevised, err2 := filepath.Abs(revisedPath)
	if err1 == nil && err2 == nil {
		if rel, err := filepath.Rel(absRoot, absRevised); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(revisedPath)
}

// inventory walks the staged tree and builds its manifest entries, with
// paths as they will read after the swap: relative to root, forward slashes.
func inventory(staging, root, outAbs, source string) ([]manifest.Entry, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	relOut, err := filepath.Rel(absRoot, outAbs)
	if err != nil || strings.HasPrefix(relOut, "..") {
		return nil, fmt.Errorf("the output directory %s is outside the repo root %s; the manifest cannot record it", outAbs, root)
	}

	var entries []manifest.Entry
	err = filepath.WalkDir(staging, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // walking the tree this run wrote
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		rel, err := filepath.Rel(staging, path)
		if err != nil {
			return err
		}
		entries = append(entries, manifest.Entry{
			Path:   filepath.ToSlash(filepath.Join(relOut, rel)),
			SHA256: hex.EncodeToString(sum[:]),
			Source: source,
			Origin: manifest.OriginSDK,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("the backend reported success but generated no files — nothing to install into %s", outAbs)
	}
	return entries, nil
}

// swap replaces out with the staged tree. The old tree is parked first so a
// failed rename restores it rather than leaving nothing.
func swap(staging, out string) error {
	parked := out + ".tfpfgen-previous"
	if err := os.RemoveAll(parked); err != nil {
		return err
	}

	hadPrevious := false
	if _, err := os.Stat(out); err == nil {
		hadPrevious = true
		if err := os.Rename(out, parked); err != nil {
			return err
		}
	}

	if err := os.Rename(staging, out); err != nil {
		if hadPrevious {
			_ = os.Rename(parked, out) //nolint:errcheck // restoring on the error path; the rename error is the report
		}
		return fmt.Errorf("installing the generated tree into %s: %w", out, err)
	}

	return os.RemoveAll(parked)
}
