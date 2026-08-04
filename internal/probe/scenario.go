package probe

import (
	"errors"
	"fmt"
)

// Scenario is the operator-supplied knowledge a probe cannot discover.
//
// The prober can find out what an API does to values it is given. It cannot invent a
// valid object, know which fields co-vary, or guess which alternative value for an enum
// is acceptable. That knowledge is handed over here, and the split matters: the
// prober's job is to *check* what a human declares, not to discover it. Cross-field
// constraints in particular are out of reach without this -- one-field-at-a-time
// perturbation cannot find "port is required unless protocol is icmp" without being
// told the two are related.
type Scenario struct {
	// Fixtures are complete, valid request bodies. At least one is required for any
	// mutating probe.
	//
	// More than one is what makes conditional requirement detectable: a field whose
	// requiredness differs between two fixtures produces a note rather than a fact,
	// because one-field-at-a-time omission from a single fixture reports half a truth
	// either way.
	Fixtures []Fixture `json:"fixtures,omitempty"`

	// Candidates are alternative valid values per JSON path, used by the immutability
	// and enum protocols.
	//
	// The immutability protocol needs a second value it can prove is acceptable --
	// that is what separates "the field is immutable" from "the value was invalid" --
	// so a field with no candidate here cannot be probed for immutability at all.
	Candidates map[string][]any `json:"candidates,omitempty"`

	// DefaultInfluencers are the JSON paths whose value might determine another
	// field's server-assigned default. Varying them across fixtures is how a derived
	// default is told apart from a constant one.
	DefaultInfluencers []string `json:"defaultInfluencers,omitempty"`

	// Expansions are query parameters that cause extra fields to be returned, e.g.
	// "expand=assignments".
	//
	// Without these, a probe that reads back once will conclude an expansion-gated
	// field is never returned, and the generated state mapper will then blank a real
	// value on every refresh. Any API with an `expand`, `include` or `fields`
	// parameter has this shape.
	Expansions []string `json:"expansions,omitempty"`

	// Deny lists JSON paths no probe may send a value for, whatever else it is told.
	// The escape hatch for a field that is writable and has consequences -- a webhook
	// target, a notification address.
	Deny []string `json:"deny,omitempty"`

	Budget Budget `json:"budget,omitzero"`
}

// Fixture is one complete, valid request body plus a label.
type Fixture struct {
	// Name identifies the fixture in reports and notes.
	Name string `json:"name"`
	// Body is a complete create request, as JSON. Hand-built as a map rather than
	// generated from the schema, because a schema-valid body is frequently not an
	// API-valid one.
	Body map[string]any `json:"body"`
}

// Budget caps the blast radius. Every field has a default, because a plan that forgets
// one must not thereby become unlimited.
type Budget struct {
	// MaxRequests caps total requests, mutating or not.
	MaxRequests int `json:"maxRequests,omitempty"`
	// MaxCreates caps objects created. The cap that actually matters: requests are
	// cheap and objects are not.
	MaxCreates int `json:"maxCreates,omitempty"`
	// MaxWallClockSeconds bounds the whole run, enforced as a context deadline.
	MaxWallClockSeconds int `json:"maxWallClockSeconds,omitempty"`
	// MaxSweepSeconds bounds the sweep, separately from the run. Separate because the
	// commonest reason to be sweeping is that the run's own deadline expired, and a sweep
	// sharing that deadline would be dead before its first request.
	MaxSweepSeconds int `json:"maxSweepSeconds,omitempty"`
	// MaxDeleteFailures is deliberately defaulted to zero: the moment a delete fails,
	// the run stops creating anything new and proceeds to sweep. Continuing to create
	// after demonstrating you cannot clean up is the worst available behaviour.
	MaxDeleteFailures int `json:"maxDeleteFailures,omitempty"`
}

// Budget defaults. Chosen to be enough for the full catalogue against one resource and
// nowhere near enough to disturb a tenant.
const (
	defaultMaxRequests         = 200
	defaultMaxCreates          = 25
	defaultMaxWallClockSeconds = 600
)

// WithDefaults fills unset caps. Returns a copy; a plan read from disk is not mutated.
func (b Budget) WithDefaults() Budget {
	if b.MaxRequests <= 0 {
		b.MaxRequests = defaultMaxRequests
	}
	if b.MaxCreates <= 0 {
		b.MaxCreates = defaultMaxCreates
	}
	if b.MaxWallClockSeconds <= 0 {
		b.MaxWallClockSeconds = defaultMaxWallClockSeconds
	}
	// MaxDeleteFailures is not defaulted: zero is the intended value, and treating
	// zero as "unset" would turn the safest setting into the one you cannot express.
	return b
}

// ErrInvalidPlan marks a probe plan that cannot be used.
var ErrInvalidPlan = errors.New("invalid probe plan")

// Validate checks a plan against the subject it will run on.
//
// Collects every problem rather than stopping at the first, following
// blueprint.Validate: a plan is hand-authored, and being told about one missing field
// at a time is a poor way to write one.
func (p Scenario) Validate(subj Subject) error {
	var problems []string

	for i, f := range p.Fixtures {
		if f.Name == "" {
			problems = append(problems, fmt.Sprintf("fixtures[%d].name: is required", i))
		}
		if len(f.Body) == 0 {
			problems = append(problems, fmt.Sprintf("fixtures[%s].body: is required", f.Name))
		}
		for key := range f.Body {
			if _, ok := subj.Field(key); !ok {
				// A fixture key the schema does not know is nearly always a typo, and
				// a typo here silently omits the field it meant to set -- which then
				// looks like a server default.
				problems = append(problems, fmt.Sprintf(
					"fixtures[%s].body[%s]: no attribute has this JSON path", f.Name, key))
			}
		}
	}

	for path := range p.Candidates {
		if _, ok := subj.Field(path); !ok {
			problems = append(
				problems,
				fmt.Sprintf("candidates[%s]: no attribute has this JSON path", path),
			)
		}
	}

	for _, path := range p.DefaultInfluencers {
		if _, ok := subj.Field(path); !ok {
			problems = append(problems, fmt.Sprintf(
				"defaultInfluencers[%s]: no attribute has this JSON path", path))
		}
	}

	for _, path := range p.Deny {
		if _, ok := subj.Field(path); !ok {
			problems = append(
				problems,
				fmt.Sprintf("deny[%s]: no attribute has this JSON path", path),
			)
		}
	}

	if len(problems) == 0 {
		return nil
	}

	msg := fmt.Sprintf("%d problem(s):", len(problems))
	for _, p := range problems {
		msg += "\n  " + p
	}

	return fmt.Errorf("%w: %s", ErrInvalidPlan, msg)
}

// Denied reports whether a JSON path is on the deny list.
func (p Scenario) Denied(jsonPath string) bool {
	for _, d := range p.Deny {
		if d == jsonPath {
			return true
		}
	}
	return false
}

// CandidatesFor returns the alternative values declared for a path.
func (p Scenario) CandidatesFor(jsonPath string) []any {
	return p.Candidates[jsonPath]
}
