// Package observe is the observation model: the typed record of what an
// audit learned from a live API, plus the committed per-entity file layout
// it is stored in and the redaction that runs before anything is written.
//
// An observation is one finding — "this attribute is immutable", "a delete
// treats 404 as already gone" — with a redacted request/response excerpt as
// proof. Observations are committed under audit/observations/ so a human
// can see why every later spec correction was proposed; each correction
// points at its justifying observation by ID, which is why the ID is a
// stable hash of what the observation is about rather than of when it was
// made. Excerpts are deliberately not replayable: they show a reviewer the
// evidence, they do not let a machine re-enact the traffic.
package observe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"
)

// Kind names what a single observation claims. The set is closed: the
// revision stage switches on it exhaustively when compiling observations
// into proposed corrections, and a kind it does not recognise would be
// silently dropped — the one failure mode an evidence store must not have.
//
// Each kind's comment states three things: what the claim means, how the
// audit learns it, and which correction it can become.
type Kind string

const (
	// KindWritable: the API accepts this attribute on write and stores it
	// (true), or accepts and discards it (false). Learned by sending a
	// value on create and reading it back. False becomes a correction
	// marking the property readOnly, which downstream generates as
	// Computed rather than Optional.
	KindWritable Kind = "writable"

	// KindImmutable: the API refuses to change this attribute after
	// create. Learned when an update naming only this attribute is
	// rejected while the same value was accepted at create. Becomes an
	// x-tfpfgen-create-only correction, which downstream turns into a
	// RequiresReplace recommendation.
	KindImmutable Kind = "immutable"

	// KindRequiredByAPI: the API enforces this attribute's presence
	// whatever the document declares. Learned when a create omitting it is
	// rejected with an error naming the attribute. Becomes a correction
	// adding the property to the schema's required list.
	KindRequiredByAPI Kind = "requiredByAPI"

	// KindRequiredWhen: the attribute is required only when a sibling
	// holds a particular value — the Condition names the sibling and the
	// value. Learned from conditional creates: omission accepted under one
	// sibling value, rejected under another. Becomes an
	// x-tfpfgen-required-when correction.
	KindRequiredWhen Kind = "requiredWhen"

	// KindServerDefault: the constant the API applies when the attribute
	// is omitted — the Value is that constant. Learned by omitting the
	// attribute on create and reading back a stable value. Becomes a
	// correction adding a schema default.
	KindServerDefault Kind = "serverDefault"

	// KindDerivedDefault: the omitted-attribute value is computed — from
	// another field, the tenant, a counter — rather than constant. Learned
	// when two creates that both omit it read back different values. Its
	// correction is a veto: it blocks a serverDefault correction that a
	// single lucky read would otherwise justify, leaving the attribute
	// computed with no declared default.
	KindDerivedDefault Kind = "derivedDefault"

	// KindNormalisation: the server stored a transform of the accepted
	// value — case-folded, trimmed, reformatted — and the Value is the
	// normalised form that came back. Learned when the read-back differs
	// from what was sent but is derivable from it. No mechanical
	// correction today; it is recorded so provider semantic-equality
	// handling can be configured from evidence, and so the finding is not
	// misread as immutability or a server-forced value.
	KindNormalisation Kind = "normalisation"

	// KindIgnoredOnUpdate: an update accepted a new value with a success
	// status and did not apply it. Distinct from immutability — nothing
	// was refused. Learned when the post-update read still shows the old
	// value. Becomes an x-tfpfgen-silently-ignored-on-update correction.
	KindIgnoredOnUpdate Kind = "ignoredOnUpdate"

	// KindServerForced: the server substituted its own value for the one
	// sent — send x, store y, y independent of x. Learned when the stored
	// value neither matches what was sent nor derives from it, and equals
	// what an omitting create produces. Becomes an x-tfpfgen-server-forced
	// correction.
	KindServerForced Kind = "serverForced"

	// KindVolatile: the attribute differs between two identical reads.
	// Learned from the consecutive read. Becomes an x-tfpfgen-volatile
	// correction, which downstream excludes from drift comparison.
	KindVolatile Kind = "volatile"

	// KindValues: what the API actually did with the documented value set.
	// The Value is a Values record listing accepted values, rejected
	// documented values, and whether the set is closed — closed=false
	// meaning an undocumented value was accepted. Learned from conditional
	// creates across the documented values plus one undocumented value.
	// Rejected values become enum-removal corrections; closed=false
	// becomes an x-tfpfgen-values-open correction.
	KindValues Kind = "values"

	// KindUpdateStyle: how the update operation treats omitted fields. The
	// Value is one of "patch-merge" (omitted fields keep their stored
	// values), "put-full" (omitted fields are cleared or reset) or
	// "replace-only" (partial updates are refused outright). Learned by
	// updating one field and re-reading the others. Becomes an
	// x-tfpfgen-update-style correction.
	KindUpdateStyle Kind = "updateStyle"

	// KindDeleteNotFoundOK: a delete answered 404 for an object that is
	// already gone, which generated delete logic should treat as success.
	// Learned from the confirm half of deleteWithConfirmation: delete, verify
	// gone, delete again. Becomes an x-tfpfgen-delete-not-found-ok
	// correction.
	KindDeleteNotFoundOK Kind = "deleteNotFoundOK"

	// KindReadAfterWrite: how long a read lagged a write before showing
	// it. The Value is a duration string such as "2s" — the longest lag
	// the read-back polling measured. Becomes an
	// x-tfpfgen-eventual-consistency correction.
	KindReadAfterWrite Kind = "readAfterWrite"

	// KindUndocumentedFieldInSpec: the API returned or accepted a field
	// the spec's schema does not declare — a real field the document
	// omits. The Value is the field's stable JSON type: "string",
	// "number", "boolean", "object" or "array". Learned from fields
	// present in read-back and consecutive-read responses that the
	// entity's declared schema lacks, envelope noise excluded — only the
	// entity object itself is diffed, never a collection wrapper. Becomes
	// a correction adding the property, with the observed type, to the
	// entity schema's properties.
	KindUndocumentedFieldInSpec Kind = "undocumentedFieldInSpec"
)

