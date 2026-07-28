package probe

import (
	"context"
	"errors"
	"net/url"
)

// This file declares the two session types and the Grant that authorises the mutating
// one. The read half is implemented in Phase 4.4 and the mutating half in 4.6; what is
// fixed here is the shape, because the shape is the safety property.

// Grant is proof that mutating probes were authorised.
//
// It has no exported fields and exactly one constructor, which performs the whole
// gating conjunction: record mode, the --allow-mutations flag, a profile declaring
// sandbox with human-written evidence, every runtime assertion passing against the live
// tenant, the token present in the environment, and nothing credential-shaped in the
// profile itself.
//
// MutatingSession cannot be built without one. So "the gate was checked" is a property
// the type system enforces rather than a boolean somebody forgets to test, and it
// cannot be bypassed by a probe, by a future refactor, or by a test that constructs a
// session directly.
//
// The profile and budget it will carry are added in Phase 4.6, alongside the gate that
// populates them. Declaring them now would be dead weight, and the safety property does
// not depend on them: it comes from the blank field plus the unexported constructor.
type Grant struct {
	// The blank field makes Grant unconstructable from outside this package even as a
	// zero value in a composite literal, which is what closes the last hole.
	_ struct{}
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
func newMutatingSession(g *Grant, read ReadSession) (*MutatingSession, error) {
	if g == nil {
		return nil, ErrNoGrant
	}
	return &MutatingSession{ReadSession: read, grant: g}, nil
}

// Create posts a body to the collection.
//
// Takes no path: see MutatingSession. Returns the created object's id as the blueprint's
// ID binding locates it, so a probe never parses an id out of a body itself.
//
// Implemented in Phase 4.7.
func (s *MutatingSession) Create(
	ctx context.Context,
	probe string,
	body map[string]any,
) (*Response, string, error) {
	return nil, "", errNotImplemented
}

// Update sends a body to one item. Implemented in Phase 4.7.
func (s *MutatingSession) Update(
	ctx context.Context,
	probe, id string,
	body map[string]any,
) (*Response, error) {
	return nil, errNotImplemented
}

// Delete removes one item. Implemented in Phase 4.7.
func (s *MutatingSession) Delete(ctx context.Context, probe, id string) (*Response, error) {
	return nil, errNotImplemented
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
