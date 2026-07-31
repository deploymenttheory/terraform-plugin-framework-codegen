// Package emit renders a blueprint into a provider's source tree.
//
// It owns three things beyond template execution: formatting the output the way
// the target repository's formatter would, so that emitted code is a fixed point
// and does not show up as drift the first time anyone opens it; refusing to
// overwrite a file it does not own; and producing a plan that can be inspected
// without writing anything.
package emit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"mvdan.cc/gofumpt/format"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/render"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/templates"
)

// generatedMarker is the substring that identifies a file this tool owns.
//
// Overwrite protection keys on it, so a file without it is never clobbered even
// if a stale manifest claims it.
const generatedMarker = "DO NOT EDIT."

// langVersion is the Go language version gofumpt formats for.
//
// It must carry the "go" prefix: gofumpt parses it with go/version and *panics*
// on a value it cannot read, so a plain "1.25" crashes the generator rather than
// returning an error. TestUnit_Emit_LangVersionIsAccepted pins it.
const langVersion = "go1.25"

var (
	// ErrRefusedOverwrite reports a target file the emitter does not own.
	ErrRefusedOverwrite = errors.New("refusing to overwrite a file that is not generated")
	// ErrFormat reports output that is not valid Go.
	ErrFormat = errors.New("generated output is not valid Go")
)

// File is one emitted file.
type File struct {
	// Path is repo-relative to the output root.
	Path string
	// Content is the formatted bytes.
	Content []byte

	// Scaffold marks a file the generator writes once and then never again.
	//
	// It is the escape hatch made mechanical. A scaffold carries no generated marker, so it is
	// not recorded in the manifest and the drift check does not police it: once it exists it
	// belongs to whoever edits it, and a later emit leaves it exactly as found.
	Scaffold bool
}

// SHA256 returns the content digest, which the manifest records so drift can be
// detected without keeping a copy of the output.
func (f File) SHA256() string {
	sum := sha256.Sum256(f.Content)
	return hex.EncodeToString(sum[:])
}

// Plan is the full set of files a blueprint produces.
//
// Building a plan touches nothing on disk. Writing is a separate step, so
// -dry-run is the same code path as a real run rather than an approximation of it.
type Plan struct {
	Files []File
}

// Generator renders blueprints.
type Generator struct {
	tmpl *template.Template
}

// New returns a generator with the embedded templates parsed.
func New() (*Generator, error) {
	tmpl, err := template.ParseFS(templates.FS, "*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	return &Generator{tmpl: tmpl}, nil
}

// Options configures a generation run.
type Options struct {
	// BlueprintPath is recorded in each file's generated header.
	BlueprintPath string
	// Only, when non-empty, restricts generation to one resource key. It exists
	// for inspecting output while iterating on a template.
	Only string
}

// Build renders every file the blueprint produces, without writing anything.
func (g *Generator) Build(bp blueprint.Blueprint, opts Options) (Plan, error) {
	// Refused rather than skipped, per the failure-behaviour rule: an ephemeral the
	// blueprint declares and the emitter silently omitted would leave a provider that
	// looks complete and cannot open the value.
	if len(bp.Ephemerals) > 0 {
		return Plan{}, fmt.Errorf("the blueprint declares %d ephemeral(s), which this emitter "+
			"does not yet render; drop them or wait for ephemeral support", len(bp.Ephemerals))
	}

	digest := blueprintDigest(bp)

	ropts := render.Options{
		BlueprintPath:   opts.BlueprintPath,
		BlueprintSHA256: digest,
	}

	var plan Plan

	for _, r := range bp.Resources {
		if r.Drop || (opts.Only != "" && r.Key != opts.Only) {
			continue
		}

		files, err := g.resourceFiles(bp, r, ropts)
		if err != nil {
			return Plan{}, fmt.Errorf("resource %q: %w", r.Key, err)
		}
		plan.Files = append(plan.Files, files...)
	}

	for _, d := range bp.DataSources {
		if d.Drop || (opts.Only != "" && d.Key != opts.Only) {
			continue
		}

		files, err := g.dataSourceFiles(bp, d, ropts)
		if err != nil {
			return Plan{}, fmt.Errorf("data source %q: %w", d.Key, err)
		}
		plan.Files = append(plan.Files, files...)
	}

	for _, a := range bp.Actions {
		if a.Drop || (opts.Only != "" && a.Key != opts.Only) {
			continue
		}

		files, err := g.actionFiles(bp, a, ropts)
		if err != nil {
			return Plan{}, fmt.Errorf("action %q: %w", a.Key, err)
		}
		plan.Files = append(plan.Files, files...)
	}

	// Registration files are rendered from the whole blueprint even under -only,
	// because a registration listing a subset would not compile against a tree
	// containing the rest.
	if opts.Only == "" {
		regs, err := g.registrationFiles(bp, ropts)
		if err != nil {
			return Plan{}, err
		}
		plan.Files = append(plan.Files, regs...)
	}

	sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].Path < plan.Files[j].Path })

	return plan, nil
}

