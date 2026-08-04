package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// This file declares the two session types and the Grant that authorises the mutating
// one. The read half is implemented in Phase 4.4 and the mutating half in 4.6; what is
// fixed here is the shape, because the shape is the safety property.

// Grant is proof that mutating probes were authorised.
//
// It has no exported fields and exactly one constructor, which performs the whole
// gating conjunction: the record verb, the -allow-mutations flag, a profile declaring
// sandbox with human-written evidence, every runtime assertion passing against the live
// tenant, the token present in the environment, and nothing credential-shaped in the
// profile itself.
//
// MutatingSession cannot be built without one. So "the gate was checked" is a property
// the type system enforces rather than a boolean somebody forgets to test, and it
// cannot be bypassed by a probe, by a future refactor, or by a test that constructs a
// session directly.
//
// It also carries the name prefix, because every created object must be findable by it and a
// prefix that travelled separately could get out of step with the one the gate validated.
type Grant struct {
	// The blank field makes Grant unconstructable from outside this package even as a
	// zero value in a composite literal, which is what closes the last hole.
	_ struct{}

	namePrefix string

	// replay is true for a grant issued to replay a recorded mutating run. Nothing is
	// created in replay -- the transport answers from the cassette -- but a mutating replay
	// still needs a grant, because the probes are the same code.
	//
	// This is the one hole the type does not close by itself, so Run closes it: a replay
	// grant used in record mode is refused. One check, one place.
	replay bool
}

// NamePrefix is the string every created object's name must begin with.
func (g *Grant) NamePrefix() string {
	if g == nil {
		return ""
	}
	return g.namePrefix
}

// IsReplay reports whether this grant was issued for replay rather than by the gate.
func (g *Grant) IsReplay() bool { return g != nil && g.replay }

// ReplayGrant authorises a mutating replay.
//
// Exported so cmd can replay a mutating cassette without a profile, a token or a tenant.
// Safe because a replay transport cannot reach a network at all: its base is DenyTransport.
// The hole it would otherwise open -- a replay grant handed to a recording run -- is closed in
// Run, which refuses that combination explicitly.
func ReplayGrant(namePrefix string) *Grant {
	return &Grant{namePrefix: namePrefix, replay: true}
}

// Profile is where and how a mutating run is allowed to reach an API.
//
// The token is deliberately absent: TokenEnv names an environment variable, and the
// value never enters the profile, a flag, a cassette or a report. A profile is a
// committed or at least a written file, and the one thing that must never be in it is
// the credential.
type Profile struct {
	// Endpoint is the API base URL.
	Endpoint string `json:"endpoint"`
	// TokenEnv names the environment variable holding the bearer token.
	TokenEnv string `json:"tokenEnv"`

	// Sandbox is the operator's claim. A claim in a file is not evidence, which is why
	// the assertions below exist.
	Sandbox bool `json:"sandbox"`
	// SandboxEvidence is a sentence a human wrote about why this is a sandbox. Required
	// and non-empty, because the act of writing it is the point: it makes an operator
	// state the reason rather than tick a box.
	SandboxEvidence string `json:"sandboxEvidence"`

	Assertions Assertions `json:"assertions"`

	// NamePrefix is prepended to every created object's name field. Non-negotiable,
	// minimum length enforced, and it must contain the tool name so anybody who finds
	// one in a UI knows what made it.
	NamePrefix string `json:"namePrefix"`

	// SecretEnv names further environment variables whose values must never appear in
	// a cassette. The redaction scanner checks every one.
	SecretEnv []string `json:"secretEnv,omitempty"`

	// RedactValues are literal strings to substitute out of recorded traffic.
	RedactValues map[string]string `json:"redactValues,omitempty"`
}

// Assertions are the checks performed against the live tenant before anything mutates.
//
// The distinction from Profile.Sandbox is the whole design: Sandbox is a claim, these
// are evidence. MaxExistingObjects in particular is the cheapest and most effective of
// them -- a tenant holding four tags is a sandbox, one holding nine hundred is
// production, and no amount of YAML can misrepresent that.
type Assertions struct {
	// EndpointHostSuffix stops a profile aimed at the wrong host.
	EndpointHostSuffix string `json:"endpointHostSuffix,omitempty"`
	// AccountGroupID is confirmed by a read before any write.
	AccountGroupID string `json:"accountGroupId,omitempty"`
	// AccountGroupParam is the query parameter that scopes a request to a group, e.g. "aid".
	//
	// Required for AccountGroupID to mean anything: which parameter carries a group id is not
	// knowable in advance, and guessing would make the assertion pass by accident on any API
	// that ignores an unknown query parameter.
	AccountGroupParam string `json:"accountGroupParam,omitempty"`
	// AccountGroupJSONPath is the field an object echoes its group id under, e.g. "aid".
	//
	// Optional. Without it the assertion rests on the scoped read alone, and the gate records
	// that weaker outcome verbatim rather than upgrading it.
	AccountGroupJSONPath string `json:"accountGroupJsonPath,omitempty"`
	// MaxExistingObjects refuses a collection with more than N objects in it.
	MaxExistingObjects int `json:"maxExistingObjects,omitempty"`
}

