package render

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// joined flattens check expressions so a test can search them as one string.
func joined(checks []string) string { return strings.Join(checks, "\n") }

// TestUnit_Render_AnAttributeTheAPINeverReturnsIsNotAsserted.
//
// The pilot's match_type is the case: observed returnedOnRead=false. Asserting the configured
// value would fail while the provider was behaving correctly, because state cannot hold what the
// read never delivered -- and a test that fails when the code is right gets deleted, taking the
// checks that did work with it.
func TestUnit_Render_AnAttributeTheAPINeverReturnsIsNotAsserted(t *testing.T) {
	t.Parallel()

	a := attr("match_type", blueprint.KindString, blueprint.ComputedOptional)
	a.Behaviour.AcceptedValues = []string{"and", "or"}
	a.Behaviour.ReturnedOnRead = boolPtr(false)

	bp, r := fixtureResource(
		attr("key", blueprint.KindString, blueprint.Required),
		a,
	)

	f, err := fixtureView(bp, r, "", false, "")
	if err != nil {
		t.Fatalf("fixtureView: %v", err)
	}

	checks := joined(checksFor(r, f, newPatternVars()))

	if strings.Contains(checks, "match_type") {
		t.Errorf("match_type must not be asserted -- the API never returns it:\n%s", checks)
	}
	// And the one that is returned still is, or the suppression is just "assert nothing".
	if !strings.Contains(checks, `Key("key").HasValue("tfacc-key")`) {
		t.Errorf("a returned attribute should still be asserted:\n%s", checks)
	}
}

// TestUnit_Render_ACuratedValueIsAssertedAsPresenceNotEquality.
//
// A curated fixture value may be a reference whose value exists only at apply time, so
// asserting equality against its HCL text would compare state to source code. Presence
// is the honest claim: the step configured it, and a state that lost it is wrong
// whatever the value was.
func TestUnit_Render_ACuratedValueIsAssertedAsPresenceNotEquality(t *testing.T) {
	t.Parallel()

	bp, r := fixtureResource(
		attr("key", blueprint.KindString, blueprint.Required),
		attr("target_id", blueprint.KindString, blueprint.Required),
	)
	r.AccFixture = &blueprint.AccFixture{
		DataBlocks: []string{`data "te_agents" "test" {}`},
		Values: []blueprint.FixtureHint{{
			Attr: "target_id",
			HCL:  "data.te_agents.test.agents[0].agent_id",
		}},
	}

	f, err := fixtureView(bp, r, "", true, "")
	if err != nil {
		t.Fatalf("fixtureView: %v", err)
	}

	checks := joined(checksFor(r, f, newPatternVars()))

	if !strings.Contains(checks, `Key("target_id").Exists()`) {
		t.Errorf("a curated value should be asserted as present:\n%s", checks)
	}
	if strings.Contains(checks, `Key("target_id").HasValue`) {
		t.Errorf("a curated value must not be asserted for equality against its own source text:\n%s", checks)
	}
	// The derived neighbour keeps its exact assertion.
	if !strings.Contains(checks, `Key("key").HasValue("tfacc-key")`) {
		t.Errorf("a derived value should still be asserted exactly:\n%s", checks)
	}
}

// TestUnit_Render_ImportVerificationSkipsWhatTheAPIWillNotReturn.
//
// ImportStateVerify re-imports and compares every attribute against the state the apply produced.
// An attribute the API never returns is absent after import and present before it, so the
// comparison fails for a reason that has nothing to do with import being broken.
func TestUnit_Render_ImportVerificationSkipsWhatTheAPIWillNotReturn(t *testing.T) {
	t.Parallel()

	notReturned := attr("match_type", blueprint.KindString, blueprint.ComputedOptional)
	notReturned.Behaviour.ReturnedOnRead = boolPtr(false)

	volatile := attr("last_seen", blueprint.KindString, blueprint.Computed)
	volatile.Behaviour.Volatile = boolPtr(true)

	returned := attr("key", blueprint.KindString, blueprint.Required)
	returned.Behaviour.ReturnedOnRead = boolPtr(true)

	_, r := fixtureResource(returned, notReturned, volatile)

	names, reasons := importIgnores(r)

	got := strings.Join(names, ",")
	for _, want := range []string{"match_type", "last_seen"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s should be ignored on import, got %s", want, got)
		}
	}
	if strings.Contains(got, "key") {
		t.Errorf("an attribute the API returns must still be verified, got %s", got)
	}
	if len(reasons) != len(names) {
		t.Errorf("every ignored attribute needs a stated reason: %d names, %d reasons",
			len(names), len(reasons))
	}
	if !strings.Contains(strings.Join(reasons, " "), "never returns it") {
		t.Errorf("the reason should say why: %v", reasons)
	}
}