// knownKinds is the closed set, for validation.
var knownKinds = map[Kind]bool{
	KindWritable: true, KindImmutable: true, KindRequiredByAPI: true,
	KindRequiredWhen: true, KindServerDefault: true, KindDerivedDefault: true,
	KindNormalisation: true, KindIgnoredOnUpdate: true, KindServerForced: true,
	KindVolatile: true, KindValues: true, KindUpdateStyle: true,
	KindDeleteNotFoundOK: true, KindReadAfterWrite: true,
	KindUndocumentedFieldInSpec: true,
}

// Outcome says how far the audit got with this claim.
type Outcome string

const (
	// OutcomeConfirmed: the finding was made and the Value holds it.
	OutcomeConfirmed Outcome = "confirmed"
	// OutcomeInconclusive: the steps ran but the responses did not
	// discriminate between readings; no Value is claimed.
	OutcomeInconclusive Outcome = "inconclusive"
	// OutcomeBlocked: a precondition failed — a parent could not be
	// created, an input was missing — so the steps never ran.
	OutcomeBlocked Outcome = "blocked"
	// OutcomeTimeoutExhausted: a run limit ran out before this claim's
	// steps could run. Despite the name's emphasis on time, it covers
	// every exhausted budget alike — the request budget, the live-object
	// budget and the time budget.
	OutcomeTimeoutExhausted Outcome = "timeoutExhausted"
)

var knownOutcomes = map[Outcome]bool{
	OutcomeConfirmed: true, OutcomeInconclusive: true,
	OutcomeBlocked: true, OutcomeTimeoutExhausted: true,
}

// Condition scopes a value-conditional observation: the claim held while
// the named sibling attribute equalled the value.
type Condition struct {
	Attribute string `json:"attribute"`
	Equals    any    `json:"equals"`
}

// Values is the Value shape of a KindValues observation: what the API did
// with a documented value set.
type Values struct {
	// Accepted lists documented values the API took.
	Accepted []string `json:"accepted,omitempty"`
	// Rejected lists documented values the API refused — evidence the
	// document is stale.
	Rejected []string `json:"rejected,omitempty"`
	// Closed reports whether an undocumented value was refused (true) or
	// accepted (false); nil when the undocumented check did not run.
	Closed *bool `json:"closed,omitempty"`
}

