package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint/merge"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/fixturespec"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
)

// rehearsalDerive builds the derive closure the rehearsal probe's fixpoint loop calls.
//
// The loop lives here rather than in the probe because deriving a round means merging
// the facts gathered so far into the blueprint and reading the result -- and cmd owns
// blueprint and merge, while probe must never import merge (merge imports probe).
func rehearsalDerive(
	bp blueprint.Blueprint,
	key string,
	plan probe.Plan,
) func([]probe.Fact) (probe.RehearsalRound, error) {
	return func(facts []probe.Fact) (probe.RehearsalRound, error) {
		return rehearsalBodies(bp, key, plan, facts)
	}
}

// rehearsalBodies derives the wire bodies the generated fixtures would apply, with
// the given facts folded into an in-memory copy of the blueprint first.
//
// The copy goes through Marshal/Unmarshal rather than a hand-written deep copy: the
// blueprint is a large evolving struct, and a copy that silently missed a new field
// would rehearse a body subtly different from the one emit derives.
func rehearsalBodies(
	bp blueprint.Blueprint,
	key string,
	plan probe.Plan,
	facts []probe.Fact,
) (probe.RehearsalRound, error) {
	// Narrowed to the one resource before the round-trip: Unmarshal validates, and a
	// blueprint carrying data sources whose seeds name resources outside this run's
	// scope is valid as a whole document and invalid as a slice of one.
	narrow := blueprint.Blueprint{
		FormatVersion: bp.FormatVersion,
		Provider:      bp.Provider,
		Source:        bp.Source,
	}
	for _, r := range bp.Resources {
		if r.Key == key {
			narrow.Resources = []blueprint.Resource{r}
			break
		}
	}
	if len(narrow.Resources) == 0 {
		return probe.RehearsalRound{}, fmt.Errorf("no resource %q in the blueprint", key)
	}

	data, err := blueprint.Marshal(narrow)
	if err != nil {
		return probe.RehearsalRound{}, err
	}
	working, err := blueprint.Unmarshal(data)
	if err != nil {
		return probe.RehearsalRound{}, err
	}

	if len(facts) > 0 {
		// Apply, so a corroborated server default promotes presence exactly as the
		// real merge will. Conflicts are the blueprint disagreeing with this run's
		// facts; the rehearsal proceeds on the merged view either way, because the
		// body it should send is the one the post-merge emit would derive.
		if _, err := merge.Apply(&working, facts, merge.Options{
			Strategy: merge.StrategyApply,
		}); err != nil {
			return probe.RehearsalRound{}, err
		}
	}

	var res *blueprint.Resource
	for i := range working.Resources {
		if working.Resources[i].Key == key {
			res = &working.Resources[i]
			break
		}
	}
	if res == nil {
		return probe.RehearsalRound{}, fmt.Errorf("no resource %q in the blueprint", key)
	}

	return probe.RehearsalRound{
		Minimal: wireBody(*res, plan, true),
		Maximal: wireBody(*res, plan, false),
	}, nil
}

// wireBody derives one fixture's wire form: JSONPath-keyed values chosen exactly as
// the generated HCL fixture chooses them, in fixturespec.
func wireBody(res blueprint.Resource, plan probe.Plan, minimal bool) map[string]any {
	body := map[string]any{}

	for _, a := range res.Schema.Attributes {
		if a.Drop || !fixturespec.Wants(a, minimal) {
			continue
		}
		// An optional immutable attribute stays out of the maximal body for the same
		// reason it stays out of the maximal fixture: introducing it at update forces
		// a replacement in the middle of an in-place experiment.
		if !minimal && fixturespec.Immutable(a) && !fixturespec.Wants(a, true) {
			continue
		}

		path := a.Wire.JSONPath
		if path == "" || strings.Contains(path, ".") {
			// A nested path is carried by its top-level parent; only the parent can
			// be a body key.
			continue
		}

		if v, ok := hintWire(res, a.Name); ok {
			if unsendableZero(a, v) {
				continue
			}
			body[path] = v
			continue
		}

		if e := fixturespec.Derive(a, ""); !e.Skipped {
			if unsendableZero(a, e.Value) {
				continue
			}
			body[path] = e.Value
			continue
		}

		// The generator refused a value -- credential-shaped, a live reference, a
		// pattern. The plan's fixture is the operator's stated valid body, so its
		// value for this key is the closest thing to what a curated hint will carry.
		if v, ok := planValue(plan, path); ok {
			body[path] = v
		}
	}

	return body
}

// unsendableZero reports whether the generated provider's encoder would drop this
// value before it travels: a zero on a field the static facts marked omitempty.
//
// The rehearsal sends raw JSON, so it *can* send false where the SDK cannot -- and a
// body that carries what the provider never will rehearses an encoding that does not
// exist. The live proof: sending networkMeasurements true with bandwidthMeasurements
// false travels fine raw and passes, while the SDK drops the false and the same
// update is refused. SDK-faithful omission is what lets the rehearsal meet that
// refusal before an acceptance run does.
func unsendableZero(a blueprint.Attribute, v any) bool {
	z := a.Behaviour.ZeroValueUnsendable
	if z == nil || !*z {
		return false
	}

	switch tv := v.(type) {
	case bool:
		return !tv
	case string:
		return tv == ""
	case float64:
		return tv == 0
	case int64:
		return tv == 0
	default:
		return false
	}
}

// hintWire resolves a curated accFixture hint to a wire value: the declared Wire form
// when the hint carries one, else a trivially-parseable HCL literal. A reference into
// a data block has no wire form and resolves to nothing -- its value exists only at
// apply time.
func hintWire(res blueprint.Resource, attr string) (any, bool) {
	if res.AccFixture == nil {
		return nil, false
	}

	for _, h := range res.AccFixture.Values {
		if h.Attr != attr {
			continue
		}
		if h.Wire != nil {
			return h.Wire, true
		}
		return scalarHCLValue(h.HCL)
	}

	return nil, false
}

// scalarHCLValue parses the HCL literals a hint commonly holds -- a quoted string, a
// bool, a number -- without an HCL dependency. Anything else is not a wire value.
func scalarHCLValue(hcl string) (any, bool) {
	s := strings.TrimSpace(hcl)

	if unquoted, err := strconv.Unquote(s); err == nil && strings.HasPrefix(s, `"`) {
		return unquoted, true
	}
	if b, err := strconv.ParseBool(s); err == nil {
		return b, true
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i, true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}

	return nil, false
}

// planValue reads a top-level key out of the first plan fixture that sets it.
func planValue(plan probe.Plan, path string) (any, bool) {
	for _, f := range plan.Fixtures {
		if v, ok := f.Body[path]; ok {
			return v, true
		}
	}
	return nil, false
}

// frozenRehearsal loads a snapshot's frozen rehearsal rounds, nil when the recording
// predates the probe -- the same degradation the frozen subject uses, and the probe
// states the skip itself.
func frozenRehearsal(snap cassette.Snapshot) (*probe.RehearsalConfig, error) {
	data, err := os.ReadFile(snap.RehearsalPath()) //nolint:gosec // snapshot path by construction
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", snap.RehearsalPath(), err)
	}

	var rounds []probe.RehearsalRound
	if err := json.Unmarshal(data, &rounds); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", snap.RehearsalPath(), err)
	}

	return &probe.RehearsalConfig{Rounds: rounds}, nil
}
