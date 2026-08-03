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
func sentinelFor(sc Scope, f Field, round int) any {
	// The plan's candidates first, because they are the only source of a value the *API* will
	// accept for a field whose constraint its specification does not describe. The pilot's tag has
	// one: icon rejects anything but a value from an undocumented set, and a synthesised sentinel
	// got every fixture round refused -- losing the observation for every other field in the body,
	// not just for icon.
	if declared := sc.Candidates(f.JSONPath); len(declared) > 0 {
		return declared[(round-1)%len(declared)]
	}

	// A field the specification documents a value set for cannot take an arbitrary value, and
	// sending one gets the whole body refused -- which is not a fact about that field, it is the
	// loss of every observation the request would have made about every other field.
	//
	// A live run made this concrete: write.writable-returned sent "probe-accessType-1" into an enum
	// field, both fixture rounds came back 400, and the probe observed nothing whatsoever about
	// writability for any field. Rounds take different documented values where the set has more
	// than one, which preserves the two-distinct-values property the writability conclusion needs.
	//
	// The pool prefers what this API was *observed* to accept and never contains what it was
	// observed to refuse -- the same asymmetry a generated fixture uses, for the same reason. A
	// second live run made THAT concrete: rotating accessType into the documented "system", which
	// the committed evidence already recorded as refused, cost two of three fixture rounds and
	// with them every observation those bodies would have made.
	if pool := safeEnumPool(f); len(pool) > 0 {
		return pool[(round-1)%len(pool)]
	}

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

// safeEnumPool is the documented values worth sending: observed-accepted first, then the
// documented remainder minus anything observed refused. Empty when the field documents no
// set at all.
func safeEnumPool(f Field) []string {
	if len(f.AllowedValues) == 0 {
		return nil
	}

	rejected := map[string]bool{}
	for _, v := range f.Behaviour.RejectedValues {
		rejected[v] = true
	}
	seen := map[string]bool{}

	pool := make([]string, 0, len(f.AllowedValues))
	for _, v := range f.Behaviour.AcceptedValues {
		if !rejected[v] && !seen[v] {
			pool = append(pool, v)
			seen[v] = true
		}
	}
	for _, v := range f.AllowedValues {
		if !rejected[v] && !seen[v] {
			pool = append(pool, v)
			seen[v] = true
		}
	}

	return pool
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
	// gates is what the declared influencer fields held in the body this round actually
	// sent -- recorded, never reconstructed, so a conclusion about a branch cites the
	// state that was really measured.
	gates []Condition
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

		// Read off the body actually sent, after bodyFor's substitutions, so a branch
		// conclusion cites the state that was really measured.
		gates := gateConditions(sc, body)

		for path, value := range sent {
			field, _ := sc.Subject.Field(path)
			o := observe(fixture.Name, field, value, bare, expanded)
			o.gates = gates
			seen[path] = append(seen[path], o)
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

	influencer := map[string]bool{}
	for _, f := range sc.Influencers() {
		influencer[f.JSONPath] = true
	}

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
		case influencer[f.JSONPath]:
			// A declared gate keeps the value its fixture paired the other fields with.
			// Rotating it -- which the AllowedValues branch of sentinelFor would do --
			// manufactures cross-branch bodies the operator never declared: the
			// endpoint-agent fixture's filters make sense *because* its objectType is
			// endpoint-agent, and a rotated gate would send them under a branch that
			// refuses them. A gate the fixture leaves unset still gets a sentinel, so
			// omitting one is an experiment the operator can run deliberately.
			if _, ok := body[f.JSONPath]; !ok {
				body[f.JSONPath] = sentinelFor(sc, f, round+1)
			}
		default:
			body[f.JSONPath] = sentinelFor(sc, f, round+1)
		}

		sent[f.JSONPath] = body[f.JSONPath]
	}

	// NestedAttributeObject paths the fixture set are still observed, they are simply not synthesised.
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
			out.Facts = append(out.Facts, scopeToMeasuredBranch(
				p.absentFact(sc, path, rounds), sc, rounds))

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

		case !allPresent(rounds):
			// Mixed presence: returned under some fixtures and not others. This used to fall
			// through to an unconditional returnedOnRead=true, which is the same half-truth
			// the unconditional false was -- matchType is returned for a dynamic tag and
			// discarded for a static one, and either answer stored unconditionally makes the
			// generated code wrong for one branch. Attributed to a declared gate when the
			// observations allow it; a note naming what to declare when they do not.
			p.concludeMixed(sc, path, rounds, out)

		default:
			out.Facts = append(out.Facts, scopeToMeasuredBranch(
				p.returnedFact(sc, path, rounds), sc, rounds))

			if fact, ok := p.writableFact(sc, path, rounds); ok {
				out.Facts = append(out.Facts, scopeToMeasuredBranch(fact, sc, rounds))
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

// concludeMixed turns a field's mixed presence -- returned under some fixtures, absent
// under others -- into per-branch facts when a declared gate separates them.
//
// The attribution semantics are attributeToGate's, shared with the requiredness probe: one
// clean separator or nothing. When a branch's rounds allow it, writability is decided
// within the branch too, because "did the server store what I sent" is as branch-scoped a
// question as "did it return the field".
func (p writableAndReturned) concludeMixed(
	sc Scope,
	path string,
	rounds []observation,
	out *Result,
) {
	outcomes := make([]gatedOutcome, len(rounds))
	for i, o := range rounds {
		outcomes[i] = gatedOutcome{gates: o.gates, pass: o.outcome == Present}
	}

	separator, branches, ok := attributeToGate(outcomes)
	if !ok {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
			Message: "the field was returned under some fixtures and not others, so its " +
				"presence is conditional on something else in the body. No declared gate " +
				"separated the two, so no fact was recorded -- declare the deciding field " +
				"in the plan's defaultInfluencers to settle it",
		})

		return
	}

	condition := func(br gateBranch) []Condition {
		return []Condition{{JSONPath: separator, Equals: br.value}}
	}

	for _, br := range branches {
		group := make([]observation, 0, len(br.indices))
		for _, i := range br.indices {
			group = append(group, rounds[i])
		}

		var fact Fact
		if br.pass {
			fact = p.returnedFact(sc, path, group)
		} else {
			fact = p.absentFact(sc, path, group)
		}
		fact.When = condition(br)
		fact.Rationale += ", " + fact.Because()
		out.Facts = append(out.Facts, fact)

		if !br.pass {
			continue
		}

		if wf, ok := p.writableFact(sc, path, group); ok {
			wf.When = condition(br)
			wf.Rationale += ", " + wf.Because()
			out.Facts = append(out.Facts, wf)
		} else {
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
				Message: fmt.Sprintf("the field was returned when %s is %q, but the rounds "+
					"under that branch cannot separate \"the server stored what was sent\" "+
					"from \"the server returned its own value\"; a second fixture on the "+
					"same branch would settle it", separator, br.value),
			})
		}
	}
}

