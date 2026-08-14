// Resolving a drafted call against the SDK that has to make it: every hop of
// the builder chain, the model its body is constructed from, and the types
// its path parameters really take.

package sdkbind

import (
	"fmt"
	"go/types"
	"strings"
)

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
			// An SDK runtime interface constructs through a companion of
			// its own name rather than through a concrete type: the
			// interface is the model.
			if p.l.functionExists(pkg.Path(), "New"+name) {
				return qualifier + "." + name, qualifier + ".New" + name + "()", ""
			}
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
// different rules, none of them derivable from the document, and each one
// enough to refuse a resource whose read call the SDK plainly has.
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

// recordTypePackage notes the package a type is declared in, so the emitter
// imports what a rendered expression names. A list element resolved straight
// off the collection accessor never passes through type-expression lookup,
// and a generator puts the element of an inline response in the operation's
// own package: the generated mapper named it and imported nothing.
func (p *pruner) recordTypePackage(t types.Type) {
	for {
		switch shaped := t.(type) {
		case *types.Pointer:
			t = shaped.Elem()
		case *types.Slice:
			t = shaped.Elem()
		case *types.Named:
			if pkg := shaped.Obj().Pkg(); pkg != nil {
				p.bindings.recordPackage(pkg.Name(), pkg.Path())
			}
			return
		default:
			return
		}
	}
}
