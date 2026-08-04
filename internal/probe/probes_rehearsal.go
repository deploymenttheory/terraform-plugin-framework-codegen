package probe

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// hopSet is everything one direction of one round observed, per JSON path.
type hopSet struct {
	// sentMax is what the maximal write actually carried, after contrast substitution.
	sentMax map[string]any
	// contrasted names the paths whose value was substituted because the derived one
	// equalled what the object already held -- an identical-value write observes nothing.
	contrasted map[string]bool

	createEcho *Response // the write response of the create
	afterMin   *Response // read after the minimal state was established (nil in direction B)
	updateEcho *Response // the maximal PUT's own response (nil without update)
	afterMax   *Response // read after the maximal state was established
	afterDown  *Response // read after the downgrade back to minimal (nil without update)
}

func (p rehearsal) Exercise(ctx context.Context, s *MutatingSession, sc Scope) (Result, error) {
	var out Result

	cfg := s.cfg.Rehearsal
	if cfg == nil {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "no rehearsal bodies were supplied; record through cmd (which derives " +
				"them from the blueprint) or replay a snapshot that froze rehearsal.json",
		})
		return out, nil
	}
	if can, why := sc.CanMutate(); !can {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(), Message: why,
		})
		return out, nil
	}

	maxRounds := cfg.MaxRounds
	if maxRounds == 0 {
		maxRounds = rehearsalMaxRounds
	}

	base := s.Findings().Facts()

	var prev RehearsalRound
	for i := range maxRounds {
		round, ok, err := p.nextRound(cfg, i, prev, base, &out, sc)
		if err != nil || !ok {
			return out, err
		}
		prev = round

		out.Rehearsal = append(out.Rehearsal, round)

		if err := p.rehearse(ctx, s, sc, round, i, &out); err != nil {
			return out, err
		}
	}

	return out, nil
}

// nextRound picks the round's bodies: frozen if the config carries one, derived
// otherwise. Not ok, with no error, when the fixpoint has converged or there is
// nothing left to run.
func (p rehearsal) nextRound(
	cfg *RehearsalConfig,
	i int,
	prev RehearsalRound,
	base []Fact,
	out *Result,
	sc Scope,
) (RehearsalRound, bool, error) {
	if i < len(cfg.Rounds) {
		return cfg.Rounds[i], true, nil
	}

	if cfg.Derive == nil {
		// Frozen rounds exhausted and nothing can derive more: replay of an
		// old-enough snapshot, or a config built without the closure. Both are
		// complete runs, not failures.
		return RehearsalRound{}, false, nil
	}

	facts := append(append([]Fact(nil), base...), out.Facts...)
	round, err := cfg.Derive(facts)
	if err != nil {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: "the next round's bodies could not be derived: " + err.Error(),
		})
		return RehearsalRound{}, false, nil
	}

	// Fixpoint: the new facts changed nothing about what would be sent, so another
	// round would replay the last one against a live tenant for no new observation.
	if i > 0 && equalRound(round, prev) {
		return RehearsalRound{}, false, nil
	}

	return round, true, nil
}

// rehearse executes both lifecycle directions for one round.
func (p rehearsal) rehearse(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	round RehearsalRound,
	roundIdx int,
	out *Result,
) error {
	// Direction A: the generated lifecycle exactly -- minimal create, maximal update.
	a, err := p.directionA(ctx, s, sc, round, roundIdx, out)
	if err != nil {
		return err
	}

	// Direction B: maximal create, minimal downgrade. The other half of the update
	// asymmetry, and the corroboration for every forced value A observed.
	b, err := p.directionB(ctx, s, sc, round, roundIdx, a, out)
	if err != nil {
		return err
	}

	p.conclude(sc, round, a, b, out)

	// The bisection spends its own bounded budget naming interaction culprits, and
	// only when an update operation exists to probe with.
	if sc.Subject.Update != nil {
		if err := p.bisectSuppressed(ctx, s, sc, round, roundIdx, a, out); err != nil {
			return err
		}
	}

	return nil
}