// MutatingSession is the full-access half.
//
// It embeds ReadSession, so a mutating probe can read as well. The methods take no path
// argument: the session is constructed from exactly one resource's paths, so a probe
// cannot point it at another resource. Blast radius is bounded structurally rather than
// by a deny list somebody has to maintain.
type MutatingSession struct {
	ReadSession

	grant *Grant
	// live is the concrete session, because ReadSession has only Get and there is no way to
	// POST through it. Unexported, so the narrow read interface stays the one probes see.
	live *httpSession

	cfg MutationConfig

	readBack ReadBackMeasurement
}

// MutationConfig is what writing needs beyond what reading needs.
type MutationConfig struct {
	// Ledger records every create before it is issued. Never nil: a memory ledger is used
	// for replay, so there is no code path where a create goes unrecorded.
	Ledger *Ledger

	// NameField is the JSON key the stamped name goes in, and IDField the key an
	// identifier comes back under. Both from the Subject, so a probe cannot disagree with
	// the sweeper about either.
	NameField string
	IDField   string

	// UpdateMethod is PUT unless the blueprint says otherwise. Carried rather than assumed
	// because an API that only accepts PATCH would have every update probe report a fact
	// about the method rather than about the field.
	UpdateMethod string

	// ReadDelay is the measured consistency window, applied before reading back a created
	// object. Zero until something measures it.
	ReadDelay time.Duration

	// Findings is what earlier probes established, for the one real dependency in the
	// catalogue: write.server-default cannot tell "the server assigned this" from "the field is
	// not settable at all" without knowing whether the field is writable.
	//
	// On the session rather than in the probe signature, because the session is already a
	// probe's window onto the run -- it carries the ledger and the read-back measurement for the
	// same reason. Read-only from a probe's side: the runner adds to it after each probe
	// returns, so there is exactly one writer.
	Findings *FactSet

	// Rehearsal is write.rehearsal's configuration, carried the same way Findings is
	// and for the same reason: the session is a probe's window onto the run.
	Rehearsal *RehearsalConfig
}

// ErrNoGrant is returned when a mutating session is requested without authorisation.
// Unreachable through the exported API -- newMutatingSession is unexported and takes a
// *Grant -- and returned rather than panicking so that a mistake degrades into a refused
// run rather than a crash.
var ErrNoGrant = errors.New("a mutating session requires a grant")

// newMutatingSession is unexported and takes a *Grant, which cannot be obtained without
// Authorise having succeeded.
//
// The signature is the safety property this file exists to fix, which is why it is
// declared in Phase 4.1 rather than alongside the gate it depends on.
func newMutatingSession(g *Grant, live *httpSession, cfg MutationConfig) (*MutatingSession, error) {
	if g == nil {
		return nil, ErrNoGrant
	}
	if live == nil {
		return nil, fmt.Errorf("%w: a mutating session needs a session to write through",
			ErrInvalidPlan)
	}
	if cfg.Ledger == nil {
		return nil, fmt.Errorf("%w: a mutating session needs a ledger, and there is no mode "+
			"without one -- replay uses MemoryLedger", ErrLedger)
	}
	if cfg.NameField == "" || cfg.IDField == "" {
		return nil, fmt.Errorf("%w: a mutating session needs both a name field and an "+
			"identifier field; Subject.CanMutate exists to refuse this earlier",
			ErrInvalidPlan)
	}

	return &MutatingSession{ReadSession: live, grant: g, live: live, cfg: cfg}, nil
}

// NamePrefix is the prefix every created object's name must carry.
func (s *MutatingSession) NamePrefix() string { return s.grant.NamePrefix() }

