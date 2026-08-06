package sdkbind

import (
	"fmt"
	"go/types"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// This file is the method-shaped half of the type checker: fluent
// request-builder chains and Get/Set accessor pairs, beside the struct-field
// machinery in sdkbind.go. Nothing here knows which generator produced the
// SDK -- it resolves whatever methods the loaded types actually carry.

// methodOn resolves a method on t, following a pointer receiver for named
// types the way LookupMethod does, and working directly on interfaces --
// generated builders return interface types, and their method sets carry no
// pointer receivers.
func methodOn(t types.Type, name string) (*types.Signature, bool) {
	ms := methodSetOf(t)
	for i := range ms.Len() {
		if sel := ms.At(i); sel.Obj().Name() == name {
			if sig, ok := sel.Obj().Type().(*types.Signature); ok {
				return sig, true
			}
		}
	}
	return nil, false
}

func methodSetOf(t types.Type) *types.MethodSet {
	switch t.Underlying().(type) {
	case *types.Interface, *types.Pointer:
		return types.NewMethodSet(t)
	default:
		return types.NewMethodSet(types.NewPointer(t))
	}
}

func methodNamesOf(t types.Type) []string {
	ms := methodSetOf(t)
	names := make([]string, 0, ms.Len())
	for i := range ms.Len() {
		names = append(names, ms.At(i).Obj().Name())
	}
	return names
}

// MethodChain walks a fluent chain from a starting type and returns the final
// segment's method, for the same arity and result checks a flat call gets.
//
// Every hop before the last must exist with the declared argument count and
// return exactly one value -- a builder hop yielding two has no single type
// for the next hop to resolve against, so the walk refuses it by name rather
// than guessing which result carries the builder.
func (l *Loader) MethodChain(start types.Type, chain []blueprint.ChainSegment) (Method, error) {
	current := start

	for i, seg := range chain {
		sig, ok := methodOn(current, seg.Method)
		if !ok {
			return Method{}, fmt.Errorf("%w: %s has no method %s%s",
				ErrBindings, shortType(current), seg.Method,
				didYouMean(seg.Method, methodNamesOf(current)))
		}

		if got, want := len(seg.Args), sig.Params().Len(); got != want {
			return Method{}, fmt.Errorf(
				"%w: chain segment %s declares %d argument(s) but the method takes %d: %s",
				ErrBindings, seg.Method, got, want, methodFrom(seg.Method, sig).Signature())
		}

		// An identifier hop passes the state or configuration field as a string,
		// because that is what generated code renders there by default. An
		// indexer typed on anything else -- kiota mints uuid.UUID indexers from
		// a uuid path parameter -- needs the argument to declare its own
		// expression (convert.ParseUUID(...) is the proven shape), and one that
		// does is taken at its word; without one, waving the hop through here
		// is a compile error in generated code.
		for j, arg := range seg.Args {
			if arg.Kind != blueprint.ArgStateField && arg.Kind != blueprint.ArgConfigField {
				continue
			}
			if arg.Expr != "" {
				continue
			}
			if p := shortType(sig.Params().At(j).Type()); p != "string" {
				return Method{}, fmt.Errorf(
					"%w: chain segment %s takes %s, and the generated chain passes the %s field as a "+
						"string; declare the argument's expr to convert it",
					ErrBindings, seg.Method, p, arg.Field)
			}
		}

		if i == len(chain)-1 {
			return methodFrom(seg.Method, sig), nil
		}

		if sig.Results().Len() != 1 {
			return Method{}, fmt.Errorf(
				"%w: chain segment %s returns %d values; a builder hop must return exactly one",
				ErrBindings, seg.Method, sig.Results().Len())
		}
		current = sig.Results().At(0).Type()
	}

	return Method{}, fmt.Errorf("%w: the chain is empty", ErrBindings)
}

// LookupAccessorPair verifies a model exposes the Get/Set pair for a field and
// returns the getter's result type -- the method-access analogue of
// LookupField, and the place a mangling miss (GetError vs GetErrorEscaped)
// surfaces with the real spelling beside the guess.
//
// wantSet is false for a response model: builders return read-side interfaces
// whose method sets legitimately carry getters only.
func (l *Loader) LookupAccessorPair(importPath, typeName, field string, wantSet bool) (types.Type, error) {
	named, err := l.LookupType(importPath, typeName)
	if err != nil {
		return nil, err
	}

	getter := "Get" + field
	sig, ok := methodOn(named, getter)
	if !ok {
		return nil, fmt.Errorf("%w: %s has no method %s%s",
			ErrBindings, shortType(named), getter, didYouMean(getter, methodNamesOf(named)))
	}
	if sig.Params().Len() != 0 || sig.Results().Len() != 1 {
		return nil, fmt.Errorf("%w: %s.%s is not an accessor: %s",
			ErrBindings, shortType(named), getter, methodFrom(getter, sig).Signature())
	}
	result := sig.Results().At(0).Type()

	if wantSet {
		setter := "Set" + field
		setSig, ok := methodOn(named, setter)
		if !ok {
			return nil, fmt.Errorf("%w: %s has no method %s%s",
				ErrBindings, shortType(named), setter, didYouMean(setter, methodNamesOf(named)))
		}
		if setSig.Params().Len() != 1 {
			return nil, fmt.Errorf("%w: %s.%s is not a setter: %s",
				ErrBindings, shortType(named), setter, methodFrom(setter, setSig).Signature())
		}
	}

	return result, nil
}

// LookupFieldAccess dispatches on the binding's access style: a struct-field
// SDK is checked through LookupField, a method-access one through the Get/Set
// pair. This is the checker-side twin of the emitter's access seam.
func (l *Loader) LookupFieldAccess(
	style blueprint.AccessStyle,
	importPath, typeName, field string,
	wantSet bool,
) (types.Type, error) {
	if style == blueprint.AccessMethod {
		return l.LookupAccessorPair(importPath, typeName, field, wantSet)
	}
	return l.LookupField(importPath, typeName, field)
}
