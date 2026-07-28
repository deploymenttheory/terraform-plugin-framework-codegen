package probe

// This file is the catalogue.
//
// Every probe is registered here with its name, kind, worst-case cost, and a doc
// comment stating three things: what it sends, what Terraform-relevant fact it infers,
// and **how it can be wrong**. The third is not documentation etiquette. A probe whose
// failure mode nobody has written down is a probe whose facts nobody can weigh, and the
// whole store is worth only as much as it is trusted.
//
// Registering the catalogue before implementing it is deliberate. It makes the costs,
// the priorities and the stated failure modes reviewable before a single request is
// issued at anybody's tenant, and it makes `probe -list` useful with no credentials.
//
// # Order
//
// The read-only tier runs first and is safe against any API including production. Within
// it, R4 runs before everything because it calibrates the others. The mutating tier runs
// only behind the full gating conjunction.
//
// # Not in this catalogue, on purpose
//
//   - POST idempotency. It deliberately creates a duplicate; the cleanup story is worse
//     than the fact is worth.
//   - Pagination exhaustion. Needs a tenant with enough data, and the SDK already
//     documents it as unverifiable.
//   - Rate-limit exhaustion. Antisocial in somebody's sandbox, and the
//     x-organization-rate-limit headers already state the budget.
//   - Delete cascade, and whether a 202 delete ever completes. Both need deliberate
//     destruction of related objects or open-ended waiting, and neither is something a
//     prefix sweep can guarantee to undo.
//   - Auth-scope checking. Marginal.
func init() {
	// Read-only tier, in run order.
	registerRead(unknownParamTolerance{})
	registerRead(notFoundShape{})
	registerRead(listShape{})
	registerRead(volatileOnRead{})
	registerRead(errorEnvelope{})
	registerRead(returnedOnReadWeak{})

	// Mutating tier, in run order.
	registerMutating(requiredByAPI{})
	registerMutating(writableAndReturned{})
	registerMutating(updateStyle{})
	registerMutating(serverDefault{})
	registerMutating(immutability{})
	registerMutating(enumBoundary{})
	registerMutating(writeSideEffect{})
	registerMutating(normalization{})
	// Last, and it has to be. It creates nothing and reports the consistency window the session
	// measured while the probes above read back the objects *they* created -- so registered
	// second, as it was when it created its own object, it saw nothing and concluded nothing.
	registerMutating(readYourWrites{})
}

// ---------------------------------------------------------------------------
// Read-only tier
// ---------------------------------------------------------------------------

// unknownParamTolerance sends one GET to the collection with a query parameter the API
// cannot know, and observes whether it is ignored or rejected.
//
// It infers no Terraform fact directly. Its job is to **calibrate every other probe**:
// an API that silently ignores an unknown query parameter very likely also ignores an
// unknown body field, and "unknown fields are ignored rather than rejected" is the
// assumption the writability and server-default protocols rest on. Running it first
// means those protocols know whether their own premise holds.
//
// This is the kind of thing that gets discovered by hand and written into a comment: an
// unrecognized parameter is ignored while a misspelled *known* one is rejected. Formalising it
// means later probes can state their premise rather than assume it.
//
// How it can be wrong: a gateway rather than the application may reject the parameter,
// in which case tolerance of body fields does not follow. The fact is emitted at
// Observed for the query-parameter claim itself and never used to conclude anything about
// bodies on its own.
type unknownParamTolerance struct{}

func (unknownParamTolerance) Name() string   { return "read.unknown-param" }
func (unknownParamTolerance) Kind() Kind     { return KindRead }
func (unknownParamTolerance) Cost(Scope) int { return 1 }

