package emit

import (
	"encoding/json"
	"fmt"
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
		Excluded: []ir.UnsupportedEntity{
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
	_, entries, err := RenderUnsupported(unsupportedModel(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}

	got := make(map[string]string, len(entries))
	for _, e := range entries {
		got[subject(e)] = e.Stage
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
	_, entries, err := RenderUnsupported(unsupportedModel(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}
	for _, e := range entries {
		for _, supported := range []string{`attribute "id"`, `attribute "settings.retries"`} {
			if strings.Contains(subject(e), supported) {
				t.Errorf("the report records %s, which generated fine", subject(e))
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
	emission := []ir.UnsupportedEntity{{Key: "nested_path", Reason: "a path parameter no attribute answers"}}

	_, entries, err := RenderUnsupported(unsupportedModel(), removals, dropped, emission, nil)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}

	byPath := make(map[string]Unsupported, len(entries))
	for _, e := range entries {
		byPath[subject(e)] = e
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

	first, _, err := RenderUnsupported(unsupportedModel(), removals, nil, nil, nil)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, _, err := RenderUnsupported(unsupportedModel(), removals, nil, nil, nil)
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
		if report.Unsupported[i-1].Entity > report.Unsupported[i].Entity {
			t.Fatalf("entries are not sorted by path: %q before %q",
				report.Unsupported[i-1].Entity, report.Unsupported[i].Entity)
		}
	}
}

// TestUnit_RenderUnsupported_EmptyModelStillRendersTheReport proves a
// provider that refused nothing still gets a well-formed report rather than
// a missing file, because a missing file cannot be drift-gated.
func TestUnit_RenderUnsupported_EmptyModelStillRendersTheReport(t *testing.T) {
	file, entries, err := RenderUnsupported(&ir.Model{}, nil, nil, nil, nil)
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
		{Kind: "resource", Entity: "a", Stage: StageBinding},
		{Kind: "resource", Entity: "b", Stage: StageBinding},
		{Entity: "c", Stage: StageDerivation},
	})
	for _, want := range []string{UnsupportedName, "3 refusals", "2 in binding", "1 in derivation"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary %q does not carry %q", got, want)
		}
	}

	if one := UnsupportedSummary([]Unsupported{{Kind: "resource", Entity: "a", Stage: StageBinding}}); !strings.Contains(one, "1 refusal (") {
		t.Errorf("a single refusal summarised as %q, want the singular noun", one)
	}
}

// TestUnit_RenderUnsupported_KeptAttributesAreNotLosses proves the report
// leaves out a removal the emitter kept anyway. Pruning removes the binding
// for `id` and the addressing attributes because no model carries them —
// they address the object rather than describe it — and the attribute
// reaches the schema regardless. Reporting those as refusals would be
// wrong: the operator lost nothing.
func TestUnit_RenderUnsupported_KeptAttributesAreNotLosses(t *testing.T) {
	removals := []sdkbind.Removal{
		{Kind: "resource", Key: "tag", Attribute: "id", Reason: "carries no GetId"},
		{Kind: "resource", Key: "tag", Attribute: "owner", Reason: "carries no GetOwner"},
		{Kind: "resource", Key: "tag", Attribute: "colour", Reason: "carries no GetColour"},
	}
	kept := map[string]bool{
		keptUnboundKey("resource", "tag", "id"):    true,
		keptUnboundKey("resource", "tag", "owner"): true,
	}

	_, entries, err := RenderUnsupported(&ir.Model{}, removals, nil, nil, kept)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}

	paths := make(map[string]bool, len(entries))
	for _, e := range entries {
		paths[subject(e)] = true
	}
	for _, gone := range []string{`resource "tag" attribute "id"`, `resource "tag" attribute "owner"`} {
		if paths[gone] {
			t.Errorf("%s reached the schema, so it must not be reported as a refusal", gone)
		}
	}
	if !paths[`resource "tag" attribute "colour"`] {
		t.Error(`resource "tag" attribute "colour" was genuinely lost and must still be reported`)
	}
	if len(entries) != 1 {
		t.Errorf("the report carries %d entries, want 1: %v", len(entries), paths)
	}
}

// TestUnit_RenderUnsupported_KeptIsMatchedPerEntity proves the filter is
// keyed on the whole triple. Two entities can both carry an attribute of
// one name, and only the one that actually kept it may be filtered.
func TestUnit_RenderUnsupported_KeptIsMatchedPerEntity(t *testing.T) {
	removals := []sdkbind.Removal{
		{Kind: "resource", Key: "kept", Attribute: "org", Reason: "carries no GetOrg"},
		{Kind: "resource", Key: "lost", Attribute: "org", Reason: "carries no GetOrg"},
		{Kind: "datasource", Key: "kept", Attribute: "org", Reason: "carries no GetOrg"},
	}
	kept := map[string]bool{keptUnboundKey("resource", "kept", "org"): true}

	_, entries, err := RenderUnsupported(&ir.Model{}, removals, nil, nil, kept)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, subject(e))
	}
	if len(entries) != 2 {
		t.Fatalf("the report carries %v, want the lost resource and the datasource only", got)
	}
	for _, want := range []string{`resource "lost" attribute "org"`, `datasource "kept" attribute "org"`} {
		found := false
		for _, p := range got {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the report does not carry %s; got %v", want, got)
		}
	}
}

