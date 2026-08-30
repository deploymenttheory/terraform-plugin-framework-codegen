package fixtures

import (
	"reflect"
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// nestedCollectionEntry is a fixture entry for a collection of collections
// with the given leaf value.
func nestedCollectionEntry(name string, collection ir.AttributeType, levels []ir.AttributeType, leaf any) Entry {
	return Entry{Name: name, Wire: name, Kind: collection, ElementType: levels[len(levels)-1],
		NestedCollectionElementTypes: levels, ComputedOptionalRequired: ir.Optional, Scalar: leaf}
}

// TestUnit_Fixtures_ANestedCollectionRendersOneMemberPerLevel holds the HCL
// and wire renderings of each of the four shapes to one member at every
// level, the leaf kept as its own kind, and a map keyed by the attribute's
// own name at every map level.
func TestUnit_Fixtures_ANestedCollectionRendersOneMemberPerLevel(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		entry    Entry
		wantHCL  string
		wantWire any
	}{
		{"list of lists", nestedCollectionEntry("grid", ir.TypeList, []ir.AttributeType{ir.TypeList, ir.TypeString}, "a"),
			`[["a"]]`, []any{[]any{"a"}}},
		{"list of maps", nestedCollectionEntry("rows", ir.TypeList, []ir.AttributeType{ir.TypeMap, ir.TypeBool}, true),
			`[{ "rows" = true }]`, []any{map[string]any{"rows": true}}},
		{"map of lists", nestedCollectionEntry("groups", ir.TypeMap, []ir.AttributeType{ir.TypeList, ir.TypeInt64}, int64(7)),
			`{ "groups" = [7] }`, map[string]any{"groups": []any{int64(7)}}},
		{"map of maps", nestedCollectionEntry("headers", ir.TypeMap, []ir.AttributeType{ir.TypeMap, ir.TypeString}, "v"),
			`{ "headers" = { "headers" = "v" } }`, map[string]any{"headers": map[string]any{"headers": "v"}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := scalarHCL(testCase.entry); got != testCase.wantHCL {
				t.Errorf("HCL = %s, want %s", got, testCase.wantHCL)
			}
			if got := wireOne(testCase.entry, ConfigMaximal); !reflect.DeepEqual(got, testCase.wantWire) {
				t.Errorf("wire = %#v, want %#v", got, testCase.wantWire)
			}
			var b strings.Builder
			writeWireValue(&b, testCase.entry, ConfigMaximal, 0)
			if !strings.Contains(b.String(), "[") && !strings.Contains(b.String(), "{") {
				t.Errorf("written wire = %s, want a collection", b.String())
			}
		})
	}
}

// TestUnit_Fixtures_ANestedCollectionValueTakesTheLeafKind proves the
// derived fixture value of a collection of collections is the leaf's,
// with the levels carried onto the entry.
func TestUnit_Fixtures_ANestedCollectionValueTakesTheLeafKind(t *testing.T) {
	tree := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "groups", WireName: "groups", Type: ir.TypeMap, ElementType: ir.TypeInt64,
			NestedCollectionElementTypes: []ir.AttributeType{ir.TypeList, ir.TypeInt64}, ComputedOptionalRequired: ir.Optional},
	}}
	entries, omissions := deriveTree(tree, nil)
	if len(omissions) != 0 || len(entries) != 1 {
		t.Fatalf("derived %d entries and %d omissions", len(entries), len(omissions))
	}
	groups := entries[0]
	if groups.Scalar != int64(7) || groups.CollectionNestingDepth() != 2 || !reflect.DeepEqual(groups.CollectionLevels(), []ir.AttributeType{ir.TypeMap, ir.TypeList}) {
		t.Errorf("derived %+v", groups)
	}
}

// TestUnit_Fixtures_AReplayDescendsEveryLevelToTheLeaf holds the replay of
// a body carrying a collection of collections to its first leaf, and an
// empty level to the derived value.
func TestUnit_Fixtures_AReplayDescendsEveryLevelToTheLeaf(t *testing.T) {
	entry := nestedCollectionEntry("headers", ir.TypeMap, []ir.AttributeType{ir.TypeMap, ir.TypeString}, "derived")
	body := map[string]any{"headers": map[string]any{"zeta": map[string]any{"k": "late"}, "alpha": map[string]any{"k": "first"}}}
	kept, dropped := overlayEntries([]Entry{entry}, body, nil, nil, nil, false)
	if len(dropped) != 0 || len(kept) != 1 || kept[0].Scalar != "first" {
		t.Errorf("replayed %+v (dropped %v), want the leaf under the smallest key", kept, dropped)
	}

	empty := map[string]any{"headers": map[string]any{}}
	kept, _ = overlayEntries([]Entry{entry}, empty, nil, nil, nil, false)
	if len(kept) != 1 || kept[0].Scalar != "derived" {
		t.Errorf("an empty level replayed %+v, want the derived value kept", kept)
	}

	list := nestedCollectionEntry("grid", ir.TypeList, []ir.AttributeType{ir.TypeList, ir.TypeString}, "derived")
	kept, _ = overlayEntries([]Entry{list}, map[string]any{"grid": []any{[]any{"x", "y"}, []any{"z"}}}, nil, nil, nil, false)
	if len(kept) != 1 || kept[0].Scalar != "x" {
		t.Errorf("a list of lists replayed %+v, want its first leaf", kept)
	}
}
