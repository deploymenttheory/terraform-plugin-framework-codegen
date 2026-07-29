package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
)

// The gate is the whole of the authorisation for writing to somebody's API.
//
// # Why it is a table
//
// The conjunction is long, and a long conjunction written as nested ifs has two failure modes:
// a condition gets dropped in a refactor and nothing notices, and the run refuses with the first
// reason rather than all of them. A table fixes both -- the completeness test iterates the same
// data, and a refusal lists everything unmet, so an operator fixes their profile once instead of
// discovering the next problem on the next attempt.
//
// # Two tiers, and why
//
// "Report every unmet condition" and "issue read-only requests before anything mutates" pull
// against each other: enumerating runtime failures against a tenant already refused on static
// grounds spends somebody's rate-limit allowance to learn nothing. So every *static* condition
// is reported together, and the runtime assertions are attempted only once the static tier is
// clean.
//
// # A claim is not evidence
//
// Profile.Sandbox is what an operator wrote in a file. The assertions are what the tenant
// demonstrated. maxExistingObjects is the cheapest and most empirical of them: a tenant holding
// four objects is a sandbox, one holding nine hundred is production, and no amount of
// configuration can misrepresent that.

// Environ reads the environment.
//
// An interface because t.Setenv forbids t.Parallel, and every test in this package is parallel.
// It also makes the gate's inputs total: everything it decides on arrives through its arguments,
// so a gate test cannot pass by accident because of something the process happened to have set.
type Environ interface {
	Lookup(name string) (string, bool)
}

// OSEnviron reads the real environment.
type OSEnviron struct{}

// Lookup implements Environ.
func (OSEnviron) Lookup(name string) (string, bool) { return os.LookupEnv(name) }

// MapEnviron is an Environ backed by a map, for tests and for a caller that has already resolved
// its variables.
type MapEnviron map[string]string

// Lookup implements Environ.
func (m MapEnviron) Lookup(name string) (string, bool) {
	v, ok := m[name]
	return v, ok
}

// GateOptions is what the caller asked for, as distinct from what the profile allows.
type GateOptions struct {
	Mode Mode
	// AllowMutations is the flag. A request, not an authorisation.
	AllowMutations bool

	Subject Subject
	Plan    Plan

	// EquivalentSnapshotExists is true when committed evidence was already recorded with an
	// identical plan.
	//
	// Equivalent, not merely present, and the distinction is the whole condition. A recording
	// does not overwrite anything -- cassette.Write creates a new timestamped snapshot and the
	// old one stays for comparison -- so refusing whenever *any* snapshot exists would mean the
	// toolkit could never record a second time. What is worth refusing is a run that would add a
	// snapshot indistinguishable from one already committed: churn in the evidence store, a new
	// directory for CI to verify against, and nothing learned.
	//
	// A run whose plan differs is new evidence and is allowed. The first write-tier recording
	// against a resource that only has read-tier evidence is exactly that case.
	EquivalentSnapshotExists bool
	Force                    bool
}

// gateInput is everything a condition may look at.
//
// One struct rather than a closure over locals, so the table's entries are pure functions of
// their input and a test can build any combination it likes.
type gateInput struct {
	profile Profile
	opts    GateOptions

	// token is resolved once, and is the only place the credential appears in this file.
	token string
	// endpoint is the parsed endpoint, nil when it does not parse.
	endpoint *url.URL
}

// condition is one thing that must hold.
type condition struct {
	// name is stable and is what a refusal and the report's AssertionsPassed both use.
	name string
	// modes are the run modes this condition applies to. A sweep is gated differently: see
	// AuthoriseSweep.
	modes []Mode
	// check returns "" when satisfied, and otherwise the sentence an operator reads.
	check func(gateInput) string
}

// minSecretLength is the shortest value worth looking for verbatim. Below it, a match is far
// likelier to be a coincidence than a credential.
const minSecretLength = 8

const (
	// minEvidenceChars and minEvidenceWords are what makes sandboxEvidence more than a
	// checkbox. Writing the sentence is the check: it makes an operator state a reason
	// rather than tick a box, and "yes" or "sandbox" does not survive either bound.
	minEvidenceChars = 24
	minEvidenceWords = 4

	// minPrefixChars bounds the name prefix. Short enough to fit a name field, long enough
	// that the prefix sweep cannot match something it did not create.
	minPrefixChars = 8
	// requiredPrefixToken must appear in the prefix, so anybody who finds one of these
	// objects in a UI can tell what made it.
	requiredPrefixToken = "tfpfgen"
)

