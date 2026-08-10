// Package run executes an audit plan against a live API. It is the only
// code in the toolkit that touches a network with credentials, and every
// piece of it is shaped by that: a sandbox guard sits in front of every
// mutating request, every created object is recorded in a durable ledger
// before the request that creates it is sent, budgets bound what a run may
// spend, and everything written or logged passes through redaction first.
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

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/plan"
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
	Plan    *plan.Plan
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
	// WorkDir holds the created-object ledgers. Empty refuses: a mutating
	// run without a durable ledger cannot promise cleanup after a crash.
	WorkDir string
	// SpecHash stamps every observation with the pinned document hash it
	// was observed against.
	SpecHash string
	// AllowSharedTenant skips the pre-flight foreign-object refusal.
	AllowSharedTenant bool
	Logger            zerolog.Logger
	// Lookup reads the environment: secrets, ${VAR} inputs and the
	// sandbox acknowledgement. Nil means os.LookupEnv.
	Lookup func(string) (string, bool)
	// RunID overrides the generated run id. Leave empty outside tests.
	RunID string

	// beforeSend, when set, is called after the ledger write and
	// immediately before each request is sent. Test hook.
	beforeSend func(*http.Request)
}

// SandboxEnv is the environment variable an operator sets to 1 to
// acknowledge that the tenant BaseURL points at may have objects created
// and deleted in it. Without it no mutating request is ever sent.
const SandboxEnv = "TFPFGEN_SANDBOX_OK"

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
	// Tolerance records the undeclaredSpecField calibration per entity:
	// whether the API ignored or rejected a body field no schema declares.
	// Recorded here rather than as an observation because the observation
	// kind set is closed and calibration is about how to read the other
	// findings, not a finding itself.
	Tolerance map[string]string `json:"tolerance,omitempty"`
}

// Run executes the plan and returns every observation it derived, however
// far it got. The error reports a run that could not be attempted — bad
// options, a refused sandbox guard, a cancelled context — never a budget
// or a misbehaving entity, which are recorded and moved past.
func Run(ctx context.Context, opts Options) ([]observe.Observation, Summary, error) {
	r, err := newRunner(opts)
	if err != nil {
		return nil, Summary{}, err
	}
	defer r.ledger.close()

	// The sandbox guard is checked before the first request of any kind
	// when the plan mutates at all: even the pre-run cleanup pass deletes.
	if planMutates(opts.Plan) {
		if err := r.checkSandboxEnv(); err != nil {
			return nil, Summary{}, err
		}
	}

	r.started = time.Now()
	r.deadline = r.started.Add(r.budget.Duration)

	r.summary.CleanupStart = r.cleanupDebris(ctx)

	for i := range opts.Plan.Entities {
		if ctx.Err() != nil {
			break
		}
		r.runEntity(ctx, &opts.Plan.Entities[i])
	}

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
	ledger *ledger
	base   *url.URL
	runID  string
	budget Budgets

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
	if opts.WorkDir == "" && planMutates(opts.Plan) {
		return nil, fmt.Errorf("audit run: no work directory for the created-object ledger; a mutating run without a durable ledger cannot promise cleanup after a crash")
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

	led, err := openLedger(opts.WorkDir, runID)
	if err != nil {
		return nil, err
	}

	r := &runner{
		opts:     opts,
		log:      opts.Logger.With().Str("runId", runID).Logger(),
		client:   &http.Client{},
		auth:     auth,
		bucket:   newBucket(rps),
		ledger:   led,
		base:     base,
		runID:    runID,
		budget:   budget,
		registry: map[string]*createdObject{},
		recipes:  map[string]*entityRecipe{},
		summary: Summary{
			RunID:     runID,
			ByKind:    map[observe.Kind]int{},
			ByOutcome: map[observe.Outcome]int{},
			Tolerance: map[string]string{},
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

// checkSandboxEnv is the first tier of the sandbox guard: the operator's
// explicit acknowledgement, required before any request when the plan
// mutates.
func (r *runner) checkSandboxEnv() error {
	if v, ok := r.opts.Lookup(SandboxEnv); !ok || v != "1" {
		return fmt.Errorf("audit run: %s is not set to 1; this plan creates and deletes real objects in the tenant %s points at, and the acknowledgement is required every time",
			SandboxEnv, r.base.Host)
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
// step could not re-earn it. On equal outcomes the later record wins —
// the finalize-time conclusions are drawn from the create/read pair,
// which is the canonical evidence for a per-field claim.
func (r *runner) finishObservations() []observe.Observation {
	best := map[string]int{}
	for i, o := range r.obs {
		if j, ok := best[o.ID]; !ok || outcomeRank(o.Outcome) >= outcomeRank(r.obs[j].Outcome) {
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

// finishSummary fills the counters a table prints.
func (r *runner) finishSummary(obs []observe.Observation) {
	for _, o := range obs {
		r.summary.ByKind[o.Kind]++
		r.summary.ByOutcome[o.Outcome]++
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
	r.summary.Skipped = len(r.opts.Plan.Skipped)
	r.summary.Requests = r.reqTotal
	r.summary.RequestBudget = r.budget.Requests
	r.summary.ObjectBudget = r.budget.Objects
	r.summary.Elapsed = time.Since(r.started).Round(time.Millisecond)
	r.summary.DurationBudget = r.budget.Duration
}