// resourceFiles renders the five files that make up one resource.
func (g *Generator) resourceFiles(
	bp blueprint.Blueprint,
	r blueprint.Resource,
	ropts render.Options,
) ([]File, error) {
	view, err := render.Resource(bp, r, ropts)
	if err != nil {
		return nil, fmt.Errorf("building the render view: %w", err)
	}

	dir := render.ResourceDir(bp, r)

	// Each entry is (output file, template). A template whose driving data is
	// absent is skipped rather than emitted empty.
	wanted := []struct {
		name, tmpl string
		skip       bool
	}{
		{"resource.go", "resource.go.tmpl", false},
		{"model.go", "model.go.tmpl", false},
		{"construct.go", "construct.go.tmpl", len(view.Construct.Assignments) == 0},
		{"state.go", "state.go.tmpl", len(view.State.Assignments) == 0},
		{"crud.go", "crud.go.tmpl", false},
		// A list resource lives beside the resource it lists, because it is a facet of it:
		// same package, same type name, same identity schema.
		{"list_resource.go", "list_resource.go.tmpl", view.List == nil},
	}

	// The escape hatches. Scaffolded once and then owned by whoever edits them, which is why
	// they are a separate list: the loop above produces files the generator keeps writing.
	scaffolds := []struct {
		name, tmpl string
		skip       bool
	}{
		{"modify_plan.go", "modify_plan.go.tmpl", !r.Hooks.ModifyPlan},
		{"predicate.go", "predicate.go.tmpl", !r.Hooks.ReadBackPredicate},
		{"state_upgrade.go", "state_upgrade.go.tmpl", !r.Hooks.StateUpgrade},
	}

	out := make([]File, 0, len(wanted))
	for _, w := range wanted {
		if w.skip {
			continue
		}
		content, err := g.renderFile(w.tmpl, view)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", w.name, err)
		}
		out = append(out, File{Path: filepath.Join(dir, w.name), Content: content})
	}

	for _, w := range scaffolds {
		if w.skip {
			continue
		}
		content, err := g.renderFile(w.tmpl, view)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", w.name, err)
		}
		out = append(out, File{
			Path:     filepath.Join(dir, w.name),
			Content:  content,
			Scaffold: true,
		})
	}

	// The requiredWhen support type, emitted exactly when resource.go references it --
	// both derive from the same rule set, so they cannot disagree.
	if cv, ok := render.ConditionalValidatorFile(r, ropts); ok {
		content, err := g.renderFile("conditional_validators.go.tmpl", cv)
		if err != nil {
			return nil, fmt.Errorf("conditional_validators.go: %w", err)
		}
		out = append(out, File{
			Path:    filepath.Join(dir, "conditional_validators.go"),
			Content: content,
		})
	}

	acc, err := g.acceptanceFiles(bp, r, ropts, dir)
	if err != nil {
		return nil, err
	}
	out = append(out, acc...)

	return out, nil
}

