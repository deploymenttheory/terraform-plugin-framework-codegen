package curated_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/curated"
)

// TestUnit_Curated_TheSetLoads is the floor: the embedded fixture materialises,
// parses and validates.
//
// blueprint.LoadDir validates the merged document, so a green run here already
// means every cross-block rule holds -- duplicate type names, duplicate import
// aliases, a list facet without an identity. Those are the rules a hand-authored
// fixture is most likely to break, and they are invisible to a per-file check.
func TestUnit_Curated_TheSetLoads(t *testing.T) {
	t.Parallel()

	bp := curated.Blueprint(t)

	if got, want := bp.Provider.Name, "nimbus"; got != want {
		t.Errorf("provider name = %q, want %q", got, want)
	}
	if got, want := len(bp.Resources), 3; got != want {
		t.Errorf("resources = %d, want %d", got, want)
	}
	if len(bp.DataSources) != 1 || len(bp.Actions) != 1 || len(bp.Ephemerals) != 1 {
		t.Errorf("the fixture must carry one of each read-only kind; got %d data source(s), "+
			"%d action(s), %d ephemeral(s)",
			len(bp.DataSources), len(bp.Actions), len(bp.Ephemerals))
	}
}

// TestUnit_Curated_DirIsAPrivateCopy proves a test can write into the fixture
// without the next test seeing it.
//
// The fixture is embedded and therefore read-only, so the only way a test could
// corrupt it for another is by sharing the materialised directory. Two calls
// returning different paths is what makes Dir safe to hand to a command that
// writes.
func TestUnit_Curated_DirIsAPrivateCopy(t *testing.T) {
	t.Parallel()

	first, second := curated.Dir(t), curated.Dir(t)
	if first == second {
		t.Fatalf("two calls returned the same directory: %s", first)
	}

	extra := filepath.Join(first, "resources", "scribble.blueprint.json")
	if err := os.WriteFile(extra, []byte("{}"), 0o600); err != nil {
		t.Fatalf("writing into the materialised set: %v", err)
	}
	if _, err := os.Stat(filepath.Join(second, "resources", "scribble.blueprint.json")); !os.IsNotExist(err) {
		t.Errorf("a write into one copy reached the other: %v", err)
	}
}

