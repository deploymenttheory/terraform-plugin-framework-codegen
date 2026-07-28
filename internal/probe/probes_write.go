package probe

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// The mutating probe bodies. Their contracts -- what each sends, what it infers, and how each can
// be wrong -- are documented on the types in catalogue.go, and this file implements them without
// restating that.
//
// Three conventions run through all of them, and each exists because of a specific way a mutating
// probe can produce a wrong fact rather than no fact:
//
//   - **Read back through the session, never with a bare Get.** MutatingSession.ReadCreated
//     retries while an object is not yet visible. A probe that read once would record "the field
//     is absent" at Observed confidence when what it saw was an eventually-consistent read.
//
//   - **Two distinct values, or no fact about writability.** Send one value, read it back, and
//     "the server stored what I sent" and "the server ignored me and returned its own value" are
//     equally good explanations. Only a second, different value separates them -- which is why
//     Writable=false requires two fixtures and is Corroborated when it has them.
//
//   - **Partial results are returned alongside an error.** MaxDeleteFailures defaults to zero, so
//     a run stops early routinely, and a probe that returned nothing on the way out would lose
//     facts it had already established honestly.

// sentinelFor is the distinctive value a probe sends for a field.
//
// Derived from the field's own path so two fields never carry the same value: if they did, a
// response that echoed one back under the other's key would read as correct. Deterministic,
// because a cassette records exact bodies.
func sentinelFor(f Field, round int) any {
	switch f.Kind {
	case blueprint.KindBool:
		// Alternating rather than always true, so the second round differs from the first --
		// which is the whole basis of telling "stored what I sent" from "returned its own".
		return round%2 == 1
	case blueprint.KindInt64, blueprint.KindFloat64, blueprint.KindNumber:
		return 1000 + round
	default:
		return fmt.Sprintf("probe-%s-%d", strings.ReplaceAll(f.JSONPath, ".", "-"), round)
	}
}

// observation is what one create-and-read-back round saw about one field.
type observation struct {
	fixture string
	// sent is what the request carried, and readBack what the response returned.
	sent     any
	readBack any
	// outcome distinguishes absent from ambiguous, because they demand opposite responses.
	outcome FieldOutcome
	// gated is true when the field appeared only once an expansion was asked for.
	gated bool
	// evidence is the exact interaction the read came from.
	evidence string
}

// writableAndReturned implements the contract on the type in catalogue.go.
func (p writableAndReturned) Exercise(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
) (Result, error) {
	var out Result

	if len(sc.Fixtures()) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "no fixture was supplied, so there is no valid body to send",
		})

		return out, nil
	}

	// Keyed by JSON path, one entry per fixture round. Accumulated across rounds because the
	// interesting conclusions are cross-round: a field whose read-back does not change when the
	// sent value does is a field the server is ignoring.
	seen := map[string][]observation{}

	for round := range sc.Fixtures() {
		fixture, _ := sc.Fixture(round)

		body, sent := p.bodyFor(s, sc, fixture, round)

		resp, id, err := s.Create(ctx, p.Name(), body)
		out.Requests++

		if err != nil {
			// Returned with whatever earlier rounds established rather than discarded.
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, Probe: p.Name(),
				Message: fmt.Sprintf("fixture %s could not be created: %v", fixture.Name, err),
			})

			return out, err
		}

		if resp.Status >= 400 {
			// A refused create is an observation, not a failure -- but it settles nothing about
			// writability, so it is a note.
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, Probe: p.Name(),
				Message: fmt.Sprintf("fixture %s was refused with %d (%s), so nothing was "+
					"observed about writability from it",
					fixture.Name, resp.Status, resp.Error().Detail),
			})

			continue
		}

		bare, err := s.ReadCreated(ctx, p.Name(), id, nil)
		out.Requests++

		if err != nil {
			return out, err
		}
		if e := bare.Error(); e.IsAuth() {
			return out, authError(e)
		}

		expanded := p.readExpanded(ctx, s, sc, id, &out)

		for path, value := range sent {
			field, _ := sc.Subject.Field(path)
			seen[path] = append(seen[path], observe(fixture.Name, field, value, bare, expanded))
		}
	}

	p.conclude(sc, seen, &out)
	p.noteDenied(sc, &out)

	return out, nil
}

// bodyFor builds one round's request body and reports what it sent, by JSON path.
//
// The name field always carries the stamped name: the session refuses a body without it, because
// an object the sweeper cannot find by name is an object that survives a crash.
func (p writableAndReturned) bodyFor(
	s *MutatingSession,
	sc Scope,
	fixture Fixture,
	round int,
) (map[string]any, map[string]any) {
	body := fixture.Body
	sent := map[string]any{}

	for _, f := range sc.Sendable() {
		// Only top-level keys can be set independently; a nested path is carried by whatever
		// its parent holds, and overwriting the parent would discard the fixture's own shape.
		if strings.Contains(f.JSONPath, ".") {
			continue
		}

		switch {
		case f.JSONPath == sc.Subject.NameField:
			body[f.JSONPath] = s.NameValue(p.Name(), round+1)
		case f.Kind.IsCollection(), f.Kind.IsNested():
			// Left as the fixture declared it. A synthesised collection would be rejected for
			// its shape, and the probe would then record a fact about validation.
			if _, ok := body[f.JSONPath]; !ok {
				continue
			}
		default:
			body[f.JSONPath] = sentinelFor(f, round+1)
		}

		sent[f.JSONPath] = body[f.JSONPath]
	}

	// Nested paths the fixture set are still observed, they are simply not synthesised.
	for _, f := range sc.Sendable() {
		if !strings.Contains(f.JSONPath, ".") {
			continue
		}
		if v, ok := fieldIn(body, f.JSONPath); ok {
			sent[f.JSONPath] = v
		}
	}

	return body, sent
}

// readExpanded reads the object again with every expansion the plan lists, returning nil when
// there are none.
//
// This is the probe's stated failure mode made into a request. Any API with an expand, include or
// fields parameter may withhold a field until asked; a probe that read once would conclude
// ReturnedOnRead=false, and the generated state mapper would then blank a real value on every
// refresh.
func (p writableAndReturned) readExpanded(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	id string,
	out *Result,
) *Response {
	query := expansionQuery(sc)
	if len(query) == 0 {
		return nil
	}

	resp, err := s.ReadCreated(ctx, p.Name(), id, query)
	out.Requests++

	if err != nil {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("the expansion read failed (%v), so a field returned only "+
				"under expansion would look absent", err),
		})

		return nil
	}

	if resp.Status >= 400 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("the expansion read answered %d, so the plan's expansions may "+
				"not be the parameters this API uses", resp.Status),
		})

		return nil
	}

	return resp
}

// expansionQuery turns the plan's expansion strings into query parameters.
func expansionQuery(sc Scope) url.Values {
	query := url.Values{}

	for _, raw := range sc.Plan.Expansions {
		key, value, found := strings.Cut(raw, "=")
		if !found {
			continue
		}
		query.Add(key, value)
	}

	if len(query) == 0 {
		return nil
	}

	return query
}

// observe records what one read saw about one field.
func observe(fixture string, f Field, sent any, bare, expanded *Response) observation {
	o := observation{fixture: fixture, sent: sent, evidence: bare.Interaction}

	value, outcome := bare.LookupField(f.JSONPath)
	o.readBack, o.outcome = value, outcome

	if outcome == Present || expanded == nil {
		return o
	}

	// Absent bare, so try the expansion. A field that appears only here is the trap this probe
	// exists to catch, and it is recorded as gated rather than as returned: the generated
	// provider has to ask for it.
	if value, outcome := expanded.LookupField(f.JSONPath); outcome == Present {
		o.readBack, o.outcome, o.gated = value, outcome, true
		o.evidence = expanded.Interaction
	}

	return o
}

