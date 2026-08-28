package run

// applyStrategies is the bridge from the per-resource strategy compiler to the
// executable plan the runner consumes. For every entity the addressing plan
// already resolved — paths, path values, parent and self references — it
// compiles a strategy.Strategy from the document and swaps in a step program
// shaped by that strategy: creates and reads repeated per variant, the widest
// body per variant, the negatives and per-value creates the gates imply, all
// under a complexity-scaled budget. The addressing is reused verbatim; only
// the program, the request bodies and the per-entity request budget change.
//
// Field values are synthesised here, at run start, from the strategy's
// per-field SynthHints — never baked into the compiled strategy, which stays a
// pure, value-free description of the program. That is what "the executor
// synthesises live" means in practice: the compiler says which fields and what
// material, this step turns the material into a body carrying the run's
// prefix and run-id placeholder.

import (
	"encoding/json"
	"fmt"
	"math/bits"
	"reflect"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// applyStrategies rewrites every entity of p to execute its compiled strategy,
// returning the new plan, the per-entity field synthesis hints the adjustment
// loop draws on when it has to add a field live, and the compiled strategies
// themselves, which the triangulating inference reads for the claims each
// run was meant to confirm. The input plan is left untouched: a new plan is
// built so the caller's copy is never mutated.
func applyStrategies(p *plan.Plan, document *specmodel.Document, configuration *config.Config, prefix string, inputs *plan.Inputs) (*plan.Plan, map[string]map[string]strategy.SyntheticValueRules, map[string]*strategy.Strategy, map[string]bodySynthesis) {
	cls := specmodel.Classify(document)
	byKey := make(map[string]specmodel.Classification, len(cls.Entities))
	for _, c := range cls.Entities {
		byKey[c.Key] = c
	}

	out := &plan.Plan{Skipped: p.Skipped, Budget: p.Budget}
	hints := map[string]map[string]strategy.SyntheticValueRules{}
	strategies := map[string]*strategy.Strategy{}
	syntheses := map[string]bodySynthesis{}
	references := referenceCollections(cls.Entities)
	total := 0
	for i := range p.Entities {
		ep := p.Entities[i]
		class, ok := byKey[ep.Entity]
		if !ok {
			out.Entities = append(out.Entities, ep)
			total += ep.Budget.Requests
			continue
		}
		compiled, err := strategy.Compile(document, class, configuration)
		if err != nil {
			out.Entities = append(out.Entities, ep)
			total += ep.Budget.Requests
			continue
		}
		addr := addressingOf(&ep)
		composites, attested := plan.CompositeValues(document, class, prefix)
		synthesis := bodySynthesis{
			entity:             ep.Entity,
			prefix:             prefix,
			values:             inputs.ValuesFor(ep.Entity),
			composites:         composites,
			attestedComposites: attested,
			references:         references,
		}
		ep.Steps = translateProgram(compiled, addr, synthesis)
		ep.Budget = plan.Budget{Requests: compiled.Budget.Requests}
		hints[ep.Entity] = collectHints(compiled)
		strategies[ep.Entity] = compiled
		syntheses[ep.Entity] = synthesis
		out.Entities = append(out.Entities, ep)
		total += compiled.Budget.Requests
	}
	// The run-wide request ceiling becomes the sum of the strategy budgets;
	// the object and duration ceilings stay as the config derived them.
	out.Budget.Requests = total
	return out, hints, strategies, syntheses
}

// bodySynthesis is what one entity's request bodies are synthesised from at
// run start: the operator's values, the composite values the document's
// nested schemas yield, and the collections a reference field can borrow a
// real id from.
type bodySynthesis struct {
	entity string
	prefix string
	// values are the operator's overrides, keyed by top-level wire name. They
	// outrank every synthesis and are never bound as references.
	values map[string]any
	// composites are the array- and object-typed fields synthesised from the
	// document, keyed by top-level wire name, each carrying its required
	// members alone; the executor's own synthesis reaches no deeper than a
	// scalar. attestedComposites are the same fields widened to the members
	// the document states a value for, which the adjustment loop swaps in
	// when the smaller form is refused.
	composites         map[string]any
	attestedComposites map[string]any
	// references maps the noun a collection path spells to that path, for
	// binding id-named fields to a borrow token.
	references map[string]string
}

// addressing is everything the translator needs to place a strategy step onto
// the wire: methods, paths, and the already-resolved path values.
type addressing struct {
	createMethod     string
	collectionPath   string
	collectionValues map[string]string
	readMethod       string
	itemPath         string
	itemValues       map[string]string
	updateMethod     string
	deleteMethod     string
	deleteQuery      map[string]string
	poll             *plan.Poll
}

// addressingOf extracts the resolved addressing from an entity's original
// (uniform) step list. Every method and path the strategy program needs is
// already present there, resolved against parents and inputs.
func addressingOf(ep *plan.EntityPlan) addressing {
	var a addressing
	setItem := func(path string, values map[string]string) {
		if a.itemPath == "" {
			a.itemPath = path
			a.itemValues = values
		}
	}
	for i := range ep.Steps {
		s := &ep.Steps[i]
		switch s.Kind {
		case plan.StepCreateMinimal, plan.StepCreateMaximal:
			if a.collectionPath == "" {
				a.createMethod = s.Method
				a.collectionPath = s.Path
				a.collectionValues = s.PathValues
			}
		case plan.StepReadWithRetry:
			a.readMethod = s.Method
			a.poll = s.Poll
			setItem(s.Path, s.PathValues)
		case plan.StepRead, plan.StepReadConsecutive:
			if a.readMethod == "" {
				a.readMethod = s.Method
			}
			setItem(s.Path, s.PathValues)
		case plan.StepUpdateField:
			a.updateMethod = s.Method
			setItem(s.Path, s.PathValues)
		case plan.StepDeleteWithConfirmation, plan.StepCleanupDelete:
			a.deleteMethod = s.Method
			a.deleteQuery = s.Query
			setItem(s.Path, s.PathValues)
		}
	}
	if a.readMethod == "" {
		a.readMethod = "GET"
	}
	if a.deleteMethod == "" {
		a.deleteMethod = "DELETE"
	}
	return a
}

// entityValues indexes the operator's value overrides by entity, for every
// entity the plan carries.
//
// The adjustment loop reads them as well as the translator: a field the API
// forces into a body live is the same field the operator supplied a value for,
// and synthesising a different one there would send two values for one field
// across a single run.
func entityValues(p *plan.Plan, inputs *plan.Inputs) map[string]map[string]any {
	if p == nil || inputs == nil {
		return nil
	}
	out := map[string]map[string]any{}
	for i := range p.Entities {
		if v := inputs.ValuesFor(p.Entities[i].Entity); len(v) > 0 {
			out[p.Entities[i].Entity] = v
		}
	}
	return out
}

// translateProgram turns a strategy's ordered, value-free program into
// executable steps: addressing from addr, request bodies synthesised from the variant
// skeletons and per-field hints.
func translateProgram(compiled *strategy.Strategy, addr addressing, synthesis bodySynthesis) []plan.Step {
	entity := synthesis.entity
	baseMinimal := map[string]any{}
	if len(compiled.Variants) > 0 {
		baseMinimal = synthesis.requestBody(compiled.Variants[0].Minimal, "", "")
	}
	hints := collectHints(compiled)

	steps := make([]plan.Step, 0, len(compiled.Program))
	for _, s := range compiled.Program {
		switch s.Kind {
		case plan.StepCreateMinimal:
			v := findVariant(compiled, s.GateField, s.GateValue)
			steps = append(steps, plan.Step{
				Kind: s.Kind, Method: addr.createMethod, Path: addr.collectionPath,
				PathValues: addr.collectionValues,
				Body:       synthesis.requestBody(v.Minimal, s.GateField, s.GateValue),
			})
		case plan.StepCreateMaximal:
			v := findVariant(compiled, s.GateField, s.GateValue)
			body := synthesis.requestBody(v.Maximal, s.GateField, s.GateValue)
			steps = append(steps, plan.Step{
				Kind: s.Kind, Method: addr.createMethod, Path: addr.collectionPath,
				PathValues: addr.collectionValues, Body: body,
				FieldNarrowingAttemptLimit: fieldNarrowingAttemptLimit(countOptional(v, body)),
			})
		case plan.StepReadWithRetry:
			steps = append(steps, plan.Step{
				Kind: s.Kind, Method: addr.readMethod, Path: addr.itemPath,
				PathValues: addr.itemValues, Poll: addr.poll,
			})
		case plan.StepRead, plan.StepReadConsecutive:
			steps = append(steps, plan.Step{
				Kind: s.Kind, Method: addr.readMethod, Path: addr.itemPath, PathValues: addr.itemValues,
			})
		case plan.StepUpdateField:
			if step, ok := updateStep(s.Field, addr, entity, hints, baseMinimal); ok {
				steps = append(steps, step)
			}
		case plan.StepOmitRequired:
			body := cloneAnyMap(baseMinimal)
			delete(body, s.Field)
			steps = append(steps, collectionStep(s.Kind, s.Field, addr, body, nil))
		case plan.StepUndocumentedEnumValue:
			body := cloneAnyMap(baseMinimal)
			body[s.Field] = plan.UndocumentedEnumValue
			steps = append(steps, collectionStep(s.Kind, s.Field, addr, body, nil))
		case plan.StepUndeclaredSpecField:
			body := cloneAnyMap(baseMinimal)
			body[plan.UndeclaredSpecFieldName] = true
			steps = append(steps, collectionStep(s.Kind, "", addr, body, nil))
		case plan.StepCreatePerEnumValue:
			steps = append(steps, perValueStep(s, addr, hints, baseMinimal))
		case plan.StepDeleteWithConfirmation, plan.StepCleanupDelete:
			steps = append(steps, plan.Step{
				Kind: s.Kind, Method: addr.deleteMethod, Path: addr.itemPath, PathValues: addr.itemValues,
				Query: addr.deleteQuery,
			})
		}
	}
	return steps
}

// collectionStep builds a create-family step against the collection path.
func collectionStep(kind plan.StepKind, attribute string, addr addressing, body map[string]any, cond *observe.Condition) plan.Step {
	return plan.Step{
		Kind: kind, Method: addr.createMethod, Path: addr.collectionPath,
		PathValues: addr.collectionValues, Attribute: attribute, Body: body, Condition: cond,
	}
}

// updateStep builds a one-field update to a distinct value, or reports that
// the field has no second value worth sending (a single-value enum, say).
func updateStep(field string, addr addressing, entity string, hints map[string]strategy.SyntheticValueRules, baseMinimal map[string]any) (plan.Step, bool) {
	if addr.updateMethod == "" {
		// The resource declares no update operation, so there is no method to
		// send a field update with. Probing one anyway would issue a
		// method-less request (which defaults to GET) and mislabel the read as
		// an accepted-but-ignored update. Skip update probing entirely.
		return plan.Step{}, false
	}
	h, ok := hints[field]
	if !ok {
		return plan.Step{}, false
	}
	base := baseMinimal[field]
	if base == nil {
		base = synthesiseValue(h, entity, "")
	}
	// A reference is resolved at send time to whatever id the collection
	// serves first; a second value would need a second live object, and
	// varying the token text would only spell a path that does not exist.
	if s, ok := base.(string); ok && strings.HasPrefix(s, "$") {
		return plan.Step{}, false
	}
	variant, ok := variantValue(h, base)
	if !ok {
		return plan.Step{}, false
	}
	body := cloneAnyMap(baseMinimal)
	body[field] = variant
	return plan.Step{
		Kind: plan.StepUpdateField, Method: addr.updateMethod, Path: addr.itemPath,
		PathValues: addr.itemValues, Attribute: field, Body: body,
	}, true
}

// perValueStep builds a createPerEnumValue: the baseline body with the gate
// pinned to the value under test, scoped by the condition the observation will
// carry.
func perValueStep(s strategy.Step, addr addressing, hints map[string]strategy.SyntheticValueRules, baseMinimal map[string]any) plan.Step {
	body := cloneAnyMap(baseMinimal)
	if s.GateField != "" {
		body[s.GateField] = typedGate(hints[s.GateField], s.GateValue)
	}
	cond := &observe.Condition{Attribute: s.GateField, Equals: typedGate(hints[s.GateField], s.GateValue)}
	return collectionStep(plan.StepCreatePerEnumValue, s.Field, addr, body, cond)
}

// findVariant returns the variant a gated step selects, or the baseline when
// the step carries no gate.
func findVariant(compiled *strategy.Strategy, gateField, gateValue string) strategy.Variant {
	for _, v := range compiled.Variants {
		if v.GateField == gateField && v.GateValue == gateValue {
			return v
		}
	}
	if len(compiled.Variants) > 0 {
		return compiled.Variants[0]
	}
	return strategy.Variant{}
}

// collectHints indexes every field the strategy knows about by name, drawn
// from the widest (maximal) skeleton of each variant so a field valid only
// under one gate is still synthesisable when a refusal demands it.
func collectHints(compiled *strategy.Strategy) map[string]strategy.SyntheticValueRules {
	out := map[string]strategy.SyntheticValueRules{}
	for _, v := range compiled.Variants {
		for _, h := range v.Maximal.Rules {
			if _, ok := out[h.Field]; !ok {
				out[h.Field] = h
			}
		}
		for _, h := range v.Minimal.Rules {
			if _, ok := out[h.Field]; !ok {
				out[h.Field] = h
			}
		}
	}
	return out
}

// countOptional counts how many of a maximal body's fields are optional, for
// sizing the bisection allowance.
func countOptional(v strategy.Variant, body map[string]any) int {
	required := map[string]bool{}
	for _, f := range v.Minimal.Fields {
		required[f] = true
	}
	n := 0
	for f := range body {
		if !required[f] {
			n++
		}
	}
	return n
}

// requestBody synthesises a create body from a skeleton: one value per
// field, drawn from the operator's inputs where they name it and from that
// field's hint otherwise, with the gate field pinned to the variant's value
// where one is given, and every reference the document can satisfy bound to
// a borrow token.
//
// An operator value outranks everything, including the gate: it is supplied
// precisely for the fields no synthesis can guess — a reachable endpoint, an
// existing agent's id, the discriminator a polymorphic body is keyed on.
// Scoped to the body's own fields, as the plan's synthesis is, because a
// wire property name says nothing about which nested object it belongs to.
func (b bodySynthesis) requestBody(sk strategy.RequestFields, gateField, gateValue string) map[string]any {
	byField := make(map[string]strategy.SyntheticValueRules, len(sk.Rules))
	for _, h := range sk.Rules {
		byField[h.Field] = h
	}
	body := map[string]any{}
	supplied := map[string]bool{}
	for _, f := range sk.Fields {
		if v, ok := b.values[f]; ok {
			body[f] = v
			supplied[f] = true
			continue
		}
		if f == gateField && gateValue != "" {
			body[f] = typedGate(byField[f], gateValue)
			continue
		}
		if h, ok := byField[f]; ok {
			body[f] = b.fieldValue(h)
		}
	}
	return bindReferences(body, "", b.references, supplied).(map[string]any)
}

// fieldValue synthesises one field: the document-derived composite where the
// field is an array or object, the scalar synthesis otherwise.
func (b bodySynthesis) fieldValue(h strategy.SyntheticValueRules) any {
	if composite, ok := b.composites[h.Field]; ok {
		return cloneAny(composite)
	}
	return synthesiseValue(h, b.entity, b.prefix)
}

// attestedValue answers the widened form of one composite field, bound to
// its references, and false where the field has none or the widened form is
// the same as the smaller one.
func (b bodySynthesis) attestedValue(field string) (any, bool) {
	wider, ok := b.attestedComposites[field]
	if !ok {
		return nil, false
	}
	if smaller, has := b.composites[field]; has && reflect.DeepEqual(smaller, wider) {
		return nil, false
	}
	return bindReferences(cloneAny(wider), field, b.references, nil), true
}

// synthesiseField synthesises one field the adjustment loop must add live, from its
// strategy hint when known and from its name and a string default otherwise.
func (r *runner) synthesiseField(entity *entityState, field string) any {
	if v, ok := r.inputValues[entity.plan.Entity][field]; ok {
		return v
	}
	synthesis := r.syntheses[entity.plan.Entity]
	if hints := r.hints[entity.plan.Entity]; hints != nil {
		if h, ok := hints[field]; ok {
			return bindReferences(synthesis.fieldValue(h), field, synthesis.references, nil)
		}
	}
	if plan.NameBearing(field) {
		return nameToken(r.opts.NamePrefix, entity.plan.Entity, field)
	}
	return bindReferences("sample-"+field, field, synthesis.references, nil)
}

// cloneAny copies a synthesised value deeply enough that the adjustment
// loop's edits to one body never reach another built from the same
// composite.
func cloneAny(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, inner := range v {
			out[k] = cloneAny(inner)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneAny(v[i])
		}
		return out
	default:
		return value
	}
}