// directionA is minimal create -> read -> maximal update -> read -> downgrade -> read -> delete.
func (p rehearsal) directionA(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	round RehearsalRound,
	roundIdx int,
	out *Result,
) (*hopSet, error) {
	hops := &hopSet{}

	minimal := p.sendable(sc, round.Minimal, out)
	minimal[sc.Subject.NameField] = s.NameValue(p.Name(), roundIdx*2+1)

	resp, id, err := s.Create(ctx, p.Name(), minimal)
	out.Requests++
	if err != nil {
		return nil, err
	}
	if resp.Status >= 400 {
		// The generated minimal fixture would not apply. That is the first-class
		// whack-a-mole signal, surfaced here instead of in an acceptance run.
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("the derived minimal body was refused with %d (%s); the "+
				"generated minimal fixture would fail exactly like this",
				resp.Status, resp.Error().Detail),
		})
		return nil, nil
	}
	hops.createEcho = resp

	read1, err := s.ReadCreated(ctx, p.Name(), id, expansionQuery(sc))
	out.Requests++
	if err != nil {
		return nil, err
	}
	if e := read1.Error(); e.IsAuth() {
		return nil, authError(e)
	}
	hops.afterMin = read1

	if sc.Subject.Update == nil {
		if _, err := s.Delete(ctx, p.Name(), id); err != nil {
			return hops, err
		}
		out.Requests++
		return hops, nil
	}

	maximal, contrasted := p.contrast(sc, p.sendable(sc, round.Maximal, out), read1)
	maximal[sc.Subject.NameField] = minimal[sc.Subject.NameField]
	hops.sentMax, hops.contrasted = maximal, contrasted

	upd, err := s.Update(ctx, p.Name(), id, maximal)
	out.Requests++
	if err != nil {
		return hops, err
	}

	// A refused contrasted body retries uncontrasted once: the contrast is this
	// probe's own experiment, and a refusal it manufactured must not silence every
	// observation the derived body itself would have earned. A refusal of the
	// *derived* body is the real signal and stands.
	if upd.Status >= 400 && len(contrasted) > 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("the contrasted maximal update was refused with %d (%s); "+
				"retrying with the derived values alone -- equality writes observe less, "+
				"but a refusal the contrast manufactured must not silence the round",
				upd.Status, upd.Error().Detail),
		})

		maximal = p.sendable(sc, round.Maximal, out)
		maximal[sc.Subject.NameField] = minimal[sc.Subject.NameField]
		hops.sentMax, hops.contrasted = maximal, map[string]bool{}

		upd, err = s.Update(ctx, p.Name(), id, maximal)
		out.Requests++
		if err != nil {
			return hops, err
		}
	}

	if upd.Status >= 400 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("the derived maximal update was refused with %d (%s); the "+
				"generated acceptance test's update step would fail exactly like this",
				upd.Status, upd.Error().Detail),
		})
		if _, err := s.Delete(ctx, p.Name(), id); err != nil {
			return hops, err
		}
		out.Requests++
		return hops, nil
	}
	hops.updateEcho = upd

	read2, err := s.ReadCreated(ctx, p.Name(), id, expansionQuery(sc))
	out.Requests++
	if err != nil {
		return hops, err
	}
	hops.afterMax = read2

	if _, err := s.Update(ctx, p.Name(), id, minimal); err != nil {
		return hops, err
	}
	out.Requests++

	read3, err := s.ReadCreated(ctx, p.Name(), id, expansionQuery(sc))
	out.Requests++
	if err != nil {
		return hops, err
	}
	hops.afterDown = read3

	if _, err := s.Delete(ctx, p.Name(), id); err != nil {
		return hops, err
	}
	out.Requests++

	return hops, nil
}