// conclude turns the per-field observations into facts.
//
// Every branch here is a decision about what a set of observations does *not* rule out, which is
// why the fact each produces carries its alternatives.
func (p writableAndReturned) conclude(sc Scope, seen map[string][]observation, out *Result) {
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	// Sorted, because map iteration order is randomised and the report is committed.
	sort.Strings(paths)

	for _, path := range paths {
		rounds := seen[path]

		switch {
		case anyAmbiguous(rounds):
			// A path crossing an array of more than one element: which element was meant
			// cannot be known, and guessing would produce a fact about element zero labelled
			// as a fact about the field.
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
				Message: "the read-back crossed a collection holding more than one element, so " +
					"which element this path refers to is ambiguous and no fact was recorded",
			})

		case allAbsent(rounds):
			out.Facts = append(out.Facts, p.absentFact(sc, path, rounds))

			// Deliberately no writability fact. From outside, "accepted and discarded" and
			// "stored and never returned" are the same observation, and merge would act on the
			// first by making the attribute Computed. Saying so is the difference between an
			// unprobed field and one this protocol cannot reach.
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
				Message: "the field never came back, so whether the API stored it cannot be " +
					"observed from a read: discarded and stored-but-not-returned look identical " +
					"from here",
			})

		default:
			out.Facts = append(out.Facts, p.returnedFact(sc, path, rounds))

			if fact, ok := p.writableFact(sc, path, rounds); ok {
				out.Facts = append(out.Facts, fact)
			} else {
				out.Notes = append(out.Notes, Note{
					Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
					Message: "the field was returned, but one round cannot separate \"the " +
						"server stored what was sent\" from \"the server returned its own " +
						"value\"; a second fixture sending a different value would settle it",
				})
			}
		}

		if gated := gatedRounds(rounds); gated > 0 {
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
				Message: fmt.Sprintf("this field was returned only when an expansion was "+
					"requested (%d of %d round(s)); the generated read must ask for it or it "+
					"will blank a real value on every refresh", gated, len(rounds)),
			})
		}
	}
}

// absentFact records a field that was sent and never came back.
func (p writableAndReturned) absentFact(sc Scope, path string, rounds []observation) Fact {
	return Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   path,
		Field:      FactReturnedOnRead,
		Value:      BoolValue(false),
		Confidence: Observed,
		Probe:      p.Name(),
		Evidence:   evidenceOf(rounds),
		Rationale: fmt.Sprintf(
			"the field was sent on %d create(s) and appeared in no read response, including "+
				"every expansion the plan declares", len(rounds)),
		Alternatives: []string{
			"the API may return it only on the collection endpoint rather than the item one",
			"an expansion parameter this plan does not know about may reveal it",
		},
	}
}

// returnedFact records a field that came back.
func (p writableAndReturned) returnedFact(sc Scope, path string, rounds []observation) Fact {
	confidence := Observed
	if len(rounds) > 1 && allPresent(rounds) {
		confidence = Corroborated
	}

	return Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   path,
		Field:      FactReturnedOnRead,
		Value:      BoolValue(true),
		Confidence: confidence,
		Probe:      p.Name(),
		Evidence:   evidenceOf(rounds),
		Rationale:  fmt.Sprintf("the field appeared in the read after %d create(s)", len(rounds)),
	}
}

// writableFact decides whether the API stored what was sent, and reports false only when it can.
//
// The asymmetry is deliberate and is the whole reason this probe runs more than one fixture.
// Writable=true needs only that the read-back tracked the sent value. Writable=false is the
// claim that the API accepted a value and discarded it, which merge will act on by making an
// attribute Computed -- so it requires two distinct sent values producing the same read-back,
// and it is Corroborated when it has them.
func (p writableAndReturned) writableFact(
	sc Scope,
	path string,
	rounds []observation,
) (Fact, bool) {
	if len(rounds) < 2 {
		return Fact{}, false
	}

	distinctSent := distinctValues(rounds, func(o observation) any { return o.sent })
	if distinctSent < 2 {
		// Both rounds sent the same value, usually because the fixture pinned it. Nothing here
		// separates the two explanations.
		return Fact{}, false
	}

	distinctBack := distinctValues(rounds, func(o observation) any { return o.readBack })

	if distinctBack < 2 {
		return Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   path,
			Field:      FactWritable,
			Value:      BoolValue(false),
			Confidence: Corroborated,
			Probe:      p.Name(),
			Evidence:   evidenceOf(rounds),
			Rationale: fmt.Sprintf(
				"two creates sent %d distinct values for this field and both read back the "+
					"same value, so the API accepts it and does not store it",
				distinctSent),
			Alternatives: []string{
				"the API may store the value and return a canonical form of it that happens to " +
					"be identical for both inputs",
			},
		}, true
	}

	echoed := allEchoed(rounds)

	confidence := Corroborated
	rationale := "two creates sent distinct values and each read back the value it sent"

	if !echoed {
		// The read-back tracked the input without equalling it: the value was stored and
		// transformed. Writable, and the transform is write.normalisation's to describe --
		// conflating the two would mark a perfectly writable field as computed.
		rationale = "two creates sent distinct values and each read back a distinct value that " +
			"was not identical to it, so the field is stored and transformed"
	}

	return Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   path,
		Field:      FactWritable,
		Value:      BoolValue(true),
		Confidence: confidence,
		Probe:      p.Name(),
		Evidence:   evidenceOf(rounds),
		Rationale:  rationale,
		Alternatives: alternativesUnless(echoed,
			"the differing read-backs may reflect something other than the sent value, such as "+
				"a counter or a timestamp"),
	}, true
}

// noteDenied reports every field the plan withheld.
//
// Silence would read as agreement with whatever the blueprint already claims, and the reason a
// field is denied -- it is writable and has consequences -- is exactly the reason its existing
// guess deserves scrutiny.
func (p writableAndReturned) noteDenied(sc Scope, out *Result) {
	for _, path := range sc.Plan.Deny {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
			Message: "the plan denies this field, so nothing was sent for it and whatever the " +
				"blueprint currently claims about it is still unprobed",
		})
	}
}

