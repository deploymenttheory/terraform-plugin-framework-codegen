// Package sdkbind type-checks a blueprint's SDK bindings against the SDK the
// generated provider will actually compile against.
//
// A blueprint names SDK symbols as strings: a service type, a method, a struct
// field, an accessor expression. Nothing about a string stops it naming something
// that does not exist. When it does, the mistake surfaces as a pile of identical
// compile errors in generated code, pointing at the generated line rather than at
// the blueprint field that is wrong.
//
// This package reports it against the blueprint instead. It exists because the
// mistake has already happened once: a binding was written from the SDK's working
// tree, where the services hang directly off the root client, while the pinned
// version groups them under a nested API struct. `r.client.Tags` type-checked
// nowhere and the only signal was four identical errors in crud.go.
//
// It uses real type information rather than a syntax scan, because the questions
// worth asking are about types: does this method exist on this type, does its
// result arity match what the blueprint claims, does this field chain resolve.
package sdkbind

import (
	"errors"
	"fmt"
	"go/types"
	"sort"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

var (
	// ErrLoad reports that the SDK could not be type-checked at all.
	ErrLoad = errors.New("cannot load the SDK")
	// ErrBindings reports one or more bindings that do not match the SDK.
	ErrBindings = errors.New("blueprint bindings do not match the SDK")
)

// Loader resolves SDK packages in the context of a module that depends on them.
//
// The module directory matters: the toolkit's own module does not depend on any
// provider SDK, so packages are loaded from the generated provider's module, which
// does. That also guarantees the version checked is the version the provider will
// compile against, rather than whatever happens to be in a local checkout.
type Loader struct {
	dir string

	// mu guards cache: a shared loader is the difference between one go/packages
	// invocation per run and one per test, and sharing means concurrent lookups.
	mu    sync.Mutex
	cache map[string]*packages.Package
}

// NewLoader returns a loader resolving imports as moduleDir does.
func NewLoader(moduleDir string) *Loader {
	return &Loader{dir: moduleDir, cache: map[string]*packages.Package{}}
}

// Preload type-checks every given package in one go/packages invocation.
//
// Each packages.Load call spawns the go list machinery, which costs seconds
// regardless of how many patterns it is given -- so loading eighteen SDK packages
// one call at a time costs eighteen spawns where one would do. Verify calls this
// with everything the blueprint names; load stays as the fallback for a path
// nothing predicted.
func (l *Loader) Preload(importPaths []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	missing := make([]string, 0, len(importPaths))
	for _, ip := range importPaths {
		if _, ok := l.cache[ip]; !ok && ip != "" {
			missing = append(missing, ip)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedSyntax | packages.NeedImports | packages.NeedDeps,
		Dir: l.dir,
	}

	pkgs, err := packages.Load(cfg, missing...)
	if err != nil {
		return fmt.Errorf("%w %v from %s: %w", ErrLoad, missing, l.dir, err)
	}

	for _, pkg := range pkgs {
		// A package that failed to load is left out of the cache, so the per-path
		// fallback reports it with the message shape callers already handle.
		if len(pkg.Errors) != 0 || pkg.Types == nil || pkg.Types.Scope() == nil {
			continue
		}
		l.cache[pkg.PkgPath] = pkg
	}

	return nil
}

// load type-checks one package.
func (l *Loader) load(importPath string) (*packages.Package, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if p, ok := l.cache[importPath]; ok {
		return p, nil
	}

	cfg := &packages.Config{
		// NeedDeps and NeedImports are required for a type in one package to be
		// resolvable from another, which every accessor walk needs.
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedSyntax | packages.NeedImports | packages.NeedDeps,
		Dir: l.dir,
	}

	pkgs, err := packages.Load(cfg, importPath)
	if err != nil {
		return nil, fmt.Errorf("%w %s from %s: %w", ErrLoad, importPath, l.dir, err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("%w: %s resolved to no packages from %s", ErrLoad, importPath, l.dir)
	}

	pkg := pkgs[0]
	if len(pkg.Errors) != 0 {
		// Reporting only the first is enough: they are nearly always one cause,
		// and the full list is noise.
		return nil, fmt.Errorf("%w %s: %v", ErrLoad, importPath, pkg.Errors[0])
	}
	if pkg.Types == nil || pkg.Types.Scope() == nil {
		return nil, fmt.Errorf("%w %s: no type information", ErrLoad, importPath)
	}

	l.cache[importPath] = pkg

	return pkg, nil
}

// LookupType returns a named type declared in importPath.
func (l *Loader) LookupType(importPath, name string) (*types.Named, error) {
	pkg, err := l.load(importPath)
	if err != nil {
		return nil, err
	}

	obj := pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return nil, fmt.Errorf("%w: package %s has no exported type %s%s",
			ErrBindings, pkg.Name, name, didYouMean(name, typeNames(pkg)))
	}

	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("%w: %s.%s is not a named type", ErrBindings, pkg.Name, name)
	}

	return named, nil
}

// Method describes one method found on a type.
type Method struct {
	Name string
	// Params and Results are the signature's types, rendered as source.
	Params  []string
	Results []string
}

// Signature renders the method as it would be declared, for error messages that
// show what the SDK actually offers rather than only that the guess was wrong.
func (m Method) Signature() string {
	return fmt.Sprintf("%s(%s) (%s)", m.Name, strings.Join(m.Params, ", "), strings.Join(m.Results, ", "))
}

// LookupMethod returns a method on a named type, following a pointer receiver.
func (l *Loader) LookupMethod(importPath, typeName, method string) (Method, error) {
	named, err := l.LookupType(importPath, typeName)
	if err != nil {
		return Method{}, err
	}

	// Generated SDK services declare their methods on the pointer receiver, so
	// the method set of the value type does not contain them.
	ms := types.NewMethodSet(types.NewPointer(named))

	for i := range ms.Len() {
		sel := ms.At(i)
		if sel.Obj().Name() != method {
			continue
		}

		sig, ok := sel.Obj().Type().(*types.Signature)
		if !ok {
			continue
		}

		return methodFrom(method, sig), nil
	}

	return Method{}, fmt.Errorf("%w: %s has no method %s%s",
		ErrBindings, shortType(named), method, didYouMean(method, methodNames(ms)))
}

func methodFrom(name string, sig *types.Signature) Method {
	m := Method{Name: name}

	for i := range sig.Params().Len() {
		m.Params = append(m.Params, shortType(sig.Params().At(i).Type()))
	}
	for i := range sig.Results().Len() {
		m.Results = append(m.Results, shortType(sig.Results().At(i).Type()))
	}

	return m
}

// FieldChain walks a chain of struct field selectors from a starting type.
//
// This is what verifies an accessor. Given the client type and ["API", "Tags"] it
// resolves each field in turn, so a blueprint claiming a service hangs directly
// off the root client is caught here rather than in generated code.
func (l *Loader) FieldChain(start types.Type, chain []string) (types.Type, error) {
	current := start

	for i, name := range chain {
		st, err := structUnder(current)
		if err != nil {
			return nil, fmt.Errorf("%w: %s is %s, which has no field %q",
				ErrBindings, strings.Join(chain[:i], "."), shortType(current), name)
		}

		field, ok := fieldByName(st, name)
		if !ok {
			return nil, fmt.Errorf("%w: %s has no field %q%s",
				ErrBindings, shortType(current), name, didYouMean(name, fieldNames(st)))
		}

		current = field.Type()
	}

	return current, nil
}

// LookupField returns a struct field's type, for checking that a wire binding
// names a field the SDK model actually has.
func (l *Loader) LookupField(importPath, typeName, field string) (types.Type, error) {
	named, err := l.LookupType(importPath, typeName)
	if err != nil {
		return nil, err
	}

	st, err := structUnder(named)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not a struct", ErrBindings, shortType(named))
	}

	f, ok := fieldByName(st, field)
	if !ok {
		return nil, fmt.Errorf("%w: %s has no field %q%s",
			ErrBindings, shortType(named), field, didYouMean(field, fieldNames(st)))
	}

	return f.Type(), nil
}

