// Package probe discovers what an API actually does, as opposed to what its
// specification claims.
//
// An OpenAPI document does not record the facts that decide whether a provider
// works: which fields are genuinely writable, which are immutable, what the server
// normalises, what it defaults. internal/ingest is faithful to the specification and
// therefore inherits its gaps, and the gaps are where perpetual diffs and spurious
// replacements come from. This package settles them empirically and commits the HTTP
// evidence.
//
// # Governing principle: optimise for refutation, not discovery
//
// A confidently wrong fact is worse than no fact. A false Immutable destroys real
// infrastructure, and a false Writable=false blanks a practitioner's configuration on
// every refresh. So the design is arranged around ruling explanations out rather than
// finding them:
//
//   - No fact about a field is derived from a single sent value. Send "FOO", read back
//     "foo", and "the server lowercases" and "the server ignored me and returned a
//     default" are equally good explanations. Two distinct values separate them.
//   - Every fact names the alternatives its sequence did *not* eliminate, so
//     confidence is interrogable rather than a number nobody can argue with.
//   - Facts are re-derived from committed cassettes in CI with the network denied, so
//     derivation is provably a pure function of the transcript.
//
// # The prober speaks raw HTTP and does not import the SDK
//
// It is driven by the blueprint. Every operation in a ResourceBinding already carries
// HTTPMethod, PathTemplate and SuccessCodes, and every attribute's WireBinding carries
// JSONPath -- documented there as "the join key for probe facts". So a committed
// blueprint is already a complete, machine-readable description of the requests to
// issue and the JSON keys to watch.
//
// That keeps the toolkit independent of the SDK it generates against, which is what a
// second API needs. It also forces request bodies to be hand-built map[string]any
// rather than Go structs, which matters concretely: the update-style protocol turns on
// the difference between an absent key and an explicit null, and a struct with
// omitempty cannot express that.
package probe

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
)

// errNotImplemented marks a probe that is registered but whose body is not written
// yet. Registering first is deliberate: the catalogue, its costs and its stated
// failure modes are reviewable before any request is issued.
var errNotImplemented = errors.New("probe not implemented yet")

// Kind separates probes that only read from probes that change things.
//
// It exists for reporting and for -list. The actual enforcement is in the type
// system: the two probe interfaces take different session types, so a read-only probe
// cannot be given the means to mutate.
type Kind string

const (
	// KindRead issues GET requests only. Safe against any API, including production.
	KindRead Kind = "read"
	// KindMutating creates, updates or deletes. Runs only behind the full gating
	// conjunction in gate.go.
	KindMutating Kind = "mutating"
)

// Response is one HTTP response, as much of it as a probe is allowed to see.
//
// Deliberately not http.Response: a probe must not be able to read the body twice, or
// reach the underlying connection, or see a header the cassette does not record. What
// is here is what can be replayed.
type Response struct {
	Status int
	// Header holds only the recorded allowlist -- see internal/cassette. A probe that
	// depended on a header the cassette drops would derive facts that could not be
	// re-derived offline, which CI would catch as a replay failure.
	Header map[string]string
	// Body is the parsed JSON body, or nil when the response had none. Parsed rather
	// than raw because every probe reads it as JSON, and because canonical
	// re-encoding is what makes a cassette diff reviewable.
	Body any
	// Raw is the response body as received, for the handful of observations that need
	// the bytes -- notably whether a 404 has an empty body at all.
	Raw []byte

	// Interaction is the cassette id of the request that produced this response, empty when
	// nothing was recording.
	//
	// A probe that cites this cites the exact request its conclusion rests on. The
	// alternative -- matching by method and path suffix -- is adequate for a read tier
	// issuing a handful of distinguishable requests and useless for a write tier issuing
	// forty POSTs to one collection, where every fact would cite all forty. A fact whose
	// evidence is "all of it" cannot be checked, and cannot be refuted.
	Interaction string
}

// ReadSession is the read-only half of a probe's access to an API.
//
// The interface is the enforcement. It has no method that can change anything, so a
// read-only probe that tries to mutate does not compile -- which is a stronger
// guarantee than a boolean somebody has to remember to check, and it cannot be
// weakened by adding a call inside a probe.
type ReadSession interface {
	// Get issues a GET. The path is one of the paths this session was built with;
	// a probe cannot point it elsewhere.
	Get(ctx context.Context, path string, query url.Values) (*Response, error)

	// CollectionPath and ItemPath are the two paths this session may address,
	// resolved from the blueprint's PathTemplate.
	CollectionPath() string
	ItemPath(id string) string
}

// ReadProbe observes an API without changing it.
type ReadProbe interface {
	// Name is the stable identifier used by -only, in facts and in reports.
	Name() string

	// Kind is always KindRead. Stated rather than assumed so -list and the runner
	// need no type switch.
	Kind() Kind

	// Cost is the worst-case number of requests this probe will issue against the
	// scope. It is what lets a plan be budgeted before anything is sent, and it is
	// why `probe -list` is useful with no credentials.
	//
	// Takes a Scope rather than a Subject so this number and what Observe actually does are
	// derived from the same narrowing. A cost computed over every writable field while the
	// probe iterated a plan's narrowed set would be wrong by an order of magnitude, and
	// nothing would catch it.
	Cost(Scope) int

	// Observe runs the probe. A nil error with no facts is a legitimate outcome: an
	// empty tenant, a field that cannot be read, an observation that came out
	// ambiguous. Absence of a fact is not failure.
	Observe(ctx context.Context, s ReadSession, sc Scope) (Result, error)
}