// updateStyle implements the contract on the type in catalogue.go.
func (p updateStyle) Exercise(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
) (Result, error) {
	var out Result

	if sc.Subject.Update == nil {
		out.Facts = append(out.Facts, Fact{
			Resource: sc.Subject.Resource,
			Field:    FactUpdateStyle,
			Value:    TextValue(string(blueprint.UpdateReplaceOnly)),
			// Inferred, not Observed. Nothing was sent: this restates what the blueprint already
			// says, and a fact with no traffic behind it must not outrank one that has some.
			Confidence: Inferred,
			Probe:      p.Name(),
			Rationale: "the blueprint records no update operation, so every writable attribute " +
				"needs replacement rather than an in-place change",
		})

		return out, nil
	}

	fixture, ok := sc.Fixture(0)
	if !ok {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "no fixture was supplied, so there is no valid body to update",
		})

		return out, nil
	}

	// The omitted field. Anything sendable, top-level and not the name field -- the name has to
	// stay in every request, for a reason worth stating: on an API that clears omitted fields,
	// an update without the name would clear the stamped prefix, and the sweeper would then be
	// unable to find the object it had just orphaned.
	victim, found := p.victim(sc, fixture)
	if !found {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "the fixture sets no field that can safely be omitted from an update; the " +
				"name field must stay in every request or a cleared name would strand the object",
		})

		return out, nil
	}

	body := fixture.Body
	body[sc.Subject.NameField] = s.NameValue(p.Name(), 1)
	body[victim.JSONPath] = sentinelFor(victim, 1)

	resp, id, err := s.Create(ctx, p.Name(), body)
	out.Requests++

	if err != nil {
		return out, err
	}
	if resp.Status >= 400 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("the create was refused with %d, so nothing was observed "+
				"about update style", resp.Status),
		})

		return out, nil
	}

	before, err := s.ReadCreated(ctx, p.Name(), id, nil)
	out.Requests++

	if err != nil {
		return out, err
	}

	// The interstitial read is what makes this settle anything: without it, "the field was never
	// set" and "the field was cleared" are the same observation.
	if _, outcome := before.LookupField(victim.JSONPath); outcome != Present {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: victim.JSONPath, Probe: p.Name(),
			Message: "the field chosen to omit did not come back on the read before the update, " +
				"so a cleared value and a never-stored one cannot be told apart and no update " +
				"style was recorded",
		})

		return out, nil
	}

	renamed := s.NameValue(p.Name(), 2)

	update, err := s.Update(ctx, p.Name(), id, map[string]any{sc.Subject.NameField: renamed})
	out.Requests++

	if err != nil {
		return out, err
	}

	if update.Status >= 400 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("the partial update was refused with %d (%s); an API that "+
				"requires the whole object on update cannot be probed this way, and the "+
				"immutability protocol's control request is the place that distinguishes it",
				update.Status, update.Error().Detail),
		})

		return out, nil
	}

	after, err := s.ReadCreated(ctx, p.Name(), id, nil)
	out.Requests++

	if err != nil {
		return out, err
	}

	out.Facts = append(out.Facts, p.styleFact(sc, victim, before, after))

	if fact, ok := p.ignoredFact(sc, renamed, after); ok {
		out.Facts = append(out.Facts, fact)
	}

	return out, nil
}

// victim picks the field to omit from the update.
func (p updateStyle) victim(sc Scope, fixture Fixture) (Field, bool) {
	var candidates []Field

	for _, f := range sc.Sendable() {
		if f.JSONPath == sc.Subject.NameField || strings.Contains(f.JSONPath, ".") {
			continue
		}
		if _, set := fixture.Body[f.JSONPath]; !set {
			continue
		}
		// A collection is a poor choice: an API that merges lists element-wise would look like
		// one that merges objects, and the fact would be about the wrong thing.
		if f.Kind.IsCollection() || f.Kind.IsNested() {
			continue
		}

		candidates = append(candidates, f)
	}

	if len(candidates) == 0 {
		return Field{}, false
	}

	// Sorted, so the same plan always omits the same field and the recorded transcript is
	// reproducible.
	sortFields(candidates)

	return candidates[0], true
}

// styleFact reports whether the omitted field survived.
func (p updateStyle) styleFact(sc Scope, victim Field, before, after *Response) Fact {
	was, _ := before.LookupField(victim.JSONPath)
	now, outcome := after.LookupField(victim.JSONPath)

	survived := outcome == Present && reflect.DeepEqual(was, now)

	style := blueprint.UpdatePutFull
	rationale := fmt.Sprintf(
		"an update omitting %s cleared it, so a generated update must carry the whole object",
		victim.JSONPath)

	if survived {
		style = blueprint.UpdateMergePatch
		rationale = fmt.Sprintf(
			"an update omitting %s left it unchanged, so the API merges", victim.JSONPath)
	}

	return Fact{
		Resource:   sc.Subject.Resource,
		Field:      FactUpdateStyle,
		Value:      TextValue(string(style)),
		Confidence: Observed,
		Probe:      p.Name(),
		Evidence:   []string{before.Interaction, after.Interaction},
		Rationale:  rationale,
		Alternatives: []string{
			"an API that distinguishes an absent key from an explicit null may treat the two " +
				"differently, and only the absent case was sent",
			fmt.Sprintf("the behaviour was observed for %s and is assumed to hold for every "+
				"other field", victim.JSONPath),
		},
	}
}

// ignoredFact reports an update that answered 2xx and did not apply what it was sent.
//
// Distinct from immutability, and conflating the two is the classic error: an API that refuses a
// change says so, and one that silently drops it does not. Only the second produces a perpetual
// diff in a generated provider.
func (p updateStyle) ignoredFact(sc Scope, sent string, after *Response) (Fact, bool) {
	got, outcome := after.LookupField(sc.Subject.NameField)
	if outcome != Present {
		return Fact{}, false
	}

	if text, ok := got.(string); ok && text == sent {
		return Fact{}, false
	}

	return Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   sc.Subject.NameField,
		Field:      FactSilentlyIgnoredOnUpdate,
		Value:      BoolValue(true),
		Confidence: Observed,
		Probe:      p.Name(),
		Evidence:   []string{after.Interaction},
		Rationale: fmt.Sprintf(
			"an update answered success and the field read back as %v rather than the %q that "+
				"was sent", got, sent),
		Alternatives: []string{
			"the value may have been normalised rather than ignored, which write.normalisation " +
				"would distinguish",
			"the read may have been served by a replica that had not yet caught up",
		},
	}, true
}

// readYourWrites implements the contract on the type in catalogue.go.
//
// It creates nothing. Every mutating probe reads back the objects it makes through the session,
// so the consistency window is already measured by the time this runs -- and a probe that created
// an object purely to time how long it took to appear would spend the scarcest budget there is to
// learn something the run already knows.
func (p readYourWrites) Exercise(
	_ context.Context,
	s *MutatingSession,
	sc Scope,
) (Result, error) {
	var out Result

	measured := s.ReadBack()

	if measured.Objects == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "no object was read back during this run, so there was nothing to measure; " +
				"this probe reports what the other mutating probes observed and has nothing to " +
				"say when they did not run",
		})

		return out, nil
	}

	needed := measured.Retried > 0

	// The asymmetry is the point. enabled=true is Observed because the failure was seen;
	// enabled=false is only Inferred, because one fast success does not prove consistency. So
	// merge may add a read-back and never remove one: a needless re-read costs a request, and a
	// missing one costs a failed apply.
	confidence := Inferred
	rationale := fmt.Sprintf(
		"%d object(s) were read immediately after creation and every first read succeeded",
		measured.Objects)

	if needed {
		confidence = Observed
		rationale = fmt.Sprintf(
			"%d of %d object(s) were not readable on the first attempt (statuses %s) and became "+
				"readable after up to %d retry(ies) at %s",
			measured.Retried, measured.Objects, statusList(measured.Statuses),
			measured.WorstRetries, measured.Interval)
	}

	out.Facts = append(out.Facts, Fact{
		Resource:   sc.Subject.Resource,
		Field:      FactReadBack,
		Value:      ReadBackValue(measuredReadBack(measured, needed)),
		Confidence: confidence,
		Probe:      p.Name(),
		Evidence:   measured.Evidence,
		Rationale:  rationale,
		Alternatives: []string{
			"a consistency window measured on an idle sandbox is not production's; the interval " +
				"is a floor rather than a property of the real system",
		},
	})

	return out, nil
}

