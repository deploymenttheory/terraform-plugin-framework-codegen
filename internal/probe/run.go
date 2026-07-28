package probe

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
)

// Mode is what a run does.
type Mode string

const (
	// ModeRecord issues real requests and records them.
	ModeRecord Mode = "record"
	// ModeReplay re-derives facts from a committed cassette with the network denied.
	ModeReplay Mode = "replay"
	// ModeVerify replays and then compares the derived facts against the committed ones.
	ModeVerify Mode = "verify"
	// ModeSweep removes objects a previous run left behind.
	ModeSweep Mode = "sweep"
)

// RunOptions is what a run needs.
type RunOptions struct {
	Mode    Mode
	Subject Subject
	Plan    Plan

	// Only restricts the run to one probe by name.
	Only string

	// BaseURL and Token are needed only for ModeRecord.
	BaseURL string
	Token   string

	// Interactions is the committed transcript, for replay and verify.
	Interactions []cassette.Interaction

	// Redactor and Secrets are needed only for ModeRecord.
	Redactor *cassette.Redactor
	Secrets  map[string]string

	// AllowMutations gates the mutating tier. Carried here so the runner can report that
	// the tier was skipped rather than silently omitting it.
	AllowMutations bool
	// Grant is proof the gate passed. Without one, mutating probes do not run whatever
	// AllowMutations says -- the flag is a request, the grant is the authorisation.
	Grant *Grant
}

// RunResult is what a run produced.
type RunResult struct {
	Report Report
	// Interactions is what was recorded, empty in replay.
	Interactions []cassette.Interaction
}

// Run executes the applicable probes and assembles a report.
//
// Read-only probes always run. Mutating probes run only with a Grant, and are otherwise
// reported as skipped with the reason -- silently omitting them would make a read-only run
// look like a complete one.
func Run(ctx context.Context, opts RunOptions) (RunResult, error) {
	var out RunResult

	transport, recorder, err := TransportFor(
		opts.Mode,
		opts.Interactions,
		opts.Redactor,
		opts.Secrets,
	)
	if err != nil {
		return out, err
	}

	session, err := newHTTPSession(SessionConfig{
		Transport:          transport,
		BaseURL:            baseURLFor(opts),
		Token:              opts.Token,
		CollectionTemplate: opts.Subject.CollectionTemplate,
		ItemTemplate:       opts.Subject.ItemTemplate,
		Budget:             opts.Plan.Budget,
	})
	if err != nil {
		return out, err
	}

	out.Report.Profile = ProfileSummary{
		Host:    hostOf(baseURLFor(opts)),
		Mode:    string(opts.Mode),
		Sandbox: opts.Grant != nil,
	}

	runReadProbes(ctx, session, opts, &out.Report)
	reportSkippedMutating(opts, &out.Report)

	out.Report.Budget = session.budget.report()

	if recorder != nil {
		recorded, err := recorder.Interactions()
		if err != nil {
			// A redaction failure fails the run and writes nothing. Returned rather than
			// noted, because the whole point is that the caller cannot proceed to write.
			return out, fmt.Errorf("collecting the recorded interactions: %w", err)
		}
		out.Interactions = recorded
	}

	// Citations are rewritten against the real transcript, because a probe cannot know its
	// own position in a run that interleaves several. An uncorrected citation would name a
	// file that does not exist, which would make every fact unverifiable.
	transcript := out.Interactions
	if len(transcript) == 0 {
		transcript = opts.Interactions
	}
	attachEvidence(&out.Report, transcript)

	out.Report.Sort()

	return out, nil
}

// replayHost stands in for the real host during replay.
//
// Deliberately an unresolvable name: if the deny transport ever failed to intercept, a request to
// .invalid fails DNS rather than reaching somebody's API.
const replayHost = "https://replay.invalid"

// baseURLFor is the API root.
//
// In replay the *host* is irrelevant -- the cassette matches on path -- but the base **path** is
// not. A cassette stores full request paths, so replaying a recording made against an endpoint
// with a prefix has to reproduce that prefix or every request mismatches. The caller supplies it
// from the cassette's metadata.
func baseURLFor(opts RunOptions) string {
	if opts.Mode == ModeRecord {
		return opts.BaseURL
	}
	if opts.BaseURL != "" {
		return opts.BaseURL
	}
	return replayHost
}

func hostOf(baseURL string) string {
	trimmed := baseURL
	for _, prefix := range []string{"https://", "http://"} {
		if len(trimmed) > len(prefix) && trimmed[:len(prefix)] == prefix {
			trimmed = trimmed[len(prefix):]
			break
		}
	}
	for i, r := range trimmed {
		if r == '/' {
			return trimmed[:i]
		}
	}
	return trimmed
}

