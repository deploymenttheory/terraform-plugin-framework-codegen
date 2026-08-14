package emit

import (
	"encoding/json"
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// unsupportedModel is a model carrying one refusal of every shape the walk
// has to find: a top-level attribute, a nested one, one inside a list
// element, and an entity that fits no kind.
func unsupportedModel() *ir.Model {
	return &ir.Model{
		Excluded: []ir.Exclusion{
			{Key: "orphan", Reason: "partial lifecycle (create, update) fits no kind"},
		},
		Resources: []ir.Resource{{
			Names: ir.Names{Key: "tag"},
			Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
				{Name: "id", Kind: ir.TypeString, ComputedOptionalRequired: ir.Computed},
				{Name: "metadata", Unsupported: true, UnsupportedReason: "free-form object: map support is out of scope"},
				{Name: "settings", Kind: ir.TypeObject, Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "retries", Kind: ir.TypeInt64},
					{Name: "extra", Unsupported: true, UnsupportedReason: "no type declared"},
				}}},
				{Name: "rules", Kind: ir.TypeList, Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "shape", Unsupported: true, UnsupportedReason: "oneOf/anyOf union: no single attribute type describes it"},
				}}},
			}},
		}},
		Actions: []ir.Action{{
			Names: ir.Names{Key: "deploy"},
			RequestSchema: &ir.AttributeTree{Attributes: []ir.Attribute{
				{Name: "payload", Unsupported: true, UnsupportedReason: "free-form object: map support is out of scope"},
			}},
		}},
	}
}

// TestUnit_RenderUnsupported_FindsEveryRefusalAtEveryDepth proves the walk
// reaches nested objects and list elements, and addresses each by its
// dotted path — the half of the report that surfaces nowhere else.
func TestUnit_RenderUnsupported_FindsEveryRefusalAtEveryDepth(t *testing.T) {
	_, entries, err := RenderUnsupported(unsupportedModel(), nil, nil, nil)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}

	got := make(map[string]string, len(entries))
	for _, e := range entries {
		got[e.Path] = e.Stage
	}

	for _, want := range []string{
		`entity "orphan"`,
		`resource "tag" attribute "metadata"`,
		`resource "tag" attribute "settings.extra"`,
		`resource "tag" attribute "rules.shape"`,
		`action "deploy" attribute "payload"`,
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("the report does not address %s", want)
		}
	}
	if len(entries) != 5 {
		t.Errorf("the report carries %d entries, want 5: %v", len(entries), got)
	}
	for path, stage := range got {
		if stage != StageDerivation {
			t.Errorf("%s is stage %q, want %q", path, stage, StageDerivation)
		}
	}
}

// TestUnit_RenderUnsupported_SupportedAttributesAreAbsent proves the report
// records refusals and nothing else — an attribute that generated fine must
// not appear in it.
func TestUnit_RenderUnsupported_SupportedAttributesAreAbsent(t *testing.T) {
	_, entries, err := RenderUnsupported(unsupportedModel(), nil, nil, nil)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}
	for _, e := range entries {
		for _, supported := range []string{`attribute "id"`, `attribute "settings.retries"`} {
			if strings.Contains(e.Path, supported) {
				t.Errorf("the report records %s, which generated fine", e.Path)
			}
		}
	}
}

// TestUnit_RenderUnsupported_EveryStageIsRepresented proves each of the
// three refusing stages reaches the report under its own name, so a reader
// can tell a document the toolkit could not model from an SDK that could
// not carry it.
func TestUnit_RenderUnsupported_EveryStageIsRepresented(t *testing.T) {
	removals := []sdkbind.Removal{
		{Kind: "resource", Key: "tag", Attribute: "colour", Reason: "the SDK carries no GetColour"},
		{Kind: "datasource", Key: "gone", Reason: "the SDK carries no read call"},
	}
	dropped := []sdkbind.Dropped{{Key: "ruleset", Kind: "resource", Reason: "the generated SDK does not carry this resource's binding"}}
	emission := []ir.Exclusion{{Key: "nested_path", Reason: "a path parameter no attribute answers"}}

	_, entries, err := RenderUnsupported(unsupportedModel(), removals, dropped, emission)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}

	byPath := make(map[string]Unsupported, len(entries))
	for _, e := range entries {
		byPath[e.Path] = e
	}

	for path, wantStage := range map[string]string{
		`resource "tag" attribute "colour"`:   StageBinding,
		`datasource "gone"`:                   StageBinding,
		`resource "ruleset"`:                  StageBinding,
		`entity "nested_path"`:                StageEmission,
		`resource "tag" attribute "metadata"`: StageDerivation,
	} {
		entry, ok := byPath[path]
		if !ok {
			t.Errorf("the report does not address %s", path)
			continue
		}
		if entry.Stage != wantStage {
			t.Errorf("%s is stage %q, want %q", path, entry.Stage, wantStage)
		}
		if entry.Reason == "" {
			t.Errorf("%s carries no reason", path)
		}
	}
}

