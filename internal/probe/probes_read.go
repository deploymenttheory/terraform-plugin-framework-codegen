package probe

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe/apierr"
)

// The read-only probe bodies. Their contracts -- what each sends, what it infers, and how it
// can be wrong -- are documented on the types in catalogue.go, and this file implements them
// without restating that.
//
// A convention runs through all of them: **absence of a fact is a legitimate outcome.** An
// empty tenant, an unreadable field, an observation that came out ambiguous -- each produces
// a note and no fact rather than a weak fact. A Suspected fact in the store is a claim
// nobody can act on; a note is a prompt to look.

// probeQueryParam is the parameter R4 sends to see whether unknown ones are tolerated.
//
// Named after the tool so that anybody reading their API's access log can tell what sent it.
const probeQueryParam = "tfpfgen_probe"

func (p unknownParamTolerance) Observe(
	ctx context.Context,
	s ReadSession,
	subj Subject,
) (Result, error) {
	var out Result

	resp, err := get(ctx, s, p.Name(), s.CollectionPath(), url.Values{probeQueryParam: {"1"}})
	if err != nil {
		return out, err
	}
	out.Requests = 1

	if e := resp.Error(); e.IsAuth() {
		return out, authError(e)
	}

	tolerated := subj.Read.Succeeded(resp.Status) || (resp.Status >= 200 && resp.Status < 300)

	out.Facts = append(out.Facts, Fact{
		Resource:   subj.Resource,
		Field:      FactUnknownParamTolerated,
		Value:      BoolValue(tolerated),
		Confidence: Observed,
		Probe:      p.Name(),
		Evidence:   []string{evidenceID(1, "GET", s.CollectionPath())},
		Rationale: fmt.Sprintf(
			"a collection read carrying an unrecognised query parameter answered %d", resp.Status),
		Alternatives: []string{
			"a gateway rather than the API itself may have rejected the parameter, in which " +
				"case tolerance of unknown request-body fields does not follow",
		},
	})

	return out, nil
}

// absentID is the identifier R5 asks for.
//
// Well-formed rather than nonsense: an API that validates the shape of an identifier before
// looking it up would answer 400 for "does-not-exist", and the probe would then record a
// fact about parameter validation while believing it had learned something about absence.
// A long numeric string is the shape nearly every API accepts and nearly no tenant holds.
const absentID = "999999999"

func (p notFoundShape) Observe(ctx context.Context, s ReadSession, subj Subject) (Result, error) {
	var out Result

	resp, err := get(ctx, s, p.Name(), s.ItemPath(absentID), nil)
	if err != nil {
		return out, err
	}
	out.Requests = 1

	e := resp.Error()
	if e.IsAuth() {
		return out, authError(e)
	}

	evidence := []string{evidenceID(1, "GET", s.ItemPath(absentID))}

	// A 2xx means the identifier was not absent after all, so nothing about absence was
	// learned. Reported rather than recorded: this is the case where the chosen identifier
	// happened to exist.
	if resp.Status >= 200 && resp.Status < 300 {
		out.Notes = append(out.Notes, Note{
			Resource: subj.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("reading id %s answered %d, so it exists and nothing was "+
				"learned about how absence is reported", absentID, resp.Status),
		})
		return out, nil
	}

	out.Facts = append(out.Facts, Fact{
		Resource:   subj.Resource,
		Field:      FactNotFoundIsSuccess,
		Value:      BoolValue(resp.Status == 404),
		Confidence: Observed,
		Probe:      p.Name(),
		Evidence:   evidence,
		Rationale: fmt.Sprintf("reading an absent object answered %d with a %s error body",
			resp.Status, e.Envelope),
		Alternatives: alternativesForNotFound(resp.Status),
	})

	// The envelope is worth recording separately: it feeds the generated error mapper and
	// the Phase 5 mocks, and it is what every later probe's classifier depends on.
	out.Facts = append(out.Facts, Fact{
		Resource:   subj.Resource,
		Field:      FactErrorEnvelope,
		Value:      TextValue(fmt.Sprintf("%d=%s", resp.Status, e.Envelope)),
		Confidence: Observed,
		Probe:      p.Name(),
		Evidence:   evidence,
		Rationale:  fmt.Sprintf("a %d response used the %s error shape", resp.Status, e.Envelope),
	})

	return out, nil
}