// scopeToMeasuredBranch attaches the shared gate conditions to a fact whose rounds did
// not span every fixture, for the reason concludeRequired's uniform path does: a field
// only one branch carries was only measured there, and "returned, unconditionally" from
// the dynamic fixture alone is the same half-truth this phase set out to kill -- worn the
// other way round. A fact measured in every fixture keeps its unconditional reading.
func scopeToMeasuredBranch(fact Fact, sc Scope, rounds []observation) Fact {
	if len(rounds) >= len(sc.Fixtures()) {
		return fact
	}

	lists := make([][]Condition, len(rounds))
	for i, o := range rounds {
		lists[i] = o.gates
	}

	if common := commonConditions(lists); len(common) > 0 {
		fact.When = common
		fact.Rationale += ", " + fact.Because()
	}

	return fact
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
		// A note, not a fact. Nothing was sent, so there is no cassette interaction to
		// cite -- and fact validation rightly refuses an evidence-less claim, which is
		// exactly what this used to emit. The blueprint already says replaceOnly; a
		// probe restating it adds no knowledge, only an unverifiable line.
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "the blueprint records no update operation, so every writable attribute " +
				"needs replacement rather than an in-place change; nothing was probed",
		})

		return out, nil
	}

	if len(sc.Fixtures()) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "no fixture was supplied, so there is no valid body to update",
		})

		return out, nil
	}

	// The style is resource-level, so any one fixture that creates will do -- and a refused
	// baseline create used to lose the whole probe, which is how a live run once produced no
	// update-style observation at all. Fixtures are tried in order until one creates; the
	// switch is stated, because a fact measured under the dynamic branch's body should say so.
	var (
		victim Field
		id     string
		made   bool
		sawAny bool
	)

	for i := range sc.Fixtures() {
		fx, _ := sc.Fixture(i)

		// The omitted field. Anything sendable, top-level and not the name field -- the name
		// has to stay in every request, for a reason worth stating: on an API that clears
		// omitted fields, an update without the name would clear the stamped prefix, and the
		// sweeper would then be unable to find the object it had just orphaned.
		v, found := p.victim(sc, fx)
		if !found {
			continue
		}
		sawAny = true

		body := fx.Body
		body[sc.Subject.NameField] = s.NameValue(p.Name(), i+1)
		body[v.JSONPath] = sentinelFor(sc, v, 1)

		r, createdID, err := s.Create(ctx, p.Name(), body)
		out.Requests++

		if err != nil {
			return out, err
		}
		if r.Status >= 400 {
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, Probe: p.Name(),
				Message: fmt.Sprintf("fixture %s was refused with %d; trying the next",
					fx.Name, r.Status),
			})

			continue
		}

		if i > 0 {
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, Probe: p.Name(),
				Message: fmt.Sprintf("measured against fixture %s after earlier fixtures "+
					"were refused", fx.Name),
			})
		}

		victim, id = v, createdID
		made = true

		break
	}

	switch {
	case !sawAny:
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "no fixture sets a field that can safely be omitted from an update; the " +
				"name field must stay in every request or a cleared name would strand the object",
		})

		return out, nil
	case !made:
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "every fixture's create was refused, so nothing was observed about " +
				"update style",
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
		// Refused outright. That is not the merge-or-clear question this protocol set out to
		// answer, but it settles the question a generated provider actually has: an update must
		// carry the whole object. Inferred rather than Observed, because the refusal may be about
		// the particular fields omitted rather than about partiality itself -- and because
		// write.immutability's control update, which sends the whole body, is the thing that
		// demonstrates the difference.
		out.Facts = append(out.Facts, Fact{
			Resource:   sc.Subject.Resource,
			Field:      FactUpdateStyle,
			Value:      TextValue(string(blueprint.UpdatePutFull)),
			Confidence: Inferred,
			Probe:      p.Name(),
			Evidence:   []string{update.Interaction},
			Rationale: fmt.Sprintf("an update carrying only %s was refused with %d (%s), so a "+
				"generated update must send the whole object",
				sc.Subject.NameField, update.Status, update.Error().Detail),
			Alternatives: []string{
				"the refusal may be about the particular fields omitted rather than about " +
					"partiality, which only omitting a different subset would separate",
				"the API may still merge when given a body it accepts, which this refusal " +
					"leaves untested",
			},
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
	// Looked up under the read key -- an API may rename between accepting a value and
	// returning it -- while the fact below joins on the write field, because that is the
	// schema attribute the finding is about.
	got, outcome := after.LookupField(sc.Subject.SweepNameField())
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

	// One counter across the whole probe. Every create in a round shared a stamped name, so on an
	// API whose uniqueness key includes it, the baseline and each omission that left the rest of
	// the tuple intact collided -- and a 409 that does not name the omitted field is discarded,
	// costing the observation rather than corrupting it. Still worth not spending.
	seq := 0

	for round := range sc.Fixtures() {
		fixture, _ := sc.Fixture(round)

		// The baseline. Without it a rejection cannot be attributed to the omission: the fixture
		// itself might simply not be acceptable.
		seq++

		baseline, err := p.create(ctx, s, sc, round, seq, "")
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
			seq++

			attempt, err := p.create(ctx, s, sc, round, seq, key)
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

	// gates are the values the scope's candidate gate fields held for this attempt.
	//
	// Recorded per attempt rather than looked up later, because the correlation this feeds is
	// only sound if it reads the body that was actually sent. A fixture's body is copied and
	// mutated per attempt, so reconstructing it afterwards would be reconstructing a guess.
	gates []Condition
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
	round, seq int,
	omit string,
) (omission, error) {
	fixture, ok := sc.Fixture(round)
	if !ok {
		return omission{}, fmt.Errorf("%w: fixture %d does not exist", ErrInvalidPlan, round)
	}

	body := fixture.Body
	body[sc.Subject.NameField] = s.NameValue(p.Name(), seq)

	if omit != "" {
		delete(body, omit)
	}

	gates := gateConditions(sc, body)

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
		gates:    gates,
	}, nil
}

// gateConditions reads the candidate gate fields out of a body about to be sent.
//
// Candidate gates come from Scope.Influencers(), which the plan already declares as the fields
// "whose value might determine another field's default". That is the same relation as a gate, one
// consequence narrower -- so this reuses the declaration rather than adding a second way to say
// the same thing. Phase 7.2 widens it to gates with declared value sets.
//
// Only scalars. A gate whose value is an object or an array cannot be compared for equality in a
// condition, and pretending otherwise would produce a precondition nothing could evaluate.
func gateConditions(sc Scope, body map[string]any) []Condition {
	var out []Condition

	for _, f := range sc.Influencers() {
		v, ok := body[f.JSONPath]
		if !ok {
			continue
		}

		if text, scalar := scalarText(v); scalar {
			out = append(out, Condition{JSONPath: f.JSONPath, Equals: text})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].JSONPath < out[j].JSONPath })

	return out
}

