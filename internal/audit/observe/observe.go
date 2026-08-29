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
// revision stage holds a compilation rule for every kind when compiling
// observations into proposed corrections, and a kind it has no rule for
// would be silently dropped — the one failure mode an evidence store must
// not have.
//
// Each kind's comment states three things: what the claim means, how the
// audit learns it, and which correction it can become.
type Kind string

const (
	// KindWritable: the API accepts this attribute on write and returns it
	// (true), or accepts it and never returns it — absent from the answer,
	// or answered masked (false). Learned by sending a value on create and
	// reading it back. False becomes a correction marking the property
	// writeOnly, which downstream keeps configurable with state holding
	// the configured value: the wire cannot tell a discarded value from
	// one stored under another name, and readOnly would take a property
	// the API may require away from the practitioner.
	KindWritable Kind = "writable"

	// KindImmutable: the API refuses to change this attribute after
	// create. Learned when an update naming only this attribute is
	// rejected while the same value was accepted at create. Becomes an
	// x-tfpfgen-immutable correction, which downstream turns into a
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
	// value — case-folded, trimmed, extended, the same instant, reordered
	// — and the Value is the normalised form that came back. Learned when
	// the read-back differs from what was sent but is derivable from it.
	// Becomes an x-tfpfgen-normalisation correction naming the kind, read
	// back from the excerpts, so generated state keeps the configured
	// spelling; recorded also so the finding is not misread as
	// immutability or a server-forced value.
	KindNormalisation Kind = "normalisation"

	// KindIgnoredOnUpdate: an update accepted a new value with a success
	// status and did not apply it. Distinct from immutability — nothing
	// was refused. Learned when the post-update read still shows the old
	// value. Becomes an x-tfpfgen-ignored-on-update correction.
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
	// becomes an x-tfpfgen-values correction.
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
	// x-tfpfgen-read-after-write correction.
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

	// The conditional-edge kinds below are never drawn from one probe.
	// They are asserted only by the triangulating inference
	// (internal/audit/infer), which builds one model per entity from all
	// of a run's evidence — the per-variant create outcomes, the request
	// adjustments the executor was forced to make, and the strategy's
	// declared claims — and confirms an edge only where those signals
	// converge in both directions. A lone ambiguous 4xx is by design not
	// enough to assert one. Each carries a Provenance saying whether a
	// structural or prose claim grounded it or it was derived from
	// probing alone.

	// KindValidConfiguration: the entity has several distinct valid
	// configurations, selected by a discriminator value — the variant
	// structure the executor probed one gate value at a time. The
	// Attribute is the discriminator (gate) field; the Value is the sorted
	// list of gate values each of which produced a valid object. Learned
	// when two or more gate values each created successfully and at least
	// one field is valid under one value and refused under another. Emitted
	// as x-tfpfgen-valid-configuration.
	KindValidConfiguration Kind = "validConfiguration"

	// KindValidWhen: a field or block is valid only when a sibling gate
	// field holds a specific value — the core conditional edge. The
	// Attribute is the subject field; the Condition names the gate field
	// and value it is valid under; the Value is true. Learned by variant
	// diffing: the field is accepted in a create under exactly one gate
	// value and removed (as not valid) under at least one other, both
	// directions observed. Emitted as x-tfpfgen-valid-when.
	KindValidWhen Kind = "validWhen"

	// KindDependsOn: a field is settable only when a second field is also
	// present, whatever that second field's value — a co-requirement, not a
	// value condition. The Attribute is the dependent field; the Value is
	// the name of the field it requires. Learned when a create carrying the
	// dependent field but not its requirement is refused naming both, the
	// executor adds the requirement, and the retry succeeds — corroborated,
	// not read from a single ambiguous refusal. Emitted as
	// x-tfpfgen-depends-on.
	KindDependsOn Kind = "dependsOn"

	// KindMutuallyExclusive: at most one of a set of fields may be set. The
	// Attribute is empty (the finding is about the set, not one field); the
	// Value is the sorted list of the mutually-exclusive field names.
	// Learned when each field is accepted on its own but a create carrying
	// two of them together is refused, reproducibly. Emitted as
	// x-tfpfgen-mutually-exclusive.
	KindMutuallyExclusive Kind = "mutuallyExclusive"

	// KindListWrapper: whether a list (collection) response wraps its items
	// under a key of an object, and which key. Entity-level (empty
	// Attribute); the Value is a ListWrapper record. Learned from the
	// collection reads the executor captured, not from the document,
	// because every API wraps differently and the document often lies.
	KindListWrapper Kind = "listWrapper"

	// KindListPagination: the pagination style a list response advertises.
	// Entity-level (empty Attribute); the Value is one of "cursor",
	// "offset", "page" or "none". Read from the wire, like the wrapping,
	// and recorded separately because the two are unrelated facts about
	// one response.
	KindListPagination Kind = "listPagination"

	// KindIdentifierProperty: the response property that carries the value
	// the item path addresses the object by. Entity-level (empty Attribute);
	// the Value is the property name. Learned by matching the id the run
	// already extracted against the response body's own properties, because
	// a path parameter named "id" and a body property named "aid" are the
	// same identifier and nothing in the document says so.
	KindIdentifierProperty Kind = "identifierProperty"
)