func runReadProbes(ctx context.Context, s ReadSession, opts RunOptions, report *Report) {
	for _, p := range ReadProbes(opts.Only) {
		outcome := ProbeOutcome{Name: p.Name(), Kind: KindRead}

		result, err := p.Observe(ctx, s, opts.Subject)

		outcome.Requests = result.Requests
		outcome.Facts = len(result.Facts)

		switch {
		case err == nil:
			outcome.Status = "ok"
			if len(result.Facts) == 0 {
				// A probe that ran and concluded nothing is a legitimate and common
				// outcome -- an empty tenant, an ambiguous observation. Distinguished from
				// "ok with facts" so a reader can see which probes actually contributed.
				outcome.Status = "abandoned"
				outcome.Reason = "ran but established nothing"
			}

		case errors.Is(err, errNotImplemented):
			outcome.Status = "skipped"
			outcome.Reason = "not implemented yet"

		case errors.Is(err, ErrBudget):
			outcome.Status = "failed"
			outcome.Reason = err.Error()
			report.Probes = append(report.Probes, outcome)
			report.Facts = append(report.Facts, result.Facts...)
			report.Notes = append(report.Notes, result.Notes...)
			// The budget is a hard stop: continuing would spend past a cap somebody set
			// deliberately.
			report.Notes = append(report.Notes, Note{
				Resource: opts.Subject.Resource,
				Message:  "the run stopped early: " + err.Error(),
			})
			return

		case errors.Is(err, ErrAuth):
			outcome.Status = "failed"
			outcome.Reason = err.Error()
			report.Probes = append(report.Probes, outcome)
			// Also a hard stop, and for a subtler reason: every later response would be
			// about the credential, and a probe that carried on would attribute an
			// authentication failure to whichever field it happened to be testing.
			report.Notes = append(report.Notes, Note{
				Resource: opts.Subject.Resource,
				Message:  "the run stopped: " + err.Error(),
			})
			return

		default:
			outcome.Status = "failed"
			outcome.Reason = err.Error()
		}

		report.Probes = append(report.Probes, outcome)
		report.Facts = append(report.Facts, result.Facts...)
		report.Notes = append(report.Notes, result.Notes...)
	}
}

// reportSkippedMutating records why the mutating tier did not run.
//
// Explicitly, rather than by omission. A read-only run and a full run must not produce
// reports that look the same, or a reader has no way to tell that two thirds of the
// catalogue never executed.
func reportSkippedMutating(opts RunOptions, report *Report) {
	if opts.Grant != nil {
		return
	}

	reason := "no grant: mutating probes need -mode record, --allow-mutations and a sandbox profile"
	if can, why := opts.Subject.CanMutate(); !can {
		reason = why
	}

	for _, p := range MutatingProbes(opts.Only) {
		report.Probes = append(report.Probes, ProbeOutcome{
			Name:   p.Name(),
			Kind:   KindMutating,
			Status: "skipped",
			Reason: reason,
		})
	}
}

// attachEvidence rewrites each fact's citations against the real transcript.
//
// A probe cites an interaction by the id it *expects*, numbering from one within its own
// sequence -- because a probe cannot know where its requests will land in a run that
// interleaves several. This maps those onto the ids the cassette actually assigned.
//
// Matching is by method plus a *suffix* match on the path slug, not equality. That is not
// laxness -- it is the only thing that works. A probe cites the path it asked for, which is
// relative to the session's base URL; the cassette records the full request path. When the base
// URL carries a prefix -- "https://api.example.com/v7" with a path of "/tags" -- the two differ
// by exactly that prefix, and equality matching downgrades every fact in the run to Suspected.
//
// This was found against a live API rather than in a test: an httptest server has no base path,
// so the prefix is empty and equality happens to work. Worth stating, because the failure was
// silent and total -- correct facts, all quietly marked unverifiable.
//
// A citation with no match is dropped and the fact downgraded rather than left pointing at a
// file that does not exist: an unverifiable fact is worse than a weaker one, because it looks
// checkable and is not.
func attachEvidence(report *Report, transcript []cassette.Interaction) {
	if len(transcript) == 0 {
		return
	}

	for idx := range report.Facts {
		fact := &report.Facts[idx]

		var resolved []string
		for _, cited := range fact.Evidence {
			// Duplicates arise when a probe reads the same path repeatedly. Citing them all
			// would be more honest but makes every volatility fact cite every read in the run.
			resolved = appendUnique(resolved, matchingInteractions(transcript, cited)...)
		}

		if len(resolved) == 0 {
			// No citation could be resolved, so this fact cannot be checked against the
			// transcript. Downgraded rather than dropped, so the observation survives for a
			// human while being ineligible for merge.
			fact.Evidence = []string{}
			if fact.Confidence.AtLeast(Observed) {
				fact.Confidence = Suspected
			}
			fact.Alternatives = append(fact.Alternatives,
				"no cassette interaction could be matched to this observation, so it cannot be "+
					"re-derived offline")
			continue
		}

		sort.Strings(resolved)
		fact.Evidence = resolved
	}
}

