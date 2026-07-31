package blueprint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// jsonIndent is the on-disk indentation. Blueprints are reviewed as JSON by
// hand, so they are written indented rather than compact.
const jsonIndent = "  "

// Marshal renders the blueprint as canonical JSON: two-space indented, one
// trailing newline, HTML escaping off.
//
// Canonical means byte-stable for a given value. That is not a nicety — CI
// regenerates blueprints and fails on any diff, so an unstable encoding would
// make the drift check fire on runs that changed nothing.
func Marshal(b Blueprint) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetIndent("", jsonIndent)
	// Go escapes <, > and & by default, which mangles descriptions and URLs for
	// no benefit here.
	enc.SetEscapeHTML(false)

	if err := enc.Encode(b); err != nil {
		return nil, fmt.Errorf("encoding blueprint: %w", err)
	}

	return buf.Bytes(), nil
}

// Unmarshal parses a blueprint and validates it.
//
// Parsing rejects unknown fields. A silently ignored key is the worst failure
// mode for a hand-authored document: the author writes `updateStyle` where the
// schema says `policy.updateStyle`, sees no complaint, and gets a provider that
// clears attributes it should preserve.
func Unmarshal(data []byte) (Blueprint, error) {
	// The version is read first, before the strict decode, and that ordering is the whole point of
	// having a version at all. A format change moves fields, so a document from the previous format
	// fails a strict decode with "unknown field \"attributes\"" -- which tells a reader nothing
	// about what is actually wrong or what to do about it.
	if err := checkFormatVersion(data); err != nil {
		return Blueprint{}, err
	}

	var b Blueprint

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&b); err != nil {
		return Blueprint{}, fmt.Errorf("parsing blueprint: %w", err)
	}

	if err := b.Validate(); err != nil {
		return Blueprint{}, err
	}

	return b, nil
}

// checkFormatVersion reads only formatVersion, tolerating everything else.
//
// Deliberately not strict: the document being checked is one this build may not understand, so
// refusing it for an unknown field would defeat the purpose. A fragment with no formatVersion at all
// is accepted here and settled by the caller, because a resource file legitimately omits it.
func checkFormatVersion(data []byte) error {
	var peek struct {
		FormatVersion string `json:"formatVersion"`
	}

	if err := json.Unmarshal(data, &peek); err != nil {
		// Not decodable even loosely, so let the strict decode produce the better message.
		return nil
	}

	if peek.FormatVersion == "" || peek.FormatVersion == FormatVersion {
		return nil
	}

	return fmt.Errorf(
		"%w: found %q, this build understands %q.%s Re-run `ingest` against the snapshot, or "+
			"move the keys by hand",
		ErrUnsupportedFormat,
		peek.FormatVersion,
		FormatVersion,
		migrationNote(peek.FormatVersion),
	)
}

// migrationNote is what changed since the version a document was written against.
//
// A table rather than one sentence in the error: the message has to stay true as versions
// accrue, and a single hardcoded story silently becomes wrong at the next bump. A version
// with no entry simply gets no note, which is honest -- better than describing the wrong
// migration.
func migrationNote(found string) string {
	notes := map[string]string{
		"1": " Format 2 moved a block's attributes under \"schema\"," +
			" and renamed \"terraformType\" to \"name\" holding the short name." +
			" Format 3 renamed an attribute type's \"enum\" to \"allowedValues\".",
		"2": " Format 3 renamed an attribute type's \"enum\" to \"allowedValues\".",
	}

	return notes[found]
}

// Load reads and validates a blueprint from a file.
func Load(path string) (Blueprint, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is operator-supplied by design
	if err != nil {
		return Blueprint{}, fmt.Errorf("reading %s: %w", path, err)
	}

	b, err := Unmarshal(data)
	if err != nil {
		return Blueprint{}, fmt.Errorf("%s: %w", path, err)
	}

	return b, nil
}

