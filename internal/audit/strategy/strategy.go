// Package strategy is the per-resource strategy compiler: it reads one
// classified entity's declared spec structure and composes the ordered step
// program specific to that resource. Different resources yield different
// strategies —
// a flat resource gets a single baseline, a discriminated one gets a variant
// per gate value, a resource whose prose or dependentRequired declares
// co-requirements gets hypotheses to confirm live.
//
// Compile is pure and deterministic: the same document, classification and
// config always produce the same Strategy, byte for byte under JSON. No
// network, no clock, no randomness, no map-order leaks. It supersedes the
// uniform step derivation in internal/audit/plan — where plan applied one
// fixed sequence to every entity, strategy shapes the sequence to what the
// resource declares — and reuses plan's closed step-kind vocabulary
// (plan.StepKind) rather than minting a second copy of it.
//
// The compiler asserts nothing about the live API. Structural signals
// (oneOf/discriminator/dependentRequired) are the strongest baseline; prose in
// descriptions is the weakest, general, vendor-neutral hint; the live API,
// exercised in a later wave, is the only thing that confirms. Every finding
// leaves here as a hypothesis carrying its provenance, never an assertion.
//
// Deferred naming: the hypothesis-kind values (variant, requiredWhen,
// requiresField, mutuallyExclusive, validWhen) are working identifiers, and the
// final observation-kind names are an owner decision settled in Wave 3. The
// exported type names (Strategy, Variant, Skeleton, Hypothesis, Check, Step)
// are likewise provisional.
package strategy

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// Provenance records how strongly a variant or hypothesis is grounded. The
// set is closed and ordered weakest-last for sorting: structural claims (the
// document's own composition keywords) outrank prose (mined description text),
// and derived is reserved for the triangulating inference of a later wave.
type Provenance string

const (
	// ProvenanceStructural: read from the document's composition structure —
	// oneOf, anyOf, discriminator, dependentRequired, dependentSchemas.
	ProvenanceStructural Provenance = "structural"
	// ProvenanceProse: mined from a property description by the general
	// phrase extractor. The weakest signal; a hint to test, never a claim.
	ProvenanceProse Provenance = "prose"
	// ProvenanceDerived: a variant seeded from a gate value the document
	// gives no distinct field set for — inferred from the gate alone.
	ProvenanceDerived Provenance = "derived"
)

// HypothesisKind names the shape of a candidate conditional edge. These are
// WORKING IDENTIFIERS: the final observation-kind names are an owner decision
// settled in Wave 3, so nothing downstream should treat these spellings as the
// committed vocabulary.
type HypothesisKind string

const (
	// HypothesisVariant: a gate value selects a distinct valid field set.
	HypothesisVariant HypothesisKind = "variant"
	// HypothesisRequiredWhen: a field is required only when a sibling gate
	// holds a particular value.
	HypothesisRequiredWhen HypothesisKind = "requiredWhen"
	// HypothesisRequiresField: a field is settable only when a sibling is
	// present, regardless of that sibling's value.
	HypothesisRequiresField HypothesisKind = "requiresField"
	// HypothesisMutuallyExclusive: at most one of a set of fields may be set.
	HypothesisMutuallyExclusive HypothesisKind = "mutuallyExclusive"
	// HypothesisValidWhen: a field is valid (or prohibited) only when a
	// sibling gate holds a particular value.
	HypothesisValidWhen HypothesisKind = "validWhen"
)

// GateKind ranks a gate candidate by discriminating power: a required enum is
// the likeliest discriminator, an optional enum next, a boolean last.
type GateKind string

const (
	// GateRequiredEnum: a required enum-typed field.
	GateRequiredEnum GateKind = "requiredEnum"
	// GateOptionalEnum: an optional enum-typed field.
	GateOptionalEnum GateKind = "optionalEnum"
	// GateBool: a boolean field, required or optional.
	GateBool GateKind = "bool"
)

// rank orders gate kinds; a lower number is a likelier discriminator.
func (k GateKind) rank() int {
	switch k {
	case GateRequiredEnum:
		return 0
	case GateOptionalEnum:
		return 1
	case GateBool:
		return 2
	default:
		return 3
	}
}

// Gate is one candidate discriminator in the create body: a required-enum,
// optional-enum or boolean field whose value plausibly selects a variant.
type Gate struct {
	// Field is the gate's wire property name.
	Field string `json:"field"`
	// Kind ranks the gate by discriminating power.
	Kind GateKind `json:"kind"`
	// Values lists the gate's candidate values as the document declares them,
	// decoded and in document order: the enum members, or false and true for a
	// boolean. They keep their JSON type because they are sent on the wire —
	// a boolean gate pinned to the string "false" is a different request from
	// one pinned to false, and an API that accepts the second may refuse the
	// first.
	Values []any `json:"values"`
}

