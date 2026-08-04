package openapi

import (
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"
)

// inferCtx carries what inferring one resource's attributes needs beyond the field itself.
//
// The taken set is the reason this is a struct rather than more parameters: every nested
// object contributes five package-level identifiers to the same generated package, and
// they have to be unique across the whole resource rather than within one object. Render
// refuses a collision, so resolving it here is the difference between a draft that emits
// and one that does not.
type inferCtx struct {
	// resource is the blueprint key, for notes.
	resource string
	// resourceBase is the resource's Go type stem, e.g. "Tag", used to prefix a nested
	// object's generated names.
	resourceBase string
	// sdkPkg is the SDK package a nested object's Go type lives in, e.g. "tags".
	sdkPkg string
	// dialect decides the SDK-facing spellings: accessor bases, model type
	// shapes and constructors.
	dialect blueprint.SDKDialect

	taken map[string]bool
	notes []Caveat
}

func newInferCtx(resourceKey, sdkPkg string, dialect blueprint.SDKDialect) *inferCtx {
	return &inferCtx{
		resource:     resourceKey,
		resourceBase: namingOpts.GoTypeName(resourceKey),
		sdkPkg:       sdkPkg,
		dialect:      dialect,
		taken:        map[string]bool{},
	}
}

func (ctx *inferCtx) note(path, msg string) {
	ctx.notes = append(ctx.notes, Caveat{Resource: ctx.resource, Field: path, Message: msg})
}

// nestedObject builds the generated shape for one nested object, recursing into it.
//
// writable says whether the enclosing attribute can be written. It is threaded down rather
// than recomputed per level because a field inside a read-only object cannot be written
// however the schema marks it, and an expand conversion on it would name a helper the
// emitter never generates.
func (ctx *inferCtx) nestedObject(
	f Field,
	path string,
	writable bool,
) (*blueprint.NestedAttributeObject, string) {
	// Ordinarily unreachable: skipField refuses a cyclic attribute before it gets here, and
	// refuses it whole rather than at the cycle point. Kept because this is exported
	// behaviour of the type, not of one caller.
	if n := f.CyclicSchema(); n != "" {
		return nil, "the schema " + n + " contains itself, so its depth is decided by the " +
			"data rather than by the schema; write this resource by hand"
	}
	if len(f.Object) == 0 {
		return nil, "the nested object's schema declares no properties, so it would generate " +
			"an empty model"
	}

	base := ctx.nestedBase(f.ObjectTypeName)
	varBase := namingOpts.GoVarName(base)

	// A collection's helpers read better plural: expandTagAssignments over
	// expandTagAssignment for a function that takes a set.
	helperBase := base
	if f.Kind.IsNestedCollection() {
		helperBase = pluralise(base)
	}

	sdkType := ctx.sdkPkg + "." + namingOpts.GoTypeName(f.ObjectTypeName)
	constructor := ""
	if ctx.dialect == blueprint.DialectKiotaFluent {
		// Builders traffic in the <Type>able interface; elements are built by
		// constructor and filled through setters.
		kn := kiotaName(f.ObjectTypeName)
		if kn == "" {
			kn = ctx.resourceBase + "Object"
		}
		sdkType = "models." + kn + "able"
		constructor = "models.New" + kn + "()"
	}

	n := &blueprint.NestedAttributeObject{
		GoTypeName:      naming.Unique(ctx.taken, base+"Model"),
		SDKType:         sdkType,
		ConstructorExpr: constructor,
		AttrTypesVar:    naming.Unique(ctx.taken, varBase+"AttrTypes"),
		ObjectTypeVar:   naming.Unique(ctx.taken, varBase+"ObjectType"),
		// Both directions are named even when the object is read-only. The name costs
		// nothing, blueprint.Validate requires it on a resource, and a later decision to
		// make the attribute writable then needs no new identifier.
		ExpandFunc:  naming.Unique(ctx.taken, "expand"+helperBase),
		FlattenFunc: naming.Unique(ctx.taken, "flatten"+helperBase),
	}

	for _, child := range f.Object {
		cpath := path + "." + child.Name

		if skip, why := skipField(child); skip {
			ctx.note(cpath, why)
			continue
		}

		attr, why := ctx.attributeOf(child, cpath, writable && !child.ReadOnly)
		if why != "" {
			ctx.note(cpath, why)
			continue
		}
		n.Attributes = append(n.Attributes, attr)
	}

	if len(n.Attributes) == 0 {
		return nil, "every property of the nested object was skipped, so it would generate an " +
			"empty model"
	}

	return n, ""
}

// nestedBase names a nested object's generated types, eliding a repeated prefix.
//
// A schema already named for its resource -- TagFilter inside tag -- would otherwise
// become TagTagFilter. Both forms are unique, so this is a readability rule rather than a
// correctness one, but it is what makes an inferred draft match what a person would have
// written by hand.
func (ctx *inferCtx) nestedBase(objectTypeName string) string {
	name := namingOpts.GoTypeName(objectTypeName)
	if name == "" {
		return ctx.resourceBase + "Object"
	}
	if strings.HasPrefix(name, ctx.resourceBase) {
		return name
	}
	return ctx.resourceBase + name
}

// pluralise is the small subset of English plurals that generated identifiers need.
//
// It reads a generated function name, not prose: the point is that expandTagAssignments
// says it takes many where expandTagAssignment would not. An irregular plural comes out
// wrong and a person renames it, which is the same thing they do to every other inferred
// name.
func pluralise(s string) string {
	switch {
	case s == "":
		return s
	case hasAnySuffix(s, "s", "x", "z", "ch", "sh"):
		return s + "es"
	case len(s) > 1 && strings.HasSuffix(s, "y") && !isVowel(s[len(s)-2]):
		return s[:len(s)-1] + "ies"
	default:
		return s + "s"
	}
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

func isVowel(b byte) bool {
	return strings.IndexByte("aeiouAEIOU", b) >= 0
}
