package strategy

import (
	"fmt"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// The step-kind names are the plan package's closed set, reused verbatim so
// there is one spelling of "createMinimal" in the toolkit, not two. When plan's
// uniform derivation is retired the set moves; until then strategy borrows it.
const (
	stepCreateMinimal          = plan.StepCreateMinimal
	stepReadWithRetry          = plan.StepReadWithRetry
	stepReadConsecutive        = plan.StepReadConsecutive
	stepUpdateField            = plan.StepUpdateField
	stepDeleteWithConfirmation = plan.StepDeleteWithConfirmation
	stepCreateMaximal          = plan.StepCreateMaximal
	stepOmitRequired           = plan.StepOmitRequired
	stepUndocumentedEnumValue  = plan.StepUndocumentedEnumValue
	stepUndeclaredSpecField    = plan.StepUndeclaredSpecField
	stepCreatePerEnumValue     = plan.StepCreatePerEnumValue
	stepRead                   = plan.StepRead
	stepCleanupDelete          = plan.StepCleanupDelete
)

// The program caps. Each bounds one rule, so a wide schema widens the program
// sub-linearly and the budget stays honest.
const (
	// perObjectCost is how many requests one live object is worth when the
	// per-entity request ceiling is derived from the object budget. It is the
	// safety guard on the request budget, not the budget itself: the budget is
	// summed from the program (deriveBudget) and only clamped to this ceiling.
	perObjectCost = 12
	// readOnlyBudget is a read-only entity's budget: a read and a consecutive
	// read.
	readOnlyBudget = 2
	// maxUpdateFields caps the per-field update checks.
	maxUpdateFields = 12
	// maxOmitRequired caps the omit-one-required negatives.
	maxOmitRequired = 6
	// maxPerEnumValues caps the value-conditional creates one gate spends.
	maxPerEnumValues = 6
)

// The per-step request reserves the entity budget is summed from. Each is the
// requests one step of that kind is expected to spend in a normal (non-
// pathological) run: the happy-path call plus headroom for what that step kind
// legitimately retries. The create-family kinds reserve the executor's adaptive
// adjustment loop; the read-family kinds reserve their poll/retry reads; the
// delete-with-confirmation kind reserves its confirm-and-repeat.
//
// The reserves are deliberately generous. Audit cost is expected to grow with a
// resource's complexity, and the guards against a runaway are the object ceiling
// above and the run-wide duration and rate limits — never a tight request cap. A
// budget too small to let a resource's whole program complete is the exhaustion
// this weighting exists to prevent: the multi-variant monitor's 33-step program
// spends ~92 requests live, which the old base(10)+fields×variants=38 could not
// cover, so it exhausted before completing.
const (
	// refineReserve is a create-family step's reserve: one create plus the
	// retries the adaptive loop makes as it adds, removes or borrows a field a
	// refusal named. It mirrors the executor's adjustment cap (maxAdjustIters in
	// internal/audit/run) so a body that needs several honest adjustments to be
	// accepted never exhausts the entity mid-program.
	refineReserve = 5
	// maximalReserve is a maximal create's reserve: the adaptive create, the
	// bounded bisection that narrows a refused optional field, and the delete
	// that levels the object afterward.
	maximalReserve = 6
	// updateReserve is a per-field update's reserve: the adaptive update and the
	// read-back that classifies what the API did with the field.
	updateReserve = 4
	// pollReserve is a read-with-retry's reserve: the reads it may spend polling
	// for the created object to become readable.
	pollReserve = 3
	// deleteConfirmReserve is a delete-with-confirmation's reserve: the delete,
	// the read that confirms it gone, and the second delete that observes the
	// 404 semantics.
	deleteConfirmReserve = 3
	// cleanupReserve is an end-of-entity cleanup's reserve: the deletes it may
	// spend clearing whatever objects the entity left unresolved.
	cleanupReserve = 3
	// negativeReserve is a single-shot negative create's reserve: the create and
	// the delete that follows an unexpected acceptance.
	negativeReserve = 2
	// preflightReserve is the one foreign-object pre-flight read every mutating
	// entity spends before its first create.
	preflightReserve = 1
)

// stepRequests is the request weight one program step of the given kind
// contributes to the entity budget. The default is the negative-create reserve:
// a kind the budget does not specially recognise still gets create-shaped
// headroom rather than a bare single request.
func stepRequests(kind plan.StepKind) int {
	switch kind {
	case stepCreateMinimal, stepCreatePerEnumValue:
		return refineReserve
	case stepCreateMaximal:
		return maximalReserve
	case stepUpdateField:
		return updateReserve
	case stepReadWithRetry:
		return pollReserve
	case stepReadConsecutive:
		return 2
	case stepRead:
		return 1
	case stepOmitRequired, stepUndocumentedEnumValue, stepUndeclaredSpecField:
		return negativeReserve
	case stepDeleteWithConfirmation:
		return deleteConfirmReserve
	case stepCleanupDelete:
		return cleanupReserve
	default:
		return negativeReserve
	}
}

// buildProgram composes the ordered steps for a resource, shaped by its
// variants, gates and hypotheses. The order is fixed and every rule iterates a
// sorted set, so the program is byte-stable.
func buildProgram(createBody *specmodel.Schema, gates []Gate, variants []Variant, hyps []Hypothesis) []Step {
	fields := flatFields(createBody)
	var prog []Step

	// Create, then read back and read again, per variant.
	for _, v := range variants {
		prog = append(prog,
			gatedStep(stepCreateMinimal, v),
			gatedStep(stepReadWithRetry, v),
			gatedStep(stepReadConsecutive, v),
		)
	}
	// The widest valid body, per variant.
	for _, v := range variants {
		prog = append(prog, gatedStep(stepCreateMaximal, v))
	}
	// One update per writable field, capped.
	for i, name := range fieldNames(fields) {
		if i == maxUpdateFields {
			break
		}
		prog = append(prog, Step{Kind: stepUpdateField, Field: name})
	}
	// The negatives: omit each required field, send an undocumented value at
	// each enum gate, send one undeclared field.
	for i, name := range requiredNames(fields) {
		if i == maxOmitRequired {
			break
		}
		prog = append(prog, Step{Kind: stepOmitRequired, Field: name})
	}
	for _, g := range gates {
		if g.Kind == GateBool {
			continue
		}
		prog = append(prog, Step{Kind: stepUndocumentedEnumValue, Field: g.Field})
	}
	prog = append(prog, Step{Kind: stepUndeclaredSpecField})

	// Value-conditional creates: every gate value, then the value-gated
	// prose hypotheses the gate loop did not already cover.
	prog = append(prog, perValueSteps(gates, hyps)...)

	// Teardown.
	prog = append(prog,
		Step{Kind: stepDeleteWithConfirmation},
		Step{Kind: stepCleanupDelete},
	)
	return prog
}

// perValueSteps builds the createPerEnumValue steps: one per gate value
// (capped per gate), plus one per value-gated prose hypothesis whose gate
// value the gate loop did not already pin.
func perValueSteps(gates []Gate, hyps []Hypothesis) []Step {
	var steps []Step
	covered := map[string]bool{}
	for _, g := range gates {
		for i, value := range g.Values {
			if i == maxPerEnumValues {
				break
			}
			// A step carries the gate value as a label; the executor restores
			// its declared type from the field's hint before sending it.
			label := stringifyScalar(value)
			steps = append(steps, Step{Kind: stepCreatePerEnumValue, Field: g.Field, GateField: g.Field, GateValue: label})
			covered[g.Field+"\x00"+label] = true
		}
	}
	for _, h := range hyps {
		if h.GateField == "" || h.GateValue == "" {
			continue
		}
		if h.Kind != HypothesisRequiredWhen && h.Kind != HypothesisValidWhen {
			continue
		}
		key := h.GateField + "\x00" + h.GateValue
		if covered[key] {
			continue
		}
		covered[key] = true
		subject := ""
		if len(h.Subjects) > 0 {
			subject = h.Subjects[0]
		}
		steps = append(steps, Step{Kind: stepCreatePerEnumValue, Field: subject, GateField: h.GateField, GateValue: h.GateValue})
	}
	return steps
}

// gatedStep builds a step carrying a variant's gate, leaving the gate empty on
// the baseline variant.
func gatedStep(kind plan.StepKind, v Variant) Step {
	return Step{Kind: kind, GateField: v.GateField, GateValue: v.GateValue}
}

// deriveBudget sizes the per-resource request budget from the resource's actual
// step program: the one-time pre-flight read plus the summed per-step request
// reserve of every step the program will run. Because the program grows with the
// resource's variants, writable fields and gates, so does the budget — a
// multi-variant resource is budgeted for the many creates, reads, updates and
// per-value probes its program actually contains, each with headroom for the
// adaptive adjustment loop and its poll/confirm reads.
//
// The result is clamped to a ceiling drawn from the configured live-object
// budget (maxObjects × perObjectCost). That ceiling is the safety guard against
// a pathologically wide schema, not the budget itself; a resource of ordinary
// complexity stays well under it. The run-wide duration and rate limits are the
// other guards. The formula string records the arithmetic for a plan dump.
//
//	requests = preflight + Σ step-request reserve over the program
//	           (capped at maxObjects × perObjectCost)
func deriveBudget(program []Step, cfg *config.Config) Budget {
	requests := preflightReserve
	for i := range program {
		requests += stepRequests(program[i].Kind)
	}

	maxObjects := cfg.Audit.MaxObjects
	if maxObjects < 1 {
		maxObjects = 25
	}
	ceiling := maxObjects * perObjectCost

	capped := ""
	if requests > ceiling {
		requests = ceiling
		capped = fmt.Sprintf(", capped at maxObjects(%d)×%d", maxObjects, perObjectCost)
	}
	return Budget{
		Requests: requests,
		Formula:  fmt.Sprintf("preflight(%d) + Σ step-request reserve over %d program steps%s", preflightReserve, len(program), capped),
	}
}
