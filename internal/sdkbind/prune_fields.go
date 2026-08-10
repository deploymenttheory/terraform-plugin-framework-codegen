package sdkbind

import (
	"fmt"
	"go/types"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
)

// fields resolves one attribute tree's accesses against the models that
// actually carry them, at every depth. A field the SDK cannot carry is
// removed where it is found, with the SDK's reason; an object emptied by
// its own removals is removed by the level above, which owns the
// attribute that held it.
func (p *pruner) fields(kind, key, prefix string, fbs []FieldBinding, read, write types.Type) []FieldBinding {
	kept := fbs[:0]
	for i := range fbs {
		fb := fbs[i]
		at := fb.Attr
		if prefix != "" {
			at = prefix + "." + fb.Attr
		}
		if why := p.resolveField(&fb, read, write, kind, key, at); why != "" {
			p.remove(kind, key, at, why)
			continue
		}
		kept = append(kept, fb)
	}
	return kept
}

// resolveField settles one field's accessors, carried type and
// conversions; the returned reason is empty when the field survives.
func (p *pruner) resolveField(fb *FieldBinding, read, write types.Type, kind, key, at string) string {
	var result, param types.Type

	if fb.Access.Get != "" && read != nil {
		sig, ok := methodOn(read, fb.Access.Get)
		if !ok {
			flipEscaped(&fb.Access)
			sig, ok = methodOn(read, fb.Access.Get)
		}
		if !ok {
			return fmt.Sprintf("%s carries no %s to read %q from%s",
				shortType(read), fb.Access.Get, fb.Wire, didYouMean(fb.Access.Get, methodNamesOf(read)))
		}
		if sig.Params().Len() != 0 || sig.Results().Len() != 1 {
			return fmt.Sprintf("%s.%s is not an accessor: %s", shortType(read), fb.Access.Get, shortSignature(sig))
		}
		result = sig.Results().At(0).Type()
	}

	if fb.Access.Set != "" && write != nil {
		sig, ok := methodOn(write, fb.Access.Set)
		if !ok && result == nil {
			// The read side may already have settled the Escaped spelling;
			// with no read side, the setter gets its own chance to flip.
			flipEscaped(&fb.Access)
			sig, ok = methodOn(write, fb.Access.Set)
		}
		if !ok {
			return fmt.Sprintf("%s carries no settable %s to write %q to%s",
				shortType(write), fb.Access.Set, fb.Wire, didYouMean(fb.Access.Set, methodNamesOf(write)))
		}
		if sig.Params().Len() != 1 {
			return fmt.Sprintf("%s.%s is not a setter: %s", shortType(write), fb.Access.Set, shortSignature(sig))
		}
		param = sig.Params().At(0).Type()
	}

	if result != nil && param != nil && !types.Identical(result, param) {
		return fmt.Sprintf("it is read as %s but written as %s; no one conversion satisfies both",
			shortType(result), shortType(param))
	}

	basis := result
	if basis == nil {
		basis = param
	}
	if basis == nil {
		// Neither direction is live — nothing to settle and nothing to
		// generate against; the field carries no risk either way.
		return ""
	}

	if len(fb.Nested) > 0 {
		return p.resolveNested(fb, basis, kind, key, at)
	}
	return p.settleScalar(fb, basis)
}

// resolveNested points a nested object at the SDK model its parent
// accessor actually carries, and recurses into its fields.
func (p *pruner) resolveNested(fb *FieldBinding, basis types.Type, kind, key, at string) string {
	named, why := nestedModelOf(basis)
	if why != "" {
		return why
	}
	fb.Access.SDKType = shortType(basis)
	fb.NestedModel = qualifiedName(named)

	readModel := types.Type(named)
	var writeModel types.Type
	fb.NestedWriteModel, fb.NestedConstructor = "", ""
	if fb.Access.Set != "" {
		model, constructor, wwhy := p.writeModelFromNamed(named)
		if wwhy != "" {
			return fmt.Sprintf("its nested object cannot be constructed: %s", wwhy)
		}
		fb.NestedWriteModel, fb.NestedConstructor = model, constructor
		w, err := p.l.typeFromExpr(p.info, model)
		if err != nil {
			return unwrapDetail(err)
		}
		writeModel = w
	}

	fb.Nested = p.fields(kind, key, at, fb.Nested, readModel, writeModel)
	if len(fb.Nested) == 0 {
		return "every field of its nested object went, leaving nothing to map"
	}
	return ""
}

// nestedModelOf reaches the named model under a nested accessor's type:
// the model itself, a pointer to it, or a slice of either.
func nestedModelOf(t types.Type) (*types.Named, string) {
	cur := t
	if slice, ok := cur.Underlying().(*types.Slice); ok {
		cur = slice.Elem()
	}
	if ptr, ok := cur.(*types.Pointer); ok {
		cur = ptr.Elem()
	}
	n, ok := cur.(*types.Named)
	if !ok || n.Obj().Pkg() == nil {
		return nil, fmt.Sprintf("the SDK carries it as %s, which names no model to map fields on", shortType(t))
	}
	switch n.Underlying().(type) {
	case *types.Interface, *types.Struct:
		return n, ""
	}
	return nil, fmt.Sprintf("the SDK carries it as %s, which names no model to map fields on", shortType(t))
}

// writeModelFromNamed is writeModelFor after type resolution: interface
// models resolve to the struct their constructor yields, structs
// construct themselves.
func (p *pruner) writeModelFromNamed(named *types.Named) (model, constructor, reason string) {
	pkg := named.Obj().Pkg()
	qualifier, name := pkg.Name(), named.Obj().Name()

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
	if p.l.functionExists(pkg.Path(), "New"+name+"WithDefaults") {
		return qualifier + "." + name, qualifier + ".New" + name + "WithDefaults()", ""
	}
	return qualifier + "." + name, "&" + qualifier + "." + name + "{}", ""
}