// NameValue composes the name a probe should send.
//
// Deterministic -- "<prefix>-<probe>-<seq>" -- because a random suffix or a timestamp would
// make every mutating cassette unreplayable: ReplayTransport matches request bodies, so a
// replayed create must send the identical name it recorded.
//
// Composed by the probe rather than injected by Create, which is the correction that matters
// here. Silently rewriting a probe's body to add the prefix would confound the normalisation
// protocol -- send "  MiXeD  ", receive prefix + "  MiXeD  " -- and make bodies unpredictable
// at replay. So the session states the rule and enforces it; the probe follows it.
func (s *MutatingSession) NameValue(probe string, seq int) string {
	return fmt.Sprintf("%s-%s-%d", s.grant.NamePrefix(), strings.ReplaceAll(probe, ".", "-"), seq)
}

// NameField is the JSON key a name goes in.
func (s *MutatingSession) NameField() string { return s.cfg.NameField }

// Create posts a body to the collection.
//
// Takes no path: see MutatingSession. The ordering here is the safety argument of the whole
// phase, so it is worth stating plainly.
//
//  1. The body is checked for the name prefix. A body without it is refused *before* anything
//     is recorded or sent, because an object the sweeper cannot find by name is an object that
//     survives a crash.
//  2. The intent is written and fsynced. If that fails the request is not issued: a create
//     that cannot be recorded is a create that cannot be cleaned up.
//  3. The request goes out under a context that cannot be cancelled. Cancellation must stop
//     *new* requests, not abort one already sent -- the response to a create in flight is the
//     one thing that turns an intent into a resolvable line.
//  4. The status decides what the intent becomes. This is the classification that matters: a
//     4xx resolves it, because a 4xx is reliable evidence nothing was created; anything else
//     leaves it outstanding, because a 500 may well have created the object.
func (s *MutatingSession) Create(
	ctx context.Context,
	probe string,
	body map[string]any,
) (*Response, string, error) {
	if err := s.checkNamePrefix(body); err != nil {
		return nil, "", err
	}

	name, _ := body[s.cfg.NameField].(string)

	seq, err := s.cfg.Ledger.Intent(probe, s.CollectionPath(), name)
	if err != nil {
		return nil, "", err
	}

	sendCtx, cancel := inFlightContext(ctx)
	defer cancel()

	resp, err := s.live.write(sendCtx, http.MethodPost, s.CollectionPath(), body)
	if err != nil {
		// A transport error is the ambiguous case, and it is left outstanding deliberately: a
		// timeout is indistinguishable from a create that succeeded on a connection that then
		// dropped.
		_ = s.cfg.Ledger.Resolve(seq, EntryKindFailed, "", 0, err.Error())
		return nil, "", err
	}

	switch {
	case resp.Status >= 400 && resp.Status < 500:
		if rErr := s.cfg.Ledger.Resolve(seq, EntryKindRejected, "", resp.Status, ""); rErr != nil {
			return resp, "", rErr
		}
		// Not an error: a refused create is an observation, and several probes exist to
		// provoke exactly this.
		return resp, "", nil

	case resp.Status >= 500:
		_ = s.cfg.Ledger.Resolve(seq, EntryKindFailed, "", resp.Status, "server error")
		return resp, "", nil
	}

	id := s.identifierOf(resp)
	if id == "" {
		// The object exists and the intent stays outstanding with only a name, which is
		// precisely the case the prefix pass of the sweeper is for.
		_ = s.cfg.Ledger.Resolve(seq, EntryKindFailed, "", resp.Status,
			"created, but no identifier could be read from the response")

		return resp, "", fmt.Errorf("%w: %s created an object but the response carried no %q; "+
			"the prefix sweep will remove it", ErrNoIdentifier, probe, s.cfg.IDField)
	}

	if err := s.cfg.Ledger.Resolve(seq, EntryKindCreated, id, resp.Status, ""); err != nil {
		return resp, id, err
	}

	return resp, id, nil
}

// checkNamePrefix enforces the invariant rather than performing it.
func (s *MutatingSession) checkNamePrefix(body map[string]any) error {
	raw, present := body[s.cfg.NameField]
	if !present {
		return fmt.Errorf("%w: a create body must set %q so the object can be swept by name",
			ErrInvalidPlan, s.cfg.NameField)
	}

	name, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%w: %q must be a string in a create body, got %T",
			ErrInvalidPlan, s.cfg.NameField, raw)
	}

	if prefix := s.grant.NamePrefix(); !strings.HasPrefix(name, prefix) {
		return fmt.Errorf("%w: %q is %q, which does not start with %q; compose it with "+
			"NameValue so the sweeper can find the object again",
			ErrInvalidPlan, s.cfg.NameField, name, prefix)
	}

	return nil
}