// directionB is maximal create -> read -> minimal downgrade -> read -> delete.
//
// The maximal body is direction A's actual sent body, contrasts included, so every
// observation here corroborates A's on the other write path rather than measuring a
// slightly different experiment.
func (p rehearsal) directionB(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	round RehearsalRound,
	roundIdx int,
	a *hopSet,
	out *Result,
) (*hopSet, error) {
	hops := &hopSet{}

	maximal := map[string]any{}
	if a != nil && a.sentMax != nil {
		for k, v := range a.sentMax {
			maximal[k] = v
		}
		hops.contrasted = a.contrasted
	} else {
		maximal = p.sendable(sc, round.Maximal, out)
	}
	maximal[sc.Subject.NameField] = s.NameValue(p.Name(), roundIdx*2+2)
	hops.sentMax = maximal

	resp, id, err := s.Create(ctx, p.Name(), maximal)
	out.Requests++
	if err != nil {
		return nil, err
	}
	if resp.Status >= 400 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("the derived maximal body was refused as a create with %d "+
				"(%s); a maximal-first acceptance configuration would fail exactly like this",
				resp.Status, resp.Error().Detail),
		})
		return nil, nil
	}
	hops.createEcho = resp

	read1, err := s.ReadCreated(ctx, p.Name(), id, expansionQuery(sc))
	out.Requests++
	if err != nil {
		return nil, err
	}
	hops.afterMax = read1

	if sc.Subject.Update != nil {
		minimal := p.sendable(sc, round.Minimal, out)
		minimal[sc.Subject.NameField] = maximal[sc.Subject.NameField]

		if _, err := s.Update(ctx, p.Name(), id, minimal); err != nil {
			return hops, err
		}
		out.Requests++

		read2, err := s.ReadCreated(ctx, p.Name(), id, expansionQuery(sc))
		out.Requests++
		if err != nil {
			return hops, err
		}
		hops.afterDown = read2
	}

	if _, err := s.Delete(ctx, p.Name(), id); err != nil {
		return hops, err
	}
	out.Requests++

	return hops, nil
}

// sendable copies a derived body for sending. Denied paths stay in: the deny list
// forbids *probing* a field -- rotating candidates, perturbing values -- not carrying
// it in a fixture-shaped body, and every fixture-driven probe has always sent them.
// The wave's plans deny interval, which the API requires: a rehearsal that stripped
// it could never rehearse anything. What deny does gate here is the experiments --
// no contrast substitution and no bisection on a denied path.
func (p rehearsal) sendable(sc Scope, body map[string]any, out *Result) map[string]any {
	sent := make(map[string]any, len(body))
	for k, v := range body {
		sent[k] = v
	}
	return sent
}

// contrast substitutes a fresh value where the derived one equals what the object
// already holds: an identical-value write observes nothing, and the bool-default trap
// -- derived true, server default true -- is exactly the case that hid
// networkMeasurements. Strings without a value set are left alone; inventing one
// risks a pattern refusal that would abort the whole lifecycle.
func (p rehearsal) contrast(
	sc Scope,
	body map[string]any,
	current *Response,
) (map[string]any, map[string]bool) {
	contrasted := map[string]bool{}

	for k, v := range body {
		if sc.Denied(k) {
			continue // deny forbids the experiment, not the send
		}

		held, outcome := current.LookupField(k)
		if outcome != Present || fmt.Sprint(held) != fmt.Sprint(v) {
			continue
		}

		f, ok := sc.Subject.Field(k)
		if !ok {
			continue
		}

		if next, ok := contrastValue(f, v); ok {
			body[k] = next
			contrasted[k] = true
		}
	}

	return body, contrasted
}

