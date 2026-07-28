package probe

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

// baseURLFor is the API root.
//
// In replay the host is irrelevant -- the cassette matches on path -- but a client still
// needs a syntactically valid URL, so a placeholder stands in. It is deliberately not a real
// host: if the deny transport ever failed to intercept, a request to "replay.invalid" fails
// DNS rather than reaching somebody's API.
func baseURLFor(opts RunOptions) string {
	if opts.Mode == ModeRecord {
		return opts.BaseURL
	}
	return "https://replay.invalid"
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
// Matching is by method and path slug, in order, which is enough because a probe's requests
// appear in the transcript in the order it made them. A citation with no match is dropped and
// the fact is downgraded rather than left pointing at a file that does not exist: an
// unverifiable fact is worse than a weaker one, because it looks checkable and is not.
func attachEvidence(report *Report, transcript []cassette.Interaction) {
	if len(transcript) == 0 {
		return
	}

	// Every real id, indexed by the suffix a probe would have guessed.
	bySuffix := map[string][]string{}
	for _, i := range transcript {
		suffix := idSuffix(i.ID)
		bySuffix[suffix] = append(bySuffix[suffix], i.ID)
	}

	for idx := range report.Facts {
		fact := &report.Facts[idx]

		var resolved []string
		for _, cited := range fact.Evidence {
			candidates := bySuffix[idSuffix(cited)]
			if len(candidates) == 0 {
				continue
			}
			// The first matching interaction. Duplicates arise when a probe reads the same
			// path repeatedly, and citing them all would be more honest but makes every
			// volatility fact cite every read in the run.
			resolved = appendUnique(resolved, candidates...)
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

// idSuffix strips the sequence prefix from an interaction id, leaving method and path.
func idSuffix(id string) string {
	// "004-post-v7-tags" -> "post-v7-tags"
	for i, r := range id {
		if r == '-' {
			return id[i+1:]
		}
	}
	return id
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
