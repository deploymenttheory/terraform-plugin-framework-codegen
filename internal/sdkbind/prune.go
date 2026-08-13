package sdkbind

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
)

// Prune resolves every drafted binding against the generated SDK under
// sdkDir, and deletes what the SDK cannot carry.
//
// A draft is derived from the document alone, and a document routinely
// promises what the generated SDK does not deliver: a model deduplicated
// under another name, an integer carried narrower than declared, an
// enumeration minted from an inline value set the document never named.
// Where the SDK's truth admits exactly one answer, the draft is repaired
// in place — the response type is read off the real method signature, a
// mangled accessor gains its Escaped suffix, an enumeration gains its
// Parse companion. Everything else is deleted with the SDK's own reason:
// an attribute-level problem removes the attribute, and a problem in the
// call chain removes the entity, because nothing smaller leaves a
// buildable binding. Nothing is invented and nothing is widened — every
// surviving binding names symbols the SDK demonstrably has.
//
// The removals are returned sorted and recorded on the binding set, so a
// generation run reports what the provider will not carry.
func Prune(b *Bindings, sdkDir string) ([]Removal, error) {
	l, err := loadSDK(sdkDir)
	if err != nil {
		return nil, err
	}
	client, err := l.lookupType(b.SDK.ImportPath, b.SDK.ClientTypeName)
	if err != nil {
		return nil, fmt.Errorf("%w: sdk.client_type_name: %s", ErrBindings, unwrapDetail(err))
	}

	p := &pruner{l: l, info: b.SDK, client: client, bindings: b}

	for _, key := range sortedKeys(b.Resources) {
		if !p.resource(b.Resources[key]) {
			delete(b.Resources, key)
		}
	}
	for _, key := range sortedKeys(b.Datasources) {
		if !p.datasource(b.Datasources[key]) {
			delete(b.Datasources, key)
		}
	}
	for _, key := range sortedKeys(b.ListResources) {
		if !p.listResource(b.ListResources[key]) {
			delete(b.ListResources, key)
		}
	}
	for _, key := range sortedKeys(b.Actions) {
		if !p.action(b.Actions[key]) {
			delete(b.Actions, key)
		}
	}

	sort.Slice(p.removed, func(i, j int) bool {
		a, z := p.removed[i], p.removed[j]
		if a.Kind != z.Kind {
			return a.Kind < z.Kind
		}
		if a.Key != z.Key {
			return a.Key < z.Key
		}
		return a.Attribute < z.Attribute
	})
	b.Removed = p.removed
	return p.removed, nil
}

type pruner struct {
	l        *loader
	info     SDKInfo
	client   types.Type
	bindings *Bindings
	removed  []Removal
}

// resolveType resolves a type expression and records the package it was
// found under, so the emitter can import whatever the expression names.
func (p *pruner) resolveType(expr string) (*types.Named, error) {
	named, importPath, err := p.l.typeAndPackageFromExpr(p.info, expr)
	if err != nil {
		return nil, err
	}
	if pkg := named.Obj().Pkg(); pkg != nil {
		p.bindings.recordPackage(pkg.Name(), importPath)
	}
	return named, nil
}

func (p *pruner) remove(kind, key, attribute, reason string) {
	p.removed = append(p.removed, Removal{Kind: kind, Key: key, Attribute: attribute, Reason: reason})
}