func alternativesForNotFound(status int) []string {
	if status == 403 {
		// The genuinely ambiguous case, and worth stating rather than resolving: an API
		// that hides other tenants' objects behind 403 looks identical to one that returns
		// 403 for anything absent.
		return []string{
			"a 403 may mean the object exists in another tenant rather than not existing, " +
				"which this sequence cannot distinguish",
		}
	}
	return []string{
		"the API may answer differently for an identifier that once existed and was deleted",
	}
}

func (p listShape) Observe(ctx context.Context, s ReadSession, subj Subject) (Result, error) {
	var out Result

	resp, err := get(ctx, s, p.Name(), s.CollectionPath(), nil)
	if err != nil {
		return out, err
	}
	out.Requests = 1

	if e := resp.Error(); e.IsAuth() {
		return out, authError(e)
	}
	if !subj.Read.Succeeded(resp.Status) && (resp.Status < 200 || resp.Status >= 300) {
		out.Notes = append(out.Notes, Note{
			Resource: subj.Resource,
			Probe:    p.Name(),
			Message: fmt.Sprintf(
				"listing the collection answered %d: %s",
				resp.Status,
				resp.Error(),
			),
		})
		return out, nil
	}

	items := resp.Items()

	// An empty collection is the *normal* state of a good sandbox, so this degrades to a
	// note rather than erroring. A probe that failed here would make a clean tenant look
	// like a broken one.
	if len(items) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: subj.Resource, Probe: p.Name(),
			Message: "the collection is empty, so no item shape was observed and later probes " +
				"have no fixture to draw on",
		})
		return out, nil
	}

	if key := resp.EnvelopeKey(); key != "" {
		out.Facts = append(out.Facts, Fact{
			Resource:   subj.Resource,
			Field:      FactErrorEnvelope,
			Value:      TextValue("listEnvelopeKey=" + key),
			Confidence: Observed,
			Probe:      p.Name(),
			Evidence:   []string{evidenceID(1, "GET", s.CollectionPath())},
			Rationale:  fmt.Sprintf("the list response wraps its items under %q", key),
		})
	}

	return out, nil
}

// volatileReads is how many times R3 reads the same object.
//
// Three rather than two: two reads that differ could be one read that was unlucky, and three
// makes "changes on every read" distinguishable from "changed once".
const volatileReads = 3

func (p volatileOnRead) Observe(ctx context.Context, s ReadSession, subj Subject) (Result, error) {
	var out Result

	// A fixture is needed, and the only source available to a read-only probe is the
	// collection. An empty tenant means no observation, which is not a failure.
	listResp, err := get(ctx, s, p.Name(), s.CollectionPath(), nil)
	if err != nil {
		return out, err
	}
	out.Requests = 1

	if e := listResp.Error(); e.IsAuth() {
		return out, authError(e)
	}

	id := firstID(listResp.Items())
	if id == "" {
		out.Notes = append(out.Notes, Note{
			Resource: subj.Resource, Probe: p.Name(),
			Message: "no existing object to read repeatedly, so volatility was not observed",
		})
		return out, nil
	}

	snapshots := make([]map[string]any, 0, volatileReads)

	for i := range volatileReads {
		resp, err := get(ctx, s, p.Name(), s.ItemPath(id), nil)
		if err != nil {
			return out, err
		}
		out.Requests++

		if resp.Status < 200 || resp.Status >= 300 {
			out.Notes = append(out.Notes, Note{
				Resource: subj.Resource, Probe: p.Name(),
				Message: fmt.Sprintf("read %d of %d answered %d, so volatility was not observed",
					i+1, volatileReads, resp.Status),
			})
			return out, nil
		}

		obj, _ := resp.Body.(map[string]any)
		snapshots = append(snapshots, obj)
	}

	changed := fieldsThatDiffer(snapshots)

	// No diff produces **no fact at all**. Stability across three reads a few milliseconds
	// apart is not evidence of stability, and emitting Volatile=false would license merge to
	// act on an absence of evidence.
	if len(changed) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: subj.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("no field differed across %d reads; that is not evidence of "+
				"stability, so no volatility fact was recorded", volatileReads),
		})
		return out, nil
	}

	evidence := make([]string, 0, volatileReads)
	for i := range volatileReads {
		evidence = append(evidence, evidenceID(i+2, "GET", s.ItemPath(id)))
	}

	for _, field := range changed {
		// Only fields the schema knows about: a fact about a field with no attribute has
		// nowhere to be merged to.
		if _, known := subj.Field(field); !known {
			out.Notes = append(out.Notes, Note{
				Resource: subj.Resource, JSONPath: field, Probe: p.Name(),
				Message: "this field changed between reads but has no attribute in the blueprint",
			})
			continue
		}

		out.Facts = append(out.Facts, Fact{
			Resource:   subj.Resource,
			JSONPath:   field,
			Field:      FactVolatile,
			Value:      BoolValue(true),
			Confidence: Observed,
			Probe:      p.Name(),
			Evidence:   evidence,
			Rationale: fmt.Sprintf(
				"the value differed across %d consecutive reads of the same object", volatileReads),
			Alternatives: []string{
				"something else in the tenant may have been editing the object concurrently",
			},
		})
	}

	return out, nil
}

