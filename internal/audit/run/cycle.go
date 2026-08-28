package run

// Value-cycling is what keeps a free-form conditional refusal from blocking a
// whole entity. A real API often value-gates one enum field on another —
// A real API may make one enum value valid only for certain values of a sibling field — and
// enforces it with prose the refusal grammar cannot parse. The executor
// synthesises the first enum value of every field, so the first body it sends
// carries the combination least likely to be right, and a conditional API
// refuses it. Rather than give up, cycleConditional holds the field the step is
// exercising fixed and tries the other enum fields' remaining values until the
// API accepts a body — recording which combinations were refused and which
// accepted, the both-direction evidence the inference confirms a
// validConfiguration edge from.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
)

// maxCycleAttempts hard-bounds the create attempts one value-cycling pass makes,
// on top of the enum sizes and the entity request budget, so a wide enum can
// never spin the loop.
const maxCycleAttempts = 8

// cycleConditional heals a free-form conditional refusal by trying alternate
// enum values for the sibling fields present in the body, holding the pinned
// field fixed. It returns the accepted object and its response when a
// combination works, and records every combination it tried — refused and
// accepted — as value-conditional evidence. When nothing works within the cap
// it records an inconclusive edge per implicated sibling and returns healed
// false, so the caller continues rather than blocking.
//
// It acts only when the refusal names a field the entity declares (the
// generalized extraction) and the body carries a cyclable enum sibling; on a
// non-strategy run, where no synthesis hints exist, it is inert.
func (r *runner) cycleConditional(ctx context.Context, entity *entityState, rec *entityRecipe, body map[string]any, held string, refusal *httpResult, applied map[string]bool) (*createdObject, *httpResult, bool, error) {
	hints := r.hints[entity.plan.Entity]
	if hints == nil || refusal == nil {
		return nil, nil, false, nil
	}
	if len(namedKnownFields(refusalMessage(refusal.body), hints)) == 0 {
		// Nothing the entity declares is named: an unintelligible refusal, not a
		// conditional constraint. Give up without spending a request.
		return nil, nil, false, nil
	}
	gate := r.gateFieldFor(entity)
	if gate == "" {
		gate = held
	}
	targets := cyclableSiblings(body, hints, held)
	attempts := 0
	for i := range targets {
		tgt := targets[i]
		if applied["c:"+tgt.Field] {
			continue
		}
		applied["c:"+tgt.Field] = true
		current := fmt.Sprint(body[tgt.Field])

		// The current value under the current discriminator value is what was
		// just refused.
		discriminator, cf, cv := condCoords(body, tgt.Field, held, gate)
		r.recordConditional(entity, gate, discriminator, cf, cv, false)

		for _, v := range tgt.Enum {
			// Compared as text because the body holds whatever JSON decoding
			// produced, but sent as declared: the hint now keeps each enum
			// member's type, so there is nothing left to convert.
			if fmt.Sprint(v) == current || attempts >= maxCycleAttempts {
				continue
			}
			attempts++
			body[tgt.Field] = v
			obj, res, err := r.createObject(ctx, entity, rec, body)
			if err != nil {
				return nil, nil, false, err
			}
			d2, f2, v2 := condCoords(body, tgt.Field, held, gate)
			r.recordConditional(entity, gate, d2, f2, v2, obj != nil)
			if obj != nil {
				return obj, res, true, nil
			}
			if res == nil || !res.refused() {
				// A non-4xx discriminates nothing; stop cycling this field.
				break
			}
		}
		body[tgt.Field] = typedGate(tgt, current) // restore before the next target
	}

	// No combination worked. Record an inconclusive edge per implicated sibling
	// so the suspicion is visible without being asserted, and let the entity
	// continue with the variants and probes it can still exercise.
	for i := range targets {
		discriminator, cf, _ := condCoords(body, targets[i].Field, held, gate)
		r.recordConditionalInconclusive(entity, gate, discriminator, cf, refusal.excerpt)
	}
	return nil, nil, false, nil
}

