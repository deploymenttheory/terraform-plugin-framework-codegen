package sdkbind

import (
	"errors"
	"fmt"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

var (
	// ErrLoad reports that the SDK could not be type-checked at all.
	ErrLoad = errors.New("cannot load the SDK")
	// ErrBindings reports one or more bindings that do not match the SDK.
	ErrBindings = errors.New("bindings do not match the SDK")
)

// loader type-checks the generated SDK once and answers symbol lookups
// from the result. One packages.Load call per run: the go list spawn
// dominates the cost, so everything under the SDK directory loads in a
// single invocation.
type loader struct {
	pkgs map[string]*packages.Package
}

// loadSDK type-checks every package under sdkDir. The directory must be
// resolvable by the go tool — a module of its own or a directory inside
// the provider module — because real type information, not a syntax scan,
// is what the checks here need.
func loadSDK(sdkDir string) (*loader, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedSyntax | packages.NeedImports | packages.NeedDeps,
		Dir: sdkDir,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("%w from %s: %w", ErrLoad, sdkDir, err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("%w: %s resolved to no packages", ErrLoad, sdkDir)
	}

	l := &loader{pkgs: map[string]*packages.Package{}}
	for _, pkg := range pkgs {
		if len(pkg.Errors) != 0 {
			// Reporting only the first is enough: they are nearly always
			// one cause, and the full list is noise.
			return nil, fmt.Errorf("%w: %s does not type-check: %v", ErrLoad, pkg.PkgPath, pkg.Errors[0])
		}
		if pkg.Types == nil || pkg.Types.Scope() == nil {
			return nil, fmt.Errorf("%w: %s carries no type information", ErrLoad, pkg.PkgPath)
		}
		l.pkgs[pkg.PkgPath] = pkg
		registerDependencies(l.pkgs, pkg)
	}
	return l, nil
}

// registerDependencies adds a package's transitive imports to the index.
//
// packages.Load answers only the roots, so without this the index holds the
// SDK's own packages alone. A generated SDK names types from its runtime in
// signatures — a multipart body, a serialisation primitive — and those
// resolve to no package, so any call taking one is refused as though the
// type did not exist.
//
// A dependency that failed to type-check is skipped rather than fatal: only
// the SDK's own packages are held to that, and one unrelated dependency
// must not stop the whole binding.
func registerDependencies(index map[string]*packages.Package, pkg *packages.Package) {
	for path, imported := range pkg.Imports {
		if _, seen := index[path]; seen {
			continue
		}
		if imported == nil || imported.Types == nil || imported.Types.Scope() == nil {
			continue
		}
		index[path] = imported
		registerDependencies(index, imported)
	}
}

// pkg answers one loaded package by import path.
func (l *loader) pkg(importPath string) (*packages.Package, error) {
	if p, ok := l.pkgs[importPath]; ok {
		return p, nil
	}
	have := make([]string, 0, len(l.pkgs))
	for p := range l.pkgs {
		have = append(have, p)
	}
	sort.Strings(have)
	return nil, fmt.Errorf("%w: the SDK has no package %s (loaded: %s)",
		ErrBindings, importPath, strings.Join(have, ", "))
}

// lookupType returns a named type declared in importPath.
func (l *loader) lookupType(importPath, name string) (*types.Named, error) {
	pkg, err := l.pkg(importPath)
	if err != nil {
		return nil, err
	}
	obj := pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return nil, fmt.Errorf("%w: package %s has no type %s%s",
			ErrBindings, pkg.Name, name, didYouMean(name, typeNames(pkg)))
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return nil, fmt.Errorf("%w: %s.%s is not a named type", ErrBindings, pkg.Name, name)
	}
	return named, nil
}

// functionExists reports whether a package declares a function by this
// name — how a minted enum's Parse companion is proven before a converter
// names it.
func (l *loader) functionExists(importPath, name string) bool {
	pkg, err := l.pkg(importPath)
	if err != nil {
		return false
	}
	obj := pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return false
	}
	_, isFunc := obj.Type().(*types.Signature)
	return isFunc
}