// TestUnit_Curated_TheSetRoundTrips re-serialises the loaded document and reloads
// it, and requires the second load to agree with the first.
//
// This is the property the committed form depends on. Blueprints are regenerated
// and diffed by CI, so a fixture that does not survive Marshal followed by
// Unmarshal would make every drift check downstream of it meaningless -- and the
// failure would surface somewhere unrelated to the fixture.
func TestUnit_Curated_TheSetRoundTrips(t *testing.T) {
	t.Parallel()

	bp := curated.Blueprint(t)

	data, err := blueprint.Marshal(bp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	again, err := blueprint.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	second, err := blueprint.Marshal(again)
	if err != nil {
		t.Fatalf("Marshal, second time: %v", err)
	}

	if string(data) != string(second) {
		t.Error("the fixture is not a fixed point of Marshal/Unmarshal")
	}
}

// TestUnit_Curated_TheCuratedLayerIsPresent is the test this package exists for.
//
// Every feature asserted here is one that drafting an OpenAPI document never
// produces: it arrives from probing or from a person, and it is what the state
// mapper, the expand and flatten helpers and the conditional paths are generated
// from. Without this test the fixture could be trimmed back to something a draft
// would have produced and keep passing, at which point it proves nothing and
// nobody would notice.
func TestUnit_Curated_TheCuratedLayerIsPresent(t *testing.T) {
	t.Parallel()

	bp := curated.Blueprint(t)

	seen := map[string]string{}
	note := func(feature, where string) {
		if _, already := seen[feature]; !already {
			seen[feature] = where
		}
	}

	var walk func(where string, attrs []blueprint.Attribute)
	walk = func(where string, attrs []blueprint.Attribute) {
		for _, a := range attrs {
			at := where + "." + a.Name
			b := a.Behaviour

			if b.Writable != nil {
				note("behaviour.writable", at)
			}
			if b.Immutable != nil {
				note("behaviour.immutable", at)
			}
			if b.RequiredByAPI != nil {
				note("behaviour.requiredByApi", at)
			}
			if b.ServerDefault != nil {
				note("behaviour.serverDefault", at)
			}
			if b.ReturnedOnRead != nil {
				note("behaviour.returnedOnRead", at)
			}
			if b.ReturnedOnCreate != nil {
				note("behaviour.returnedOnCreate", at)
			}
			if b.ZeroValueUnsendable != nil {
				note("behaviour.zeroValueUnsendable", at)
			}
			if b.Volatile != nil {
				note("behaviour.volatile", at)
			}
			if len(b.AcceptedValues) > 0 {
				note("behaviour.acceptedValues", at)
			}
			if len(b.RejectedValues) > 0 {
				note("behaviour.rejectedValues", at)
			}
			if b.ValuesClosed != nil {
				note("behaviour.valuesClosed", at)
			}
			if len(b.Conditional) > 0 {
				note("behaviour.conditional", at)
				for _, v := range b.Conditional {
					if len(v.When) > 1 {
						note("behaviour.conditional (conjunctive)", at)
					}
				}
			}

			if len(a.Type.AllowedValues) > 0 {
				note("type.allowedValues", at)
			}
			if a.Type.Constraints.Minimum != nil {
				note("constraints.minimum", at)
			}
			if a.Type.Constraints.Maximum != nil {
				note("constraints.maximum", at)
			}
			if a.Type.Constraints.MinLength != nil || a.Type.Constraints.MaxLength != nil {
				note("constraints.length", at)
			}
			if a.Type.Constraints.Pattern != "" {
				note("constraints.pattern", at)
			}

			if a.Wire.SkipFlatten {
				note("wire.skipFlatten", at)
			}
			if a.Wire.SkipExpand {
				note("wire.skipExpand", at)
			}
			for _, c := range []*blueprint.ConvertCall{a.Wire.Expand, a.Wire.Flatten} {
				if c == nil {
					continue
				}
				if len(c.TypeArgs) > 0 {
					note("convert.typeArgs", at)
				}
				if c.NeedsCtx {
					note("convert.needsCtx", at)
				}
				if c.ReturnsError {
					note("convert.returnsError", at)
				}
			}

			if a.Type.NestedObject != nil {
				note("type.nestedObject", at)
				walk(at, a.Type.NestedObject.Attributes)
			}
		}
	}

	for _, r := range bp.Resources {
		walk("resources."+r.Key, r.Schema.Attributes)

		if r.Hooks.ModifyPlan {
			note("hooks.modifyPlan", r.Key)
		}
		if r.Hooks.ReadBackPredicate {
			note("hooks.readBackPredicate", r.Key)
		}
		if r.Policy.UpdateStyle != "" {
			note("policy.updateStyle", r.Key)
		}
		if r.Policy.UpdateStyle == blueprint.UpdatePutFull {
			note("policy.updateStyle putFull", r.Key)
		}
		if r.Policy.UpdateStyle == blueprint.UpdateReplaceOnly {
			note("policy.updateStyle replaceOnly", r.Key)
		}
		if r.AccFixture != nil {
			note("accFixture", r.Key)
		}
		if r.Identity != nil {
			note("identity", r.Key)
		}
		if r.List != nil {
			note("list facet", r.Key)
		}
		if r.Sweep != nil {
			note("sweep", r.Key)
		}

		for _, op := range []*blueprint.Operation{
			r.Binding.Create, r.Binding.Read, r.Binding.Update, r.Binding.Delete,
		} {
			if op != nil && len(op.SuccessCodes) > 0 {
				note("operation.successCodes", r.Key)
			}
		}
	}

	for _, d := range bp.DataSources {
		walk("dataSources."+d.Key, d.Schema.Attributes)
		note("data source", d.Key)
		if len(d.Binding.Selectors) > 0 {
			note("dataSource.selectors", d.Key)
		}
	}
	for _, e := range bp.Ephemerals {
		walk("ephemerals."+e.Key, e.Schema.Attributes)
		note("ephemeral", e.Key)
	}
	for _, a := range bp.Actions {
		walk("actions."+a.Key, a.Schema.Attributes)
		note("action", a.Key)
		if a.AccTest != nil && a.AccTest.Cleanup != nil {
			note("action.cleanup", a.Key)
		}
	}

	required := []string{
		"behaviour.writable",
		"behaviour.immutable",
		"behaviour.requiredByApi",
		"behaviour.serverDefault",
		"behaviour.returnedOnRead",
		"behaviour.returnedOnCreate",
		"behaviour.zeroValueUnsendable",
		"behaviour.volatile",
		"behaviour.acceptedValues",
		"behaviour.rejectedValues",
		"behaviour.valuesClosed",
		"behaviour.conditional",
		"behaviour.conditional (conjunctive)",
		"type.allowedValues",
		"type.nestedObject",
		"constraints.minimum",
		"constraints.maximum",
		"constraints.length",
		"constraints.pattern",
		"wire.skipFlatten",
		"wire.skipExpand",
		"convert.typeArgs",
		"convert.needsCtx",
		"convert.returnsError",
		"operation.successCodes",
		"hooks.modifyPlan",
		"hooks.readBackPredicate",
		"policy.updateStyle putFull",
		"policy.updateStyle replaceOnly",
		"accFixture",
		"identity",
		"list facet",
		"sweep",
		"data source",
		"dataSource.selectors",
		"ephemeral",
		"action",
		"action.cleanup",
	}

	for _, feature := range required {
		if _, ok := seen[feature]; !ok {
			t.Errorf("the fixture no longer carries %s, which is one of the reasons it exists; "+
				"restore it rather than deleting this assertion", feature)
		}
	}
}

// TestUnit_Curated_TheConditionalGatesAreReal checks the hardest curated feature
// in the detail that makes it worth having.
//
// A variant is only meaningful if its precondition names a wire path the schema
// actually has and if two variants of one attribute disagree about something --
// that disagreement is what "unknown unconditionally" is derived from, and it is
// what caught the pilot suppressing an attribute's read-back for every tag on the
// strength of an observation about static ones. Validation already refuses a gate
// on an unknown path, so what is asserted here is the disagreement.
func TestUnit_Curated_TheConditionalGatesAreReal(t *testing.T) {
	t.Parallel()

	bp := curated.Blueprint(t)

	var disagreed bool

	for _, r := range bp.Resources {
		for _, a := range r.Schema.Attributes {
			variants := a.Behaviour.Conditional
			if len(variants) < 2 {
				continue
			}

			answers := map[bool]string{}
			for _, v := range variants {
				if v.Behaviour.ReturnedOnRead != nil {
					answers[*v.Behaviour.ReturnedOnRead] = v.WhenKey()
				}
			}
			if len(answers) == 2 {
				disagreed = true
			}
			if a.Behaviour.ReturnedOnRead != nil {
				t.Errorf("resources.%s.%s states returnedOnRead unconditionally as well as per "+
					"variant; the base field must stay unset when variants disagree", r.Key, a.Name)
			}
		}
	}

	if !disagreed {
		t.Error("no attribute has two variants disagreeing about returnedOnRead, which is the " +
			"case conditional behaviour exists for")
	}
}

// TestUnit_Curated_TheFixtureIsVendorNeutral guards the property that let this be
// committed in the first place.
//
// The structural shapes came from blueprints drafted against a pinned GitHub
// document, and a rename that misses one leaves a third party's name in a file
// this repository presents as its own artefact.
func TestUnit_Curated_TheFixtureIsVendorNeutral(t *testing.T) {
	t.Parallel()

	forbidden := []string{"github", "thousandeyes", "jamf", "microsoft365", "deploymenttheory"}

	dir := curated.Dir(t)

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		body, err := os.ReadFile(path) //nolint:gosec // paths come from walking a temp dir
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(body))

		for _, name := range forbidden {
			if strings.Contains(lower, name) {
				rel, _ := filepath.Rel(dir, path)
				t.Errorf("%s names %q; the fixture must not carry a real vendor", rel, name)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking the materialised set: %v", err)
	}
}