// acceptanceFiles renders the test helper and the two HCL fixtures.
//
// Separate from resourceFiles' own lists because these are not all one kind of file: the helper
// and the minimal fixture are generated and policed, and the maximal fixture is a scaffold.
//
// A resource that cannot produce them is not a failure. A read call an acceptance check has no
// arguments for, or a required attribute with no derivable value, are both refusals with a stated
// reason -- and refusing the test while still emitting a working resource is better than refusing
// both. The reason is surfaced by `emit -v` rather than swallowed.
func (g *Generator) acceptanceFiles(
	bp blueprint.Blueprint,
	r blueprint.Resource,
	ropts render.Options,
	dir string,
) ([]File, error) {
	var out []File

	helper, err := render.TestHelper(bp, r, ropts)
	if err != nil {
		if unsupported(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("test helper: %w", err)
	}

	content, err := g.renderFile("test_helper_test.go.tmpl", helper)
	if err != nil {
		return nil, fmt.Errorf("test_helper_test.go: %w", err)
	}
	out = append(out, File{Path: filepath.Join(dir, "test_helper_test.go"), Content: content})

	acc, err := render.AccTest(bp, r, ropts)
	switch {
	case err == nil:
		body, rErr := g.renderFile("resource_acceptance_test.go.tmpl", acc)
		if rErr != nil {
			return nil, fmt.Errorf("resource_acceptance_test.go: %w", rErr)
		}
		out = append(out, File{
			Path:    filepath.Join(dir, "resource_acceptance_test.go"),
			Content: body,
		})
	case !unsupported(err):
		return nil, fmt.Errorf("acceptance test: %w", err)
	}

	fixtures := []struct {
		name     string
		tmpl     string
		minimal  bool
		scaffold bool
	}{
		{"minimal.tf", "fixture_minimal.tf.tmpl", true, false},
		{"maximal.tf", "fixture_maximal.tf.tmpl", false, true},
	}

	for _, f := range fixtures {
		view, vErr := render.Fixture(bp, r, ropts, f.minimal)
		if vErr != nil {
			if unsupported(vErr) {
				continue
			}

			return nil, fmt.Errorf("%s: %w", f.name, vErr)
		}

		body, rErr := g.renderFile(f.tmpl, view)
		if rErr != nil {
			return nil, fmt.Errorf("%s: %w", f.name, rErr)
		}

		out = append(out, File{
			Path:     filepath.Join(dir, "testdata", f.name),
			Content:  body,
			Scaffold: f.scaffold,
		})
	}

	// The replace fixture exists exactly when the acceptance test has a replace step;
	// both derive from the same candidate, so they cannot disagree about whether to run.
	replace, ok, rErr := render.ReplaceFixture(bp, r, ropts)
	switch {
	case rErr != nil && !unsupported(rErr):
		return nil, fmt.Errorf("replace.tf: %w", rErr)
	case rErr == nil && ok:
		body, tErr := g.renderFile("fixture_minimal.tf.tmpl", replace)
		if tErr != nil {
			return nil, fmt.Errorf("replace.tf: %w", tErr)
		}
		out = append(out, File{Path: filepath.Join(dir, "testdata", "replace.tf"), Content: body})
	}

	return out, nil
}

// unsupported reports whether an error is a stated refusal rather than a fault.
func unsupported(err error) bool {
	var u *render.ErrUnsupported

	return errors.As(err, &u)
}

// dataSourceFiles renders the four files that make up one data source.
//
// Four rather than the resource's five: a data source sends no request body, so there is
// no construct.go. The names match the reference provider's, in which read lives in
// read.go rather than in a crud.go that would be three-quarters empty.
func (g *Generator) dataSourceFiles(
	bp blueprint.Blueprint,
	d blueprint.DataSource,
	ropts render.Options,
) ([]File, error) {
	view, err := render.DataSource(bp, d, ropts)
	if err != nil {
		return nil, fmt.Errorf("building the render view: %w", err)
	}

	dir := render.DataSourceDir(bp, d)

	wanted := []struct {
		name, tmpl string
		skip       bool
	}{
		{"datasource.go", "datasource.go.tmpl", false},
		{"model.go", "datasource_model.go.tmpl", false},
		{"read.go", "datasource_read.go.tmpl", false},
		{"state.go", "datasource_state.go.tmpl", len(view.State.Assignments) == 0},
	}

	out := make([]File, 0, len(wanted))
	for _, w := range wanted {
		if w.skip {
			continue
		}
		content, err := g.renderFile(w.tmpl, view)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", w.name, err)
		}
		out = append(out, File{Path: filepath.Join(dir, w.name), Content: content})
	}

	return out, nil
}

// actionFiles renders the three files that make up one action.
//
// Three, against a resource's five and a data source's four. An action sends its arguments as
// call parameters rather than a request body, so there is no construct.go, and it writes
// nothing back, so there is no state.go.
func (g *Generator) actionFiles(
	bp blueprint.Blueprint,
	a blueprint.Action,
	ropts render.Options,
) ([]File, error) {
	view, err := render.Action(bp, a, ropts)
	if err != nil {
		return nil, fmt.Errorf("building the render view: %w", err)
	}

	dir := render.ActionDir(bp, a)

	wanted := []struct{ name, tmpl string }{
		{"action.go", "action.go.tmpl"},
		{"model.go", "action_model.go.tmpl"},
		{"invoke.go", "action_invoke.go.tmpl"},
	}

	out := make([]File, 0, len(wanted))
	for _, w := range wanted {
		content, err := g.renderFile(w.tmpl, view)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", w.name, err)
		}
		out = append(out, File{Path: filepath.Join(dir, w.name), Content: content})
	}

	return out, nil
}

