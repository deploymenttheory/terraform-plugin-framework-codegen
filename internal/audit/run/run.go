// Package run executes an audit plan against a live API. It is the only
// code in the toolkit that touches a network with credentials, and every
// piece of it is shaped by that: a host allowlist derived from the base
// URL sits in front of every mutating request, a foreign-object pre-flight
// refuses a tenant that does not look like a sandbox, every created object
// is recorded in a durable activity ledger before the request that creates
// it is sent, budgets bound what a run may spend, and everything written
// or logged passes through redaction first.
//
// The audit creates and deletes real objects in the tenant the base URL
// points at. Run it only against sandbox or non-production tenants; the
// toolkit does not police this — it is the operator's responsibility.
//
// Run walks the plan's entities in order, executes each entity's step
// sequence over HTTP, and derives observations from the responses — the
// step kinds say what each step can learn (see internal/audit/plan). A
// precondition that fails blocks the entity, never the run; a budget that
// runs out records timeoutExhausted and moves on. Cleanup runs at both
// boundaries and is callable standalone, so no invocation path can create
// an object without also owning its removal.
package run

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// Auth names how requests authenticate. Secret values are never carried
// here — they are read from the environment through Options.Lookup, under
// the fixed TFPFGEN_AUTH_* role names internal/config owns.
type Auth struct {
	// Method is one of the config auth methods: bearer_token,
	// api_key_header, basic, oauth2_client_credentials, github_app.
	Method string
	// APIKeyHeader is the header name an api_key_header method sends the
	// token under.
	APIKeyHeader string
	// TokenURL is the oauth2_client_credentials token endpoint.
	TokenURL string
}

// Budgets bounds one run. A zero field takes its value from the plan's own
// budget, which derived it from the config.
type Budgets struct {
	// Requests is the run-wide request ceiling.
	Requests int
	// Objects is the ceiling on simultaneously live created objects, and
	// doubles as the shared-tenant threshold: a collection already holding
	// more foreign objects than this is not a sandbox.
	Objects int
	// Duration is the wall-clock allowance.
	Duration time.Duration
}

// Options is everything Run needs.
type Options struct {
	Plan *plan.Plan
	// Doc and Config, when both set, make the run strategy-driven: each
	// entity's uniform plan steps are replaced by the program its compiled
	// strategy.Strategy describes, under a complexity-scaled per-entity
	// budget. When Doc is nil the plan is executed as given — the path the
	// executor's own unit tests take.
	Doc     *specmodel.Document
	Config  *config.Config
	BaseURL string
	Auth    Auth
	// NamePrefix marks every created object's name-bearing fields and
	// bounds every prefix pass. It must be at least minPrefixChars long
	// and contain "tfpfgen", so a cleanup pass can never match an object the
	// audit did not make.
	NamePrefix string
	Budgets    Budgets
	// RateLimitRPS paces every request through a token bucket. Zero means
	// the config default of 2.
	RateLimitRPS int
	// RequestTimeout bounds one HTTP request. Zero means 15s.
	RequestTimeout time.Duration
	// RunsDir holds the audit activity ledgers. Empty refuses: a mutating
	// run without a durable ledger cannot promise cleanup after a crash.
	RunsDir string
	// SpecHash stamps every observation with the pinned document hash it
	// was observed against.
	SpecHash string
	// ForceAPIAudit skips the pre-flight foreign-object refusal: proceed
	// despite foreign objects beyond the object budget.
	ForceAPIAudit bool
	Logger        zerolog.Logger
	// Lookup reads the environment: secrets and ${VAR} inputs. Nil means
	// os.LookupEnv.
	Lookup func(string) (string, bool)
	// RunID overrides the generated run id. Leave empty outside tests.
	RunID string

	// beforeSend, when set, is called after the ledger write and
	// immediately before each request is sent. Test hook.
	beforeSend func(*http.Request)
}

// minPrefixChars bounds NamePrefix from below: every prefix pass deletes
// whatever matches it, and a short prefix widens that to objects the audit
// never made.
const minPrefixChars = 6

