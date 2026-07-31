package probe

import (
	"fmt"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// Confidence is how much weight a fact can bear.
//
// The levels are not decoration: each carries a rule about what merge may do with it,
// and those rules are the difference between a toolkit that widens a schema safely and
// one that destroys infrastructure. See internal/blueprint/merge.
type Confidence string

const (
	// Observed means the fact follows from the responses by one unambiguous reading,
	// and the sequence eliminated the alternatives named on the fact.
	Observed Confidence = "observed"

	// Corroborated means observed twice, with independent fixtures or values. It is
	// *required* for Immutable=true and Writable=false, because both of those drive
	// changes a practitioner cannot work around.
	Corroborated Confidence = "corroborated"

	// Inferred means the fact follows from the responses plus an assumption, which is
	// stated in Alternatives. Merge may write it into Behaviour but must never let it
	// change an attribute's presence.
	Inferred Confidence = "inferred"

	// Suspected means one ambiguous observation. Reported, never written. A suspected
	// fact is a prompt for a human to look, not a conclusion.
	Suspected Confidence = "suspected"
)

// rank orders confidence, strongest first.
func (c Confidence) rank() int {
	switch c {
	case Corroborated:
		return 0
	case Observed:
		return 1
	case Inferred:
		return 2
	case Suspected:
		return 3
	default:
		return 4
	}
}

// AtLeast reports whether c is at least as strong as want.
func (c Confidence) AtLeast(want Confidence) bool { return c.rank() <= want.rank() }

// FactField names what a fact is about.
//
// A closed set, because merge switches on it exhaustively: a fact whose field merge
// does not recognise would be silently ignored, which is the one failure mode a fact
// store must not have.
type FactField string

const (
	// FactWritable: the API accepts this field on write and stores it. False means it
	// accepts and discards, which is the difference between Computed and
	// Optional+Computed.
	FactWritable FactField = "writable"
	// FactImmutable: the API refuses to change this field after create. Drives a
	// RequiresReplace *recommendation* only, never an automatic plan modifier.
	FactImmutable FactField = "immutable"
	// FactRequiredByAPI: the API enforces this field's presence, whatever the
	// specification declares.
	FactRequiredByAPI FactField = "requiredByApi"
	// FactServerDefault: the constant value the API applies when the field is omitted.
	FactServerDefault FactField = "serverDefault"
	// FactReturnedOnRead: the field appears in a read response.
	FactReturnedOnRead FactField = "returnedOnRead"
	// FactVolatile: the field differs between two identical reads.
	FactVolatile FactField = "volatile"

	// FactUpdateStyle: whether an update preserves or clears omitted fields.
	FactUpdateStyle FactField = "policy.updateStyle"
	// FactReadBack: whether a re-read after write is needed, and how long it takes.
	FactReadBack FactField = "policy.readBack"
	// FactNotFoundIsSuccess: whether a 404 on delete means "already gone".
	FactNotFoundIsSuccess FactField = "policy.delete.notFoundIsSuccess"

	// FactDefaultIsDerived: the omitted-field value is computed from something --
	// another field, the tenant, a counter -- rather than being a constant. This is
	// the fact that stops a round-robin colour assignment becoming a static default.
	FactDefaultIsDerived FactField = "defaultIsDerived"
	// FactSilentlyIgnoredOnUpdate: an update accepted a new value with 2xx and did
	// not apply it. Distinct from immutability, and conflating the two is the classic
	// error.
	FactSilentlyIgnoredOnUpdate FactField = "silentlyIgnoredOnUpdate"
	// FactNormalisation: the server transformed a value it accepted.
	FactNormalisation FactField = "normalisation"
	// FactSideEffect: writing one field changed another that was not sent.
	FactSideEffect FactField = "sideEffect"

	// The three below name themselves after the Behaviour fields merge writes them into,
	// as every other fact field does -- FactWritable to Writable, FactServerDefault to
	// ServerDefault. Keeping that mirroring is worth re-deriving the committed facts from
	// the cassette, which is a replay and touches no recorded traffic.

	// FactValuesClosed: whether the API rejects values outside the documented set. False is
	// the load-bearing answer -- it is direct evidence that a generated OneOf would reject
	// configurations this API takes.
	FactValuesClosed FactField = "valuesClosed"
	// FactAcceptedValues: the documented values the API actually accepted.
	FactAcceptedValues FactField = "acceptedValues"
	// FactRejectedValues: documented values the API refused, which says the specification is
	// stale. The generated schema names them in a comment beside the validator, so a reader
	// of the schema meets the staleness rather than having to go looking for it.
	FactRejectedValues FactField = "rejectedValues"

	// FactErrorEnvelope: which error shape the API returns for which status.
	FactErrorEnvelope FactField = "errorEnvelope"
	// FactUnknownParamTolerated: the API ignores query parameters it does not know,
	// which calibrates every probe that depends on an unknown body field being
	// ignored rather than rejected.
	FactUnknownParamTolerated FactField = "unknownParamTolerated"
)

// Value is a fact's value.
//
// A tagged union rather than an `any`, so the committed JSON is legible and a merge
// that expects a bool cannot be handed a string. Exactly one field is set.
type Value struct {
	Bool     *bool               `json:"bool,omitempty"`
	Literal  *blueprint.Literal  `json:"literal,omitempty"`
	Text     string              `json:"text,omitempty"`
	List     []string            `json:"list,omitempty"`
	ReadBack *blueprint.ReadBack `json:"readBack,omitempty"`
}

// BoolValue and friends are the constructors. Using them rather than the struct
// literal keeps "exactly one field is set" true by construction.
func BoolValue(b bool) Value                    { return Value{Bool: &b} }
func TextValue(s string) Value                  { return Value{Text: s} }
func ListValue(ss []string) Value               { return Value{List: ss} }
func LiteralValue(l blueprint.Literal) Value    { return Value{Literal: &l} }
func ReadBackValue(rb blueprint.ReadBack) Value { return Value{ReadBack: &rb} }

// String renders a value for a report.
func (v Value) String() string {
	switch {
	case v.Bool != nil:
		return fmt.Sprintf("%t", *v.Bool)
	case v.Literal != nil:
		return v.Literal.Raw
	case v.List != nil:
		return fmt.Sprintf("%v", v.List)
	case v.ReadBack != nil:
		return fmt.Sprintf("enabled=%t retries=%d interval=%dms",
			v.ReadBack.Enabled, v.ReadBack.MaxRetries, v.ReadBack.IntervalMS)
	case v.Text != "":
		return v.Text
	default:
		return "(empty)"
	}
}

// IsZero reports whether nothing was set, which load treats as a malformed fact.
func (v Value) IsZero() bool {
	return v.Bool == nil && v.Literal == nil && v.List == nil && v.ReadBack == nil && v.Text == ""
}

// Fact is one thing the prober established.
type Fact struct {
	// Resource is the blueprint key.
	Resource string `json:"resource"`
	// JSONPath is the field this is about, empty for a resource-level fact.
	JSONPath string `json:"jsonPath,omitempty"`

	Field FactField `json:"field"`
	Value Value     `json:"value"`

	Confidence Confidence `json:"confidence"`
	// Probe names what produced this, so a wrong fact can be traced to the protocol
	// that produced it rather than argued about in the abstract.
	Probe string `json:"probe"`

	// Evidence names the cassette interactions supporting this fact, by their stable
	// ids. A fact with no evidence is not a fact, and Load rejects it: the whole
	// premise of committing cassettes is that every claim can be checked against the
	// traffic that produced it.
	Evidence []string `json:"evidence"`

	// Rationale is one sentence, rendered into the merge report and into the
	// attribute's own description.
	Rationale string `json:"rationale"`

	// Alternatives are the explanations this probe's sequence did *not* rule out.
	//
	// This is the honest half of the record. Without it "confidence: observed" is a
	// number nobody can interrogate; with it, a reviewer can see exactly which other
	// readings of the same traffic remain open, and decide whether they care.
	Alternatives []string `json:"alternatives,omitempty"`

	// When is the precondition this fact holds under. Empty means unconditional.
	//
	// Every fact was unconditional until this existed, which made a whole class of
	// observation unrecordable -- and, worse, made half of one recordable as though it were
	// whole. The pilot has two. `matchType` is not returned on read *for a static tag*, where
	// the field is meaningless; on a dynamic tag it is, and the unconditional fact makes
	// generated code suppress its read-back for every tag. `endpoint-agent` sits in
	// `objectType`'s rejected set because every fixture that tried it was creating a static
	// tag; it is a perfectly good object type for a dynamic one.
	//
	// The design already knew. requiredByAPI.concludeRequired detects exactly this shape -- a
	// field required by one fixture and not another -- and deliberately downgrades it to a
	// note, because "conditional on something else in the body" was all it could say. With a
	// precondition it can say which something, and only then is it a fact.
	//
	// Conditions are conjunctive: all of them held. There is no disjunction, because two
	// branches producing the same observation are two facts, and merging them into one would
	// hide that each was separately measured.
	When []Condition `json:"when,omitempty"`
}

// Condition is one precondition: a field held this value while the fact was observed.
//
// An alias for the blueprint's type rather than a mirror of it, for the same reason Value
// embeds blueprint.Literal: merge copies a fact's conditions into a behaviour variant
// verbatim, and two structurally identical types would add a conversion whose only failure
// mode is drifting apart. The JSONPath is in the same vocabulary as Fact.JSONPath, and
// Equals is a string because a gate has to be *enumerable* to be crossed -- a probe can only
// vary a field across values somebody listed, and the plan declares gate values as strings,
// via Plan.DefaultInfluencers.
type Condition = blueprint.Condition

// Conditional reports whether this fact holds only under a precondition.
func (f Fact) Conditional() bool { return len(f.When) > 0 }

// whenKey renders the preconditions as one canonical string, empty when unconditional.
//
// This is the identity of a fact's condition set: two facts about the same path and field
// are the same claim only if this agrees too. Sorted by path -- validateConditions already
// guarantees each path appears once -- so the key does not depend on the order a probe
// happened to append conditions in.
func (f Fact) whenKey() string {
	if len(f.When) == 0 {
		return ""
	}

	parts := make([]string, 0, len(f.When))
	for _, c := range f.When {
		parts = append(parts, c.JSONPath+"="+c.Equals)
	}
	sort.Strings(parts)

	return strings.Join(parts, ";")
}

// Because renders a fact's preconditions as one prose clause, empty when unconditional.
//
// Used by merge to state the condition in an attribute's description, which is where a
// conditional fact goes until something can act on it.
func (f Fact) Because() string {
	if len(f.When) == 0 {
		return ""
	}

	parts := make([]string, 0, len(f.When))
	for _, c := range f.When {
		parts = append(parts, c.String())
	}

	return "when " + strings.Join(parts, " and ")
}

// Validate checks a fact is well-formed enough to be trusted.
//
// Called on load rather than only on write, because the committed facts document is
// hand-editable and a fact with no evidence or an unrecognised field would otherwise
// flow into merge and change a schema on the strength of nothing.
func (f Fact) Validate() error {
	if err := f.ValidateShape(); err != nil {
		return err
	}

	switch {
	case len(f.Evidence) == 0:
		return fmt.Errorf("%w: %s.%s: a fact with no evidence; every claim must name the "+
			"cassette interactions that support it", ErrInvalidFacts, f.Resource, f.Field)
	case f.Rationale == "":
		return fmt.Errorf(
			"%w: %s.%s: a fact with no rationale",
			ErrInvalidFacts,
			f.Resource,
			f.Field,
		)
	default:
		return nil
	}
}

// ValidateShape checks the parts of a fact that must hold whatever its confidence.
//
// A Suspected fact is exempt from the strength checks -- it may lack evidence, because it
// is a prompt for a human rather than a conclusion -- but a malformed precondition or an
// unknown field is malformed at any confidence, and letting one load would put a fact
// downstream code cannot interpret into the store.
func (f Fact) ValidateShape() error {
	switch {
	case f.Resource == "":
		return fmt.Errorf("%w: a fact with no resource", ErrInvalidFacts)
	case f.Field == "":
		return fmt.Errorf("%w: %s: a fact with no field", ErrInvalidFacts, f.Resource)
	case !knownFactFields[f.Field]:
		return fmt.Errorf("%w: %s: unknown fact field %q", ErrInvalidFacts, f.Resource, f.Field)
	case f.Value.IsZero():
		return fmt.Errorf("%w: %s.%s: a fact with no value", ErrInvalidFacts, f.Resource, f.Field)
	case !knownConfidence[f.Confidence]:
		return fmt.Errorf("%w: %s.%s: unknown confidence %q",
			ErrInvalidFacts, f.Resource, f.Field, f.Confidence)
	default:
		return f.validateConditions()
	}
}

// validateConditions refuses a precondition that does not say anything.
//
// A condition with no path constrains nothing, so the fact would read as conditional while
// applying always -- the worst of both, and undetectable downstream. An empty Equals is
// refused for the same reason rather than being treated as "absent": absence of a gate value
// is a different observation from a gate holding the empty string, and nothing distinguishes
// them once written.
func (f Fact) validateConditions() error {
	seen := make(map[string]bool, len(f.When))

	for _, c := range f.When {
		switch {
		case c.JSONPath == "":
			return fmt.Errorf("%w: %s.%s: a condition with no jsonPath",
				ErrInvalidFacts, f.Resource, f.Field)
		case c.Equals == "":
			return fmt.Errorf("%w: %s.%s: condition on %q with no value",
				ErrInvalidFacts, f.Resource, f.Field, c.JSONPath)
		case seen[c.JSONPath]:
			// Two values for one path cannot both have held, so the fact is about a state
			// that never existed.
			return fmt.Errorf("%w: %s.%s: conditions name %q twice, which cannot both hold",
				ErrInvalidFacts, f.Resource, f.Field, c.JSONPath)
		}

		seen[c.JSONPath] = true
	}

	return nil
}

// String renders a fact for a report.
func (f Fact) String() string {
	at := f.Resource
	if f.JSONPath != "" {
		at += "." + f.JSONPath
	}
	if because := f.Because(); because != "" {
		return fmt.Sprintf("%s: %s = %s %s (%s, %s)",
			at, f.Field, f.Value, because, f.Confidence, f.Probe)
	}

	return fmt.Sprintf("%s: %s = %s (%s, %s)", at, f.Field, f.Value, f.Confidence, f.Probe)
}

var knownFactFields = map[FactField]bool{
	FactWritable: true, FactImmutable: true, FactRequiredByAPI: true,
	FactServerDefault: true, FactReturnedOnRead: true, FactVolatile: true,
	FactUpdateStyle: true, FactReadBack: true, FactNotFoundIsSuccess: true,
	FactDefaultIsDerived: true, FactSilentlyIgnoredOnUpdate: true,
	FactNormalisation: true, FactSideEffect: true,
	FactValuesClosed: true, FactAcceptedValues: true, FactRejectedValues: true,
	FactErrorEnvelope: true, FactUnknownParamTolerated: true,
}

var knownConfidence = map[Confidence]bool{
	Observed: true, Corroborated: true, Inferred: true, Suspected: true,
}

// SortFacts orders facts so a committed document is byte-stable.
//
// By resource, then JSON path, then field, then precondition. Not by discovery order: the
// order probes run in is an implementation detail, and letting it into a committed artefact
// would make the file churn whenever the catalogue is reordered. The precondition tiebreak
// matters the moment one path carries a fact per branch -- without it two branch facts have
// no defined relative order, and the committed document churns between identical runs.
func SortFacts(facts []Fact) {
	sort.SliceStable(facts, func(i, j int) bool {
		a, b := facts[i], facts[j]
		if a.Resource != b.Resource {
			return a.Resource < b.Resource
		}
		if a.JSONPath != b.JSONPath {
			return a.JSONPath < b.JSONPath
		}
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		return a.whenKey() < b.whenKey()
	})
}