// SynthHint is the raw material the live executor synthesises a field's value
// from. It carries what the schema declares, never a value: value synthesis
// happens live, so a plan derived twice is identical and a field with no
// example is not guessed at offline.
type SynthHint struct {
	// Field is the wire property name.
	Field string `json:"field"`
	// Required reports whether the field is required in the flattened create
	// body.
	Required bool `json:"required"`
	// Type is the declared JSON type, empty when the schema does not say.
	Type string `json:"type,omitempty"`
	// Format is the declared string format.
	Format string `json:"format,omitempty"`
	// Pattern is the declared regular-expression constraint.
	Pattern string `json:"pattern,omitempty"`
	// Enum lists the declared enum values, decoded and in document order.
	//
	// Type and order both matter. These values go on the wire, so rendering
	// an integer enum as strings sends "120" where the document says 120; an
	// API that coerces it answers with the integer, the echo no longer matches
	// what was sent, and the run concludes the server forced a value of its
	// own. Sorting them as strings compounded it by making "120" the first
	// member of [60, 120, 300, 600, 900, 1800, 3600].
	Enum []any `json:"enum,omitempty"`
	// Example is the declared example value, decoded; nil when absent.
	Example any `json:"example,omitempty"`
	// Default is the declared default value, decoded; nil when absent.
	Default any `json:"default,omitempty"`
	// Minimum and Maximum are the declared numeric bounds; nil when absent.
	// A probe value outside them tells the API nothing about the field, only
	// about its validation.
	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`
}

// Skeleton is a field list plus the per-field synthesis hints for one shape of
// create body. Values are not synthesised here — the executor does that live —
// so a skeleton says which fields to send and what material to build each
// from, not what to send.
type Skeleton struct {
	// Fields is the sorted list of writable wire field names to send.
	Fields []string `json:"fields"`
	// Hints carries one synthesis hint per field, sorted by field.
	Hints []SynthHint `json:"hints"`
}

// Variant is one shape the resource's create body can take: the no-gate
// baseline (empty GateField and GateValue) or a gate value with the field set
// valid for it. Each carries a minimal skeleton (required writable fields for
// the variant) and a maximal skeleton (every writable field plausibly valid
// for it).
type Variant struct {
	// GateField and GateValue name the value that selects this variant; both
	// empty on the baseline.
	GateField string `json:"gateField,omitempty"`
	GateValue string `json:"gateValue,omitempty"`
	// Provenance records how the variant was seeded: structural for a
	// declared oneOf/discriminator branch and for the baseline, derived for a
	// gate value with no declared distinct field set.
	Provenance Provenance `json:"provenance"`
	// Minimal is the smallest valid body for this variant.
	Minimal Skeleton `json:"minimal"`
	// Maximal is the widest plausibly-valid body for this variant.
	Maximal Skeleton `json:"maximal"`
}

// Check describes the request that would confirm or refute a hypothesis: which
// step kind exercises it, the field it targets, the gate it pins, and what the
// API is expected to do. (Working name — see the package note on deferred
// naming; the retired term "probe" is deliberately avoided.)
type Check struct {
	// Step is the step kind that exercises the hypothesis.
	Step plan.StepKind `json:"step"`
	// Field is the field the check targets, empty when it is about a whole
	// variant.
	Field string `json:"field,omitempty"`
	// GateField and GateValue pin the gate the check holds, where the
	// hypothesis has one.
	GateField string `json:"gateField,omitempty"`
	GateValue string `json:"gateValue,omitempty"`
	// Expect is a human-readable statement of the expected outcome:
	// "accept", "reject" or "conditional".
	Expect string `json:"expect"`
}

// Hypothesis is one candidate conditional edge for the executor to confirm or
// refute live. Its kind is a working identifier (see HypothesisKind); its
// provenance says how strongly the document grounds it.
type Hypothesis struct {
	// Kind names the edge shape (a working identifier).
	Kind HypothesisKind `json:"kind"`
	// Subjects are the field(s) the edge is about, sorted.
	Subjects []string `json:"subjects"`
	// GateField and GateValue name the gate value the edge is conditioned on,
	// where the kind has one; empty otherwise.
	GateField string `json:"gateField,omitempty"`
	GateValue string `json:"gateValue,omitempty"`
	// Provenance is structural or prose.
	Provenance Provenance `json:"provenance"`
	// Check is the request that would confirm or refute the edge.
	Check Check `json:"check"`
}

// Step is one ordered step of the resource's step program. It reuses the
// approved step-kind names (plan.StepKind) but is composed per-resource:
// creates and reads repeat per variant, updates walk the writable fields, the
// negative and per-value steps target the gates.
type Step struct {
	// Kind is the approved step kind.
	Kind plan.StepKind `json:"kind"`
	// Field is the wire property under test, for per-field steps.
	Field string `json:"field,omitempty"`
	// GateField and GateValue pin a variant- or value-scoped step; empty on
	// a baseline or entity-level step.
	GateField string `json:"gateField,omitempty"`
	GateValue string `json:"gateValue,omitempty"`
}

// Budget is the per-resource request budget. Requests scales with complexity;
// Formula records how it was computed so a plan dump can show the arithmetic.
type Budget struct {
	Requests int    `json:"requests"`
	Formula  string `json:"formula"`
}

// Strategy is the compiled step program for one resource: the gate candidates
// found, the variants (baseline plus one per gate value), the hypotheses to
// confirm live, the ordered step program, and the scaled budget.
type Strategy struct {
	// Entity is the classified entity key the strategy is for.
	Entity string `json:"entity"`
	// Role is "resource" for a full lifecycle, "lookup" for a datasource
	// whose only access is the item read, "datasource" for a list-plus-read
	// datasource read read-only.
	Role string `json:"role"`
	// ReadOnly marks a strategy that never writes: its program is reads only.
	ReadOnly bool `json:"readOnly"`
	// Gates lists the gate candidates, ranked likeliest-first.
	Gates []Gate `json:"gates,omitempty"`
	// Variants lists the baseline and one variant per gate value.
	Variants []Variant `json:"variants,omitempty"`
	// Hypotheses lists the candidate conditional edges, sorted.
	Hypotheses []Hypothesis `json:"hypotheses,omitempty"`
	// Program is the ordered steps.
	Program []Step `json:"program"`
	// Budget is the per-resource request budget.
	Budget Budget `json:"budget"`
}

// Compile composes the strategy for one classified entity. It is pure: the
// same document, classification and config always produce the same Strategy.
//
// A resource (full create/read/delete lifecycle) yields a writing strategy —
// gates, variants, hypotheses and the full step program. An entity whose only
// access is a read yields a read-only strategy. An entity that is neither is an
// error: the caller decides what to audit, and a strategy for something with no
// exercisable surface would be an empty program masquerading as a plan.
func Compile(doc *specmodel.Document, class specmodel.Classification, cfg *config.Config) (*Strategy, error) {
	if doc == nil {
		return nil, fmt.Errorf("strategy: the document is nil")
	}
	if cfg == nil {
		return nil, fmt.Errorf("strategy: the config is nil")
	}

	if hasKind(class, specmodel.KindResource) {
		return compileResource(doc, class, cfg)
	}
	if class.Read != nil {
		return compileReadOnly(class), nil
	}
	return nil, fmt.Errorf("strategy: entity %q is not auditable: it has neither a create/read/delete lifecycle nor a readable item", class.Key)
}

// compileReadOnly builds the strategy for an entity the audit only reads: a
// lookup-by-key datasource, or a list-plus-read datasource. Nothing is
// created, so the program is the read and a consecutive read for volatility.
func compileReadOnly(class specmodel.Classification) *Strategy {
	role := "datasource"
	if class.LookupByKey {
		role = "lookup"
	}
	return &Strategy{
		Entity:   class.Key,
		Role:     role,
		ReadOnly: true,
		Program: []Step{
			{Kind: plan.StepRead},
			{Kind: plan.StepReadConsecutive},
		},
		Budget: Budget{
			Requests: readOnlyBudget,
			Formula:  "read-only: 2 reads",
		},
	}
}

// compileResource builds the writing strategy for a full-lifecycle resource.
func compileResource(doc *specmodel.Document, class specmodel.Classification, cfg *config.Config) (*Strategy, error) {
	createOp := findOp(doc, class.Create)
	if createOp == nil || createOp.RequestBody == nil {
		return nil, fmt.Errorf("strategy: resource %q has no create request body to compile against", class.Key)
	}
	createBody := createOp.RequestBody

	gates := detectGates(createBody)
	variants := deriveVariants(createBody, gates)
	hyps := deriveHypotheses(createBody, variants)

	s := &Strategy{
		Entity:     class.Key,
		Role:       "resource",
		Gates:      gates,
		Variants:   variants,
		Hypotheses: hyps,
	}
	s.Program = buildProgram(createBody, gates, variants, hyps)
	s.Budget = deriveBudget(s.Program, cfg)
	return s, nil
}

// JSON renders the strategy for printing and for a future `audit plan --print`
// dump: two-space indented, HTML escaping off, trailing newline. Deterministic
// for a given strategy — struct fields encode in declaration order and maps
// encode sorted.
func (s *Strategy) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, fmt.Errorf("encoding the strategy: %w", err)
	}
	return buf.Bytes(), nil
}

// findOp resolves a classification operation reference to the loaded
// operation, nil when the reference is nil or unresolvable.
func findOp(doc *specmodel.Document, ref *specmodel.Op) *specmodel.Operation {
	if ref == nil {
		return nil
	}
	for pi := range doc.Paths {
		p := &doc.Paths[pi]
		if p.Path != ref.Path {
			continue
		}
		for oi := range p.Operations {
			if p.Operations[oi].Method == ref.Method {
				return &p.Operations[oi]
			}
		}
	}
	return nil
}

// hasKind reports whether a classification yields the kind.
func hasKind(c specmodel.Classification, k specmodel.Kind) bool {
	for _, have := range c.Kinds {
		if have == k {
			return true
		}
	}
	return false
}