// knownKinds is the closed set, for validation.
var knownKinds = map[Kind]bool{
	KindWritable: true, KindImmutable: true, KindRequiredByAPI: true,
	KindRequiredWhen: true, KindServerDefault: true, KindDerivedDefault: true,
	KindNormalisation: true, KindIgnoredOnUpdate: true, KindServerForced: true,
	KindVolatile: true, KindValues: true, KindUpdateStyle: true,
	KindDeleteNotFoundOK: true, KindReadAfterWrite: true,
	KindUndocumentedFieldInSpec: true,
	KindValidConfiguration:      true, KindValidWhen: true,
	KindDependsOn: true, KindMutuallyExclusive: true,
	KindListWrapper: true, KindListPagination: true, KindIdentifierProperty: true,
}

// Kinds is the closed observation-kind set as a sorted fresh slice, so a
// package that must cover every kind can hold itself to the vocabulary
// rather than to a second list of its own.
func Kinds() []Kind {
	out := make([]Kind, 0, len(knownKinds))
	for kind := range knownKinds {
		out = append(out, kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Provenance records how strongly an inferred edge is grounded: a structural
// claim (the document's own composition keywords) is strongest, a prose
// claim (mined description text) weaker, and derived means the edge was
// concluded from live probing alone with no declared claim behind it. It
// mirrors the strategy package's vocabulary and is carried only on the
// conditional-edge kinds the inference asserts.
type Provenance string

const (
	ProvenanceStructural Provenance = "structural"
	ProvenanceProse      Provenance = "prose"
	ProvenanceDerived    Provenance = "derived"
)

var knownProvenances = map[Provenance]bool{
	ProvenanceStructural: true, ProvenanceProse: true, ProvenanceDerived: true,
}

// ListWrapper is the Value of a KindListWrapper observation: whether a
// collection response wraps its items, and under which key.
type ListWrapper struct {
	// Wrapped is true when the items sit under a key of an object, false
	// when the response is the item array itself.
	Wrapped bool `json:"wrapped"`
	// Key is the wrapping key when Wrapped, empty otherwise.
	Key string `json:"key,omitempty"`
}

var listPaginations = map[string]bool{"cursor": true, "offset": true, "page": true, "none": true}

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
	// Condition scopes value-conditional kinds; required for requiredWhen
	// and validWhen.
	Condition *Condition `json:"condition,omitempty"`
	// Provenance says how an inferred conditional edge was grounded —
	// structural, prose or derived. Empty on the scalar kinds an executor
	// reads from one probe, since those are not grounded in a claim.
	// It does not participate in the ID: the same edge grounded two ways in
	// two runs keeps one identity.
	Provenance Provenance `json:"provenance,omitempty"`
	Outcome    Outcome    `json:"outcome"`
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
	case o.Kind == KindValidWhen && o.Condition == nil:
		return fmt.Errorf("%s: a validWhen observation needs the gate condition it is about", at)
	case o.Provenance != "" && !knownProvenances[o.Provenance]:
		return fmt.Errorf("%s (%s): unknown provenance %q", at, o.Kind, o.Provenance)
	}
	if want := ComputeID(o.Entity, o.Attribute, o.Kind, o.Condition); o.ID != "" && o.ID != want {
		return fmt.Errorf("%s (%s): id %q does not match the computed %q — hand-edited or corrupted", at, o.Kind, o.ID, want)
	}
	if o.Outcome == OutcomeConfirmed {
		if err := validateValueForKind(o.Kind, o.Value); err != nil {
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

// validateValueForKind refuses a value the kind cannot mean. It accepts both the
// typed shapes an executor constructs and the decoded shapes a JSON read
// produces, because both pass through Validate.
func validateValueForKind(kind Kind, v any) error {
	switch kind {
	case KindWritable, KindImmutable, KindRequiredByAPI, KindRequiredWhen,
		KindDerivedDefault, KindIgnoredOnUpdate, KindServerForced,
		KindVolatile, KindDeleteNotFoundOK, KindValidWhen:
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
		return validateValuesValue(v)
	case KindDependsOn:
		s, ok := v.(string)
		if !ok || s == "" {
			return fmt.Errorf("value must be the name of the required field, got %v", v)
		}
	case KindValidConfiguration, KindMutuallyExclusive:
		if !isStringList(v) {
			return fmt.Errorf("value must be a list of field or gate-value names, got %T", v)
		}
	case KindListWrapper:
		return listWrapperValue(v)
	case KindListPagination:
		style, ok := v.(string)
		if !ok || !listPaginations[style] {
			return fmt.Errorf("value must be one of cursor, offset, page, none, got %v", v)
		}
	case KindIdentifierProperty:
		s, ok := v.(string)
		if !ok || s == "" {
			return fmt.Errorf("value must be the name of the identifying property, got %v", v)
		}
	}
	return nil
}

// isStringList accepts a []string or the []any-of-strings a JSON round trip
// produces.
func isStringList(v any) bool {
	switch t := v.(type) {
	case []string:
		return len(t) > 0
	case []any:
		if len(t) == 0 {
			return false
		}
		for _, e := range t {
			if _, ok := e.(string); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// listWrapperValue accepts a ListWrapper in typed or decoded form. A wrapped
// response must name the key its items sit under: read as unwrapped it would
// quietly unwrap a response that is not an array.
func listWrapperValue(v any) error {
	var w ListWrapper
	switch t := v.(type) {
	case ListWrapper:
		w = t
	case *ListWrapper:
		if t == nil {
			return fmt.Errorf("value must be a list-wrapper record, got a nil one")
		}
		w = *t
	case map[string]any:
		for k := range t {
			switch k {
			case "wrapped", "key":
			default:
				return fmt.Errorf("list-wrapper has unknown key %q", k)
			}
		}
		w.Wrapped, _ = t["wrapped"].(bool)
		w.Key, _ = t["key"].(string)
	default:
		return fmt.Errorf("value must be a list-wrapper record, got %T", v)
	}
	switch {
	case w.Wrapped && w.Key == "":
		return fmt.Errorf("a wrapped list response must name the key its items sit under")
	case !w.Wrapped && w.Key != "":
		return fmt.Errorf("an unwrapped list response wraps nothing, so a key is meaningless")
	}
	return nil
}

// validateValuesValue accepts a Values record in either its typed or its decoded
// form.
func validateValuesValue(v any) error {
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
// entity, attribute, placement, kind, then condition key. Never by discovery
// order — the order steps ran in is an implementation detail, and letting it
// into a committed artefact would churn the file whenever the plan is
// reordered.
//
// Placement orders one attribute's kinds by what each needs to exist first.
// Revision applies corrections in this order, so a kind that declares a
// property must come before every kind that annotates one: an API demanding a
// property the document omits yields both an undocumentedFieldInSpec and a
// requiredByAPI, and required cannot be written onto a property no schema
// declares yet.
func Sort(obs []Observation) {
	sort.SliceStable(obs, func(i, j int) bool {
		a, b := obs[i], obs[j]
		if a.Entity != b.Entity {
			return a.Entity < b.Entity
		}
		if a.Attribute != b.Attribute {
			return a.Attribute < b.Attribute
		}
		if placement(a.Kind) != placement(b.Kind) {
			return placement(a.Kind) < placement(b.Kind)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return conditionKey(a.Condition) < conditionKey(b.Condition)
	})
}

// placement ranks a kind by what it needs to exist before it can be applied:
// a kind that declares a property sorts ahead of every kind that annotates
// one.
func placement(k Kind) int {
	if k == KindUndocumentedFieldInSpec {
		return 0
	}
	return 1
}
