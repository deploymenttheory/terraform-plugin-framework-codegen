package sdkbind

import (
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// nestedBinding is a field binding for a collection of collections, the
// levels spelled the way the derivation carries them: beneath the outer
// collection, outermost first, ending in the leaf.
func nestedBinding(collection ir.AttributeType, levels ...ir.AttributeType) FieldBinding {
	return FieldBinding{Attr: "grid", Wire: "grid", Type: collection, ElementType: levels[len(levels)-1],
		NestedCollectionElementTypes: levels, Access: FieldAccess{Get: "GetGrid", Set: "SetGrid"}}
}

// bagNamed is a kiota-shaped model carrying an additionalData bag.
func bagNamed(name string) *types.Named {
	goPackage := types.NewPackage("example.com/sdk/models", "models")
	obj := types.NewTypeName(0, goPackage, name, nil)
	named := types.NewNamed(obj, types.NewStruct(nil, nil), nil)
	sig := types.NewSignatureType(types.NewVar(0, nil, "m", named), nil, nil, nil,
		types.NewTuple(types.NewVar(0, nil, "", types.NewMap(types.Typ[types.String], types.NewInterfaceType(nil, nil)))), false)
	named.AddMethod(types.NewFunc(0, goPackage, "GetAdditionalData", sig))
	return named
}

// untypedNodeNamed is kiota's UntypedNodeable interface, at the package
// path the binder matches on.
func untypedNodeNamed() *types.Named {
	goPackage := types.NewPackage("github.com/microsoft/kiota-abstractions-go/serialization", "serialization")
	obj := types.NewTypeName(0, goPackage, "UntypedNodeable", nil)
	return types.NewNamed(obj, types.NewInterfaceType(nil, nil), nil)
}

// emptyPruner is a pruner with nothing loaded, enough for
// writeModelFromNamed to fall through to the struct literal.
func emptyPruner() *pruner {
	return &pruner{l: &loader{goPackages: map[string]*packages.Package{}}}
}

// TestUnit_SettleCollection_BridgesPlainNestedTypes holds every pairing of
// list and map at two levels, and one at three, to the plain bridge: the
// SDK type spelled as the SDK carries it, the shorthand naming the outer
// collection, and a narrower leaf width accepted as the scalar bridges do.
func TestUnit_SettleCollection_BridgesPlainNestedTypes(t *testing.T) {
	str, int32Type := types.Typ[types.String], types.Typ[types.Int32]
	for _, testCase := range []struct {
		name     string
		binding  FieldBinding
		sdk      types.Type
		wantType string
		wantGet  string
	}{
		{"list of lists", nestedBinding(ir.TypeList, ir.TypeList, ir.TypeString),
			types.NewSlice(types.NewSlice(str)), "[][]string", "FromNestedCollectionSlice"},
		{"list of maps", nestedBinding(ir.TypeList, ir.TypeMap, ir.TypeString),
			types.NewSlice(types.NewMap(str, str)), "[]map[string]string", "FromNestedCollectionSlice"},
		{"map of lists", nestedBinding(ir.TypeMap, ir.TypeList, ir.TypeInt64),
			types.NewMap(str, types.NewSlice(int32Type)), "map[string][]int32", "FromNestedCollectionMap"},
		{"map of maps", nestedBinding(ir.TypeMap, ir.TypeMap, ir.TypeBool),
			types.NewMap(str, types.NewMap(str, types.Typ[types.Bool])), "map[string]map[string]bool", "FromNestedCollectionMap"},
		{"list of lists of lists", nestedBinding(ir.TypeList, ir.TypeList, ir.TypeList, ir.TypeFloat64),
			types.NewSlice(types.NewSlice(types.NewSlice(types.Typ[types.Float64]))), "[][][]float64", "FromNestedCollectionSlice"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fb := testCase.binding
			if why := emptyPruner().settleScalar(&fb, testCase.sdk); why.refused() {
				t.Fatalf("refused: %s", why.Reason)
			}
			if fb.Access.SDKType != testCase.wantType {
				t.Errorf("SDKType = %q, want %q", fb.Access.SDKType, testCase.wantType)
			}
			if fb.Access.ConvertGet != testCase.wantGet || fb.Access.ConvertSet != strings.Replace(testCase.wantGet, "From", "To", 1) {
				t.Errorf("conversions = %q / %q, want %q and its To twin", fb.Access.ConvertGet, fb.Access.ConvertSet, testCase.wantGet)
			}
		})
	}
}