// staticConditions are checked without issuing a single request.
//
// Ordered from cheapest and most likely to be wrong, downwards, so the first thing an operator
// reads in a refusal is usually the thing they need to change.
var staticConditions = []condition{
	{
		name:  "mode",
		modes: []Mode{ModeRecord},
		check: func(in gateInput) string {
			if in.opts.Mode == ModeRecord {
				return ""
			}
			return fmt.Sprintf("mutating probes need -mode record, and this is %s; "+
				"replay and verify derive facts from a transcript and must never write",
				in.opts.Mode)
		},
	},
	{
		name:  "allowMutations",
		modes: []Mode{ModeRecord},
		check: func(in gateInput) string {
			if in.opts.AllowMutations {
				return ""
			}
			return "--allow-mutations was not given; mutating probes create real objects in " +
				"the tenant the profile points at, so the flag is required every time"
		},
	},
	{
		name:  "sandbox",
		modes: []Mode{ModeRecord},
		check: func(in gateInput) string {
			if in.profile.Sandbox {
				return ""
			}
			return "the profile does not declare sandbox: true"
		},
	},
	{
		name:  "sandboxEvidence",
		modes: []Mode{ModeRecord},
		check: func(in gateInput) string {
			evidence := strings.TrimSpace(in.profile.SandboxEvidence)

			switch {
			case evidence == "":
				return "the profile carries no sandboxEvidence; write a sentence saying why " +
					"this tenant is safe to create and delete objects in"
			case len(evidence) < minEvidenceChars:
				return fmt.Sprintf("sandboxEvidence is %d characters, and at least %d are "+
					"required; the point is that a human states a reason rather than ticks a box",
					len(evidence), minEvidenceChars)
			case len(strings.Fields(evidence)) < minEvidenceWords:
				return fmt.Sprintf("sandboxEvidence is %d word(s), and at least %d are required",
					len(strings.Fields(evidence)), minEvidenceWords)
			}

			return ""
		},
	},
	{
		name:  "namePrefix",
		modes: []Mode{ModeRecord, ModeSweep},
		check: func(in gateInput) string {
			prefix := in.profile.NamePrefix

			switch {
			case len(prefix) < minPrefixChars:
				return fmt.Sprintf("namePrefix %q is shorter than %d characters; every created "+
					"object carries it, and the sweeper's second pass is bounded by nothing else",
					prefix, minPrefixChars)
			case !strings.Contains(prefix, requiredPrefixToken):
				return fmt.Sprintf("namePrefix %q does not contain %q; anybody who finds one of "+
					"these objects in a UI needs to be able to tell what made it",
					prefix, requiredPrefixToken)
			}

			return ""
		},
	},
	{
		name:  "tokenEnv",
		modes: []Mode{ModeRecord, ModeSweep},
		check: func(in gateInput) string {
			if in.profile.TokenEnv == "" {
				return "the profile names no tokenEnv; the credential is read from an " +
					"environment variable and must never be in the profile or on a command line"
			}
			if in.token == "" {
				return fmt.Sprintf("the environment variable %s named by tokenEnv is unset or "+
					"empty", in.profile.TokenEnv)
			}

			return ""
		},
	},
	{
		name:  "noCredentialInProfile",
		modes: []Mode{ModeRecord, ModeSweep},
		check: func(in gateInput) string { return credentialShaped(in.profile, in.token) },
	},
	{
		name:  "https",
		modes: []Mode{ModeRecord, ModeSweep},
		check: func(in gateInput) string {
			if in.endpoint == nil {
				return fmt.Sprintf("the endpoint %q is not a URL", in.profile.Endpoint)
			}
			if in.endpoint.Scheme == "https" {
				return ""
			}

			return fmt.Sprintf("the endpoint scheme is %q; a bearer token over plain HTTP is a "+
				"credential on the wire in clear", in.endpoint.Scheme)
		},
	},
	{
		name:  "endpointHostSuffix",
		modes: []Mode{ModeRecord, ModeSweep},
		check: func(in gateInput) string {
			want := in.profile.Assertions.EndpointHostSuffix
			if want == "" || in.endpoint == nil {
				return ""
			}

			if strings.HasSuffix(in.endpoint.Hostname(), want) {
				return ""
			}

			return fmt.Sprintf("the endpoint host %q does not end in %q, so this profile is "+
				"pointed somewhere it was not written for",
				in.endpoint.Hostname(), want)
		},
	},
	{
		name:  "canMutate",
		modes: []Mode{ModeRecord},
		check: func(in gateInput) string {
			if can, why := in.opts.Subject.CanMutate(); !can {
				return why
			}
			return ""
		},
	},
	{
		name:  "plan",
		modes: []Mode{ModeRecord},
		check: func(in gateInput) string {
			if err := in.opts.Plan.Validate(in.opts.Subject); err != nil {
				return "the probe plan is not usable: " + err.Error()
			}

			// Checked here rather than in Plan.Validate, because `probe -list` legitimately
			// works with no fixture at all -- it reports the unnarrowed worst case. A mutating
			// run cannot: a fixture is the only source of a body the API will accept, and
			// without one every probe would abandon its protocol on the first create.
			if len(in.opts.Plan.Fixtures) == 0 {
				return "the plan declares no fixtures; a mutating probe has no valid request " +
					"body to build on without at least one"
			}

			return ""
		},
	},
	{
		name:  "noSnapshotOverwrite",
		modes: []Mode{ModeRecord},
		check: func(in gateInput) string {
			if !in.opts.EquivalentSnapshotExists || in.opts.Force {
				return ""
			}

			return "evidence recorded with this exact plan is already committed, so another " +
				"recording would add a snapshot nothing distinguishes from it. Change the plan, " +
				"or pass -force if re-recording the same run is what you mean"
		},
	},
}