// scalarText renders a JSON scalar as the text a condition compares against.
//
// Reports false for anything else, rather than formatting it: "%v" on a map produces something
// that looks like a value and matches nothing, which is worse than declining.
func scalarText(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return fmt.Sprintf("%t", t), true
	case float64:
		return trimFloat(t), true
	default:
		return "", false
	}
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
		// matters only when a protocol field says tcp.
		//
		// This used to be a note and never a fact, because "conditional on something else in the
		// body" was all a fact could say. Now a fact can carry a precondition, so the question
		// becomes whether the disagreement can be attributed to a specific gate value. If it can,
		// each branch is its own fact under its own condition. If it cannot, it stays a note --
		// "conditional on something" is still not a fact, and inventing a condition to make it one
		// would be worse than the note ever was.
		if accepted > 0 && refused > 0 {
			if facts := p.conditionalRequiredFacts(sc, path, rounds); len(facts) > 0 {
				out.Facts = append(out.Facts, facts...)

				continue
			}

			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
				Message: fmt.Sprintf("omitting this field was accepted by %d fixture(s) and "+
					"refused by %d, so its requiredness is conditional on something else in the "+
					"body. No candidate gate separated the two, so no fact was recorded -- "+
					"declare the deciding field in the plan to settle it", accepted, refused),
			})

			continue
		}

		fact := p.requiredFact(sc, path, rounds, accepted > 0)

		// A field only some fixtures declare was only measured under those fixtures'
		// branches, and a uniform answer from one branch is not an unconditional fact --
		// matchType's omission is refused by a dynamic tag and meaningless to a static
		// one, and recording "required, unconditionally" from the dynamic fixture alone
		// would put matchType into every generated minimal configuration, breaking the
		// static create it was derived to protect. The gates every attempt shared are
		// the honest scope; a field every fixture carried keeps its unconditional
		// reading, because the branches were measured and agreed.
		if len(rounds) < len(sc.Fixtures()) {
			if common := commonGates(rounds); len(common) > 0 {
				fact.When = common
				fact.Rationale += ", " + fact.Because()
			}
		}

		out.Facts = append(out.Facts, fact)
	}
}

// commonGates is the gate conditions every attempt held with the same value, sorted for a
// stable committed order. Empty when the attempts' gates disagree -- the field was
// measured across branches, so no single branch scopes it.
func commonGates(rounds []omission) []Condition {
	lists := make([][]Condition, len(rounds))
	for i, a := range rounds {
		lists[i] = a.gates
	}

	return commonConditions(lists)
}