// contrastValue picks a different value the API plausibly takes, inside the
// declared bounds -- a contrast outside them manufactures a refusal about a value
// nobody would configure, and it aborts the whole lifecycle it was meant to observe.
func contrastValue(f Field, v any) (any, bool) {
	switch tv := v.(type) {
	case bool:
		return !tv, true
	case float64:
		return numericContrast(f, tv)
	case int:
		if next, ok := numericContrast(f, float64(tv)); ok {
			return int(next.(float64)), true
		}
		return nil, false
	case int64:
		if next, ok := numericContrast(f, float64(tv)); ok {
			return int64(next.(float64)), true
		}
		return nil, false
	case string:
		rejected := map[string]bool{}
		for _, r := range f.Behaviour.RejectedValues {
			rejected[r] = true
		}
		for _, pool := range [][]string{f.Behaviour.AcceptedValues, f.AllowedValues} {
			for _, candidate := range pool {
				if candidate != tv && !rejected[candidate] {
					return candidate, true
				}
			}
		}
		return nil, false
	default:
		return nil, false
	}
}

// numericContrast steps a number up when the bounds allow it, down when only down
// fits, and refuses when the range holds a single value.
func numericContrast(f Field, v float64) (any, bool) {
	c := f.Constraints
	if c.Maximum == nil || v+1 <= *c.Maximum {
		return v + 1, true
	}
	if c.Minimum == nil || v-1 >= *c.Minimum {
		return v - 1, true
	}
	return nil, false
}

// conclude turns the two directions' hop matrices into facts.
func (p rehearsal) conclude(sc Scope, round RehearsalRound, a, b *hopSet, out *Result) {
	if a == nil || a.sentMax == nil {
		return
	}

	paths := make([]string, 0, len(a.sentMax))
	for k := range a.sentMax {
		if k == sc.Subject.NameField {
			continue
		}
		paths = append(paths, k)
	}
	sort.Strings(paths)

	for _, path := range paths {
		field, _ := sc.Subject.Field(path)
		sent := a.sentMax[path]

		p.concludeUpdateEcho(sc, path, sent, a, out)
		p.concludeForced(sc, path, field, sent, a, b, out)
		p.concludeDowngrade(sc, round, path, field, a, b, out)
		p.concludeNeverRead(sc, path, a, b, out)
	}
}

// concludeNeverRead emits returnedOnRead=false when a field the maximal write sent
// reads back null or absent -- null-aware, which the read-tier conclusion is not.
//
// The distinction is the includeHeaders class: the API answers an explicit null, the
// path resolves, and a presence-only observation calls that "returned". Flattening it
// blanks the configured value on every refresh, so explicit-null-for-a-sent-value is
// the false that matters. Only false is emitted: an unconditional true from this one
// context could overwrite branch-scoped knowledge the mixed-presence machinery holds.
func (p rehearsal) concludeNeverRead(sc Scope, path string, a, b *hopSet, out *Result) {
	nulled := func(h *hopSet) (bool, string) {
		if h == nil || h.afterMax == nil {
			return false, ""
		}
		v, outcome := h.afterMax.LookupField(path)
		return outcome == Absent || (outcome == Present && v == nil), h.afterMax.Interaction
	}

	nulledA, evA := nulled(a)
	if !nulledA {
		return
	}

	confidence := Observed
	evidence := []string{evA}
	if nulledB, evB := nulled(b); nulledB {
		confidence = Corroborated
		evidence = appendUnique(evidence, evB)
	}

	out.Facts = append(out.Facts, Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   path,
		Field:      FactReturnedOnRead,
		Value:      BoolValue(false),
		Confidence: confidence,
		Probe:      p.Name(),
		Evidence:   evidence,
		Rationale: "sent a value and read back null or nothing, so state must carry the " +
			"configured value rather than flatten an empty read",
	})
}