// identifierOf reads the created object's identifier.
//
// Coerced from a number as well as a string, because an API returning a numeric id is common
// and refusing it would strand the object for no better reason than JSON's type system.
func (s *MutatingSession) identifierOf(resp *Response) string {
	v, ok := resp.Field(s.cfg.IDField)
	if !ok {
		return ""
	}

	switch typed := v.(type) {
	case string:
		return typed
	case float64:
		return trimFloat(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

// Update sends a body to one item.
//
// No ledger entry: an update creates nothing, so there is nothing new to clean up, and
// recording it would put lines in the ledger that can never be outstanding.
func (s *MutatingSession) Update(
	ctx context.Context,
	probe, id string,
	body map[string]any,
) (*Response, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: %s tried to update without an identifier",
			ErrNoIdentifier, probe)
	}

	method := http.MethodPut
	if s.cfg.UpdateMethod != "" {
		method = s.cfg.UpdateMethod
	}

	sendCtx, cancel := inFlightContext(ctx)
	defer cancel()

	return s.live.write(sendCtx, method, s.ItemPath(id), body)
}

// Delete removes one item.
//
// Runs under a context that cannot be cancelled, for the same reason a create does: a delete
// abandoned halfway is how an orphan is made, and the whole point of getting here is to leave
// nothing behind.
//
// A 404 is *not* treated as success here. On an eventually-consistent API it may simply mean
// the object is not visible yet, and calling that "gone" is how an orphan gets reported as
// cleaned up. The sweeper decides, because only it knows the measured window and can confirm
// absence with a prefix-filtered read.
func (s *MutatingSession) Delete(ctx context.Context, probe, id string) (*Response, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: %s tried to delete without an identifier",
			ErrNoIdentifier, probe)
	}

	sendCtx, cancel := inFlightContext(ctx)
	defer cancel()

	resp, err := s.live.write(sendCtx, http.MethodDelete, s.ItemPath(id), nil)
	if err != nil {
		return resp, s.failedDelete(id, err.Error())
	}

	if resp.Status >= 400 && resp.Status != http.StatusNotFound {
		return resp, s.failedDelete(id, fmt.Sprintf("status %d", resp.Status))
	}

	if resp.Status < 400 {
		if err := s.markDeleted(id, resp.Status); err != nil {
			return resp, err
		}
	}

	return resp, nil
}

// failedDelete counts a delete that did not work, and returns an error only once the cap is
// exceeded.
//
// Below the cap the caller carries on: the sweeper retries, and one failed attempt is not
// evidence that cleanup is impossible. Above it, the run stops creating -- which is why the
// counting lives in the session, where every delete passes through, rather than in a caller
// that might forget.
func (s *MutatingSession) failedDelete(id, why string) error {
	if !s.live.budget.recordDeleteFailure() {
		return nil
	}

	return fmt.Errorf("%w: deleting %s failed (%s)", ErrDeleteFailures, id, why)
}

// markDeleted resolves whichever outstanding intent this identifier belongs to.
//
// Matched by id rather than tracked by the caller, because the sweeper deletes objects it read
// out of a collection and never had an intent sequence for.
func (s *MutatingSession) markDeleted(id string, status int) error {
	for _, o := range Unresolved(s.cfg.Ledger.Entries()) {
		if o.ID == id {
			return s.cfg.Ledger.Resolve(o.Seq, EntryKindDeleted, id, status, "")
		}
	}

	return nil
}

// ReadBackMeasurement is what the session observed about read-after-write.
//
// Accumulated on the session rather than in a probe, because every mutating probe reads back the
// objects it creates and would otherwise each measure the same thing separately -- and because
// the *use* of the measurement is not optional. Without it every probe would read once, get a 404
// or a partial object, and record "the field is absent" at Observed confidence when what it
// actually saw was an eventually-consistent read.
type ReadBackMeasurement struct {
	// Objects is how many created objects were read back.
	Objects int
	// Reads is every read issued, including retries.
	Reads int
	// Retried is how many objects needed more than one read.
	Retried int
	// WorstRetries is the most retries any single object needed, which is what a generated
	// read-back loop has to be able to survive.
	WorstRetries int
	// Interval is the delay between attempts.
	Interval time.Duration
	// Statuses are the non-success statuses seen on a first read, so a fact can say what the
	// API actually did rather than "it failed".
	Statuses []int

	// Evidence is every read-back interaction, which is what the claim rests on.
	//
	// All of them rather than a sample: unlike a per-field fact, this one is a statement about
	// the whole run's reads, so the whole set *is* the precise citation rather than a vague one.
	Evidence []string
}

// readBackAttempts is the first read plus two retries.
//
// Three rather than a longer loop: the question is whether a re-read is needed at all, and an
// object still invisible after three reads is a finding in itself rather than something to wait
// out. A generated provider's own retry count comes from the fact, not from this.
const readBackAttempts = 3