// requiredPrefixToken must appear in NamePrefix, so anybody who finds an
// audit object in a UI can tell what made it.
const requiredPrefixToken = "tfpfgen"

const (
	defaultRequestTimeout = 15 * time.Second
	defaultRateLimitRPS   = 2
	// cleanupAllowance bounds the end-of-run cleanup, which must be able
	// to spend after every run budget is exhausted — that is the state it
	// is most needed in.
	cleanupAllowance = 120 * time.Second
)

// Entity statuses a Summary reports.
const (
	StatusAudited          = "audited"
	StatusBlocked          = "blocked"
	StatusTimeoutExhausted = "timeoutExhausted"
)

// EntityResult is how far one entity got.
type EntityResult struct {
	Entity string `json:"entity"`
	Status string `json:"status"`
	// Reason is set when the status is not audited.
	Reason string `json:"reason,omitempty"`
}

// Summary is what a run did, for the operator's table.
type Summary struct {
	RunID    string         `json:"runId"`
	Entities []EntityResult `json:"entities"`
	// Audited, Blocked and TimedOut count entities by status; Skipped
	// counts the entities the plan itself left out.
	Audited  int `json:"audited"`
	Blocked  int `json:"blocked"`
	TimedOut int `json:"timedOut"`
	Skipped  int `json:"skipped"`
	// SkippedEntities is every entity the plan left out, with its reason.
	// Carried because the count alone cannot be acted on: it does not
	// distinguish a run that covered the API from one that skipped most of it.
	SkippedEntities []plan.Skipped `json:"skippedEntities,omitempty"`
	// ByKind and ByOutcome count observations.
	ByKind    map[observe.Kind]int    `json:"byKind"`
	ByOutcome map[observe.Outcome]int `json:"byOutcome"`
	// Budget usage.
	Requests       int           `json:"requests"`
	RequestBudget  int           `json:"requestBudget"`
	ObjectsCreated int           `json:"objectsCreated"`
	ObjectBudget   int           `json:"objectBudget"`
	Elapsed        time.Duration `json:"elapsed"`
	DurationBudget time.Duration `json:"durationBudget"`
	// Cleanup results at the two run boundaries.
	CleanupStart CleanupSummary `json:"cleanupStart"`
	CleanupEnd   CleanupSummary `json:"cleanupEnd"`
	// RejectsUnknownFields records the undeclaredSpecField probe's finding
	// per entity: true when the API rejected a body field no schema
	// declares, false when it accepted and ignored it. When true, that
	// entity's refusal-based findings need caution — a refusal may have
	// been about the unknown field, not the claim under test. Recorded
	// here rather than as an observation because it is about how to read
	// the other findings, not a finding itself.
	RejectsUnknownFields map[string]bool `json:"rejectsUnknownFields,omitempty"`
	// Adjustments is every change the adaptive loop made to a body to get it
	// accepted — a field added, removed, borrowed, or added because another
	// required it. It is the raw signal the triangulating inference folds into
	// conditional-edge findings; the required-field adds already surface as
	// requiredByAPI or requiredWhen observations here.
	Adjustments []infer.RequestAdjustment `json:"adjustments,omitempty"`
	// EdgesConfirmed and EdgesInconclusive count the conditional-edge
	// observations the inference produced — validConfiguration, validWhen,
	// dependsOn and mutuallyExclusive — by outcome, so the run table can say
	// how many edges were asserted versus tested-but-unconfirmed.
	EdgesConfirmed    int `json:"edgesConfirmed"`
	EdgesInconclusive int `json:"edgesInconclusive"`
	// RateLimited counts every rate-limit refusal the run met, and Slowdowns
	// how many times it halved its own rate in answer. Both belong on the
	// summary because a throttled run explains itself: an entity that came
	// back thin after fifty refusals was not measuring a quiet API, and
	// reading its findings as though it were is the mistake this records
	// against. RateLimitRPS is the rate the run finished on, which is the
	// configured rate only when nothing forced it down.
	RateLimited  int `json:"rateLimited"`
	Slowdowns    int `json:"slowdowns"`
	RateLimitRPS int `json:"rateLimitRps"`
}

