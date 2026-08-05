package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/docpatch"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/kiota"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/snapshot"
)

const usageSDKGenerate = "sdk generate [-openapi-dir DIR] [-snapshot NAME] -out DIR " +
	"[-mode embed|external] [-module PATH] [-client-name NAME] [-check] [-dry-run]"

// SDK modes. Embed is the default: one module, one drift check, no publishing
// loop between the SDK and the provider that consumes it.
const (
	sdkModeEmbed    = "embed"
	sdkModeExternal = "external"
)

// sdkVerbs is the sdk group's verb table.
var sdkVerbs = []command{
	{
		name:    "generate",
		summary: "generate a Go SDK from a pinned OpenAPI snapshot with kiota; -check fails on drift",
		usage:   usageSDKGenerate,
		run:     runSDKGenerate,
	},
}

func runSDK(args []string) error {
	return runVerbs("sdk", sdkVerbs, "", args)
}

type sdkGenerateOptions struct {
	openapiDir   string
	snapshotName string
	out          string
	mode         string
	module       string
	clientName   string
	include      string
	exclude      string
	clean        bool
	check        bool
	dryRun       bool
}

func runSDKGenerate(args []string) error {
	fs, _ := newFlagSet("sdk generate", usageSDKGenerate)

	var o sdkGenerateOptions
	fs.StringVar(&o.openapiDir, "openapi-dir", "openapi/thousandeyes", "directory holding pinned OpenAPI snapshots")
	fs.StringVar(&o.snapshotName, "snapshot", "", "snapshot to read (default: the newest)")
	fs.StringVar(&o.out, "out", "", "SDK root to write into (required)")
	fs.StringVar(&o.mode, "mode", sdkModeEmbed,
		"embed generates under the enclosing module; external emits a standalone tree with its own go.mod")
	fs.StringVar(&o.module, "module", "",
		"Go import path of the SDK root; derived from the enclosing go.mod under embed, required under external")
	fs.StringVar(&o.clientName, "client-name", "ApiClient", "root client type name")
	fs.StringVar(&o.include, "include", "", "restrict to these API path globs, e.g. /tags/**; comma-separate several")
	fs.StringVar(&o.exclude, "exclude", "", "drop these API path globs; comma-separate several")
	fs.BoolVar(&o.clean, "clean", false, "delete files a previous generation produced that this one does not")
	fs.BoolVar(&o.check, "check", false,
		"write nothing and exit 1 if the committed SDK has drifted from the snapshot or the pinned kiota version")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the resolved snapshot and invocation, run nothing")

	if err := parse(fs, args); err != nil {
		return err
	}

	if o.out == "" {
		return usagef("-out is required: it names the SDK root")
	}
	switch o.mode {
	case sdkModeEmbed, sdkModeExternal:
	default:
		return usagef("-mode %q is not embed or external", o.mode)
	}
	if o.mode == sdkModeExternal && o.module == "" {
		return usagef("-module is required under -mode external: it becomes the SDK's own module path")
	}

	snap, err := snapshot.Find(o.openapiDir, o.snapshotName)
	if err != nil {
		return err
	}
	// A snapshot is meant to be immutable; generating an SDK from an edited one
	// would make the committed tree unreproducible from anything committed.
	if err := snap.Verify(); err != nil {
		return err
	}

	// Document patches: curated, recording-justified corrections applied to a copy
	// of the snapshot when the published document is provably wrong about the
	// live API. The snapshot's own bytes never change; generation reads the
	// patched copy, and with no patches present the snapshot is read directly.
	patches, err := docpatch.Load(filepath.Join(o.openapiDir, docpatch.DirName))
	if err != nil {
		return err
	}

	module := o.module
	if module == "" {
		module, err = embedModulePath(o.out)
		if err != nil {
			return err
		}
	}

	gen := kiota.GenerateOptions{
		Description: snap.SpecPath(),
		Out:         o.out,
		Module:      module,
		ClientName:  o.clientName,
		Include:     splitGlobs(o.include),
		Exclude:     splitGlobs(o.exclude),
		Clean:       o.clean,
	}

	if o.dryRun {
		log.Printf("snapshot: %s", snap.SpecPath())
		for _, p := range patches {
			log.Printf("would apply %s: %s", p.File, p.Justification)
		}
		log.Printf("would run: kiota generate -l go -d %s -o %s -n %s -c %s --exclude-backward-compatible",
			gen.Description, gen.Out, gen.Module, gen.ClientName)
		log.Printf("dry run; nothing was generated")
		return nil
	}

	if len(patches) > 0 {
		patched, err := patchedDocument(snap, patches)
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(filepath.Dir(patched)) }()
		gen.Description = patched
		log.Printf("applied %d document patch(es) from %s", len(patches), filepath.Join(o.openapiDir, docpatch.DirName))
	}

	// The version gate: a committed tree names the kiota that produced it, and
	// running any other version rewrites the whole tree as an inexplicable diff.
	lock, hadLock, err := kiota.ReadLock(o.out)
	if err != nil {
		return err
	}
	if hadLock {
		if err := kiota.Gate(lock); err != nil {
			return err
		}
	} else if _, err := kiota.BinaryVersion(); err != nil {
		return err
	}

	if o.check {
		if !hadLock {
			return fmt.Errorf("%s has no %s to check against; generate the SDK first", o.out, kiota.LockFileName)
		}
		return checkSDKDrift(gen, o.out)
	}

	log.Printf("generating %s from %s", o.out, snap.SpecPath())
	if err := kiota.Generate(gen); err != nil {
		return err
	}

	// A patched generation read a temporary copy, and a temp path must not
	// reach the committed lock: point descriptionLocation back at the pinned
	// snapshot the patched copy was derived from.
	if gen.Description != snap.SpecPath() {
		rel, err := relDescription(o.out, snap.SpecPath())
		if err != nil {
			return err
		}
		if err := kiota.SetDescriptionLocation(o.out, rel); err != nil {
			return err
		}
	}

	if o.mode == sdkModeExternal {
		if err := ensureSDKModule(o.out, module); err != nil {
			return err
		}
	}

	return sdkPostcheck(o.out)
}

