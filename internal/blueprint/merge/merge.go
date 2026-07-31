// Package merge folds probe facts into a blueprint.
//
// # Precedence
//
// Four layers, lowest first: what ingest inferred from the specification, what the prober
// observed, what a human curated, and an explicit override. **Curated outranks observed,
// deliberately.** Somebody who wrote "computed_optional because the API assigns a default"
// made a decision, and a probe does not overrule it silently.
//
// But it is not ignored either. Where a fact contradicts the curated blueprint, the
// disagreement becomes a Conflict: the evidence, the suggested change, and why it was not
// applied. That is the whole design -- merge widens, annotates and records, and everything else
// is a conversation with a person.
//
// # What merge may do unaided
//
//   - Always write attribute.behaviour.*. Nothing else writes those fields, so no conflict is
//     possible. This is the bulk of the value and it is entirely safe.
//   - Turn policy.readBack on, never off, and never lower its retries. The confidence in that
//     fact is asymmetric: seeing a stale read proves inconsistency, while one fast success
//     proves nothing.
//   - Set policy.delete.notFoundIsSuccess in either direction -- error handling rather than
//     schema, and a wrong answer is recoverable.
//   - Rewrite an attribute's description inside a regenerable marker.
//   - Change presence only under StrategyApply, only from an observed-or-better fact, and only
//     in a widening direction.
//
// # Why the safe direction matters
//
// Widening cannot break an existing configuration: anything that worked before still works.
// Narrowing can. So optional to computed_optional is automatic and the reverse is a conflict,
// and adding or removing RequiresReplace is *never* automatic in either direction -- one
// destroys infrastructure and the other leaves resources unappliable.
package merge

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
)

// Strategy is how much merge is allowed to change.
type Strategy string

const (
	// StrategyAnnotate writes behaviour and descriptions only. The default, because it
	// cannot change what a practitioner may write.
	StrategyAnnotate Strategy = "annotate"
	// StrategyApply additionally changes presence, in the widening direction only.
	StrategyApply Strategy = "apply"
)

// ErrConflicts marks a merge with unresolved disagreements.
var ErrConflicts = errors.New("probe facts disagree with the curated blueprint")

// Conflict is a fact that contradicts the blueprint and was not applied.
//
// Every field is there so a human can act without going back to the evidence themselves: what
// the blueprint says, what was observed, what the fact would suggest, why it was refused, and
// which interactions support it.
type Conflict struct {
	Resource string `json:"resource"`
	JSONPath string `json:"jsonPath,omitempty"`

	// Curated is what the blueprint currently says.
	Curated string `json:"curated"`
	// Observed is what the prober found.
	Observed string `json:"observed"`
	// Suggested is the change the fact implies.
	Suggested string `json:"suggested,omitempty"`
	// Why is the reason it was not applied automatically.
	Why string `json:"why"`

	Evidence []string `json:"evidence,omitempty"`
	// Fix is the concrete next step.
	Fix string `json:"fix,omitempty"`
}

func (c Conflict) String() string {
	at := c.Resource
	if c.JSONPath != "" {
		at += "." + c.JSONPath
	}

	var b strings.Builder

	fmt.Fprintf(&b, "%s: %s\n", at, c.Observed)
	fmt.Fprintf(&b, "  blueprint says: %s\n", c.Curated)
	if c.Suggested != "" {
		fmt.Fprintf(&b, "  suggested:      %s\n", c.Suggested)
	}
	fmt.Fprintf(&b, "  not applied:    %s\n", c.Why)
	if len(c.Evidence) > 0 {
		fmt.Fprintf(&b, "  evidence:       %s\n", strings.Join(c.Evidence, ", "))
	}
	if c.Fix != "" {
		fmt.Fprintf(&b, "  to accept:      %s\n", c.Fix)
	}

	return b.String()
}

// Change is something merge did.
type Change struct {
	Resource string `json:"resource"`
	JSONPath string `json:"jsonPath,omitempty"`
	// What names the field that changed, e.g. "behaviour.writable".
	What string `json:"what"`
	From string `json:"from,omitempty"`
	To   string `json:"to"`
	// Warning is set for a change that is safe but surprising enough to say out loud.
	Warning string `json:"warning,omitempty"`
}

func (c Change) String() string {
	at := c.Resource
	if c.JSONPath != "" {
		at += "." + c.JSONPath
	}

	out := fmt.Sprintf("%s: %s = %s", at, c.What, c.To)
	if c.From != "" {
		out = fmt.Sprintf("%s: %s %s -> %s", at, c.What, c.From, c.To)
	}
	if c.Warning != "" {
		out += "  (" + c.Warning + ")"
	}

	return out
}

