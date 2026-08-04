package probe

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The sweeper is the obligation half of the prober: nothing may create an object it cannot
// guarantee to remove.
//
// # Two passes, because one is not enough
//
// The ledger pass deletes by identifier. It cannot be the whole story, because the failure that
// strands an object is a create whose response was never seen -- and such an intent has no
// identifier to delete by.
//
// The prefix pass covers exactly that gap. It reads the collection and deletes every object
// whose name field starts with the run's prefix. Bounded by the prefix, so it can never touch
// an object the prober did not make.
//
// Both passes are idempotent, which is what makes `-mode sweep` safe to run again after it
// fails halfway.
//
// # What it does not claim
//
// A single-page prefix read is not a complete enumeration of a collection, and paging is not
// generically implementable -- every API spells it differently. Rather than pretend, the
// sweeper reports what it can vouch for: a suspiciously round item count means the page was
// probably a limit rather than the whole collection, and Complete goes false. The gate's
// maxExistingObjects assertion is what makes a single-page sweep sound in the first place: a
// tenant small enough to pass it is a tenant whose collection fits in one page.

// SweepOptions is what a sweep needs.
type SweepOptions struct {
	// Session is the mutating session. A sweep writes -- it deletes -- so it needs one, and
	// needing one means it cannot run without a grant either.
	Session *MutatingSession

	// NamePrefix bounds the second pass. Empty is refused: a prefix sweep with no prefix
	// would delete a tenant.
	NamePrefix string
	// NameField is the JSON key to compare the prefix against.
	NameField string

	// PageParams lets an operator widen the collection read on an API whose default page is
	// small, e.g. "limit=1000". Offered rather than guessed at, because a wrong guess is a
	// 400 and a sweep that does nothing.
	PageParams url.Values

	// RetryDelay spaces the retries of a failed delete. Constant rather than jittered:
	// jitter would vary the number of requests a run makes, and a cassette is a strictly
	// ordered transcript.
	RetryDelay time.Duration

	// MaxSeconds bounds the sweep. Separate from the run's own deadline, because the
	// commonest reason to be sweeping is that the run's deadline expired.
	MaxSeconds int
}

const (
	// defaultSweepRetryDelay spaces a retried delete.
	defaultSweepRetryDelay = 2 * time.Second
	// sweepDeleteAttempts is the first attempt plus two retries.
	sweepDeleteAttempts = 3
	// defaultMaxSweepSeconds bounds a sweep.
	defaultMaxSweepSeconds = 120
)

// SweepSummary is what the sweep did, for the committed report.
type SweepSummary struct {
	// LedgerDeletes and PrefixDeletes are separated because they mean different things. A
	// prefix delete means something got past the ledger, which is worth knowing.
	LedgerDeletes int `json:"ledgerDeletes"`
	PrefixDeletes int `json:"prefixDeletes"`

	// Complete is false when the sweeper cannot vouch for having seen the whole collection.
	// Reported rather than asserted, because claiming a clean sweep it cannot demonstrate is
	// the one thing a sweeper must not do.
	Complete bool `json:"complete"`

	// Orphans are what is left. Non-empty means the run failed, whatever else it achieved.
	Orphans []Orphan `json:"orphans,omitempty"`

	Notes []Note `json:"notes,omitempty"`
}

// SweepContext detaches the sweep from the run's cancellation.
//
// Reusing the run's context would guarantee the orphans the sweeper exists to prevent: the
// commonest reason to sweep is that the run was cancelled or timed out, and a sweep inheriting
// that context would be dead before its first request. A one-liner whose being wrong is
// invisible in every green run, which is why it is named and separately tested.
func SweepContext(parent context.Context, maxSeconds int) (context.Context, context.CancelFunc) {
	if maxSeconds <= 0 {
		maxSeconds = defaultMaxSweepSeconds
	}

	return context.WithTimeout(
		context.WithoutCancel(parent),
		time.Duration(maxSeconds)*time.Second,
	)
}

// SweepRunOptions is what `probe sweep` needs.
//
// A separate entry point rather than an exported constructor, because newMutatingSession is
// unexported on purpose: a caller that could build one could write to an API without a grant.
// This is the seam that lets cmd sweep without opening that hole.
type SweepRunOptions struct {
	// Grant comes from AuthoriseSweep, which is the reduced gate. Required.
	Grant *Grant

	Subject Subject

	BaseURL string
	Token   string

	// Ledger is the previous run's ledger, opened for append so this sweep's deletes are
	// recorded too. A sweep is the continuation of a run that did not finish.
	Ledger *Ledger

	PageParams url.Values
	MaxSeconds int
	RetryDelay time.Duration
}