// TestUnit_RenderUnsupported_IsAStableDiff proves the report cannot leak
// walk order or map iteration into its bytes. It is committed and
// byte-compared, so an unstable order would make every regeneration a
// spurious diff.
func TestUnit_RenderUnsupported_IsAStableDiff(t *testing.T) {
	removals := []sdkbind.Removal{
		{Kind: "resource", Key: "zeta", Attribute: "b", Reason: "no accessor"},
		{Kind: "resource", Key: "alpha", Attribute: "a", Reason: "no accessor"},
	}

	first, _, err := RenderUnsupported(unsupportedModel(), removals, nil, nil)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, _, err := RenderUnsupported(unsupportedModel(), removals, nil, nil)
		if err != nil {
			t.Fatalf("RenderUnsupported: %v", err)
		}
		if string(again.Content) != string(first.Content) {
			t.Fatal("two renders of one model disagree; the report is not a stable diff")
		}
	}

	var report UnsupportedReport
	if err := json.Unmarshal(first.Content, &report); err != nil {
		t.Fatalf("the report is not valid JSON: %v", err)
	}
	if report.FormatVersion != unsupportedFormatVersion {
		t.Errorf("format_version = %d, want %d", report.FormatVersion, unsupportedFormatVersion)
	}
	for i := 1; i < len(report.Unsupported); i++ {
		if report.Unsupported[i-1].Path > report.Unsupported[i].Path {
			t.Fatalf("entries are not sorted by path: %q before %q",
				report.Unsupported[i-1].Path, report.Unsupported[i].Path)
		}
	}
}

// TestUnit_RenderUnsupported_EmptyModelStillRendersTheReport proves a
// provider that refused nothing still gets a well-formed report rather than
// a missing file, because a missing file cannot be drift-gated.
func TestUnit_RenderUnsupported_EmptyModelStillRendersTheReport(t *testing.T) {
	file, entries, err := RenderUnsupported(&ir.Model{}, nil, nil, nil)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("an empty model produced %d entries, want none", len(entries))
	}
	if file.Path != UnsupportedName {
		t.Errorf("the report landed at %q, want %q", file.Path, UnsupportedName)
	}

	var report UnsupportedReport
	if err := json.Unmarshal(file.Content, &report); err != nil {
		t.Fatalf("the report is not valid JSON: %v", err)
	}
	if report.FormatVersion != unsupportedFormatVersion {
		t.Errorf("format_version = %d, want %d", report.FormatVersion, unsupportedFormatVersion)
	}
}

// TestUnit_UnsupportedSummary_CountsByStageAndStaysSilentWhenClean proves
// the one line generate prints, and that a clean run prints nothing —
// a report of nothing is not news.
func TestUnit_UnsupportedSummary_CountsByStageAndStaysSilentWhenClean(t *testing.T) {
	if got := UnsupportedSummary(nil); got != "" {
		t.Errorf("a clean run summarised as %q, want silence", got)
	}

	got := UnsupportedSummary([]Unsupported{
		{Path: `resource "a"`, Stage: StageBinding},
		{Path: `resource "b"`, Stage: StageBinding},
		{Path: `entity "c"`, Stage: StageDerivation},
	})
	for _, want := range []string{UnsupportedName, "3 refusals", "2 in binding", "1 in derivation"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary %q does not carry %q", got, want)
		}
	}

	if one := UnsupportedSummary([]Unsupported{{Path: `resource "a"`, Stage: StageBinding}}); !strings.Contains(one, "1 refusal (") {
		t.Errorf("a single refusal summarised as %q, want the singular noun", one)
	}
}