// patchedDocument applies the loaded patches to the snapshot's document and
// writes the result into a fresh temp directory, returning the file path.
func patchedDocument(snap snapshot.Snapshot, patches []docpatch.Patch) (string, error) {
	raw, err := os.ReadFile(snap.SpecPath()) //nolint:gosec // the verified snapshot's own path
	if err != nil {
		return "", err
	}
	patched, err := docpatch.Apply(raw, patches)
	if err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "tfpfgen-patched-spec-*")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "api.yaml")
	if err := os.WriteFile(path, patched, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// relDescription is the lock-file spelling of the snapshot's location: the
// same relative-to-the-SDK-root form kiota itself writes.
func relDescription(out, spec string) (string, error) {
	absOut, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}
	absSpec, err := filepath.Abs(spec)
	if err != nil {
		return "", err
	}
	return filepath.Rel(absOut, absSpec)
}

// embedModulePath derives kiota's -n for an embedded SDK: the enclosing
// module's path joined with the SDK root's relative directory -- kiota's own
// rule for generating inside an existing module.
func embedModulePath(out string) (string, error) {
	abs, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}

	for dir := abs; ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod")) //nolint:gosec // fixed name on an ancestor walk
		if err == nil {
			module := modulePathOf(data)
			if module == "" {
				return "", fmt.Errorf("%s/go.mod declares no module path", dir)
			}
			rel, err := filepath.Rel(dir, abs)
			if err != nil {
				return "", err
			}
			if rel == "." {
				return module, nil
			}
			return module + "/" + filepath.ToSlash(rel), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if parent := filepath.Dir(dir); parent == dir {
			return "", usagef("-out %s is not inside a Go module and -mode is embed; "+
				"create the provider module first, or pass -mode external with -module", out)
		}
	}
}