// Result is what a merge produced.
type Result struct {
	Changes   []Change   `json:"changes,omitempty"`
	Conflicts []Conflict `json:"conflicts,omitempty"`
	// Ignored counts facts too weak to act on, so a report distinguishes "nothing was
	// observed" from "nothing observed was strong enough".
	Ignored int `json:"ignored,omitempty"`
	// Conditional counts facts held back because they hold only under a precondition.
	//
	// Separate from Ignored, which would be the wrong word: these are strong enough to act on
	// and were withheld because Behaviour cannot express the condition, not because the
	// evidence was weak. A reader deciding whether to trust a schema needs to tell those apart.
	Conditional int `json:"conditional,omitempty"`
	// Recommendations are things a human may want to do that merge will not do itself --
	// notably RequiresReplace, which is always a decision about somebody's infrastructure.
	Recommendations []string `json:"recommendations,omitempty"`
}

// Err returns ErrConflicts when anything was refused.
func (r Result) Err() error {
	if len(r.Conflicts) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %d unresolved", ErrConflicts, len(r.Conflicts))
}

// Changed reports whether merge would alter the blueprint.
func (r Result) Changed() bool { return len(r.Changes) > 0 }

// Options configures a merge.
type Options struct {
	Strategy Strategy
	// SnapshotID identifies the evidence, and goes into the regenerable description marker so
	// re-merging the same snapshot is idempotent.
	SnapshotID string
}

// Apply folds facts into a blueprint, returning what changed and what was refused.
//
// The blueprint is modified in place. Callers that want a dry run pass a copy -- `merge -check`
// does exactly that, which is how it reports whether anything *would* change without writing.
func Apply(bp *blueprint.Blueprint, facts []probe.Fact, opts Options) (Result, error) {
	var result Result

	if opts.Strategy == "" {
		opts.Strategy = StrategyAnnotate
	}

	// Sorted before anything reads them, so the blueprint merge writes -- variant order,
	// description observation order -- cannot depend on the order facts arrived in. The
	// committed facts document is sorted already; this makes the property merge's own
	// rather than an assumption about the caller.
	facts = append([]probe.Fact(nil), facts...)
	probe.SortFacts(facts)

	byResource := map[string][]probe.Fact{}
	for _, f := range facts {
		byResource[f.Resource] = append(byResource[f.Resource], f)
	}

	for i := range bp.Resources {
		res := &bp.Resources[i]

		resourceFacts := byResource[res.Key]
		if len(resourceFacts) == 0 {
			continue
		}
		delete(byResource, res.Key)

		if err := applyToResource(res, resourceFacts, opts, &result); err != nil {
			return result, err
		}
	}

	// A fact for a resource the blueprint does not have is reported rather than dropped: it
	// usually means the facts and the blueprint have drifted apart, which is worth knowing
	// before somebody trusts either.
	keys := make([]string, 0, len(byResource))
	for k := range byResource {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		result.Conflicts = append(result.Conflicts, Conflict{
			Resource: key,
			Curated:  "no such resource in the blueprint",
			Observed: fmt.Sprintf("%d fact(s) were recorded for it", len(byResource[key])),
			Why:      "the facts and the blueprint disagree about which resources exist",
			Fix:      "re-run ingest, or re-record the evidence against the current blueprint",
		})
	}

	result.sort()

	return result, nil
}

func applyToResource(
	res *blueprint.Resource,
	facts []probe.Fact,
	opts Options,
	result *Result,
) error {
	byPath := map[string][]probe.Fact{}
	var resourceLevel []probe.Fact

	for _, f := range facts {
		if f.JSONPath == "" {
			resourceLevel = append(resourceLevel, f)
			continue
		}
		byPath[f.JSONPath] = append(byPath[f.JSONPath], f)
	}

	applyResourceLevel(res, resourceLevel, result)

	// Attributes are walked by pointer, to any depth of nesting, so a fact about a field
	// inside an object lands on the right attribute wherever it sits.
	for i := range res.Schema.Attributes {
		applyToAttribute(res, &res.Schema.Attributes[i], "", byPath, opts, result)
	}

	// Anything left addressed a field with no attribute. Reported, because a fact that cannot
	// be merged is either a schema that has moved on or a probe addressing the wrong thing,
	// and both matter.
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		result.Conflicts = append(result.Conflicts, Conflict{
			Resource: res.Key,
			JSONPath: path,
			Curated:  "no attribute has this JSON path",
			Observed: fmt.Sprintf("%d fact(s) were recorded for it", len(byPath[path])),
			Why:      "the facts and the blueprint disagree about which fields exist",
			Fix:      "re-run ingest, or re-record the evidence against the current blueprint",
		})
	}

	return nil
}

func applyToAttribute(
	res *blueprint.Resource,
	attr *blueprint.Attribute,
	prefix string,
	byPath map[string][]probe.Fact,
	opts Options,
	result *Result,
) {
	path := attr.Wire.JSONPath
	if path == "" {
		path = attr.Name
	}
	if prefix != "" {
		path = prefix + "." + path
	}

	if facts, ok := byPath[path]; ok {
		delete(byPath, path)
		applyAttributeFacts(res, attr, path, facts, opts, result)
	}

	if attr.Type.NestedObject != nil {
		for i := range attr.Type.NestedObject.Attributes {
			applyToAttribute(res, &attr.Type.NestedObject.Attributes[i], path, byPath, opts, result)
		}
	}
}

