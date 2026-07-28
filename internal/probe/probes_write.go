package probe

import (
	"context"
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