// resource resolves one resource's calls and fields; false removes it.
func (p *pruner) resource(rb *ResourceBinding) bool {
	const kind = "resource"
	calls := []struct {
		name string
		call *Call
	}{
		{"create", rb.Create}, {"read", rb.Read}, {"update", rb.Update}, {"delete", rb.Delete},
	}
	for _, c := range calls {
		if c.call == nil {
			continue
		}
		if why := p.resolveCall(c.call); why != "" {
			p.remove(kind, rb.Key, "", fmt.Sprintf("its %s call cannot be made: %s", c.name, why))
			return false
		}
	}

	// The read payload is what state maps from; create's is the fallback
	// for an API that only answers on create.
	rb.ReadModel = ""
	if rb.Read != nil {
		rb.ReadModel = rb.Read.ResponseType
	}
	if rb.ReadModel == "" && rb.Create != nil {
		rb.ReadModel = rb.Create.ResponseType
	}
	if rb.ReadModel == "" {
		p.remove(kind, rb.Key, "", "no lifecycle call yields a payload to map state from")
		return false
	}

	rb.WriteModel, rb.WriteConstructor = "", ""
	if rb.Create != nil && rb.Create.RequestType != "" {
		write, constructor, why := p.writeModelFor(rb.Create.RequestType)
		if why != "" {
			p.remove(kind, rb.Key, "", fmt.Sprintf("its request body cannot be constructed: %s", why))
			return false
		}
		rb.WriteModel, rb.WriteConstructor = write, constructor
	}

	read, err := p.resolveType(rb.ReadModel)
	if err != nil {
		p.remove(kind, rb.Key, "", unwrapDetail(err))
		return false
	}
	var write types.Type
	if rb.WriteModel != "" {
		w, err := p.resolveType(rb.WriteModel)
		if err != nil {
			p.remove(kind, rb.Key, "", unwrapDetail(err))
			return false
		}
		write = w
	}

	rb.Fields = p.fields(kind, rb.Key, "", rb.Fields, read, write)

	if why := unbuildableReason(rb.Fields, rb.Create != nil); why != "" {
		p.remove(kind, rb.Key, "", why)
		return false
	}
	return true
}

// datasource resolves a lookup's read or a companion's list; false
// removes it.
func (p *pruner) datasource(db *DatasourceBinding) bool {
	const kind = "datasource"
	if db.Read != nil {
		if why := p.resolveCall(db.Read); why != "" {
			p.remove(kind, db.Key, "", fmt.Sprintf("its read call cannot be made: %s", why))
			return false
		}
		db.ReadModel = db.Read.ResponseType
	}

	var element types.Type
	if db.List != nil {
		elem, why := p.resolveListElement(db.List, &db.ElementType, &db.CollectionAccess, db.EnvelopeKey)
		if why != "" {
			p.remove(kind, db.Key, "", why)
			return false
		}
		element = elem
	}

	model := element
	if model == nil {
		named, err := p.resolveType(db.ReadModel)
		if err != nil {
			p.remove(kind, db.Key, "", unwrapDetail(err))
			return false
		}
		model = named
	}

	db.Fields = p.fields(kind, db.Key, "", db.Fields, model, nil)
	if why := unbuildableReason(db.Fields, false); why != "" {
		p.remove(kind, db.Key, "", why)
		return false
	}
	return true
}

func (p *pruner) listResource(lb *ListResourceBinding) bool {
	const kind = "list_resource"
	if lb.List == nil {
		p.remove(kind, lb.Key, "", "it has no list call")
		return false
	}
	element, why := p.resolveListElement(lb.List, &lb.ElementType, &lb.CollectionAccess, lb.EnvelopeKey)
	if why != "" {
		p.remove(kind, lb.Key, "", why)
		return false
	}
	lb.Fields = p.fields(kind, lb.Key, "", lb.Fields, element, nil)
	if why := unbuildableReason(lb.Fields, false); why != "" {
		p.remove(kind, lb.Key, "", why)
		return false
	}
	return true
}

func (p *pruner) action(ab *ActionBinding) bool {
	const kind = "action"
	if ab.Invoke == nil {
		p.remove(kind, ab.Key, "", "it has no invoke call")
		return false
	}
	if why := p.resolveCall(ab.Invoke); why != "" {
		p.remove(kind, ab.Key, "", fmt.Sprintf("its invoke call cannot be made: %s", why))
		return false
	}

	ab.WriteModel, ab.WriteConstructor = "", ""
	var write types.Type
	if ab.Invoke.RequestType != "" {
		model, constructor, why := p.writeModelFor(ab.Invoke.RequestType)
		if why != "" {
			p.remove(kind, ab.Key, "", fmt.Sprintf("its request body cannot be constructed: %s", why))
			return false
		}
		ab.WriteModel, ab.WriteConstructor = model, constructor
		w, err := p.resolveType(model)
		if err != nil {
			p.remove(kind, ab.Key, "", unwrapDetail(err))
			return false
		}
		write = w

		ab.Fields = p.fields(kind, ab.Key, "", ab.Fields, nil, write)
		settable := false
		for _, fb := range ab.Fields {
			if fb.Access.Set != "" {
				settable = true
				break
			}
		}
		if !settable {
			p.remove(kind, ab.Key, "",
				"no argument survives that can be sent in its request body, so there is nothing to invoke it with")
			return false
		}
	}
	return true
}