// RunSweep removes what a previous run left behind.
//
// Its own entry point rather than a mode of Run for two reasons: a sweep derives no facts, so
// everything Run does with reports and evidence is irrelevant to it; and it must be able to spend
// after the run's budget is exhausted, which is exactly the state it is usually called in.
func RunSweep(ctx context.Context, opts SweepRunOptions) (SweepSummary, error) {
	var summary SweepSummary

	if opts.Grant == nil {
		return summary, ErrNoGrant
	}
	if opts.Ledger == nil {
		return summary, fmt.Errorf("%w: a sweep needs a ledger", ErrLedger)
	}

	transport, _, err := TransportFor(ModeSweep, nil, nil, nil)
	if err != nil {
		return summary, err
	}

	live, err := newHTTPSession(SessionConfig{
		Transport:          transport,
		BaseURL:            opts.BaseURL,
		Token:              opts.Token,
		CollectionTemplate: opts.Subject.CollectionTemplate,
		ItemTemplate:       opts.Subject.ItemTemplate,
		// The reserve is what a sweep spends from, and it is derived from MaxCreates -- so a
		// sweep resuming a previous run needs the same cap that run had.
		Budget: Budget{MaxCreates: defaultMaxCreates, MaxRequests: 1},
	})
	if err != nil {
		return summary, err
	}

	ms, err := newMutatingSession(opts.Grant, live, MutationConfig{
		Ledger:    opts.Ledger,
		NameField: opts.Subject.NameField,
		IDField:   opts.Subject.IDField,
	})
	if err != nil {
		return summary, err
	}

	return Sweep(ctx, SweepOptions{
		Session:    ms,
		NamePrefix: opts.Grant.NamePrefix(),
		// The read field: the prefix pass matches against what the list response
		// actually says, and an API may rename between accepting a value and listing it.
		NameField:  opts.Subject.SweepNameField(),
		PageParams: opts.PageParams,
		RetryDelay: opts.RetryDelay,
		MaxSeconds: opts.MaxSeconds,
	})
}

// Sweep removes everything the run created.
//
// Returns ErrOrphans when anything is left, even if every fact was gathered: a run that leaves
// rubbish in somebody's tenant has failed at the thing that matters most.
func Sweep(ctx context.Context, opts SweepOptions) (SweepSummary, error) {
	summary := SweepSummary{Complete: true}

	if opts.Session == nil {
		return summary, fmt.Errorf("%w: a sweep needs a session", ErrInvalidPlan)
	}
	if opts.NamePrefix == "" {
		return summary, fmt.Errorf(
			"%w: a sweep needs a name prefix; without one the second pass would not be "+
				"bounded to objects this tool created", ErrInvalidPlan)
	}
	if opts.NameField == "" {
		return summary, fmt.Errorf("%w: a sweep needs the name field to compare the prefix "+
			"against", ErrInvalidPlan)
	}

	// The run's cap is spent by now, often deliberately. The sweep spends from its own
	// reserve, which is the only way "exceed the budget, then clean up, then exit 4" can be
	// implemented at all.
	opts.Session.live.budget.enterSweep()

	opts.sweepLedger(ctx, &summary)
	opts.sweepByPrefix(ctx, &summary)

	summary.Orphans = opts.orphansOf()
	sortOrphans(summary.Orphans)

	if len(summary.Orphans) > 0 {
		return summary, fmt.Errorf("%w: %d object(s) remain; see the orphan table",
			ErrOrphans, len(summary.Orphans))
	}

	return summary, nil
}

// sweepLedger is the first pass: delete by identifier.
func (opts SweepOptions) sweepLedger(ctx context.Context, summary *SweepSummary) {
	for _, o := range Unresolved(opts.Session.cfg.Ledger.Entries()) {
		if o.ID == "" {
			// Nothing to address. The prefix pass is this object's only chance, which is
			// what that pass is for.
			continue
		}

		if opts.deleteWithRetries(ctx, o.Probe, o.ID) {
			summary.LedgerDeletes++
		}
	}
}

