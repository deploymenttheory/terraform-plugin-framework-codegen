// Package infer is the triangulating inference: it turns one run's raw,
// per-entity evidence into the conditional-edge observations no single probe
// could justify. Where the executor (internal/audit/run) records what each
// request did, inference stands back and reads the whole picture — every
// create the API accepted, every body change a refusal forced, the collection
// responses, and the strategy's declared hypotheses — and asserts an edge only
// where those signals converge.
//
// The discipline is deliberately conservative. An edge is confirmed only when
// the evidence points the same way from more than one direction: a field
// accepted under one gate value and refused under another, a co-requirement
// the API forced and a hypothesis that predicted it. Thin, one-directional or
// conflicting evidence yields an inconclusive observation, never an assertion,
// and a lone ambiguous 4xx yields nothing at all. That is what stops a planted
// or accidental false edge from reaching a spec correction: the inference
// would rather say "inconclusive" than guess.
//
// Infer is pure: the same evidence and strategy always produce the same
// observations, byte for byte under JSON. No network, no clock, no map-order
// leaks — the run stamps (id run, observed-at) are the caller's to add.
package infer

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/strategy"
)

// AdjustAction names one kind of change the adaptive executor made to a
// request body to get it accepted. It is the raw signal inference reads to
// learn what the live API forced; the action values are stable because the
// executor's refusal grammar is.
type AdjustAction string

const (
	// AdjustAdd: a field was added because the API said it was required.
	AdjustAdd AdjustAction = "add"
	// AdjustRemove: a field was removed because the API said it was not
	// valid for the variant under test.
	AdjustRemove AdjustAction = "remove"
	// AdjustRequires: a field was added because the API said another field
	// requires it — a co-requirement, GateField naming the triggering field.
	AdjustRequires AdjustAction = "requires"
	// AdjustBorrow: a field's value was replaced with a real id borrowed
	// from another collection.
	AdjustBorrow AdjustAction = "borrow"
)

// RequestAdjustment is one change the adaptive executor made, carried from the
// run to the inference and reported on the run summary. It holds no excerpt:
// the proof lives on the observations, and an excerpt-free summary keeps
// redaction trivial.
//
// For AdjustRemove and AdjustAdd, GateField and GateValue name the value
// condition the refusal cited ("not valid when kind=web"). For AdjustRequires,
// GateField names the field that triggered the requirement ("dnssec requires
// domain") and Field names the field that was added. For AdjustBorrow,
// GateField names the collection the id was borrowed from.
type RequestAdjustment struct {
	Entity    string       `json:"entity"`
	Action    AdjustAction `json:"action"`
	Field     string       `json:"field"`
	GateField string       `json:"gateField,omitempty"`
	GateValue string       `json:"gateValue,omitempty"`
}

// FieldPair is an unordered pair of field names, used as the mutual-exclusion
// signal: the executor saw a create refused because both were set together.
type FieldPair struct {
	A string `json:"a"`
	B string `json:"b"`
}

// ConditionalValue is one value-cycling outcome: under discriminator GateField
// = GateValue, sibling field Field carrying value Value was either accepted or
// refused. It is the value-level analogue of a variant diff — the same sibling
// value accepted under one discriminator value and refused under another is the
// both-direction corroboration of a value-conditional edge — recorded when the
// executor cycles an enum field's value to heal a free-form conditional
// refusal the refusal grammar could not classify.
type ConditionalValue struct {
	GateField string `json:"gateField"`
	GateValue string `json:"gateValue"`
	Field     string `json:"field"`
	Value     string `json:"value"`
	Accepted  bool   `json:"accepted"`
}

// Evidence is everything one entity's run gathered that inference reads. The
// run populates it; Infer consumes it alongside that entity's compiled
// strategy. Every slice is the executor's raw record — inference does the
// converging, so a caller cannot bias a conclusion by pre-summarising here.
type Evidence struct {
	// Entity is the classified entity key the evidence is about.
	Entity string
	// AcceptedBodies is every create body the API accepted, resolved as
	// sent. Their gate values and field sets are the positive half of
	// variant diffing.
	AcceptedBodies []map[string]any
	// Adjustments is every body change the executor was forced to make.
	Adjustments []RequestAdjustment
	// CombinedRefusals lists field pairs a create was refused for carrying
	// together — the mutual-exclusion signal. Empty for most entities; the
	// executor records one only on the specific refusal grammar.
	CombinedRefusals []FieldPair
	// ConditionalValues lists the value-cycling outcomes the executor gathered
	// healing free-form conditional refusals: each (discriminator value,
	// sibling field, sibling value) the API accepted or refused. A sibling
	// value accepted under one discriminator value and refused under another is
	// the both-direction signal for a value-conditional validConfiguration.
	ConditionalValues []ConditionalValue
	// ListBodies is the raw collection responses the executor captured, for
	// the list-response-shape finding. Only the structure is read; no value
	// is stored on the observation.
	ListBodies [][]byte
	// RejectsUnknownFields is the summary's caution flag for this entity:
	// when the API rejects unknown body fields, a removal-based signal is
	// weaker, because a refusal might have been about the unknown field.
	RejectsUnknownFields bool
}

