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
	// budgetBase is the fixed request cost every resource pays: the baseline
	// create/read/read cycle, the negatives, and teardown.
	budgetBase = 10
	// perObjectCost is how many requests one live object is worth when the
	// request ceiling is derived from the object budget.
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
			steps = append(steps, Step{Kind: stepCreatePerEnumValue, Field: g.Field, GateField: g.Field, GateValue: value})
			covered[g.Field+"\x00"+value] = true
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

// deriveBudget sizes the per-resource request budget with complexity: a fixed
// base plus the writable-field count times the variant count, capped by a
// ceiling drawn from the configured live-object budget. The formula string
// records the arithmetic for a plan dump.
//
//	requests = base + writableFields × variants   (capped at maxObjects × perObjectCost)
func deriveBudget(createBody *specmodel.Schema, variants []Variant, cfg *config.Config) Budget {
	writable := len(flatFields(createBody))
	nVariants := len(variants)
	requests := budgetBase + writable*nVariants

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
		Formula:  fmt.Sprintf("base(%d) + writableFields(%d)×variants(%d)%s", budgetBase, writable, nVariants, capped),
	}
}
