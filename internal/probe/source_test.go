package probe

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This file holds the machinery for the structural tests: assertions about the shape of
// the source rather than about behaviour.
//
// They exist because two of this package's guarantees are properties of how the code is
// written, not of what it computes. "Every probe documents how it can be wrong" cannot be
// checked at runtime. Neither can "only session.go builds an HTTP client", which is what
// keeps the mutation choke point a choke point as the package grows -- and which no
// behavioural test would notice being broken.

// probeTypeNames maps a probe's registered name to its Go type name, by reflection over
// the registered values.
//
// Reflection here rather than a hand-written table, because a table would go stale the
// first time somebody adds a probe and does not update it -- which is the exact failure
// the tests using this are meant to catch.
func probeTypeNames() map[string]string {
	out := map[string]string{}

	for _, p := range readProbes {
		out[p.Name()] = reflect.TypeOf(p).Name()
	}
	for _, p := range mutatingProbes {
		out[p.Name()] = reflect.TypeOf(p).Name()
	}

	return out
}

func typeNameFor(_ *ast.File, probeName string) (string, bool) {
	name, ok := probeTypeNames()[probeName]
	return name, ok
}

// catalogueSource parses catalogue.go.
func catalogueSource(t *testing.T) *ast.File {
	t.Helper()

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "catalogue.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing catalogue.go: %v", err)
	}

	return file
}

// docCommentBefore returns the doc comment attached to a type declaration.
//
// The decl argument is the rendered form the caller is looking for, e.g.
// "type listShape struct{}" -- matched on the type name parsed out of it, so the caller
// does not have to care how gofumpt formatted the declaration.
func docCommentBefore(file *ast.File, decl string) string {
	want := typeNameIn(decl)
	if want == "" {
		return ""
	}

	for _, d := range file.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}

		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != want {
				continue
			}

			// A single-spec declaration carries its comment on the GenDecl; a grouped
			// one carries it on the TypeSpec. Both forms appear in real code, and
			// checking only one would silently pass a probe with no comment at all.
			if gen.Doc != nil {
				return gen.Doc.Text()
			}
			if ts.Doc != nil {
				return ts.Doc.Text()
			}
			return ""
		}
	}

	return ""
}

// typeNameIn pulls the identifier out of "type NAME struct{}".
func typeNameIn(decl string) string {
	rest, ok := strings.CutPrefix(decl, "type ")
	if !ok {
		return ""
	}

	name, _, _ := strings.Cut(rest, " ")

	return name
}

// TestUnit_Probe_OnlyTheSessionBuildsAnHTTPClient is what keeps the choke point a choke
// point.
//
// Every request has to go through a Session, because that is where the budget, the ledger
// and the read-only guarantee are enforced. A probe that built its own http.Client would
// bypass all three, and would do so while passing every behavioural test in the package --
// the facts would still be right, the budget would simply stop meaning anything and a
// created object would go unrecorded.
//
// So this is checked structurally. session.go may construct a client; nothing else may.
func TestUnit_Probe_OnlyTheSessionBuildsAnHTTPClient(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()

	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		// Test files are exempt: a test that stands up an httptest server and points a
		// client at it is the normal way to test a transport.
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}

	// The identifiers that mean "I am about to make my own way onto the network".
	forbidden := map[string]string{
		"Client":           "http.Client",
		"Transport":        "http.Transport",
		"Get":              "http.Get",
		"Post":             "http.Post",
		"NewRequest":       "http.NewRequest",
		"DefaultTransport": "http.DefaultTransport",
		"DefaultClient":    "http.DefaultClient",
	}

	allowed := map[string]bool{
		"session.go": true,
		// The transport itself legitimately lives here once Phase 4.4 lands.
		"transport.go": true,
	}

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			base := filepath.Base(path)
			if allowed[base] {
				continue
			}

			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok || pkgIdent.Name != "http" {
					return true
				}
				if what, bad := forbidden[sel.Sel.Name]; bad {
					t.Errorf("%s uses %s; only session.go may reach the network, "+
						"or the budget and ledger stop being enforceable", base, what)
				}
				return true
			})
		}
	}
}
