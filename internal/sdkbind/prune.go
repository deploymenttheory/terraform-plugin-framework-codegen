package sdkbind

import (
	"fmt"
	"go/types"
	"regexp"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// Removal is one thing pruning took out of a drafted set, with the SDK's reason.
type Removal struct {
	// Kind is "resource" or "dataSource".
	Kind string
	// Key is the entity's blueprint key.
	Key string
	// Attribute is the pruned attribute, or empty when the whole entity went.
	Attribute string
	// Reason is the binding problem that made the draft unbuildable.
	Reason string
}

func (p Removal) String() string {
	if p.Attribute == "" {
		return fmt.Sprintf("%s %s: %s", p.Kind, p.Key, p.Reason)
	}
	return fmt.Sprintf("%s %s.%s: %s", p.Kind, p.Key, p.Attribute, p.Reason)
}

// Prune removes from a drafted blueprint whatever the pinned SDK cannot carry.
//
// This is the generated replacement for the drop: true a curator used to write.
// Inference deliberately runs from the pinned document alone, and a document
// routinely promises what the generated SDK does not deliver: a schema kiota
// never reached, a request field whose setter takes a different type than the
// response's getter returns, a model deduplicated under another name. Curation
// used to reconcile that by hand, attribute by attribute; here the same
// reconciliation is computed, and every removal is returned with the SDK's own
// reason so the draft run reports what the provider will not carry.
//
// An attribute-level problem removes the attribute. A problem anywhere else --
// the call chain, the body models, the selector machinery -- removes the
// entity, because there is nothing smaller to remove that leaves a buildable
// binding. Removing an identifier removes its resource: a resource that cannot
// be read back is not a resource.
func Prune(l *Loader, bp *blueprint.Blueprint) []Removal {
	var removals []Removal

	// Rounds, because one removal changes what is checkable next: a resource
	// whose body type resolves only after a broken attribute is gone gets its
	// field checks in the following round. Convergence is guaranteed -- every
	// round either removes something or stops -- and the bound is a backstop.
	for range 8 {
		// Reconciliation runs first so that Verify judges the repaired blueprint
		// rather than the drafted one. Both see the same round otherwise, and a
		// spelling repair here would lose to a stale existence complaint there:
		// the attribute would be pruned for a name it no longer uses.
		wire, nested := reconcileWireTypes(l, bp)
		// A nested field is removed where it is found rather than reported: a
		// Problem names a top-level attribute, and routing a leaf's failure
		// through one would prune the whole nested object over a single field.
		removals = append(removals, nested...)

		problems := append(Verify(l, *bp).Problems, wire...)
		if len(problems) == 0 {
			break
		}

		for _, p := range problems {
			removals = append(removals, apply(bp, p)...)
		}
	}

	// Last, because it judges what survived rather than what the SDK carries:
	// an entity can lose its final usable field to any of the rounds above.
	removals = append(removals, dropUnbuildable(bp)...)

	// A lookup companion's seed is the resource it looks up, and pruning may
	// have just removed that resource -- leaving the data source pointing at a
	// key the blueprint no longer declares, which validation refuses. The
	// lookup itself is still good; only its acceptance seed is gone.
	removals = append(removals, dropDanglingSeeds(bp)...)

	return removals
}

// dropUnbuildable removes an entity that survived reconciliation with nothing
// left to generate.
//
// A resource sends a request body built from its expandable attributes and maps
// the response back through its flattenable ones. With neither, the emitter
// skips construct.go and state.go -- there is nothing to put in them -- while
// crud.go still calls constructResource and mapRemoteStateToTerraform, and the
// package does not compile. Jamf's cloud_azure is the case: every field but the
// server-assigned identifier went, and what was left was a resource whose whole
// schema is one computed value, which no HCL can configure and no operator
// could use.
//
// Dropped here rather than refused by the emitter because a draft has to be
// loadable: `blueprint draft` writes what it inferred, and `provider generate`
// reads it back. The emitter refuses the same shape as a backstop, for a
// blueprint written by hand.
func dropUnbuildable(bp *blueprint.Blueprint) []Removal {
	var out []Removal

	for i := range bp.Resources {
		r := &bp.Resources[i]
		if r.Drop {
			continue
		}
		if why := unbuildableReason(r.Schema.Attributes, true); why != "" {
			r.Drop = true
			out = append(out, Removal{Kind: "resource", Key: r.Key, Reason: why})
		}
	}

	for i := range bp.DataSources {
		d := &bp.DataSources[i]
		if d.Drop {
			continue
		}
		// A data source sends no body, so only the read direction is required
		// of it.
		if why := unbuildableReason(d.Schema.Attributes, false); why != "" {
			d.Drop = true
			out = append(out, Removal{Kind: "dataSource", Key: d.Key, Reason: why})
		}
	}

	return out
}

// unbuildableReason names the missing direction, or is empty when both the
// emitter needs are present. The tests mirror constructView and stateView: an
// attribute counts when it is live and carries a conversion that way.
func unbuildableReason(attrs []blueprint.Attribute, needsExpand bool) string {
	var expands, flattens bool
	for _, a := range attrs {
		if a.Drop {
			continue
		}
		if !a.Wire.SkipExpand && a.Wire.Expand != nil {
			expands = true
		}
		if !a.Wire.SkipFlatten && a.Wire.Flatten != nil {
			flattens = true
		}
	}

	switch {
	case needsExpand && !expands:
		return "no attribute survives that can be sent in a request body, so there is " +
			"nothing to construct and nothing an operator could configure"
	case !flattens:
		return "no attribute survives that can be read back from a response, so there is " +
			"nothing to map into state"
	}

	return ""
}

// dropDanglingSeeds clears an accTest seed naming a resource that is absent or
// dropped, so a surviving data source does not carry a reference to something
// pruning removed.
func dropDanglingSeeds(bp *blueprint.Blueprint) []Removal {
	live := make(map[string]bool, len(bp.Resources))
	for _, r := range bp.Resources {
		if !r.Drop {
			live[r.Key] = true
		}
	}

	var out []Removal
	for i := range bp.DataSources {
		d := &bp.DataSources[i]
		if d.Drop || d.AccTest == nil || live[d.AccTest.SeedResourceKey] {
			continue
		}
		out = append(out, Removal{
			Kind: "dataSource", Key: d.Key, Attribute: "accTest",
			Reason: fmt.Sprintf(
				"its acceptance seed named resource %q, which is not in the blueprint; the "+
					"lookup stands, but nothing generated can create an object for it to find",
				d.AccTest.SeedResourceKey),
		})
		d.AccTest = nil
	}

	return out
}

var attrPath = regexp.MustCompile(`attributes\[([^\]]+)\]`)

// apply turns one problem into removals on the blueprint, in place.
func apply(bp *blueprint.Blueprint, p Problem) []Removal {
	attr := ""
	if m := attrPath.FindStringSubmatch(p.Path); m != nil {
		attr = m[1]
	}

	reason := p.Path + ": " + p.Detail

	for i := range bp.Resources {
		r := &bp.Resources[i]
		if r.Key != p.Resource || r.Drop {
			continue
		}
		if attr == "" || attr == r.Binding.ID.Attribute {
			why := reason
			if attr != "" {
				why = "its identifier does not survive: " + reason
			}
			r.Drop = true
			return []Removal{{Kind: "resource", Key: r.Key, Reason: why}}
		}
		if removeAttribute(&r.Schema.Attributes, attr) {
			if len(r.Schema.Attributes) == 0 {
				r.Drop = true
				return []Removal{{Kind: "resource", Key: r.Key, Reason: "every attribute was pruned: " + reason}}
			}
			return []Removal{{Kind: "resource", Key: r.Key, Attribute: attr, Reason: reason}}
		}
		return nil
	}

	for i := range bp.DataSources {
		d := &bp.DataSources[i]
		if d.Key != p.Resource || d.Drop {
			continue
		}
		// A data source is its selectors: pruning an attribute a selector reads
		// leaves a lookup that cannot be asked for, so the entity goes whole.
		if attr != "" && !selectorNamed(*d, attr) {
			if removeAttribute(&d.Schema.Attributes, attr) && len(d.Schema.Attributes) > 0 {
				return []Removal{{Kind: "dataSource", Key: d.Key, Attribute: attr, Reason: reason}}
			}
		}
		d.Drop = true
		return []Removal{{Kind: "dataSource", Key: d.Key, Reason: reason}}
	}

	return nil
}

func removeAttribute(attrs *[]blueprint.Attribute, name string) bool {
	for i := range *attrs {
		if (*attrs)[i].Name == name {
			*attrs = append((*attrs)[:i], (*attrs)[i+1:]...)
			return true
		}
	}
	return false
}

func selectorNamed(d blueprint.DataSource, attr string) bool {
	for _, s := range d.Binding.Selectors {
		if s.Attribute == attr {
			return true
		}
	}
	return false
}

// reconcileWireTypes holds every attribute's declared SDK Go type against the
// type the SDK actually carries for that field, repairing what it can and
// reporting what it cannot.
//
// Existence is not agreement: ThousandEyes' voice test writes alertRules as a
// list of rule identifiers and reads it back as a list of rule objects, so the
// setter exists, takes []string, and the drafted object conversion against it
// does not compile. bindings check stays existence-only because the committed
// curated sets record sdkGoType loosely; a draft records exactly what it
// derived, so a draft can be held to the letter.
//
// Two divergences are repaired in place rather than pruned, because the SDK's
// side is usable and only the spelling is the draft's fault: kiota deduplicates
// structurally identical models under one canonical name the document never
// uses, and it mints named enum types from inline enumerations the document
// never named. Everything else -- a genuinely different shape on each side --
// is returned as a problem for pruning.
func reconcileWireTypes(l *Loader, bp *blueprint.Blueprint) ([]Problem, []Removal) {
	var (
		out      []Problem
		removals []Removal
	)

	problem := func(key, attrName, direction, want string, got string) Problem {
		return Problem{
			Resource: key,
			Path:     fmt.Sprintf("attributes[%s].wire.sdkGoType", attrName),
			Detail: fmt.Sprintf("declared %s, but the SDK's %s side carries %s; "+
				"one of them cannot be generated against", want, direction, got),
		}
	}

	for i := range bp.Resources {
		res := &bp.Resources[i]
		if res.Drop {
			continue
		}
		svc := res.Binding.Service
		style := res.Binding.Body.AccessStyle
		request := typeNameOf(res.Binding.Body.RequestType)
		response := typeNameOf(res.Binding.Body.ResponseType)

		for j := range res.Schema.Attributes {
			a := &res.Schema.Attributes[j]
			want := a.Wire.SDKGoType
			if a.Drop || a.Wire.SDKField == "" || want == "" || want == "any" {
				continue
			}
			settled := len(out)
			readActive := !a.Wire.SkipFlatten && a.Wire.Flatten != nil && response != ""
			if readActive {
				got, err := resolveField(l, style, svc.ImportPath, response, a, false)
				if err == nil && !typeMatches(got, want) {
					if !repair(l, svc.ImportPath, a, got) {
						out = append(out, problem(res.Key, a.Name, "read", want, shortType(got)))
						continue
					}
				}
			}
			if !a.Wire.SkipExpand && a.Wire.Expand != nil && request != "" {
				got, err := resolveField(l, style, svc.ImportPath, request, a, true)
				if err == nil && !typeMatches(got, a.Wire.SDKGoType) {
					// With a live read side, the read spelling is already settled
					// above; a write side that still disagrees is a genuinely
					// different shape per direction -- ThousandEyes writes rule
					// identifiers and reads rule objects -- and no repair can
					// satisfy both. Repair is for the write-only case.
					if readActive || !repair(l, svc.ImportPath, a, got) {
						out = append(out, problem(res.Key, a.Name, "write", a.Wire.SDKGoType, shortType(got)))
					}
				}
			}

			// Only once this attribute's own spelling is settled: a nested
			// object retargeted at kiota's canonical model has different fields
			// to check than the one the document named.
			if len(out) == settled {
				rem, emptied := reconcileNested(
					l, style, svc.ImportPath, "resource", res.Key, a.Name, a.Type.NestedObject)
				removals = append(removals, rem...)
				if emptied {
					out = append(out, emptiedProblem(res.Key, a.Name))
				}
			}
		}
	}

	for i := range bp.DataSources {
		d := &bp.DataSources[i]
		if d.Drop {
			continue
		}
		svc := d.Binding.Service
		response := typeNameOf(d.Binding.Response.Type)
		if response == "" {
			continue
		}
		for j := range d.Schema.Attributes {
			a := &d.Schema.Attributes[j]
			want := a.Wire.SDKGoType
			if a.Drop || a.Wire.SDKField == "" || want == "" || want == "any" {
				continue
			}
			if a.Wire.SkipFlatten || a.Wire.Flatten == nil {
				continue
			}
			style := d.Binding.Response.AccessStyle
			got, err := resolveField(l, style, svc.ImportPath, response, a, false)
			if err == nil && !typeMatches(got, want) && !repair(l, svc.ImportPath, a, got) {
				out = append(out, Problem{
					Resource: d.Key,
					Path:     fmt.Sprintf("schema.attributes[%s].wire.sdkGoType", a.Name),
					Detail: fmt.Sprintf("declared %s, but the SDK carries %s; "+
						"the flatten cannot be generated against it", want, shortType(got)),
				})
				continue
			}

			rem, emptied := reconcileNested(
				l, style, svc.ImportPath, "dataSource", d.Key, a.Name, a.Type.NestedObject)
			removals = append(removals, rem...)
			if emptied {
				out = append(out, emptiedProblem(d.Key, a.Name))
			}
		}

		reconcileSelectors(l, d)
	}

	return out, removals
}

// resolveField looks one attribute's field up on a model, repairing the one
// mangling the document cannot predict.
//
// kiota escapes a property whose name its Go generator reserves, and inference
// has to guess that list from the document alone. It guesses with Go's own
// keywords and predeclared names, and Jamf shows the guess wrong in both
// directions: "vendor" is escaped by kiota and not by us, because Go reserves
// the name for a directory rather than as an identifier; "make" is escaped by
// us and not by kiota, because a predeclared function is not a name kiota's
// refiner avoids. Either way the drafted accessor does not exist.
//
// So the repair is symmetric, and it reads the answer off the SDK rather than
// off a longer word list -- a word list is only ever as current as the kiota
// release someone last read, and the loaded SDK is the release in use.
func resolveField(
	l *Loader,
	style blueprint.AccessStyle,
	modelsImport, typeName string,
	a *blueprint.Attribute,
	wantSet bool,
) (types.Type, error) {
	got, err := l.LookupFieldAccess(style, modelsImport, typeName, a.Wire.SDKField, wantSet)
	if err == nil {
		return got, nil
	}

	other, ok := strings.CutSuffix(a.Wire.SDKField, escapedSuffix)
	if !ok {
		other = a.Wire.SDKField + escapedSuffix
	}

	alt, altErr := l.LookupFieldAccess(style, modelsImport, typeName, other, wantSet)
	if altErr != nil {
		// The original miss is the honest one to report: the other spelling is a
		// guess this function made, not something the blueprint claimed.
		return nil, err
	}
	a.Wire.SDKField = other

	return alt, nil
}

const escapedSuffix = "Escaped"

// emptiedProblem reports a nested object whose every field turned out to be
// ungeneratable. The attribute goes, because an object model with no fields
// maps nothing and the generated helpers for it would not compile.
func emptiedProblem(key, attrName string) Problem {
	return Problem{
		Resource: key,
		Path:     fmt.Sprintf("attributes[%s].nestedObject", attrName),
		Detail: "every field of its nested object is either absent from the SDK's model or " +
			"cannot be generated against it, leaving nothing to map",
	}
}

// nestedModels names the two SDK types a nested object's fields are reached
// through: the type a getter reads out of, and the type a setter writes into.
//
// They are one type under a struct-field SDK and two under kiota, whose
// response interface carries getters only and whose constructor yields the
// concrete struct the setters hang off. Computed rather than assumed, because
// checking a setter against the interface would report every writable field as
// missing and prune the object whole.
func nestedModels(n *blueprint.NestedAttributeObject) (read, write string) {
	read = typeNameOf(n.SDKType)
	write = read
	if n.ConstructorExpr != "" {
		if base, ok := cutSuffix(read, "able"); ok {
			write = base
		}
	}
	return read, write
}

// reconcileNested holds a nested object's fields against the SDK model that
// actually carries them, at every depth.
//
// The top-level loop cannot reach these. It checks an attribute's declared
// spelling against a field on the body model, and a nested attribute's field
// lives on a different model entirely -- the one its parent resolves to. Left
// unvisited, an enumeration the document declared inline two levels down kept
// the string spelling inference implied for it, and the generated flatten did
// not compile: the same divergence repair already fixes at the top, on fields it
// simply never saw.
//
// A field is removed where it is found rather than reported as a Problem,
// because a Problem names a top-level attribute and pruning one would take a
// whole nested object with it over a single unbuildable leaf. An object left
// with no fields is reported to the level above instead, which owns removing
// the attribute that held it.
func reconcileNested(
	l *Loader,
	style blueprint.AccessStyle,
	modelsImport, kind, key, path string,
	n *blueprint.NestedAttributeObject,
) (removals []Removal, emptied bool) {
	if n == nil {
		return nil, false
	}
	readModel, writeModel := nestedModels(n)
	if readModel == "" {
		return nil, false
	}

	kept := n.Attributes[:0]
	for i := range n.Attributes {
		c := n.Attributes[i]
		at := path + "." + c.Name

		if why := reconcileNestedAttr(l, style, modelsImport, readModel, writeModel, &c); why != "" {
			removals = append(removals, Removal{Kind: kind, Key: key, Attribute: at, Reason: why})
			continue
		}

		sub, subEmptied := reconcileNested(l, style, modelsImport, kind, key, at, c.Type.NestedObject)
		removals = append(removals, sub...)
		if subEmptied {
			removals = append(removals, Removal{
				Kind: kind, Key: key, Attribute: at,
				Reason: "every field of its nested object went, leaving nothing to map",
			})
			continue
		}

		kept = append(kept, c)
	}
	n.Attributes = kept

	return removals, len(n.Attributes) == 0
}

// reconcileNestedAttr checks one nested field in both directions, repairing a
// type the SDK carries under another spelling and returning, in the SDK's own
// terms, why a field that cannot be generated has to go.
func reconcileNestedAttr(
	l *Loader,
	style blueprint.AccessStyle,
	modelsImport, readModel, writeModel string,
	a *blueprint.Attribute,
) string {
	if a.Drop || a.Wire.SDKField == "" {
		return ""
	}
	want := a.Wire.SDKGoType

	readActive := !a.Wire.SkipFlatten && a.Wire.Flatten != nil
	if readActive {
		got, err := resolveField(l, style, modelsImport, readModel, a, false)
		if err != nil {
			// kiota deduplicates structurally identical models under one
			// canonical name, and the winner's fields are the SDK's, not the
			// document's: a field the losing spelling declared is simply not
			// there to read.
			return fmt.Sprintf("%s carries no %s to read it from", readModel, a.Wire.SDKField)
		}
		if want != "" && want != "any" && !typeMatches(got, want) &&
			!repair(l, modelsImport, a, got) {
			return fmt.Sprintf("declared %s, but %s carries %s", want, readModel, shortType(got))
		}
	}

	if !a.Wire.SkipExpand && a.Wire.Expand != nil && writeModel != "" {
		got, err := resolveField(l, style, modelsImport, writeModel, a, true)
		if err != nil {
			return fmt.Sprintf("%s carries no settable %s to write it to", writeModel, a.Wire.SDKField)
		}
		if want := a.Wire.SDKGoType; want != "" && want != "any" && !typeMatches(got, want) &&
			(readActive || !repair(l, modelsImport, a, got)) {
			return fmt.Sprintf("declared %s, but %s takes %s", want, writeModel, shortType(got))
		}
	}

	return ""
}

// reconcileSelectors records, for each selector, whether the list element holds
// the field as a generated enumeration.
//
// The element is a different model from the read response -- Jamf returns
// CloudIdPCommon from the by-id read and CloudIdPCommonResponse from the list --
// so the attribute's own reconciled spelling says nothing about it. And the
// distinction is load-bearing rather than cosmetic: an enumeration compared
// against a configured string is not a wrong answer but a compile error, so the
// emitter has to know to go through String().
//
// Nothing is reported as a problem here. A selector that resolves to neither an
// enumeration nor a scalar is a shape the resolver already refuses to render,
// and it refuses it by name.
func reconcileSelectors(l *Loader, d *blueprint.DataSource) {
	element := typeNameOf(d.Binding.ElementType)
	if element == "" {
		return
	}
	for i := range d.Binding.Selectors {
		s := &d.Binding.Selectors[i]
		if s.ViaRead || s.SDKField == "" {
			continue
		}
		got, err := l.LookupFieldAccess(
			d.Binding.Response.AccessStyle, d.Binding.Service.ImportPath, element, s.SDKField, false)
		if err != nil {
			continue
		}
		s.SDKEnum = l.isKiotaEnum(got)
	}
}

// isKiotaEnum reports whether a type is a generated enumeration: a pointer to an
// int-backed named type paired with a Parse function of its own. That triple is
// what kiota mints for a documented value set, and it is what makes String() safe
// to reach for -- an ordinary named integer carries no such method.
func (l *Loader) isKiotaEnum(t types.Type) bool {
	ptr, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok {
		return false
	}
	return l.isKiotaEnumNamed(named)
}

func (l *Loader) isKiotaEnumNamed(named *types.Named) bool {
	if named.Obj().Pkg() == nil {
		return false
	}
	basic, ok := named.Underlying().(*types.Basic)
	if !ok || basic.Info()&types.IsInteger == 0 {
		return false
	}
	return l.functionExists(named.Obj().Pkg().Path(), "Parse"+named.Obj().Name())
}

// repair rewrites an attribute's SDK spelling to the type the SDK actually
// carries, when the SDK's side is the same shape under another name.
func repair(l *Loader, modelsImport string, a *blueprint.Attribute, got types.Type) bool {
	switch t := got.(type) {
	case *types.Pointer:
		named, ok := t.Elem().(*types.Named)
		if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != modelsImport {
			return false
		}
		// A pointer to an int-backed named type with a Parse function is a kiota
		// enum, minted from an enumeration the document declared inline. The
		// attribute stays a string; the converters bridge through Parse.
		if l.isKiotaEnumNamed(named) &&
			a.Type.Kind == blueprint.KindString &&
			a.Type.NestedObject == nil {
			ref := "models." + named.Obj().Name()
			a.Wire.SDKGoType = "*" + ref
			a.Wire.Flatten = &blueprint.ConvertCall{Func: "convert.PtrStringerToFramework"}
			if !a.Wire.SkipExpand && a.Wire.Expand != nil {
				a.Wire.Expand = &blueprint.ConvertCall{
					Func: "convert.FrameworkToKiotaEnum", TypeArgs: []string{ref},
					ExtraArgs: []string{"models.Parse" + named.Obj().Name()}, ReturnsError: true,
				}
			}
			return true
		}
		return false

	case *types.Named:
		// A single nested object under another canonical name: kiota deduplicates
		// structurally identical models, and the winner's name is the SDK's, not
		// the document's.
		return repairNested(a, t, modelsImport, false)

	case *types.Slice:
		named, ok := t.Elem().(*types.Named)
		if !ok {
			return false
		}
		// A slice of a generated enumeration. The attribute stays a set of
		// strings and the pair bridges through String() and Parse -- exactly
		// what inference wires for an enumeration it could see named in the
		// document, and what it could not wire for one declared inline.
		if named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == modelsImport &&
			l.isKiotaEnumNamed(named) &&
			a.Type.Kind == blueprint.KindSet && a.Type.NestedObject == nil &&
			a.Type.ElementType != nil && a.Type.ElementType.Kind == blueprint.KindString {
			ref := "models." + named.Obj().Name()
			a.Wire.SDKGoType = "[]" + ref
			a.Wire.Flatten = &blueprint.ConvertCall{
				Func: "convert.KiotaEnumSliceToFrameworkSet", NeedsCtx: true, ReturnsError: true,
			}
			if !a.Wire.SkipExpand && a.Wire.Expand != nil {
				a.Wire.Expand = &blueprint.ConvertCall{
					Func: "convert.FrameworkSetToKiotaEnumSlice", TypeArgs: []string{ref},
					ExtraArgs: []string{"models.Parse" + named.Obj().Name()},
					NeedsCtx:  true, ReturnsError: true,
				}
			}
			return true
		}
		return repairNested(a, named, modelsImport, true)
	}

	return false
}

// repairNested points a drafted nested object at the SDK's canonical model.
func repairNested(a *blueprint.Attribute, named *types.Named, modelsImport string, collection bool) bool {
	n := a.Type.NestedObject
	if n == nil || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != modelsImport {
		return false
	}
	if _, isInterface := named.Underlying().(*types.Interface); !isInterface {
		return false
	}

	iface := named.Obj().Name()
	base, ok := cutSuffix(iface, "able")
	if !ok {
		return false
	}

	n.SDKType = "models." + iface
	if n.ConstructorExpr != "" {
		n.ConstructorExpr = "models.New" + base + "()"
	}
	sdkGoType := "models." + iface
	if collection {
		sdkGoType = "[]models." + iface
	}
	a.Wire.SDKGoType = sdkGoType

	return true
}

func cutSuffix(s, suffix string) (string, bool) {
	if len(s) <= len(suffix) || s[len(s)-len(suffix):] != suffix {
		return s, false
	}
	return s[:len(s)-len(suffix)], true
}

// typeMatches compares the declared spelling against the resolved type,
// textually, on the package-qualified rendering -- the same spelling the
// blueprint declares.
func typeMatches(got types.Type, want string) bool {
	return shortType(got) == want
}