func (g *Generator) registrationFiles(
	bp blueprint.Blueprint,
	ropts render.Options,
) ([]File, error) {
	dir := render.ProviderDir(bp)

	specs := []struct {
		name, tmpl string
		kind       render.Kind
	}{
		{"resources.go", "provider_resources.go.tmpl", render.KindResources},
		{"datasources.go", "provider_datasources.go.tmpl", render.KindDataSources},
		{"list_resources.go", "provider_list_resources.go.tmpl", render.KindListResources},
		{"actions.go", "provider_actions.go.tmpl", render.KindActions},
	}

	out := make([]File, 0, len(specs))
	for _, s := range specs {
		view := render.Registration(bp, s.kind, ropts)

		content, err := g.renderFile(s.tmpl, view)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.name, err)
		}
		out = append(out, File{Path: filepath.Join(dir, s.name), Content: content})
	}

	return out, nil
}

// renderFile executes one template and formats the result.
//
// Go output goes through gofumpt; anything else is emitted as the template wrote it. The
// distinction comes from the template's own name rather than a flag at each call site, because a
// caller that forgot the flag would hand HCL to a Go parser and get an error about the language
// instead of about the mistake.
func (g *Generator) renderFile(name string, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := g.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, fmt.Errorf("rendering %s: %w", name, err)
	}

	if !strings.HasSuffix(name, ".go.tmpl") {
		return buf.Bytes(), nil
	}

	formatted, err := formatGo(buf.Bytes())
	if err != nil {
		return nil, err
	}

	return formatted, nil
}

// formatGo formats emitted Go with gofumpt.
//
// gofumpt rather than go/format, and this is not a stylistic preference: the
// target repositories run gofumpt, gci and golines with autofix enabled, so
// output that is merely gofmt-clean gets silently rewritten the first time a
// developer opens it and then shows up as drift with no source change.
func formatGo(src []byte) ([]byte, error) {
	out, err := format.Source(src, format.Options{LangVersion: langVersion})
	if err != nil {
		// The unformattable source is the only way to debug a broken template, so
		// it is returned with the error rather than discarded.
		return nil, fmt.Errorf(
			"%w: %w\n--- unformatted output ---\n%s",
			ErrFormat,
			err,
			numberLines(src),
		)
	}
	return out, nil
}

// numberLines annotates source with line numbers, since a formatter error reports
// a position and matching it against unnumbered output is needless work.
func numberLines(src []byte) string {
	lines := strings.Split(string(src), "\n")

	var b strings.Builder
	for i, l := range lines {
		fmt.Fprintf(&b, "%4d | %s\n", i+1, l)
	}

	return b.String()
}

// blueprintDigest is the content digest recorded in each generated header.
//
// It is computed from the canonical encoding rather than from the file bytes on
// disk, so reformatting a blueprint without changing its meaning does not rewrite
// every generated file.
func blueprintDigest(bp blueprint.Blueprint) string {
	data, err := blueprint.Marshal(bp)
	if err != nil {
		return "unknown"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// WriteOptions configures writing a plan to disk.
type WriteOptions struct {
	// Root is the output directory.
	Root string
	// Force writes files even when the existing content is not marked generated.
	// It exists for adopting a tree, and is deliberately not the default.
	Force bool
}

// WriteResult reports what a write did.
type WriteResult struct {
	Written   []string
	Unchanged []string
	// Kept are scaffolds that already existed and were deliberately left alone.
	Kept []string
}

// Write writes a plan to disk.
//
// A file whose content is already identical is left alone and reported as
// unchanged, so that regenerating does not churn modification times and a
// file-watcher does not rebuild the world on a no-op run.
func Write(plan Plan, opts WriteOptions) (WriteResult, error) {
	var res WriteResult

	for _, f := range plan.Files {
		target := filepath.Join(opts.Root, f.Path)

		// A scaffold that exists is left exactly as found, whatever it now contains. Checked
		// before reading it, because the content is irrelevant: the only question is whether
		// somebody already owns this file.
		//
		// Distinct from the overwrite refusal below, which is a safety net for a collision
		// nobody intended. This is intent -- the file is meant to diverge.
		if f.Scaffold {
			if _, statErr := os.Stat(target); statErr == nil {
				res.Kept = append(res.Kept, f.Path)

				continue
			}
		}

		existing, err := os.ReadFile(
			target,
		) //nolint:gosec // the path is operator-supplied by design
		switch {
		case err == nil:
			if bytes.Equal(existing, f.Content) {
				res.Unchanged = append(res.Unchanged, f.Path)
				continue
			}
			// Overwriting something this tool does not own would destroy work
			// with no way to recover it.
			if !opts.Force && !bytes.Contains(existing, []byte(generatedMarker)) {
				return res, fmt.Errorf("%w: %s", ErrRefusedOverwrite, target)
			}
		case !os.IsNotExist(err):
			return res, fmt.Errorf("reading %s: %w", target, err)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return res, fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, f.Content, 0o600); err != nil {
			return res, fmt.Errorf("writing %s: %w", target, err)
		}

		res.Written = append(res.Written, f.Path)
	}

	return res, nil
}
