package main

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/ingest/openapi"
)

// TestUnit_CLI_CommittedBlueprintMatchesTheSpecItWasInferredFrom is the gate that was missing.
//
// A blueprint is a curated document. Most of it deviates from a fresh inference on purpose: the
// pilot marks `key` required where the specification does not, and four attributes are
// optional-and-computed because the prober watched the API supply its own values. Re-inferring
// over the top would destroy all of that, which is why `ingest` writes a new file rather than
// updating one.
//
// The consequence went unnoticed for three phases. Probe facts have `merge` to fold new evidence
// into a curated blueprint, and `merge -check` to prove it was done. Specification facts have
// nothing. So when 5.4 taught inference to carry enum members into the IR, the pilot's blueprint
// stayed as it was, and nothing said so: it compiled, matched its own generated output, exported
// cleanly, and reproduced every probe fact.
//
// What that cost: `filters.mode`, `filters.scope` and `assignments.type` are enumerations in the
// pinned document and carried no allowed values in the blueprint, so the generated provider
// accepted any string for them, and the fixture generator had nothing to build a nested value
// from. It surfaced only when a live acceptance run needed a nested fixture and there was no way
// to derive one.
//
// Only genuinely spec-derived fields are compared. AllowedValues is the documented set on the type
// -- what a human believes the API accepts lives in Behaviour.AcceptedValues instead, which is why
// overriding this one would be overriding the document rather than curating it. Constraints are
// the same: a pattern or a bound is the document's claim.
//
// Deliberately not compared: ComputedOptionalRequired, naming, bindings and anything else a person
// or the prober decides. A drift check over those would fail on every deliberate decision and be
// switched off within a week.
func TestUnit_CLI_CommittedBlueprintMatchesTheSpecItWasInferredFrom(t *testing.T) {
	t.Parallel()

	committed, err := blueprint.LoadDir(filepath.Join(repoRoot, "blueprints", "thousandeyes"))
	if err != nil {
		t.Fatalf("loading the committed blueprint: %v", err)
	}

	doc, err := openapi.Load(newestSnapshot(t))
	if err != nil {
		t.Fatalf("loading the pinned specification: %v", err)
	}

	fresh := map[string]specFacts{}

	for _, c := range doc.Discover() {
		res, _, inferErr := doc.Infer(c, openapi.InferOptions{
			Provider:          committed.Provider.Name,
			SDKServiceRoot:    committed.Provider.SDK.ModulePath,
			SDKAccessorPrefix: "r.client.API",
			APIVersionDir:     "v7",
		})
		if inferErr != nil {
			// A candidate inference refuses is not this test's business; it only compares the
			// resources the blueprint actually declares.
			continue
		}

		collectSpecFacts(res.Schema.Attributes, res.Key, fresh)
	}

	for _, r := range committed.Resources {
		got := map[string]specFacts{}
		collectSpecFacts(r.Schema.Attributes, r.Key, got)

		for path, want := range got {
			if knownStale[path] {
				continue
			}

			inferred, ok := fresh[path]
			if !ok {
				// An attribute inference no longer produces -- a renamed field, or one the
				// blueprint added by hand. Not drift.
				continue
			}

			if !reflect.DeepEqual(want.allowed, inferred.allowed) {
				t.Errorf(
					"%s: allowedValues is %v in the blueprint and %v in the pinned "+
						"specification.\nAllowedValues is spec-derived; an observed set belongs "+
						"in behaviour.acceptedValues. Re-run ingest and fold the change in.",
					path, want.allowed, inferred.allowed,
				)
			}
			if !reflect.DeepEqual(want.constraints, inferred.constraints) {
				t.Errorf(
					"%s: constraints are %+v in the blueprint and %+v in the pinned "+
						"specification.\nRe-run ingest and fold the change in.",
					path, want.constraints, inferred.constraints,
				)
			}
		}
	}
}

// knownStale is the drift this gate found on the day it was written, and cannot fix yet.
//
// These three are enumerations in the pinned document that the committed blueprint does not carry.
// Applying them is a one-line change each. What makes it not a one-line change is that
// AllowedValues sizes the write.enum probe's plan: with the members present the probe wants 76
// requests where the committed cassette recorded far fewer, so `probe -mode verify` stops
// reproducing its 39 facts and starts reporting orphans. The blueprint cannot be corrected without
// re-recording the evidence.
//
// The re-record is deliberately waiting on something else. Manual exploration of this API found
// that its interesting facts are all conditional -- objectType decides which `type` is legal,
// `type` decides whether matchType and filters are required, matchType is silently discarded on a
// static tag -- and the fact set has nowhere to record a precondition. Re-recording now would
// re-measure the same unconditional half-truths over a larger enum set. See
// docs/findings/tag-conditional-structure.md.
//
// So the exception is listed rather than the gate being weakened. Any *other* spec drift fails
// here, which is the whole point: this gate exists because three fields went stale for three
// phases with nothing to say so.
var knownStale = map[string]bool{
	"tag.filters.mode":     true,
	"tag.filters.scope":    true,
	"tag.assignments.type": true,
}

// specFacts are the fields a specification decides and a curator should not.
type specFacts struct {
	allowed     []string
	constraints blueprint.Constraints
}

// collectSpecFacts indexes an attribute tree by dotted path, nested members included.
//
// Nested members are the point: the drift this exists to catch was three levels of enumeration
// inside two nested objects, and a check that walked only the top level would have missed all of
// it.
func collectSpecFacts(attrs []blueprint.Attribute, prefix string, out map[string]specFacts) {
	for _, a := range attrs {
		if a.Drop {
			continue
		}

		path := fmt.Sprintf("%s.%s", prefix, a.Name)
		out[path] = specFacts{allowed: a.Type.AllowedValues, constraints: a.Type.Constraints}

		if a.Type.NestedObject != nil {
			collectSpecFacts(a.Type.NestedObject.Attributes, path, out)
		}
	}
}

// newestSnapshot is the pinned document the committed blueprint was inferred from.
func newestSnapshot(t *testing.T) string {
	t.Helper()

	root := filepath.Join(repoRoot, "openapi-specs", "thousandeyes")

	matches, err := filepath.Glob(filepath.Join(root, "*", "api.yaml"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no pinned snapshot under %s: %v", root, err)
	}

	// One snapshot is pinned at a time; more than one and the newest by name wins, which is how
	// the store orders them.
	newest := matches[0]
	for _, m := range matches {
		if m > newest {
			newest = m
		}
	}

	return newest
}