// conditionalObservation is how a withheld fact reads in an attribute's description.
//
// The condition comes first, because that is the part a reader needs before the claim means
// anything -- "matchType is not returned on read" is wrong, and "when objectType is
// endpoint-agent, matchType is not returned on read" is right.
func conditionalObservation(f probe.Fact) string {
	return fmt.Sprintf("Conditionally, %s: %s. Not applied to the schema, because this holds "+
		"only under that condition and the schema has no way to say so.",
		f.Because(), f.Rationale)
}

// applyConditionalFact folds a conditional fact into the attribute's behaviour variant for
// its branch, and reports whether the fact's dimension has a variant home at all.
//
// False means the caller keeps the older hold-back -- the condition stated in the
// description, nothing generated from it. The set of dimensions with a home is deliberately
// the set a conditional *derivation* exists for; a fact kind no probe can attribute to a
// gate arriving here conditionally would be hand-authored, and hand-authored behaviour
// belongs in the blueprint directly, not smuggled through a facts document.
func applyConditionalFact(
	res *blueprint.Resource,
	attr *blueprint.Attribute,
	path string,
	f probe.Fact,
	observations *[]string,
	result *Result,
) bool {
	switch f.Field {
	case probe.FactWritable, probe.FactRequiredByAPI, probe.FactReturnedOnRead,
		probe.FactAcceptedValues, probe.FactRejectedValues:
	default:
		return false
	}

	// The same corroboration floor the unconditional path has, for the same reason: a branch
	// observation still turns an attribute a practitioner can set into one they cannot.
	if f.Field == probe.FactWritable {
		if v := f.Value.Bool; v != nil && !*v && !f.Confidence.AtLeast(probe.Corroborated) {
			result.Conflicts = append(result.Conflicts, needsCorroboration(res, path, f,
				"writable=false "+f.Because()))
			return true
		}
	}

	// A conditional bool contradicting a *set* unconditional base is a conflict, not a
	// write. The base is nearly always a stale unconditional half-truth -- returnedOnRead
	// false, measured on a static tag -- and the re-record that produced this branch fact
	// replaces it; until then the disagreement must be visible rather than resolved by
	// whichever fact merged last.
	if base := baseFor(attr, f.Field); base != nil && f.Value.Bool != nil && *base != *f.Value.Bool {
		result.Conflicts = append(result.Conflicts, Conflict{
			Resource: res.Key, JSONPath: path,
			Curated:  fmt.Sprintf("behaviour.%s is %t unconditionally", behaviourName(f.Field), *base),
			Observed: fmt.Sprintf("%t %s", *f.Value.Bool, f.Because()),
			Why: "an unconditional claim and a branch observation disagree; one of them is a " +
				"half-truth, and merge has no basis for choosing which",
			Evidence: f.Evidence,
			Fix: "re-record so the unconditional fact is re-derived per branch, then merge " +
				"again",
		})
		return true
	}

	variant := variantFor(attr, f.When)
	what := fmt.Sprintf("behaviour.conditional[%s].%s", variant.WhenKey(), behaviourName(f.Field))

	switch f.Field {
	case probe.FactWritable:
		setBool(&variant.Behaviour.Writable, f, res, path, what, result)
	case probe.FactRequiredByAPI:
		setBool(&variant.Behaviour.RequiredByAPI, f, res, path, what, result)
	case probe.FactReturnedOnRead:
		setBool(&variant.Behaviour.ReturnedOnRead, f, res, path, what, result)
	case probe.FactAcceptedValues:
		variant.Behaviour.AcceptedValues = append([]string(nil), f.Value.List...)
	case probe.FactRejectedValues:
		variant.Behaviour.RejectedValues = append([]string(nil), f.Value.List...)
	}

	*observations = appendUniqueString(*observations, fmt.Sprintf(
		"Conditionally, %s: %s. Applied as a behaviour variant.", f.Because(), f.Rationale))

	sortVariants(attr.Behaviour.Conditional)

	return true
}

// variantFor finds the attribute's variant for this precondition, appending one if no
// branch matches. Matching is by canonical key, so the order a probe appended conditions
// in cannot mint a duplicate branch.
func variantFor(attr *blueprint.Attribute, when []probe.Condition) *blueprint.BehaviourVariant {
	probe := blueprint.BehaviourVariant{When: append([]blueprint.Condition(nil), when...)}
	key := probe.WhenKey()

	for i := range attr.Behaviour.Conditional {
		if attr.Behaviour.Conditional[i].WhenKey() == key {
			return &attr.Behaviour.Conditional[i]
		}
	}

	attr.Behaviour.Conditional = append(attr.Behaviour.Conditional, probe)

	return &attr.Behaviour.Conditional[len(attr.Behaviour.Conditional)-1]
}

