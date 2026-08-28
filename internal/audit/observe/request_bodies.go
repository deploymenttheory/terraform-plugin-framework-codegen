// The request bodies a run got the API to accept, and what it answered with.
//
// An observation says something about one property. These say what a whole
// create looked like when it worked, which is the one thing a generated
// acceptance test cannot derive: the document describes what should be
// accepted, and only a run knows what was.

package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// encodeRequestBodies renders one entity's record deterministically,
// matching the observations beside it: sorted map keys, no HTML escaping,
// two-space indent.
func encodeRequestBodies(b RequestBodies) ([]byte, error) {
	var buffer bytes.Buffer
	enc := json.NewEncoder(&buffer)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(b); err != nil {
		return nil, fmt.Errorf("encoding request bodies for %s: %w", b.Entity, err)
	}
	return buffer.Bytes(), nil
}

// RequestBodiesSuffix is the committed file naming, one file per entity,
// matching the observations beside it.
const RequestBodiesSuffix = ".request_bodies.json"

// RequestBodies is what one entity's creates looked like when the API
// accepted them.
type RequestBodies struct {
	Entity string `json:"entity"`
	// Minimal is the smallest create the run got accepted, and Maximal the
	// fullest. Either may be absent when no create of that shape succeeded.
	Minimal *AcceptedRequestBody `json:"minimal,omitempty"`
	Maximal *AcceptedRequestBody `json:"maximal,omitempty"`
}

// AcceptedRequestBody is one create the API answered 2xx to.
type AcceptedRequestBody struct {
	// Status is the code the API answered, kept so a reader can see that this
	// was an acceptance rather than an assumption.
	Status int `json:"status"`
	// Request is the body as sent, with the run's own placeholders already
	// resolved — what a repeat of this create would have to carry.
	Request map[string]any `json:"request"`
	// Response is the object the API answered with. A property the request
	// carried and this does not is one the API accepts and never returns,
	// which terraform cannot hold in state without losing it on the next read.
	Response map[string]any `json:"response,omitempty"`
}

// Echoed reports whether the response carried the named wire property.
//
// A field the API accepts and never echoes cannot appear in a generated
// configuration: terraform compares what it planned against what the provider
// answers, and a value that never comes back reads as the provider losing it.
func (b *AcceptedRequestBody) Echoed(wire string) bool {
	if b == nil || b.Response == nil {
		return false
	}
	_, ok := b.Response[wire]
	return ok
}

// WriteRequestBodies commits one <entity>.request_bodies.json per entity
// under dir. Encoding matches the observations: sorted keys, stable bytes,
// so a re-run that learned nothing new rewrites nothing.
func WriteRequestBodies(dir string, requestBodies []RequestBodies) error {
	if len(requestBodies) == 0 {
		return nil
	}
	byEntity := map[string]RequestBodies{}
	for _, b := range requestBodies {
		if b.Entity == "" || (b.Minimal == nil && b.Maximal == nil) {
			continue
		}
		byEntity[b.Entity] = b
	}
	entities := make([]string, 0, len(byEntity))
	for e := range byEntity {
		entities = append(entities, e)
	}
	sort.Strings(entities)

	encoded := make(map[string][]byte, len(entities))
	for _, entity := range entities {
		raw, err := encodeRequestBodies(byEntity[entity])
		if err != nil {
			return err
		}
		encoded[entity] = raw
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	for _, entity := range entities {
		path := filepath.Join(dir, entity+RequestBodiesSuffix)
		if err := os.WriteFile(path, encoded[entity], 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

// ReadRequestBodies loads every recorded request body under dir, keyed by
// entity. A missing directory is not an error: an entity the probe never
// cleared has none, and generation falls back to deriving values from the
// document.
func ReadRequestBodies(dir string) (map[string]RequestBodies, error) {
	out := map[string]RequestBodies{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		var b RequestBodies
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("reading %s: %w", e.Name(), err)
		}
		if b.Entity != "" {
			out[b.Entity] = b
		}
	}
	return out, nil
}