// appliesTo reports whether a condition is checked in a mode.
func (c condition) appliesTo(m Mode) bool {
	for _, mode := range c.modes {
		if mode == m {
			return true
		}
	}

	return false
}

// credentialShaped looks for a secret in the profile itself.
//
// Three independent checks, because they catch different mistakes.
//
// The exact-token check is the one that matters most, and it is the one a shape heuristic cannot
// do. cassette.Scan's unstructured rule needs forty unbroken base64 characters; a great many real
// credentials -- anything hyphenated, anything shorter -- sail straight through it. But the token
// is right there in the environment, so "is this exact value in the profile" is both precise and
// free, and it catches the actual mistake: an operator pasting their token in rather than the name
// of the variable holding it.
//
// The other two are the cheap generalisations. A key *named* like a credential is somebody putting
// a secret where the schema does not expect one; cassette.Scan catches a credential-shaped value
// under an innocent key.
//
// The profile is a file somebody writes down and often commits, and it is the one place the
// credential must never be.
func credentialShaped(p Profile, token string) string {
	encoded, err := json.Marshal(p)
	if err != nil {
		return "the profile could not be re-encoded to check it for credentials: " + err.Error()
	}

	var generic map[string]any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return "the profile could not be re-read to check it for credentials: " + err.Error()
	}

	if token != "" && len(token) >= minSecretLength && strings.Contains(string(encoded), token) {
		// Deliberately checked before anything is excluded, RedactValues included. An operator
		// listing their own token there has still written it into the profile, and the bearer
		// credential is redacted from recordings automatically -- so there is no reason to put
		// it there and every reason not to.
		return fmt.Sprintf("the profile contains the value of %s; the credential belongs in "+
			"that environment variable and nowhere else, and a profile is a file that gets "+
			"written down", p.TokenEnv)
	}

	if key := credentialNamedKey(generic); key != "" {
		return fmt.Sprintf("the profile carries a key named %q; the credential belongs in the "+
			"environment variable named by tokenEnv and nowhere else", key)
	}

	// RedactValues legitimately holds secret-shaped strings -- that is its entire purpose --
	// so it is excluded rather than tripping the scanner it feeds.
	withoutRedactions := p
	withoutRedactions.RedactValues = nil

	safe, err := json.Marshal(withoutRedactions)
	if err != nil {
		return "the profile could not be re-encoded to scan it: " + err.Error()
	}

	if findings := cassette.Scan("profile", safe, nil); len(findings) > 0 {
		return fmt.Sprintf("the profile contains something credential-shaped (%s); if that is a "+
			"false positive, move the value into an environment variable rather than widening "+
			"this check", findings[0].Shape)
	}

	return ""
}