// Run executes the plan and returns every observation it derived, however
// far it got. The error reports a run that could not be attempted — bad
// options, a cancelled context — never a budget or a misbehaving entity,
// which are recorded and moved past.
//
// A mutating plan creates and deletes real objects in the tenant. Point
// the run only at sandbox or non-production tenants — the toolkit does
// not police this; it is the operator's responsibility.
func Run(ctx context.Context, opts Options) ([]observe.Observation, Summary, error) {
	var hints map[string]map[string]strategy.SynthHint
	var strategies map[string]*strategy.Strategy
	if opts.Plan != nil && opts.Doc != nil && opts.Config != nil {
		opts.Plan, hints, strategies = strategize(opts.Plan, opts.Doc, opts.Config, opts.NamePrefix)
	}
	r, err := newRunner(opts)
	if err != nil {
		return nil, Summary{}, err
	}
	r.hints = hints
	r.strategies = strategies
	defer r.ledger.close()

	r.started = time.Now()
	r.deadline = r.started.Add(r.budget.Duration)

	r.summary.CleanupStart = r.cleanupDebris(ctx)

	for i := range opts.Plan.Entities {
		if ctx.Err() != nil {
			break
		}
		r.runEntity(ctx, &opts.Plan.Entities[i])
	}

	// The triangulating inference runs after every entity, over all of the
	// run's evidence at once: the conditional edges no single probe could
	// justify are asserted here, or reported inconclusive, never guessed.
	r.inferEdges()

	// End-of-run cleanup is detached from the run's cancellation and its
	// budgets: the commonest reason to be cleaning up is that one of them
	// ran out.
	cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupAllowance)
	defer cancel()
	r.inCleanup = true
	r.summary.CleanupEnd = r.cleanupOwn(cleanCtx)

	obs := r.finishObservations()
	r.finishSummary(obs)

	if ctx.Err() != nil {
		return obs, r.summary, fmt.Errorf("the audit run was cancelled: %w", ctx.Err())
	}
	return obs, r.summary, nil
}

// runner carries one run's state.
type runner struct {
	opts   Options
	log    zerolog.Logger
	client *http.Client
	auth   authenticator
	bucket *bucket
	// backoff answers a rate-limit refusal: wait, retry, and slow the run
	// down for good. See backoff.go.
	backoff *backoff
	ledger  *activityLedger
	base    *url.URL
	runID   string
	budget  Budgets

	started  time.Time
	deadline time.Time
	// reqTotal counts every request the run makes; runOut is set when a
	// run-wide budget is exhausted, with the reason.
	reqTotal int
	runOut   string
	// inCleanup exempts the boundary cleanups from the run budgets.
	inCleanup bool

	// registry maps an entity to its live minimal object, for
	// $created:<entity> resolution.
	registry map[string]*createdObject
	// recipes remembers how each executed entity creates and addresses
	// its objects, for re-creation and end-of-run deletion.
	recipes map[string]*entityRecipe
	// hints carries, per entity, the field synthesis material the adjustment
	// loop draws on when it must add a field a refusal named. Nil on a
	// non-strategy run.
	hints map[string]map[string]strategy.SynthHint
	// strategies carries each entity's compiled strategy, so the inference
	// can read the hypotheses the run was meant to confirm. Nil on a
	// non-strategy run, which is what makes such a run skip inference.
	strategies map[string]*strategy.Strategy
	// evidence accumulates, per entity, the raw record the inference reads —
	// accepted bodies, forced adjustments, list responses.
	evidence map[string]*infer.Evidence
	// borrowed caches one real id per collection the run has borrowed a
	// reference from, so a second create needing it costs no extra request.
	borrowed map[string]string
	// adjustments accumulates every change the adaptive loop made, for the
	// triangulating inference and the run summary.
	adjustments []infer.RequestAdjustment

	obs     []observe.Observation
	summary Summary
}

// createdObject is one live object the run made.
type createdObject struct {
	entity string
	id     string
	seq    int
}

// entityRecipe is what re-creation and deletion need to know about an
// entity after its plan has executed.
type entityRecipe struct {
	entity           string
	collectionPath   string
	collectionValues map[string]string
	itemPath         string
	itemValues       map[string]string
	createMethod     string
	deleteMethod     string
	minimalBody      map[string]any
}

