package emit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// stateMapperName is the function every generated resource maps its API response through.
const stateMapperName = "mapRemoteStateToTerraform"

// callsTo counts calls to a named function in one file's source, and records where.
//
// Parsed rather than grepped, following internal/probe/source_test.go: a string search
// counts the definition, the doc comments that name it, and a call inside a string
// literal, none of which are call sites. The distinction is the whole assertion here.
func callsTo(t *testing.T, path string, src []byte, name string) []string {
	t.Helper()

	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	var at []string

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == name {
			at = append(at, fset.Position(call.Pos()).String())
		}

		return true
	})

	return at
}

// TestUnit_Emit_TheStateMapperHasOneCallSite is the property phase 5.5 exists to establish.
//
// Before it, Create mapped the *create* response, Read mapped the *read* response and
// Update mapped the *update* response: three call sites for one function, so any change to
// state mapping had to be made in three places that then had to agree.
//
// Worse, the pilot's own probe evidence says those responses are not interchangeable.
// assignments is expansion-gated, so a create response never carries it and mapping that
// response writes it null -- then the next read fills it in and the practitioner gets a
// diff no configuration change resolves. So this is not a tidiness property: the two extra
// call sites were mapping the wrong thing.
//
// Create and Update now delegate to readAfterWrite, which retries readState, which is the
// one caller.
func TestUnit_Emit_TheStateMapperHasOneCallSite(t *testing.T) {
	t.Parallel()

	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	bp := pilotBlueprint(t)

	plan, err := g.Build(bp, Options{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Per generated package: each resource and data source declares its own mapper, so the
	// property is one call site within a package rather than across the provider.
	byPackage := map[string][]string{}

	for _, f := range plan.Files {
		if !strings.HasSuffix(f.Path, ".go") {
			continue
		}

		dir := filepath.Dir(f.Path)
		byPackage[dir] = append(byPackage[dir], callsTo(t, f.Path, f.Content, stateMapperName)...)
	}

	var checked int

	for dir, sites := range byPackage {
		if len(sites) == 0 {
			// A package with no mapper at all: the provider registry files.
			continue
		}

		checked++

		if len(sites) != 1 {
			t.Errorf("%s calls %s %d times, want exactly one:\n  %s",
				dir, stateMapperName, len(sites), strings.Join(sites, "\n  "))
		}
	}

	// Guard against the assertion passing because nothing was generated.
	if checked == 0 {
		t.Fatal("no generated package calls the state mapper, so this test proves nothing")
	}
}

// TestUnit_Emit_CreateAndUpdateDoNotMapTheirOwnResponse is the same property stated the
// other way round, and it is the one that would catch a regression.
//
// One call site could be satisfied by Create mapping the create response and Read doing
// nothing. What must hold is that the write operations delegate.
func TestUnit_Emit_CreateAndUpdateDoNotMapTheirOwnResponse(t *testing.T) {
	t.Parallel()

	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plan, err := g.Build(pilotBlueprint(t), Options{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var crud string
	for _, f := range plan.Files {
		if strings.HasSuffix(f.Path, "resources/tags/v7/tag/crud.go") {
			crud = string(f.Content)
		}
	}
	if crud == "" {
		t.Fatal("no crud.go was emitted")
	}

	// Both write paths go through the retrying read.
	if got := strings.Count(crud, "r.readAfterWrite(ctx,"); got != 2 {
		t.Errorf("got %d calls to readAfterWrite, want one from Create and one from Update", got)
	}

	// And neither maps a write response. `created` is still bound, because the identifier
	// comes from it; `updated` must not be, or the file would not compile.
	for _, bad := range []string{
		"mapRemoteStateToTerraform(ctx, &plan, created)",
		"mapRemoteStateToTerraform(ctx, &plan, updated)",
		"updated, _, err :=",
	} {
		if strings.Contains(crud, bad) {
			t.Errorf("crud.go still contains %q", bad)
		}
	}
}