// synthesiseValue derives one field's value from its hint. The priority mirrors the
// plan package's synthesis: example, then default, then the first enum member,
// then a format-driven value, then a type-driven one.
func synthesiseValue(h strategy.SyntheticValueRules, entity, prefix string) any {
	// A name must be unique per run and must carry the prefix cleanup matches
	// on, so the invented token beats a declared example. An example is a
	// value the API accepted once; an API that requires a name to be unique
	// refuses it every time after, and an object created under it is invisible
	// to the prefix pass and stays in the tenant. An enum, a format or a
	// pattern leaves the ordinary priority alone: the token satisfies none of
	// those shapes, so a field carrying one is not a field to invent a name
	// for.
	if plan.NameBearing(h.Field) && len(h.Enum) == 0 && h.Format == "" && h.Pattern == "" {
		return nameToken(prefix, entity, h.Field)
	}
	if h.Example != nil {
		return h.Example
	}
	if h.Default != nil {
		return h.Default
	}
	if len(h.Enum) > 0 {
		return h.Enum[0]
	}
	if v, ok := formatValue(h.Format, entity, prefix); ok {
		return v
	}
	switch h.Type {
	case "boolean":
		return true
	case "integer":
		return 1
	case "number":
		return 1.5
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	case "string":
		if plan.NameBearing(h.Field) {
			return nameToken(prefix, entity, h.Field)
		}
		return "sample-" + h.Field
	default:
		if plan.NameBearing(h.Field) {
			return nameToken(prefix, entity, h.Field)
		}
		return "sample-" + h.Field
	}
}