// notFoundShape sends one GET for a well-formed but absent identifier, and observes the
// status and body shape.
//
// It infers whether policy.delete.notFoundIsSuccess is implementable, and it feeds the
// generated error mapper. This matters more than it looks: internal/ingest currently hardcodes
// NotFoundIsSuccess to true with no evidence at all, and if an API in fact answers 403 or an
// empty 200 for an absent object, the generated delete swallows a real failure.
//
// How it can be wrong: an API that returns 403 for another tenant's identifier is
// indistinguishable from one that returns 403 for an absent one. That is itself worth
// recording, so the outcome is a fact about the observed status rather than a claim about
// absence.
type notFoundShape struct{}

func (notFoundShape) Name() string   { return "read.not-found-shape" }
func (notFoundShape) Kind() Kind     { return KindRead }
func (notFoundShape) Cost(Scope) int { return 1 }

// listShape sends one GET to the collection and observes the envelope key, whether a
// next-page link is present, and the shape of an item.
//
// Its intrinsic value is low; its job is to be the fixture source for later probes and
// the evidence for the sandbox-size assertion.
//
// How it can be wrong: it does not, so much as it frequently observes nothing. An empty
// tenant is the *normal* state of a good sandbox, so this must degrade to "no
// observation" cleanly rather than erroring -- a probe that failed on an empty collection
// would make a clean sandbox look like a broken one.
type listShape struct{}

func (listShape) Name() string   { return "read.list-shape" }
func (listShape) Kind() Kind     { return KindRead }
func (listShape) Cost(Scope) int { return 1 }

// volatileOnRead reads the same item three times, spaced by an interval, and observes
// which fields differ.
//
// It infers Volatile, which is what catches the perpetual-diff class: a modifiedDate that
// changes on every read makes every plan report drift forever, and the only fix is
// marking it Computed before anybody publishes the provider.
//
// A diff across all three reads is Observed. No diff produces **no fact at all** --
// stability over five seconds is not evidence of stability, and emitting Volatile=false
// would license merge to act on an absence.
//
// How it can be wrong: something else in the tenant may be editing the object
// concurrently, which manufactures a false Volatile. Mitigated by preferring an object
// the prober created itself, where nothing else has a reason to touch it.
type volatileOnRead struct{}

func (volatileOnRead) Name() string   { return "read.volatile" }
func (volatileOnRead) Kind() Kind     { return KindRead }
func (volatileOnRead) Cost(Scope) int { return 3 }

// errorEnvelope sends one deliberately malformed read -- a typed query parameter given a
// bad value -- and observes which error shape comes back for which status.
//
// APIs commonly use more than one envelope, sometimes several across one endpoint family --
// see internal/probe/apierr. Knowing which appears where feeds the generated mocks in Phase 5,
// and it validates the prober's own classifier, which every other probe depends on: telling
// "rejected because immutable" from "rejected because the token expired" is what makes the
// immutability protocol possible at all.
//
// How it can be wrong: one malformed request exercises one code path. An API may use a
// different envelope for authorization failures than for validation failures, so the fact
// is scoped to the status actually observed rather than generalised.
type errorEnvelope struct{}

func (errorEnvelope) Name() string   { return "read.error-envelope" }
func (errorEnvelope) Kind() Kind     { return KindRead }
func (errorEnvelope) Cost(Scope) int { return 1 }

// returnedOnReadWeak reads one item and notes which of the schema's JSON paths are absent.
//
// Deliberately capped at Suspected. An absent field could mean "never returned", or "null
// for this particular object", or "returned only with an expansion parameter" -- and
// nothing in a single read distinguishes them. The trustworthy version of this fact
// requires having *written* the field first, which is what write.writable-returned does.
//
// It is registered separately only so that read-only mode produces something for a
// resource with no create operation, where the stronger probe cannot run.
//
// How it can be wrong: in all three ways above, which is exactly why it may never be
// merged. Its output is a prompt to look, not a conclusion.
type returnedOnReadWeak struct{}

func (returnedOnReadWeak) Name() string   { return "read.returned-weak" }
func (returnedOnReadWeak) Kind() Kind     { return KindRead }
func (returnedOnReadWeak) Cost(Scope) int { return 1 }

