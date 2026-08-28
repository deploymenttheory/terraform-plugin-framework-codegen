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
	enumerated := ir.Attribute{Name: "modules", Kind: ir.TypeList, ElementType: ir.TypeString, OneOf: []string{"default", "extended"}}
	if v, _ := scalarFor(enumerated.ElementType, enumerated, []string{"modules"}); v != "default" {
		t.Errorf("an enumerated list element = %#v, want the first member", v)
	}
	exampled := ir.Attribute{Name: "modules", Kind: ir.TypeList, ElementType: ir.TypeString, Example: []any{"default"}}
	if v, invented := scalarFor(exampled.ElementType, exampled, []string{"modules"}); v != "default" || invented == "" {
		t.Errorf("an exampled list element = %#v (invented %q), want the example's first member with the name kept", v, invented)
	}
	bare := ir.Attribute{Name: "modules", Kind: ir.TypeList, ElementType: ir.TypeString, Example: []any{7}}
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