// defaultReadDelay spaces the retries of a read-back.
const defaultReadDelay = 500 * time.Millisecond

// ReadCreated reads an object the run just created, retrying while it is not yet visible.
//
// Constant interval rather than exponential, and no jitter: the number of requests a run makes has
// to be reproducible, because a cassette is an ordered transcript and a replay that issued a
// different number of reads would mismatch.
//
// A 404 here is not "absent". It is the signature of a read replica that has not caught up, and
// treating it as absence is how a probe concludes a field does not exist when the whole object
// does not exist yet.
func (s *MutatingSession) ReadCreated(
	ctx context.Context,
	probe, id string,
	query url.Values,
) (*Response, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: %s tried to read back without an identifier",
			ErrNoIdentifier, probe)
	}

	interval := s.cfg.ReadDelay
	if interval <= 0 {
		interval = defaultReadDelay
	}

	s.readBack.Objects++
	s.readBack.Interval = interval

	var last *Response

	for attempt := range readBackAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return last, fmt.Errorf("%w: waiting for %s to become readable",
					ErrCancelled, id)
			case <-time.After(interval):
			}
		}

		resp, err := s.Get(ctx, s.ItemPath(id), query)
		if err != nil {
			return nil, err
		}

		s.readBack.Reads++
		s.readBack.Evidence = appendUnique(s.readBack.Evidence, resp.Interaction)
		last = resp

		if resp.Status < 400 {
			if attempt > 0 {
				s.readBack.Retried++
				if attempt > s.readBack.WorstRetries {
					s.readBack.WorstRetries = attempt
				}
			}

			return resp, nil
		}

		if attempt == 0 {
			s.readBack.Statuses = appendUnique(s.readBack.Statuses, resp.Status)
		}

		// A 404 is worth retrying; a 403 or a 401 is not, and retrying one would spend
		// requests to re-learn that the credential is the problem.
		if resp.Status != http.StatusNotFound {
			return resp, nil
		}
	}

	s.readBack.Retried++
	s.readBack.WorstRetries = readBackAttempts - 1

	return last, nil
}

// ReadBack is what the session measured, for the read-your-writes probe to report.
func (s *MutatingSession) ReadBack() ReadBackMeasurement { return s.readBack }

// Findings is what the run has established so far.
//
// Never nil, so a probe need not guard: a run with nothing established yet answers "not settled"
// to every question, which is the correct answer.
func (s *MutatingSession) Findings() *FactSet {
	if s.cfg.Findings == nil {
		return NewFactSet(nil)
	}

	return s.cfg.Findings
}

// resolveByName resolves an outstanding intent that never learned an identifier.
//
// The prefix pass of the sweeper is the only caller, and it is the only thing that can close
// these entries: an intent with no identifier is unmatchable by identifier, by definition.
func (s *MutatingSession) resolveByName(name, id string) {
	if name == "" {
		return
	}

	for _, o := range Unresolved(s.cfg.Ledger.Entries()) {
		if o.Name == name {
			_ = s.cfg.Ledger.Resolve(o.Seq, EntryKindDeleted, id, 0, "removed by the prefix sweep")
			return
		}
	}
}

// inFlightGrace bounds a request that must not be cancelled. Under the client timeout, so a
// hung connection is still cut by the transport rather than by this.
const inFlightGrace = 15 * time.Second

// inFlightContext detaches a request from cancellation.
//
// context.WithoutCancel and not the parent, because the commonest reason to be cancelled is
// the commonest reason to have something to clean up. A one-liner whose being wrong is
// invisible in every green run, which is why it is a named function with its own test.
func inFlightContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), inFlightGrace)
}

// readOnly is a ReadSession that refuses everything.
//
// It exists so the catalogue can be described, and probes registered and cost-budgeted,
// before any transport is written -- which is what makes `probe -list` work with no
// credentials and no cassettes.
type readOnly struct {
	collection string
	item       string
}

func (r readOnly) Get(context.Context, string, url.Values) (*Response, error) {
	return nil, errNotImplemented
}
func (r readOnly) CollectionPath() string    { return r.collection }
func (r readOnly) ItemPath(id string) string { return resolvePath(r.item, id) }

// resolvePath substitutes an id into a path template.
//
// Replaces whatever the parameter is named rather than looking for "{id}" specifically:
// path templates in the wild use {testId}, {agentId}, {aid} and worse, and matching a
// literal would silently produce a URL with a brace still in it.
func resolvePath(template, id string) string {
	return pathParam.ReplaceAllString(template, id)
}