// ---------------------------------------------------------------------------
// Mutating tier
// ---------------------------------------------------------------------------

// requiredByAPI creates once with a full fixture, then once per candidate field with
// exactly that field omitted, and observes which omissions are refused.
//
// It infers RequiredByAPI, which is frequently not what the specification declares -- in both
// directions. The pilot blueprint has two live examples: two attributes are marked required
// although the request schema declares no required list at all.
//
// A 2xx on omission gives RequiredByAPI=false at Observed, because a success is
// unambiguous. A 4xx gives true at Observed only when the error body *names the field*;
// otherwise Inferred, because the request may have failed for an unrelated reason.
//
// How it can be wrong: **conditional requirement.** Hand-maintained fixup tables in existing
// providers are full of these -- a port field that matters only when a protocol field says tcp
// -- and one-field-at-a-time omission from a single fixture reports half a truth either way. Mitigated by running every fixture variant the plan declares: a field whose
// requiredness differs between variants produces a Note and an Alternatives entry, never
// a Fact.
type requiredByAPI struct{}

func (requiredByAPI) Name() string { return "write.required" }
func (requiredByAPI) Kind() Kind   { return KindMutating }

// Cost is one create for the baseline plus one per key the fixture sets, per fixture.
//
// The domain is the fixture's *own* keys, not every writable field. Omitting a field the fixture
// never set is not an experiment -- the baseline already omitted it -- and counting those was most
// of why the unnarrowed catalogue asked for 226 creates against a cap of 25.
// Cost is a create and a delete per attempt. The delete is the correction: an omission the API
// *accepts* leaves an object behind, and costing this probe at one request per attempt
// under-budgeted every successful omission -- which is the common case on any API whose declared
// required list is shorter than its real one.
func (requiredByAPI) Cost(sc Scope) int { return 2 * omissionCreates(sc) }

// Creates is one per attempt: the baseline, plus one omission per key the fixture sets.
func (requiredByAPI) Creates(sc Scope) int { return omissionCreates(sc) }

// readYourWrites reads each created object immediately, retrying with backoff when the
// read 404s or comes back missing fields.
//
// It infers policy.readBack: whether the generated create and update need a re-read loop,
// how many retries, and how long between them.
//
// The confidence is deliberately asymmetric. enabled=true is Observed, because the
// failure was seen. enabled=false is only Inferred -- one fast success does not prove
// consistency. So merge may **add** a read-back but never remove one, which is the safe
// direction: a needless re-read costs a request, a missing one costs a failed apply.
//
// How it can be wrong: a consistency window measured on an idle sandbox is not
// production's. The measured interval is recorded as a floor with its provenance attached,
// never as a property of the real system.
type readYourWrites struct{}

func (readYourWrites) Name() string { return "write.read-your-writes" }
func (readYourWrites) Kind() Kind   { return KindMutating }

// Nearly free: the first read after a create is one this probe would be doing anyway.
// Cost is nothing at all: this probe reports the consistency window the session already
// measured when reading back the objects other probes created.
//
// Zero rather than a create of its own, because a probe that created an object purely to time how
// long it took to appear would be spending the scarcest budget there is to learn something every
// other mutating probe learns for free.
func (readYourWrites) Cost(Scope) int    { return 0 }
func (readYourWrites) Creates(Scope) int { return 0 }

// writableAndReturned creates with every writable field set to a distinctive value, then
// reads back and compares.
//
// It infers Writable and ReturnedOnRead -- the pair that decides between Computed and
// Optional+Computed, and the pair that settles the pilot blueprint's three unprobed
// computed_optional guesses in the writable direction.
//
// Equal means Writable=true and ReturnedOnRead=true. Absent means ReturnedOnRead=false,
// and here it *is* Observed rather than Suspected, because the field was demonstrably
// sent. Present but different is **not** Writable=false: that is a normalization fact, and
// conflating the two would mark a perfectly writable field as computed.
//
// How it can be wrong: **expansion-gated fields.** Any API with an expand, include or fields
// parameter may withhold a field until asked. A probe that reads back once concludes
// ReturnedOnRead=false, and the generated state mapper then blanks a real value on every
// refresh. So this reads back twice: bare, and with every expansion the plan lists.
type writableAndReturned struct{}