// sweepByPrefix is the second pass: delete by name.
//
// This is the pass that catches an object whose identifier was never learned. It is also the
// pass that catches an object a *previous* run stranded, which is why `-mode sweep` runs it
// even when the ledger reconciles cleanly.
func (opts SweepOptions) sweepByPrefix(ctx context.Context, summary *SweepSummary) {
	resp, err := opts.Session.Get(ctx, opts.Session.CollectionPath(), opts.PageParams)
	if err != nil {
		summary.Complete = false
		summary.Notes = append(summary.Notes, Note{
			Probe: "sweep.prefix",
			Message: fmt.Sprintf("the collection could not be read, so no object could be "+
				"swept by name prefix: %v", err),
		})

		return
	}

	items := resp.Items()

	// A round number is almost always a page limit rather than a collection size, and a
	// prefix sweep that saw only the first page has not seen everything it might need to
	// delete. Reported rather than worked around: paging is not generically implementable,
	// and an operator who knows their API can widen the read with PageParams.
	if isProbablyAPageLimit(len(items)) {
		summary.Complete = false
		summary.Notes = append(summary.Notes, Note{
			Probe: "sweep.prefix",
			Message: fmt.Sprintf("the collection read returned exactly %d items, which is far "+
				"more likely to be a page limit than a collection size; this sweep cannot "+
				"vouch for having seen the whole collection. Widen the read with PageParams "+
				"if this API pages.", len(items)),
		})
	}

	for _, found := range opts.matchingObjects(items) {
		if !opts.deleteWithRetries(ctx, "sweep.prefix", found.id) {
			continue
		}

		summary.PrefixDeletes++

		// Resolve by name, because this is the pass that cleans up an intent that never
		// learned an identifier -- and matching by identifier would leave exactly those
		// intents outstanding, reporting as orphans the objects this pass just removed.
		opts.Session.resolveByName(found.name, found.id)
	}

	// The third resolution: proven absence. An id-less intent is written before the
	// create is sent, and a create the API refused after that point -- a role create
	// answering 500 was the live case -- leaves an intent no pass above can touch: no
	// identifier for the ledger pass, no object for the prefix pass. When this read
	// demonstrably covered the whole collection, a stamped name that is not in it is an
	// object that does not exist, and holding the ledger dirty over it blocks every
	// future record for the sake of phantom debris.
	if summary.Complete {
		present := map[string]bool{}
		for _, item := range items {
			if obj, ok := item.(map[string]any); ok {
				if name, ok := obj[opts.NameField].(string); ok {
					present[name] = true
				}
			}
		}

		for _, o := range Unresolved(opts.Session.cfg.Ledger.Entries()) {
			if o.ID != "" || present[o.Name] || !strings.HasPrefix(o.Name, opts.NamePrefix) {
				continue
			}
			_ = opts.Session.cfg.Ledger.Resolve(o.Seq, EntryKindRejected, "", 0,
				"absent from a complete collection read: the create never took effect")
			summary.Notes = append(summary.Notes, Note{
				Probe: "sweep.prefix",
				Message: fmt.Sprintf("intent %q resolved as never created: it has no "+
					"identifier and a complete collection read does not contain it", o.Name),
			})
		}
	}
}

// found is one object the prefix pass matched.
type found struct {
	id   string
	name string
}

// matchingObjects finds the objects whose name carries the run's prefix.
//
// Sorted, so a sweep issues its deletes in the same order twice. Not for a cassette's sake -- a
// sweep is not recorded -- but because a sweep that fails halfway should fail the same way
// twice, which is what makes debugging one possible.
func (opts SweepOptions) matchingObjects(items []any) []found {
	var out []found

	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}

		name, ok := obj[opts.NameField].(string)
		if !ok || !strings.HasPrefix(name, opts.NamePrefix) {
			continue
		}

		if id := identifierIn(obj, opts.Session.cfg.IDField); id != "" {
			out = append(out, found{id: id, name: name})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })

	return out
}