// commonConditions intersects gate-condition lists by path and value.
func commonConditions(lists [][]Condition) []Condition {
	if len(lists) == 0 {
		return nil
	}

	common := map[string]string{}
	for _, c := range lists[0] {
		common[c.JSONPath] = c.Equals
	}

	for _, gates := range lists[1:] {
		held := map[string]string{}
		for _, c := range gates {
			held[c.JSONPath] = c.Equals
		}
		for path, value := range common {
			if held[path] != value {
				delete(common, path)
			}
		}
	}

	out := make([]Condition, 0, len(common))
	for path, value := range common {
		out = append(out, Condition{JSONPath: path, Equals: value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JSONPath < out[j].JSONPath })

	return out
}

// conditionalRequiredFacts attributes a requiredness disagreement to one gate field.
//
// Returns nothing when the disagreement cannot be attributed, which is the common case and the
// honest answer: the caller then keeps its note.
func (p requiredByAPI) conditionalRequiredFacts(
	sc Scope,
	path string,
	rounds []omission,
) []Fact {
	outcomes := make([]gatedOutcome, len(rounds))
	for i, a := range rounds {
		outcomes[i] = gatedOutcome{gates: a.gates, pass: a.status < 400}
	}

	separator, branches, ok := attributeToGate(outcomes)
	if !ok {
		return nil
	}

	facts := make([]Fact, 0, len(branches))

	for _, br := range branches {
		group := make([]omission, 0, len(br.indices))
		for _, i := range br.indices {
			group = append(group, rounds[i])
		}

		fact := p.requiredFact(sc, path, group, br.pass)
		fact.When = []Condition{{JSONPath: separator, Equals: br.value}}
		fact.Rationale += ", " + fact.Because()

		facts = append(facts, fact)
	}

	return facts
}

// gatedOutcome is one attempt seen through its gates: which gate values held while it ran,
// and which side of a pass/fail question it landed on.
//
// The question is the caller's -- "was the omission accepted", "was the field returned" --
// which is what lets one attribution semantics serve every probe that measures the same
// thing under more than one fixture.
type gatedOutcome struct {
	gates []Condition
	pass  bool
}

// gateBranch is one value of the separating gate, with the outcomes measured under it.
type gateBranch struct {
	value   string
	indices []int
	// pass is the branch's shared outcome; attribution guarantees homogeneity, because a
	// value appearing on both sides of the partition would have been refused as overlap.
	pass bool
}

// attributeToGate attributes a pass/fail disagreement to exactly one gate field, and groups
// the outcomes by that gate's value, in sorted value order.
//
// "Attributed" means one candidate gate separates the outcomes cleanly -- every passing
// outcome held a gate value that no failing outcome held, and vice versa. A gate that took
// the same value on both sides explains nothing, and a gate that varies without correlating
// is coincidence. Exactly one separating gate is required: two would mean the observations
// cannot distinguish which of them decides, and picking either would be a guess presented as
// evidence. Not ok is the common case and the honest answer.
func attributeToGate(rounds []gatedOutcome) (string, []gateBranch, bool) {
	separator, ok := separatingGate(rounds)
	if !ok {
		return "", nil, false
	}

	byValue := map[string]*gateBranch{}
	order := []string{}

	for i, o := range rounds {
		v, held := conditionValue(o.gates, separator)
		if !held {
			return "", nil, false
		}

		br, seen := byValue[v]
		if !seen {
			br = &gateBranch{value: v, pass: o.pass}
			byValue[v] = br
			order = append(order, v)
		}
		br.indices = append(br.indices, i)
	}

	sort.Strings(order)

	branches := make([]gateBranch, 0, len(order))
	for _, v := range order {
		branches = append(branches, *byValue[v])
	}

	return separator, branches, true
}

// separatingGate finds the one gate whose value partitions pass from fail.
func separatingGate(rounds []gatedOutcome) (string, bool) {
	paths := map[string]bool{}
	for _, a := range rounds {
		for _, c := range a.gates {
			paths[c.JSONPath] = true
		}
	}

	var found string

	for gate := range paths {
		onPass, onFail := map[string]bool{}, map[string]bool{}

		for _, a := range rounds {
			v, held := conditionValue(a.gates, gate)
			if !held {
				// A gate absent from one attempt cannot partition them.
				onPass, onFail = nil, nil

				break
			}

			if a.pass {
				onPass[v] = true
			} else {
				onFail[v] = true
			}
		}

		if len(onPass) == 0 || len(onFail) == 0 {
			continue
		}

		if overlaps(onPass, onFail) {
			continue
		}

		if found != "" {
			// Two gates both separate the outcomes, so the evidence cannot say which decides.
			return "", false
		}

		found = gate
	}

	return found, found != ""
}

// conditionValue reads one gate's value out of a recorded condition set.
func conditionValue(gates []Condition, path string) (string, bool) {
	for _, c := range gates {
		if c.JSONPath == path {
			return c.Equals, true
		}
	}

	return "", false
}

// overlaps reports whether two value sets share a member.
func overlaps(a, b map[string]bool) bool {
	for v := range a {
		if b[v] {
			return true
		}
	}

	return false
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

		case allNil(values):
			// Present and null. Not a default: a generated stringdefault.StaticString of nothing
			// is not a thing, and Optional+Computed with no default is what null means here.
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
				Message: "the field came back null when omitted, so the API assigns no value a " +
					"generated default could carry",
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

// allNil reports whether every observed value was null.
func allNil(values []any) bool {
	for _, v := range values {
		if v != nil {
			return false
		}
	}

	return len(values) > 0
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

	// One counter across every field, not one per field. A stamped name repeated for a second
	// field collides with the first field's object on any API whose uniqueness key includes the
	// name -- the pilot's is (key, value, objectType) -- and the 409 that follows is reported as
	// "the object under test could not be created", so the field goes unprobed for a reason that
	// has nothing to do with it. Two of three fields were lost that way on a live run.
	seq := 0

	for _, f := range fields {
		if err := p.probeField(ctx, s, sc, f, &seq, &out); err != nil {
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
	seq *int,
	out *Result,
) error {
	candidates, why := p.candidatesFor(s, sc, f, seq)
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

	*seq++
	original := p.originalValue(s, sc, f, *seq)

	// Step 1: the object under test.
	subject, err := p.createWith(ctx, s, sc, f, original, *seq)
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
	control, err := s.Update(ctx, p.Name(), subject.id, p.updateBody(s, sc, f, stored, *seq-1))
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
	*seq++

	proof, err := p.createWith(ctx, s, sc, f, candidates[0], *seq)
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

	p.attempt(ctx, s, sc, f, subject.id, stored, candidates, *seq-1, out)

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
	seq int,
	out *Result,
) {
	first, firstRead, err := p.tryUpdate(ctx, s, sc, f, id, candidates[0], seq, out)
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
	second, secondRead, err := p.tryUpdate(ctx, s, sc, f, id, candidates[1], seq, out)
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
	seq int,
	out *Result,
) (int, *Response, error) {
	resp, err := s.Update(ctx, p.Name(), id, p.updateBody(s, sc, f, value, seq))
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
	seq int,
) map[string]any {
	// The field's own host fixture, so a field only the dynamic branch declares is
	// updated inside a body that branch accepts.
	fixture, _ := sc.HostFixture(f.JSONPath)

	body := fixture.Body
	body[sc.Subject.NameField] = s.NameValue(p.Name(), seq)
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
	fixture, _ := sc.HostFixture(f.JSONPath)

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
func (p immutability) originalValue(s *MutatingSession, sc Scope, f Field, seq int) any {
	if f.JSONPath == sc.Subject.NameField {
		return s.NameValue(p.Name(), seq)
	}

	fixture, _ := sc.HostFixture(f.JSONPath)

	if v, ok := fixture.Body[f.JSONPath]; ok {
		return v
	}

	return sentinelFor(sc, f, 1)
}

// candidatesFor returns the two distinct values this protocol will try, and an explanation when
// they are not the plan's own.
//
// The name field is the exception, and it has to be: an update that replaced the stamped name with
// a plan-declared candidate would leave an object the prefix sweep cannot find, so a crash between
// that update and the delete would strand it permanently. Stamped names are distinct values, which
// is all the protocol needs, and they keep the object sweepable throughout.
func (p immutability) candidatesFor(
	s *MutatingSession,
	sc Scope,
	f Field,
	seqPtr *int,
) ([]any, string) {
	if f.JSONPath == sc.Subject.NameField {
		return []any{
				s.NameValue(p.Name()+"-alt", *seqPtr),
				s.NameValue(p.Name()+"-alt", *seqPtr+100),
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

	// One counter across every field, not one per field. Restarting it per field made the second
	// field's first candidate reuse the first field's first stamped name, and an API that enforces
	// uniqueness over (key, value, objectType) answered 409 -- which the probe then had to discard
	// as unattributable. The names have to be unique across the whole probe, not within a field.
	seq := 0

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

		if err := p.probeEnum(ctx, s, sc, f, &seq, &out); err != nil {
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
	seq *int,
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
	for _, v := range f.AllowedValues {
		documented[v] = true
	}

	// Conditional acceptances, keyed by canonical condition: a candidate one branch takes
	// and another refuses is a fact per branch, exactly as a conditional requirement is.
	branchWhen := map[string][]Condition{}
	branchAccepted := map[string][]string{}
	branchRejected := map[string][]string{}

	for _, candidate := range EnumCandidates(f) {
		*seq++

		resp, hostGates, hostName, err := p.send(ctx, s, sc, f, candidate, *seq)
		out.Requests++

		if err != nil {
			return err
		}

		evidence = appendUnique(evidence, resp.Interaction)

		if resp.Status < 400 {
			if documented[candidate] {
				accepted = append(accepted, candidate)
			} else {
				negatives = append(negatives, candidate)
			}

			continue
		}

		// A refusal counts as being about *this field* only when the error says so. Without that
		// test the probe attributes any 4xx to the enum, and a body refused for an unrelated
		// reason -- a different required field missing, a value invalid elsewhere -- yields a
		// closed-set claim plus a list of "rejected documented values" that were never rejected.
		//
		// A live run produced exactly that: four facts at Observed confidence, three of them wrong,
		// because every create in the sweep was refused over a field this probe was not looking at.
		// The same guard write.required has always had, for the same reason.
		if !resp.Error().Names(f.JSONPath) {
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
				Message: fmt.Sprintf(
					"the create carrying %s was refused with %d (%s), and the "+
						"error does not name this field, so the refusal says nothing about its value "+
						"set and this candidate was not counted",
					describeCandidate(
						f.AllowedValues,
						candidate,
					),
					resp.Status,
					resp.Error().Detail,
				),
			})

			continue
		}

		if !documented[candidate] {
			negatives = append(negatives, candidate)
			refusedNegatives++

			continue
		}

		// A documented candidate the host fixture refused is retried in every other
		// declared fixture before it is called rejected. Injecting one candidate into one
		// arbitrary body is how endpoint-agent -- a perfectly good object type, for a
		// dynamic tag -- landed in the pilot's rejected set: the refusal was about the
		// combination, and the probe recorded it as a fact about the value. Every retry
		// is a complete operator-declared body with only the probed field substituted, so
		// no cross-gate body is ever manufactured.
		attempts := []gatedOutcome{{gates: hostGates, pass: false}}
		acceptedSomewhere := false

		for i := range sc.Fixtures() {
			fx, _ := sc.Fixture(i)
			if fx.Name == hostName {
				continue
			}

			*seq++
			retry, retryGates, _, rErr := p.sendInto(ctx, s, sc, fx, f, candidate, *seq)
			out.Requests++

			if rErr != nil {
				return rErr
			}
			evidence = appendUnique(evidence, retry.Interaction)

			pass := retry.Status < 400
			if !pass && !retry.Error().Names(f.JSONPath) {
				// Refused over something else; evidence about nothing here.
				continue
			}

			attempts = append(attempts, gatedOutcome{gates: retryGates, pass: pass})
			if pass {
				acceptedSomewhere = true
			}
		}

		switch {
		case !acceptedSomewhere:
			// Refused by every fixture that produced an attributable answer.
			rejected = append(rejected, candidate)

		default:
			// The disagreement is real: one branch takes the value, another refuses it.
			// Attributed to a single declared gate when the observations allow it; when
			// they do not, the value is counted accepted -- the same direction every
			// validator decision errs in, because blocking a configuration the API takes
			// is the harm nobody can work around -- and the ambiguity is said out loud.
			if sep, branches, ok := attributeToGate(attempts); ok {
				for _, br := range branches {
					cond := []Condition{{JSONPath: sep, Equals: br.value}}
					key := Fact{When: cond}.whenKey()
					branchWhen[key] = cond
					if br.pass {
						branchAccepted[key] = append(branchAccepted[key], candidate)
					} else {
						branchRejected[key] = append(branchRejected[key], candidate)
					}
				}
			} else {
				accepted = append(accepted, candidate)
				out.Notes = append(out.Notes, Note{
					Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
					Message: fmt.Sprintf("%s was refused by fixture %s and accepted by "+
						"another; no single declared gate separates the outcomes, so it is "+
						"counted accepted and the branch is left for a human",
						describeCandidate(f.AllowedValues, candidate), hostName),
				})
			}
		}
	}

	// Each branch's facts, sorted so the committed document is byte-stable.
	branchKeys := make([]string, 0, len(branchWhen))
	for key := range branchWhen {
		branchKeys = append(branchKeys, key)
	}
	sort.Strings(branchKeys)

	for _, key := range branchKeys {
		if values := branchAccepted[key]; len(values) > 0 {
			fact := Fact{
				Resource: sc.Subject.Resource, JSONPath: f.JSONPath,
				Field: FactAcceptedValues, Value: ListValue(values),
				Confidence: Observed, Probe: p.Name(), Evidence: evidence,
				When: branchWhen[key],
			}
			fact.Rationale = fmt.Sprintf(
				"the API accepted %s, %s", strings.Join(values, ", "), fact.Because())
			out.Facts = append(out.Facts, fact)
		}
		if values := branchRejected[key]; len(values) > 0 {
			fact := Fact{
				Resource: sc.Subject.Resource, JSONPath: f.JSONPath,
				Field: FactRejectedValues, Value: ListValue(values),
				Confidence: Observed, Probe: p.Name(), Evidence: evidence,
				When: branchWhen[key],
			}
			fact.Rationale = fmt.Sprintf(
				"the API refused %s naming this field, %s",
				strings.Join(values, ", "), fact.Because())
			out.Facts = append(out.Facts, fact)
		}
	}

	if len(accepted) == 0 && len(rejected) == 0 {
		// Nothing was attributable to this field at all, so there is no set to describe. Said out
		// loud rather than deriving an enumClosed fact from the negatives alone.
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
			Message: "no documented value was either accepted or refused with an error naming " +
				"this field, so its value set is unprobed",
		})

		return nil
	}

	p.concludeEnum(sc, f, enumOutcome{
		accepted:         accepted,
		rejected:         rejected,
		negatives:        negatives,
		refusedNegatives: refusedNegatives,
		evidence:         evidence,
	}, out)

	if len(accepted) > 0 {
		*seq++

		return p.probeCase(ctx, s, sc, f, accepted[0], *seq, out)
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
) (*Response, []Condition, string, error) {
	// The field's host fixture: probing an enum only the dynamic branch declares
	// inside a static body would record the body's refusal as a fact about the value.
	fixture, _ := sc.HostFixture(f.JSONPath)

	return p.sendInto(ctx, s, sc, fixture, f, candidate, seq)
}

// sendInto creates with the candidate substituted into one particular fixture, reporting
// the gate values the body actually carried -- recorded, never reconstructed, so a
// conclusion about a branch cites the state that was really measured.
func (p enumBoundary) sendInto(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	fixture Fixture,
	f Field,
	candidate string,
	seq int,
) (*Response, []Condition, string, error) {
	body := fixture.Body
	body[sc.Subject.NameField] = s.NameValue(p.Name(), seq)
	body[f.JSONPath] = candidate

	resp, _, err := s.Create(ctx, p.Name(), body)
	if err != nil && !errors.Is(err, ErrNoIdentifier) {
		return nil, nil, "", err
	}

	return resp, gateConditions(sc, body), fixture.Name, nil
}

// concludeEnum records what the sweep established.
func (p enumBoundary) concludeEnum(sc Scope, f Field, o enumOutcome, out *Result) {
	if len(o.accepted) > 0 {
		out.Facts = append(out.Facts, Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   f.JSONPath,
			Field:      FactAcceptedValues,
			Value:      ListValue(o.accepted),
			Confidence: Observed,
			Probe:      p.Name(),
			Evidence:   o.evidence,
			Rationale: fmt.Sprintf("%d of the %d documented value(s) were accepted on create",
				len(o.accepted), len(f.AllowedValues)),
		})
	}

	// The valuable result: the specification is stale, and a spec-derived validator would have
	// been actively harmful.
	if len(o.rejected) > 0 {
		out.Facts = append(out.Facts, Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   f.JSONPath,
			Field:      FactRejectedValues,
			Value:      ListValue(o.rejected),
			Confidence: Observed,
			Probe:      p.Name(),
			Evidence:   o.evidence,
			Rationale: fmt.Sprintf("the specification documents %s but this API refused %s",
				strings.Join(f.AllowedValues, ", "), strings.Join(o.rejected, ", ")),
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
		Field:    FactValuesClosed,
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

	// What this probe establishes is which way a generated validator should err, not whether
	// there is one. The validator comes from the documented set, because that set is a
	// superset of what any one tenant takes; the accepted set is narrower, so building from
	// it would reject configurations a differently-licensed tenant can use.
	out.Notes = append(out.Notes, Note{
		Resource: sc.Subject.Resource, JSONPath: f.JSONPath, Probe: p.Name(),
		Message: "a generated OneOf comes from the documented set, not from this accepted " +
			"set: the documented set is the wider of the two, so a validator built from it " +
			"errs toward permitting and a stale specification surfaces as the API's own " +
			"error rather than as a plan failure. What this probe decides is whether there " +
			"is a validator at all -- an accepted value from outside the documented set " +
			"suppresses it",
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
	seq int,
	out *Result,
) error {
	variant := strings.ToUpper(accepted)
	if variant == accepted {
		// Nothing to vary: the value has no letters, or is already upper case.
		return nil
	}

	resp, _, _, err := p.send(ctx, s, sc, f, variant, seq)
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

// A transform is one awkward shape a value can be sent in, plus the ability to recognise that
// shape having been undone.
//
// Grouped by transform rather than by field, which is the departure from the original per-field
// contract. Whether a server trims whitespace is a property of the server far more often than of
// one field, so one create carrying "  padded  " in every string field observes all of them at
// once. Per field per value was three creates times the field count for an answer that is nearly
// always uniform.
type transform struct {
	// name is what a fact says was done to the value, phrased to read after "The API normalises
	// this value: ".
	name string
	// awkward produces the value to send, and reports whether this transform applies to the
	// field at all.
	awkward func(sc Scope, f Field) (any, bool)
	// recognise names the transform when it can see it in the difference between sent and
	// received, and reports false when the change is real but unrecognised.
	recognise func(sent, got any) (string, bool)
}

// normalisationTransforms are the shapes sent, one create each.
//
// Three, and the contract is explicit that this under-reports: numeric coercion, date
// reformatting and unicode normalisation are all missing. Those are absences rather than errors --
// the probe says nothing about them instead of saying something wrong.
var normalisationTransforms = []transform{
	{
		name: "surrounding whitespace is stripped",
		awkward: func(sc Scope, f Field) (any, bool) {
			if !freeText(sc, f) {
				return nil, false
			}

			return "  " + fmt.Sprint(sentinelFor(sc, f, 1)) + "  ", true
		},
		recognise: func(sent, got any) (string, bool) {
			from, okFrom := sent.(string)
			to, okTo := got.(string)

			if okFrom && okTo && to == strings.TrimSpace(from) {
				return "surrounding whitespace is stripped", true
			}

			return "", false
		},
	},
	{
		name: "letter case is changed",
		awkward: func(sc Scope, f Field) (any, bool) {
			if !freeText(sc, f) {
				return nil, false
			}

			return "MiXeD-" + fmt.Sprint(sentinelFor(sc, f, 2)), true
		},
		recognise: func(sent, got any) (string, bool) {
			from, okFrom := sent.(string)
			to, okTo := got.(string)

			if !okFrom || !okTo {
				return "", false
			}

			switch to {
			case strings.ToLower(from):
				return "the value is lower-cased", true
			case strings.ToUpper(from):
				return "the value is upper-cased", true
			}

			return "", false
		},
	},
	{
		name: "collection order is changed",
		awkward: func(_ Scope, f Field) (any, bool) {
			if !f.Kind.IsCollection() {
				return nil, false
			}

			// Deliberately not in sorted order, so a server that sorts is visible.
			return []any{"c", "b", "a"}, true
		},
		recognise: func(sent, got any) (string, bool) {
			from, okFrom := sent.([]any)
			to, okTo := got.([]any)

			if !okFrom || !okTo || len(from) != len(to) {
				return "", false
			}

			sorted := make([]any, len(from))
			copy(sorted, from)
			sort.Slice(sorted, func(i, j int) bool {
				return fmt.Sprint(sorted[i]) < fmt.Sprint(sorted[j])
			})

			if reflect.DeepEqual(to, sorted) && !reflect.DeepEqual(from, sorted) {
				return "the collection is re-sorted", true
			}

			return "", false
		},
	},
}

// freeText reports whether a field's value is unconstrained enough to carry an awkward shape.
//
// A field with a documented value set, or one the plan supplies values for, has exactly one thing it
// will accept -- so padding or re-casing it is not a normalisation experiment, it is a guaranteed
// 400 that loses the observation for every other field in the same body. A live run showed the
// cost: "Invalid Access Type: '  all  '" refused a create carrying awkward values for four fields.
func freeText(sc Scope, f Field) bool {
	return f.Kind == blueprint.KindString &&
		len(f.AllowedValues) == 0 &&
		len(sc.Candidates(f.JSONPath)) == 0
}

// normalisation implements the contract on the type in catalogue.go.
func (p normalisation) Exercise(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
) (Result, error) {
	var out Result

	if len(sc.Fixtures()) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "no fixture was supplied, so there is no valid body to send awkward values in",
		})

		return out, nil
	}

	for i, t := range normalisationTransforms {
		if err := p.sendTransform(ctx, s, sc, t, i+1, &out); err != nil {
			return out, err
		}
	}

	return out, nil
}

// sendTransform issues one create with every applicable field carrying the awkward shape.
func (p normalisation) sendTransform(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	t transform,
	seq int,
	out *Result,
) error {
	fixture, _ := sc.Fixture(0)

	body := fixture.Body
	body[sc.Subject.NameField] = s.NameValue(p.Name(), seq)

	sent := map[string]any{}
	skipped := 0

	for _, f := range sc.Sendable() {
		if strings.Contains(f.JSONPath, ".") {
			continue
		}

		value, applies := t.awkward(sc, f)
		if !applies {
			continue
		}

		// The name field can only carry a shape that keeps the prefix intact. Leading whitespace
		// and mixed case both destroy it, and a body whose name has lost the prefix is refused
		// before it is sent -- correctly, since the sweeper could not find the object.
		if f.JSONPath == sc.Subject.NameField {
			text, ok := value.(string)
			if !ok || !strings.HasPrefix(text, s.NamePrefix()) {
				skipped++
				continue
			}
		}

		body[f.JSONPath] = value
		sent[f.JSONPath] = value
	}

	if skipped > 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: sc.Subject.NameField, Probe: p.Name(),
			Message: fmt.Sprintf("%q could not be sent for this field because it carries the "+
				"sweeper's name prefix and the awkward shape would destroy it, so this transform "+
				"is unprobed for it", t.name),
		})
	}

	if len(sent) == 0 {
		return nil
	}

	resp, id, err := s.Create(ctx, p.Name(), body)
	out.Requests++

	if err != nil {
		return err
	}

	// A 4xx is no observation rather than evidence: a rejected value tells you nothing about what
	// the server would have done with an accepted one.
	if resp.Status >= 400 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("the create carrying %q was refused with %d (%s), so nothing "+
				"was observed about that transform", t.name, resp.Status, resp.Error().Detail),
		})

		return nil
	}

	read, err := s.ReadCreated(ctx, p.Name(), id, expansionQuery(sc))
	out.Requests++

	if err != nil {
		return err
	}

	p.compare(sc, t, sent, read, out)

	return nil
}