// credentialSuspiciousKeys are substrings that mean a key is holding something it should not.
var credentialSuspiciousKeys = []string{
	"token", "secret", "password", "passwd", "apikey", "api_key", "bearer", "credential",
}

// credentialNamedKey walks the profile looking for a suspiciously named key.
//
// tokenEnv and secretEnv are exempt by exact name: they hold variable *names*, which is the
// design, and refusing them would make the safe pattern unexpressible.
func credentialNamedKey(v any) string {
	switch typed := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		// Sorted, so which key a refusal names does not depend on map iteration order.
		sort.Strings(keys)

		for _, k := range keys {
			if k == "tokenEnv" || k == "secretEnv" {
				continue
			}
			lower := strings.ToLower(k)
			for _, bad := range credentialSuspiciousKeys {
				if strings.Contains(lower, bad) {
					return k
				}
			}
			if found := credentialNamedKey(typed[k]); found != "" {
				return found
			}
		}

	case []any:
		for _, item := range typed {
			if found := credentialNamedKey(item); found != "" {
				return found
			}
		}
	}

	return ""
}

// Authorise performs the whole gating conjunction and issues a Grant only if all of it holds.
//
// Takes a context because the runtime tier makes read-only requests. Returns the passed
// assertions alongside the grant so the report can state what evidence stood behind a mutating
// run -- a report that recorded only "sandbox: true" would be recording the claim rather than
// the evidence.
func Authorise(
	ctx context.Context,
	read ReadSession,
	p Profile,
	opts GateOptions,
	env Environ,
) (*Grant, []string, error) {
	if env == nil {
		env = OSEnviron{}
	}

	in := inputFor(p, opts, env)

	var (
		refusals []string
		passed   []string
	)

	for _, c := range staticConditions {
		if !c.appliesTo(ModeRecord) {
			continue
		}

		if why := c.check(in); why != "" {
			refusals = append(refusals, fmt.Sprintf("%s: %s", c.name, why))
		} else {
			passed = append(passed, c.name)
		}
	}

	// The static tier gates the runtime tier deliberately. Issuing tenant reads to enumerate
	// runtime failures against a tenant already refused on static grounds spends somebody's
	// quota to learn nothing.
	if len(refusals) > 0 {
		return nil, nil, refusedError(refusals)
	}

	runtimePassed, runtimeRefusals := checkRuntime(ctx, read, in)
	if len(runtimeRefusals) > 0 {
		return nil, nil, refusedError(runtimeRefusals)
	}

	passed = append(passed, runtimePassed...)

	return &Grant{namePrefix: p.NamePrefix}, passed, nil
}

// AuthoriseSweep is the same table, filtered to what cleaning up needs.
//
// Deliberately does not require --allow-mutations: demanding the mutation flag in order to clean
// up after yourself is perverse, and an operator staring at an orphan table should not have to
// re-read the documentation. Deliberately does not check maxExistingObjects either: a tenant
// that now fails it may be failing it *because* it is holding your orphans.
func AuthoriseSweep(p Profile, opts GateOptions, env Environ) (*Grant, error) {
	if env == nil {
		env = OSEnviron{}
	}

	in := inputFor(p, opts, env)

	var refusals []string

	for _, c := range staticConditions {
		if !c.appliesTo(ModeSweep) {
			continue
		}
		if why := c.check(in); why != "" {
			refusals = append(refusals, fmt.Sprintf("%s: %s", c.name, why))
		}
	}

	if len(refusals) > 0 {
		return nil, refusedError(refusals)
	}

	return &Grant{namePrefix: p.NamePrefix}, nil
}

// inputFor resolves everything a condition may look at.
//
// The token is read here and held nowhere else in this file: it exists only so the tokenEnv
// condition can tell "no variable named" from "named but unset", which are different mistakes
// with different fixes.
func inputFor(p Profile, opts GateOptions, env Environ) gateInput {
	in := gateInput{profile: p, opts: opts}
	in.token, _ = env.Lookup(p.TokenEnv)

	// A URL that does not parse leaves endpoint nil, which the https condition reports. Parsed
	// once rather than per condition, because three conditions need it and a URL that parses
	// differently between them would be worse than one that does not parse at all.
	if parsed, err := url.Parse(p.Endpoint); err == nil && parsed.Host != "" {
		in.endpoint = parsed
	}

	return in
}

