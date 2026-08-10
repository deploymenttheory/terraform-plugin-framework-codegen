package emit

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"path"
	"strings"
	"text/template"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/templates"
)

// File is one rendered provider-core file, not yet written.
type File struct {
	// Path is where the file lands, relative to the provider root,
	// forward slashes.
	Path string
	// Content is the rendered bytes.
	Content []byte
	// Source is the template the file came from, recorded in the
	// manifest so a reviewer can find what to change instead.
	Source string
}

// headerPartial is the shared DO-NOT-EDIT header every template includes:
// "header" for //-commented files, "hashheader" for #-commented ones.
const headerPartial = "provider-core/_header.tmpl"

// RenderProviderCore renders the complete provider core for one context.
// Files come back in walk order — sorted by path — and nothing is written.
//
// Every .go file is required to be gofmt-clean exactly as rendered; a
// template whose output gofmt would rewrite is a bug reported here, not
// something to quietly reformat. The one mechanical normalization applied
// first (to every file) is blank-line bookkeeping: runs of blank lines
// collapse to one and the file ends with exactly one newline, because
// that is what presence-branching does to vertical whitespace and it
// carries no meaning. The provider core's templates never render literals
// where blank lines are load-bearing; a template family for which they
// are must not use this renderer.
func RenderProviderCore(pc ProviderCore) ([]File, error) {
	return renderProviderCore(templates.ProviderCore, pc)
}

// renderProviderCore is RenderProviderCore over any FS, so tests can feed
// broken trees.
func renderProviderCore(fsys fs.FS, pc ProviderCore) ([]File, error) {
	if err := pc.check(); err != nil {
		return nil, err
	}

	partial, err := fs.ReadFile(fsys, headerPartial)
	if err != nil {
		return nil, fmt.Errorf("reading the shared header partial: %w", err)
	}

	var files []File
	walkErr := fs.WalkDir(fsys, "provider-core", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		base := path.Base(p)
		if strings.HasPrefix(base, "_") {
			return nil // a partial, parsed with every template, never emitted
		}
		if !pc.selects(base) {
			return nil // the other backend's dialect file
		}

		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}

		rendered, err := render(pc, p, string(partial), string(raw))
		if err != nil {
			return err
		}

		out := strings.TrimSuffix(strings.TrimPrefix(p, "provider-core/"), ".tmpl")
		if strings.HasSuffix(out, ".go") {
			if err := gofmtClean(p, rendered); err != nil {
				return err
			}
		}

		files = append(files, File{Path: out, Content: rendered, Source: p})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	return files, nil
}

// selects says whether this context emits the named template. Dialect
// pairs follow one naming rule: a base name ending in _kiota.go.tmpl or
// _openapigenerator.go.tmpl belongs to that backend alone.
func (pc ProviderCore) selects(base string) bool {
	name := strings.TrimSuffix(base, ".tmpl")
	switch {
	case strings.HasSuffix(name, "_kiota.go"):
		return pc.BackendKiota
	case strings.HasSuffix(name, "_openapigenerator.go"):
		return pc.BackendOpenAPIGenerator
	default:
		return true
	}
}

// view is what one template renders against: the provider-core context
// plus the template's own path, which the shared header interpolates so
// every generated file names its origin.
type view struct {
	ProviderCore
	// Source is the template path under the templates tree.
	Source string
}

// render parses the header partial and one template body together —
// per-file parsing, so repeated base names across directories can never
// shadow one another — and executes them against the view.
func render(pc ProviderCore, source, partial, body string) ([]byte, error) {
	t, err := template.New(path.Base(source)).Parse(partial)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", headerPartial, err)
	}
	if _, err := t.Parse(body); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", source, err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, view{ProviderCore: pc, Source: source}); err != nil {
		return nil, fmt.Errorf("rendering %s: %w", source, err)
	}

	return squeezeBlankLines(buf.Bytes()), nil
}

// squeezeBlankLines is the blank-line bookkeeping RenderProviderCore
// documents: whitespace-only lines become empty, runs of them collapse to
// one, and the file ends with exactly one newline.
func squeezeBlankLines(b []byte) []byte {
	lines := strings.Split(string(b), "\n")

	out := lines[:0]
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}

	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}

	return []byte(strings.Join(out, "\n") + "\n")
}

// gofmtClean holds one rendered .go file to the fixed-point requirement.
func gofmtClean(source string, rendered []byte) error {
	formatted, err := format.Source(rendered)
	if err != nil {
		return fmt.Errorf("%s renders Go that does not parse: %w", source, err)
	}
	if !bytes.Equal(formatted, rendered) {
		return fmt.Errorf("%s does not render gofmt-clean; fix the template's spacing, not the output", source)
	}
	return nil
}