// compare records what came back changed.
func (p normalisation) compare(
	sc Scope,
	t transform,
	sent map[string]any,
	read *Response,
	out *Result,
) {
	paths := make([]string, 0, len(sent))
	for path := range sent {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		got, outcome := read.LookupField(path)
		if outcome != Present {
			// Absent is write.writable-returned's finding, not this probe's.
			continue
		}

		if sameValue(got, sent[path]) {
			continue
		}

		named, identified := t.recognise(sent[path], got)

		confidence := Suspected
		description := fmt.Sprintf("the value came back changed by %q, in a way this probe could "+
			"not identify", t.name)

		if identified {
			confidence = Observed
			description = named
		}

		out.Facts = append(out.Facts, Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   path,
			Field:      FactNormalisation,
			Value:      TextValue(description),
			Confidence: confidence,
			Probe:      p.Name(),
			Evidence:   []string{read.Interaction},
			Rationale:  fmt.Sprintf("%v was sent and %v came back", sent[path], got),
			Alternatives: alternativesUnless(identified,
				"the difference may be a server default overwriting the sent value rather than a "+
					"transformation of it, which write.writable-returned would distinguish"),
		})

		if identified {
			// Named in the layer a provider author actually reaches for. This note used to point
			// at semantic equality on a custom type, which is the framework's most precise
			// answer and almost nobody's: the reference provider -- 167 resources -- implements
			// CustomType zero times and StringSemanticEquals zero times, and solves exactly this
			// problem with plan modifiers instead. Pointing at the mechanism nobody uses reads
			// as a recommendation to go and build one.
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
				Message: "this is the direct cause of a perpetual diff: the practitioner writes " +
					"one value and the API stores another. Suppress it with a plan modifier on " +
					"this attribute that applies the same transformation to the planned value, " +
					"so the plan already says what the API will store. A custom type " +
					"implementing the framework's semantic-equality interface is the more " +
					"precise answer and is worth it for a type used widely; re-sorting or " +
					"re-casing inside the state mapper on every read is the wrong layer either " +
					"way, because it hides the difference rather than planning for it",
			})
		}
	}
}