// modulePathOf reads the module directive out of go.mod bytes.
func modulePathOf(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func splitGlobs(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, g := range strings.Split(s, ",") {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// checkSDKDrift regenerates into scratch and diffs against the committed tree.
//
// Byte comparison rather than lock comparison, deliberately: the lock proves
// which inputs produced a tree, but only regeneration proves the tree still
// follows from the pinned snapshot -- including after a kiota version bump
// that slipped past review, or a hand edit to a generated file.
func checkSDKDrift(gen kiota.GenerateOptions, committed string) error {
	scratch, err := os.MkdirTemp("", "tfpfgen-sdk-check-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	gen.Out = scratch
	gen.Clean = false
	if err := kiota.Generate(gen); err != nil {
		return err
	}

	drifted, err := diffTrees(scratch, committed)
	if err != nil {
		return err
	}
	if len(drifted) == 0 {
		log.Printf("✅ %s matches a fresh generation from the pinned snapshot", committed)
		return nil
	}

	for _, p := range drifted {
		fmt.Fprintf(os.Stderr, "  drifted %s\n", p)
	}
	fmt.Fprintf(os.Stderr, "::error::the committed SDK is out of date; run sdk generate and commit the result\n")
	return fmt.Errorf("%d file(s) differ from a fresh generation", len(drifted))
}

// diffTrees compares fresh against committed, ignoring the committed tree's
// go.mod/go.sum (creation-time scaffold kiota does not produce) and anything
// that is not a regular file.
func diffTrees(fresh, committed string) ([]string, error) {
	var drifted []string

	seen := map[string]bool{}
	err := filepath.Walk(fresh, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(fresh, path)
		if err != nil {
			return err
		}
		seen[rel] = true

		want, err := os.ReadFile(path) //nolint:gosec // scratch tree this run wrote
		if err != nil {
			return err
		}
		have, err := os.ReadFile(filepath.Join(committed, rel)) //nolint:gosec // operator-supplied root
		switch {
		case os.IsNotExist(err):
			drifted = append(drifted, rel+" (missing)")
		case err != nil:
			return err
		case rel == kiota.LockFileName:
			// The lock records the description's path relative to the output
			// directory, so a scratch regeneration always differs on that one
			// field; every other field must still match byte-for-byte.
			if !lockEquivalent(want, have) {
				drifted = append(drifted, rel)
			}
		case !bytes.Equal(want, have):
			drifted = append(drifted, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Files the committed tree carries that a fresh generation does not.
	err = filepath.Walk(committed, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(committed, path)
		if relErr != nil {
			return relErr
		}
		if seen[rel] || rel == "go.mod" || rel == "go.sum" {
			return nil
		}
		drifted = append(drifted, rel+" (orphaned)")
		return nil
	})
	return drifted, err
}

// lockEquivalent compares two kiota lock files ignoring descriptionLocation,
// the one field that legitimately depends on where the output directory sits.
func lockEquivalent(a, b []byte) bool {
	var la, lb map[string]any
	if json.Unmarshal(a, &la) != nil || json.Unmarshal(b, &lb) != nil {
		return bytes.Equal(a, b)
	}
	delete(la, "descriptionLocation")
	delete(lb, "descriptionLocation")
	ja, errA := json.Marshal(la)
	jb, errB := json.Marshal(lb)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// ensureSDKModule writes a minimal go.mod for an external SDK tree and lets
// `go mod tidy` compute the requirements -- a creation step, run once; the
// file is the operator's afterwards, exactly like a scaffold.
func ensureSDKModule(out, module string) error {
	modPath := filepath.Join(out, "go.mod")
	if _, err := os.Stat(modPath); err == nil {
		return nil
	}

	goVersion, err := toolchainGoVersion()
	if err != nil {
		return err
	}

	content := fmt.Sprintf("module %s\n\ngo %s\n", module, goVersion)
	if err := os.WriteFile(modPath, []byte(content), 0o600); err != nil {
		return err
	}

	log.Printf("go mod tidy (resolving the kiota runtime modules)")
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = out
	if msg, err := tidy.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod tidy in %s: %v\n%s", out, err, msg)
	}
	return nil
}

// toolchainGoVersion reads the language version out of the nearest enclosing
// go.mod, so the SDK module never claims a newer Go than the tree it is
// generated from builds with.
func toolchainGoVersion() (string, error) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	for ; ; dir = filepath.Dir(dir) {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod")) //nolint:gosec // fixed name on an ancestor walk
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "go "); ok {
					return strings.TrimSpace(rest), nil
				}
			}
			return "", fmt.Errorf("%s/go.mod carries no go directive", dir)
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if parent := filepath.Dir(dir); parent == dir {
			return "", fmt.Errorf("no go.mod found above the working directory for the go directive")
		}
	}
}

// sdkPostcheck compiles the generated tree; nothing else matters if it does
// not build.
func sdkPostcheck(out string) error {
	log.Printf("postcheck: go build ./...")
	build := exec.Command("go", "build", "./...")
	build.Dir = out
	if msg, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("postcheck: the generated SDK does not compile:\n%s", msg)
	}
	log.Printf("✅ SDK generated at %s", out)
	return nil
}