// UpdateStyle values a KindUpdateStyle observation may carry, matching the
// approved x-tfpfgen-update-style spellings.
var updateStyles = map[string]bool{
	"patch-merge": true, "put-full": true, "replace-only": true,
}

// jsonTypes are the values a KindUndocumentedFieldInSpec observation may
// carry: the JSON type the field was observed with. "null" is absent on
// purpose — a field only ever seen null has no stable type to declare.
var jsonTypes = map[string]bool{
	"string": true, "number": true, "boolean": true, "object": true, "array": true,
}

// Observation is one recorded finding of an audit.
type Observation struct {
	// ID is the stable identity corrections point at: a hash of the
	// entity, attribute, kind and condition — see ComputeID for the exact
	// inputs. It deliberately excludes the value, outcome, excerpts and
	// run stamps, so the same finding re-observed in a later run keeps its
	// identity and a correction's evidence pointer survives re-audits.
	ID string `json:"id"`
	// Entity is the classified entity key the finding is about.
	Entity string `json:"entity"`
	// Attribute is the wire property name, empty for an entity-level
	// finding such as updateStyle or deleteNotFoundOK.
	Attribute string `json:"attribute,omitempty"`
	Kind      Kind   `json:"kind"`
	// Value is the finding itself, present when Outcome is confirmed. It is
	// JSON-marshalable: bool, string, number, []string, a duration string
	// for readAfterWrite, or a Values record for the values kind.
	Value any `json:"value,omitempty"`
	// Condition scopes value-conditional kinds; required for requiredWhen.
	Condition *Condition `json:"condition,omitempty"`
	Outcome   Outcome    `json:"outcome"`
	// Excerpts is the redacted proof: fragments of the requests and
	// responses the finding was read from. Every excerpt passes through
	// Redact before it is stored.
	Excerpts []Excerpt `json:"excerpts,omitempty"`
	// RunID names the audit run that produced this observation.
	RunID string `json:"runId"`
	// SpecHash is the pinned document hash the observation was taken
	// against; revision flags staleness when the pin moves.
	SpecHash   string    `json:"specHash"`
	ObservedAt time.Time `json:"observedAt"`
}