// writeSideEffect implements the contract on the type in catalogue.go.
func (p writeSideEffect) Exercise(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
) (Result, error) {
	var out Result

	triggers := sc.Influencers()

	if len(sc.Fixtures()) == 0 || len(triggers) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "the plan declares no field whose value might affect another, so there is " +
				"no suspected trigger to perturb; declare one under defaultInfluencers",
		})

		return out, nil
	}

	// One trigger, and the first the plan declares. The protocol is a pair of creates -- with the
	// trigger and without it -- and running the pair per trigger would multiply the most
	// expensive budget there is for a fact that is Inferred either way.
	trigger := triggers[0]

	if len(triggers) > 1 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("the plan declares %d influencing field(s) and this protocol "+
				"perturbs one, %s; the others are unprobed for coupling",
				len(triggers), trigger.JSONPath),
		})
	}

	// Three creates, not two. The first two are byte-identical apart from the stamped name and
	// establish which unsent fields are stable at all -- without them every per-object field the
	// server assigns, the identifier first among them, differs between two objects and reads as
	// coupling. That was a false positive against an API with no coupling whatsoever.
	//
	// The same reasoning as write.server-default's identical pair, and self-contained rather than
	// borrowed from it: a probe run on its own with -only must not be less sound than one run in
	// sequence.
	first, sentWith, err := p.createAndRead(ctx, s, sc, trigger, true, 1, &out)
	if err != nil || first == nil {
		return out, err
	}

	second, _, err := p.createAndRead(ctx, s, sc, trigger, true, 2, &out)
	if err != nil || second == nil {
		return out, err
	}

	without, _, err := p.createAndRead(ctx, s, sc, trigger, false, 3, &out)
	if err != nil || without == nil {
		return out, err
	}

	p.compareCoupling(sc, trigger, sentWith, first, second, without, &out)

	return out, nil
}

