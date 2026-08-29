// Resolving a drafted call against the SDK that has to make it: every hop of
// the builder chain, the model its body is constructed from, and the types
// its path parameters really take.

package sdkbind

import (
	"fmt"
	"go/types"
	"reflect"
	"strconv"
	"strings"
)

// resolveCall walks a call's segments against the real client, repairing
// the two spellings the document cannot determine — the service field a
// flat SDK groups operations under, and the fluent body setter's name —
// and settling the payload types from the final method's signature. The
// returned reason is empty on success.
func (p *pruner) resolveCall(c *Call) refusal {
	current := p.client
	var final *types.Signature

	for i := range c.Segments {
		seg := &c.Segments[i]
		last := i == len(c.Segments)-1

		if !seg.Call {
			t, why := p.resolveFieldHop(current, seg, i, c)
			if why.refused() {
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
				return because(CauseUnresolvableCall, shortType(current), "%s has no method %s%s",
					shortType(current), seg.Name, didYouMean(seg.Name, methodNamesOf(current)))
			}
			sig = repaired
		}
		if got, want := len(seg.Args), sig.Params().Len(); got != want {
			return because(CauseUnresolvableCall, shortType(current), "%s takes %d argument(s) but the call passes %d: %s",
				seg.Name, want, got, shortSignature(sig))
		}
		settleParameterTypes(c, seg, sig)
		if last {
			final = sig
			break
		}
		if sig.Results().Len() != 1 {
			return because(CauseUnresolvableCall, shortType(current), "%s returns %d values; a builder hop must return exactly one",
				seg.Name, sig.Results().Len())
		}
		current = sig.Results().At(0).Type()
	}

	if final == nil {
		return because(CauseUnresolvableCall, "", "the call ends without a method")
	}
	p.settleCall(c, final)
	p.settleQueryParameters(c, final)
	c.rerender()
	return refusal{}
}

// settleQueryParameters replaces the call's trailing nil request
// configuration with one carrying the query parameters the operation
// requires, spelled through the SDK's own types: the final method's last
// parameter is a pointer to a generic request configuration whose type
// argument is the query-parameter struct, and each of its fields names the
// wire parameter it carries in a uriparametername tag. A parameter the
// struct does not carry, or a value of a shape no field takes, leaves the
// configuration nil — the call still compiles, and the API answers what it
// answers.
func (p *pruner) settleQueryParameters(c *Call, sig *types.Signature) {
	if len(c.QueryParameters) == 0 || len(c.Segments) == 0 || sig.Params().Len() == 0 {
		return
	}
	last := &c.Segments[len(c.Segments)-1]
	if len(last.Args) == 0 || last.Args[len(last.Args)-1] != "nil" {
		return
	}
	pointer, ok := sig.Params().At(sig.Params().Len() - 1).Type().(*types.Pointer)
	if !ok {
		return
	}
	configuration, ok := pointer.Elem().(*types.Named)
	if !ok || configuration.TypeArgs().Len() != 1 || configuration.Obj().Pkg() == nil {
		return
	}
	parameters, ok := configuration.TypeArgs().At(0).(*types.Named)
	if !ok || parameters.Obj().Pkg() == nil {
		return
	}
	fields, ok := parameters.Underlying().(*types.Struct)
	if !ok {
		return
	}

	var assignments []string
	for _, query := range c.QueryParameters {
		field, fieldType := fieldByURIParameterName(fields, query.Name)
		if field == "" {
			return
		}
		literal, ok := queryLiteral(query.Value, fieldType)
		if !ok {
			return
		}
		assignments = append(assignments, field+": convert.PointerTo("+literal+")")
	}

	generic := p.qualifierFor(configuration.Obj().Pkg())
	carrier := p.qualifierFor(parameters.Obj().Pkg())
	parametersType := carrier + "." + parameters.Obj().Name()
	last.Args[len(last.Args)-1] = fmt.Sprintf("&%s.%s[%s]{QueryParameters: &%s{%s}}",
		generic, configuration.Obj().Name(), parametersType, parametersType, strings.Join(assignments, ", "))
}

// qualifierFor answers the package qualifier a rendered expression names a
// package by, recording the package for the emitter's imports. The SDK's
// root package is imported under the fixed alias every generated file
// uses.
func (p *pruner) qualifierFor(goPackage *types.Package) string {
	if goPackage.Path() == p.info.ImportPath {
		return "sdk"
	}
	p.bindings.recordPackage(goPackage.Name(), goPackage.Path())
	return goPackage.Name()
}

// fieldByURIParameterName finds the struct field carrying one wire
// parameter, by its uriparametername tag, answering the field's name and
// its type.
func fieldByURIParameterName(st *types.Struct, wire string) (string, types.Type) {
	for i := range st.NumFields() {
		tag := reflect.StructTag(st.Tag(i))
		if tag.Get("uriparametername") == wire {
			return st.Field(i).Name(), st.Field(i).Type()
		}
	}
	return "", nil
}