func (writableAndReturned) Name() string { return "write.writable-returned" }
func (writableAndReturned) Kind() Kind   { return KindMutating }

// One create plus a bare read plus one read per expansion.
// Cost is one create, read, expansion read and delete per fixture.
//
// Per fixture rather than per field: one create carrying every sendable field observes all of
// them at once, which is the whole reason this probe gates the others.
func (writableAndReturned) Cost(sc Scope) int    { return fixtureCount(sc) * 4 }
func (writableAndReturned) Creates(sc Scope) int { return fixtureCount(sc) }

// updateStyle creates with two fields set, reads to confirm both, updates sending only
// the first, then reads again.
//
// It infers policy.updateStyle. If the second field survived, the API merges; if it was
// cleared, the request must carry the whole object. internal/ingest currently derives this
// from the HTTP verb alone, and the IR's own comment says what getting it wrong costs:
// the generated provider "silently erases attributes the practitioner never mentioned".
//
// This is one of the few facts a single sequence genuinely settles, provided the
// interstitial read happened -- without it, "the field was never set" and "the field was
// cleared" are the same observation.
//
// How it can be wrong: an API that distinguishes an absent key from an explicit null would
// behave differently for the two, and a request body built from a Go struct with omitempty
// cannot express the difference. That is why the prober builds bodies as maps.
type updateStyle struct{}

func (updateStyle) Name() string { return "write.update-style" }
func (updateStyle) Kind() Kind   { return KindMutating }

// Cost is one create, then a full update, a partial update and a null update, each followed by a
// read, then a delete.
func (updateStyle) Cost(sc Scope) int { return needsUpdate(sc, withFixture(sc, 9)) }

// Creates is one: the three update shapes are all exercised against the same object, because the
// question is what an update does rather than what a create does.
func (updateStyle) Creates(sc Scope) int { return needsUpdate(sc, withFixture(sc, 1)) }

// serverDefault establishes whether an omitted field's value is a constant, is derived, or
// means the field is not settable at all.
//
// Terraform cares about exactly those three outcomes: a constant default can be
// Optional plus a static Default; a derived default must be computed_optional with no
// static default; not-settable is plain Computed. This is the probe that settles the
// pilot's three computed_optional guesses.
//
// The protocol is three creates, and each one rules something out. Two byte-identical creates
// catch a value that varies between them, which rules out a constant -- a counter, a timestamp, a
// random assignment. A third built from a different fixture catches a value derived from the
// request. Every one of the three observes *every* omitted field simultaneously, because a default
// appears in the response body whether or not this probe was looking for that field.
//
// An earlier draft of this contract called for five, the fourth and fifth setting each field
// explicitly to catch the case where it was never writable and the observed value merely collided
// with the default. That step is real and is not skipped -- it is performed by
// write.writable-returned, which runs first and sets every sendable field, and whose conclusion
// this probe reads out of the run's findings rather than re-establishing. Five creates per field
// was 32% of the whole catalogue's cost and most of it was re-observing the same three responses.
//
// How it can be wrong: the dominant false positive is treating a *derived* value as a
// constant -- a color hashed from a key, a counter, a timestamp -- and writing it into the
// blueprint as stringdefault.StaticString, which is then a permanent lie. The second and third
// creates exist for that, and the fact carries "derived from tenant configuration" and
// "derived from a field this plan did not vary" as standing alternatives.
type serverDefault struct{}

func (serverDefault) Name() string { return "write.server-default" }
func (serverDefault) Kind() Kind   { return KindMutating }