// createAndRead creates one object with the trigger present or absent, and reads it back.
func (p writeSideEffect) createAndRead(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	trigger Field,
	include bool,
	seq int,
	out *Result,
) (*Response, map[string]any, error) {
	fixture, _ := sc.HostFixture(trigger.JSONPath)

	body := fixture.Body
	body[sc.Subject.NameField] = s.NameValue(p.Name(), seq)

	// Perturbed by *value* rather than by omission wherever a second value is available. A
	// required field cannot be omitted at all -- the create is refused and nothing is observed --
	// and the plan's influencers are very often required, which is how the pilot's objectType
	// blocked this protocol entirely on a live run.
	body[trigger.JSONPath] = p.perturbed(sc, trigger, include)

	if !include && body[trigger.JSONPath] == nil {
		delete(body, trigger.JSONPath)
	}

	sent := make(map[string]any, len(body))
	for k, v := range body {
		sent[k] = v
	}

	resp, id, err := s.Create(ctx, p.Name(), body)
	out.Requests++

	if err != nil {
		return nil, nil, err
	}

	if resp.Status >= 400 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("the create %s %s was refused with %d (%s), so no coupling was "+
				"observed", withOrWithout(include), trigger.JSONPath, resp.Status,
				resp.Error().Detail),
		})

		return nil, nil, nil
	}

	read, err := s.ReadCreated(ctx, p.Name(), id, expansionQuery(sc))
	out.Requests++

	if err != nil {
		return nil, nil, err
	}

	return read, sent, nil
}

