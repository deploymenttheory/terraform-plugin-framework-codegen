// Package plan derives what an audit run would do, without doing any of
// it. Derive is a pure function of the loaded document, the config and the
// operator inputs: no network, no clock, no randomness. The same three
// inputs always produce the same Plan, byte for byte — which is what makes
// the plan printable for review before anything credentialed runs, and
// what makes the execution half (a later stage) accountable to it.
//
// A plan is ordered: parents before the children whose paths embed them,
// resources before the lookup datasources that only read. Each entity
// carries its ordered step list and a request budget; the plan carries the
// run-wide budgets. Values the steps will send are synthesized here, from
// the operator's inputs first and the document second; the one thing the
// plan cannot know offline — the run id that names created objects — is
// carried as the RunIDToken placeholder for execution to substitute.
package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
)

// RunIDToken is the placeholder a synthesized name carries where the run
// id belongs. Derive never knows the run id — substituting it at execution
// time is what keeps Derive deterministic.
const RunIDToken = "<runid>"

// envRefOpen and envRefClose delimit an operator input whose value is
// read from the named environment variable at execution time — the token
// is ${VAR} — so audit/inputs.json can be committed without committing
// the value.
const (
	envRefOpen  = "${"
	envRefClose = "}"
)

// createdRefPrefix marks a path value that is the id of an object an
// earlier entity's createMinimal step made. See CreatedRef.
const createdRefPrefix = "$created:"

// CreatedRef renders the path-value token for an object the audit itself
// creates: the id of the named entity's minimal object. Execution
// substitutes the live id; the plan only records the dependency, which is
// also what the entity ordering is derived from.
func CreatedRef(entity string) string { return createdRefPrefix + entity }

// StepKind names one derivation rule's output. The set is closed and the
// order below is the order steps appear in an entity's list.
type StepKind string

const (
	// StepCreateMinimal creates the smallest valid object: required
	// writable fields only. Everything later reads, mutates or deletes
	// this object unless it says otherwise.
	StepCreateMinimal StepKind = "createMinimal"
	// StepReadWithRetry polls the item read until it shows the created object,
	// within Poll. Learns readAfterWrite; its per-field comparison learns
	// writable, serverDefault and normalisation.
	StepReadWithRetry StepKind = "readWithRetry"
	// StepReadConsecutive reads the same object twice and compares. Learns
	// volatile.
	StepReadConsecutive StepKind = "readConsecutive"
	// StepUpdateField updates exactly one attribute — Attribute — to the
	// variant value the body carries, then re-reads. Learns immutable,
	// ignoredOnUpdate, serverForced and updateStyle.
	StepUpdateField StepKind = "updateField"
	// StepDeleteWithConfirmation deletes the minimal object, confirms it is
	// gone, and deletes again. Learns deleteNotFoundOK.
	StepDeleteWithConfirmation StepKind = "deleteWithConfirmation"
	// StepCreateMaximal creates an object with every writable field
	// populated. When it is refused, execution may spend up to
	// BisectionAllowance extra creates halving the optional set to name
	// the field the API objects to.
	StepCreateMaximal StepKind = "createMaximal"
	// StepOmitRequired sends the minimal body minus the one required
	// field named by Attribute, expecting refusal. Learns requiredByAPI.
	StepOmitRequired StepKind = "omitRequired"
	// StepUndocumentedEnumValue sends the minimal body with the enum
	// attribute set to a value the document does not list. Learns whether
	// the documented set is closed.
	StepUndocumentedEnumValue StepKind = "undocumentedEnumValue"
	// StepUndeclaredSpecField sends the minimal body plus one field no schema
	// declares. Learns whether the API rejects unknown body fields, which
	// the summary reports as rejectsUnknownFields — an API that rejects
	// them discriminates differently in every omission-based check than
	// one that ignores them.
	StepUndeclaredSpecField StepKind = "undeclaredSpecField"
	// StepCreatePerEnumValue creates an object with the Condition's
	// attribute pinned to one value, to observe value-conditional
	// behaviour; the object is read back and deleted within the step.
	// When Attribute is set and absent from the body, the omission is the
	// check — a requiredWhen hint being tested.
	StepCreatePerEnumValue StepKind = "createPerEnumValue"
	// StepRead is a lookup datasource's read: fetch by the operator-
	// supplied key and confirm the response decodes.
	StepRead StepKind = "read"
	// StepCleanupDelete removes whatever the entity's later creates left
	// behind, ending the entity at zero live objects.
	StepCleanupDelete StepKind = "cleanupDelete"
)

// Poll bounds a readWithRetry: try every Interval until Timeout. Durations are
// strings in Go duration syntax, as committed artefacts spell them.
type Poll struct {
	Interval string `json:"interval"`
	Timeout  string `json:"timeout"`
}

// Step is one derived action. Method and Path name the operation as the
// document spells it; PathValues carries a value per path parameter — a
// literal, a ${VAR} environment reference, or a CreatedRef token.
type Step struct {
	Kind   StepKind `json:"kind"`
	Method string   `json:"method"`
	Path   string   `json:"path"`
	// Attribute is the wire property under test, for per-field steps.
	Attribute string `json:"attribute,omitempty"`
	// Condition scopes a createPerEnumValue, in the same vocabulary the
	// resulting observation will carry.
	Condition *observe.Condition `json:"condition,omitempty"`
	// Body is the synthesized request body, nil for bodiless steps.
	Body map[string]any `json:"body,omitempty"`
	// PathValues maps each path parameter to its value or token.
	PathValues map[string]string `json:"pathValues,omitempty"`
	// Poll bounds a readWithRetry step.
	Poll *Poll `json:"poll,omitempty"`
	// FieldNarrowingAttemptLimit is the extra createMaximal attempts execution may
	// spend narrowing a refusal; zero elsewhere.
	FieldNarrowingAttemptLimit int `json:"fieldNarrowingAttemptLimit,omitempty"`
}