// MutatingProbe creates, updates or deletes objects.
//
// A separate interface from ReadProbe with a differently named method, rather than one
// interface with a Kind flag. Three things follow: a probe cannot be registered in the
// wrong list by accident, a read-only probe cannot be widened into a mutating one by
// adding a call, and the session type it receives can only be constructed from a Grant.
type MutatingProbe interface {
	Name() string
	Kind() Kind
	Cost(Scope) int

	// Creates is the worst-case number of objects this probe brings into existence.
	//
	// Required rather than optional, which it was until the cost model became scope-aware. An
	// optional interface meant a probe that forgot to implement it declared zero creates and
	// was budgeted as free -- the one mistake the blast-radius cap exists to catch.
	Creates(Scope) int

	// Exercise runs the probe. Every object it creates is recorded in the ledger
	// before the request is issued, and swept afterwards.
	Exercise(ctx context.Context, s *MutatingSession, sc Scope) (Result, error)
}

// Result is what one probe learned.
type Result struct {
	// Facts are the conclusions, each carrying its own evidence and confidence.
	Facts []Fact
	// Notes are the things this probe could not do, or could not decide. A probe that
	// abandons a protocol partway through -- because its control request failed, or
	// because two fixtures disagreed -- reports here rather than emitting a weak fact.
	Notes []Note
	// Requests counts what was actually issued, for the budget report.
	Requests int
	// Rehearsal is the derived bodies write.rehearsal actually sent, one entry per
	// executed round, carried out for freezing beside the cassette. Empty for every
	// other probe.
	Rehearsal []RehearsalRound
}

// registry holds every probe this build knows about.
//
// Two slices rather than one, because the two kinds are genuinely different types.
// Registration is at init time from catalogue.go.
var (
	readProbes     []ReadProbe
	mutatingProbes []MutatingProbe
)

// registerRead adds a read-only probe. Panics on a duplicate or empty name, because
// both are programming errors that would otherwise surface as a silently missing probe.
func registerRead(p ReadProbe) {
	mustBeNamed(p.Name())
	if _, ok := Lookup(p.Name()); ok {
		panic(fmt.Sprintf("probe: %q is registered twice", p.Name()))
	}
	readProbes = append(readProbes, p)
}

// registerMutating adds a mutating probe.
func registerMutating(p MutatingProbe) {
	mustBeNamed(p.Name())
	if _, ok := Lookup(p.Name()); ok {
		panic(fmt.Sprintf("probe: %q is registered twice", p.Name()))
	}
	mutatingProbes = append(mutatingProbes, p)
}

func mustBeNamed(name string) {
	if name == "" {
		panic("probe: a probe with no name was registered")
	}
}

// Entry is one catalogue entry, flattened so callers that only need to describe a
// probe do not have to care which interface it implements.
type Entry struct {
	Name string
	Kind Kind
	// Cost is the worst-case request count against a given subject.
	Cost int
	// Creates is the worst-case number of objects created. Zero for every read probe,
	// and the number the blast-radius budget is checked against.
	Creates int
}

// Catalogue describes every registered probe against a subject, ordered read-first
// and then by name.
//
// Read-first because that is the order they run in and the order a reader wants: the
// safe tier, then the tier that needs a sandbox and a flag.
func Catalogue(sc Scope) []Entry {
	out := make([]Entry, 0, len(readProbes)+len(mutatingProbes))

	for _, p := range readProbes {
		out = append(out, Entry{Name: p.Name(), Kind: KindRead, Cost: p.Cost(sc)})
	}
	for _, p := range mutatingProbes {
		out = append(out, Entry{
			Name:    p.Name(),
			Kind:    KindMutating,
			Cost:    p.Cost(sc),
			Creates: p.Creates(sc),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == KindRead
		}
		return out[i].Name < out[j].Name
	})

	return out
}

// Lookup finds a probe by name, in either list.
func Lookup(name string) (Entry, bool) {
	for _, p := range readProbes {
		if p.Name() == name {
			return Entry{Name: p.Name(), Kind: KindRead}, true
		}
	}
	for _, p := range mutatingProbes {
		if p.Name() == name {
			return Entry{Name: p.Name(), Kind: KindMutating}, true
		}
	}
	return Entry{}, false
}

// ReadProbes returns the registered read-only probes, filtered by name if only is
// non-empty.
func ReadProbes(only string) []ReadProbe {
	if only == "" {
		return readProbes
	}

	for _, p := range readProbes {
		if p.Name() == only {
			return []ReadProbe{p}
		}
	}

	return nil
}

// MutatingProbes returns the registered mutating probes, filtered by name if only is
// non-empty.
func MutatingProbes(only string) []MutatingProbe {
	if only == "" {
		return mutatingProbes
	}

	for _, p := range mutatingProbes {
		if p.Name() == only {
			return []MutatingProbe{p}
		}
	}

	return nil
}

// TotalCost sums the worst-case requests and creations for a set of probes.
//
// This is what a plan is budgeted against, and what -list prints. A run that would
// exceed its budget is refused before the first request rather than partway through.
func TotalCost(sc Scope, only string) (requests, creates int) {
	for _, e := range Catalogue(sc) {
		if only != "" && e.Name != only {
			continue
		}
		requests += e.Cost
		creates += e.Creates
	}
	return requests, creates
}
