package probe

import (
	"fmt"
	"sort"

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
}

// Validate checks a fact is well-formed enough to be trusted.
//
// Called on load rather than only on write, because the committed facts document is
// hand-editable and a fact with no evidence or an unrecognised field would otherwise
// flow into merge and change a schema on the strength of nothing.
func (f Fact) Validate() error {
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

// String renders a fact for a report.
func (f Fact) String() string {
	at := f.Resource
	if f.JSONPath != "" {
		at += "." + f.JSONPath
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
// By resource, then JSON path, then field. Not by discovery order: the order probes run
// in is an implementation detail, and letting it into a committed artefact would make
// the file churn whenever the catalogue is reordered.
func SortFacts(facts []Fact) {
	sort.SliceStable(facts, func(i, j int) bool {
		a, b := facts[i], facts[j]
		if a.Resource != b.Resource {
			return a.Resource < b.Resource
		}
		if a.JSONPath != b.JSONPath {
			return a.JSONPath < b.JSONPath
		}
		return a.Field < b.Field
	})
}
