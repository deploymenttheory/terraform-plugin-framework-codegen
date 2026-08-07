package sdkbind

import (
	"fmt"
	"go/types"
	"regexp"

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
		problems := Verify(l, *bp).Problems
		problems = append(problems, reconcileWireTypes(l, bp)...)
		if len(problems) == 0 {
			break
		}

		for _, p := range problems {
			removals = append(removals, apply(bp, p)...)
		}
	}

	// A lookup companion's seed is the resource it looks up, and pruning may
	// have just removed that resource -- leaving the data source pointing at a
	// key the blueprint no longer declares, which validation refuses. The
	// lookup itself is still good; only its acceptance seed is gone.
	removals = append(removals, dropDanglingSeeds(bp)...)

	return removals
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
func reconcileWireTypes(l *Loader, bp *blueprint.Blueprint) []Problem {
	var out []Problem

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
			readActive := !a.Wire.SkipFlatten && a.Wire.Flatten != nil && response != ""
			if readActive {
				got, err := l.LookupFieldAccess(style, svc.ImportPath, response, a.Wire.SDKField, false)
				if err == nil && !typeMatches(got, want) {
					if !repair(l, svc.ImportPath, a, got) {
						out = append(out, problem(res.Key, a.Name, "read", want, shortType(got)))
						continue
					}
				}
			}
			if !a.Wire.SkipExpand && a.Wire.Expand != nil && request != "" {
				got, err := l.LookupFieldAccess(style, svc.ImportPath, request, a.Wire.SDKField, true)
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
			got, err := l.LookupFieldAccess(d.Binding.Response.AccessStyle, svc.ImportPath, response, a.Wire.SDKField, false)
			if err == nil && !typeMatches(got, want) {
				if repair(l, svc.ImportPath, a, got) {
					continue
				}
				out = append(out, Problem{
					Resource: d.Key,
					Path:     fmt.Sprintf("schema.attributes[%s].wire.sdkGoType", a.Name),
					Detail: fmt.Sprintf("declared %s, but the SDK carries %s; "+
						"the flatten cannot be generated against it", want, shortType(got)),
				})
			}
		}
	}

	return out
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
		if basic, isBasic := named.Underlying().(*types.Basic); isBasic &&
			basic.Info()&types.IsInteger != 0 &&
			a.Type.Kind == blueprint.KindString &&
			a.Type.NestedObject == nil &&
			l.functionExists(modelsImport, "Parse"+named.Obj().Name()) {
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