func withOrWithout(include bool) string {
	if include {
		return "carrying the first value for"
	}

	return "carrying a different value for"
}

// perturbed is the value the trigger carries in each half of the comparison.
//
// A second candidate or a second documented enum value where one exists, and otherwise nil -- which
// the caller turns into an omission, the only perturbation left for a field with exactly one
// acceptable value.
func (p writeSideEffect) perturbed(sc Scope, trigger Field, first bool) any {
	alternatives := sc.Candidates(trigger.JSONPath)
	if len(alternatives) == 0 {
		for _, v := range trigger.AllowedValues {
			alternatives = append(alternatives, v)
		}
	}

	if first {
		if len(alternatives) > 0 {
			return alternatives[0]
		}

		return triggerValue(sc, trigger)
	}

	if len(alternatives) > 1 {
		return alternatives[1]
	}

	return nil
}

// triggerValue is what a suspected trigger is set to.
//
// True for a boolean, because the coupling this probe is looking for -- enabling one measurement
// silently enabling another -- is nearly always expressed as a flag.
func triggerValue(sc Scope, f Field) any {
	if f.Kind == blueprint.KindBool {
		return true
	}

	return sentinelFor(sc, f, 1)
}

// compareCoupling finds a field that differs between the two objects and was sent in neither.
func (p writeSideEffect) compareCoupling(
	sc Scope,
	trigger Field,
	sentWith map[string]any,
	first, second, without *Response,
	out *Result,
) {
	var (
		affected []string
		unstable []string
	)

	for _, f := range sc.Subject.Fields {
		if f.JSONPath == trigger.JSONPath || strings.Contains(f.JSONPath, ".") {
			continue
		}
		// Sent, so whatever it holds is ours rather than the server's.
		if _, wasSent := sentWith[f.JSONPath]; wasSent {
			continue
		}
		// The identifier differs between any two objects by definition.
		if f.JSONPath == sc.Subject.IDField {
			continue
		}

		a, aOutcome := first.LookupField(f.JSONPath)
		b, bOutcome := second.LookupField(f.JSONPath)
		c, cOutcome := without.LookupField(f.JSONPath)

		if aOutcome == Ambiguous || bOutcome == Ambiguous || cOutcome == Ambiguous {
			continue
		}
		if aOutcome == Absent && bOutcome == Absent && cOutcome == Absent {
			continue
		}

		// Not stable across the identical pair, so it varies on its own -- a counter, a
		// timestamp, an identifier -- and nothing can be attributed to the trigger.
		if aOutcome != bOutcome || !sameValue(a, b) {
			unstable = append(unstable, f.JSONPath)
			continue
		}

		if aOutcome != cOutcome || !sameValue(a, c) {
			affected = append(affected, f.JSONPath)
		}
	}

	if len(unstable) > 0 {
		sort.Strings(unstable)

		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: trigger.JSONPath, Probe: p.Name(),
			Message: fmt.Sprintf("%s varied between two byte-identical creates, so no coupling "+
				"could be attributed to this field for them", strings.Join(unstable, ", ")),
		})
	}

	if len(affected) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: trigger.JSONPath, Probe: p.Name(),
			Message: "no field the request did not carry differed between an object created with " +
				"this field and one created without it, so no coupling was observed",
		})

		return
	}

	sort.Strings(affected)

	// Inferred even after confirmation, and deliberately. "The server set it in response to this
	// request" and "the server always sets it for every object of this type" are different claims,
	// and one perturbation establishes only the first.
	out.Facts = append(out.Facts, Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   trigger.JSONPath,
		Field:      FactSideEffect,
		Value:      TextValue(strings.Join(affected, ", ")),
		Confidence: Inferred,
		Probe:      p.Name(),
		Evidence:   []string{first.Interaction, second.Interaction, without.Interaction},
		Rationale: fmt.Sprintf("%s was identical across two byte-identical creates carrying %s "+
			"and differed on one created without it, and no request sent %s",
			strings.Join(affected, ", "), trigger.JSONPath, strings.Join(affected, ", ")),
		Alternatives: []string{
			"the coupling may hold only for the value this probe sent rather than for the field " +
				"being present at all",
			"a field that changed may be a server default responding to the shorter body rather " +
				"than a side effect of the trigger",
		},
	})
}