// concludeUpdateEcho reads the maximal PUT's own response for the field it was sent.
func (p rehearsal) concludeUpdateEcho(sc Scope, path string, sent any, a *hopSet, out *Result) {
	if a.updateEcho == nil {
		return
	}

	value, outcome := a.updateEcho.LookupField(path)
	if outcome == Ambiguous {
		return
	}

	echoed := outcome == Present && value != nil
	rationale := "the update response carried the field it was sent"
	if !echoed {
		rationale = "the update response did not carry this field although the update sent it"
	}

	out.Facts = append(out.Facts, Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   path,
		Field:      FactReturnedOnUpdate,
		Value:      BoolValue(echoed),
		Confidence: Observed,
		Probe:      p.Name(),
		Evidence:   []string{a.updateEcho.Interaction},
		Rationale:  rationale,
	})
	_ = sent
}

// concludeForced looks for the server substituting its own value: sent x, stored
// y, y independent of x. Corroborated when both write paths stored the same y for
// the same sent x; Observed from one path alone.
func (p rehearsal) concludeForced(
	sc Scope,
	path string,
	field Field,
	sent any,
	a, b *hopSet,
	out *Result,
) {
	stored := func(h *hopSet) (any, bool) {
		if h == nil || h.afterMax == nil {
			return nil, false
		}
		v, outcome := h.afterMax.LookupField(path)
		return v, outcome == Present && v != nil
	}

	va, okA := stored(a)
	if !okA || fmt.Sprint(va) == fmt.Sprint(sent) {
		return
	}

	// A transform of the sent value is normalisation, another probe's business;
	// forcing means the stored value owes nothing to the sent one. The cheap
	// separator: a transform of a string still contains or resembles it, but the
	// decisive evidence is the second path storing the identical value.
	evidence := []string{a.afterMax.Interaction}
	confidence := Observed

	if vb, okB := stored(b); okB && fmt.Sprint(vb) == fmt.Sprint(va) {
		confidence = Corroborated
		evidence = appendUnique(evidence, b.afterMax.Interaction)
	}

	lit, ok := literalFor(field.Kind, va)
	if !ok {
		return
	}

	out.Facts = append(out.Facts, Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   path,
		Field:      FactServerForced,
		Value:      LiteralValue(lit),
		Confidence: confidence,
		Probe:      p.Name(),
		Evidence:   evidence,
		Rationale: fmt.Sprintf("sent %v, the API stored %v on the update path"+
			corroboratedSuffix(confidence), sent, va),
	})
}

func corroboratedSuffix(c Confidence) string {
	if c == Corroborated {
		return " and the create path stored the same value"
	}
	return ""
}

// concludeDowngrade watches what the minimal write did to each field only the
// maximal body carries: preserved, cleared, or reset to a constant.
func (p rehearsal) concludeDowngrade(
	sc Scope,
	round RehearsalRound,
	path string,
	field Field,
	a, b *hopSet,
	out *Result,
) {
	if _, inMinimal := round.Minimal[path]; inMinimal {
		return
	}
	if a.afterMax == nil || a.afterDown == nil {
		return
	}

	before, outcomeBefore := a.afterMax.LookupField(path)
	after, outcomeAfter := a.afterDown.LookupField(path)
	if outcomeBefore != Present || outcomeAfter == Ambiguous {
		return
	}

	held := outcomeAfter == Present && after != nil
	if held && fmt.Sprint(after) == fmt.Sprint(before) {
		return // preserved: merge semantics, updateStyle's fact already covers it
	}
	if !held {
		return // cleared: putFull, likewise updateStyle's conclusion
	}

	// Reverted to a constant that is neither the stored value nor absence.
	confidence := Inferred
	evidence := []string{a.afterDown.Interaction}
	if b != nil && b.afterDown != nil {
		if vb, outcome := b.afterDown.LookupField(path); outcome == Present &&
			fmt.Sprint(vb) == fmt.Sprint(after) {
			confidence = Corroborated
			evidence = appendUnique(evidence, b.afterDown.Interaction)
		}
	}

	out.Facts = append(out.Facts, Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   path,
		Field:      FactUpdateResets,
		Value:      BoolValue(true),
		Confidence: Observed,
		Probe:      p.Name(),
		Evidence:   []string{a.afterDown.Interaction},
		Rationale: fmt.Sprintf("held %v, was omitted from an update, and read back %v "+
			"rather than the held value", before, after),
	})

	if lit, ok := literalFor(field.Kind, after); ok {
		out.Facts = append(out.Facts, Fact{
			Resource:   sc.Subject.Resource,
			JSONPath:   path,
			Field:      FactUpdateDefault,
			Value:      LiteralValue(lit),
			Confidence: confidence,
			Probe:      p.Name(),
			Evidence:   evidence,
			Rationale: "the constant an omitted-on-update field reverts to; a derived " +
				"value remains the open alternative until a second downgrade agrees",
		})
	}
}

