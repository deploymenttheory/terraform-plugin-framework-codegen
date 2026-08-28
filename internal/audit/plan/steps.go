package plan

import (
	"sort"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// UndocumentedEnumValue is what the undocumented-value check sends: a
// string no sane document lists, recognisably the toolkit's.
const UndocumentedEnumValue = "tfpfgen-undocumented"

// UndeclaredSpecFieldName is the field the unknown-field check adds to an
// otherwise valid body.
const UndeclaredSpecFieldName = "tfpfgen_unknown_field"

// resourcePlan derives one resource's full lifecycle: create minimal,
// read with retry, read consecutive, per-field updates, delete with
// confirmation, create maximal, the negative checks, the per-enum-value
// creates, and the cleanup delete. The step order is fixed; the caps in
// derive.go bound each rule.
func (d *planBuilder) resourcePlan(c specmodel.Classification) (EntityPlan, *Skipped) {
	ei := d.inputs.forEntity(c.Key)
	sy := synth{entity: c.Key, prefix: d.configuration.Audit.NamePrefix, inputs: ei}

	createOp := d.operation(c.Create)
	readOp := d.operation(c.Read)
	updateOp := d.operation(c.Update)

	itemValues, parents, reason := d.pathValues(c.ItemPath, c.Key, ei)
	if reason != "" {
		return EntityPlan{}, &Skipped{Entity: c.Key, Reason: reason}
	}
	collectionValues, _, reason := d.pathValues(c.CollectionPath, "", ei)
	if reason != "" {
		return EntityPlan{}, &Skipped{Entity: c.Key, Reason: reason}
	}

	// A singleton is a resource with no create operation: it is written
	// through its update call, and the classification leaves the create slot
	// empty. Auditing that shape is not derived yet, so the entity is
	// refused rather than the run.
	if createOp == nil {
		return EntityPlan{}, &Skipped{
			Entity: c.Key,
			Reason: "the entity has no create operation to exercise: a singleton is written through its update call, which the audit does not derive",
		}
	}

	createSchema := createOp.RequestBody
	minimal, reason := sy.minimalBody(createSchema)
	if reason != "" {
		return EntityPlan{}, &Skipped{Entity: c.Key, Reason: reason}
	}

	post := func(kind StepKind, attribute string, body map[string]any) Step {
		return Step{
			Kind: kind, Method: c.Create.Method, Path: c.CollectionPath,
			Attribute: attribute, Body: body, PathValues: collectionValues,
		}
	}
	onItem := func(kind StepKind, method string) Step {
		return Step{Kind: kind, Method: method, Path: c.ItemPath, PathValues: itemValues}
	}

	steps := []Step{post(StepCreateMinimal, "", minimal)}

	readWithRetry := onItem(StepReadWithRetry, c.Read.Method)
	readWithRetry.Poll = pollSpec(readOp, createOp)
	steps = append(steps, readWithRetry, onItem(StepReadConsecutive, c.Read.Method))

	if updateOp != nil {
		steps = append(steps, d.updateSteps(c, sy, createSchema, minimal, itemValues)...)
	}

	steps = append(steps, onItem(StepDeleteWithConfirmation, c.Delete.Method))

	maximal, optional := sy.maximalBody(createSchema)
	createMaximal := post(StepCreateMaximal, "", maximal)
	createMaximal.BisectionAllowance = bisectionAllowance(optional)
	steps = append(steps, createMaximal)

	steps = append(steps, negativeSteps(createSchema, minimal, post)...)
	steps = append(steps, conditionalSteps(sy, createSchema, minimal, post)...)
	steps = append(steps, onItem(StepCleanupDelete, c.Delete.Method))

	return EntityPlan{
		Entity:             c.Key,
		Role:               "resource",
		Parents:            parents,
		DeclaredProperties: declaredProperties(createSchema, successSchemaOf(readOp)),
		Budget:             Budget{Requests: entityRequestBudget},
		Steps:              steps,
	}, nil
}

// lookupPlan derives a lookup datasource's read checks: fetch by the
// operator-supplied key, then read twice for volatility. Nothing is
// created, so a missing key is a graceful skip, not an error.
func (d *planBuilder) lookupPlan(c specmodel.Classification) (EntityPlan, *Skipped) {
	ei := d.inputs.forEntity(c.Key)
	values, parents, reason := d.pathValues(c.ItemPath, "", ei)
	if reason != "" {
		return EntityPlan{}, &Skipped{Entity: c.Key, Reason: reason}
	}
	read := Step{Kind: StepRead, Method: c.Read.Method, Path: c.ItemPath, PathValues: values}
	double := read
	double.Kind = StepReadConsecutive
	return EntityPlan{
		Entity:             c.Key,
		Role:               "lookup",
		Parents:            parents,
		DeclaredProperties: declaredProperties(successSchemaOf(d.operation(c.Read))),
		Budget:             Budget{Requests: entityRequestBudget},
		Steps:              []Step{read, double},
	}, nil
}

// successSchemaOf is a nil-tolerant Operation.SuccessSchema.
func successSchemaOf(operation *specmodel.Operation) *specmodel.Schema {
	if operation == nil {
		return nil
	}
	return operation.SuccessSchema()
}

// declaredProperties flattens the given schemas into the sorted union of
// their property names — what the executor diffs read responses against
// when it looks for a field the spec omits.
func declaredProperties(schemas ...*specmodel.Schema) []string {
	seen := map[string]bool{}
	for _, s := range schemas {
		if s == nil {
			continue
		}
		properties, _ := flatProps(s)
		for _, p := range properties {
			seen[p.Name] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// updateSteps derives one update per writable scalar field, capped. Each
// body is the minimal body with that one field moved to a variant value,
// so a refusal names the field and nothing else.
func (d *planBuilder) updateSteps(c specmodel.Classification, sy synth, schema *specmodel.Schema, minimal map[string]any, itemValues map[string]string) []Step {
	properties, _ := flatProps(schema)
	var steps []Step
	for _, p := range properties {
		if len(steps) == maxUpdateFields {
			break
		}
		if p.Schema.Resolved().ReadOnly {
			continue
		}
		base := minimal[p.Name]
		if base == nil {
			if v, ok := sy.value(p.Name, p.Schema, 0, true); ok {
				base = v
			}
		}
		variant, ok := sy.alternateValue(p.Name, p.Schema, base)
		if !ok {
			continue
		}
		body := cloneBody(minimal)
		body[p.Name] = variant
		steps = append(steps, Step{
			Kind: StepUpdateField, Method: c.Update.Method, Path: c.ItemPath,
			Attribute: p.Name, Body: body, PathValues: itemValues,
		})
	}
	return steps
}

// negativeSteps derives the checks that expect refusal: omit each
// required field (capped), send an undocumented enum value, send an
// unknown field.
func negativeSteps(schema *specmodel.Schema, minimal map[string]any, post func(StepKind, string, map[string]any) Step) []Step {
	properties, required := flatProps(schema)
	var steps []Step

	omitted := 0
	for _, p := range properties {
		if omitted == maxOmitRequired {
			break
		}
		if !required[p.Name] || minimal[p.Name] == nil {
			continue
		}
		body := cloneBody(minimal)
		delete(body, p.Name)
		steps = append(steps, post(StepOmitRequired, p.Name, body))
		omitted++
	}

	for _, p := range properties {
		r := p.Schema.Resolved()
		if r.ReadOnly || len(r.Enum) == 0 {
			continue
		}
		if _, isString := r.Enum[0].(string); !isString {
			continue
		}
		body := cloneBody(minimal)
		body[p.Name] = UndocumentedEnumValue
		steps = append(steps, post(StepUndocumentedEnumValue, p.Name, body))
		break
	}

	body := cloneBody(minimal)
	body[UndeclaredSpecFieldName] = true
	steps = append(steps, post(StepUndeclaredSpecField, "", body))
	return steps
}

// conditionalSteps derives the value-conditional creates, only where the
// document hints that behaviour varies: an x-tfpfgen-required-when
// annotation, or an enum-typed field with siblings whose behaviour could
// depend on it. Enum values are capped across the entity.
func conditionalSteps(sy synth, schema *specmodel.Schema, minimal map[string]any, post func(StepKind, string, map[string]any) Step) []Step {
	properties, _ := flatProps(schema)
	var steps []Step

	writable := 0
	for _, p := range properties {
		if !p.Schema.Resolved().ReadOnly {
			writable++
		}
	}

	// The required-when hints: for each annotated property, create with
	// the gate pinned and the property present, then with the gate pinned
	// and the property omitted — acceptance of one and refusal of the
	// other is the observation.
	for _, p := range properties {
		rw, ok := requiredWhenHint(p.Schema)
		if !ok {
			continue
		}
		with := cloneBody(minimal)
		with[rw.Property] = rw.Equals
		if v, ok := sy.value(p.Name, p.Schema, 0, true); ok {
			with[p.Name] = v
		}
		without := cloneBody(minimal)
		without[rw.Property] = rw.Equals
		delete(without, p.Name)

		for _, body := range []map[string]any{with, without} {
			step := post(StepCreatePerEnumValue, p.Name, body)
			step.Condition = &observe.Condition{Attribute: rw.Property, Equals: rw.Equals}
			steps = append(steps, step)
		}
	}

	// The enum-sibling hints: pin each documented value in turn, capped,
	// so value-conditional requirements and defaults surface. An enum
	// with no siblings conditions nothing.
	if writable > 1 {
		pinned := 0
		for _, p := range properties {
			r := p.Schema.Resolved()
			if r.ReadOnly || len(r.Enum) == 0 {
				continue
			}
			for _, v := range r.Enum {
				if pinned == maxConditionalValues {
					break
				}
				body := cloneBody(minimal)
				body[p.Name] = v
				step := post(StepCreatePerEnumValue, p.Name, body)
				step.Condition = &observe.Condition{Attribute: p.Name, Equals: v}
				steps = append(steps, step)
				pinned++
			}
		}
	}
	return steps
}

// requiredWhenHint reads x-tfpfgen-required-when from a property, checking
// the written node first — an annotation can sit beside a $ref — and the
// resolved schema second.
func requiredWhenHint(s *specmodel.Schema) (specmodel.RequiredWhen, bool) {
	if rw, ok := s.Extensions.RequiredWhen(); ok {
		return rw, true
	}
	return s.Resolved().Extensions.RequiredWhen()
}

// pollSpec bounds a readWithRetry: the document's eventual-consistency hint on
// the read or create operation when present, the default ceiling
// otherwise.
func pollSpec(readOp, createOp *specmodel.Operation) *Poll {
	timeout := defaultPollTimeout
	for _, operation := range []*specmodel.Operation{readOp, createOp} {
		if operation == nil {
			continue
		}
		if d, ok := operation.Extensions.EventualConsistency(); ok {
			timeout = d
			break
		}
	}
	return &Poll{Interval: pollInterval.String(), Timeout: timeout.String()}
}

// cloneBody is the shallow copy each step mutates: top-level fields are
// per-step, nested values are shared and never mutated.
func cloneBody(body map[string]any) map[string]any {
	out := make(map[string]any, len(body))
	for k, v := range body {
		out[k] = v
	}
	return out
}