// TestUnit_SettleCollection_ExcludesWhatDoesNotNestAsDeclared holds the
// plain bridge to an exact match of every level: a shallower type, a
// deeper one, a pointer, a non-string key and a named leaf all name the
// declared shape in the reason rather than settling to a walk that would
// panic.
func TestUnit_SettleCollection_ExcludesWhatDoesNotNestAsDeclared(t *testing.T) {
	str := types.Typ[types.String]
	enum := structNamed("example.com/sdk/models", "models", "Kind")
	for _, testCase := range []struct {
		name    string
		binding FieldBinding
		sdk     types.Type
	}{
		{"one level short", nestedBinding(ir.TypeList, ir.TypeList, ir.TypeString), types.NewSlice(str)},
		{"one level deep", nestedBinding(ir.TypeList, ir.TypeList, ir.TypeString), types.NewSlice(types.NewSlice(types.NewSlice(str)))},
		{"map where a list is declared", nestedBinding(ir.TypeList, ir.TypeList, ir.TypeString), types.NewSlice(types.NewMap(str, str))},
		{"pointer at a level", nestedBinding(ir.TypeList, ir.TypeList, ir.TypeString), types.NewSlice(types.NewPointer(types.NewSlice(str)))},
		{"non-string key", nestedBinding(ir.TypeMap, ir.TypeList, ir.TypeString), types.NewMap(types.Typ[types.Int], types.NewSlice(str))},
		{"named leaf", nestedBinding(ir.TypeList, ir.TypeList, ir.TypeString), types.NewSlice(types.NewSlice(enum))},
		{"incompatible leaf", nestedBinding(ir.TypeList, ir.TypeList, ir.TypeBool), types.NewSlice(types.NewSlice(str))},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fb := testCase.binding
			why := emptyPruner().settleScalar(&fb, testCase.sdk)
			if !why.refused() {
				t.Fatalf("settled to %q", fb.Access.ConvertGet)
			}
			if why.Cause.Code != CauseUnbridgeableType || !strings.Contains(why.Reason, "does not nest the way the document declares a "+describeCollection(&fb)) {
				t.Errorf("reason = %q (%s)", why.Reason, why.Cause.Code)
			}
		})
	}
}

// TestUnit_SettleCollection_MapOfListsThroughAdditionalData settles a map
// whose values are lists onto the bag model kiota carries it as, the
// model and its constructor travelling for the write.
func TestUnit_SettleCollection_MapOfListsThroughAdditionalData(t *testing.T) {
	fb := nestedBinding(ir.TypeMap, ir.TypeList, ir.TypeString)
	if why := emptyPruner().settleScalar(&fb, bagNamed("Thing_mapOfLists")); why.refused() {
		t.Fatalf("refused: %s", why.Reason)
	}
	if fb.Access.ConvertGet != "FromNestedCollectionMapAdditionalData" || fb.Access.ConvertSet != "ToNestedCollectionMapAdditionalData" {
		t.Errorf("conversions = %q / %q", fb.Access.ConvertGet, fb.Access.ConvertSet)
	}
	if fb.NestedModel != "models.Thing_mapOfLists" || fb.NestedConstructor == "" || fb.NestedWriteModel == "" {
		t.Errorf("model = %q, write model = %q, constructor = %q", fb.NestedModel, fb.NestedWriteModel, fb.NestedConstructor)
	}
}