// resolveCall walks a call's segments against the real client, repairing
// the two spellings the document cannot determine — the service field a
// flat SDK groups operations under, and the fluent body setter's name —
// and settling the payload types from the final method's signature. The
// returned reason is empty on success.
func (p *pruner) resolveCall(c *Call) string {
	current := p.client
	var final *types.Signature

	for i := range c.Segments {
		seg := &c.Segments[i]
		last := i == len(c.Segments)-1

		if !seg.Call {
			t, why := p.resolveFieldHop(current, seg, i, c)
			if why != "" {
				return why
			}
			current = t
			continue
		}

		sig, ok := methodOn(current, seg.Name)
		if !ok {
			repaired, found := p.repairIndexer(current, seg)
			if !found {
				repaired, found = p.repairBodySetter(current, seg)
			}
			if !found {
				return fmt.Sprintf("%s has no method %s%s",
					shortType(current), seg.Name, didYouMean(seg.Name, methodNamesOf(current)))
			}
			sig = repaired
		}
		if got, want := len(seg.Args), sig.Params().Len(); got != want {
			return fmt.Sprintf("%s takes %d argument(s) but the call passes %d: %s",
				seg.Name, want, got, shortSignature(sig))
		}
		settleParamTypes(c, seg, sig)
		if last {
			final = sig
			break
		}
		if sig.Results().Len() != 1 {
			return fmt.Sprintf("%s returns %d values; a builder hop must return exactly one",
				seg.Name, sig.Results().Len())
		}
		current = sig.Results().At(0).Type()
	}

	if final == nil {
		return "the call ends without a method"
	}
	p.settleCall(c, final)
	c.rerender()
	return ""
}

// resolveFieldHop selects a struct field on the client, repairing a
// drafted service name when exactly one of the client's fields carries
// the following method — the generator names services after spec tags,
// which the intermediate representation does not see.
func (p *pruner) resolveFieldHop(current types.Type, seg *Segment, i int, c *Call) (types.Type, string) {
	st, err := structUnder(current)
	if err != nil {
		return nil, fmt.Sprintf("%s is not a struct, so it has no field %s", shortType(current), seg.Name)
	}
	if f, ok := fieldByName(st, seg.Name); ok {
		return f.Type(), ""
	}

	if i+1 < len(c.Segments) && c.Segments[i+1].Call {
		wanted := c.Segments[i+1].Name
		var matches []*types.Var
		for fi := range st.NumFields() {
			f := st.Field(fi)
			if !f.Exported() {
				continue
			}
			if _, has := methodOn(f.Type(), wanted); has {
				matches = append(matches, f)
			}
		}
		if len(matches) == 1 {
			seg.Name = matches[0].Name()
			return matches[0].Type(), ""
		}
	}
	return nil, fmt.Sprintf("%s has no field %s%s",
		shortType(current), seg.Name, didYouMean(seg.Name, fieldNames(st)))
}