// settleScalar records the type the SDK actually carries a scalar field
// as, and the conversions that bridge it: pointer and value scalars at
// the SDK's own width, generated enumerations with their parse
// companions, and slices of both. Anything else names itself in the
// reason.
func (p *pruner) settleScalar(fb *FieldBinding, t types.Type) string {
	fa := &fb.Access
	settle := func(sdkType, get, set, parse string) {
		fa.SDKType = sdkType
		fa.ConvertGet, fa.ConvertSet, fa.ParseFunc = get, set, parse
		if fa.Get == "" {
			fa.ConvertGet = ""
		}
		if fa.Set == "" {
			fa.ConvertSet = ""
		}
	}
	cannot := func(shape string) string {
		return fmt.Sprintf("the SDK carries it as %s, which cannot be bridged to a %s attribute", shape, fb.Kind)
	}

	// A slice of scalars or enumerations.
	if slice, ok := t.Underlying().(*types.Slice); ok && fb.Kind == ir.TypeList {
		elem := slice.Elem()
		if basic, ok := elem.Underlying().(*types.Basic); ok {
			if named, isNamed := elem.(*types.Named); isNamed {
				if parse, isEnum := p.enumParse(named); isEnum && fb.ElemKind == ir.TypeString {
					settle("[]"+qualifiedName(named), "FromEnumSlice", "ToEnumSlice", parse)
					return ""
				}
				return cannot(shortType(t))
			}
			if !kindCompatible(fb.ElemKind, basic) {
				return cannot(shortType(t))
			}
			title := exportedName(basic.Name())
			settle("[]"+basic.Name(), "From"+title+"Slice", "To"+title+"Slice", "")
			return ""
		}
		return cannot(shortType(t))
	}

	ptr := false
	cur := t
	if pt, ok := cur.(*types.Pointer); ok {
		ptr = true
		cur = pt.Elem()
	}
	prefix := func(s string) string {
		if ptr {
			return "*" + s
		}
		return s
	}
	convert := func(base string) (string, string) {
		if ptr {
			return "FromPtr" + base, "ToPtr" + base
		}
		return "From" + base, "To" + base
	}

	if named, ok := cur.(*types.Named); ok {
		if parse, isEnum := p.enumParse(named); isEnum {
			if fb.Kind != ir.TypeString {
				return cannot(shortType(t))
			}
			get, set := convert("Enum")
			settle(prefix(qualifiedName(named)), get, set, parse)
			return ""
		}
		if named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "time" && named.Obj().Name() == "Time" {
			if fb.Kind != ir.TypeString {
				return cannot(shortType(t))
			}
			get, set := convert("Time")
			settle(prefix("time.Time"), get, set, "")
			return ""
		}
		return cannot(shortType(t))
	}

	if basic, ok := cur.(*types.Basic); ok {
		if !kindCompatible(fb.Kind, basic) {
			return cannot(shortType(t))
		}
		get, set := convert(exportedName(basic.Name()))
		settle(prefix(basic.Name()), get, set, "")
		return ""
	}

	return cannot(shortType(t))
}

// enumParse recognises a generated enumeration by the companion the
// generator mints beside it — an int-backed type with Parse<Name>
// (kiota), or a string-backed one with New<Name>FromValue
// (openapi-generator) — and returns the companion's finished name.
func (p *pruner) enumParse(named *types.Named) (string, bool) {
	pkg := named.Obj().Pkg()
	if pkg == nil {
		return "", false
	}
	basic, ok := named.Underlying().(*types.Basic)
	if !ok {
		return "", false
	}
	name := named.Obj().Name()
	switch {
	case basic.Info()&types.IsInteger != 0 && p.l.functionExists(pkg.Path(), "Parse"+name):
		return pkg.Name() + ".Parse" + name, true
	case basic.Info()&types.IsString != 0 && p.l.functionExists(pkg.Path(), "New"+name+"FromValue"):
		return pkg.Name() + ".New" + name + "FromValue", true
	}
	return "", false
}

// kindCompatible reports whether a basic SDK type can carry an attribute
// kind: integers of any width carry int64 attributes, floats of either
// width carry float64 ones.
func kindCompatible(kind ir.TypeKind, basic *types.Basic) bool {
	switch kind {
	case ir.TypeString:
		return basic.Info()&types.IsString != 0
	case ir.TypeBool:
		return basic.Info()&types.IsBoolean != 0
	case ir.TypeInt64:
		return basic.Info()&types.IsInteger != 0
	case ir.TypeFloat64:
		return basic.Info()&types.IsFloat != 0
	}
	return false
}

// flipEscaped toggles the Escaped suffix on an accessor pair. kiota
// escapes a property whose name its Go generator reserves, and the
// reserved-word list is only ever as current as the release someone last
// read — so the answer is read off the SDK: whichever spelling resolves
// is the SDK's.
func flipEscaped(fa *FieldAccess) {
	toggle := func(accessor, verb string) string {
		if accessor == "" {
			return ""
		}
		base := strings.TrimPrefix(accessor, verb)
		if cut, ok := strings.CutSuffix(base, "Escaped"); ok {
			return verb + cut
		}
		return verb + base + "Escaped"
	}
	fa.Get = toggle(fa.Get, "Get")
	fa.Set = toggle(fa.Set, "Set")
}

// qualifiedName renders a named type the way generated code spells it:
// package name dot type name.
func qualifiedName(named *types.Named) string {
	return named.Obj().Pkg().Name() + "." + named.Obj().Name()
}