// measuredReadBack turns the session's measurement into the blueprint's shape.
func measuredReadBack(m ReadBackMeasurement, needed bool) blueprint.ReadBack {
	if !needed {
		return blueprint.ReadBack{
			Enabled: false,
			Reason: fmt.Sprintf("every one of %d read(s) immediately after a create succeeded "+
				"on the first attempt", m.Objects),
		}
	}

	return blueprint.ReadBack{
		Enabled: true,
		// One more than was needed, because the measurement is a floor: the worst case observed
		// on an idle sandbox is not the worst case in production.
		MaxRetries: m.WorstRetries + 1,
		IntervalMS: int(m.Interval.Milliseconds()),
		Reason: fmt.Sprintf("%d of %d object(s) were not readable immediately after creation",
			m.Retried, m.Objects),
	}
}

// statusList renders the statuses seen, for a rationale.
func statusList(statuses []int) string {
	if len(statuses) == 0 {
		return "none recorded"
	}

	parts := make([]string, 0, len(statuses))
	for _, s := range statuses {
		parts = append(parts, fmt.Sprint(s))
	}
	sort.Strings(parts)

	return strings.Join(parts, ", ")
}

// -- shared reasoning over observations ----------------------------------------------------

func anyAmbiguous(rounds []observation) bool {
	for _, o := range rounds {
		if o.outcome == Ambiguous {
			return true
		}
	}

	return false
}

func allAbsent(rounds []observation) bool {
	for _, o := range rounds {
		if o.outcome != Absent {
			return false
		}
	}

	return len(rounds) > 0
}

func allPresent(rounds []observation) bool {
	for _, o := range rounds {
		if o.outcome != Present {
			return false
		}
	}

	return len(rounds) > 0
}

// allEchoed reports whether every round read back exactly what it sent.
func allEchoed(rounds []observation) bool {
	for _, o := range rounds {
		if o.outcome != Present || !reflect.DeepEqual(o.sent, o.readBack) {
			return false
		}
	}

	return len(rounds) > 0
}

func gatedRounds(rounds []observation) int {
	n := 0
	for _, o := range rounds {
		if o.gated {
			n++
		}
	}

	return n
}

// distinctValues counts how many different values a projection took across rounds.
//
// The number the writability conclusion rests on: two distinct sent values producing one distinct
// read-back is the signature of a field the API accepts and discards.
func distinctValues(rounds []observation, of func(observation) any) int {
	var seen []any

	for _, o := range rounds {
		v := of(o)

		found := false
		for _, existing := range seen {
			if reflect.DeepEqual(existing, v) {
				found = true
				break
			}
		}

		if !found {
			seen = append(seen, v)
		}
	}

	return len(seen)
}

// evidenceOf collects the interactions the rounds read from.
func evidenceOf(rounds []observation) []string {
	var out []string

	for _, o := range rounds {
		if o.evidence != "" {
			out = appendUnique(out, o.evidence)
		}
	}

	return out
}

// alternativesUnless returns the alternative only when the condition does not hold.
//
// A fact's alternatives are the explanations its sequence did *not* rule out, so an alternative
// that has been ruled out must not be listed: a reader who cannot tell the difference between a
// live alternative and a boilerplate one has no reason to read any of them.
func alternativesUnless(ruledOut bool, alternative string) []string {
	if ruledOut {
		return nil
	}

	return []string{alternative}
}

// requiredByAPI implements the contract on the type in catalogue.go.
func (p requiredByAPI) Exercise(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
) (Result, error) {
	var out Result

	if len(sc.Fixtures()) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "no fixture was supplied, so there is nothing to omit a field from",
		})

		return out, nil
	}

	// Keyed by JSON path: one omission attempt per fixture that sets the field. Accumulated
	// across fixtures because the interesting outcome is disagreement -- a field required in one
	// variant and not in another is conditionally required, and reporting either half as a fact
	// would be reporting half a truth.
	attempts := map[string][]omission{}

	for round := range sc.Fixtures() {
		fixture, _ := sc.Fixture(round)

		// The baseline. Without it a rejection cannot be attributed to the omission: the fixture
		// itself might simply not be acceptable.
		baseline, err := p.create(ctx, s, sc, round, "")
		out.Requests++

		if err != nil {
			return out, err
		}

		if baseline.status >= 400 {
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, Probe: p.Name(),
				Message: fmt.Sprintf("fixture %s was refused with %d (%s), so no omission from "+
					"it can be attributed to the omitted field",
					fixture.Name, baseline.status, baseline.detail),
			})

			continue
		}

		for _, key := range sc.Omittable(fixture) {
			attempt, err := p.create(ctx, s, sc, round, key)
			out.Requests++

			if err != nil {
				// Returned with what earlier attempts established rather than discarded.
				return out, err
			}

			attempts[key] = append(attempts[key], attempt)
		}
	}

	p.concludeRequired(sc, attempts, &out)
	p.noteNameField(sc, &out)

	return out, nil
}

// omission is one create with one key left out.
type omission struct {
	fixture string
	// omitted is the key left out, empty for the baseline.
	omitted string
	status  int
	// named is true when the error body named the omitted field, which is what separates an
	// Observed conclusion from an Inferred one.
	named    bool
	detail   string
	evidence string
}

// create issues one create, optionally with a key removed.
//
// Fetches its own copy of the fixture rather than taking one, and that is not fastidiousness: the
// first version deleted from a body shared across the whole omission loop, so each attempt sent a
// body already missing every key the attempts before it had removed. Against a conditionally
// required field that silently produced the wrong answer -- omitting the *trigger* field first
// removed the condition, so omitting the dependent field afterwards was accepted, and the probe
// concluded "not required" for a field that is required half the time.
func (p requiredByAPI) create(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	round int,
	omit string,
) (omission, error) {
	fixture, ok := sc.Fixture(round)
	if !ok {
		return omission{}, fmt.Errorf("%w: fixture %d does not exist", ErrInvalidPlan, round)
	}

	body := fixture.Body
	body[sc.Subject.NameField] = s.NameValue(p.Name(), round+1)

	if omit != "" {
		delete(body, omit)
	}

	resp, _, err := s.Create(ctx, p.Name(), body)
	if err != nil {
		// A missing identifier is not fatal here: the object exists, the prefix sweep will
		// remove it, and the status is what this probe is reading.
		if resp == nil || !errors.Is(err, ErrNoIdentifier) {
			return omission{}, err
		}
	}

	e := resp.Error()

	return omission{
		fixture:  fixture.Name,
		omitted:  omit,
		status:   resp.Status,
		named:    omit != "" && e.Names(omit),
		detail:   e.Detail,
		evidence: resp.Interaction,
	}, nil
}

// concludeRequired turns the omission attempts into facts.
func (p requiredByAPI) concludeRequired(sc Scope, attempts map[string][]omission, out *Result) {
	paths := make([]string, 0, len(attempts))
	for path := range attempts {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		rounds := attempts[path]

		accepted, refused := 0, 0
		for _, a := range rounds {
			if a.status < 400 {
				accepted++
			} else {
				refused++
			}
		}

		// Disagreement between fixtures is the conditional-requirement signature, and it is the
		// case hand-maintained fixup tables in existing providers are full of: a port field that
		// matters only when a protocol field says tcp. One-field-at-a-time omission from a single
		// fixture reports half a truth either way, so this is a note and never a fact.
		if accepted > 0 && refused > 0 {
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
				Message: fmt.Sprintf("omitting this field was accepted by %d fixture(s) and "+
					"refused by %d, so its requiredness is conditional on something else in the "+
					"body and no fact was recorded", accepted, refused),
			})

			continue
		}

		out.Facts = append(out.Facts, p.requiredFact(sc, path, rounds, accepted > 0))
	}
}