// repairBodySetter renames a drafted body hop to the request builder's
// real setter, when the builder carries exactly one candidate: a
// single-parameter method, not Execute, whose parameter is a model shape
// a body could travel as. The generator names the setter after the body
// parameter's name, which only the SDK knows.
func (p *pruner) repairBodySetter(current types.Type, seg *Segment) (*types.Signature, bool) {
	if len(seg.Args) != 1 || (seg.Args[0] != "body" && seg.Args[0] != "*body") {
		return nil, false
	}
	var name string
	var found *types.Signature
	for _, m := range methodNamesOf(current) {
		sig, _ := methodOn(current, m)
		if m == "Execute" || sig == nil || sig.Params().Len() != 1 || sig.Results().Len() != 1 {
			continue
		}
		if _, err := structUnder(sig.Params().At(0).Type()); err != nil {
			continue
		}
		if found != nil {
			return nil, false // ambiguous: repairing would be a guess
		}
		name, found = m, sig
	}
	if found == nil {
		return nil, false
	}
	seg.Name = name
	return found, true
}

// settleCall records the call's real result shape: every result type in
// order, the success payload, and the type the body travels as.
func (p *pruner) settleCall(c *Call, sig *types.Signature) {
	results := make([]string, 0, sig.Results().Len())
	for i := range sig.Results().Len() {
		results = append(results, shortType(sig.Results().At(i).Type()))
	}
	c.Results = results

	c.ResponseType = ""
	if len(results) > 0 && results[0] != "error" && results[0] != "*http.Response" {
		c.ResponseType = results[0]
	}

	// The body hop is the one whose argument is the constructed body; its
	// parameter type is what the request travels as.
	c.RequestType = ""
	current := p.client
	for _, seg := range c.Segments {
		if !seg.Call {
			st, err := structUnder(current)
			if err != nil {
				return
			}
			f, ok := fieldByName(st, seg.Name)
			if !ok {
				return
			}
			current = f.Type()
			continue
		}
		s, ok := methodOn(current, seg.Name)
		if !ok {
			return
		}
		for ai, arg := range seg.Args {
			if (arg == "body" || arg == "*body") && ai < s.Params().Len() {
				c.RequestType = shortType(s.Params().At(ai).Type())
			}
		}
		if s.Results().Len() != 1 {
			return
		}
		current = s.Results().At(0).Type()
	}
}