// TestUnit_SettleCollection_ListOfBagsCarriesTheConstructor settles a list
// of maps onto the slice of bag models kiota carries it as.
func TestUnit_SettleCollection_ListOfBagsCarriesTheConstructor(t *testing.T) {
	fb := nestedBinding(ir.TypeList, ir.TypeMap, ir.TypeString)
	if why := emptyPruner().settleScalar(&fb, types.NewSlice(bagNamed("Thing_listOfMaps"))); why.refused() {
		t.Fatalf("refused: %s", why.Reason)
	}
	if fb.Access.SDKType != "[]models.Thing_listOfMaps" {
		t.Errorf("SDKType = %q", fb.Access.SDKType)
	}
	if fb.Access.ConvertGet != "FromNestedCollectionSliceAdditionalData" || fb.Access.ConvertSet != "ToNestedCollectionSliceAdditionalData" {
		t.Errorf("conversions = %q / %q", fb.Access.ConvertGet, fb.Access.ConvertSet)
	}
	if fb.NestedModel != "models.Thing_listOfMaps" || fb.NestedConstructor == "" {
		t.Errorf("model = %q, constructor = %q", fb.NestedModel, fb.NestedConstructor)
	}
}

// TestUnit_SettleCollection_ListOfListsThroughUntypedNode settles a list of
// lists onto kiota's untyped node, matched on its package path, and holds
// a map declared over the same node to an exclusion: the node walks to a
// list and nothing else.
func TestUnit_SettleCollection_ListOfListsThroughUntypedNode(t *testing.T) {
	fb := nestedBinding(ir.TypeList, ir.TypeList, ir.TypeString)
	if why := emptyPruner().settleScalar(&fb, untypedNodeNamed()); why.refused() {
		t.Fatalf("refused: %s", why.Reason)
	}
	if fb.Access.SDKType != "serialization.UntypedNodeable" || !fb.Access.NestedNilable {
		t.Errorf("SDKType = %q, nilable %v", fb.Access.SDKType, fb.Access.NestedNilable)
	}
	if fb.Access.ConvertGet != "FromNestedCollectionSliceUntypedNode" || fb.Access.ConvertSet != "ToNestedCollectionSliceUntypedNode" {
		t.Errorf("conversions = %q / %q", fb.Access.ConvertGet, fb.Access.ConvertSet)
	}

	asMap := nestedBinding(ir.TypeMap, ir.TypeList, ir.TypeString)
	if why := emptyPruner().settleScalar(&asMap, untypedNodeNamed()); !why.refused() || !strings.Contains(why.Reason, "untyped node") {
		t.Errorf("a map over an untyped node settled, or the reason does not say why: %s", why.Reason)
	}

	foreign := types.NewNamed(types.NewTypeName(0, types.NewPackage("example.com/other/serialization", "serialization"), "UntypedNodeable", nil), types.NewInterfaceType(nil, nil), nil)
	if isKiotaUntypedNode(foreign) {
		t.Error("another SDK's UntypedNodeable matched kiota's")
	}
}

// TestUnit_NestedModelOf_UnwrapsEveryCollectionLevel finds the object model
// at the bottom of a collection of collections, so a nested-attribute tree
// beneath one resolves against the model rather than against the slice.
func TestUnit_NestedModelOf_UnwrapsEveryCollectionLevel(t *testing.T) {
	leaf := structNamed("example.com/sdk/models", "models", "Leaf")
	for name, sdk := range map[string]types.Type{
		"list of lists of objects": types.NewSlice(types.NewSlice(leaf)),
		"map of lists of objects":  types.NewMap(types.Typ[types.String], types.NewSlice(types.NewPointer(leaf))),
	} {
		named, why := nestedModelOf(sdk)
		if why.refused() || named != leaf {
			t.Errorf("%s: model = %v (%s), want models.Leaf", name, named, why.Reason)
		}
	}
}

// TestUnit_NestedCollectionGoType_ComposesEveryLevel holds the type a
// binder drafts for a plain SDK to the levels, the leaf spelled as given.
func TestUnit_NestedCollectionGoType_ComposesEveryLevel(t *testing.T) {
	if got := nestedCollectionGoType([]ir.AttributeType{ir.TypeList, ir.TypeMap}, "string"); got != "[]map[string]string" {
		t.Errorf("drafted %q", got)
	}
	if got := nestedCollectionGoType(nil, "int64"); got != "int64" {
		t.Errorf("a leaf alone drafted %q", got)
	}
	if got := describeCollection(&FieldBinding{Type: ir.TypeMap, NestedCollectionElementTypes: []ir.AttributeType{ir.TypeList, ir.TypeString}}); got != "map of list of string" {
		t.Errorf("described %q", got)
	}
}