// matchingInteractions returns the recorded ids a citation could refer to.
//
// Method must match exactly. The path slug matches when the recorded one *ends with* the cited
// one, which absorbs a base-URL prefix without letting "/tags" match "/other-tags": the boundary
// is checked so a suffix has to begin at a segment separator.
func matchingInteractions(transcript []cassette.Interaction, cited string) []string {
	citedMethod, citedPath := splitID(cited)
	if citedMethod == "" {
		return nil
	}

	var out []string

	for _, i := range transcript {
		method, path := splitID(i.ID)
		if method != citedMethod {
			continue
		}
		if pathMatches(path, citedPath) {
			out = append(out, i.ID)
		}
	}

	return out
}

// splitID pulls the method and path slug out of "004-post-v7-tags".
func splitID(id string) (method, path string) {
	rest := id
	// Drop the numeric sequence prefix.
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		rest = rest[i+1:]
	}

	i := strings.IndexByte(rest, '-')
	if i < 0 {
		return rest, ""
	}

	return rest[:i], rest[i+1:]
}

// pathMatches reports whether recorded ends with cited at a segment boundary.
//
// The boundary check is what stops a cited "tags" matching a recorded "other-tags", which would
// attach a fact to evidence about a different endpoint -- a worse failure than no citation at
// all, because it would look checked.
func pathMatches(recorded, cited string) bool {
	switch {
	case cited == "":
		return recorded == ""
	case recorded == cited:
		return true
	case len(recorded) <= len(cited):
		return false
	default:
		return strings.HasSuffix(recorded, "-"+cited)
	}
}

func appendUnique(dst []string, values ...string) []string {
	for _, v := range values {
		found := false
		for _, existing := range dst {
			if existing == v {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, v)
		}
	}
	return dst
}

// VerifyFacts compares freshly derived facts against committed ones.
//
// This is the purity test. Replaying a committed transcript must produce exactly the facts
// that were committed with it; if it does not, derivation depends on something outside the
// transcript -- a clock, a random value, an environment variable -- and every fact in the
// store is then unreproducible.
func VerifyFacts(derived, committed []Fact) error {
	SortFacts(derived)
	SortFacts(committed)

	if len(derived) != len(committed) {
		return fmt.Errorf("%w: replay derived %d fact(s), the cassette was committed with %d",
			ErrReplayMismatch, len(derived), len(committed))
	}

	for i := range derived {
		got, want := derived[i], committed[i]

		if got.Resource != want.Resource || got.JSONPath != want.JSONPath ||
			got.Field != want.Field {
			return fmt.Errorf("%w: fact %d is %s, expected %s",
				ErrReplayMismatch, i, factKey(got), factKey(want))
		}
		if got.Value.String() != want.Value.String() {
			return fmt.Errorf("%w: %s = %s, the cassette was committed with %s",
				ErrReplayMismatch, factKey(got), got.Value, want.Value)
		}
		if got.Confidence != want.Confidence {
			return fmt.Errorf("%w: %s has confidence %s, the cassette was committed with %s",
				ErrReplayMismatch, factKey(got), got.Confidence, want.Confidence)
		}
	}

	return nil
}

func factKey(f Fact) string {
	if f.JSONPath == "" {
		return fmt.Sprintf("%s.%s", f.Resource, f.Field)
	}
	return fmt.Sprintf("%s.%s.%s", f.Resource, f.JSONPath, f.Field)
}

// RecordingMetadata builds the cassette metadata for a run.
func RecordingMetadata(provider string, subj Subject, host, probeVersion string) cassette.Metadata {
	return cassette.Metadata{
		Provider:     provider,
		Resource:     subj.Resource,
		Host:         host,
		ProbeVersion: probeVersion,
	}
}

// DeadlineFor derives the run's context deadline from the plan's wall-clock cap.
func DeadlineFor(ctx context.Context, b Budget) (context.Context, context.CancelFunc) {
	limits := b.WithDefaults()
	return context.WithTimeout(ctx, time.Duration(limits.MaxWallClockSeconds)*time.Second)
}