// writeModelFor derives the concrete, constructible write model from the
// type a body parameter takes: a kiota "…able" interface resolves to the
// struct its constructor yields, a flat struct constructs itself. The
// returned reason names what stops a body being built.
func (p *pruner) writeModelFor(requestType string) (model, constructor, reason string) {
	named, err := p.resolveType(requestType)
	if err != nil {
		return "", "", unwrapDetail(err)
	}
	pkg := named.Obj().Pkg()
	if pkg == nil {
		return "", "", fmt.Sprintf("%s is not declared by the SDK", requestType)
	}
	qualifier := pkg.Name()
	name := named.Obj().Name()

	if _, isInterface := named.Underlying().(*types.Interface); isInterface {
		base, ok := strings.CutSuffix(name, "able")
		if !ok {
			return "", "", fmt.Sprintf("%s.%s is an interface with no constructible model behind it", qualifier, name)
		}
		if _, err := p.l.lookupType(pkg.Path(), base); err != nil {
			return "", "", fmt.Sprintf("%s.%s names no concrete %s to construct", qualifier, name, base)
		}
		if !p.l.functionExists(pkg.Path(), "New"+base) {
			return "", "", fmt.Sprintf("the SDK declares no constructor New%s for %s.%s", base, qualifier, base)
		}
		return qualifier + "." + base, qualifier + ".New" + base + "()", ""
	}

	if _, err := structUnder(named); err != nil {
		return "", "", fmt.Sprintf("%s.%s is not a struct a body could be built as", qualifier, name)
	}
	if p.l.functionExists(pkg.Path(), "New"+name+"WithDefaults") {
		return qualifier + "." + name, qualifier + ".New" + name + "WithDefaults()", ""
	}
	return qualifier + "." + name, "&" + qualifier + "." + name + "{}", ""
}

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
func (p *pruner) resolveListElement(list *Call, elementType, access *string, envelopeKey string) (types.Type, string) {
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
		return slice.Elem(), ""
	}

	getters := sliceGetters(result)
	fields := sliceFields(result)

	settle := func(c listElementCandidate) (types.Type, string) {
		*access = c.access
		*elementType = shortType(c.elem)
		return c.elem, ""
	}

	// The observed envelope key names the wrapper's getter or field
	// directly: kiota's Get<Key>, an openapi-generator field <Key>.
	if envelopeKey != "" {
		want := exportedName(envelopeKey)
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
	name   string
	access string
	elem   types.Type
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
			out = append(out, listElementCandidate{name: obj.Name(), access: obj.Name() + "()", elem: slice.Elem()})
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
			out = append(out, listElementCandidate{name: f.Name(), access: f.Name(), elem: slice.Elem()})
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

// unbuildableReason names the missing direction after pruning, or is
// empty when the binding still has something to generate: an entity with
// nothing readable maps nothing into state, and a resource that sends a
// body with nothing settable gives an operator nothing to configure.
func unbuildableReason(fbs []FieldBinding, needsWrite bool) string {
	var readable, settable bool
	for _, fb := range fbs {
		if fb.Access.Get != "" {
			readable = true
		}
		if fb.Access.Set != "" {
			settable = true
		}
	}
	switch {
	case !readable:
		return "no attribute survives that can be read back from a response, so there is nothing to map into state"
	case needsWrite && !settable:
		return "no attribute survives that can be sent in a request body, so there is nothing an operator could configure"
	}
	return ""
}

// settleParamTypes records what the SDK method really takes for each path
// parameter the segment passes, replacing the type drafted from the document.
//
// The draft can only spell what the intermediate representation knows, and
// the representation has one integer type. A generator does not: a path
// parameter the document calls an integer routinely arrives as int32, and one
// the document calls an integer arrives as a string wherever the generator
// read the operation differently. Passing the drafted type produced code that
// did not compile — "cannot use id (variable of type int64) as int32 value" —
// on every such operation.
//
// Resolution is by argument position against the real signature, which is the
// only place the truth lives. A parameter the segment does not pass is left
// exactly as drafted.
func settleParamTypes(c *Call, seg *Segment, sig *types.Signature) {
	if c == nil || sig == nil {
		return
	}
	for position, arg := range seg.Args {
		if position >= sig.Params().Len() {
			return
		}
		for i := range c.Params {
			if c.Params[i].Local != arg {
				continue
			}
			c.Params[i].GoType = shortType(sig.Params().At(position).Type())
		}
	}
}

// indexerPrefix is what a generated collection builder names its
// by-identifier hop with, in both supported backends.
const indexerPrefix = "By"

// repairIndexer resolves a by-identifier hop to the collection builder's
// only such method.
//
// The draft spells the hop from the document's path parameter, and no
// generator promises to spell it the same way. kiota keeps the parameter's
// own punctuation, so a path parameter named gist_id becomes ByGist_id where
// the draft asked for ByGistId; it appends a suffix where the parameter is
// bare, so owner becomes ByOwnerId; and where it has already used a name it
// renames wholesale, so team_slug becomes ByEnterpriseTeamId. Three
// different rules, none of them derivable from the document — and between
// them they took thirty-eight of one document's forty resources, every one
// with a read call the SDK plainly had.
//
// A collection builder indexes by exactly one thing, so where it declares
// exactly one such method there is nothing to guess: the hop is that method
// or the entity has no read at all. Two would be a guess, and the draft
// stands so the entity is removed with the SDK's own reason.
func (p *pruner) repairIndexer(current types.Type, seg *Segment) (*types.Signature, bool) {
	if !strings.HasPrefix(seg.Name, indexerPrefix) || len(seg.Args) != 1 {
		return nil, false
	}

	var name string
	var found *types.Signature
	for _, candidate := range methodNamesOf(current) {
		if !strings.HasPrefix(candidate, indexerPrefix) {
			continue
		}
		sig, ok := methodOn(current, candidate)
		if !ok || sig.Params().Len() != 1 || sig.Results().Len() != 1 {
			continue
		}
		if found != nil {
			return nil, false // ambiguous: repairing would be a guess
		}
		name, found = candidate, sig
	}
	if found == nil {
		return nil, false
	}
	seg.Name = name
	return found, true
}