// baseFor is the attribute's unconditional value for a boolean dimension, nil when unset or
// not boolean.
func baseFor(attr *blueprint.Attribute, field probe.FactField) *bool {
	switch field {
	case probe.FactWritable:
		return attr.Behaviour.Writable
	case probe.FactRequiredByAPI:
		return attr.Behaviour.RequiredByAPI
	case probe.FactReturnedOnRead:
		return attr.Behaviour.ReturnedOnRead
	default:
		return nil
	}
}

// behaviourName is the Behaviour field a fact writes to, for report labels.
func behaviourName(field probe.FactField) string {
	switch field {
	case probe.FactWritable:
		return "writable"
	case probe.FactRequiredByAPI:
		return "requiredByApi"
	case probe.FactReturnedOnRead:
		return "returnedOnRead"
	case probe.FactAcceptedValues:
		return "acceptedValues"
	case probe.FactRejectedValues:
		return "rejectedValues"
	default:
		return string(field)
	}
}

// sortVariants keeps the committed order canonical, so two merges over the same facts in
// any order produce byte-identical variants.
func sortVariants(variants []blueprint.BehaviourVariant) {
	sort.SliceStable(variants, func(i, j int) bool {
		return variants[i].WhenKey() < variants[j].WhenKey()
	})
}

// appendUniqueString adds a description observation once, which is what keeps a second
// merge over the same facts from stuttering the marker block.
func appendUniqueString(dst []string, s string) []string {
	if slices.Contains(dst, s) {
		return dst
	}
	return append(dst, s)
}