// requiredFact records whether the API enforced a field's presence.
//
// The confidence is asymmetric for a reason that is easy to miss. A 2xx on omission is
// unambiguous: the API accepted a body without the field, so it is not required. A 4xx is not --
// the request may have failed for an unrelated reason -- so it is Observed only when the error
// body *names* the field, and Inferred otherwise.
func (p requiredByAPI) requiredFact(
	sc Scope,
	path string,
	rounds []omission,
	accepted bool,
) Fact {
	evidence := make([]string, 0, len(rounds))
	for _, a := range rounds {
		if a.evidence != "" {
			evidence = appendUnique(evidence, a.evidence)
		}
	}

	if accepted {
		confidence := Observed
		if len(rounds) > 1 {
			confidence = Corroborated
		}

		return Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   path,
			Field:      FactRequiredByAPI,
			Value:      BoolValue(false),
			Confidence: confidence,
			Probe:      p.Name(),
			Evidence:   evidence,
			Rationale: fmt.Sprintf(
				"a create omitting this field succeeded in %d fixture(s)", len(rounds)),
		}
	}

	named := false
	for _, a := range rounds {
		if a.named {
			named = true
			break
		}
	}

	confidence := Inferred
	rationale := fmt.Sprintf("a create omitting this field was refused with %d, but the error "+
		"body did not name the field, so the refusal may have had another cause",
		rounds[0].status)

	if named {
		confidence = Observed
		if len(rounds) > 1 {
			confidence = Corroborated
		}
		rationale = fmt.Sprintf("a create omitting this field was refused with %d and the error "+
			"named the field", rounds[0].status)
	}

	return Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   path,
		Field:      FactRequiredByAPI,
		Value:      BoolValue(true),
		Confidence: confidence,
		Probe:      p.Name(),
		Evidence:   evidence,
		Rationale:  rationale,
		Alternatives: alternativesUnless(named,
			"the create may have been refused for a reason unrelated to this field, which only "+
				"an error body naming it would rule out"),
	}
}

// noteNameField records the one field this protocol structurally cannot test.
//
// Omitting the name field is not an experiment available to this tool: an object created without
// the stamped prefix could not be found by the sweeper, so the session refuses the body before it
// is sent. Silence would leave the field looking probed and unremarkable.
func (p requiredByAPI) noteNameField(sc Scope, out *Result) {
	if sc.Subject.NameField == "" {
		return
	}

	out.Notes = append(out.Notes, Note{
		Resource: sc.Subject.Resource, JSONPath: sc.Subject.NameField, Probe: p.Name(),
		Message: "this field carries the sweeper's name prefix, so a create omitting it would " +
			"produce an object that could not be found again and is refused before it is sent; " +
			"its requiredness is therefore unprobed",
	})
}

// serverDefault implements the contract on the type in catalogue.go.
func (p serverDefault) Exercise(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
) (Result, error) {
	var out Result

	if len(sc.Fixtures()) == 0 || len(sc.Omitted()) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "no fixture leaves a sendable field unset, so there is no omitted field " +
				"whose default could be observed",
		})

		return out, nil
	}

	// Two byte-identical creates, then a third from a second fixture. The first pair rules out a
	// value that varies on its own -- a counter, a timestamp, a random assignment -- and the
	// third rules out one derived from the request.
	steps := []struct {
		label   string
		fixture int
	}{
		{"first", 0},
		{"second, byte-identical to the first", 0},
	}

	if len(sc.Fixtures()) > 1 {
		steps = append(steps, struct {
			label   string
			fixture int
		}{"third, from a different fixture", 1})
	}

	reads := make([]*Response, 0, len(steps))

	for i, step := range steps {
		fixture, _ := sc.Fixture(step.fixture)

		body := fixture.Body
		// The same stamped name for every step would collide on an API that enforces
		// uniqueness, and a 409 would then look like a fact about defaults.
		body[sc.Subject.NameField] = s.NameValue(p.Name(), i+1)

		resp, id, err := s.Create(ctx, p.Name(), body)
		out.Requests++

		if err != nil {
			return out, err
		}

		if resp.Status >= 400 {
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, Probe: p.Name(),
				Message: fmt.Sprintf("the %s create was refused with %d (%s), so no default was "+
					"observed", step.label, resp.Status, resp.Error().Detail),
			})

			return out, nil
		}

		read, err := s.ReadCreated(ctx, p.Name(), id, expansionQuery(sc))
		out.Requests++

		if err != nil {
			return out, err
		}

		reads = append(reads, read)
	}

	p.concludeDefaults(sc, s.Findings(), reads, &out)

	return out, nil
}

// concludeDefaults reads the three responses and decides, per omitted field, which of Terraform's
// three outcomes applies.
func (p serverDefault) concludeDefaults(
	sc Scope,
	findings *Findings,
	reads []*Response,
	out *Result,
) {
	for _, f := range sc.Omitted() {
		values := make([]any, 0, len(reads))
		outcomes := make([]FieldOutcome, 0, len(reads))
		evidence := make([]string, 0, len(reads))

		for _, r := range reads {
			v, outcome := r.LookupField(f.JSONPath)
			values = append(values, v)
			outcomes = append(outcomes, outcome)
			evidence = appendUnique(evidence, r.Interaction)
		}

		switch {
		case anyOutcome(outcomes, Ambiguous):
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
				Message: "the read crossed a collection holding more than one element, so no " +
					"default could be attributed to this path",
			})

		case !anyOutcome(outcomes, Present):
			// Omitted and still absent: there is nothing to default to. Not a fact -- the API
			// may simply not return the field, which write.writable-returned reports.
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
				Message: "the field was omitted and did not appear in the read, so the API " +
					"assigns no value a generated default could carry",
			})

		default:
			p.classify(sc, findings, f, values, outcomes, evidence, out)
		}
	}
}

