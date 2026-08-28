package run

import (
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
)

// A serverForced finding must not be manufactured out of the audit's own
// probe value. Sending the string "120" into an integer enum, having the API
// accept it and answer with the integer 120, and comparing the echo
// byte-strictly reads as the server substituting a value of its own. It is
// not: the value came back unchanged, in the type the document declared.
//
// The distinction matters because serverForced makes an attribute Computed,
// taking it away from practitioners. Three separate things have to hold for
// the comparison to be honest, and each is pinned here.

func TestUnit_Run_IntegerEnumVariantKeepsItsDeclaredType(t *testing.T) {
	h := strategy.SynthHint{
		Field: "interval",
		Type:  "integer",
		Enum:  []any{60, 120, 300, 600, 900, 1800, 3600},
	}
	got, ok := variantValue(h, 60)
	if !ok {
		t.Fatal("no variant for a seven-member enum")
	}
	if _, isString := got.(string); isString {
		t.Fatalf("variant %#v is a string; an integer enum must go on the wire as an integer", got)
	}
	if got != 120 {
		t.Errorf("variant = %#v, want the document's next member, 120", got)
	}
}

// Sorting enum members as text made "120" the first of
// [60 120 300 600 900 1800 3600], so both the value synthesis reached for and
// the members the per-value probes covered were chosen by spelling.
func TestUnit_Run_EnumVariantFollowsDocumentOrder(t *testing.T) {
	h := strategy.SynthHint{Field: "interval", Type: "integer", Enum: []any{60, 120, 300}}
	if got := synthValue(h, "e", "p"); got != 60 {
		t.Errorf("synthValue = %#v, want the document's first member, 60", got)
	}
}

// The base value arrives from a decoded response body as a float64 while the
// document holds ints, so the "is this the value we already have" check has to
// compare across types or it hands back the value it was told to move away
// from — an update that changes nothing, which reads as ignoredOnUpdate.
func TestUnit_Run_EnumVariantDiffersFromABaseOfAnotherNumericType(t *testing.T) {
	h := strategy.SynthHint{Field: "interval", Type: "integer", Enum: []any{60, 120}}
	got, ok := variantValue(h, float64(60))
	if !ok {
		t.Fatal("no variant")
	}
	if got == 60 {
		t.Error("variant repeats the base value; the update would prove nothing")
	}
}

// A probe outside the declared bounds tests the API's validation, not the
// field. httpTimeLimit declares minimum 5, and the audit sent the constant 2.
func TestUnit_Run_NumericVariantStaysInsideDeclaredBounds(t *testing.T) {
	min, max := 5.0, 60.0
	h := strategy.SynthHint{Field: "httpTimeLimit", Type: "integer", Minimum: &min, Maximum: &max}

	got, ok := variantValue(h, 5)
	if !ok {
		t.Fatal("no variant inside 5..60")
	}
	f, _ := asFloat(got)
	if f < min || f > max {
		t.Errorf("variant %#v is outside the declared 5..60", got)
	}

	// At the ceiling the only way to move is down.
	got, ok = variantValue(h, 60)
	if !ok {
		t.Fatal("no variant at the ceiling")
	}
	if f, _ = asFloat(got); f != 59 {
		t.Errorf("variant at the ceiling = %#v, want 59", got)
	}

	// A field pinned to one legal value has no variant worth sending.
	only := 7.0
	pinned := strategy.SynthHint{Field: "x", Type: "integer", Minimum: &only, Maximum: &only}
	if v, ok := variantValue(pinned, 7); ok {
		t.Errorf("variant = %#v for a field with a single legal value; want none", v)
	}
}

// Even with the probe fixed, an API free to answer "120" for an integer it was
// given as 120 must not read as a substitution.
func TestUnit_Run_EqualJSONAcceptsTheSameNumberInAnotherType(t *testing.T) {
	for _, c := range []struct {
		name string
		a, b any
	}{
		{"string echo of an int", "120", 120},
		{"int against a decoded float", 120, float64(120)},
		{"int64 against a decoded float", int64(120), float64(120)},
	} {
		if !equalJSON(c.a, c.b) {
			t.Errorf("%s: %#v and %#v read as different values", c.name, c.a, c.b)
		}
	}
	// Numbers only. An API answering true for 1 is doing something worth
	// recording, and a string that is not a number is simply a different value.
	for _, c := range []struct {
		name string
		a, b any
	}{
		{"bool is not one", true, 1},
		{"different numbers", 60, 120},
		{"non-numeric string", "auto", 0},
	} {
		if equalJSON(c.a, c.b) {
			t.Errorf("%s: %#v and %#v read as the same value", c.name, c.a, c.b)
		}
	}
}
