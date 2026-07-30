package render

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// attr returns a writable attribute of the given kind.
func attr(
	name string,
	kind blueprint.TypeKind,
	cor blueprint.ComputedOptionalRequired,
) blueprint.Attribute {
	return blueprint.Attribute{
		Name:                     name,
		GoField:                  "F",
		ComputedOptionalRequired: cor,
		Type:                     blueprint.AttrType{Kind: kind},
	}
}

// fixtureResource returns a minimal blueprint and resource for fixture tests.
func fixtureResource(attrs ...blueprint.Attribute) (blueprint.Blueprint, blueprint.Resource) {
	bp := blueprint.Blueprint{Provider: blueprint.Provider{Name: "te", TypePrefix: "te"}}
	r := blueprint.Resource{Key: "thing", Name: "thing"}
	r.Schema.Attributes = attrs

	return bp, r
}

func names(vs []fixtureValue) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Name)
	}

	return out
}

// TestUnit_Render_AFixtureValuePrefersObservedEvidenceOverDocumentation is the phase's
// load-bearing decision, and it is the exact inverse of how a validator is built.
//
// A validator comes from the documented set, so it errs toward permitting: blocking a
// configuration the API would have taken is a harm nobody can work around. A fixture comes from
// the observed set, so it errs toward working: it is an input rather than a contract, and one the
// API rejects tests nothing at all.
//
// The pilot is why this is not theoretical. `access_type` documents "system" and the probe watched
// the API refuse it, so taking the first documented value -- the obvious implementation -- would
// put a known-rejected value into every generated fixture.
func TestUnit_Render_AFixtureValuePrefersObservedEvidenceOverDocumentation(t *testing.T) {
	t.Parallel()

	a := attr("access_type", blueprint.KindString, blueprint.Optional)
	a.Type.AllowedValues = []string{"system", "all", "partner"}
	a.Behaviour.AcceptedValues = []string{"all"}
	a.Behaviour.RejectedValues = []string{"system"}

	got := fixtureValueFor(a)

	if got.Skipped {
		t.Fatalf("should have produced a value: %s", got.Reason)
	}
	if got.HCL != `"all"` {
		t.Errorf(`got %s, want the observed-accepted value "all"`, got.HCL)
	}
	if !strings.Contains(got.Note, "refused system") {
		t.Errorf("the note should name the rejected value so the choice is explicable: %q", got.Note)
	}

	// With no observation the documented set is all there is -- and the note says so, rather
	// than implying the value was ever tested.
	a.Behaviour.AcceptedValues = nil
	a.Behaviour.RejectedValues = nil

	got = fixtureValueFor(a)
	if got.HCL != `"system"` {
		t.Errorf("unprobed, got %s, want the first documented value", got.HCL)
	}
	if !strings.Contains(got.Note, "unprobed") {
		t.Errorf("an unprobed value should say so: %q", got.Note)
	}
}

// TestUnit_Render_AServerDefaultBeatsASynthesisedString.
//
// The API produced the value itself, so it cannot be the wrong shape. That matters for a field
// whose format is real but undocumented: the pilot's `color` is a hex triplet with no `pattern`
// recorded anywhere, so a synthesised "tfacc-color" would be refused for a reason no reader of
// the blueprint could have predicted.
func TestUnit_Render_AServerDefaultBeatsASynthesisedString(t *testing.T) {
	t.Parallel()

	a := attr("color", blueprint.KindString, blueprint.ComputedOptional)
	a.Behaviour.ServerDefault = &blueprint.Literal{Raw: `"#A7EB10"`}

	got := fixtureValueFor(a)
	if got.HCL != `"#A7EB10"` {
		t.Errorf("got %s, want the API's own default", got.HCL)
	}
	if !strings.Contains(got.Note, "known to accept") {
		t.Errorf("the note should say where the value came from: %q", got.Note)
	}

	// Without one, a synthesised value carries the test prefix, so anything it leaves behind in
	// a tenant is identifiable as debris.
	a.Behaviour.ServerDefault = nil

	if got := fixtureValueFor(a); !strings.HasPrefix(got.HCL, `"tfacc-`) {
		t.Errorf("a synthesised string should be recognisable as test debris: %s", got.HCL)
	}
}

// TestUnit_Render_AnUnsatisfiablePatternIsReportedRatherThanGuessed.
//
// A string matching an arbitrary regular expression cannot be constructed by reading the pattern
// forwards. Emitting a plausible-looking string and hoping is how a fixture starts failing in
// somebody's acceptance run for a reason that is invisible in the diff.
func TestUnit_Render_AnUnsatisfiablePatternIsReportedRatherThanGuessed(t *testing.T) {
	t.Parallel()

	a := attr("hostname", blueprint.KindString, blueprint.Optional)
	a.Type.Constraints.Pattern = `^[a-z]{3}-\d{4}$`

	got := fixtureValueFor(a)
	if !got.Skipped {
		t.Fatalf("a patterned string should be skipped, got %s", got.HCL)
	}
	if !strings.Contains(got.Reason, a.Type.Constraints.Pattern) {
		t.Errorf("the reason should quote the pattern: %q", got.Reason)
	}
}

// TestUnit_Render_ANestedAttributeIsNeverSynthesised.
//
// The pilot has two set_nested attributes that are structurally identical and behave completely
// differently: `filters` holds plain strings, and `assignments` holds identifiers of tests and
// dashboards that must already exist. Nothing in the IR tells them apart, because the
// specification does not say.
//
// So neither is synthesised. A fabricated identifier produces HCL that is valid, matches the
// schema, applies cleanly and then fails against the API complaining about an object that does
// not exist -- worse than an omission a practitioner can see and fill in.
func TestUnit_Render_ANestedAttributeIsNeverSynthesised(t *testing.T) {
	t.Parallel()

	for _, kind := range []blueprint.TypeKind{
		blueprint.KindSingleNested, blueprint.KindListNested, blueprint.KindSetNested,
	} {
		got := fixtureValueFor(attr("assignments", kind, blueprint.Optional))
		if !got.Skipped {
			t.Errorf("%s should be skipped, got %s", kind, got.HCL)
		}
		if !strings.Contains(got.Reason, "must already exist") {
			t.Errorf("%s: the reason should explain the risk: %q", kind, got.Reason)
		}
	}
}