// classify decides between a constant default, a derived one, and a value that is not a default at
// all because the field was never settable.
func (p serverDefault) classify(
	sc Scope,
	findings *Findings,
	f Field,
	values []any,
	outcomes []FieldOutcome,
	evidence []string,
	out *Result,
) {
	// The dependency, satisfied by asking about a *fact* rather than about a probe: a field the
	// API does not store is plain Computed, and the value it reports when omitted is not a
	// default a practitioner could override. Writing it as a static Default would produce a
	// provider that plans a change it cannot apply.
	//
	// Two facts satisfy it, and the second matters more than it looks. Writable=false is the
	// direct statement. ReturnedOnRead=false is the composite one: an earlier probe *sent* a
	// value for this field and never saw it come back, and this probe *omitted* it and did see a
	// value -- so the API assigns the field and ignores what it is told, which is the same
	// conclusion by a different route. Consulting only Writable would miss it entirely, because a
	// field that never echoes anything back leaves writability unobservable and produces no
	// Writable fact at all.
	if why, settled := notADefault(findings, f.JSONPath); settled {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: "the field carries a value when omitted, but " + why + " -- so this is a " +
				"computed value rather than a default, and a static default would make the " +
				"provider plan a change it cannot apply",
		})

		return
	}

	stable := len(values) >= 2 && sameValue(values[0], values[1]) &&
		outcomes[0] == Present && outcomes[1] == Present

	if !stable {
		out.Facts = append(out.Facts, Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   f.JSONPath,
			Field:      FactDefaultIsDerived,
			Value:      BoolValue(true),
			Confidence: Observed,
			Probe:      p.Name(),
			Evidence:   evidence,
			Rationale: "two byte-identical creates produced different values for this omitted " +
				"field, so whatever assigns it is not a constant",
			Alternatives: []string{
				"a counter, a timestamp and a random assignment are all consistent with this " +
					"observation, and none of them is distinguishable from the others here",
			},
		})

		return
	}

	// Stable across identical creates. If a third create from a different fixture disagrees, the
	// value is derived from the request -- which is the false positive this protocol exists to
	// catch, because writing it as a static default would be a permanent lie.
	if len(values) > 2 && !sameValue(values[0], values[2]) {
		out.Facts = append(out.Facts, Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   f.JSONPath,
			Field:      FactDefaultIsDerived,
			Value:      BoolValue(true),
			Confidence: Corroborated,
			Probe:      p.Name(),
			Evidence:   evidence,
			Rationale: "the value was identical across two byte-identical creates and different " +
				"for a create built from another fixture, so it is derived from the request",
		})

		return
	}

	confidence := Observed
	if len(values) > 2 {
		confidence = Corroborated
	}

	out.Facts = append(out.Facts, Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   f.JSONPath,
		Field:      FactServerDefault,
		Value:      literalOf(values[0]),
		Confidence: confidence,
		Probe:      p.Name(),
		Evidence:   evidence,
		Rationale: fmt.Sprintf(
			"the field was omitted from %d create(s) and came back as %v every time",
			len(values), values[0]),
		Alternatives: []string{
			"the value may be derived from tenant configuration rather than being a constant, " +
				"which no number of creates in one tenant can rule out",
			"the value may be derived from a field this plan's fixtures did not vary",
		},
	})
}

// notADefault reports whether an earlier fact rules this value out as a practitioner-settable
// default, and says which fact did it.
func notADefault(findings *Findings, jsonPath string) (string, bool) {
	if fact, settled := findings.Settled(jsonPath, FactWritable, Observed); settled {
		if fact.Value.Bool != nil && !*fact.Value.Bool {
			return fact.Probe + " established that the API does not store what is sent for it", true
		}
	}

	if fact, settled := findings.Settled(jsonPath, FactReturnedOnRead, Observed); settled {
		if fact.Value.Bool != nil && !*fact.Value.Bool {
			return fact.Probe + " sent a value for it and never saw one come back, so the API " +
				"assigns this field rather than accepting it", true
		}
	}

	return "", false
}

// literalOf renders an observed value as a blueprint literal.
//
// A literal rather than text, because merge writes this into a generated Default and the
// difference between the string "3" and the number 3 decides whether the emitted code compiles.
func literalOf(v any) Value {
	switch typed := v.(type) {
	case bool:
		return LiteralValue(blueprint.Literal{Raw: fmt.Sprintf("%t", typed)})
	case float64:
		return LiteralValue(blueprint.Literal{Raw: trimFloat(typed)})
	case string:
		return LiteralValue(blueprint.Literal{Raw: fmt.Sprintf("%q", typed)})
	default:
		// A collection or an object. Recorded as text so the observation survives for a human,
		// and merge refuses to build a default out of it.
		return TextValue(fmt.Sprint(v))
	}
}

func anyOutcome(outcomes []FieldOutcome, want FieldOutcome) bool {
	for _, o := range outcomes {
		if o == want {
			return true
		}
	}

	return false
}

// sameValue compares two observed values structurally.
func sameValue(a, b any) bool { return reflect.DeepEqual(a, b) }

// immutability implements the contract on the type in catalogue.go.
func (p immutability) Exercise(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
) (Result, error) {
	var out Result

	if sc.Subject.Update == nil {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "the resource has no update operation, so immutability is not a question " +
				"about it: write.update-style records replaceOnly instead",
		})

		return out, nil
	}

	fields := sc.Immutable()
	if len(fields) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "no field carries two or more candidate values, so no field can be probed " +
				"for immutability: the fact requires two distinct values both refused, and one " +
				"candidate cannot supply that",
		})

		return out, nil
	}

	for _, f := range fields {
		if err := p.probeField(ctx, s, sc, f, &out); err != nil {
			// Returned with whatever earlier fields established.
			return out, err
		}
	}

	return out, nil
}

// probeField runs the whole protocol for one field.
//
// Every step exists to eliminate one alternative explanation for a 4xx on update, and the order
// matters: the control comes before the real attempt, because a control that fails means the
// update request shape itself is wrong and every later conclusion would be an artefact of that.
func (p immutability) probeField(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	f Field,
	out *Result,
) error {
	candidates, why := p.candidatesFor(s, sc, f)
	if len(candidates) < 2 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: why,
		})

		return nil
	}

	if why != "" {
		// The candidates were substituted rather than taken from the plan, which a reader needs
		// to know: the transcript will not match what the plan declares.
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: why,
		})
	}

	original := p.originalValue(s, sc, f)

	// Step 1: the object under test.
	subject, err := p.createWith(ctx, s, sc, f, original, 1)
	out.Requests++

	if err != nil {
		return err
	}
	if subject.status >= 400 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: fmt.Sprintf("the object under test could not be created (%d: %s), so "+
				"nothing was observed about this field", subject.status, subject.detail),
		})

		return nil
	}

	// Step 2: the value has to be observable, or "refused" and "never stored" are the same
	// observation.
	before, err := s.ReadCreated(ctx, p.Name(), subject.id, expansionQuery(sc))
	out.Requests++

	if err != nil {
		return err
	}

	stored, outcome := before.LookupField(f.JSONPath)
	if outcome != Present {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: "the field did not come back on the read after create, so a refused update " +
				"could not be told from a field the API never stored, and immutability was not " +
				"probed",
		})

		return nil
	}

	// Step 3: the control. Send the value back unchanged. This is the step that separates "this
	// field cannot be changed" from "this update request is malformed", and it is the reason the
	// quirk server has a RequiresExtraFieldOnUpdate switch at all.
	control, err := s.Update(ctx, p.Name(), subject.id, p.updateBody(s, sc, f, stored))
	out.Requests++

	if err != nil {
		return err
	}

	if control.Status >= 400 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: fmt.Sprintf("the control update -- the same value sent back unchanged -- was "+
				"refused with %d (%s), so this update request shape is wrong and any refusal of a "+
				"changed value would say nothing about immutability",
				control.Status, control.Error().Detail),
		})

		return nil
	}

	// Step 4: prove the new value is acceptable to the API at all, by creating a second object
	// with it. Without this, "the field is immutable" and "that value is invalid" are the same
	// observation.
	proof, err := p.createWith(ctx, s, sc, f, candidates[0], 2)
	out.Requests++

	if err != nil {
		return err
	}

	if proof.status >= 400 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: fmt.Sprintf("the candidate value %v was refused on create (%d: %s), so it "+
				"is not an acceptable value and a refused update would prove nothing about "+
				"immutability", candidates[0], proof.status, proof.detail),
		})

		return nil
	}

	p.attempt(ctx, s, sc, f, subject.id, stored, candidates, out)

	return nil
}