// fieldsThatDiffer returns the top-level fields that are not identical across every snapshot.
//
// Every snapshot, not just the first and last: a field that changes on read 1 and reverts on
// read 3 is still volatile, and comparing only the ends would miss it.
func fieldsThatDiffer(snapshots []map[string]any) []string {
	if len(snapshots) < 2 {
		return nil
	}

	changed := map[string]bool{}

	for _, field := range unionOfKeys(snapshots) {
		first, firstOK := snapshots[0][field]

		for _, snap := range snapshots[1:] {
			v, ok := snap[field]
			if ok != firstOK || !sameJSON(first, v) {
				changed[field] = true
				break
			}
		}
	}

	out := make([]string, 0, len(changed))
	for field := range changed {
		out = append(out, field)
	}
	sort.Strings(out)

	return out
}

func unionOfKeys(snapshots []map[string]any) []string {
	seen := map[string]bool{}
	for _, snap := range snapshots {
		for k := range snap {
			seen[k] = true
		}
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)

	return out
}

// badTypedValue is the value R6 sends for a typed parameter, to provoke an error without
// mutating anything.
const badTypedValue = "not-a-number"

// typedParamCandidates are parameters likely to be typed, tried in order.
//
// "limit" first because a paginated collection almost always has one. Nothing here mutates,
// so trying several costs only requests.
var typedParamCandidates = []string{"limit", "max", "offset", "page"}

func (p errorEnvelope) Observe(ctx context.Context, s ReadSession, subj Subject) (Result, error) {
	var out Result

	for i, param := range typedParamCandidates {
		resp, err := get(ctx, s, p.Name(), s.CollectionPath(), url.Values{param: {badTypedValue}})
		if err != nil {
			return out, err
		}
		out.Requests++

		e := resp.Error()
		if e.IsAuth() {
			return out, authError(e)
		}

		// A 2xx means the parameter was ignored rather than validated, so nothing about the
		// error shape was learned from it. Try the next candidate.
		if resp.Status >= 200 && resp.Status < 300 {
			continue
		}

		out.Facts = append(out.Facts, Fact{
			Resource:   subj.Resource,
			Field:      FactErrorEnvelope,
			Value:      TextValue(fmt.Sprintf("%d=%s", resp.Status, e.Envelope)),
			Confidence: Observed,
			Probe:      p.Name(),
			Evidence:   []string{evidenceID(i+1, "GET", s.CollectionPath())},
			Rationale: fmt.Sprintf(
				"an invalid value for the %q parameter answered %d with a %s body",
				param,
				resp.Status,
				e.Envelope,
			),
			Alternatives: []string{
				"an API may use a different error shape for authorisation failures than for " +
					"validation failures, which one malformed read cannot establish",
			},
		})

		// The detail field is what lets later probes emit Observed rather than Inferred, so
		// whether this API populates it is worth its own fact.
		out.Facts = append(out.Facts, Fact{
			Resource:   subj.Resource,
			Field:      FactErrorEnvelope,
			Value:      TextValue(fmt.Sprintf("namesOffendingField=%t", e.Detail != "")),
			Confidence: Observed,
			Probe:      p.Name(),
			Evidence:   []string{evidenceID(i+1, "GET", s.CollectionPath())},
			Rationale: "whether the error body carries a detail naming what was wrong decides " +
				"whether later probes can attribute a rejection to a specific field",
		})

		return out, nil
	}

	out.Notes = append(out.Notes, Note{
		Resource: subj.Resource, Probe: p.Name(),
		Message: fmt.Sprintf("no candidate query parameter (%s) was validated, so no error "+
			"shape was observed without mutating anything",
			strings.Join(typedParamCandidates, ", ")),
	})

	return out, nil
}