// applyAttributeFacts is one case per fact field, deliberately.
//
// The complexity is the arity of the fact vocabulary, not tangled control flow: every branch is
// a flat "this kind of fact means this". A reviewer checking what merge does with a given fact
// reads one case; a handler map would satisfy the linter and turn that into a two-hop lookup.
//
// More importantly, the switch is what makes the coverage visible. A fact whose field merge did
// not recognise would be silently ignored -- the one failure mode a fact store must not have --
// and the default case sitting in plain sight next to the others is what stops that.
//
//nolint:gocognit,cyclop,gocyclo // See above: the shape is the point.
func applyAttributeFacts(
	res *blueprint.Resource,
	attr *blueprint.Attribute,
	path string,
	facts []probe.Fact,
	opts Options,
	result *Result,
) {
	// Descriptions are accumulated and written once, so an attribute with three facts gets one
	// marker block rather than three.
	var observations []string

	for _, f := range facts {
		// Suspected facts are report-only, by construction. A suspected fact is a prompt for
		// somebody to look, not a conclusion, and letting one into the blueprint would put a
		// claim there that no sequence actually supports.
		if !f.Confidence.AtLeast(probe.Inferred) {
			result.Ignored++
			continue
		}

		// A conditional fact must never reach the unconditional fields below: writing one
		// there makes it a claim about every case, which is exactly how the pilot came to
		// suppress matchType's read-back for every tag on the strength of an observation
		// about static ones, and how endpoint-agent came to sit in objectType's rejected
		// set.
		//
		// The dimensions a conditional derivation exists for go into a Behaviour variant --
		// structure emission can act on. Everything else keeps the older hold-back: the
		// condition is stated in the description, where a person reads it and nothing
		// generates from it. That is strictly better than both alternatives -- applying it
		// unconditionally is the bug, and dropping it silently loses evidence somebody paid
		// a live run for.
		if f.Conditional() {
			if applyConditionalFact(res, attr, path, f, &observations, result) {
				continue
			}

			observations = append(observations, conditionalObservation(f))
			result.Conditional++

			continue
		}

		switch f.Field {
		case probe.FactWritable:
			// Writable=false requires corroboration: it turns an attribute the practitioner
			// can set into one they cannot, which they have no way to work around.
			if v := f.Value.Bool; v != nil && !*v && !f.Confidence.AtLeast(probe.Corroborated) {
				result.Conflicts = append(result.Conflicts, needsCorroboration(res, path, f,
					"writable=false"))
				continue
			}
			setBool(&attr.Behaviour.Writable, f, res, path, "behaviour.writable", result)
			observations = append(observations, describeWritable(f))

		case probe.FactImmutable:
			if v := f.Value.Bool; v != nil && *v && !f.Confidence.AtLeast(probe.Corroborated) {
				result.Conflicts = append(result.Conflicts, needsCorroboration(res, path, f,
					"immutable=true"))
				continue
			}
			setBool(&attr.Behaviour.Immutable, f, res, path, "behaviour.immutable", result)

			// Never a plan modifier, whatever the confidence. Whether Terraform should destroy
			// and recreate is a decision about somebody's infrastructure; the toolkit's job is
			// to put the evidence in front of the person making it.
			if v := f.Value.Bool; v != nil && *v {
				result.Recommendations = append(result.Recommendations, fmt.Sprintf(
					"%s.%s: the API refuses in-place changes, so RequiresReplace is now "+
						"warranted. Add it by hand: it destroys and recreates real "+
						"infrastructure, which is not a decision this tool makes.",
					res.Key, path))
			}

		case probe.FactRequiredByAPI:
			wrote := setBool(&attr.Behaviour.RequiredByAPI, f, res, path,
				"behaviour.requiredByApi", result)
			observations = append(observations, describeRequired(f))
			applyRequiredPresence(res, attr, path, f, wrote, opts, result)

		case probe.FactReturnedOnRead:
			setBool(
				&attr.Behaviour.ReturnedOnRead,
				f,
				res,
				path,
				"behaviour.returnedOnRead",
				result,
			)

		case probe.FactVolatile:
			setBool(&attr.Behaviour.Volatile, f, res, path, "behaviour.volatile", result)
			if v := f.Value.Bool; v != nil && *v {
				observations = append(
					observations,
					"This value changes on every read, so it is Computed to avoid a perpetual diff.",
				)
			}

		case probe.FactServerDefault:
			applyServerDefault(res, attr, path, f, opts, result)
			if f.Value.Literal != nil {
				observations = append(observations, fmt.Sprintf(
					"Observed: the API assigns %s when this is omitted.", f.Value.Literal.Raw))
			}

		case probe.FactDefaultIsDerived:
			applyDerivedDefault(res, attr, path, f, result)
			if v := f.Value.Bool; v != nil && *v {
				observations = append(observations,
					"The API assigns this when omitted, but the value is derived rather than "+
						"constant, so no static default is generated.")
			}

		case probe.FactAcceptedValues:
			if len(f.Value.List) > 0 {
				observations = append(observations, fmt.Sprintf(
					"Values accepted here: %s.", joinCode(f.Value.List)))
				// Stored as data as well as prose. The validator render generates comes from
				// the *documented* set rather than this one -- the documented set is a
				// superset of what any one tenant takes, so building from it errs toward
				// permitting -- but the two together are what let render say, next to the
				// validator, which documented values this tenant refused.
				attr.Behaviour.AcceptedValues = append([]string(nil), f.Value.List...)
			}

		case probe.FactRejectedValues:
			if len(f.Value.List) > 0 {
				observations = append(observations, fmt.Sprintf(
					"The specification documents %s, which the API rejected.",
					joinCode(f.Value.List),
				))
				attr.Behaviour.RejectedValues = append([]string(nil), f.Value.List...)
			}

		case probe.FactValuesClosed:
			// Per attribute, not resource-level: this is the one fact with direct evidence
			// that a generated OneOf would be harmful, so render needs it beside the
			// attribute it belongs to. It was being discarded with the resource-level facts.
			// setBool rather than a raw pointer: it records the change and returns false
			// when the value already matches, which is what keeps a second merge over the
			// same facts a no-op.
			setBool(&attr.Behaviour.ValuesClosed, f, res, path, "behaviour.valuesClosed", result)

			if v := f.Value.Bool; v != nil && !*v {
				observations = append(observations,
					"The API accepted a value from outside the documented set, so no OneOf "+
						"validator is generated for this attribute.")
			}

		case probe.FactNormalisation:
			observations = append(observations,
				"The API normalises this value: "+f.Value.Text+".")

			// Also recorded structurally, because a generated acceptance test has to act on
			// it: an equality assertion on a value the API rewrites fails by design. The
			// prose is for whoever reads the schema; this is for the generator. Guarded so a
			// second merge over the same facts stays a no-op.
			if attr.Behaviour.Normalises != f.Value.Text {
				result.Changes = append(result.Changes, Change{
					Resource: res.Key, JSONPath: path, What: "behaviour.normalises",
					From: attr.Behaviour.Normalises, To: f.Value.Text,
				})
				attr.Behaviour.Normalises = f.Value.Text
			}

		case probe.FactSilentlyIgnoredOnUpdate:
			if v := f.Value.Bool; v != nil && *v {
				result.Recommendations = append(result.Recommendations, fmt.Sprintf(
					"%s.%s: an update accepted a new value and did not apply it. That is not "+
						"immutability and needs a human decision: RequiresReplace, an error, "+
						"or not supporting the change.", res.Key, path))
			}

		case probe.FactSideEffect:
			observations = append(observations,
				"Writing this also changes "+f.Value.Text+".")

		case probe.FactIntegral:
			// A recommendation and nothing else, deliberately: changing an attribute's
			// type breaks state compatibility, which is a human decision, like
			// RequiresReplace -- and the fact is capped at Inferred anyway, because JSON
			// cannot distinguish 5 from 5.0.
			if v := f.Value.Bool; v != nil && *v {
				result.Recommendations = append(result.Recommendations, fmt.Sprintf(
					"%s.%s: every observed value was a whole number. If the API's own type "+
						"is an integer, consider int64 -- a type change breaks state "+
						"compatibility, so it is not made automatically.", res.Key, path))
			}

		case probe.FactErrorEnvelope, probe.FactUnknownParamTolerated,
			probe.FactUpdateStyle, probe.FactReadBack, probe.FactNotFoundIsSuccess:
			// Resource-level or informational; handled elsewhere or deliberately not merged
			// into an attribute.
			result.Ignored++

		default:
			// An unrecognised field is reported rather than skipped. Silently ignoring a fact
			// is the one failure mode a fact store must not have: it would look merged and
			// would not be.
			result.Conflicts = append(result.Conflicts, Conflict{
				Resource: res.Key, JSONPath: path,
				Curated:  "merge does not recognise this fact field",
				Observed: string(f.Field),
				Why:      "a fact merge cannot interpret must not be silently discarded",
				Evidence: f.Evidence,
			})
		}
	}

	if len(observations) > 0 {
		rewriteDescription(attr, observations, opts.SnapshotID, res, path, result)
	}
}