// Save writes a blueprint as canonical JSON.
func Save(path string, b Blueprint) error {
	data, err := Marshal(b)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// Ext is the blueprint file extension.
const Ext = ".blueprint.json"

// LoadDir loads every blueprint under root, recursively, and merges them into
// one. It is how a provider is split across a file per resource while the emitter
// still sees a single document.
//
// A directory containing no blueprint is an error rather than an empty result: it
// is nearly always a mistyped path, and returning success with nothing to emit
// makes that look like a working run that generated no files.
func LoadDir(root string) (Blueprint, error) {
	paths, err := findBlueprints(root)
	if err != nil {
		return Blueprint{}, err
	}
	if len(paths) == 0 {
		return Blueprint{}, fmt.Errorf(
			"%w under %s (expected files named *%s)",
			ErrNoBlueprint,
			root,
			Ext,
		)
	}

	// Sorted so that the merged result does not depend on directory order.
	sort.Strings(paths)

	var merged Blueprint
	providerSetBy := ""

	for _, path := range paths {
		part, err := loadPart(path)
		if err != nil {
			return Blueprint{}, err
		}

		if merged.FormatVersion == "" {
			merged.FormatVersion = part.FormatVersion
		}

		// Exactly one file carries the provider block. Two would mean the
		// emitter silently picks one, and which one would depend on filename
		// ordering.
		if part.Provider.Name != "" {
			if providerSetBy != "" {
				return Blueprint{}, fmt.Errorf("%w: provider block declared in both %s and %s",
					ErrInvalid, providerSetBy, filepath.Base(path))
			}
			merged.Provider = part.Provider
			merged.Source = part.Source
			providerSetBy = filepath.Base(path)
		}

		merged.Resources = append(merged.Resources, part.Resources...)
		merged.DataSources = append(merged.DataSources, part.DataSources...)
		merged.Actions = append(merged.Actions, part.Actions...)
		merged.Ephemerals = append(merged.Ephemerals, part.Ephemerals...)
	}

	if providerSetBy == "" {
		return Blueprint{}, fmt.Errorf(
			"%w: no file under %s declares a provider block",
			ErrInvalid,
			root,
		)
	}

	sort.Slice(
		merged.Resources,
		func(i, j int) bool { return merged.Resources[i].Key < merged.Resources[j].Key },
	)
	sort.Slice(
		merged.DataSources,
		func(i, j int) bool { return merged.DataSources[i].Key < merged.DataSources[j].Key },
	)
	sort.Slice(
		merged.Actions,
		func(i, j int) bool { return merged.Actions[i].Key < merged.Actions[j].Key },
	)
	sort.Slice(
		merged.Ephemerals,
		func(i, j int) bool { return merged.Ephemerals[i].Key < merged.Ephemerals[j].Key },
	)

	// Validate once on the whole document. Cross-resource rules -- duplicate
	// type names, duplicate import aliases -- are invisible to a per-file check,
	// and they are exactly the ones that break a provider at startup.
	if err := merged.Validate(); err != nil {
		return Blueprint{}, fmt.Errorf("%s: %w", root, err)
	}

	return merged, nil
}

// loadPart parses one file of a split blueprint without validating it, since a
// fragment is legitimately incomplete on its own.
func loadPart(path string) (Blueprint, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is operator-supplied by design
	if err != nil {
		return Blueprint{}, fmt.Errorf("reading %s: %w", path, err)
	}

	// Version before strict decode, for the reason given on checkFormatVersion.
	if err := checkFormatVersion(data); err != nil {
		return Blueprint{}, fmt.Errorf("%s: %w", path, err)
	}

	var b Blueprint

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&b); err != nil {
		return Blueprint{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	if b.FormatVersion != FormatVersion {
		return Blueprint{}, fmt.Errorf("%s: %w: %q (this build understands %q)",
			path, ErrUnsupportedFormat, b.FormatVersion, FormatVersion)
	}

	return b, nil
}

func findBlueprints(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", root, err)
	}

	// A single file is accepted so that -blueprint can name either one resource
	// or a whole tree.
	if !info.IsDir() {
		return []string{root}, nil
	}

	var paths []string

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), Ext) {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}

	return paths, nil
}