// The most expensive probe in the catalogue: five creates and five reads per candidate
// field. Worth every request, because this is the guess sitting in the committed blueprint.
// Cost is a create, a read and a delete per step.
//
// Not scaled by fixture count, unlike most of the catalogue: the protocol is one baseline, and its
// third step *is* the second fixture. Running the whole thing again per fixture would buy nothing
// -- the omitted set is the fields no fixture sets, so it is the same set either way.
func (serverDefault) Cost(sc Scope) int { return 3 * (serverDefault{}).Creates(sc) }

// Creates is two byte-identical creates, plus a third from a second fixture where one exists.
//
// With one fixture the derived-from-request check cannot be run, so the third create would tell us
// nothing and is not budgeted for.
func (serverDefault) Creates(sc Scope) int {
	switch len(sc.Fixtures()) {
	case 0:
		return 0
	case 1:
		return 2
	default:
		return 3
	}
}

// immutability establishes whether the API refuses to change a field after create.
//
// Six requests and a control. A 4xx on an update alone proves nothing, and there are at
// least five other explanations for it: the value was invalid rather than the field
// immutable; the whole request was rejected for an unrelated reason, which is very likely
// under putFull semantics; the field is immutable only after some state transition; the API
// returned 200 and silently ignored the change, which is **not** immutability; or the
// request was rate limited.
//
// So: create with v1, read to confirm v1 is observable, update with v1 unchanged as a
// **control** -- if that fails, the update request shape itself is wrong and every later
// conclusion is an artefact of that -- create a second object with v2 to prove v2 is an
// acceptable value at all, then update the first object to v2. A 2xx that leaves the value
// at v1 emits silentlyIgnoredOnUpdate rather than Immutable, because they are different
// facts that happen to want similar handling.
//
// Immutable=true requires Corroborated: two fixtures and two distinct values, both refused.
//
// How it can be wrong: in the five ways above, each of which one step of the protocol
// exists to eliminate. And regardless of outcome, this **never** sets a plan modifier.
// Whether Terraform should destroy and recreate is a decision about somebody's
// infrastructure; the toolkit's job is to put the evidence in front of the person making it.
type immutability struct{}

func (immutability) Name() string { return "write.immutability" }
func (immutability) Kind() Kind   { return KindMutating }

// Cost is, per candidate field: two creates, three reads, three updates and two deletes.
//
// Ten, not the eight an earlier draft assumed. The count that was missing is the *second* update
// attempt and its read -- and those are not optional: one refusal is consistent with the value
// simply being invalid, so Immutable=true requires two distinct values both refused. A cost model
// that budgeted for one would have refused the run partway through the evidence it needs.
//
// The domain is Scope.Immutable(), which is the fields with *two or more* candidate values. That
// is not a convenience -- Immutable=true requires Corroborated, which needs two distinct proven
// values, so a field with one candidate cannot reach the confidence the fact demands and probing
// it would produce nothing but a note.
func (immutability) Cost(sc Scope) int { return needsUpdate(sc, 10*immutabilityFields(sc)) }

// Creates is two per candidate field: the object under test, and the control that proves the
// second value is acceptable at all.
func (immutability) Creates(sc Scope) int { return needsUpdate(sc, 2*immutabilityFields(sc)) }