func applyResourceLevel(res *blueprint.Resource, facts []probe.Fact, result *Result) {
	for _, f := range facts {
		if !f.Confidence.AtLeast(probe.Inferred) {
			result.Ignored++
			continue
		}

		// The same guard applyAttributeFacts has, for the same reason. Policy is
		// unconditional -- one update style, one delete semantics per resource -- so a fact
		// that holds only under a precondition must not be written into it. No probe emits a
		// conditional resource-level fact today; when one does, this keeps it out of the
		// blueprint instead of silently applying it to every branch. A resource-level fact
		// has no description to carry the condition, so a recommendation is its visible home.
		if f.Conditional() {
			result.Conditional++
			result.Recommendations = append(result.Recommendations, fmt.Sprintf(
				"%s: %s = %s holds only %s; policy has no way to say so, so it was not applied",
				res.Key, f.Field, f.Value, f.Because()))

			continue
		}

		switch f.Field {
		case probe.FactNotFoundIsSuccess:
			// Error handling rather than schema, and a wrong answer is recoverable, so this
			// moves in either direction.
			v := f.Value.Bool
			if v == nil {
				continue
			}
			if res.Policy.Delete.NotFoundIsSuccess != *v {
				result.Changes = append(result.Changes, Change{
					Resource: res.Key, What: "policy.delete.notFoundIsSuccess",
					From: fmt.Sprintf("%t", res.Policy.Delete.NotFoundIsSuccess),
					To:   fmt.Sprintf("%t", *v),
				})
				res.Policy.Delete.NotFoundIsSuccess = *v
			}

		case probe.FactReadBack:
			applyReadBack(res, f, result)

		case probe.FactUpdateStyle:
			applyUpdateStyle(res, f, result)

		default:
			// Informational resource-level facts -- the error envelope, unknown-parameter
			// tolerance -- have no blueprint field. They stay in the report.
			result.Ignored++
		}
	}
}

// applyReadBack turns a read-back on but never off.
//
// The confidence in that fact is asymmetric: observing a stale read proves the API is not
// read-your-writes consistent, while one fast success proves nothing. So the safe direction is
// the only automatic one -- a needless re-read costs a request, a missing one costs a failed
// apply.
func applyReadBack(res *blueprint.Resource, f probe.Fact, result *Result) {
	rb := f.Value.ReadBack
	if rb == nil {
		return
	}

	if !rb.Enabled {
		if res.Policy.ReadBack.Enabled {
			result.Conflicts = append(result.Conflicts, Conflict{
				Resource: res.Key,
				Curated:  "policy.readBack is enabled",
				Observed: "the probe saw no stale read",
				Why: "one fast success does not prove read-your-writes consistency, so a " +
					"read-back is never removed automatically",
				Evidence: f.Evidence,
				Fix:      "edit the blueprint if you are confident the API is consistent",
			})
		}
		return
	}

	changed := false

	if !res.Policy.ReadBack.Enabled {
		res.Policy.ReadBack.Enabled = true
		changed = true
	}
	// Retries only ever increase, for the same reason.
	if rb.MaxRetries > res.Policy.ReadBack.MaxRetries {
		res.Policy.ReadBack.MaxRetries = rb.MaxRetries
		changed = true
	}
	if rb.IntervalMS > res.Policy.ReadBack.IntervalMS {
		res.Policy.ReadBack.IntervalMS = rb.IntervalMS
		changed = true
	}

	if changed {
		if res.Policy.ReadBack.Reason == "" {
			// A retry loop with no stated reason is indistinguishable from cargo cult.
			res.Policy.ReadBack.Reason = f.Rationale
		}
		result.Changes = append(result.Changes, Change{
			Resource: res.Key, What: "policy.readBack",
			To: fmt.Sprintf("enabled, %d retries, %dms",
				res.Policy.ReadBack.MaxRetries, res.Policy.ReadBack.IntervalMS),
		})
	}
}

