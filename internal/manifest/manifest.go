// Package manifest records what generation produced and what it must never
// touch.
//
// It exists for two things nothing else can answer. First, which files
// generation produced *last* time: comparing the revised spec against what
// is on disk finds a file that changed and a file that went missing, but it
// cannot find a file the spec no longer produces at all.
// Renaming an entity leaves one behind, it still compiles, it is registered
// nowhere, and nothing complains. Second, which paths are authored: the
// small set of committed data files (corrections, audit inputs,
// tfpfgen.yaml) that generation may never write, enforced here rather than
// by convention.
//
// CI catches these anyway by regenerating and diffing the whole worktree,
// which is strictly stronger. The manifest is what gives the same answer
// locally, without requiring a clean git tree to interpret.
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
const Name = "manifest.json"

// FormatVersion is the manifest format version.
const FormatVersion = "1"

// ErrUnsupportedFormat reports a manifest this build cannot read.
var ErrUnsupportedFormat = errors.New("unsupported manifest format version")

// OriginSDK marks an entry written by `sdk generate`. The two generating
// verbs share one manifest so the drift check covers everything the toolkit
// owns, and the origin is what lets each verb replace its own inventory
// without stranding the other's.
//
// `provider generate`'s own entries carry no origin, deliberately: the empty
// value is the common case and keeps the committed file small.
const OriginSDK = "sdk"

// OriginToolchain marks an entry the toolchain itself finalised after
// install: `go mod tidy` writes go.sum, a file no template emits. Its
// digest is recorded so the drift gate covers it, under its own origin so
// neither generating verb's inventory replacement strands it.
const OriginToolchain = "toolchain"

// Entry is one file the manifest accounts for.
type Entry struct {
	// Path is relative to the provider root, always with forward slashes so
	// the manifest is identical whichever platform generated it.
	Path string `json:"path"`
	// SHA256 is the content digest, which lets drift be detected without
	// keeping a copy of the output. Empty on authored entries — their content
	// is owned by a human and no digest polices it.
	SHA256 string `json:"sha256,omitempty"`
	// Source says what the file was generated from — an entity key, a
	// template name — so a reviewer looking at a generated file can find what
	// to change instead. Empty on authored entries.
	Source string `json:"source,omitempty"`
	// Origin names the verb that wrote the file. Empty means `provider
	// generate`; OriginSDK means `sdk generate`.
	Origin string `json:"origin,omitempty"`
	// Authored marks a path generation may never write: the file belongs to
	// a human and carries data, not derived output.
	Authored bool `json:"authored,omitempty"`
}

// Manifest is the inventory of a generation run.
type Manifest struct {
	FormatVersion string `json:"formatVersion"`
	// ToolVersion is recorded here and nowhere else. Generated files
	// deliberately carry no version, because stamping one into every file
	// would make each release rewrite the entire tree; keeping it in one
	// place makes a version bump a one-line diff.
	ToolVersion string  `json:"toolVersion"`
	Files       []Entry `json:"files"`
}

// New builds a manifest from a set of entries, sorted by path.
//
// Sorting matters: the manifest is committed and diffed, so an ordering that
// depended on map iteration would produce spurious changes.
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

// AuthoredPaths returns the paths generation may never write.
func (m Manifest) AuthoredPaths() map[string]bool {
	out := make(map[string]bool)
	for _, f := range m.Files {
		if f.Authored {
			out[f.Path] = true
		}
	}
	return out
}

// RefusesWrites returns the subset of candidate paths that are authored, in
// sorted order. A generating verb calls this before touching the tree and
// treats a non-empty answer as an error, not a warning.
func (m Manifest) RefusesWrites(candidates []string) []string {
	authored := m.AuthoredPaths()
	var refused []string
	for _, p := range candidates {
		if authored[p] {
			refused = append(refused, p)
		}
	}
	sort.Strings(refused)
	return refused
}

// EntriesNotOf returns the entries other verbs recorded, authored entries
// included. A verb rewriting the manifest carries these forward unchanged,
// so replacing its own inventory cannot silently drop another's.
func (m Manifest) EntriesNotOf(origin string) []Entry {
	var out []Entry
	for _, f := range m.Files {
		if f.Origin != origin || f.Authored {
			out = append(out, f)
		}
	}
	return out
}

// Marshal renders the manifest as canonical JSON with a trailing newline.
func Marshal(m Manifest) ([]byte, error) {
	var buffer bytes.Buffer

	enc := json.NewEncoder(&buffer)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)

	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("encoding manifest: %w", err)
	}

	return buffer.Bytes(), nil
}

// Load reads a manifest from a provider root.
//
// A missing manifest is not an error: it is the state of a tree that has
// never been generated. It returns an empty manifest and false, so a caller
// can tell "nothing unproduced" apart from "cannot know".
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
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// UnproducedFilesOf returns the paths one verb's entries record that the current run
// no longer produces and which still exist on disk. Authored entries are
// never unproduced — nothing produces them.
//
// A path recorded but already deleted is not unproduced: somebody removed it,
// the revised spec agrees it should not exist, and there is nothing to
// report.
func (m Manifest) UnproducedFilesOf(root, origin string, produced map[string]bool) ([]string, error) {
	var out []string

	for _, f := range m.Files {
		if f.Origin != origin || f.Authored || produced[f.Path] {
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