// enumBoundary asks three questions of a field the specification documents as an enum:
// is the set closed, are the documented values actually accepted, and is it case
// sensitive.
//
// It emits enumClosed, enumAccepted and enumRejectedDocumented. **None of them generates an
// active validator**, ever. The README and one of the pilot blueprint's own attribute
// descriptions already state why: an over-tight validator rejects configurations the API would
// have accepted, and the practitioner cannot work around it. Generated SDKs frequently model
// enums as open strings for the same reason, so a routine upstream addition must not become a
// plan failure. Merge writes the accepted set into the attribute's description and reports
// that a OneOf is now supportable by hand.
//
// The valuable result is the third one: a **documented value the API rejects** means the
// specification is stale, and a spec-derived validator would have been actively harmful.
//
// How it can be wrong: a negative value must be shaped like the documented ones -- short,
// lower-case, hyphenated -- or a length or character-set validator answers instead of enum
// membership, and the probe concludes the set is closed when it merely rejected a
// forty-character string. Two negative candidates are required, both rejected. And a value
// the sandbox refuses may be licence-gated rather than nonexistent, so the fact says
// "rejected here", never "does not exist".
//
// The documented values come from the specification, not from a human. That distinction is what
// makes enumRejectedDocumented worth anything: if somebody had transcribed the set by hand, a
// rejection would mean "the API disagrees with what somebody typed". blueprint.AttrType.Enum
// carries them through ingest for this one consumer, and the pilot is already a case in point --
// its committed object_type description lists five values where the specification declares six.
//
// Its cost is zero against a subject whose fields declare none, and -list reports the zero rather
// than hiding it: a probe that costs nothing because there is nothing to check should look
// different from one that found nothing.
type enumBoundary struct{}

func (enumBoundary) Name() string { return "write.enum" }
func (enumBoundary) Kind() Kind   { return KindMutating }

// Cost is a create and a delete per value tried: every documented value, two generated negatives,
// and one case variant per enum field.
func (enumBoundary) Cost(sc Scope) int { return 2 * (enumValueCount(sc) + len(sc.Enums())) }

// Creates is one per value tried. A rejected value creates nothing, so this is the worst case in
// which the API accepts everything -- which is exactly the case that produces the interesting
// fact, that the enum is not closed.
func (enumBoundary) Creates(sc Scope) int { return enumValueCount(sc) + len(sc.Enums()) }

// writeSideEffect looks for a field the server set that was never sent, then perturbs the
// suspected trigger and re-reads to confirm the coupling.
//
// Coupled fields of this kind -- enabling one measurement silently enables another -- turn up
// in hand-maintained fixup tables, and they are the class of quirk a human would never guess
// from a specification and a prober genuinely can find.
//
// It stays Inferred even after confirmation. "The server set it in response to this
// request" and "the server always sets it, for every object of this type" are different
// claims, and one perturbation establishes only the first. Emitted as a sideEffect fact
// feeding documentation and a Phase 5 test; the IR has nowhere else to put it.
//
// How it can be wrong: a field that changes could be a server default rather than a side
// effect of the field that was sent, which is why confirmation requires perturbing the
// trigger rather than just noticing the correlation once.
type writeSideEffect struct{}

func (writeSideEffect) Name() string { return "write.side-effect" }
func (writeSideEffect) Kind() Kind   { return KindMutating }

// Cost is three creates with their reads and deletes: two carrying the trigger, one without.
//
// Three rather than a pair, and not scaled by fixture count. The identical pair is what establishes
// which unsent fields are stable at all -- without it the identifier and every other per-object
// value the server assigns differ between two objects and read as coupling. And the question is
// about one trigger, so a second fixture would ask it again with nothing new.
func (writeSideEffect) Cost(sc Scope) int { return 3 * (writeSideEffect{}).Creates(sc) }

func (writeSideEffect) Creates(sc Scope) int {
	if len(sc.Fixtures()) == 0 || len(sc.Influencers()) == 0 {
		return 0
	}

	return 3
}

// normalization sends awkward values -- mixed case, surrounding whitespace, a reversed
// collection -- and observes what comes back changed.
//
// It is the highest-value class of fact in the catalogue, because server normalization is the
// direct cause of a perpetual diff: the practitioner writes one value, the API stores another,
// and every subsequent plan proposes changing it back. Existing providers carry runtime helpers
// that re-sort collections purely to suppress this, which is the wrong layer to fix it at.
//
// A specific identified transform is Observed; "changed somehow" is Suspected. A 4xx is
// **no observation** rather than evidence -- a rejected value tells you nothing about what
// the server would have done with an accepted one.
//
// How it can be wrong: it needs a value generator per type to be thorough, and a first cut
// covering case, whitespace and collection order will miss numeric coercion, date
// reformatting and unicode normalization. Those are absences rather than errors: the probe
// under-reports rather than reporting wrongly.
type normalization struct{}

