package sdkbind

import (
	"go/types"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// settleCollection settles a collection of collections against the type the
// SDK carries it as, matching every level the document declares to the
// carrier. Four carriers exist, and each bridges through one helper pair
// that takes the whole value and the framework element type:
//
//   - a plain Go value — [][]string, map[string][]int64 — which the framework's
//     own reflection walks;
//   - one kiota model whose only content is an additionalData bag, which is
//     how kiota carries a map whose values are lists or maps;
//   - a slice of such models, which is how kiota carries a list of maps;
//   - kiota's untyped node, which is how it carries a list of lists — the
//     generator declares no element type at all, so the node is walked at
//     runtime.
func (p *pruner) settleCollection(fb *FieldBinding, t types.Type) refusal {
	fa := &fb.Access
	settle := func(sdkType, get, set string) {
		fa.SDKType = sdkType
		fa.ConvertGet, fa.ConvertSet = get, set
		if fa.Get == "" {
			fa.ConvertGet = ""
		}
		if fa.Set == "" {
			fa.ConvertSet = ""
		}
	}
	get, set := nestedCollectionShorthand(fb.Type)

	if named, ok := t.(*types.Named); ok && isKiotaUntypedNode(named) {
		if fb.Type != ir.TypeList {
			return because(CauseUnbridgeableType, shortType(t),
				"the SDK carries it as %s, an untyped node, which bridges to a list and not to a %s", shortType(t), describeCollection(fb))
		}
		fa.NestedNilable = true
		settle("serialization.UntypedNodeable", get+"UntypedNode", set+"UntypedNode")
		return refusal{}
	}

	if fb.Type == ir.TypeMap {
		if named, ok := t.(*types.Named); ok && carriesAdditionalData(named) {
			if why := p.settleBag(fb, named, t); why.refused() {
				return why
			}
			settle(shortType(t), get+"AdditionalData", set+"AdditionalData")
			return refusal{}
		}
	}

	if slice, ok := t.Underlying().(*types.Slice); ok && fb.Type == ir.TypeList && fb.NestedCollectionElementTypes[0] == ir.TypeMap {
		if named, isNamed := slice.Elem().(*types.Named); isNamed && carriesAdditionalData(named) {
			if why := p.settleBag(fb, named, slice.Elem()); why.refused() {
				return why
			}
			settle(shortType(t), get+"AdditionalData", set+"AdditionalData")
			return refusal{}
		}
	}

	if plainCollectionMatches(t, append([]ir.AttributeType{fb.Type}, fb.NestedCollectionElementTypes...)) {
		settle(shortType(t), get, set)
		return refusal{}
	}

	return because(CauseUnbridgeableType, shortType(t),
		"the SDK carries it as %s, which does not nest the way the document declares a %s", shortType(t), describeCollection(fb))
}

// settleBag records the model a bag-carried value is read through and, for
// a field that is written, the model and constructor construction builds.
// The write model is only resolved for a written field: construction is
// what needs it, and a read-only field has no constructor to name.
func (p *pruner) settleBag(fb *FieldBinding, named *types.Named, carrier types.Type) refusal {
	if fb.Access.Set != "" {
		model, constructor, why := p.writeModelFromNamed(named)
		if why.refused() {
			return why
		}
		fb.NestedWriteModel, fb.NestedConstructor = model, constructor
	}
	fb.NestedModel = qualifiedName(named)
	fb.Access.NestedNilable = nilableType(carrier)
	return refusal{}
}

// carriesAdditionalData reports a kiota model whose undeclared values live
// in an additionalData bag, which every kiota model does: the document, not
// the SDK, decides that the bag is the field's whole content.
func carriesAdditionalData(named *types.Named) bool {
	_, hasGet := methodOn(named, "GetAdditionalData")
	return hasGet
}

// plainCollectionMatches reports whether a plain Go type nests exactly the
// way the levels declare, outermost first down to a scalar leaf: a slice
// for each list, a string-keyed map for each map, and a basic type the leaf
// kind is compatible with at the bottom. A pointer at any level, a named
// leaf — an enumeration, a time, a UUID — or any other key type does not
// match: none of those is a value the framework's reflection can walk.
func plainCollectionMatches(t types.Type, levels []ir.AttributeType) bool {
	if len(levels) == 0 {
		return false
	}
	if _, isNamed := t.(*types.Named); isNamed && len(levels) == 1 {
		return false
	}
	if _, isPointer := t.(*types.Pointer); isPointer {
		return false
	}
	switch levels[0] {
	case ir.TypeList:
		slice, ok := t.Underlying().(*types.Slice)
		return ok && plainCollectionMatches(slice.Elem(), levels[1:])
	case ir.TypeMap:
		mapType, ok := t.Underlying().(*types.Map)
		if !ok {
			return false
		}
		key, isBasic := mapType.Key().Underlying().(*types.Basic)
		return isBasic && key.Kind() == types.String && plainCollectionMatches(mapType.Elem(), levels[1:])
	default:
		basic, isBasic := t.Underlying().(*types.Basic)
		return isBasic && len(levels) == 1 && kindCompatible(levels[0], basic)
	}
}

// isKiotaUntypedNode recognises kiota's untyped node interface, the type its
// generator falls back to for a collection whose element it does not model.
// Matched on the package path, as isKiotaDateOnly is, because another SDK's
// type of the same name would not walk through kiota's node kinds.
func isKiotaUntypedNode(named *types.Named) bool {
	goPackage := named.Obj().Pkg()
	if goPackage == nil || named.Obj().Name() != "UntypedNodeable" {
		return false
	}
	return strings.HasSuffix(goPackage.Path(), "kiota-abstractions-go/serialization")
}

// describeCollection spells a binding's collection and its levels the way
// the derivation's exclusions do: "map of list of string".
func describeCollection(fb *FieldBinding) string {
	spelled := string(fb.Type)
	for _, level := range fb.NestedCollectionElementTypes {
		spelled += " of " + string(level)
	}
	return spelled
}