// checkRuntime performs the assertions that need the tenant to answer.
func checkRuntime(ctx context.Context, read ReadSession, in gateInput) (passed, refusals []string) {
	if read == nil {
		return nil, []string{"maxExistingObjects: no session was available to check the " +
			"tenant with, and an unchecked assertion must not read as a passed one"}
	}

	if max := in.profile.Assertions.MaxExistingObjects; max > 0 {
		resp, err := read.Get(ctx, read.CollectionPath(), nil)

		switch {
		case err != nil:
			refusals = append(refusals, fmt.Sprintf(
				"maxExistingObjects: the collection could not be read (%v), so the tenant's "+
					"size is unknown", err))

		case resp.Status >= 400:
			refusals = append(refusals, fmt.Sprintf(
				"maxExistingObjects: the collection read returned %d, so the tenant's size is "+
					"unknown", resp.Status))

		case len(resp.Items()) >= max:
			refusals = append(refusals, fmt.Sprintf(
				"maxExistingObjects: the collection holds %d object(s) and the profile allows "+
					"fewer than %d; a tenant this full is not a sandbox",
				len(resp.Items()), max))

		default:
			passed = append(passed, fmt.Sprintf("maxExistingObjects (%d < %d)",
				len(resp.Items()), max))
		}
	}

	if outcome, why := checkAccountGroup(ctx, read, in); why != "" {
		refusals = append(refusals, "accountGroupId: "+why)
	} else if outcome != "" {
		passed = append(passed, outcome)
	}

	return passed, refusals
}

// checkAccountGroup confirms the run is scoped to the account group the profile names.
//
// Needs two extra fields to be implementable at all: neither the query parameter carrying a group
// id nor the JSON field echoing it is knowable in advance, and guessing either would make the
// assertion pass by accident on an API that ignores an unknown parameter.
//
// On an empty tenant no object can confirm the scope, and the weaker outcome is recorded
// verbatim rather than upgraded. A gate that recorded the strong claim from the weak evidence
// would be worse than one that recorded nothing.
func checkAccountGroup(ctx context.Context, read ReadSession, in gateInput) (string, string) {
	want := in.profile.Assertions.AccountGroupID
	if want == "" {
		return "", ""
	}

	param := in.profile.Assertions.AccountGroupParam
	if param == "" {
		return "", "the profile asserts an accountGroupId but names no accountGroupParam, so " +
			"there is no way to scope a request to it"
	}

	resp, err := read.Get(ctx, read.CollectionPath(), url.Values{param: []string{want}})
	if err != nil {
		return "", fmt.Sprintf("the scoped read failed (%v)", err)
	}
	if resp.Status >= 400 {
		return "", fmt.Sprintf("the scoped read returned %d, so this credential may not be "+
			"scoped to account group %s at all", resp.Status, want)
	}

	items := resp.Items()
	if len(items) == 0 {
		// The honest outcome, stated as such.
		return "accountGroupId (scoped read succeeded; the tenant is empty, so no object " +
			"confirmed it)", ""
	}

	path := in.profile.Assertions.AccountGroupJSONPath
	if path == "" {
		return "accountGroupId (scoped read succeeded; no accountGroupJsonPath was given, so " +
			"no object confirmed it)", ""
	}

	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}

		got, present := fieldIn(obj, path)
		if !present {
			continue
		}

		if fmt.Sprint(got) != want {
			return "", fmt.Sprintf("an object in the scoped read carries %s = %v, not %s; the "+
				"credential is reaching a different account group",
				path, got, want)
		}

		return fmt.Sprintf("accountGroupId (confirmed by an object's %s)", path), ""
	}

	return fmt.Sprintf("accountGroupId (scoped read succeeded; no object carried %s, so none "+
		"confirmed it)", path), ""
}

// refusedError lists every unmet condition.
//
// All of them, not the first. An operator fixing a profile one refusal per attempt is an
// operator making several more requests against a tenant the tool has already decided it does
// not want to write to.
func refusedError(refusals []string) error {
	var b strings.Builder

	for _, r := range refusals {
		b.WriteString("\n  - " + r)
	}

	return fmt.Errorf("%w: %d condition(s) were not met:%s",
		ErrRefused, len(refusals), b.String())
}