// methodOn resolves a method on t, following a pointer receiver for named
// types and working directly on interfaces — generated builders return
// interface types, whose method sets carry no pointer receivers.
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

func fieldNames(st *types.Struct) []string {
	out := make([]string, 0, st.NumFields())
	for i := range st.NumFields() {
		if f := st.Field(i); f.Exported() {
			out = append(out, f.Name())
		}
	}
	return out
}

// shortType renders a type with package names rather than full import
// paths, so messages and recorded type expressions read the way generated
// code does.
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

// didYouMean suggests near matches for a name that was not found. The
// realistic failure is a plausible-but-wrong spelling — a casing
// difference, a singular where the SDK is plural — and the useful reply
// is the real spelling rather than only that the guess failed.
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
		sort.Strings(have)
		if len(have) > 8 {
			return fmt.Sprintf(" (available include: %s, ...)", strings.Join(have[:8], ", "))
		}
		return fmt.Sprintf(" (available: %s)", strings.Join(have, ", "))
	case len(close) > 6:
		return fmt.Sprintf(" (did you mean: %s, ...?)", strings.Join(close[:6], ", "))
	default:
		return fmt.Sprintf(" (did you mean: %s?)", strings.Join(close, ", "))
	}
}

// typeFromExpr resolves a recorded type expression — "*sdk.Tag",
// "models.Tagable", "[]models.Tagable" — to the named type it spells,
// using the binding set's package qualifiers.
func (l *loader) typeFromExpr(info SDKInfo, expr string) (*types.Named, error) {
	named, _, err := l.typeAndPackageFromExpr(info, expr)
	return named, err
}

// typeAndPackageFromExpr resolves a type expression and answers the import
// path it was found under, so a caller that has to import the package can.
//
// Two package qualifiers are known by construction: models, and the SDK root
// everything else used to fall back to. That fallback was wrong for a whole
// class of type. A generator puts the model for an inline request body — one
// the document declares in place rather than naming under components — in the
// package of the operation that takes it, not in models: a body declared
// inline on POST /orgs/{org}/teams becomes orgs.ItemTeamsPostRequestBody.
// Looking those up in the SDK root failed with "package sdk has no type
// ItemTeamsPostRequestBodyable (available: APIClient)", and took the resource
// with it — every resource of a document that declares its bodies inline.
//
// An unknown qualifier is therefore resolved by searching the loaded
// packages for one of that name that declares the type. Exactly one match
// resolves; several is ambiguous and resolves to nothing, because picking
// between them would be a guess.
func (l *loader) typeAndPackageFromExpr(info SDKInfo, expr string) (*types.Named, string, error) {
	name := strings.TrimPrefix(strings.TrimPrefix(expr, "[]"), "*")
	importPath := info.ImportPath
	qualifier := ""
	if i := strings.LastIndex(name, "."); i >= 0 {
		qualifier, name = name[:i], name[i+1:]
		switch qualifier {
		case "models":
			importPath = info.ModelsImportPath
		case "", info.ImportPath:
		default:
			if found, ok := l.packageDeclaring(qualifier, name); ok {
				importPath = found
			}
		}
	}
	named, err := l.lookupType(importPath, name)
	return named, importPath, err
}

// packageDeclaring finds the one loaded package named qualifier that
// declares name. Deterministic: the answer does not depend on map order,
// because more than one match is refused rather than chosen between.
func (l *loader) packageDeclaring(qualifier, name string) (string, bool) {
	found := ""
	for path, pkg := range l.pkgs {
		if pkg.Types == nil || pkg.Types.Name() != qualifier {
			continue
		}
		if pkg.Types.Scope().Lookup(name) == nil {
			continue
		}
		if found != "" {
			return "", false
		}
		found = path
	}
	return found, found != ""
}
