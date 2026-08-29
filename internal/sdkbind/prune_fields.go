package sdkbind

import (
	"fmt"
	"go/types"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
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
	var result, parameter types.Type

	if fb.Access.Get != "" && read != nil {
		draftedGet := fb.Access.Get
		sig, ok := methodOn(read, fb.Access.Get)
		if !ok {
			flipEscaped(&fb.Access)
			sig, ok = methodOn(read, fb.Access.Get)
			if ok {
				p.reconcile(at, draftedGet, fb.Access.Get)
			}
		}
		if !ok && fb.Access.Set != "" && write != nil {
			// The request carries the field and no response answers it: a
			// property the write model declares and the read model does not.
			// The practitioner can still set it, so it stays as one whose
			// state keeps the planned value, settled against the write side
			// in the spelling the draft gave it — the flip above changed the
			// setter's spelling along with the getter's.
			draftedSet := fb.Access.Set
			flipEscaped(&fb.Access)
			if _, settable := methodOn(write, fb.Access.Set); settable {
				p.reconcile(at, draftedSet, fb.Access.Set)
				keepFromPlan(fb)
				return p.resolveField(fb, nil, write, kind, key, at)
			}
			flipEscaped(&fb.Access)
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
		draftedSet := fb.Access.Set
		sig, ok := methodOn(write, fb.Access.Set)
		if !ok && result == nil {
			// The read side may already have settled the Escaped spelling;
			// with no read side, the setter gets its own chance to flip.
			flipEscaped(&fb.Access)
			sig, ok = methodOn(write, fb.Access.Set)
			if ok {
				p.reconcile(at, draftedSet, fb.Access.Set)
			}
		}
		if !ok {
			return fmt.Sprintf("%s carries no settable %s to write %q to%s",
				shortType(write), fb.Access.Set, fb.Wire, didYouMean(fb.Access.Set, methodNamesOf(write)))
		}
		if sig.Params().Len() != 1 {
			return fmt.Sprintf("%s.%s is not a setter: %s", shortType(write), fb.Access.Set, shortSignature(sig))
		}
		parameter = sig.Params().At(0).Type()
	}

	if result != nil && parameter != nil && !types.Identical(result, parameter) {
		if why := p.settleEachDirection(fb, result, parameter, kind, key, at); why != "" {
			// Read in one shape and written in another that no conversion
			// bridges — identifiers written, objects read. The write side is
			// what a practitioner configures, so where it settles on its own
			// it stays and the read side goes: state keeps the planned
			// value. Where even the write side cannot carry the attribute,
			// the mismatch is the reason.
			writeOnly := copyFieldBindings([]FieldBinding{*fb})[0]
			keepFromPlan(&writeOnly)
			if p.resolveField(&writeOnly, nil, write, kind, key, at) == "" {
				*fb = writeOnly
				return ""
			}
			return why
		}
		return ""
	}

	basis := result
	if basis == nil {
		basis = parameter
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

// settleEachDirection settles a field the SDK reads and writes as different
// types, by bridging each direction against its own type rather than
// requiring one type to satisfy both.
//
// A generator emits two models for one object when the document declares a
// request schema and a response schema separately, and the same field then
// differs only in pointer-ness or integer width. Requiring identity refused
// the field; bridging each side keeps it, and a side the conversion catalog
// cannot carry still refuses, naming both types.
//
// Nested objects are excluded: their fields would have to pair recursively
// against two model trees, which is a larger question than one accessor.
func (p *pruner) settleEachDirection(fb *FieldBinding, result, parameter types.Type, kind, key, at string) string {
	mismatch := fmt.Sprintf("it is read as %s but written as %s; no conversion carries both",
		shortType(result), shortType(parameter))

	if len(fb.Nested) > 0 {
		return p.resolveNestedPair(fb, result, parameter, kind, key, at, mismatch)
	}

	readSide := *fb
	readSide.Access.Set = ""
	if why := p.settleScalar(&readSide, result); why != "" {
		return mismatch
	}

	writeSide := *fb
	writeSide.Access.Get = ""
	if why := p.settleScalar(&writeSide, parameter); why != "" {
		return mismatch
	}

	fb.Access.SDKType = readSide.Access.SDKType
	fb.Access.ConvertGet = readSide.Access.ConvertGet
	fb.Access.ConvertSet = writeSide.Access.ConvertSet
	// A map carried through an additionalData bag is written by building a
	// model, so the write side's model and constructor travel with its
	// conversion. Without them construction emits an assignment with
	// nothing on the right of it.
	fb.NestedModel = readSide.NestedModel
	fb.NestedWriteModel = writeSide.NestedWriteModel
	fb.NestedConstructor = writeSide.NestedConstructor
	// The parse companion belongs to the direction that parses. Only the
	// write does; the read spells an enum through its String method.
	fb.Access.ParseFunc = writeSide.Access.ParseFunc
	if fb.Access.ParseFunc == "" {
		fb.Access.ParseFunc = readSide.Access.ParseFunc
	}
	return ""
}

// resolveNestedPair points a nested object at two models — the one its
// getter answers and the one its setter takes — and pairs their fields.
//
// resolveNested derives the write model from the read one, which is right
// while a generator emits a single model per object. It emits two when the
// document declares a request schema and a response schema separately, and
// then the pair has to be resolved from both accessors. The per-field walk
// is the same one either way: it already takes a read model and a write
// model, and every child settles against its own two types.
func (p *pruner) resolveNestedPair(fb *FieldBinding, result, parameter types.Type, kind, key, at, mismatch string) string {
	readNamed, why := nestedModelOf(result)
	if why != "" {
		return mismatch
	}
	writeNamed, why := nestedModelOf(parameter)
	if why != "" {
		return mismatch
	}

	model, constructor, why := p.writeModelFromNamed(writeNamed)
	if why != "" {
		return mismatch
	}
	writeModel, err := p.l.typeFromExpr(p.info, model)
	if err != nil {
		return mismatch
	}

	fb.Access.SDKType = shortType(result)
	fb.Access.SDKWriteType = shortType(parameter)
	fb.NestedModel = qualifiedName(readNamed)
	fb.NestedWriteModel, fb.NestedConstructor = model, constructor
	fb.Access.NestedNilable = nilableType(result)

	fb.Nested = p.fields(kind, key, at, fb.Nested, types.Type(readNamed), writeModel)
	if len(fb.Nested) == 0 {
		return "every field of its nested object went, leaving nothing to map"
	}
	return ""
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
	fb.Access.NestedNilable = nilableType(basis)

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

// nilableType reports whether a value of t can be nil — a pointer,
// interface, slice, map or channel. Every kiota model accessor returns an
// interface, so a kiota nested read is always nilable; a value-typed struct
// return is not. The state mapping uses this to decide whether to guard a
// nested read before dereferencing it.
func nilableType(t types.Type) bool {
	switch t.(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan:
		return true
	}
	_, isInterface := t.Underlying().(*types.Interface)
	return isInterface
}

// nestedModelOf reaches the named model under a nested accessor's type:
// the model itself, a pointer to it, or a slice of either.
func nestedModelOf(t types.Type) (*types.Named, string) {
	current := t
	if slice, ok := current.Underlying().(*types.Slice); ok {
		current = slice.Elem()
	}
	if pointer, ok := current.(*types.Pointer); ok {
		current = pointer.Elem()
	}
	n, ok := current.(*types.Named)
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
	goPackage := named.Obj().Pkg()
	qualifier, name := goPackage.Name(), named.Obj().Name()

	if _, isInterface := named.Underlying().(*types.Interface); isInterface {
		base, ok := strings.CutSuffix(name, "able")
		if !ok {
			return "", "", fmt.Sprintf("%s.%s is an interface with no constructible model behind it", qualifier, name)
		}
		if _, err := p.l.lookupType(goPackage.Path(), base); err != nil {
			return "", "", fmt.Sprintf("%s.%s names no concrete %s to construct", qualifier, name, base)
		}
		if !p.l.functionExists(goPackage.Path(), "New"+base) {
			return "", "", fmt.Sprintf("the SDK declares no constructor New%s for %s.%s", base, qualifier, base)
		}
		return qualifier + "." + base, qualifier + ".New" + base + "()", ""
	}
	if p.l.functionExists(goPackage.Path(), "New"+name+"WithDefaults") {
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
		element := slice.Elem()
		if basic, ok := element.Underlying().(*types.Basic); ok {
			if named, isNamed := element.(*types.Named); isNamed {
				if parse, isEnum := p.enumParse(named); isEnum && fb.ElementType == ir.TypeString {
					settle("[]"+qualifiedName(named), "FromEnumSlice", "ToEnumSlice", parse)
					return ""
				}
				return cannot(shortType(t))
			}
			if !kindCompatible(fb.ElementType, basic) {
				return cannot(shortType(t))
			}
			title := exportedName(basic.Name())
			settle("[]"+basic.Name(), "From"+title+"Slice", "To"+title+"Slice", "")
			return ""
		}
		// time.Time is a struct, not a basic, so a slice of them does not
		// match the branch above.
		if named, isNamed := element.(*types.Named); isNamed && isStdTime(named) && fb.ElementType == ir.TypeString {
			settle("[]time.Time", "FromTimeSlice", "ToTimeSlice", "")
			return ""
		}
		if named, isNamed := element.(*types.Named); isNamed && isGoogleUUID(named) && fb.ElementType == ir.TypeString {
			settle("[]uuid.UUID", "FromUUIDSlice", "ToUUIDSlice", "")
			return ""
		}
		return cannot(shortType(t))
	}

	// kiota models a map-shaped object as a model whose only field is an
	// untyped additionalData bag, so the map is reached through that rather
	// than through a Go map. The bag is map[string]any, so the element
	// conversion asserts each value's type at runtime.
	if fb.Kind == ir.TypeMap {
		if named, ok := t.(*types.Named); ok {
			if _, hasGet := methodOn(named, "GetAdditionalData"); hasGet {
				title := exportedName(string(fb.ElementType))
				// The write model is only resolved for a field that is
				// written: construction is what needs it, and a read-only
				// field has no constructor to name.
				if fa.Set != "" {
					model, constructor, why := p.writeModelFromNamed(named)
					if why != "" {
						return why
					}
					fb.NestedWriteModel, fb.NestedConstructor = model, constructor
				}
				fb.NestedModel = qualifiedName(named)
				fa.NestedNilable = nilableType(t)
				settle(shortType(t), "From"+title+"MapAdditionalData", "To"+title+"MapAdditionalData", "")
				return ""
			}
		}
	}

	// A map of scalars the SDK does carry as a Go map. Only a string key can
	// address a terraform map, and only a scalar value has a catalog bridge.
	if mapType, ok := t.Underlying().(*types.Map); ok && fb.Kind == ir.TypeMap {
		if basic, isBasic := mapType.Key().Underlying().(*types.Basic); !isBasic || basic.Kind() != types.String {
			return cannot(shortType(t))
		}
		basic, isBasic := mapType.Elem().Underlying().(*types.Basic)
		if !isBasic || !kindCompatible(fb.ElementType, basic) {
			return cannot(shortType(t))
		}
		title := exportedName(basic.Name())
		settle("map[string]"+basic.Name(), "From"+title+"Map", "To"+title+"Map", "")
		return ""
	}

	// OpenAPI's `format: byte` is base64, which derives a string attribute
	// rather than a list of numbers, so the bridge is base64 too.
	if slice, ok := t.Underlying().(*types.Slice); ok && fb.Kind == ir.TypeString {
		if basic, isBasic := slice.Elem().Underlying().(*types.Basic); isBasic && basic.Kind() == types.Byte {
			settle("[]byte", "FromBytesBase64", "ToBytesBase64", "")
			return ""
		}
	}

	pointer := false
	current := t
	if pt, ok := current.(*types.Pointer); ok {
		pointer = true
		current = pt.Elem()
	}
	prefix := func(s string) string {
		if pointer {
			return "*" + s
		}
		return s
	}
	convert := func(base string) (string, string) {
		if pointer {
			return "FromPtr" + base, "ToPtr" + base
		}
		return "From" + base, "To" + base
	}

	if named, ok := current.(*types.Named); ok {
		if parse, isEnum := p.enumParse(named); isEnum {
			if fb.Kind != ir.TypeString {
				return cannot(shortType(t))
			}
			get, set := convert("Enum")
			settle(prefix(qualifiedName(named)), get, set, parse)
			return ""
		}
		if isStdTime(named) {
			if fb.Kind != ir.TypeString {
				return cannot(shortType(t))
			}
			get, set := convert("Time")
			settle(prefix("time.Time"), get, set, "")
			return ""
		}
		// kiota mints its own date-only type rather than using time.Time
		// for a `format: date` field.
		if isKiotaDateOnly(named) {
			if fb.Kind != ir.TypeString {
				return cannot(shortType(t))
			}
			get, set := convert("DateOnly")
			settle(prefix("serialization.DateOnly"), get, set, "")
			return ""
		}
		// A uuid is a string to a practitioner. Parsing one back can fail,
		// which the write bridge reports rather than guessing at.
		if isGoogleUUID(named) {
			if fb.Kind != ir.TypeString {
				return cannot(shortType(t))
			}
			get, set := convert("UUID")
			settle(prefix("uuid.UUID"), get, set, "")
			return ""
		}
		return cannot(shortType(t))
	}

	if basic, ok := current.(*types.Basic); ok {
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
	goPackage := named.Obj().Pkg()
	if goPackage == nil {
		return "", false
	}
	basic, ok := named.Underlying().(*types.Basic)
	if !ok {
		return "", false
	}
	name := named.Obj().Name()
	switch {
	case basic.Info()&types.IsInteger != 0 && p.l.functionExists(goPackage.Path(), "Parse"+name):
		return goPackage.Name() + ".Parse" + name, true
	case basic.Info()&types.IsString != 0 && p.l.functionExists(goPackage.Path(), "New"+name+"FromValue"):
		return goPackage.Name() + ".New" + name + "FromValue", true
	}
	return "", false
}

// kindCompatible reports whether a basic SDK type can carry an attribute
// kind: integers of any width carry int64 attributes, floats of either
// width carry float64 ones.
func kindCompatible(kind ir.AttributeType, basic *types.Basic) bool {
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

// isStdTime reports whether a named type is the standard library's
// time.Time.
func isStdTime(named *types.Named) bool {
	goPackage := named.Obj().Pkg()
	return goPackage != nil && goPackage.Path() == "time" && named.Obj().Name() == "Time"
}

// isGoogleUUID reports whether a named type is github.com/google/uuid's
// UUID, the type kiota generates for a `format: uuid` field.
func isGoogleUUID(named *types.Named) bool {
	goPackage := named.Obj().Pkg()
	if goPackage == nil || named.Obj().Name() != "UUID" {
		return false
	}
	return goPackage.Path() == "github.com/google/uuid"
}

// isKiotaDateOnly reports whether a named type is kiota's DateOnly — the
// type its generator uses for a `format: date` field, in place of
// time.Time.
//
// Matched on the package path rather than the package name, because
// "serialization" is a name any SDK might use for a package of its own,
// and bridging some other SDK's DateOnly through kiota's ParseDateOnly
// would not compile.
func isKiotaDateOnly(named *types.Named) bool {
	goPackage := named.Obj().Pkg()
	if goPackage == nil || named.Obj().Name() != "DateOnly" {
		return false
	}
	return strings.HasSuffix(goPackage.Path(), "kiota-abstractions-go/serialization")
}

// keepFromPlan turns a binding into one the response never answers: the
// getter goes at every depth, the marker is set at the root, and what is
// left is the write side alone.
func keepFromPlan(fb *FieldBinding) {
	fb.KeptFromPlan = true
	clearGetters(fb)
}

// clearGetters removes the read accessor from a binding and its subtree.
func clearGetters(fb *FieldBinding) {
	fb.Access.Get = ""
	fb.Access.ConvertGet = ""
	for i := range fb.Nested {
		clearGetters(&fb.Nested[i])
	}
}

// liftKeptFromPlan raises the marker to the root attribute above any member
// that carries it. A response that cannot answer one member of an object
// cannot rebuild the object the state holds, so the whole subtree is kept
// from the plan rather than mapped with a hole in it.
func liftKeptFromPlan(fbs []FieldBinding) {
	for i := range fbs {
		if keptBelow(&fbs[i]) {
			keepFromPlan(&fbs[i])
		}
	}
}

// keptBelow reports whether a binding or any member under it is kept from
// the plan.
func keptBelow(fb *FieldBinding) bool {
	if fb.KeptFromPlan {
		return true
	}
	for i := range fb.Nested {
		if keptBelow(&fb.Nested[i]) {
			return true
		}
	}
	return false
}