// structUnder unwraps pointers and named types to reach a struct.
func structUnder(t types.Type) (*types.Struct, error) {
	for {
		switch u := t.(type) {
		case *types.Pointer:
			t = u.Elem()
		case *types.Named:
			t = u.Underlying()
		case *types.Struct:
			return u, nil
		default:
			return nil, fmt.Errorf("%w: %s is not a struct", ErrBindings, shortType(t))
		}
	}
}

func fieldByName(st *types.Struct, name string) (*types.Var, bool) {
	for i := range st.NumFields() {
		if f := st.Field(i); f.Name() == name {
			return f, true
		}
	}
	return nil, false
}

// shortType renders a type with package names rather than full import paths, so
// messages read the way the code does.
func shortType(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}

func typeNames(pkg *packages.Package) []string {
	var out []string
	for _, n := range pkg.Types.Scope().Names() {
		if obj := pkg.Types.Scope().Lookup(n); obj != nil && obj.Exported() {
			if _, ok := obj.Type().(*types.Named); ok {
				out = append(out, n)
			}
		}
	}
	return out
}

func methodNames(ms *types.MethodSet) []string {
	out := make([]string, 0, ms.Len())
	for i := range ms.Len() {
		if obj := ms.At(i).Obj(); obj.Exported() {
			out = append(out, obj.Name())
		}
	}
	return out
}

func fieldNames(st *types.Struct) []string {
	out := make([]string, 0, st.NumFields())
	for i := range st.NumFields() {
		if f := st.Field(i); f.Exported() {
			out = append(out, f.Name())
		}
	}
	return out
}

// didYouMean suggests near matches for a name that was not found.
//
// It exists because the realistic failure is a plausible-but-wrong name -- a
// casing difference, a version suffix, a singular where the SDK is plural -- and
// the useful reply is the real spelling rather than only that the guess failed.
func didYouMean(want string, have []string) string {
	if len(have) == 0 {
		return ""
	}

	var close []string
	lower := strings.ToLower(want)

	for _, h := range have {
		hl := strings.ToLower(h)
		if hl == lower || strings.Contains(hl, lower) || strings.Contains(lower, hl) {
			close = append(close, h)
		}
	}

	sort.Strings(close)

	switch {
	case len(close) == 0:
		// With no near match, a bounded sample of what does exist is still more
		// use than nothing.
		sort.Strings(have)
		if len(have) > 8 {
			have = have[:8]
			return fmt.Sprintf(" (available include: %s, ...)", strings.Join(have, ", "))
		}
		return fmt.Sprintf(" (available: %s)", strings.Join(have, ", "))
	case len(close) > 6:
		close = close[:6]
		return fmt.Sprintf(" (did you mean: %s, ...?)", strings.Join(close, ", "))
	default:
		return fmt.Sprintf(" (did you mean: %s?)", strings.Join(close, ", "))
	}
}