// attempt runs the two update attempts and records the conclusion.
func (p immutability) attempt(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	f Field,
	id string,
	stored any,
	candidates []any,
	out *Result,
) {
	first, firstRead, err := p.tryUpdate(ctx, s, sc, f, id, candidates[0], out)
	if err != nil {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: fmt.Sprintf("the update attempt failed: %v", err),
		})

		return
	}

	// Accepted and applied: the field is mutable, and one demonstration is enough. Immutable=false
	// is the safe direction -- it recommends nothing.
	if first < 400 {
		got, outcome := firstRead.LookupField(f.JSONPath)

		if outcome == Present && sameValue(got, candidates[0]) {
			out.Facts = append(out.Facts, Fact{
				Resource:   sc.Subject.Resource,
				JSONPath:   f.JSONPath,
				Field:      FactImmutable,
				Value:      BoolValue(false),
				Confidence: Observed,
				Probe:      p.Name(),
				Evidence:   []string{firstRead.Interaction},
				Rationale: fmt.Sprintf("an update changed this field from %v to %v and the read "+
					"confirmed it", stored, candidates[0]),
			})

			return
		}

		// Accepted, and the value did not change. Not immutability -- a different fact that
		// happens to want similar handling, and conflating the two is the classic error.
		out.Facts = append(out.Facts, Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   f.JSONPath,
			Field:      FactSilentlyIgnoredOnUpdate,
			Value:      BoolValue(true),
			Confidence: Observed,
			Probe:      p.Name(),
			Evidence:   []string{firstRead.Interaction},
			Rationale: fmt.Sprintf("an update to %v answered %d and the field still read back "+
				"as %v, so the change was accepted and not applied",
				candidates[0], first, got),
			Alternatives: []string{
				"the value may have been normalised rather than ignored, which " +
					"write.normalisation would distinguish",
			},
		})

		return
	}

	// Refused. One refusal is not enough: the value may simply have been invalid in a way its
	// acceptance on *create* did not reveal -- a uniqueness constraint, a state-dependent rule.
	// A second, distinct value rules that out, which is why Immutable=true requires two.
	second, secondRead, err := p.tryUpdate(ctx, s, sc, f, id, candidates[1], out)
	if err != nil {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: fmt.Sprintf("the second update attempt failed: %v", err),
		})

		return
	}

	if second < 400 {
		// The first value was the problem, not the field. Reported as mutable, and the note says
		// which value the API would not take -- worth knowing on its own.
		out.Facts = append(out.Facts, Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   f.JSONPath,
			Field:      FactImmutable,
			Value:      BoolValue(false),
			Confidence: Observed,
			Probe:      p.Name(),
			Evidence:   []string{secondRead.Interaction},
			Rationale: fmt.Sprintf("an update to %v was refused but an update to %v succeeded, "+
				"so the field can be changed and the first value was the problem",
				candidates[0], candidates[1]),
		})

		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: fmt.Sprintf("the value %v was accepted on create and refused on update, "+
				"which is worth a look: it may be constrained by something other than "+
				"immutability", candidates[0]),
		})

		return
	}

	out.Facts = append(out.Facts, Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   f.JSONPath,
		Field:      FactImmutable,
		Value:      BoolValue(true),
		Confidence: Corroborated,
		Probe:      p.Name(),
		Evidence:   []string{firstRead.Interaction, secondRead.Interaction},
		Rationale: fmt.Sprintf("two distinct values were refused on update (%d and %d) after a "+
			"control update of the unchanged value succeeded, and the first value was proven "+
			"acceptable on create", first, second),
		Alternatives: []string{
			"the field may be immutable only after some state transition this object never made",
			"both refusals may share a cause other than immutability that the control did not " +
				"reach",
		},
	})

	out.Notes = append(out.Notes, Note{
		Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
		Message: "this field appears immutable. Whether Terraform should destroy and recreate " +
			"the resource for it is a decision about somebody's infrastructure, so no plan " +
			"modifier is recommended here and merge will not add one",
	})
}

// tryUpdate sends one changed value and reads the result back.
func (p immutability) tryUpdate(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	f Field,
	id string,
	value any,
	out *Result,
) (int, *Response, error) {
	resp, err := s.Update(ctx, p.Name(), id, p.updateBody(s, sc, f, value))
	out.Requests++

	if err != nil {
		return 0, nil, err
	}

	read, err := s.ReadCreated(ctx, p.Name(), id, expansionQuery(sc))
	out.Requests++

	if err != nil {
		return 0, nil, err
	}

	return resp.Status, read, nil
}

// updateBody is the whole fixture with one field changed.
//
// The whole object rather than just the changed field, because an API with putFull semantics
// clears whatever the request omits -- and the field it must never clear is the name. An update
// that dropped or unprefixed the stamped name would leave an object the prefix sweep cannot find,
// so a crash between that update and the delete would strand it permanently.
//
// The name is therefore stamped first and the field under test applied last, so a probe of the
// name field itself still ends up with a prefixed value: candidatesFor guarantees its candidates
// are stamped.
func (p immutability) updateBody(
	s *MutatingSession,
	sc Scope,
	f Field,
	value any,
) map[string]any {
	fixture, _ := sc.Fixture(0)

	body := fixture.Body
	body[sc.Subject.NameField] = s.NameValue(p.Name(), 1)
	body[f.JSONPath] = value

	return body
}

// createWith creates an object carrying one particular value for one field.
func (p immutability) createWith(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	f Field,
	value any,
	seq int,
) (created, error) {
	fixture, _ := sc.Fixture(0)

	// Name first, field under test last, for the reason given on updateBody: whichever field is
	// being probed, the object leaves here findable by prefix.
	body := fixture.Body
	body[sc.Subject.NameField] = s.NameValue(p.Name(), seq)
	body[f.JSONPath] = value

	resp, id, err := s.Create(ctx, p.Name(), body)
	if err != nil && !errors.Is(err, ErrNoIdentifier) {
		return created{}, err
	}

	return created{id: id, status: resp.Status, detail: resp.Error().Detail}, nil
}

// created is one create's outcome.
type created struct {
	id     string
	status int
	detail string
}

// originalValue is the value the object under test starts with.
//
// Stamped for the name field, whatever the fixture says. A fixture's name is a placeholder -- the
// stamp is applied at send time -- so taking it literally here would build a create body whose
// name lacks the prefix, and the session would refuse it before it was sent. The same reason
// candidatesFor substitutes stamped names.
func (p immutability) originalValue(s *MutatingSession, sc Scope, f Field) any {
	if f.JSONPath == sc.Subject.NameField {
		return s.NameValue(p.Name(), 1)
	}

	fixture, _ := sc.Fixture(0)

	if v, ok := fixture.Body[f.JSONPath]; ok {
		return v
	}

	return sentinelFor(f, 1)
}

// candidatesFor returns the two distinct values this protocol will try, and an explanation when
// they are not the plan's own.
//
// The name field is the exception, and it has to be: an update that replaced the stamped name with
// a plan-declared candidate would leave an object the prefix sweep cannot find, so a crash between
// that update and the delete would strand it permanently. Stamped names are distinct values, which
// is all the protocol needs, and they keep the object sweepable throughout.
func (p immutability) candidatesFor(s *MutatingSession, sc Scope, f Field) ([]any, string) {
	if f.JSONPath == sc.Subject.NameField {
		return []any{
				s.NameValue(p.Name()+"-alt", 1),
				s.NameValue(p.Name()+"-alt", 2),
			}, "this field carries the sweeper's name prefix, so the plan's candidate values were " +
				"replaced with two stamped names: an update to an unprefixed value would leave an " +
				"object the prefix sweep could not find"
	}

	declared := sc.Candidates(f.JSONPath)
	if len(declared) < 2 {
		return nil, "fewer than two candidate values are declared for this field, and the " +
			"immutability fact requires two distinct values both refused"
	}

	return declared, ""
}