// TestUnit_Render_ANormalisedAttributeIsNotAssertedForEquality.
//
// The API rewrites the value, so an equality assertion on what was sent fails by design. This is
// why the normalisation fact had to become structural: merge recorded it as prose in the
// description, and a generated test cannot read prose.
func TestUnit_Render_ANormalisedAttributeIsNotAssertedForEquality(t *testing.T) {
	t.Parallel()

	a := attr("name", blueprint.KindString, blueprint.Required)
	a.Behaviour.Normalises = "lowercases it"

	bp, r := fixtureResource(a)

	f, err := fixtureView(bp, r, "", true, "")
	if err != nil {
		t.Fatalf("fixtureView: %v", err)
	}

	if checks := joined(checksFor(r, f, newPatternVars())); strings.Contains(checks, "HasValue") {
		t.Errorf("a normalised attribute must not be asserted for equality:\n%s", checks)
	}

	// Without the fact it is asserted, so the suppression is doing the work rather than the
	// attribute being unassertable for some other reason.
	r.Schema.Attributes[0].Behaviour.Normalises = ""

	if checks := joined(checksFor(r, f, newPatternVars())); !strings.Contains(checks, "HasValue") {
		t.Errorf("without the fact the value should be asserted:\n%s", checks)
	}
}

// TestUnit_Render_AnOmittedServerDefaultIsAsserted.
//
// This is the check that tests the probe's own evidence against reality. The attribute is not in
// the minimal fixture, so what lands in state is whatever the API applied -- and if that default
// has changed since the cassette was recorded, this assertion is what says so.
func TestUnit_Render_AnOmittedServerDefaultIsAsserted(t *testing.T) {
	t.Parallel()

	withDefault := attr("color", blueprint.KindString, blueprint.ComputedOptional)
	withDefault.Behaviour.ServerDefault = &blueprint.Literal{Raw: `"#A7EB10"`}
	withDefault.Behaviour.ReturnedOnRead = boolPtr(true)

	bp, r := fixtureResource(
		attr("key", blueprint.KindString, blueprint.Required),
		withDefault,
	)

	minimal, err := fixtureView(bp, r, "", true, "")
	if err != nil {
		t.Fatalf("fixtureView: %v", err)
	}

	// colour is absent from a minimal fixture, which is precisely why its default is assertable.
	if strings.Contains(joined([]string{minimal.Values[0].Name}), "color") {
		t.Fatal("colour should not be in the minimal fixture")
	}

	checks := joined(checksFor(r, minimal, newPatternVars()))
	if !strings.Contains(checks, `Key("color").HasValue("#A7EB10")`) {
		t.Errorf("the observed server default should be asserted:\n%s", checks)
	}
}