func (normalization) Name() string { return "write.normalization" }
func (normalization) Kind() Kind   { return KindMutating }

// Three awkward values per writable string field, each needing a create and a read.
// Cost is one create, read and delete per awkward value, with every string field carrying that
// value at once.
//
// Grouped by transform rather than by field: whether the server trims whitespace is a property of
// the server far more often than of one field, and one create carrying "  padded  " in every
// string field observes all of them together. Per field per value was 3 x fields creates for an
// answer that is nearly always uniform.
// Cost is a create and a read per transform, plus the delete. Not scaled by fixture count: the
// question is what the server does to a shape, and a second fixture sends the same shapes.
func (normalization) Cost(sc Scope) int {
	if len(sc.Fixtures()) == 0 {
		return 0
	}

	return 3 * len(normalizationTransforms)
}

func (normalization) Creates(sc Scope) int {
	if len(sc.Fixtures()) == 0 {
		return 0
	}

	return len(normalizationTransforms)
}

// ---------------------------------------------------------------------------
// Cost helpers
// ---------------------------------------------------------------------------

// immutabilityFields is how many fields the immutability protocol will exercise.
//
// With a plan it is the fields carrying two or more candidates, which is precise. Without one it
// is every sendable field, because an operator has not yet said which are worth probing and the
// honest worst case is "any of them". Reporting zero unplanned would tell an operator the most
// expensive probe in the catalogue is free.
func immutabilityFields(sc Scope) int {
	if !sc.Planned {
		return len(sc.Sendable())
	}

	return len(sc.Immutable())
}

// needsUpdate zeroes a cost for a resource with no update operation.
//
// Not an optimisation: a probe that cannot run costs nothing, and reporting its nominal cost would
// overstate the budget for every resource the API only lets you create and delete.
func needsUpdate(sc Scope, cost int) int {
	if sc.Subject.Update == nil {
		return 0
	}

	return cost
}

// fixtureCount is how many fixture variants a probe will run.
//
// One when no plan was supplied, so an unplanned -list reports the cost of the smallest real run
// rather than zero -- a catalogue that costs nothing because nobody supplied a plan is a figure
// an operator cannot budget against.
func fixtureCount(sc Scope) int {
	if n := len(sc.Fixtures()); n > 0 {
		return n
	}

	return 1
}

// withFixture scales a per-fixture cost.
func withFixture(sc Scope, perFixture int) int { return fixtureCount(sc) * perFixture }

// omissionCreates counts the requiredness protocol's creates: a baseline plus one omission per key
// the fixture actually sets.
func omissionCreates(sc Scope) int {
	if !sc.Planned {
		// No fixture, so no keys are known. The unnarrowed worst case is every sendable field,
		// which is what -list should report rather than a small number that means nothing.
		return 1 + len(sc.Sendable())
	}

	total := 0
	for _, f := range sc.Fixtures() {
		total += 1 + len(sc.Omittable(f))
	}

	return total
}

// stringFieldCount counts writable string fields, which are the only ones the
// normalization probe has awkward values for.
// enumValueCount counts the values the enum protocol will send: every documented value, plus two
// generated negatives per enum field.
//
// Two negatives rather than one, because a single rejection is not evidence of a closed set: an
// API can reject one value for a reason that has nothing to do with the enum.
func enumValueCount(sc Scope) int {
	n := 0
	for _, f := range sc.Enums() {
		n += len(f.Enum) + negativeEnumCandidates
	}

	return n
}

// negativeEnumCandidates is how many values outside the documented set are tried per field.
const negativeEnumCandidates = 2