func (p returnedOnReadWeak) Observe(
	ctx context.Context,
	s ReadSession,
	subj Subject,
) (Result, error) {
	var out Result

	listResp, err := get(ctx, s, p.Name(), s.CollectionPath(), nil)
	if err != nil {
		return out, err
	}
	out.Requests = 1

	if e := listResp.Error(); e.IsAuth() {
		return out, authError(e)
	}

	id := firstID(listResp.Items())
	if id == "" {
		out.Notes = append(out.Notes, Note{
			Resource: subj.Resource, Probe: p.Name(),
			Message: "no existing object to read, so no field presence was observed",
		})
		return out, nil
	}

	resp, err := get(ctx, s, p.Name(), s.ItemPath(id), nil)
	if err != nil {
		return out, err
	}
	out.Requests++

	if resp.Status < 200 || resp.Status >= 300 {
		out.Notes = append(out.Notes, Note{
			Resource: subj.Resource, Probe: p.Name(),
			Message: fmt.Sprintf("reading an existing object answered %d", resp.Status),
		})
		return out, nil
	}

	var absent []string
	for _, f := range subj.Fields {
		if _, present := resp.Field(f.JSONPath); !present {
			absent = append(absent, f.JSONPath)
		}
	}

	// Every fact from this probe is Suspected, and Suspected is never merged. An absent
	// field could mean "never returned", "null for this object" or "expansion-gated", and
	// nothing in a single read distinguishes them. The trustworthy version requires having
	// written the field first, which is what write.writable-returned does.
	for _, path := range absent {
		out.Facts = append(out.Facts, Fact{
			Resource:   subj.Resource,
			JSONPath:   path,
			Field:      FactReturnedOnRead,
			Value:      BoolValue(false),
			Confidence: Suspected,
			Probe:      p.Name(),
			Evidence:   []string{evidenceID(2, "GET", s.ItemPath(id))},
			Rationale:  "the field was absent from a read of one existing object",
			Alternatives: []string{
				"the field may be null for this particular object rather than never returned",
				"the field may be returned only when an expansion parameter is supplied",
				"the object may predate the field",
			},
		})
	}

	if len(absent) == 0 {
		out.Notes = append(out.Notes, Note{
			Resource: subj.Resource, Probe: p.Name(),
			Message: "every schema field was present on a read of one object",
		})
	}

	return out, nil
}

// firstID pulls an identifier out of a list response.
//
// "id" only, rather than guessing at alternatives. A wrong identifier would send later reads
// to the wrong object, and a probe that observed the wrong object would produce facts that
// are wrong rather than absent -- which is the failure this whole package is arranged to
// avoid.
func firstID(items []any) string {
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if v, present := obj["id"]; present {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
			// A numeric id is common and json.Unmarshal gives it as float64.
			if f, ok := v.(float64); ok {
				return fmt.Sprintf("%.0f", f)
			}
		}
	}

	return ""
}

// evidenceID reconstructs the cassette interaction id a probe's request will have.
//
// The sequence number is per-probe here, while the cassette numbers across the whole run --
// so these are approximate until the runner rewrites them against the real transcript. That
// rewrite is what makes the evidence citations exact; see attachEvidence.
func evidenceID(seq int, method, path string) string {
	return fmt.Sprintf("%03d-%s-%s", seq, strings.ToLower(method), pathSlug(path))
}

// pathSlug mirrors internal/cassette's slug so citations match the filenames.
func pathSlug(path string) string {
	var b strings.Builder

	lastDash := true

	for _, r := range strings.ToLower(strings.Trim(path, "/")) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}

	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "root"
	}

	const maxSlug = 48
	if len(out) > maxSlug {
		out = strings.Trim(out[:maxSlug], "-")
	}

	return out
}

// sameJSON compares two decoded values by their encodings.
func sameJSON(a, b any) bool {
	return fmt.Sprintf("%#v", a) == fmt.Sprintf("%#v", b)
}

// authError wraps a credential failure so the runner can stop the whole run.
//
// Every probe funnels a credential failure through here rather than formatting its own, so the
// runner's errors.Is check has exactly one thing to match and a probe cannot accidentally
// report one as an ordinary failure.
func authError(e apierr.Error) error {
	return fmt.Errorf("%w: %s", ErrAuth, e)
}

// get issues a read and wraps the transport error with the probe and path that caused it.
//
// A bare transport error says "connection refused" and nothing about which of a probe's several
// requests failed. Wrapping once here rather than at nine call sites keeps that context without
// nine near-identical fmt.Errorf calls.
func get(
	ctx context.Context,
	s ReadSession,
	probeName, path string,
	query url.Values,
) (*Response, error) {
	resp, err := s.Get(ctx, path, query)
	if err != nil {
		return nil, fmt.Errorf("%s: reading %s: %w", probeName, path, err)
	}
	return resp, nil
}