// TestUnit_Render_TheDestroyWaitSaysWhetherItWasMeasured.
//
// A bare duration in a test is indistinguishable from superstition. The reference provider's
// equivalent is a hardcoded 30-second sleep in every acceptance test with nothing to say whether
// 30 was measured or guessed, and this exists so a reader can tell the difference.
func TestUnit_Render_TheDestroyWaitSaysWhetherItWasMeasured(t *testing.T) {
	t.Parallel()

	var r blueprint.Resource

	unmeasured, why := destroyWait(r)
	if !strings.Contains(why, "never measured") {
		t.Errorf("an unmeasured wait should say so: %q", why)
	}
	if unmeasured == "" {
		t.Error("there should still be a wait, since a budget beats no retry at all")
	}

	r.Policy.ReadBack = blueprint.ReadBack{
		Enabled: true, MaxRetries: 4, IntervalMS: 500,
		Reason: "the API returns 404 for a moment after a write",
	}

	measured, why := destroyWait(r)
	if !strings.Contains(why, "measured") || strings.Contains(why, "never measured") {
		t.Errorf("a measured wait should say so: %q", why)
	}
	if !strings.Contains(why, "404") {
		t.Errorf("the measured reason should carry the prober's own words: %q", why)
	}
	// 4 x 500ms, so the test waits exactly as long as the provider would rather than being
	// stricter than the code under test.
	if measured != "2 * time.Second" {
		t.Errorf("got %q, want the provider's own retry budget", measured)
	}
}

// TestUnit_Render_TheStateMapperDoesNotFlattenWhatTheAPINeverReturns.
//
// Flattening a field the API accepts and never returns overwrites the configured value with the
// zero one on every read, so the next plan reports a diff nobody caused. The IR has said so since
// the field was added; nothing acted on it until a generated acceptance test made the consequence
// visible, because an empty-plan check is the only gate that can see it.
func TestUnit_Render_TheStateMapperDoesNotFlattenWhatTheAPINeverReturns(t *testing.T) {
	t.Parallel()

	returned := attr("key", blueprint.KindString, blueprint.Required)
	returned.GoField = "Key"
	returned.Wire = blueprint.WireBinding{
		SDKField: "Key",
		Flatten:  &blueprint.ConvertCall{Func: "convert.PtrStringToFramework"},
	}

	notReturned := attr("match_type", blueprint.KindString, blueprint.ComputedOptional)
	notReturned.GoField = "MatchType"
	notReturned.Behaviour.ReturnedOnRead = boolPtr(false)
	notReturned.Wire = blueprint.WireBinding{
		SDKField: "MatchType",
		Flatten:  &blueprint.ConvertCall{Func: "convert.EnumToFramework"},
	}

	s := blueprint.Schema{Attributes: []blueprint.Attribute{returned, notReturned}}

	view, err := stateView(s, "tags.Tag", nil)
	if err != nil {
		t.Fatalf("stateView: %v", err)
	}

	got := joined(view.Assignments)

	if !strings.Contains(got, "data.Key = convert.PtrStringToFramework(remote.Key)") {
		t.Errorf("a returned field should still be flattened:\n%s", got)
	}
	if strings.Contains(got, "data.MatchType = convert.") {
		t.Errorf("a never-returned field must not be flattened from the response:\n%s", got)
	}
	if !strings.Contains(got, "match_type is deliberately not read back") {
		t.Errorf("the omission should explain itself in the generated code:\n%s", got)
	}

	// Skipping the flatten is necessary and not sufficient. An optional-and-computed attribute
	// the practitioner left unset is unknown during apply, and the framework rejects a provider
	// that returns an unknown -- which the first live run proved, having passed every other gate:
	//
	//	After the apply operation, the provider still indicated an unknown value for
	//	thousandeyes_tag.test.match_type. All values must be known after apply.
	if !strings.Contains(got, "data.MatchType.IsUnknown()") {
		t.Errorf("an unset value must be resolved rather than left unknown:\n%s", got)
	}
	if !strings.Contains(got, "data.MatchType = types.StringNull()") {
		t.Errorf("it should resolve to null, which is the honest answer:\n%s", got)
	}
	if !view.NeedsTypes {
		t.Error("the null constructor needs the types package imported")
	}
}