// deleteWithRetries deletes one object and reports whether it is gone.
//
// A 404 is not taken at face value. On an eventually-consistent API it may mean the object is
// not visible yet rather than that it is absent, and treating that as success is how an orphan
// gets reported as cleaned up. So a 404 is confirmed by a collection read before it is
// believed.
func (opts SweepOptions) deleteWithRetries(ctx context.Context, probe, id string) bool {
	delay := opts.RetryDelay
	if delay <= 0 {
		delay = defaultSweepRetryDelay
	}

	for attempt := range sweepDeleteAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(delay):
			}
		}

		resp, err := opts.Session.Delete(ctx, probe, id)
		if err != nil {
			continue
		}

		switch {
		case resp.Status < 400:
			return true

		case resp.Status == http.StatusNotFound:
			if opts.confirmAbsent(ctx, id) {
				// Resolve the intent: the object is demonstrably not there, which is what
				// deleted means.
				_ = opts.Session.markDeleted(id, resp.Status)
				return true
			}
		}
	}

	return false
}

// confirmAbsent checks a 404 against the collection.
//
// The collection read rather than another item read, because an item endpoint that 404s for a
// visibility reason will keep doing so, whereas a list is the API's own answer to "what
// exists".
func (opts SweepOptions) confirmAbsent(ctx context.Context, id string) bool {
	resp, err := opts.Session.Get(ctx, opts.Session.CollectionPath(), opts.PageParams)
	if err != nil {
		// Unknown, so not confirmed. The object stays outstanding and is reported as an
		// orphan, which is the safe direction: a human checking on a deleted object costs a
		// minute, and an unreported live object costs whatever it costs.
		return false
	}

	for _, item := range resp.Items() {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if identifierIn(obj, opts.Session.cfg.IDField) == id {
			return false
		}
	}

	return true
}

// orphansOf builds the table a human has to act on.
func (opts SweepOptions) orphansOf() []Orphan {
	outstanding := Unresolved(opts.Session.cfg.Ledger.Entries())
	out := make([]Orphan, 0, len(outstanding))

	for _, o := range outstanding {
		orphan := Orphan{ID: o.ID, Path: o.Path, Probe: o.Probe}

		if o.ID != "" {
			orphan.Curl = opts.Session.curlDelete(o.ID)
		} else {
			// No identifier and the prefix pass did not find it either. All a human has is
			// the name, so give them that rather than a blank row.
			orphan.Path = fmt.Sprintf("%s (search for the name %q)", o.Path, o.Name)
		}

		out = append(out, orphan)
	}

	if len(out) == 0 {
		// nil rather than an empty slice, so a clean sweep serialises with no orphans key at
		// all instead of an empty array a reader has to interpret.
		return nil
	}

	return out
}

// curlDelete is the command a human runs to finish the job.
//
// It carries "$TFPFGEN_PROBE_TOKEN" and never a value. This string goes into a committed report
// and a CI step summary, and a token in either is a token that has to be rotated.
func (s *MutatingSession) curlDelete(id string) string {
	return fmt.Sprintf(
		`curl -X DELETE -H "Authorization: Bearer $%s" %s%s`,
		tokenEnvPlaceholder, s.live.base, s.ItemPath(id),
	)
}

// tokenEnvPlaceholder is the variable name printed in a curl line.
//
// A fixed name rather than the profile's own TokenEnv: the profile is not in the report, so a
// reader has nothing to resolve a custom name against, and the documented variable is the one
// they will have set anyway.
//
// names is the one thing that must never appear here.
//
//nolint:gosec // G101: this is an environment variable *name*, printed on purpose. The value it
const tokenEnvPlaceholder = "TFPFGEN_PROBE_TOKEN"

// identifierIn reads an identifier out of a list item.
func identifierIn(obj map[string]any, idField string) string {
	v, ok := fieldIn(obj, idField)
	if !ok {
		return ""
	}

	switch typed := v.(type) {
	case string:
		return typed
	case float64:
		return trimFloat(typed)
	default:
		return ""
	}
}

// trimFloat renders a JSON number as an identifier.
//
// Every JSON number decodes to float64, so an id of 42 arrives as 42.0 and the obvious
// formatting would send a DELETE to /things/42.000000.
func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// isProbablyAPageLimit reports whether an item count looks like a limit.
//
// A collection holding exactly 100 objects is possible; a page limit of 100 is likelier, and
// the cost of being wrong in this direction is a note rather than a deletion.
func isProbablyAPageLimit(n int) bool {
	for _, limit := range []int{50, 100, 200, 250, 500, 1000} {
		if n == limit {
			return true
		}
	}

	return false
}

func sortOrphans(o []Orphan) {
	sort.SliceStable(o, func(i, j int) bool {
		if o[i].ID != o[j].ID {
			return o[i].ID < o[j].ID
		}
		return o[i].Path < o[j].Path
	})
}