// applyUpdateStyle records the observed style, or conflicts if it disagrees.
//
// Never overwritten silently: getting this wrong makes the generated provider "silently erase
// attributes the practitioner never mentioned", in the IR's own words, and that is not a change
// to make on the strength of one observation against a human's stated choice.
func applyUpdateStyle(res *blueprint.Resource, f probe.Fact, result *Result) {
	observed := blueprint.UpdateStyle(f.Value.Text)
	if observed == "" {
		return
	}

	if res.Policy.UpdateStyle == "" {
		res.Policy.UpdateStyle = observed
		result.Changes = append(result.Changes, Change{
			Resource: res.Key, What: "policy.updateStyle", To: string(observed),
		})
		return
	}

	if res.Policy.UpdateStyle != observed {
		result.Conflicts = append(result.Conflicts, Conflict{
			Resource:  res.Key,
			Curated:   "policy.updateStyle is " + string(res.Policy.UpdateStyle),
			Observed:  "the probe observed " + string(observed),
			Suggested: "policy.updateStyle " + string(observed),
			Why: "getting this wrong silently erases attributes the practitioner never " +
				"mentioned, so it is not changed on one observation",
			Evidence: f.Evidence,
			Fix:      "edit the blueprint, or re-record to corroborate",
		})
	}
}

// applyServerDefault records a constant default, and resolves the three-way case.
//
// The interesting one is a curated computed_optional with **no** observed default. That
// combination is wrong -- the attribute reads "(known after apply)" forever and never becomes
// known -- and it is exactly the guess sitting in the pilot blueprint. Merge must not narrow it
// silently, so it conflicts.
func applyServerDefault(
	res *blueprint.Resource,
	attr *blueprint.Attribute,
	path string,
	f probe.Fact,
	opts Options,
	result *Result,
) {
	lit := f.Value.Literal

	if lit == nil {
		// No default observed. Only a problem if the blueprint assumed one.
		if attr.ComputedOptionalRequired == blueprint.ComputedOptional {
			result.Conflicts = append(result.Conflicts, Conflict{
				Resource: res.Key, JSONPath: path,
				Curated:   "presence is computed_optional",
				Observed:  "the probe found no server default: " + f.Rationale,
				Suggested: "presence optional",
				Why: "narrowing an attribute's presence can break existing state, and a " +
					"computed_optional attribute with no server default is never known " +
					"after apply",
				Evidence: f.Evidence,
				Fix:      "edit the blueprint",
			})
		}
		return
	}

	if attr.Behaviour.ServerDefault == nil ||
		attr.Behaviour.ServerDefault.Raw != lit.Raw {
		result.Changes = append(result.Changes, Change{
			Resource: res.Key, JSONPath: path,
			What: "behaviour.serverDefault", To: lit.Raw,
		})
		copied := *lit
		attr.Behaviour.ServerDefault = &copied
	}

	// optional -> computed_optional is widening, so it is automatic under apply. Adding a
	// *static* Default is not: it changes plan output for every existing configuration, which
	// is a human's call.
	if opts.Strategy == StrategyApply && attr.ComputedOptionalRequired == blueprint.Optional &&
		f.Confidence.AtLeast(probe.Corroborated) {
		result.Changes = append(result.Changes, Change{
			Resource: res.Key,
			JSONPath: path,
			What:     "presence",
			From:     string(attr.ComputedOptionalRequired),
			To:       string(blueprint.ComputedOptional),
		})
		attr.ComputedOptionalRequired = blueprint.ComputedOptional
	}

	result.Recommendations = append(result.Recommendations, fmt.Sprintf(
		"%s.%s: consider default.static so %s is known at plan time. Not applied: adding a "+
			"static default changes plan output for every existing configuration.",
		res.Key, path, lit.Raw))
}

// applyDerivedDefault records that a default exists but is not constant.
//
// computed_optional is exactly right for a derived default, so where the blueprint already says
// that, the guess turns out correct -- for a reason nobody had established. Worth saying so
// plainly rather than silently agreeing.
func applyDerivedDefault(
	res *blueprint.Resource,
	attr *blueprint.Attribute,
	path string,
	f probe.Fact,
	result *Result,
) {
	v := f.Value.Bool
	if v == nil || !*v {
		return
	}

	// A static default would be a permanent lie about a value the server computes.
	if attr.Default != nil && attr.Default.Static != nil {
		result.Conflicts = append(result.Conflicts, Conflict{
			Resource: res.Key, JSONPath: path,
			Curated:   "a static default of " + attr.Default.Static.Raw,
			Observed:  "the API derives this value rather than using a constant",
			Suggested: "remove the static default and leave the attribute computed_optional",
			Why: "a static default for a derived value is wrong at plan time for every " +
				"configuration, but removing one changes plan output, so it is not automatic",
			Evidence: f.Evidence,
			Fix:      "edit the blueprint",
		})
		return
	}

	// Only news the first time. A confirmation re-reported on every merge would make merging
	// twice a diff, which is the thing the description marker exists to prevent.
	if attr.ComputedOptionalRequired == blueprint.ComputedOptional && !derivedAlreadyNoted(attr) {
		result.Changes = append(result.Changes, Change{
			Resource: res.Key, JSONPath: path,
			What:    "behaviour (confirmed)",
			To:      "computed_optional is correct: the default is derived, not constant",
			Warning: "the blueprint's assumption was right, for a reason nobody had established",
		})
	}
}