// ComputeID derives an observation's stable identity.
//
// The hash inputs are, in order, NUL-separated: the fixed domain string
// "tfpfgen-observation", the entity key, the attribute (empty for
// entity-level), the kind, and the condition key — empty when
// unconditional, otherwise the condition attribute, "=", and the canonical
// JSON encoding of the equals value. The ID is the first 16 hex characters
// of the SHA-256. Nothing that varies between runs participates.
func ComputeID(entity, attribute string, kind Kind, cond *Condition) string {
	h := sha256.New()
	for _, part := range []string{
		"tfpfgen-observation", entity, attribute, string(kind), conditionKey(cond),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// conditionKey renders a condition canonically for hashing and sorting.
// json.Marshal sorts map keys, so structurally equal conditions render
// identically whatever order they were built in.
func conditionKey(c *Condition) string {
	if c == nil {
		return ""
	}
	raw, err := json.Marshal(c.Equals)
	if err != nil {
		// An unencodable equals value cannot have come through the JSON
		// file layout; render it distinctly rather than colliding with "".
		return c.Attribute + "=!unencodable"
	}
	return c.Attribute + "=" + string(raw)
}

// entityName is the shape an entity key must have to become a file name:
// what classification's keyForPath produces, and nothing that can escape
// the observations directory.
var entityName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validate checks an observation is well-formed enough to be committed or
// trusted after load. Called on both write and read, because the committed
// files are hand-editable and a claim with a mangled kind or a drifted ID
// would otherwise flow into revision and change a spec on the strength of
// nothing.
func (o *Observation) Validate() error {
	at := o.Entity
	if o.Attribute != "" {
		at += "." + o.Attribute
	}
	switch {
	case !entityName.MatchString(o.Entity):
		return fmt.Errorf("observation %q: entity %q is not a valid entity key", o.ID, o.Entity)
	case !knownKinds[o.Kind]:
		return fmt.Errorf("%s: unknown observation kind %q", at, o.Kind)
	case !knownOutcomes[o.Outcome]:
		return fmt.Errorf("%s (%s): unknown outcome %q", at, o.Kind, o.Outcome)
	case o.Condition != nil && o.Condition.Attribute == "":
		return fmt.Errorf("%s (%s): a condition with no attribute constrains nothing", at, o.Kind)
	case o.Kind == KindRequiredWhen && o.Condition == nil:
		return fmt.Errorf("%s: a requiredWhen observation needs the condition it is about", at)
	}
	if want := ComputeID(o.Entity, o.Attribute, o.Kind, o.Condition); o.ID != "" && o.ID != want {
		return fmt.Errorf("%s (%s): id %q does not match the computed %q — hand-edited or corrupted", at, o.Kind, o.ID, want)
	}
	if o.Outcome == OutcomeConfirmed {
		if err := valueShape(o.Kind, o.Value); err != nil {
			return fmt.Errorf("%s (%s): %w", at, o.Kind, err)
		}
	}
	for i := range o.Excerpts {
		if err := o.Excerpts[i].validate(); err != nil {
			return fmt.Errorf("%s (%s): excerpt %d: %w", at, o.Kind, i, err)
		}
	}
	return nil
}

// valueShape refuses a value the kind cannot mean. It accepts both the
// typed shapes an executor constructs and the decoded shapes a JSON read
// produces, because both pass through Validate.
func valueShape(kind Kind, v any) error {
	switch kind {
	case KindWritable, KindImmutable, KindRequiredByAPI, KindRequiredWhen,
		KindDerivedDefault, KindIgnoredOnUpdate, KindServerForced,
		KindVolatile, KindDeleteNotFoundOK:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("value must be a bool, got %T", v)
		}
	case KindServerDefault:
		switch v.(type) {
		case bool, string, int, int64, float64, json.Number:
		default:
			return fmt.Errorf("value must be a scalar, got %T", v)
		}
	case KindNormalisation:
		if _, ok := v.(string); !ok {
			return fmt.Errorf("value must be the normalised string, got %T", v)
		}
	case KindUpdateStyle:
		s, ok := v.(string)
		if !ok || !updateStyles[s] {
			return fmt.Errorf("value must be \"patch-merge\", \"put-full\" or \"replace-only\", got %v", v)
		}
	case KindReadAfterWrite:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("value must be a duration string, got %T", v)
		}
		if d, err := time.ParseDuration(s); err != nil || d < 0 {
			return fmt.Errorf("value %q is not a non-negative duration", s)
		}
	case KindUndocumentedFieldInSpec:
		s, ok := v.(string)
		if !ok || !jsonTypes[s] {
			return fmt.Errorf("value must be a JSON type name (string, number, boolean, object, array), got %v", v)
		}
	case KindValues:
		return valuesShape(v)
	}
	return nil
}

// valuesShape accepts a Values record in either its typed or its decoded
// form.
func valuesShape(v any) error {
	switch t := v.(type) {
	case Values:
		return nil
	case *Values:
		if t == nil {
			return fmt.Errorf("value must be a values record, got a nil one")
		}
		return nil
	case map[string]any:
		for k := range t {
			switch k {
			case "accepted", "rejected", "closed":
			default:
				return fmt.Errorf("value has unknown values key %q", k)
			}
		}
		return nil
	default:
		return fmt.Errorf("value must be a values record, got %T", v)
	}
}

// Sort orders observations so a committed document is byte-stable: by
// entity, attribute, kind, then condition key. Never by discovery order —
// the order steps ran in is an implementation detail, and letting it into
// a committed artefact would churn the file whenever the plan is
// reordered.
func Sort(obs []Observation) {
	sort.SliceStable(obs, func(i, j int) bool {
		a, b := obs[i], obs[j]
		if a.Entity != b.Entity {
			return a.Entity < b.Entity
		}
		if a.Attribute != b.Attribute {
			return a.Attribute < b.Attribute
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return conditionKey(a.Condition) < conditionKey(b.Condition)
	})
}
