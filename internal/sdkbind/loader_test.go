package sdkbind

import (
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestUnit_RegisterDependencies_IndexesTheWholeImportGraph proves an SDK's
// transitive imports reach the index. packages.Load answers only the roots,
// so without this a type the SDK names from its runtime — a multipart body,
// a serialisation primitive — resolves to no package and every call taking
// one is refused.
func TestUnit_RegisterDependencies_IndexesTheWholeImportGraph(t *testing.T) {
	goPackage := func(path, name string) *packages.Package {
		return &packages.Package{PkgPath: path, Types: types.NewPackage(path, name)}
	}

	deep := goPackage("example.com/deep", "deep")
	runtime := goPackage("example.com/runtime", "runtime")
	runtime.Imports = map[string]*packages.Package{deep.PkgPath: deep}
	root := goPackage("example.com/sdk", "sdk")
	root.Imports = map[string]*packages.Package{runtime.PkgPath: runtime}

	index := map[string]*packages.Package{root.PkgPath: root}
	registerDependencies(index, root)

	for _, want := range []string{"example.com/sdk", "example.com/runtime", "example.com/deep"} {
		if _, ok := index[want]; !ok {
			t.Errorf("%s is not in the index", want)
		}
	}
}

// TestUnit_RegisterDependencies_SkipsUntypedAndSurvivesCycles proves a
// dependency carrying no type information is skipped rather than fatal, and
// that an import cycle terminates.
func TestUnit_RegisterDependencies_SkipsUntypedAndSurvivesCycles(t *testing.T) {
	untyped := &packages.Package{PkgPath: "example.com/untyped"}
	a := &packages.Package{PkgPath: "example.com/a", Types: types.NewPackage("example.com/a", "a")}
	b := &packages.Package{PkgPath: "example.com/b", Types: types.NewPackage("example.com/b", "b")}
	a.Imports = map[string]*packages.Package{b.PkgPath: b, untyped.PkgPath: untyped, "nil": nil}
	b.Imports = map[string]*packages.Package{a.PkgPath: a}

	index := map[string]*packages.Package{a.PkgPath: a}
	registerDependencies(index, a) // must terminate despite a <-> b

	if _, ok := index["example.com/b"]; !ok {
		t.Error("a typed dependency must be indexed")
	}
	if _, ok := index["example.com/untyped"]; ok {
		t.Error("a dependency with no type information must be skipped")
	}
	if _, ok := index["nil"]; ok {
		t.Error("a nil dependency must be skipped")
	}
}
