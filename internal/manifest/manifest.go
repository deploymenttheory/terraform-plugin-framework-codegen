// Package manifest records what a generation run produced.
//
// It exists for one thing the emitter cannot otherwise know: which files it
// produced *last* time. Comparing the blueprints against what is on disk finds a
// file that changed and a file that went missing, but it cannot find an orphan --
// a generated file the blueprints no longer produce. Renaming a resource leaves
// one behind, it still compiles, it is still registered nowhere, and nothing
// complains.
//
// CI catches orphans anyway by regenerating and diffing the whole worktree, which
// is strictly stronger. The manifest is what gives the same answer locally,
// without requiring a clean git tree to interpret.
package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Name is the manifest's path relative to the provider root.
const Name = ".tfprovidergen/manifest.json"

// FormatVersion is the manifest format version.
const FormatVersion = "1"

// ErrUnsupportedFormat reports a manifest this build cannot read.
var ErrUnsupportedFormat = errors.New("unsupported manifest format version")

// Entry is one generated file.
type Entry struct {
	// Path is relative to the provider root, always with forward slashes so the
	// manifest is identical whichever platform generated it.
	Path string `json:"path"`
	// SHA256 is the content digest, which lets drift be detected without keeping
	// a copy of the output.
	SHA256 string `json:"sha256"`
	// Blueprint is the source the file was generated from, so a reviewer looking
	// at a generated file can find what to edit instead.
	Blueprint string `json:"blueprint"`
}

// Manifest is the inventory of a generation run.
type Manifest struct {
	FormatVersion string `json:"formatVersion"`
	// ToolVersion is recorded here and nowhere else. Generated files deliberately
	// carry no version, because stamping one into every file would make each
	// release rewrite the entire tree; keeping it in one place makes a version
	// bump a one-line diff.
	ToolVersion string  `json:"toolVersion"`
	Files       []Entry `json:"files"`
}

// New builds a manifest from a set of entries, sorted by path.
//
// Sorting matters: the manifest is committed and diffed, so an ordering that
// depended on map iteration or blueprint order would produce spurious changes.
func New(toolVersion string, entries []Entry) Manifest {
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	return Manifest{
		FormatVersion: FormatVersion,
		ToolVersion:   toolVersion,
		Files:         sorted,
	}
}

// Paths returns the recorded paths as a set.
func (m Manifest) Paths() map[string]bool {
	out := make(map[string]bool, len(m.Files))
	for _, f := range m.Files {
		out[f.Path] = true
	}
	return out
}

// Marshal renders the manifest as canonical JSON with a trailing newline.
func Marshal(m Manifest) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)

	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("encoding manifest: %w", err)
	}

	return buf.Bytes(), nil
}

// Load reads a manifest from a provider root.
//
// A missing manifest is not an error: it is the state of a tree that has never
// been generated, or one generated before manifests existed. It returns an empty
// manifest and false, so a caller can tell "no orphans" apart from "cannot know".
func Load(root string) (Manifest, bool, error) {
	path := filepath.Join(root, Name)

	data, err := os.ReadFile(path) //nolint:gosec // the path is operator-supplied by design
	switch {
	case os.IsNotExist(err):
		return Manifest{}, false, nil
	case err != nil:
		return Manifest{}, false, fmt.Errorf("reading %s: %w", path, err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, false, fmt.Errorf("parsing %s: %w", path, err)
	}

	if m.FormatVersion != FormatVersion {
		return Manifest{}, false, fmt.Errorf("%s: %w: %q (this build understands %q)",
			path, ErrUnsupportedFormat, m.FormatVersion, FormatVersion)
	}

	return m, true, nil
}

// Save writes a manifest to a provider root.
func Save(root string, m Manifest) error {
	data, err := Marshal(m)
	if err != nil {
		return err
	}

	path := filepath.Join(root, Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// Orphans returns the manifest paths that the current run no longer produces and
// which still exist on disk.
//
// A path recorded but already deleted is not an orphan: somebody removed it, the
// blueprints agree it should not exist, and there is nothing to report.
func (m Manifest) Orphans(root string, produced map[string]bool) ([]string, error) {
	var out []string

	for _, f := range m.Files {
		if produced[f.Path] {
			continue
		}

		switch _, err := os.Stat(filepath.Join(root, f.Path)); {
		case err == nil:
			out = append(out, f.Path)
		case os.IsNotExist(err):
			continue
		default:
			return nil, fmt.Errorf("checking %s: %w", f.Path, err)
		}
	}

	sort.Strings(out)

	return out, nil
}
