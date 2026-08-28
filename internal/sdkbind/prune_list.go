// Settling how a list call reaches its elements. A collection comes back as a
// slice, as an envelope with a getter, or as an envelope with a field, and
// which of those it is decides what the generated iteration reads.

package sdkbind

import (
	"fmt"
	"go/types"
)

// resolveListElement settles how a list call's result yields its
// elements. The result may be the slice itself, or a wrapper the elements
// hang off: a kiota collection response or "…able" envelope reached
// through a getter (GetValue, GetTags), or an openapi-generator envelope
// struct with a slice field (Items, Tags). The choice is data-driven — the
// observed envelope key (the IR's listResponseShape) names the wrapper's
// getter or field directly, so a wrapped list is bound rather than pruned.
// Absent a key, a lone slice-returning getter or a lone slice field is
// unambiguous. Genuine ambiguity — no envelope match and zero or several
// candidates — is refused with the SDK's shape in the reason, because
// guessing among several slices would be invention.
func (p *pruner) resolveListElement(list *Call, elementType, access *string, listWrapperKey string) (types.Type, string) {
	if why := p.resolveCall(list); why != "" {
		return nil, fmt.Sprintf("its list call cannot be made: %s", why)
	}
	if list.ResponseType == "" {
		return nil, "its list call yields no payload to read elements from"
	}
	sig, _, err := resolveChain(p.client, list.Segments)
	if err != nil || sig.Results().Len() == 0 {
		return nil, "its list call yields no payload to read elements from"
	}
	result := sig.Results().At(0).Type()

	// The slice itself.
	if slice, ok := result.Underlying().(*types.Slice); ok {
		*access = ""
		*elementType = shortType(slice.Elem())
		p.recordTypePackage(slice.Elem())
		return slice.Elem(), ""
	}

	getters := sliceGetters(result)
	fields := sliceFields(result)

	settle := func(c listElementCandidate) (types.Type, string) {
		*access = c.access
		*elementType = shortType(c.element)
		p.recordTypePackage(c.element)
		return c.element, ""
	}

	// The observed envelope key names the wrapper's getter or field
	// directly: kiota's Get<Key>, an openapi-generator field <Key>.
	if listWrapperKey != "" {
		want := exportedName(listWrapperKey)
		if c, ok := pickByName(getters, "Get"+want); ok {
			return settle(c)
		}
		if c, ok := pickByName(fields, want); ok {
			return settle(c)
		}
	}

	// No envelope key, or it named nothing the SDK carries: a lone
	// slice-returning getter (the kiota collection response) or a lone
	// slice field (the openapi-generator envelope) is unambiguous.
	if len(getters) == 1 {
		return settle(getters[0])
	}
	if len(fields) == 1 {
		return settle(fields[0])
	}

	return nil, fmt.Sprintf(
		"its list call returns %s, which carries no single way to reach its elements",
		shortType(result))
}

// listElementCandidate is one way a list result reaches its element slice:
// a getter method (rendered "GetTags()") or a struct field (rendered
// "Tags"), with the slice's element type.
type listElementCandidate struct {
	name    string
	access  string
	element types.Type
}

// sliceGetters returns every zero-argument, single-slice-returning getter
// on t — the shape a kiota wrapper reaches its elements through: GetValue
// on a collection response, GetTags on a Tagsable envelope. It works on an
// interface and on a concrete type alike, because both carry the method.
func sliceGetters(t types.Type) []listElementCandidate {
	var out []listElementCandidate
	ms := methodSetOf(t)
	for i := range ms.Len() {
		obj := ms.At(i).Obj()
		sig, ok := obj.Type().(*types.Signature)
		if !ok || sig.Params().Len() != 0 || sig.Results().Len() != 1 {
			continue
		}
		if slice, ok := sig.Results().At(0).Type().Underlying().(*types.Slice); ok {
			out = append(out, listElementCandidate{name: obj.Name(), access: obj.Name() + "()", element: slice.Elem()})
		}
	}
	return out
}

// sliceFields returns every exported slice field on t when t is a struct —
// the shape an openapi-generator envelope reaches its elements through:
// Items on a GroupList, Tags on a wrapped list.
func sliceFields(t types.Type) []listElementCandidate {
	st, err := structUnder(t)
	if err != nil {
		return nil
	}
	var out []listElementCandidate
	for i := range st.NumFields() {
		f := st.Field(i)
		if !f.Exported() {
			continue
		}
		if slice, ok := f.Type().Underlying().(*types.Slice); ok {
			out = append(out, listElementCandidate{name: f.Name(), access: f.Name(), element: slice.Elem()})
		}
	}
	return out
}

// pickByName returns the candidate whose name equals want, if present.
func pickByName(cands []listElementCandidate, want string) (listElementCandidate, bool) {
	for _, c := range cands {
		if c.name == want {
			return c, true
		}
	}
	return listElementCandidate{}, false
}