// Budget is what one entity's steps may spend.
type Budget struct {
	Requests int `json:"requests"`
}

// RunBudget bounds the whole run. Requests and Duration derive from the
// entity count and the configured rate limit; Objects is the configured
// audit.max_objects ceiling on simultaneously live created objects.
type RunBudget struct {
	Requests int    `json:"requests"`
	Objects  int    `json:"objects"`
	Duration string `json:"duration"`
}

// EntityPlan is one entity's derived audit.
type EntityPlan struct {
	Entity string `json:"entity"`
	// AuditShape is "resource" for a full lifecycle, "lookupByKey" for a datasource
	// that is only read.
	AuditShape string `json:"auditShape"`
	// Parents lists the entity keys whose created objects this entity's
	// paths embed, in path order. Every parent appears earlier in the
	// plan.
	Parents []string `json:"parents,omitempty"`
	// DeclaredProperties is the sorted union of the property names the
	// entity's create request and read response schemas declare. Execution
	// diffs read responses against it to observe undocumentedFieldInSpec —
	// a real field the API carries that the spec omits. Empty means the
	// schemas are unknown and the diff is skipped.
	DeclaredProperties []string `json:"declaredProperties,omitempty"`
	Budget             Budget   `json:"budget"`
	Steps              []Step   `json:"steps"`
}

// Skipped is one entity the plan leaves out, and why — surfaced rather
// than silently dropped, so a surprising omission traces to a reason.
type Skipped struct {
	Entity string `json:"entity"`
	Reason string `json:"reason"`
}

// Plan is everything a run would do.
type Plan struct {
	Entities []EntityPlan `json:"entities"`
	Skipped  []Skipped    `json:"skipped,omitempty"`
	Budget   RunBudget    `json:"budget"`
}

// JSON renders the plan for printing and for committing beside a run:
// two-space indented, HTML escaping off, trailing newline. Deterministic
// for a given plan — struct fields encode in declaration order and maps
// encode sorted.
func (p *Plan) JSON() ([]byte, error) {
	var buffer bytes.Buffer
	enc := json.NewEncoder(&buffer)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(p); err != nil {
		return nil, fmt.Errorf("encoding the plan: %w", err)
	}
	return buffer.Bytes(), nil
}

// nearest returns the candidate closest to got, or "" when nothing is
// close enough to be a plausible typo. The same rule config uses for its
// unknown keys.
func nearest(got string, candidates []string) string {
	best, bestDist := "", len(got)/2+1
	for _, c := range candidates {
		if d := levenshtein(got, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// didYouMean renders the suggestion suffix, empty when there is none.
func didYouMean(got string, candidates []string) string {
	if best := nearest(got, candidates); best != "" {
		return fmt.Sprintf(" (did you mean %q?)", best)
	}
	return ""
}

// levenshtein is the standard two-row edit distance.
func levenshtein(a, b string) int {
	previous := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(previous[j]+1, min(curr[j-1]+1, previous[j-1]+cost))
		}
		previous, curr = curr, previous
	}
	return previous[len(b)]
}

// isEnvRef reports whether an operator value is a ${VAR} environment
// reference.
func isEnvRef(v string) bool {
	return strings.HasPrefix(v, envRefOpen) && strings.HasSuffix(v, envRefClose)
}

// OnlyEntities narrows the plan to the named entities and the parents their
// paths embed; every other entity moves to Skipped with the reason. A name
// that classifies as nothing is refused with a did-you-mean, because a typo
// here would silently audit nothing.
//
// Parents come along because a child addresses its paths through an object
// the parent's own recipe creates: without the parent in the plan the child
// has nothing to resolve `$created:<parent>` against, and blocks before its
// first request.
func (p *Plan) OnlyEntities(names []string) error {
	if len(names) == 0 {
		return nil
	}
	known := make([]string, 0, len(p.Entities))
	byKey := make(map[string]EntityPlan, len(p.Entities))
	for _, ep := range p.Entities {
		known = append(known, ep.Entity)
		byKey[ep.Entity] = ep
	}
	keep := map[string]bool{}
	var walk func(name string) error
	walk = func(name string) error {
		ep, ok := byKey[name]
		if !ok {
			return fmt.Errorf("--entity %q names no entity the plan carries%s", name, didYouMean(name, known))
		}
		if keep[name] {
			return nil
		}
		keep[name] = true
		for _, parent := range ep.Parents {
			if err := walk(parent); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range names {
		if err := walk(name); err != nil {
			return err
		}
	}
	kept := make([]EntityPlan, 0, len(keep))
	for _, ep := range p.Entities {
		if keep[ep.Entity] {
			kept = append(kept, ep)
			continue
		}
		p.Skipped = append(p.Skipped, Skipped{Entity: ep.Entity, Reason: "not named by --entity"})
	}
	p.Entities = kept
	return nil
}