// queryLiteral spells a query parameter's value as a Go literal of the
// type the struct field points at, and false where the value is not of
// that type.
func queryLiteral(value any, fieldType types.Type) (string, bool) {
	pointer, ok := fieldType.(*types.Pointer)
	if !ok {
		return "", false
	}
	basic, ok := pointer.Elem().Underlying().(*types.Basic)
	if !ok {
		return "", false
	}
	switch {
	case basic.Info()&types.IsBoolean != 0:
		b, ok := value.(bool)
		if !ok {
			return "", false
		}
		return strconv.FormatBool(b), true
	case basic.Info()&types.IsString != 0:
		s, ok := value.(string)
		if !ok {
			return "", false
		}
		return strconv.Quote(s), true
	case basic.Info()&types.IsInteger != 0:
		n, ok := integerValue(value)
		if !ok {
			return "", false
		}
		return fmt.Sprintf("%s(%d)", basic.Name(), n), true
	case basic.Info()&types.IsFloat != 0:
		f, ok := floatValue(value)
		if !ok {
			return "", false
		}
		return fmt.Sprintf("%s(%s)", basic.Name(), strconv.FormatFloat(f, 'g', -1, 64)), true
	}
	return "", false
}

// integerValue reads a decoded document number as an integer.
func integerValue(value any) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n == float64(int64(n)) {
			return int64(n), true
		}
	}
	return 0, false
}

// floatValue reads a decoded document number as a float.
func floatValue(value any) (float64, bool) {
	switch n := value.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// resolveFieldHop selects a struct field on the client, repairing a
// drafted service name when exactly one of the client's fields carries
// the following method — the generator names services after spec tags,
// which the intermediate representation does not see.
func (p *pruner) resolveFieldHop(current types.Type, seg *Segment, i int, c *Call) (types.Type, refusal) {
	st, err := structUnder(current)
	if err != nil {
		return nil, because(CauseUnresolvableCall, shortType(current), "%s is not a struct, so it has no field %s", shortType(current), seg.Name)
	}
	if f, ok := fieldByName(st, seg.Name); ok {
		return f.Type(), refusal{}
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
			return matches[0].Type(), refusal{}
		}
	}
	return nil, because(CauseUnresolvableCall, shortType(current), "%s has no field %s%s",
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
	p.reconcile("", seg.Name, name)
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
func (p *pruner) writeModelFor(requestType string) (model, constructor string, why refusal) {
	named, err := p.resolveType(requestType)
	if err != nil {
		return "", "", because(CauseNoRequestBodyType, requestType, "%s", unwrapDetail(err))
	}
	goPackage := named.Obj().Pkg()
	if goPackage == nil {
		return "", "", because(CauseNoRequestBodyType, requestType, "%s is not declared by the SDK", requestType)
	}
	qualifier := goPackage.Name()
	name := named.Obj().Name()

	if _, isInterface := named.Underlying().(*types.Interface); isInterface {
		base, ok := strings.CutSuffix(name, "able")
		if !ok {
			// An SDK runtime interface constructs through a companion of
			// its own name rather than through a concrete type: the
			// interface is the model.
			if p.l.functionExists(goPackage.Path(), "New"+name) {
				return qualifier + "." + name, qualifier + ".New" + name + "()", refusal{}
			}
			return "", "", because(CauseNoConstructor, qualifier+"."+name, "%s.%s is an interface with no constructible model behind it", qualifier, name)
		}
		if _, err := p.l.lookupType(goPackage.Path(), base); err != nil {
			return "", "", because(CauseNoConstructor, qualifier+"."+name, "%s.%s names no concrete %s to construct", qualifier, name, base)
		}
		if !p.l.functionExists(goPackage.Path(), "New"+base) {
			return "", "", because(CauseNoConstructor, qualifier+"."+base, "the SDK declares no constructor New%s for %s.%s", base, qualifier, base)
		}
		return qualifier + "." + base, qualifier + ".New" + base + "()", refusal{}
	}

	if _, err := structUnder(named); err != nil {
		return "", "", because(CauseNoRequestBodyType, qualifier+"."+name, "%s.%s is not a struct a body could be built as", qualifier, name)
	}
	if p.l.functionExists(goPackage.Path(), "New"+name+"WithDefaults") {
		return qualifier + "." + name, qualifier + ".New" + name + "WithDefaults()", refusal{}
	}
	return qualifier + "." + name, "&" + qualifier + "." + name + "{}", refusal{}
}

// settleParameterTypes records what the SDK method really takes for each path
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
func settleParameterTypes(c *Call, seg *Segment, sig *types.Signature) {
	if c == nil || sig == nil {
		return
	}
	for position, arg := range seg.Args {
		if position >= sig.Params().Len() {
			return
		}
		for i := range c.Parameters {
			if c.Parameters[i].Local != arg {
				continue
			}
			c.Parameters[i].GoType = shortType(sig.Params().At(position).Type())
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
	p.reconcile("", seg.Name, name)
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
			if goPackage := shaped.Obj().Pkg(); goPackage != nil {
				p.bindings.recordPackage(goPackage.Name(), goPackage.Path())
			}
			return
		default:
			return
		}
	}
}
