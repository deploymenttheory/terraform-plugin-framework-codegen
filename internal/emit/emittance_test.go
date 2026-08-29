package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// emittanceModel is one entity that generated and one that became nothing,
// in two of the document's groups.
func emittanceModel() *ir.Model {
	return &ir.Model{
		Resources: []ir.Resource{{
			Names: ir.Names{Key: "tag", Service: "tags", Tag: "Tags"},
		}},
		Datasources: []ir.Datasource{{
			Names: ir.Names{Key: "preload", Service: "inventory_preload", Tag: "Inventory Preload"},
		}},
		ExcludedByClassification: []ir.UnsupportedEntity{{
			Key: "orphan", Service: "orphans", Tag: "Tags",
			CollectionPath: "/v1/orphans",
			Cause:          ir.Cause{Code: "partialLifecycle"},
			Reason:         "partial lifecycle (create) fits no kind",
		}},
	}
}

// TestUnit_RenderEmittance_ConsequencesOfOneFactAreOneEntry is the whole
// point of the page. A model carrying none of an entity's fields refuses
// every one of them, and the report says that once with the casualties
// listed — not once per casualty.
func TestUnit_RenderEmittance_ConsequencesOfOneFactAreOneEntry(t *testing.T) {
	shared := &ir.Cause{Code: "noAccessor", Subject: "models.Envelopeable"}
	refusals := []Unsupported{
		{Kind: "datasource", Entity: "preload", Attribute: "asset_tag", Tag: "Inventory Preload",
			Cause: shared, Stage: StageBinding, Reason: "models.Envelopeable carries no GetAssetTag"},
		{Kind: "datasource", Entity: "preload", Attribute: "serial_number", Tag: "Inventory Preload",
			Cause: shared, Stage: StageBinding, Reason: "models.Envelopeable carries no GetSerialNumber"},
	}

	tags, unplaced := groupByTag(emittanceModel(), refusals)
	if len(unplaced) != 0 {
		t.Errorf("unplaced = %+v, want none: every refusal names a derived entity", unplaced)
	}

	var preload *EmittanceEntity
	for i := range tags {
		for j := range tags[i].Entities {
			if tags[i].Entities[j].Key == "preload" {
				preload = &tags[i].Entities[j]
			}
		}
	}
	if preload == nil {
		t.Fatal("the entity is not in the report")
	}
	if len(preload.Causes) != 1 {
		t.Fatalf("got %d causes, want the two refusals gathered into one: %+v", len(preload.Causes), preload.Causes)
	}
	if got := preload.Causes[0].Attributes; len(got) != 2 || got[0] != "asset_tag" || got[1] != "serial_number" {
		t.Errorf("casualties = %v, want both, sorted", got)
	}
}

// TestUnit_RenderEmittance_AnEntityThatBecameNothingIsStillUnderItsTag holds
// the grouping to every entity, not only the ones that generated. An entity
// refused before it became any kind is most of what a large document
// refuses, and a reader looks for it where its siblings are.
func TestUnit_RenderEmittance_AnEntityThatBecameNothingIsStillUnderItsTag(t *testing.T) {
	tags, _ := groupByTag(emittanceModel(), nil)

	for _, tag := range tags {
		if tag.Name != "Tags" {
			continue
		}
		var found bool
		for _, e := range tag.Entities {
			if e.Key == "orphan" {
				found = true
				if len(e.Produced) != 0 {
					t.Errorf("orphan produced %v, want nothing", e.Produced)
				}
				if e.CollectionPath != "/v1/orphans" {
					t.Errorf("orphan carries no path to look up: %q", e.CollectionPath)
				}
			}
		}
		if !found {
			t.Errorf("the refused entity is not under its tag: %+v", tag.Entities)
		}
		return
	}
	t.Fatal("the tag is missing from the report")
}

// TestUnit_RenderEmittance_ARefusalNamingNoDerivedEntityIsNotFiled keeps the
// page honest. Inventing a tag for a refusal whose entity the run never
// derived would file it somewhere a reader cannot check.
func TestUnit_RenderEmittance_ARefusalNamingNoDerivedEntityIsNotFiled(t *testing.T) {
	refusals := []Unsupported{{
		Entity: "never_derived", Stage: StageBinding,
		Cause: &ir.Cause{Code: "noAccessor"}, Reason: "the SDK carries no such thing",
	}}
	tags, unplaced := groupByTag(emittanceModel(), refusals)
	if len(unplaced) != 1 || unplaced[0].Entity != "never_derived" {
		t.Errorf("unplaced = %+v, want the one refusal naming no derived entity", unplaced)
	}
	for _, tag := range tags {
		for _, e := range tag.Entities {
			if e.Key == "never_derived" {
				t.Error("a refusal naming no derived entity was filed under a tag")
			}
		}
	}
}

// TestUnit_RenderEmittance_IsAFunctionOfItsInput pins determinism. The
// report is manifest-covered and byte-compared, so a map iteration or a
// clock reaching the page would fail the drift gate on every run.
func TestUnit_RenderEmittance_IsAFunctionOfItsInput(t *testing.T) {
	e := Emittance{Provider: "acme", Document: EmittanceDocument{Source: "https://example.invalid/api.json"}}
	refusals := []Unsupported{{
		Kind: "datasource", Entity: "preload", Attribute: "asset_tag", Tag: "Inventory Preload",
		Cause: &ir.Cause{Code: "noAccessor", Subject: "models.Envelopeable"},
		Stage: StageBinding, Reason: "no getter",
	}}

	first, err := RenderEmittance(e, emittanceModel(), refusals)
	if err != nil {
		t.Fatalf("RenderEmittance: %v", err)
	}
	for range 4 {
		again, err := RenderEmittance(e, emittanceModel(), refusals)
		if err != nil {
			t.Fatalf("RenderEmittance: %v", err)
		}
		if string(again.Content) != string(first.Content) {
			t.Fatal("the report is not a function of its input")
		}
	}
	if first.Path != "generated_provider_acme.html" {
		t.Errorf("landed at %q", first.Path)
	}
	if !strings.Contains(string(first.Content), "https://example.invalid/api.json") {
		t.Error("the page does not say what document it is a fact about")
	}
}
