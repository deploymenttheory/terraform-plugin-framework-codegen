package plan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// InputsPath is where a provider repo commits its operator inputs. The
// file is authored data — generation never writes it — and optional: its
// absence means the audit covers what it can synthesize.
const InputsPath = "audit/inputs.json"

// Inputs is the parsed audit/inputs.json: per-entity values the audit
// cannot invent. The zero value is a valid, empty inputs set.
type Inputs struct {
	// Entities is keyed by classified entity key. Derive refuses a key
	// that classifies as nothing, with a did-you-mean — the same
	// strictness config applies to its own unknown keys, because a typo
	// here silently un-supplies a value.
	Entities map[string]EntityInputs
}

// EntityInputs is one entity's operator-supplied material.
type EntityInputs struct {
	// Values overrides value synthesis per wire property name: the
	// operator's value for an example-less field the API is picky about.
	Values map[string]any `json:"values,omitempty"`
	// ParentRefs supplies path-parameter values the audit cannot create
	// its way to: an existing parent object's id, or a lookup key. A
	// value is either a literal or a ${VAR} environment reference
	// resolved at execution time.
	ParentRefs map[string]string `json:"parentRefs,omitempty"`
	// Skip excludes the entity from the audit; it is listed in the plan
	// as skipped rather than silently absent.
	Skip bool `json:"skip,omitempty"`
}

// entityInputKeys is the closed key set inside one entity object.
var entityInputKeys = []string{"values", "parentRefs", "skip"}

// ParseInputs reads audit/inputs.json bytes. Nil or empty bytes are the
// absent file: an empty inputs set. Unknown keys inside an entity object
// are refused with a did-you-mean; entity keys themselves are checked by
// Derive, which knows the classified entities.
func ParseInputs(data []byte) (*Inputs, error) {
	in := &Inputs{}
	if len(data) == 0 {
		return in, nil
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("%s: %w", InputsPath, err)
	}

	entities := make([]string, 0, len(top))
	for entity := range top {
		entities = append(entities, entity)
	}
	sort.Strings(entities)

	in.Entities = make(map[string]EntityInputs, len(top))
	for _, entity := range entities {
		ei, err := parseEntityInputs(entity, top[entity])
		if err != nil {
			return nil, err
		}
		in.Entities[entity] = ei
	}
	return in, nil
}

// parseEntityInputs strictly decodes one entity's object.
func parseEntityInputs(entity string, raw json.RawMessage) (EntityInputs, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return EntityInputs{}, fmt.Errorf("%s: entity %q: must be an object with %s",
			InputsPath, entity, strings.Join(entityInputKeys, ", "))
	}
	for key := range keys {
		known := false
		for _, k := range entityInputKeys {
			known = known || key == k
		}
		if !known {
			return EntityInputs{}, fmt.Errorf("%s: entity %q: unknown key %q%s",
				InputsPath, entity, key, didYouMean(key, entityInputKeys))
		}
	}

	var ei EntityInputs
	if err := json.Unmarshal(raw, &ei); err != nil {
		return EntityInputs{}, fmt.Errorf("%s: entity %q: %w", InputsPath, entity, err)
	}
	params := make([]string, 0, len(ei.ParentRefs))
	for param := range ei.ParentRefs {
		params = append(params, param)
	}
	sort.Strings(params)
	for _, param := range params {
		if val := ei.ParentRefs[param]; val == "" || val == envRefOpen+envRefClose {
			return EntityInputs{}, fmt.Errorf("%s: entity %q: parentRefs.%s: must be a literal or ${VAR} naming an environment variable",
				InputsPath, entity, param)
		}
	}
	return ei, nil
}

// forEntity is the lookup Derive uses: absent entities read as the zero
// value, per the graceful-degradation contract.
func (in *Inputs) forEntity(entity string) EntityInputs {
	if in == nil || in.Entities == nil {
		return EntityInputs{}
	}
	return in.Entities[entity]
}