// applyRequiredPresence widens required to optional where the API does not enforce it.
//
// Widening, so automatic under apply -- but loudly. An attribute the specification marked
// required turning out optional is surprising enough that it usually means the fixture was odd
// rather than the schema wrong.
func applyRequiredPresence(
	res *blueprint.Resource,
	attr *blueprint.Attribute,
	path string,
	f probe.Fact,
	wroteBehaviour bool,
	opts Options,
	result *Result,
) {
	v := f.Value.Bool
	if v == nil {
		return
	}

	if !*v && attr.ComputedOptionalRequired == blueprint.Required {
		if opts.Strategy != StrategyApply {
			result.Conflicts = append(result.Conflicts, Conflict{
				Resource: res.Key, JSONPath: path,
				Curated:   "presence is required",
				Observed:  "the API accepted a create omitting it",
				Suggested: "presence optional",
				Why:       "-strategy apply is needed to change presence",
				Evidence:  f.Evidence,
				Fix:       "re-run with -strategy apply, or edit the blueprint",
			})
			return
		}

		result.Changes = append(result.Changes, Change{
			Resource: res.Key,
			JSONPath: path,
			What:     "presence",
			From:     string(attr.ComputedOptionalRequired),
			To:       string(blueprint.Optional),
			Warning: "a required attribute turning out optional is surprising; check the " +
				"fixture was not odd before trusting it",
		})
		attr.ComputedOptionalRequired = blueprint.Optional
		return
	}

	// The API enforces it although the specification did not declare it. ComputedOptionalRequired is already
	// right; what is worth recording is that the requirement is real, so nobody regenerating
	// from a newer specification "fixes" it back.
	if *v && attr.ComputedOptionalRequired == blueprint.Required && wroteBehaviour {
		result.Changes = append(result.Changes, Change{
			Resource: res.Key, JSONPath: path,
			What: "behaviour.requiredByApi (confirmed)", To: "true",
		})
	}
}

// setBool writes a Behaviour pointer, recording the change.
//
// Behaviour is the one place merge writes freely: nothing else populates those fields, so no
// conflict is possible and this is the bulk of merge's value.
func setBool(
	target **bool,
	f probe.Fact,
	res *blueprint.Resource,
	path, what string,
	result *Result,
) bool {
	v := f.Value.Bool
	if v == nil {
		return false
	}

	if *target != nil && **target == *v {
		return false
	}

	from := ""
	if *target != nil {
		from = fmt.Sprintf("%t", **target)
	}

	result.Changes = append(result.Changes, Change{
		Resource: res.Key, JSONPath: path, What: what,
		From: from, To: fmt.Sprintf("%t", *v),
	})

	copied := *v
	*target = &copied

	return true
}

// derivedAlreadyNoted reports whether a previous merge already described the derived default.
//
// Read off the description rather than a separate flag, because the description is where the
// observation lives: adding an IR field to remember it would be state duplicated for the sake
// of one confirmation message.
func derivedAlreadyNoted(attr *blueprint.Attribute) bool {
	return strings.Contains(attr.MarkdownDescription, "derived rather than")
}

func needsCorroboration(res *blueprint.Resource, path string, f probe.Fact, claim string) Conflict {
	return Conflict{
		Resource: res.Key, JSONPath: path,
		Curated:  "unchanged",
		Observed: claim + " at confidence " + string(f.Confidence),
		Why: claim + " changes what a practitioner may write, with no workaround, so it " +
			"requires corroboration from a second fixture",
		Evidence: f.Evidence,
		Fix:      "re-record with a second fixture to corroborate",
	}
}

func describeWritable(f probe.Fact) string {
	if v := f.Value.Bool; v != nil && !*v {
		return "The API accepts this field on write and does not store it, so it is Computed."
	}
	return ""
}

func describeRequired(f probe.Fact) string {
	if v := f.Value.Bool; v != nil && *v {
		return "The API enforces this field's presence, which the specification does not declare."
	}
	return ""
}

func joinCode(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, "`"+v+"`")
	}
	return strings.Join(quoted, ", ")
}

func (r *Result) sort() {
	sort.SliceStable(r.Changes, func(i, j int) bool {
		a, b := r.Changes[i], r.Changes[j]
		if a.Resource != b.Resource {
			return a.Resource < b.Resource
		}
		if a.JSONPath != b.JSONPath {
			return a.JSONPath < b.JSONPath
		}
		return a.What < b.What
	})

	sort.SliceStable(r.Conflicts, func(i, j int) bool {
		a, b := r.Conflicts[i], r.Conflicts[j]
		if a.Resource != b.Resource {
			return a.Resource < b.Resource
		}
		return a.JSONPath < b.JSONPath
	})

	sort.Strings(r.Recommendations)
}
