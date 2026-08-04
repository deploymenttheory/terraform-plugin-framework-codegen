package fixturespec

import (
	"reflect"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

func strAttr(name string) blueprint.Attribute {
	return blueprint.Attribute{
		Name:                     name,
		ComputedOptionalRequired: blueprint.Optional,
		Type:                     blueprint.AttrType{Kind: blueprint.KindString},
	}
}

func TestUnit_Fixturespec_ObservedEvidenceBeatsDocumentation(t *testing.T) {
	a := strAttr("access_type")
	a.Type.AllowedValues = []string{"system", "user"}
	a.Behaviour.AcceptedValues = []string{"user"}
	a.Behaviour.RejectedValues = []string{"system"}

	e := Derive(a, "")
	if e.Value != "user" || e.Source != SourceObservedAccepted {
		t.Fatalf("want observed-accepted %q, got %+v", "user", e)
	}

	a.Behaviour.AcceptedValues = nil
	a.Behaviour.RejectedValues = nil
	e = Derive(a, "")
	if e.Value != "system" || e.Source != SourceDocumented {
		t.Fatalf("want documented %q, got %+v", "system", e)
	}
}

func TestUnit_Fixturespec_AServerDefaultCarriesBothWireValueAndVerbatimLiteral(t *testing.T) {
	cases := []struct {
		raw  string
		kind blueprint.TypeKind
		want any
	}{
		{`true`, blueprint.KindBool, true},
		{`3600`, blueprint.KindInt64, int64(3600)},
		{`"0"`, blueprint.KindString, "0"},
		{`1.5`, blueprint.KindNumber, 1.5},
	}
	for _, c := range cases {
		a := strAttr("field")
		a.Type.Kind = c.kind
		a.Behaviour.ServerDefault = &blueprint.Literal{Raw: c.raw}

		e := Derive(a, "")
		if e.Verbatim != c.raw {
			t.Fatalf("raw %s: verbatim must be the evidence's own spelling, got %q", c.raw, e.Verbatim)
		}
		if !reflect.DeepEqual(e.Value, c.want) {
			t.Fatalf("raw %s: want wire value %#v, got %#v", c.raw, c.want, e.Value)
		}
		if e.Source != SourceServerDefault {
			t.Fatalf("raw %s: want source server-default, got %q", c.raw, e.Source)
		}
	}
}

func TestUnit_Fixturespec_AnEmptyStringDefaultIsNeverAFixtureValue(t *testing.T) {
	a := strAttr("content_regex")
	a.Behaviour.ServerDefault = &blueprint.Literal{Raw: `""`}

	e := Derive(a, "")
	if e.Source == SourceServerDefault {
		t.Fatal("an empty-string default is indistinguishable from absence and must not be configured")
	}
}

func TestUnit_Fixturespec_RefusalsNameTheirReason(t *testing.T) {
	for _, name := range []string{"password", "client_certificate", "username"} {
		if e := Derive(strAttr(name), ""); !e.Skipped {
			t.Fatalf("%s: credential-shaped names must be refused, got %+v", name, e)
		}
	}

	if e := Derive(strAttr("override_proxy_id"), ""); !e.Skipped {
		t.Fatalf("_id-suffixed names reference live objects and must be refused, got %+v", e)
	}

	nested := strAttr("assignments")
	nested.Type.Kind = blueprint.KindListNested
	if e := Derive(nested, ""); !e.Skipped {
		t.Fatalf("nested kinds must be refused, got %+v", e)
	}
}

func TestUnit_Fixturespec_SynthesisedValuesAreWireTyped(t *testing.T) {
	s := Derive(strAttr("dns_override"), "")
	if s.Value != "tfacc-dns-override" {
		t.Fatalf("string synthesis: got %#v", s.Value)
	}

	salted := Derive(strAttr("dns_override"), "seed")
	if salted.Value != "tfacc-seed-dns-override" {
		t.Fatalf("salted string synthesis: got %#v", salted.Value)
	}

	b := strAttr("enabled")
	b.Type.Kind = blueprint.KindBool
	if e := Derive(b, ""); e.Value != true {
		t.Fatalf("bool synthesis: got %#v", e.Value)
	}

	n := strAttr("interval")
	n.Type.Kind = blueprint.KindInt64
	min := 60.0
	n.Type.Constraints.Minimum = &min
	if e := Derive(n, ""); e.Value != int64(60) {
		t.Fatalf("int synthesis honours bounds: got %#v", e.Value)
	}

	l := strAttr("headers")
	l.Type.Kind = blueprint.KindList
	l.Type.ElementType = &blueprint.AttrType{Kind: blueprint.KindString}
	e := Derive(l, "")
	want := []any{"tfacc-headers-element"}
	if !reflect.DeepEqual(e.Value, want) {
		t.Fatalf("collection synthesis: want %#v, got %#v", want, e.Value)
	}
}

func TestUnit_Fixturespec_WantsSelectsMinimalByEvidenceNotOnlySchema(t *testing.T) {
	required := strAttr("key")
	required.ComputedOptionalRequired = blueprint.Required

	optional := strAttr("description")

	enforced := strAttr("value")
	yes := true
	enforced.Behaviour.RequiredByAPI = &yes

	computed := strAttr("id")
	computed.ComputedOptionalRequired = blueprint.Computed

	if !Wants(required, true) || !Wants(enforced, true) {
		t.Fatal("minimal must include schema-required and API-enforced attributes")
	}
	if Wants(optional, true) {
		t.Fatal("minimal must exclude plain optional attributes")
	}
	if Wants(computed, true) || Wants(computed, false) {
		t.Fatal("computed attributes are never configurable")
	}
	if !Wants(optional, false) {
		t.Fatal("maximal includes every writable attribute")
	}
}

func TestUnit_Fixturespec_AnUnparseableLiteralFallsBackToItsRawSpelling(t *testing.T) {
	if got := parseLiteral("[1, 2]"); got != "[1, 2]" {
		t.Fatalf("want raw fallback, got %#v", got)
	}
}