// bisectSuppressed names the sibling whose presence suppresses a field that
// round-trips alone.
//
// Candidates are the maximal-sent scalar paths that came back null or absent after
// the maximal update. For each, the field is first sent alone on top of the minimal
// body: if it still does not come back, it is write-only territory and the echo facts
// already say so. If it does come back, some sibling suppressed it, and a binary
// search over the delta finds a single culprit in log2 writes. Two culprits that only
// suppress together defeat the search; that outcome is reported as the untested
// combination it is.
func (p rehearsal) bisectSuppressed(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	round RehearsalRound,
	roundIdx int,
	a *hopSet,
	out *Result,
) error {
	if a == nil || a.afterMax == nil {
		return nil
	}

	candidates := p.suppressedCandidates(sc, round, a)
	if len(candidates) == 0 {
		return nil
	}

	// One object hosts the whole bisection: created minimal, mutated per experiment.
	name := s.NameValue(p.Name(), 100+roundIdx)
	minimal := p.sendable(sc, round.Minimal, out)
	minimal[sc.Subject.NameField] = name

	resp, id, err := s.Create(ctx, p.Name(), minimal)
	out.Requests++
	if err != nil {
		return err
	}
	if resp.Status >= 400 {
		return nil
	}
	defer func() {
		if _, derr := s.Delete(ctx, p.Name(), id); derr == nil {
			out.Requests++
		}
	}()

	budget := rehearsalBisectBudget
	for _, path := range candidates {
		if budget <= 0 {
			out.Notes = append(out.Notes, Note{
				Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
				Message: "suppressed in the maximal body, and the bisection budget was " +
					"spent before this field's culprit could be named",
			})
			continue
		}

		if err := p.bisectOne(ctx, s, sc, round, id, name, path, a, &budget, out); err != nil {
			return err
		}
	}

	return nil
}

// suppressedCandidates is every maximal-sent scalar path, not itself minimal, that
// read back null or absent after the maximal update -- capped so a schema where
// everything vanished (one giant interaction, or plain write-only) does not bisect
// the world.
func (p rehearsal) suppressedCandidates(sc Scope, round RehearsalRound, a *hopSet) []string {
	var out []string

	for path := range a.sentMax {
		if path == sc.Subject.NameField || sc.Denied(path) {
			continue
		}
		if _, inMinimal := round.Minimal[path]; inMinimal {
			continue
		}
		if f, ok := sc.Subject.Field(path); !ok || f.Kind.IsNested() || f.Kind.IsCollection() {
			continue
		}
		if v, outcome := a.afterMax.LookupField(path); outcome == Present && v != nil {
			continue
		}
		out = append(out, path)
	}

	sort.Strings(out)

	const maxCandidates = 3
	if len(out) > maxCandidates {
		out = out[:maxCandidates]
	}

	return out
}