// gate is the primary discriminator inference diffs variants across: the gate
// field and the values that appeared as variants. It is read from the
// strategy's variants, which already chose and ranked the gate.
type gate struct {
	field  string
	values []string
}

// gateOf reads the strategy's primary gate: the field the non-baseline
// variants pin, and the sorted set of values they pin it to. Empty when the
// strategy has no gate (a flat resource), which is what makes such an entity
// yield no variant edges.
func gateOf(compiled *strategy.Strategy) gate {
	if compiled == nil {
		return gate{}
	}
	var g gate
	seen := map[string]bool{}
	for _, v := range compiled.Variants {
		if v.GateField == "" || v.GateValue == "" {
			continue
		}
		g.field = v.GateField
		if !seen[v.GateValue] {
			seen[v.GateValue] = true
			g.values = append(g.values, v.GateValue)
		}
	}
	sort.Strings(g.values)
	return g
}

// acceptedUnder buckets the fields of every accepted create body by the gate
// value that body pinned, returning value -> set of field names present
// (excluding the gate field itself). A body with no gate value lands under the
// empty string.
func acceptedUnder(bodies []map[string]any, gateField string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, body := range bodies {
		val := ""
		if gateField != "" {
			if raw, ok := body[gateField]; ok {
				val = fmt.Sprint(raw)
			}
		}
		set := out[val]
		if set == nil {
			set = map[string]bool{}
			out[val] = set
		}
		for k := range body {
			if k == gateField {
				continue
			}
			set[k] = true
		}
	}
	return out
}

// removedUnder buckets removed fields by the gate value the removal cited.
func removedUnder(adjustments []RequestAdjustment, gateField string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, a := range adjustments {
		if a.Action != AdjustRemove || a.GateField != gateField || a.GateValue == "" {
			continue
		}
		set := out[a.GateValue]
		if set == nil {
			set = map[string]bool{}
			out[a.GateValue] = set
		}
		set[a.Field] = true
	}
	return out
}

// listShapeOf reads a collection response's structure: wrapped in an envelope
// object under a single array-valued key, or a bare array, plus any pagination
// style the body advertises. It returns nil when nothing decodes as a list.
func listShapeOf(bodies [][]byte) *observe.ListResponseShape {
	for _, raw := range bodies {
		if len(raw) == 0 {
			continue
		}
		var arr []any
		if err := json.Unmarshal(raw, &arr); err == nil {
			return &observe.ListResponseShape{Envelope: "bare", Pagination: "none"}
		}
		var env map[string]any
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		key := soleArrayKey(env)
		if key == "" {
			continue
		}
		return &observe.ListResponseShape{
			Envelope:   "wrapped",
			Key:        key,
			Pagination: paginationOf(env),
		}
	}
	return nil
}

// soleArrayKey returns the envelope's array-valued key: the preferred names
// first, then the sole array key when there is exactly one. Empty when the
// object carries no array or several ambiguous ones.
func soleArrayKey(env map[string]any) string {
	for _, preferred := range []string{"items", "data", "results"} {
		if _, ok := env[preferred].([]any); ok {
			return preferred
		}
	}
	keys := make([]string, 0, len(env))
	for k, v := range env {
		if _, ok := v.([]any); ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 1 {
		return keys[0]
	}
	return ""
}

// paginationOf classifies the pagination markers an envelope advertises by the
// keys it carries, in a fixed precedence so the finding is deterministic.
func paginationOf(env map[string]any) string {
	has := func(names ...string) bool {
		for _, n := range names {
			if _, ok := env[n]; ok {
				return true
			}
		}
		return false
	}
	switch {
	case has("cursor", "next_cursor", "nextCursor", "next"):
		return "cursor"
	case has("offset"):
		return "offset"
	case has("page", "page_number", "pageNumber"):
		return "page"
	default:
		return "none"
	}
}