// condCoords resolves one value-cycling attempt to the coordinates the evidence
// is keyed on: the discriminator (primary-gate) value, the constrained sibling
// field, and its current value. When the field being cycled is the gate itself
// (a per-enum-value step that pins a non-primary field), the constrained field
// is the held one instead.
func condCoords(body map[string]any, cycled, held, gate string) (discriminator, field, value string) {
	discriminator = fmt.Sprint(body[gate])
	field = cycled
	if cycled == gate {
		field = held
	}
	if field == "" {
		return discriminator, "", ""
	}
	value = fmt.Sprint(body[field])
	return discriminator, field, value
}

// cyclableSiblings is the sorted set of enum-typed fields present in the body
// that value-cycling may vary: every declared enum field with at least two
// members, except the one the step holds fixed.
func cyclableSiblings(body map[string]any, hints map[string]strategy.SynthHint, held string) []strategy.SynthHint {
	var out []strategy.SynthHint
	for f := range body {
		if f == held {
			continue
		}
		h, ok := hints[f]
		if !ok || len(h.Enum) < 2 {
			continue
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// recordConditional appends one value-cycling outcome for the inference to read.
func (r *runner) recordConditional(entity *entityState, gateField, gateValue, field, value string, accepted bool) {
	if gateField == "" || field == "" || value == "" {
		return
	}
	entity.ev.conditionalValues = append(entity.ev.conditionalValues, infer.ConditionalValue{
		GateField: gateField, GateValue: gateValue, Field: field, Value: value, Accepted: accepted,
	})
}

// recordConditionalInconclusive records that a sibling's valid values under one
// discriminator value could not be established — value-cycling exhausted every
// alternative without an accepted body — as an inconclusive validWhen edge. It
// asserts nothing; it makes the untested edge visible.
func (r *runner) recordConditionalInconclusive(entity *entityState, gateField, gateValue, subject string, ex observe.Excerpt) {
	if gateField == "" || gateValue == "" || subject == "" {
		return
	}
	cond := &observe.Condition{Attribute: gateField, Equals: gateValue}
	r.record(entity.plan.Entity, subject, observe.KindValidWhen, nil, cond, observe.OutcomeInconclusive, ex)
}

// gateFieldFor is the field the entity's strategy ranked as the likeliest
// discriminator, or "" on a non-strategy run.
func (r *runner) gateFieldFor(entity *entityState) string {
	if s := r.strategies[entity.plan.Entity]; s != nil && len(s.Gates) > 0 {
		return s.Gates[0].Field
	}
	return ""
}

// isConditionalRefusal reports whether a refusal the grammar could not classify
// nonetheless names a field the entity declares — a free-form conditional
// constraint the caller should treat as captured edge-evidence rather than a
// reason to block the whole entity.
func (r *runner) isConditionalRefusal(entity *entityState, res *httpResult) bool {
	if res == nil {
		return false
	}
	return len(namedKnownFields(refusalMessage(res.body), r.hints[entity.plan.Entity])) > 0
}

// namedKnownFields is the generalized field extraction: it scans a refusal the
// grammar could not classify for any field the entity declares, returning the
// ones it names. A real API phrases a value-conditional refusal in prose —
// "type: Dynamic tags are not supported for the provided object type" — that
// names the offending field without the "field X ..." shape classifyRefusal
// keys on; a message naming even one declared field is a conditional-constraint
// signal, not an unintelligible refusal.
//
// Matching is on word boundaries so a field is recognised as a whole token, not
// as a fragment of an unrelated word.
func namedKnownFields(message string, known map[string]strategy.SynthHint) []string {
	if message == "" || len(known) == 0 {
		return nil
	}
	low := strings.ToLower(message)
	var out []string
	for f := range known {
		if f != "" && containsWord(low, strings.ToLower(f)) {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// containsWord reports whether token appears in s delimited by non-alphanumeric
// boundaries, so "mode" matches "mode:" and "the mode" but not "model".
func containsWord(s, token string) bool {
	if token == "" {
		return false
	}
	from := 0
	for {
		i := strings.Index(s[from:], token)
		if i < 0 {
			return false
		}
		i += from
		before := i == 0 || !isWordByte(s[i-1])
		after := i+len(token) >= len(s) || !isWordByte(s[i+len(token)])
		if before && after {
			return true
		}
		from = i + 1
	}
}

// isWordByte reports whether b is part of a field-name token — a letter, digit
// or underscore — so a boundary is anything else.
func isWordByte(b byte) bool {
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}