// variantValue derives a second, distinct value for an update — scalars only,
// because updating one element of a nested value is a claim about the nested
// field, not the attribute.
// The value must be one the document says is acceptable. A probe the schema
// itself forbids — a string into an integer enum, a number under the declared
// minimum — tests the API's validation rather than the field, and whatever
// comes back is then read as behaviour: a coerced echo becomes "the server
// forced its own value", an unchanged field becomes "silently ignored on
// update". Both are conclusions about the probe.
func variantValue(h strategy.SyntheticValueRules, base any) (any, bool) {
	if len(h.Enum) > 0 {
		// Compared as text so a float64 from a decoded body matches an int
		// from the document; returned as declared so the wire value keeps the
		// type the schema gave it.
		bs := fmt.Sprint(base)
		for _, e := range h.Enum {
			if fmt.Sprint(e) != bs {
				return e, true
			}
		}
		return nil, false
	}
	switch h.Type {
	case "boolean":
		if b, ok := base.(bool); ok {
			return !b, true
		}
		return false, true
	case "integer":
		if v, ok := numericVariant(h, base); ok {
			return int64(v), true
		}
		return nil, false
	case "number":
		if v, ok := numericVariant(h, base); ok {
			return v, true
		}
		return nil, false
	case "string":
		if s, ok := base.(string); ok {
			return s + "-2", true
		}
		return "sample-" + h.Field + "-2", true
	default:
		return nil, false
	}
}

