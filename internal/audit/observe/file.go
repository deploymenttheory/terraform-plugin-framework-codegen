package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileSuffix is the committed file naming: one file per entity, so a
// re-audit of one entity rewrites one file and review diffs stay scoped.
const FileSuffix = ".observations.json"

// entityFile is the on-disk document: the entity named once, then its
// observations sorted. The per-observation run stamps stay on each
// observation — one file may legitimately carry findings from different
// runs, because a later run can extend an entity's coverage without
// re-earning what an earlier run already observed.
type entityFile struct {
	Entity       string        `json:"entity"`
	Observations []Observation `json:"observations"`
}

// Write commits observations under dir, one <entity>.observations.json per
// entity. Encoding is deterministic: observations are sorted, IDs are
// computed (or verified when already present), struct fields encode in
// fixed order, and json.Marshal sorts any map inside a value — the same
// findings always produce the same bytes. Every observation is validated
// first; nothing partial is written on error.
func Write(dir string, obs []Observation) error {
	byEntity := map[string][]Observation{}
	for _, o := range obs {
		// Canonicalize the open-typed parts through a JSON round trip, so
		// a typed value (a Values struct, an int) and its decoded form (a
		// map, a float) encode identically — which is what makes a
		// read/write round trip a fixed point.
		o.Value = canonicalValue(o.Value)
		if o.Condition != nil {
			c := *o.Condition
			c.Equals = canonicalValue(c.Equals)
			o.Condition = &c
		}
		want := ComputeID(o.Entity, o.Attribute, o.Kind, o.Condition)
		if o.ID == "" {
			o.ID = want
		}
		if err := o.Validate(); err != nil {
			return fmt.Errorf("observations for %s: %w", dir, err)
		}
		byEntity[o.Entity] = append(byEntity[o.Entity], o)
	}

	entities := make([]string, 0, len(byEntity))
	for e := range byEntity {
		entities = append(entities, e)
	}
	sort.Strings(entities)

	encoded := make(map[string][]byte, len(entities))
	for _, entity := range entities {
		group := byEntity[entity]
		Sort(group)
		raw, err := encodeFile(entityFile{Entity: entity, Observations: group})
		if err != nil {
			return err
		}
		encoded[entity] = raw
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	for _, entity := range entities {
		path := filepath.Join(dir, entity+FileSuffix)
		if err := os.WriteFile(path, encoded[entity], 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

// canonicalValue reduces any JSON-marshalable value to its decoded form:
// maps with sorted keys, numbers as float64. A value that does not survive
// the trip is left as it was, for Validate to refuse with a real message.
func canonicalValue(v any) any {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return v
	}
	return out
}

// encodeFile renders one entity document: two-space indented, trailing
// newline, HTML escaping off so excerpts read as written.
func encodeFile(f entityFile) ([]byte, error) {
	var buffer bytes.Buffer
	enc := json.NewEncoder(&buffer)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, fmt.Errorf("encoding observations for %s: %w", f.Entity, err)
	}
	return buffer.Bytes(), nil
}

// Read loads every entity's observations from dir. A missing directory is
// an audit that has not run yet, which is a normal state, not an error.
// Everything read is validated — the files are committed and hand-editable,
// so an unknown key, a drifted ID or a filename/entity mismatch is refused
// by name rather than flowing silently into revision.
func Read(dir string) ([]Observation, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var out []Observation
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, FileSuffix) {
			continue
		}
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}

		var f entityFile
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if want := f.Entity + FileSuffix; name != want {
			return nil, fmt.Errorf("%s: declares entity %q, which belongs in %s", path, f.Entity, want)
		}
		for i := range f.Observations {
			o := &f.Observations[i]
			if o.Entity != f.Entity {
				return nil, fmt.Errorf("%s: observation %q is about entity %q, not %q", path, o.ID, o.Entity, f.Entity)
			}
			if o.ID == "" {
				return nil, fmt.Errorf("%s: an observation with no id", path)
			}
			if err := o.Validate(); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
		}
		out = append(out, f.Observations...)
	}
	return out, nil
}
