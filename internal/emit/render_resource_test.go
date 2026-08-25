package emit

import (
	"strconv"
	"strings"
	"testing"
)

// TestUnit_Emit_AWireFixtureCarryingABacktickStaysOneLiteral pins the literal
// a value cannot end early. A document's own example may contain a backtick,
// and a raw literal has no escape for one, so the generated file would stop
// being Go at that character.
func TestUnit_Emit_AWireFixtureCarryingABacktickStaysOneLiteral(t *testing.T) {
	plain := goStringLiteral("{\n  \"id\": \"x\"\n}")
	if plain != "`{\n  \"id\": \"x\"\n}`" {
		t.Errorf("a value with no backtick = %s, want a raw literal", plain)
	}

	quoted := goStringLiteral("the pattern `/` never matches")
	if strings.HasPrefix(quoted, "`") {
		t.Fatalf("a value carrying a backtick = %s, want an interpreted literal", quoted)
	}
	unquoted, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatalf("the rendered literal does not parse as one: %v", err)
	}
	if unquoted != "the pattern `/` never matches" {
		t.Errorf("round trip = %q", unquoted)
	}
}