// numericVariant moves a number one step away from its current value and keeps
// it inside the declared bounds, stepping the other way when the first
// direction would leave them. It gives up rather than send a value the
// document forbids: a field pinned to a single legal value has no variant, and
// probing it anyway only proves the API enforces its own schema.
func numericVariant(h strategy.SyntheticValueRules, base any) (float64, bool) {
	current, ok := asFloat(base)
	if !ok {
		// No current value to move away from: start from the low bound, or
		// from a value no bound excludes.
		switch {
		case h.Minimum != nil:
			current = *h.Minimum
		case h.Maximum != nil:
			current = *h.Maximum - 1
		default:
			return 2, true
		}
		if inBounds(h, current) {
			return current, true
		}
		return 0, false
	}
	for _, cand := range []float64{current + 1, current - 1} {
		if inBounds(h, cand) {
			return cand, true
		}
	}
	return 0, false
}

func inBounds(h strategy.SyntheticValueRules, v float64) bool {
	if h.Minimum != nil && v < *h.Minimum {
		return false
	}
	if h.Maximum != nil && v > *h.Maximum {
		return false
	}
	return true
}

// asFloat reads a JSON-decoded or document-decoded number as a float.
func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	}
	return 0, false
}

// typedGate converts a gate value string to the type the gate field declares,
// so a boolean gate is pinned with a JSON boolean and an integer gate with a
// number.
func typedGate(h strategy.SyntheticValueRules, value string) any {
	switch h.Type {
	case "boolean":
		return value == "true"
	case "integer":
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	case "number":
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return value
}

// formatValue derives a value from a declared string format. Constants
// throughout — a date-time is a fixed instant, not the clock — so nothing here
// varies between runs except the substituted run id.
func formatValue(format, entity, prefix string) (any, bool) {
	_ = entity
	switch format {
	case "email":
		return prefix + "-" + plan.RunIDToken + "@example.invalid", true
	case "uri", "url":
		return "https://example.invalid/" + prefix, true
	case "uuid":
		return "00000000-0000-4000-8000-000000000000", true
	case "date-time":
		return "2026-01-01T00:00:00Z", true
	case "date":
		return "2026-01-01", true
	case "hostname":
		return "example.invalid", true
	case "ipv4":
		return "192.0.2.1", true
	default:
		return nil, false
	}
}

// nameToken is the value of a synthesised name-bearing string: the prefix, the
// run-id placeholder and the attribute's address, so a created object is
// recognisable as audit debris and unique per attribute.
func nameToken(prefix, entity, field string) string {
	return prefix + "-" + plan.RunIDToken + "-" + entity + "-" + field
}

// fieldNarrowingAttemptLimit is the extra createMaximal attempts worth reserving to
// halve the optional set down to one field, plus the retry that confirms it.
func fieldNarrowingAttemptLimit(optional int) int {
	if optional == 0 {
		return 0
	}
	return bits.Len(uint(optional-1)) + 1
}