// TestUnit_Render_AFixtureRefusesRatherThanEmitAnUnusableRequiredAttribute.
//
// An optional attribute with no derivable value is omitted, which leaves valid HCL. A *required*
// one cannot be: the file would be a generated artefact that fails on first use. Refusing moves
// the failure from somebody's acceptance run to the generator, where it can be read and acted on.
func TestUnit_Render_AFixtureRefusesRatherThanEmitAnUnusableRequiredAttribute(t *testing.T) {
	t.Parallel()

	bp, r := fixtureResource(
		attr("name", blueprint.KindString, blueprint.Required),
		attr("nested", blueprint.KindSetNested, blueprint.Optional),
	)

	// Optional and undecidable is reported, not fatal.
	v, err := fixtureView(bp, r, "", false)
	if err != nil {
		t.Fatalf("an optional undecidable attribute should be skipped, not fatal: %v", err)
	}
	if len(v.Skipped) != 1 || v.Skipped[0].Name != "nested" {
		t.Errorf("nested should be reported as skipped, got %v", names(v.Skipped))
	}

	// Required and undecidable refuses the whole fixture, by name.
	r.Schema.Attributes[1].ComputedOptionalRequired = blueprint.Required

	_, err = fixtureView(bp, r, "", false)
	if err == nil {
		t.Fatal("a required undecidable attribute should refuse the fixture")
	}
	if !strings.Contains(err.Error(), "nested") {
		t.Errorf("the refusal should name the attribute: %v", err)
	}
}

// TestUnit_Render_AMinimalFixtureIsRequiredAttributesOnly, and no computed one ever appears.
//
// A purely computed attribute cannot be set in configuration, so writing it is invalid HCL rather
// than merely redundant -- Terraform rejects the file outright.
func TestUnit_Render_AMinimalFixtureIsRequiredAttributesOnly(t *testing.T) {
	t.Parallel()

	bp, r := fixtureResource(
		attr("id", blueprint.KindString, blueprint.Computed),
		attr("name", blueprint.KindString, blueprint.Required),
		attr("note", blueprint.KindString, blueprint.Optional),
	)

	minimal, err := fixtureView(bp, r, "", true)
	if err != nil {
		t.Fatalf("fixtureView: %v", err)
	}
	if got := names(minimal.Values); len(got) != 1 || got[0] != "name" {
		t.Errorf("minimal should be the required attribute alone, got %v", got)
	}

	maximal, err := fixtureView(bp, r, "", false)
	if err != nil {
		t.Fatalf("fixtureView: %v", err)
	}
	if got := names(maximal.Values); len(got) != 2 {
		t.Errorf("maximal should add the optional attribute, got %v", got)
	}
	for _, v := range maximal.Values {
		if v.Name == "id" {
			t.Error("a computed attribute must never reach a fixture: it is not settable")
		}
	}
}

// TestUnit_Render_FixtureNamesAreAlignedAsTerraformFmtWould.
//
// Not cosmetic. `terraform fmt` aligns consecutive assignments, so unaligned output is output a
// formatter rewrites -- and a generated file a formatter rewrites shows up as drift with no source
// change, which is exactly what the drift check exists to rule out.
func TestUnit_Render_FixtureNamesAreAlignedAsTerraformFmtWould(t *testing.T) {
	t.Parallel()

	bp, r := fixtureResource(
		attr("a", blueprint.KindString, blueprint.Required),
		attr("object_type", blueprint.KindString, blueprint.Required),
	)

	v, err := fixtureView(bp, r, "", true)
	if err != nil {
		t.Fatalf("fixtureView: %v", err)
	}

	widest := len("object_type")
	for _, val := range v.Values {
		if got := len(val.Name) + len(val.Padding); got != widest {
			t.Errorf("%s padded to %d, want %d so the = signs line up", val.Name, got, widest)
		}
	}
}

// TestUnit_Render_OnlyTheMinimalFixtureCarriesAGeneratedMarker.
//
// The maximal fixture is a scaffold: written once, then owned. The marker is what the drift check
// and the overwrite refusal both key on, so a file meant to be edited must not carry it -- or the
// first edit would be reported as drift and the next emit would refuse to run.
func TestUnit_Render_OnlyTheMinimalFixtureCarriesAGeneratedMarker(t *testing.T) {
	t.Parallel()

	bp, r := fixtureResource(attr("name", blueprint.KindString, blueprint.Required))
	opts := Options{BlueprintPath: "blueprints/x", BlueprintSHA256: "abc"}

	minimal, err := Fixture(bp, r, opts, true)
	if err != nil {
		t.Fatalf("Fixture: %v", err)
	}
	if !strings.Contains(minimal.Header, "DO NOT EDIT.") {
		t.Errorf("the generated fixture should carry the marker: %q", minimal.Header)
	}
	if !strings.HasPrefix(minimal.Header, "#") {
		t.Errorf("an HCL header needs HCL's comment syntax: %q", minimal.Header)
	}

	maximal, err := Fixture(bp, r, opts, false)
	if err != nil {
		t.Fatalf("Fixture: %v", err)
	}
	if maximal.Header != "" {
		t.Errorf("the scaffolded fixture must carry no header: %q", maximal.Header)
	}
}