// enumBoundary implements the contract on the type in catalogue.go.
func (p enumBoundary) Exercise(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
) (Result, error) {
	var out Result

	fields := sc.Enums()
	if len(fields) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "no sendable field carries documented enum values, so there is no " +
				"specification claim to check against the API",
		})

		return out, nil
	}

	for _, f := range fields {
		// The name field carries the sweeper's prefix, so its value is not free to be an enum
		// member. A create sending one would be refused before it was sent.
		if f.JSONPath == sc.Subject.NameField {
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
				Message: "this field carries the sweeper's name prefix, so its value cannot be " +
					"set to an enum member and its documented set is unprobed",
			})

			continue
		}

		if err := p.probeEnum(ctx, s, sc, f, &out); err != nil {
			return out, err
		}
	}

	return out, nil
}

// probeEnum sends every documented value and two generated negatives for one field.
func (p enumBoundary) probeEnum(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	f Field,
	out *Result,
) error {
	var (
		accepted         []string
		rejected         []string
		negatives        []string
		refusedNegatives int
		evidence         []string
	)

	documented := map[string]bool{}
	for _, v := range f.Enum {
		documented[v] = true
	}

	for i, candidate := range EnumCandidates(f) {
		resp, err := p.send(ctx, s, sc, f, candidate, i+1)
		out.Requests++

		if err != nil {
			return err
		}

		evidence = appendUnique(evidence, resp.Interaction)

		switch {
		case documented[candidate] && resp.Status < 400:
			accepted = append(accepted, candidate)
		case documented[candidate]:
			rejected = append(rejected, candidate)
		default:
			negatives = append(negatives, candidate)
			if resp.Status >= 400 {
				refusedNegatives++
			}
		}
	}

	p.concludeEnum(sc, f, enumOutcome{
		accepted:         accepted,
		rejected:         rejected,
		negatives:        negatives,
		refusedNegatives: refusedNegatives,
		evidence:         evidence,
	}, out)

	if len(accepted) > 0 {
		return p.probeCase(ctx, s, sc, f, accepted[0], out)
	}

	return nil
}

// enumOutcome is what the whole candidate sweep saw for one field.
type enumOutcome struct {
	accepted  []string
	rejected  []string
	negatives []string
	// refusedNegatives is how many of the generated values outside the documented set were
	// refused. The set is closed only when every one of them was.
	refusedNegatives int
	evidence         []string
}

// send issues one create carrying one enum candidate.
func (p enumBoundary) send(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	f Field,
	candidate string,
	seq int,
) (*Response, error) {
	fixture, _ := sc.Fixture(0)

	body := fixture.Body
	body[sc.Subject.NameField] = s.NameValue(p.Name(), seq)
	body[f.JSONPath] = candidate

	resp, _, err := s.Create(ctx, p.Name(), body)
	if err != nil && !errors.Is(err, ErrNoIdentifier) {
		return nil, err
	}

	return resp, nil
}

// concludeEnum records what the sweep established.
func (p enumBoundary) concludeEnum(sc Scope, f Field, o enumOutcome, out *Result) {
	if len(o.accepted) > 0 {
		out.Facts = append(out.Facts, Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   f.JSONPath,
			Field:      FactEnumAccepted,
			Value:      ListValue(o.accepted),
			Confidence: Observed,
			Probe:      p.Name(),
			Evidence:   o.evidence,
			Rationale: fmt.Sprintf("%d of the %d documented value(s) were accepted on create",
				len(o.accepted), len(f.Enum)),
		})
	}

	// The valuable result: the specification is stale, and a spec-derived validator would have
	// been actively harmful.
	if len(o.rejected) > 0 {
		out.Facts = append(out.Facts, Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   f.JSONPath,
			Field:      FactEnumRejectedDocumented,
			Value:      ListValue(o.rejected),
			Confidence: Observed,
			Probe:      p.Name(),
			Evidence:   o.evidence,
			Rationale: fmt.Sprintf("the specification documents %s but this API refused %s",
				strings.Join(f.Enum, ", "), strings.Join(o.rejected, ", ")),
			Alternatives: []string{
				"a value this tenant refuses may be licence-gated or plan-gated rather than " +
					"nonexistent, so this says rejected here and not does not exist",
			},
		})
	}

	// Closed only when every generated negative was refused. One refusal is consistent with the
	// value having failed some other check, which is the whole reason two are sent.
	closed := len(o.negatives) >= negativeEnumCandidates &&
		o.refusedNegatives == len(o.negatives)

	rationale := fmt.Sprintf("%d value(s) outside the documented set were sent and all %d were "+
		"refused", len(o.negatives), o.refusedNegatives)

	if !closed {
		rationale = fmt.Sprintf("%d of %d value(s) outside the documented set were accepted, so "+
			"the API takes values the specification does not list",
			len(o.negatives)-o.refusedNegatives, len(o.negatives))
	}

	if len(o.negatives) < negativeEnumCandidates {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: fmt.Sprintf("only %d value(s) outside the documented set could be generated, "+
				"and %d are required before the set can be called closed",
				len(o.negatives), negativeEnumCandidates),
		})

		return
	}

	out.Facts = append(out.Facts, Fact{
		Resource: sc.Subject.Resource,
		JSONPath: f.JSONPath,
		Field:    FactEnumClosed,
		Value:    BoolValue(closed),
		// Observed either way: both answers rest on what the API did with values this probe
		// chose, and neither needs a second fixture to be believable.
		Confidence: Observed,
		Probe:      p.Name(),
		Evidence:   o.evidence,
		Rationale:  rationale,
		Alternatives: alternativesUnless(!closed,
			"a generated value may have been refused by a length or character-set check rather "+
				"than by enum membership, which is why the candidates are shaped like the "+
				"documented values"),
	})

	// Whatever the answer, no validator. An over-tight one rejects configurations the API would
	// have accepted and the practitioner cannot work around it.
	out.Notes = append(out.Notes, Note{
		Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
		Message: "the accepted set is recorded for the attribute's description only; no " +
			"validator is generated from it, because a routine upstream addition to the set " +
			"would then become a plan failure the practitioner cannot work around",
	})
}

// probeCase asks whether the API treats the documented values case-sensitively.
//
// The third question the contract asks, and it has no fact field of its own: recording it as one
// would need merge to act on it, and the only sound action -- describing the behaviour -- is what a
// note already does. Worth asking anyway, because an API that accepts TEST for a field documented
// as test is a second, independent reason a generated validator would be wrong.
func (p enumBoundary) probeCase(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	f Field,
	accepted string,
	out *Result,
) error {
	variant := strings.ToUpper(accepted)
	if variant == accepted {
		// Nothing to vary: the value has no letters, or is already upper case.
		return nil
	}

	resp, err := p.send(ctx, s, sc, f, variant, len(f.Enum)+negativeEnumCandidates+1)
	out.Requests++

	if err != nil {
		return err
	}

	message := fmt.Sprintf("the documented value %q was accepted and %q was refused with %d, so "+
		"the API treats this set case-sensitively", accepted, variant, resp.Status)

	if resp.Status < 400 {
		message = fmt.Sprintf("the API accepted %q for a value the specification documents as "+
			"%q, so the documented set is not the whole of what it takes and a validator built "+
			"from it would reject configurations the API allows", variant, accepted)
	}

	out.Notes = append(out.Notes, Note{
		Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
		Message: message,
	})

	return nil
}