// bisectOne isolates one field's suppressor, spending from the shared budget.
func (p rehearsal) bisectOne(
	ctx context.Context,
	s *MutatingSession,
	sc Scope,
	round RehearsalRound,
	id, name, path string,
	a *hopSet,
	budget *int,
	out *Result,
) error {
	value := a.sentMax[path]

	// echoes reports whether a PUT of minimal + the field + the given siblings
	// answers the field back. The body carries the object's own stamped name, so the
	// session's prefix check holds and no rename muddies the experiment.
	echoes := func(siblings []string) (bool, string, error) {
		body := p.sendable(sc, round.Minimal, out)
		body[sc.Subject.NameField] = name
		body[path] = value
		for _, sib := range siblings {
			body[sib] = a.sentMax[sib]
		}

		resp, err := s.Update(ctx, p.Name(), id, body)
		out.Requests++
		*budget--
		if err != nil {
			return false, "", err
		}
		if resp.Status >= 400 {
			return false, "", nil
		}
		v, outcome := resp.LookupField(path)
		return outcome == Present && v != nil, resp.Interaction, nil
	}

	// Alone first: if the field does not come back even by itself, no sibling did
	// this, and the echo facts already describe a write-only field.
	alone, _, err := echoes(nil)
	if err != nil || !alone {
		return err
	}

	// The delta the maximal body added, minus the field itself.
	var delta []string
	for k := range a.sentMax {
		if k == path || k == sc.Subject.NameField {
			continue
		}
		if _, inMinimal := round.Minimal[k]; inMinimal {
			continue
		}
		delta = append(delta, k)
	}
	sort.Strings(delta)

	for len(delta) > 1 && *budget > 0 {
		half := delta[:len(delta)/2]
		echoed, _, err := echoes(half)
		if err != nil {
			return err
		}
		if echoed {
			delta = delta[len(delta)/2:]
		} else {
			delta = half
		}
	}

	if len(delta) != 1 || *budget <= 0 {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
			Message: "round-trips alone and is suppressed in the maximal body, but no " +
				"single sibling explains it; the combination is untested",
		})
		return nil
	}

	culprit := delta[0]

	// Confirm the pair, so the conclusion is an observation rather than the last
	// state of a search.
	suppressed, interaction, err := echoes([]string{culprit})
	if err != nil {
		return err
	}
	if suppressed {
		out.Notes = append(out.Notes, Note{
			Resource: sc.Subject.Resource, JSONPath: path, Probe: p.Name(),
			Message: fmt.Sprintf("the bisection converged on %q, but the pair alone does "+
				"not reproduce the suppression; the combination is untested", culprit),
		})
		return nil
	}

	out.Facts = append(out.Facts, Fact{
		Resource:   sc.Subject.Resource,
		JSONPath:   path,
		Field:      FactInteractionSuppressed,
		Value:      TextValue(fmt.Sprintf("suppressed when %s is present", culprit)),
		Confidence: Observed,
		Probe:      p.Name(),
		Evidence:   []string{interaction},
		When: []Condition{{
			JSONPath: culprit, Equals: fmt.Sprint(a.sentMax[culprit]),
		}},
		Rationale: fmt.Sprintf("round-trips alone, and is stored as null the moment %q "+
			"rides along; isolated by bisecting the maximal body's delta", culprit),
	})

	return nil
}

// literalFor renders an observed wire value as a blueprint literal.
func literalFor(kind blueprint.TypeKind, v any) (blueprint.Literal, bool) {
	switch tv := v.(type) {
	case bool:
		return blueprint.Literal{Kind: kind, Raw: strconv.FormatBool(tv)}, true
	case string:
		return blueprint.Literal{Kind: kind, Raw: strconv.Quote(tv)}, true
	case float64:
		return blueprint.Literal{Kind: kind, Raw: trimFloat(tv)}, true
	default:
		return blueprint.Literal{}, false
	}
}

// equalRound reports whether two rounds would send the same bodies.
func equalRound(a, b RehearsalRound) bool {
	return equalBody(a.Minimal, b.Minimal) && equalBody(a.Maximal, b.Maximal)
}

func equalBody(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || fmt.Sprint(v) != fmt.Sprint(w) {
			return false
		}
	}
	return true
}
