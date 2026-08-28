package quirkserver

// Conditional describes a requirement that depends on another field's value.
//
// The archetypal case is a port field that matters only when a protocol field
// says tcp. It is the quirk that proves one-field-at-a-time omission reports
// half a truth, and that the auditor must flag the disagreement between two
// fixtures rather than pick a side.
type Conditional struct {
	// When is the field/value pair that triggers the requirement.
	WhenField string
	WhenValue any
	// Then is the field that becomes required.
	Then string
}

// Quirks turns each misbehaviour on.
//
// The zero value is a well-behaved API, so a test enables exactly the quirk it
// is about.
type Quirks struct {
	// SilentlyDiscards accepts these fields on write and never stores them.
	//
	// Ground truth for a field that cannot be written, and the trap that makes
	// a naive read-back check wrong: the field was demonstrably sent and is
	// demonstrably absent.
	SilentlyDiscards []string

	// ImmutableAfterCreate refuses to change these fields on update, with a
	// problem+json body that names the field.
	ImmutableAfterCreate []string

	// RequiresExtraFieldOnUpdate makes update reject any request omitting this
	// field, even though create does not need it.
	//
	// The quirk that proves the immutability protocol's control request is
	// load-bearing rather than ceremony: without the control, the auditor sees
	// a 4xx on update and concludes immutability when the request shape was
	// simply wrong.
	RequiresExtraFieldOnUpdate string

	// ConstantDefaults are values assigned when a field is omitted. Ground
	// truth for a real server-side default.
	ConstantDefaults map[string]any

	// DerivedDefaults assigns a value computed from another field.
	//
	// An auditor without the derivation check confidently writes this down as
	// a static default, which is then a permanent lie. The map is target field
	// to source field.
	DerivedDefaults map[string]string

	// CounterDefault assigns an incrementing value to this field when omitted,
	// so two byte-identical creates differ. Exercises the
	// two-identical-creates check.
	CounterDefault string

	// RequiredButUndeclared rejects a create omitting these, naming them in
	// the error. The key/object_type case.
	RequiredButUndeclared []string

	// ConditionallyRequired makes one field required only when another has a
	// given value.
	ConditionallyRequired *Conditional

	// DiscardsWhen accepts field Then on create and stores it only when the
	// request's WhenField does NOT hold WhenValue; on the matching branch it
	// is silently dropped.
	//
	// The conditional variant of SilentlyDiscards, and the matchType case
	// exactly: sent on a static tag, 201, and the value is gone; sent on a
	// dynamic one, stored and returned. An audit measuring one branch reports
	// a half-truth about both.
	DiscardsWhen *Conditional

	// WriteSideEffects sets one field as a consequence of another being
	// written.
	//
	// Enabling one measurement silently enabling another: the class of quirk a
	// human would never guess from a specification and an auditor genuinely
	// can find. Maps the trigger field to the field it also sets.
	WriteSideEffects map[string]string

	// NormalisesCase lower-cases these fields' values.
	NormalisesCase []string
	// TrimsWhitespace strips surrounding whitespace from these fields.
	TrimsWhitespace []string
	// SortsLists sorts these list-valued fields.
	//
	// Hand-written providers carry runtime helpers that re-sort collections
	// purely to suppress the drift this causes, which is the wrong layer to
	// fix it at.
	SortsLists []string

	// ExpansionGated returns these fields only when the matching expansion
	// query parameter is present.
	//
	// An audit that reads back once concludes the field is never returned, and
	// the generated state mapper then blanks a real value on every refresh.
	// Any API with an expand, include or fields parameter can do this. Maps
	// field name to the value that reveals it.
	ExpansionGated map[string]string

	// SilentlyDiscardsOnUpdate accepts these fields on update, answers 2xx,
	// and leaves the stored value alone. Create stores them normally.
	//
	// The behaviour that has to be distinguishable from immutability, and
	// conflating the two is the classic error: an API that refuses a change
	// says so, and one that drops it does not. Only the second produces a
	// perpetual diff in a generated provider.
	SilentlyDiscardsOnUpdate []string

	// PutClearsOmitted makes update replace what is stored rather than
	// preserve what the request omits.
	PutClearsOmitted bool

	// EventuallyConsistentReads makes the first N reads of a new object 404.
	EventuallyConsistentReads int

	// NamesRefusedFieldInProse makes a refusal spell the missing field into a
	// sentence — "field serial is required" — rather than leaving the bare
	// name in the detail beside a generic title.
	//
	// Both are real. The bare form is deliberately uncorrectable, because a name
	// alone beside a generic title could be about anything; this is the form
	// an adjustment loop can act on, and an API that writes it is one whose
	// refusals teach the auditor what the create needs.
	NamesRefusedFieldInProse bool

	// ErrorBody selects which shape errors take. Defaults to problem+json.
	ErrorBody ErrorBodyShape

	// ClosedEnum rejects values outside the listed set, per field.
	ClosedEnum map[string][]string
	// RejectsValueUnless refuses one field's value except while another field
	// holds a given value, keyed "field=value" to a Conditional whose Then is
	// unused.
	//
	// The endpoint-agent case as ground truth: a documented objectType that is
	// refused while creating a static tag and perfectly good for a dynamic
	// one. An auditor that injects candidates into one arbitrary body writes
	// the combination's refusal down as a truth about the value; escalation
	// into the other declared fixtures is what corrects it.
	RejectsValueUnless map[string]Conditional

	// RejectsDocumentedValue refuses a value the specification documents, per
	// field.
	//
	// The valuable enum result: the specification is stale, and a spec-derived
	// validator would have been actively harmful.
	RejectsDocumentedValue map[string]string

	// DeleteFails makes every delete fail, which must stop the run creating
	// more and leave orphans reported.
	DeleteFails bool
	// DeleteFlakyEvery makes every Nth delete fail.
	DeleteFlakyEvery int

	// RateLimitHeaders emits a rate-limit header trio and 429s once the budget
	// is spent, which lets pacing be tested with no live tenant.
	RateLimitHeaders bool
	// RateLimit is the budget when RateLimitHeaders is set.
	RateLimit int

	// VolatileFields change on every read. The modifiedDate perpetual-diff
	// class.
	VolatileFields []string

	// IgnoresUnknownQueryParams returns 200 for an unrecognised query
	// parameter rather than 400.
	//
	// Ground truth for the checks that depend on unknown *body* fields
	// being ignored rather than rejected — what the audit summary reports
	// as rejectsUnknownFields.
	IgnoresUnknownQueryParams bool

	// TypedQueryParams reject a bad value, which is how the error-envelope
	// check provokes an error without mutating anything.
	TypedQueryParams []string

	// BasePath serves the collection under a prefix, e.g. "/v7".
	//
	// Real endpoints carry one, and it is the difference between the relative
	// path an audit assembles and the full path the wire actually sees. Bugs
	// hide behind its absence -- anything comparing the two silently assumes
	// they are the same string -- so the fixture is able to model it
	// deliberately.
	BasePath string

	// NotFoundStatus overrides the status for an absent object. An API that
	// returns 403 for another tenant's identifier is indistinguishable from
	// one that returns it for an absent one, and that is itself worth
	// observing.
	NotFoundStatus int

	// Forces stores this value for a field regardless of what was sent, on
	// create and update alike, and echoes the forced value back.
	//
	// The networkMeasurements case: send false, the API stores true, and the
	// practitioner's value can never take effect. Distinct from a
	// normalisation (a transform of the sent value) and from SilentlyDiscards
	// (nothing stored at all).
	Forces map[string]any

	// NullsInWriteResponse accepts these fields, stores nothing, and answers
	// explicit null for them in every response -- write responses and reads
	// alike.
	//
	// The includeHeaders case, and distinct from SilentlyDiscards on exactly
	// the axis the auditor must keep: present-and-null is a different
	// observation from absent, and the two must not be conflated.
	NullsInWriteResponse []string

	// SuppressWhenSibling drops field Then from storage whenever the same
	// request's WhenField holds WhenValue, on create and update alike.
	//
	// The postBody case: stored and echoed by a small body, stripped the
	// moment requestMethod:"get" rides along. Distinct from DiscardsWhen,
	// which models the create-branch variant only -- this is the field
	// interaction a maximal update meets and a minimal one never does.
	SuppressWhenSibling *Conditional

	// UpdateDefaults assigns this value when the field is omitted from an
	// update, overriding whatever the object held. Create is untouched.
	//
	// The full-replace footgun as ground truth: an API whose PUT does not
	// preserve an omitted field but resets it to a constant -- and the
	// constant may differ from the create-path default, which is why it is its
	// own map rather than a flag on ConstantDefaults.
	UpdateDefaults map[string]any
}