func newRunner(opts Options) (*runner, error) {
	if opts.Plan == nil {
		return nil, fmt.Errorf("audit run: no plan was given")
	}
	if opts.Lookup == nil {
		opts.Lookup = os.LookupEnv
	}
	base, err := url.Parse(opts.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("audit run: base URL %q is not an absolute URL", opts.BaseURL)
	}
	if err := checkPrefix(opts.NamePrefix); err != nil {
		return nil, err
	}
	if opts.RunsDir == "" && planMutates(opts.Plan) {
		return nil, fmt.Errorf("audit run: no runs directory for the activity ledger; a mutating run without a durable ledger cannot promise cleanup after a crash")
	}

	auth, err := newAuthenticator(opts.Auth, opts.Lookup, opts.RequestTimeoutOrDefault())
	if err != nil {
		return nil, err
	}

	budget := opts.Budgets
	if budget.Requests == 0 {
		budget.Requests = opts.Plan.Budget.Requests
	}
	if budget.Objects == 0 {
		budget.Objects = opts.Plan.Budget.Objects
	}
	if budget.Duration == 0 {
		if d, err := time.ParseDuration(opts.Plan.Budget.Duration); err == nil {
			budget.Duration = d
		}
	}
	if budget.Duration == 0 {
		budget.Duration = 10 * time.Minute
	}

	rps := opts.RateLimitRPS
	if rps <= 0 {
		rps = defaultRateLimitRPS
	}

	runID := opts.RunID
	if runID == "" {
		runID = newRunID()
	}

	led, err := openActivityLedger(opts.RunsDir, runID)
	if err != nil {
		return nil, err
	}

	r := &runner{
		opts:     opts,
		log:      opts.Logger.With().Str("runId", runID).Logger(),
		client:   &http.Client{Transport: newTransport()},
		auth:     auth,
		bucket:   newBucket(rps),
		backoff:  newBackoff(),
		ledger:   led,
		base:     base,
		runID:    runID,
		budget:   budget,
		registry: map[string]*createdObject{},
		recipes:  map[string]*entityRecipe{},
		evidence: map[string]*infer.Evidence{},
		borrowed: map[string]string{},
		summary: Summary{
			RunID:                runID,
			ByKind:               map[observe.Kind]int{},
			ByOutcome:            map[observe.Outcome]int{},
			RejectsUnknownFields: map[string]bool{},
		},
	}
	return r, nil
}

// RequestTimeoutOrDefault is the per-request ceiling in force.
func (o Options) RequestTimeoutOrDefault() time.Duration {
	if o.RequestTimeout > 0 {
		return o.RequestTimeout
	}
	return defaultRequestTimeout
}

// checkPrefix refuses a prefix too weak to bound a cleanup pass.
func checkPrefix(prefix string) error {
	switch {
	case len(prefix) < minPrefixChars:
		return fmt.Errorf("audit run: name prefix %q is shorter than %d characters; every prefix pass deletes whatever matches it, and a short prefix widens that beyond the audit's own objects", prefix, minPrefixChars)
	case !strings.Contains(prefix, requiredPrefixToken):
		return fmt.Errorf("audit run: name prefix %q does not contain %q; anybody who finds an audit object in a UI needs to be able to tell what made it", prefix, requiredPrefixToken)
	}
	return nil
}

// planMutates reports whether any planned step sends a mutating method.
func planMutates(p *plan.Plan) bool {
	for _, ep := range p.Entities {
		for _, s := range ep.Steps {
			if mutatingMethod(s.Method) {
				return true
			}
		}
	}
	return false
}

func mutatingMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// newRunID generates the 8-character lowercase id that names this run's
// objects.
func newRunID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	for i, b := range raw {
		raw[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(raw)
}

// record adds one observation, stamped with the run's identity.
func (r *runner) record(entity, attribute string, kind observe.Kind, value any, cond *observe.Condition, outcome observe.Outcome, excerpts ...observe.Excerpt) {
	red := make([]observe.Excerpt, 0, len(excerpts))
	for _, e := range excerpts {
		red = append(red, observe.Redact(e, r.secretsNow()))
	}
	r.obs = append(r.obs, observe.Observation{
		ID:         observe.ComputeID(entity, attribute, kind, cond),
		Entity:     entity,
		Attribute:  attribute,
		Kind:       kind,
		Value:      value,
		Condition:  cond,
		Outcome:    outcome,
		Excerpts:   red,
		RunID:      r.runID,
		SpecHash:   r.opts.SpecHash,
		ObservedAt: time.Now().UTC(),
	})
}

// outcomeRank orders outcomes by how much they know, for deduplication.
func outcomeRank(o observe.Outcome) int {
	switch o {
	case observe.OutcomeConfirmed:
		return 3
	case observe.OutcomeInconclusive:
		return 2
	case observe.OutcomeBlocked:
		return 1
	default:
		return 0
	}
}

// finishObservations deduplicates by ID, keeping the most-informed outcome:
// a claim confirmed by one step is not re-listed as blocked because a later
// step could not re-earn it. On equal outcomes the record with excerpt proof
// wins over one without — a per-probe finding keeps its evidence where the
// inference derived the same claim without an excerpt — and otherwise the
// later record wins, the finalize-time conclusions being the canonical
// evidence for a per-field claim.
func (r *runner) finishObservations() []observe.Observation {
	best := map[string]int{}
	for i, o := range r.obs {
		if j, ok := best[o.ID]; !ok || moreInformed(o, r.obs[j]) {
			best[o.ID] = i
		}
	}
	idx := make([]int, 0, len(best))
	for _, i := range best {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	out := make([]observe.Observation, 0, len(idx))
	for _, i := range idx {
		out = append(out, r.obs[i])
	}
	observe.Sort(out)
	return out
}

// moreInformed reports whether observation a should displace b when the two
// share an ID: a stronger outcome wins; on equal outcome the one carrying
// excerpt proof wins, and failing that the later (a).
func moreInformed(a, b observe.Observation) bool {
	ra, rb := outcomeRank(a.Outcome), outcomeRank(b.Outcome)
	if ra != rb {
		return ra > rb
	}
	if len(a.Excerpts) != len(b.Excerpts) {
		return len(a.Excerpts) > len(b.Excerpts)
	}
	return true
}

// finishSummary fills the counters a table prints.
func (r *runner) finishSummary(obs []observe.Observation) {
	for _, o := range obs {
		r.summary.ByKind[o.Kind]++
		r.summary.ByOutcome[o.Outcome]++
		if infer.EdgeKinds[o.Kind] {
			switch o.Outcome {
			case observe.OutcomeConfirmed:
				r.summary.EdgesConfirmed++
			case observe.OutcomeInconclusive:
				r.summary.EdgesInconclusive++
			}
		}
	}
	for _, e := range r.summary.Entities {
		switch e.Status {
		case StatusAudited:
			r.summary.Audited++
		case StatusBlocked:
			r.summary.Blocked++
		case StatusTimeoutExhausted:
			r.summary.TimedOut++
		}
	}
	r.summary.Adjustments = sortedAdjustments(r.adjustments)
	r.summary.Skipped = len(r.opts.Plan.Skipped)
	r.summary.SkippedEntities = r.opts.Plan.Skipped
	r.summary.Requests = r.reqTotal
	r.summary.RequestBudget = r.budget.Requests
	r.summary.ObjectBudget = r.budget.Objects
	r.summary.Elapsed = time.Since(r.started).Round(time.Millisecond)
	r.summary.DurationBudget = r.budget.Duration
	r.summary.RateLimited, r.summary.Slowdowns = r.backoff.counts()
	r.summary.RateLimitRPS = r.bucket.rate()
}

// newTransport gives a runner's client its own connection pool. The
// default transport is process-wide, and anything that closes its idle
// connections — httptest.Server.Close does, on every server, which under
// parallel tests means constantly — can tear a pooled connection out from
// under an in-flight audit request, surfacing as "transport connection
// broken". An owned pool makes the runner immune to its neighbours.
func newTransport() http.RoundTripper {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return http.DefaultTransport
}
