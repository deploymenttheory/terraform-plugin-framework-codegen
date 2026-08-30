package fixtures

import (
	"regexp"
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

func TestUnit_Fixturespec_AReplayedRequiredPropertyTakesTheAPIsSpelling(t *testing.T) {
	spec := Derive(acceptedTree())
	// The API stores the required colour in its own spelling: the colour
	// takes the spelling. Answered masked instead, it stays as sent because
	// the create needs it.
	got := spec.FromAcceptedRequestBody(
		map[string]any{"name": "n", "colour": "#ff0000"},
		map[string]any{"name": "n", "colour": "#FF0000"},
		map[string]bool{"name": true, "colour": true})
	if v := valueByName(t, got, "colour").Scalar; v != "#FF0000" {
		t.Errorf("colour = %#v, want the spelling the API stored", v)
	}
	masked := spec.FromAcceptedRequestBody(
		map[string]any{"name": "n", "colour": "#ff0000"},
		map[string]any{"name": "n", "colour": "*****"},
		map[string]bool{"name": true, "colour": true})
	if v := valueByName(t, masked, "colour").Scalar; v != "#ff0000" {
		t.Errorf("colour = %#v, want the value sent, since the create needs it", v)
	}
	if len(got.Omissions) != 0 || len(masked.Omissions) != 0 {
		t.Errorf("a required property was dropped: %#v %#v", got.Omissions, masked.Omissions)
	}
}

func TestUnit_Fixturespec_AnExpressionRendersUnquoted(t *testing.T) {
	spec := Derive(acceptedTree())
	got := spec.WithExpression("name", "petstore_owner.owner.id")
	if v := valueByName(t, got, "name").Expression; v != "petstore_owner.owner.id" {
		t.Fatalf("expression = %q, want it set on the named entry", v)
	}
	if spec.Entries[1].Expression != "" {
		t.Fatal("WithExpression changed the fixture it was called on")
	}
	hcl := got.HCL(ConfigMaximal)
	if !regexp.MustCompile(`(?m)^  name +?= petstore_owner\.owner\.id$`).MatchString(hcl) {
		t.Errorf("HCL = %q, want the expression rendered bare", hcl)
	}
	if strings.Contains(hcl, `"petstore_owner.owner.id"`) {
		t.Errorf("HCL = %q, want the expression unquoted", hcl)
	}
	// A name the fixture does not carry changes nothing.
	if same := spec.WithExpression("absent", "x"); len(same.Entries) != len(spec.Entries) {
		t.Errorf("an absent name changed the entries: %d", len(same.Entries))
	}
}

func TestUnit_Fixturespec_AListTakesItsEnumOrItsExamplesFirstMember(t *testing.T) {
	enumerated := ir.Attribute{Name: "modules", Type: ir.TypeList, ElementType: ir.TypeString, OneOf: []string{"default", "extended"}}
	if v, _ := scalarFor(enumerated.ElementType, enumerated, []string{"modules"}); v != "default" {
		t.Errorf("an enumerated list element = %#v, want the first member", v)
	}
	exampled := ir.Attribute{Name: "modules", Type: ir.TypeList, ElementType: ir.TypeString, Example: []any{"default"}}
	if v, invented := scalarFor(exampled.ElementType, exampled, []string{"modules"}); v != "default" || invented == "" {
		t.Errorf("an exampled list element = %#v (invented %q), want the example's first member with the name kept", v, invented)
	}
	bare := ir.Attribute{Name: "modules", Type: ir.TypeList, ElementType: ir.TypeString, Example: []any{7}}
	if v, _ := scalarFor(bare.ElementType, bare, []string{"modules"}); v != NamePrefix+"modules" {
		t.Errorf("a list whose example is not strings = %#v, want the invented name", v)
	}
}

func TestUnit_Fixturespec_AFutureDateRendersATimeOffset(t *testing.T) {
	spec := Fixture{Entries: []Entry{
		{Name: "name", Wire: "name", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required, Scalar: "n"},
		{Name: "start_date", Wire: "startDate", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required, Scalar: "2026-08-29T00:00:00Z"},
		{Name: "count", Wire: "count", Kind: ir.TypeInt64, ComputedOptionalRequired: ir.Optional, Scalar: int64(3)},
	}}
	got, rewritten := spec.WithFutureDates([]string{"startDate", "count"})
	if len(rewritten) != 1 || rewritten[0] != "start_date" {
		t.Fatalf("rewritten = %v, want the string timestamp alone", rewritten)
	}
	if v := valueByName(t, got, "start_date").Expression; v != "time_offset.start_date.rfc3339" {
		t.Errorf("expression = %q", v)
	}
	if spec.Entries[1].Expression != "" {
		t.Error("WithFutureDates changed the fixture it was called on")
	}
	if !strings.Contains(FutureDateBlock("start_date"), `resource "time_offset" "start_date"`) {
		t.Errorf("block = %q", FutureDateBlock("start_date"))
	}
}

func TestUnit_Fixturespec_AnEmptyListTheAPITookIsLeftOut(t *testing.T) {
	spec := Derive(acceptedTree())
	got := spec.FromAcceptedRequestBody(
		map[string]any{"name": "n", "labels": []any{}},
		map[string]any{"name": "n", "labels": []any{}},
		map[string]bool{"name": true})
	for _, e := range got.Entries {
		if e.Name == "labels" {
			t.Fatalf("an empty list was replayed as %#v", e)
		}
	}
	var explained bool
	for _, o := range got.Omissions {
		if strings.Contains(o.Name, "labels") && strings.Contains(o.Reason, "no value in it") {
			explained = true
		}
	}
	if !explained {
		t.Errorf("the empty list was not explained: %#v", got.Omissions)
	}
}

func TestUnit_Fixturespec_AReferenceReachesEveryDepth(t *testing.T) {
	spec := Fixture{Entries: []Entry{
		{Name: "rule_ids", Wire: "ruleIds", Kind: ir.TypeList, ElementType: ir.TypeString, Scalar: "1"},
		{Name: "agents", Wire: "agents", Kind: ir.TypeList, ElementType: ir.TypeObject, Nested: []Entry{
			{Name: "agent_id", Wire: "agentId", Kind: ir.TypeString, Scalar: "3"},
		}},
		{Name: "name", Wire: "name", Kind: ir.TypeString, Scalar: "n"},
	}}
	got, took := spec.WithReference("agentId", "petstore_agent.agent.id")
	if !took || got.Entries[1].Nested[0].Expression != "petstore_agent.agent.id" || spec.Entries[1].Nested[0].Expression != "" {
		t.Errorf("a nested reference = %+v (took %v), want the expression set on the copy alone", got.Entries[1].Nested[0], took)
	}
	got, took = got.WithReference("ruleIds", "petstore_rule.rule.id")
	if !took || !strings.Contains(got.HCL(ConfigMaximal), "rule_ids = [petstore_rule.rule.id]") {
		t.Errorf("a list reference rendered as %q", got.HCL(ConfigMaximal))
	}
	if _, took := spec.WithReference("absent", "x"); took {
		t.Error("a wire name the fixture does not carry took an expression")
	}
}

func TestUnit_Fixturespec_AReplayedNumberOutsideTheDeclaredBoundsKeepsTheDerivedValue(t *testing.T) {
	low, high := 1000.0, 6000.0
	spec := Fixture{Entries: []Entry{
		{Name: "name", Wire: "name", Kind: ir.TypeString, ComputedOptionalRequired: ir.Required, Scalar: "n"},
		{Name: "target_time", Wire: "targetTime", Kind: ir.TypeInt64, ComputedOptionalRequired: ir.Optional, Scalar: int64(1000), Minimum: &low, Maximum: &high},
	}}
	got := spec.FromAcceptedRequestBody(
		map[string]any{"name": "n", "targetTime": float64(1)},
		map[string]any{"name": "n", "targetTime": float64(1)},
		map[string]bool{"name": true})
	if v := valueByName(t, got, "target_time").Scalar; v != int64(1000) {
		t.Errorf("target_time = %#v, want the derived value inside the bounds", v)
	}
	within := spec.FromAcceptedRequestBody(
		map[string]any{"name": "n", "targetTime": float64(2000)},
		map[string]any{"name": "n", "targetTime": float64(2000)},
		map[string]bool{"name": true})
	if v := valueByName(t, within, "target_time").Scalar; v != float64(2000) {
		t.Errorf("target_time = %#v, want the accepted value inside the bounds", v)
	}
}