// TestUnit_Render_ANeverReturnedCollectionIsRefusedRatherThanApproximated.
//
// types.ListNull and types.ObjectNull take the element or attribute types, which are not
// derivable where the null is emitted. A collection the API never returns has not been observed
// on any API yet, so it is refused by name rather than approximated into something that compiles
// and is wrong.
func TestUnit_Render_ANeverReturnedCollectionIsRefusedRatherThanApproximated(t *testing.T) {
	t.Parallel()

	for _, kind := range []blueprint.TypeKind{
		blueprint.KindList, blueprint.KindSet, blueprint.KindMap, blueprint.KindSetNested,
	} {
		a := attr("things", kind, blueprint.ComputedOptional)
		a.GoField = "Things"
		a.Behaviour.ReturnedOnRead = boolPtr(false)
		a.Wire = blueprint.WireBinding{
			SDKField: "Things",
			Flatten:  &blueprint.ConvertCall{Func: "convert.Whatever"},
		}

		_, err := stateView(blueprint.Schema{Attributes: []blueprint.Attribute{a}}, "x.Y", nil)
		if err == nil {
			t.Errorf("%s should be refused rather than approximated", kind)
			continue
		}
		if !strings.Contains(err.Error(), "things") {
			t.Errorf("%s: the refusal should name the attribute: %v", kind, err)
		}
	}
}

// TestUnit_Render_ASchemaVersionReachesTheSchemaAndTheUpgraderKeys.
//
// This was the third dead field in this project, after ReadBack and the never-returned flatten.
// The IR documented Schema.Version as "bumped when an attribute change needs a state upgrader" and
// nothing read it, so a blueprint could say version 2 and emit a schema Terraform reads as version
// 0. State written earlier also says 0, the versions match, fwserver passes it through untouched --
// and every old field is silently reinterpreted under the new schema with no upgrade and no error.
// Silence is the whole problem: a version that fails to arrive is invisible.
//
// Driven through the real Resource() view builder against the committed pilot, because a test that
// recomputed the assignment itself would pass whether or not the generator did it.
func TestUnit_Render_ASchemaVersionReachesTheSchemaAndTheUpgraderKeys(t *testing.T) {
	t.Parallel()

	bp := pilot(t)

	// The pilot declares no version, and zero is the framework's default, so nothing is emitted.
	base, err := Resource(bp, bp.Resources[0], Options{})
	if err != nil {
		t.Fatalf("Resource: %v", err)
	}
	if base.SchemaVersion != 0 || len(base.PriorVersions) != 0 {
		t.Errorf("version 0 should emit nothing, got %d with keys %v",
			base.SchemaVersion, base.PriorVersions)
	}
	for _, iface := range base.Interfaces {
		if strings.Contains(iface, "ResourceWithUpgradeState") {
			t.Error("no upgrader is declared, so the interface must not be asserted")
		}
	}

	r := bp.Resources[0]
	r.Schema.Version = 3
	r.Hooks.StateUpgrade = true

	got, err := Resource(bp, r, Options{})
	if err != nil {
		t.Fatalf("Resource: %v", err)
	}

	if got.SchemaVersion != 3 {
		t.Errorf("SchemaVersion = %d, want 3 -- the whole point is that it arrives",
			got.SchemaVersion)
	}

	// Every version a practitioner might hold, not just the most recent. fwserver looks the map up
	// by the version found in state, so somebody who skipped a release needs their key present or
	// they get "expecting an implementation for version N upgrade" and no way forward.
	want := []int64{0, 1, 2}
	if len(got.PriorVersions) != len(want) {
		t.Fatalf("PriorVersions = %v, want %v", got.PriorVersions, want)
	}
	for i := range want {
		if got.PriorVersions[i] != want[i] {
			t.Fatalf("PriorVersions = %v, want %v", got.PriorVersions, want)
		}
	}

	// And the assertion appears, so deleting the scaffold breaks the build with a message naming
	// the interface rather than silently ceasing to migrate state.
	var asserted bool
	for _, iface := range got.Interfaces {
		if strings.Contains(iface, "ResourceWithUpgradeState") {
			asserted = true
		}
	}
	if !asserted {
		t.Errorf("ResourceWithUpgradeState should be asserted: %v", got.Interfaces)
	}
}