// TestUnit_JoinTreeKeeping_ReportsOnlyWhatItKeptUnbound proves the set the
// report filters on is exactly what the join decided to keep: the id and
// the addressing attributes at the root, and nothing that had a binding or
// that was dropped.
func TestUnit_JoinTreeKeeping_ReportsOnlyWhatItKeptUnbound(t *testing.T) {
	tree := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "id", Kind: ir.TypeString},
		{Name: "org", Kind: ir.TypeString},
		{Name: "name", Kind: ir.TypeString},
		{Name: "colour", Kind: ir.TypeString},
	}}
	// Only "name" has a binding; "colour" has none and is not addressing.
	fbs := []sdkbind.FieldBinding{{Attr: "name"}}

	nodes, kept := joinTreeKeeping(tree, fbs, map[string]bool{"org": true})

	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.attribute.Name)
	}
	if len(names) != 3 {
		t.Fatalf("the join kept %v, want id, org and name", names)
	}

	keptSet := map[string]bool{}
	for _, k := range kept {
		keptSet[k] = true
	}
	for _, want := range []string{"id", "org"} {
		if !keptSet[want] {
			t.Errorf("%q was kept with no binding and must be reported as such", want)
		}
	}
	if keptSet["name"] {
		t.Error("name has a binding, so it was not kept unbound")
	}
	if keptSet["colour"] {
		t.Error("colour was dropped, not kept")
	}
	if len(kept) != 2 {
		t.Errorf("the join reports %v kept unbound, want exactly id and org", kept)
	}
}

// subject renders a refusal's subject the way these expectations read it.
// The record carries fields; a test asserting on one entity's refusal still
// reads better as a sentence than as five comparisons.
func subject(e Unsupported) string {
	kind := e.Kind
	if kind == "" {
		kind = "entity"
	}
	if e.Attribute == "" {
		return fmt.Sprintf("%s %q", kind, e.Entity)
	}
	return fmt.Sprintf("%s %q attribute %q", kind, e.Entity, e.Attribute)
}

// TestUnit_RenderUnsupported_ARefusalCarriesItsSubjectAsFields holds the
// subject to fields rather than to a rendered sentence. A reader grouping
// refusals by entity or by kind should not have to parse prose to do it.
func TestUnit_RenderUnsupported_ARefusalCarriesItsSubjectAsFields(t *testing.T) {
	model := unsupportedModel()
	model.Resources[0].Names = ir.Names{Key: "tag", Service: "tags", Tag: "Tags"}

	_, entries, err := RenderUnsupported(model, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}

	var found bool
	for _, e := range entries {
		if e.Entity != "tag" || e.Attribute != "settings.extra" {
			continue
		}
		found = true
		if e.Kind != bindingKindResource {
			t.Errorf("Kind = %q, want %q", e.Kind, bindingKindResource)
		}
		if e.Service != "tags" || e.Tag != "Tags" {
			t.Errorf("Service/Tag = %q/%q, want tags/Tags", e.Service, e.Tag)
		}
		if e.Stage != StageDerivation {
			t.Errorf("Stage = %q, want %q", e.Stage, StageDerivation)
		}
	}
	if !found {
		t.Fatalf("the nested refusal is not addressed by entity and attribute")
	}
}

// TestUnit_RenderUnsupported_AnEntityRefusedBeforeItBecameAnythingIsStillPlaced
// is the case the fields exist for. An entity refused at classification has
// no kind, and until it carried its own location there was nothing to group
// it by — which is most of what a large document refuses.
func TestUnit_RenderUnsupported_AnEntityRefusedBeforeItBecameAnythingIsStillPlaced(t *testing.T) {
	model := unsupportedModel()
	model.Excluded[0] = ir.UnsupportedEntity{
		Key:            "orphan",
		CollectionPath: "/v1/orphans",
		Service:        "orphans",
		Tag:            "Orphans",
		Reason:         "partial lifecycle (create, update) fits no kind",
	}

	_, entries, err := RenderUnsupported(model, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}

	for _, e := range entries {
		if e.Entity != "orphan" {
			continue
		}
		if e.Kind != "" {
			t.Errorf("Kind = %q, want empty: it became no kind, which is why it was refused", e.Kind)
		}
		if e.Service != "orphans" || e.Tag != "Orphans" {
			t.Errorf("Service/Tag = %q/%q, want orphans/Orphans", e.Service, e.Tag)
		}
		return
	}
	t.Fatal("the excluded entity is not in the report")
}

// TestUnit_RenderUnsupported_ADroppedEntityKeepsItsKind holds the merge that
// used to flatten sdkbind's answer to a key and a reason. The SDK said which
// kind it could not carry, and a report that drops that reads as an entity
// which became nothing at all.
func TestUnit_RenderUnsupported_ADroppedEntityKeepsItsKind(t *testing.T) {
	model := unsupportedModel()
	model.Resources[0].Names = ir.Names{Key: "tag", Service: "tags", Tag: "Tags"}
	dropped := []sdkbind.Dropped{{Key: "tag", Kind: bindingKindResource, Reason: "the generated SDK does not carry this resource's binding"}}

	_, entries, err := RenderUnsupported(model, nil, dropped, nil, nil)
	if err != nil {
		t.Fatalf("RenderUnsupported: %v", err)
	}
	for _, e := range entries {
		if e.Stage != StageBinding || e.Entity != "tag" || e.Attribute != "" {
			continue
		}
		if e.Kind != bindingKindResource {
			t.Errorf("Kind = %q, want %q", e.Kind, bindingKindResource)
		}
		if e.Service != "tags" {
			t.Errorf("Service = %q, want the location read back from the model", e.Service)
		}
		return
	}
	t.Fatal("the dropped entity is not in the report")
}
