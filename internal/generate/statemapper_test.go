package generate

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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

// TestUnit_Generate_TheStateMapperHasOneCallSite is the property phase 5.5 exists to establish.
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
func TestUnit_Generate_TheStateMapperHasOneCallSite(t *testing.T) {
	t.Parallel()

	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	bp := pilotBlueprint(t)

	plan, err := g.Build(bp, BuildOptions{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Per generated package: each resource and data source declares its own mapper, so the
	// property is one call site within a package rather than across the provider.
	type packageSites struct {
		lifecycle []string
		list      []string
	}
	byPackage := map[string]*packageSites{}

	for _, f := range plan.Files {
		if !strings.HasSuffix(f.Path, ".go") {
			continue
		}

		dir := filepath.Dir(f.Path)
		if byPackage[dir] == nil {
			byPackage[dir] = &packageSites{}
		}
		sites := callsTo(t, f.Path, f.Content, stateMapperName)
		if filepath.Base(f.Path) == "list_resource.go" {
			byPackage[dir].list = append(byPackage[dir].list, sites...)
		} else {
			byPackage[dir].lifecycle = append(byPackage[dir].lifecycle, sites...)
		}
	}

	var checked int

	for dir, sites := range byPackage {
		if len(sites.lifecycle) == 0 && len(sites.list) == 0 {
			// A package with no mapper at all: the provider registry files.
			continue
		}

		checked++

		if len(sites.lifecycle) != 1 {
			t.Errorf("%s calls %s %d times in its lifecycle, want exactly one:\n  %s",
				dir, stateMapperName, len(sites.lifecycle),
				strings.Join(sites.lifecycle, "\n  "))
		}

		// The one sanctioned second caller: a list facet serving --include-resource maps
		// the element the collection read already returned. It cannot delegate through
		// readAfterWrite -- that would cost a request per element, the template's own
		// stated non-goal -- and it maps the same read response shape the lifecycle does,
		// so the property the single site exists for still holds.
		if len(sites.list) > 1 {
			t.Errorf("%s calls %s %d times in list_resource.go, want at most one:\n  %s",
				dir, stateMapperName, len(sites.list), strings.Join(sites.list, "\n  "))
		}
	}

	// Guard against the assertion passing because nothing was generated.
	if checked == 0 {
		t.Fatal("no generated package calls the state mapper, so this test proves nothing")
	}
}

// TestUnit_Generate_CreateAndUpdateDoNotMapTheirOwnResponse is the same property stated the
// other way round, and it is the one that would catch a regression.
//
// One call site could be satisfied by Create mapping the create response and Read doing
// nothing. What must hold is that the write operations delegate.
func TestUnit_Generate_CreateAndUpdateDoNotMapTheirOwnResponse(t *testing.T) {
	t.Parallel()

	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plan, err := g.Build(pilotBlueprint(t), BuildOptions{BlueprintPath: "blueprints/thousandeyes"})
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

// TestUnit_Generate_AListFacetEmitsItsFileAndRegistration is the end-to-end property of phase
// 5.6: a resource declaring a list facet makes the provider serve it, with no hand-written
// change anywhere.
func TestUnit_Generate_AListFacetEmitsItsFileAndRegistration(t *testing.T) {
	t.Parallel()

	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	bp := pilotBlueprint(t)

	var listed *string
	for i := range bp.Resources {
		if bp.Resources[i].List != nil {
			listed = &bp.Resources[i].Key
		}
	}
	if listed == nil {
		t.Skip("the committed pilot blueprint declares no list facet")
	}

	plan, err := g.Build(bp, BuildOptions{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	files := map[string]string{}
	for _, f := range plan.Files {
		files[f.Path] = string(f.Content)
	}

	// The list resource lives beside the resource it lists, because it is a facet of it.
	var listFile string
	for path, content := range files {
		if strings.HasSuffix(path, "resources/tags/v7/tag/list_resource.go") {
			listFile = content
		}
	}
	if listFile == "" {
		t.Fatal("no list_resource.go was emitted for a resource with a list facet")
	}

	// The type name is read from the same constant the resource uses. That equality is the
	// entire linkage between the two -- the framework errors from GetMetadata if they
	// differ -- so it is asserted rather than left to inspection.
	if !strings.Contains(listFile, "resp.TypeName = TypeName") {
		t.Error("the list resource should take its type name from TypeName")
	}

	// Metadata and Configure take resource.* request types, not list.*. The framework
	// declares them that way so one implementation can satisfy both interfaces, and
	// reaching for a list-specific type is the trap the shape invites.
	for _, want := range []string{
		"Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse)",
		"Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse)",
	} {
		if !strings.Contains(listFile, want) {
			t.Errorf("list_resource.go should declare %q", want)
		}
	}
	for _, bad := range []string{"list.MetadataRequest", "list.ConfigureRequest"} {
		if strings.Contains(listFile, bad) {
			t.Errorf("list_resource.go must not use %s: the framework uses the resource types", bad)
		}
	}

	// The identity is shared with the resource rather than redeclared, so the two cannot
	// disagree about its shape.
	if !strings.Contains(listFile, "var identity TagResourceIdentity") {
		t.Error("the list resource should use the resource's own identity type")
	}

	// And the registration file is what makes the provider satisfy
	// ProviderWithListResources.
	var registry string
	for path, content := range files {
		if strings.HasSuffix(path, "provider/list_resources.go") {
			registry = content
		}
	}
	if registry == "" {
		t.Fatal("no list_resources.go registration was emitted")
	}
	if !strings.Contains(registry, "func (p *Provider) ListResources(") {
		t.Error("the registration should declare ListResources")
	}
	if !strings.Contains(registry, ".NewTagListResource") {
		t.Errorf("the registration should reference the constructor:\n%s", registry)
	}
}

// TestUnit_Generate_AScaffoldIsWrittenOnceAndThenKept is the escape hatch's defining property.
//
// A scaffold is not "a file the generator has not overwritten yet" -- it is one it will never
// overwrite. Without that, a practitioner's ModifyPlan would be replaced by the stub on the next
// emit, or worse, the run would fail with the overwrite refusal and there would be no way to
// keep the file at all.
func TestUnit_Generate_AScaffoldIsWrittenOnceAndThenKept(t *testing.T) {
	t.Parallel()

	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	bp := pilotBlueprint(t)

	plan, err := g.Build(bp, BuildOptions{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var scaffolds []string
	for _, f := range plan.Files {
		if f.Scaffold {
			scaffolds = append(scaffolds, f.Path)
		}
	}
	if len(scaffolds) == 0 {
		t.Skip("the committed pilot blueprint declares no hooks")
	}

	root := t.TempDir()

	// First write: everything lands, nothing is kept.
	first, err := Write(plan, WriteOptions{Root: root})
	if err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if len(first.Kept) != 0 {
		t.Errorf("nothing exists yet, so nothing should be kept: %v", first.Kept)
	}

	// Edit one, as a practitioner would.
	target := filepath.Join(root, scaffolds[0])

	edited := []byte("package tag\n\n// mine\n")
	if err := os.WriteFile(target, edited, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Second write: the edit survives, and the run says so rather than staying silent.
	second, err := Write(plan, WriteOptions{Root: root})
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}

	got, err := os.ReadFile(target) //nolint:gosec // the path is a test temp dir
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, edited) {
		t.Errorf("the edit was overwritten:\n%s", got)
	}

	var reported bool
	for _, p := range second.Kept {
		if p == scaffolds[0] {
			reported = true
		}
	}
	if !reported {
		t.Errorf("%s should be reported as kept, got %v", scaffolds[0], second.Kept)
	}
	for _, p := range second.Written {
		if p == scaffolds[0] {
			t.Errorf("%s was rewritten", p)
		}
	}
}

// TestUnit_Generate_AScaffoldCarriesNoGeneratedMarker.
//
// The marker is what the drift check and the overwrite refusal both key on. A scaffold carrying
// one would be policed as generated code, which is the opposite of the point -- and the
// manifest excluding it is only half the job if the file still announces itself as generated.
func TestUnit_Generate_AScaffoldCarriesNoGeneratedMarker(t *testing.T) {
	t.Parallel()

	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plan, err := g.Build(pilotBlueprint(t), BuildOptions{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var checked int

	for _, f := range plan.Files {
		if !f.Scaffold {
			// And the converse: everything the generator owns must carry it.
			if !bytes.Contains(f.Content, []byte(generatedMarker)) {
				t.Errorf("%s is generated but carries no marker", f.Path)
			}

			continue
		}

		checked++

		if bytes.Contains(f.Content, []byte(generatedMarker)) {
			t.Errorf("%s is a scaffold and must not carry the generated marker", f.Path)
		}
		if strings.HasPrefix(f.Path, "examples/") {
			// Documentation payload: tfplugindocs copies these verbatim into the
			// rendered pages, so an ownership note here would leak into every
			// Example Usage block. The write-once behaviour still holds; the note
			// lives on the mechanism instead of in the user-facing file.
			continue
		}
		if !bytes.Contains(f.Content, []byte("This file is yours")) {
			t.Errorf("%s should say plainly that it is not regenerated", f.Path)
		}
	}

	if checked == 0 {
		t.Skip("the committed pilot blueprint declares no hooks")
	}
}
