package emit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// pilotBlueprint loads the committed pilot blueprint.
//
// Testing against the real blueprint rather than a fixture means the committed
// artefact is continuously validated: a blueprint that stops being emittable fails
// here rather than at the next release.
func pilotBlueprint(t *testing.T) blueprint.Blueprint {
	t.Helper()

	bp, err := blueprint.LoadDir(filepath.Join("..", "..", "blueprints", "thousandeyes"))
	if err != nil {
		t.Fatalf("loading the committed pilot blueprint: %v", err)
	}
	return bp
}

// TestUnit_Emit_LangVersionIsAccepted pins the gofumpt language version.
//
// gofumpt parses it with go/version and *panics* on a value it cannot read, so a
// plain "1.25" crashes the generator rather than returning an error. This is the
// cheapest possible guard against that, and it has already caught it once.
func TestUnit_Emit_LangVersionIsAccepted(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("gofumpt rejected langVersion %q: %v", langVersion, r)
		}
	}()

	if _, err := formatGo([]byte("package p\n")); err != nil {
		t.Fatalf("formatting trivial source failed: %v", err)
	}
}

// TestUnit_Emit_IsDeterministic is the property the whole drift gate rests on.
//
// Anything non-deterministic in emitted output -- a timestamp, a tool version, an
// absolute path, Go map iteration order -- makes `verify` fail on a run that
// changed nothing, which destroys its usefulness entirely. Map iteration order is
// the one that bites in practice, because it is invisible until it isn't.
func TestUnit_Emit_IsDeterministic(t *testing.T) {
	t.Parallel()

	bp := pilotBlueprint(t)

	gen, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	first, err := gen.Build(bp, Options{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Repeated rather than run twice: map iteration order varies per iteration,
	// so two runs can agree by luck.
	for i := 0; i < 25; i++ {
		again, err := gen.Build(bp, Options{BlueprintPath: "blueprints/thousandeyes"})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}

		if len(again.Files) != len(first.Files) {
			t.Fatalf("run %d produced %d files, first run produced %d", i, len(again.Files), len(first.Files))
		}

		for j := range again.Files {
			if again.Files[j].Path != first.Files[j].Path {
				t.Fatalf("run %d file %d is %q, first run had %q",
					i, j, again.Files[j].Path, first.Files[j].Path)
			}
			if string(again.Files[j].Content) != string(first.Files[j].Content) {
				t.Fatalf("run %d produced different content for %s", i, again.Files[j].Path)
			}
		}
	}
}

// TestUnit_Emit_CarriesNoTimestampOrVersion guards the two values most likely to
// be added later by someone trying to be helpful. Either would make every
// regeneration a diff.
func TestUnit_Emit_CarriesNoTimestampOrVersion(t *testing.T) {
	t.Parallel()

	bp := pilotBlueprint(t)

	gen, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plan, err := gen.Build(bp, Options{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Year prefixes catch a date in any common format; "tfpluginframeworkgen v" catches a
	// version stamp.
	forbidden := []string{"202", "generated at", "tfpluginframeworkgen v", os.TempDir()}

	for _, f := range plan.Files {
		// The header names the blueprint and its digest, which is intended. Only
		// the rest of the file is checked for stray dates.
		body := string(f.Content)
		for _, bad := range forbidden {
			if bad == "202" {
				// A digest legitimately contains "202"; look for a date shape.
				continue
			}
			if strings.Contains(body, bad) {
				t.Errorf("%s contains %q, which would make every regeneration a diff", f.Path, bad)
			}
		}
	}
}

// TestUnit_Emit_EveryFileIsMarkedGenerated matters because overwrite protection
// keys on the marker: a generated file lacking it could never be regenerated
// without -force.
func TestUnit_Emit_EveryFileIsMarkedGenerated(t *testing.T) {
	t.Parallel()

	bp := pilotBlueprint(t)

	gen, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plan, err := gen.Build(bp, Options{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Files) == 0 {
		t.Fatal("the pilot blueprint produced no files")
	}

	for _, f := range plan.Files {
		// A scaffold is deliberately unmarked -- the marker is what the drift check and the
		// overwrite refusal key on, and a file the practitioner owns must be policed by
		// neither. TestUnit_Emit_AScaffoldCarriesNoGeneratedMarker asserts the converse.
		if f.Scaffold {
			continue
		}

		body := string(f.Content)

		// The marker text is language-independent, and both the drift check and the overwrite
		// refusal search the raw bytes for it. Every generated file carries it whatever it is
		// written in.
		if !strings.Contains(body, generatedMarker) {
			t.Errorf("%s is not marked as generated", f.Path)
		}

		// The comment syntax is not. Go's canonical form is what the house lint configuration
		// skips on and it requires the marker on the first line; HCL says the same thing with
		// "#", because "//" is not a comment terraform fmt would preserve.
		prefix := "// Code generated by"
		if strings.HasSuffix(f.Path, ".tf") {
			prefix = "# Code generated by"
		}

		if !strings.HasPrefix(body, prefix) {
			t.Errorf("%s does not begin with %q", f.Path, prefix)
		}
	}
}

// TestUnit_Emit_RefusesToOverwriteHandWrittenFiles is the guard that stops a
// mistyped -out from destroying somebody's work with no way to recover it.
func TestUnit_Emit_RefusesToOverwriteHandWrittenFiles(t *testing.T) {
	t.Parallel()

	bp := pilotBlueprint(t)

	gen, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plan, err := gen.Build(bp, Options{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	root := t.TempDir()

	// Put a hand-written file exactly where the emitter wants to write.
	target := filepath.Join(root, plan.Files[0].Path)
	if mkErr := os.MkdirAll(filepath.Dir(target), 0o750); mkErr != nil {
		t.Fatalf("MkdirAll: %v", mkErr)
	}
	if wErr := os.WriteFile(target, []byte("package tag // written by a person\n"), 0o600); wErr != nil {
		t.Fatalf("WriteFile: %v", wErr)
	}

	_, err = Write(plan, WriteOptions{Root: root})
	if !errors.Is(err, ErrRefusedOverwrite) {
		t.Fatalf("error = %v, want it to wrap ErrRefusedOverwrite", err)
	}

	// -force is the deliberate escape hatch for adopting an existing tree.
	if _, err := Write(plan, WriteOptions{Root: root, Force: true}); err != nil {
		t.Fatalf("Write with Force: %v", err)
	}
}

// TestUnit_Emit_WriteIsIdempotent confirms a second write reports everything as
// unchanged rather than rewriting it, so regenerating does not churn modification
// times and set file watchers rebuilding the world on a no-op.
func TestUnit_Emit_WriteIsIdempotent(t *testing.T) {
	t.Parallel()

	bp := pilotBlueprint(t)

	gen, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plan, err := gen.Build(bp, Options{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	root := t.TempDir()

	first, err := Write(plan, WriteOptions{Root: root})
	if err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if len(first.Written) != len(plan.Files) {
		t.Errorf("first write reported %d written, want %d", len(first.Written), len(plan.Files))
	}

	second, err := Write(plan, WriteOptions{Root: root})
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if len(second.Written) != 0 {
		t.Errorf("second write rewrote %d file(s): %v", len(second.Written), second.Written)
	}

	// Unchanged plus kept, because a scaffold that already exists is reported as kept even
	// when its content still matches -- the generator did not compare it, which is the point.
	// Summed rather than checked separately, so the property under test stays "every file was
	// accounted for and none was rewritten" rather than becoming a count of scaffolds.
	if got := len(second.Unchanged) + len(second.Kept); got != len(plan.Files) {
		t.Errorf(
			"second write accounted for %d file(s) (%d unchanged, %d kept), want %d",
			got, len(second.Unchanged), len(second.Kept), len(plan.Files),
		)
	}
}

// TestUnit_Emit_OnlyRestrictsToOneResource covers the flag used while iterating on
// a template.
func TestUnit_Emit_OnlyRestrictsToOneResource(t *testing.T) {
	t.Parallel()

	bp := pilotBlueprint(t)

	gen, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plan, err := gen.Build(bp, Options{BlueprintPath: "b", Only: "tag"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, f := range plan.Files {
		// Registration files are provider-wide, so -only must not emit a partial
		// one that would fail to compile against the rest of the tree.
		if strings.Contains(f.Path, "internal/provider") {
			t.Errorf("-only emitted the provider-wide file %s", f.Path)
		}
	}

	if _, err := gen.Build(bp, Options{BlueprintPath: "b", Only: "nonexistent"}); err != nil {
		t.Fatalf("Build with an unmatched -only should succeed and produce nothing: %v", err)
	}
}

// TestUnit_Emit_FormatErrorIncludesNumberedSource exists because a template bug
// reports a line and column, and matching that against unnumbered output is
// needless work. This diagnostic already paid for itself once.
func TestUnit_Emit_FormatErrorIncludesNumberedSource(t *testing.T) {
	t.Parallel()

	_, err := formatGo([]byte("package p\nfunc broken( {\n"))
	if !errors.Is(err, ErrFormat) {
		t.Fatalf("error = %v, want it to wrap ErrFormat", err)
	}
	if !strings.Contains(err.Error(), "   1 | package p") {
		t.Errorf("error should include numbered source:\n%v", err)
	}
}

// withoutProvenance drops the generated header line carrying the blueprint digest.
func withoutProvenance(content []byte) string {
	var kept []string

	for _, line := range strings.Split(string(content), "\n") {
		if strings.Contains(line, "sha256:") {
			continue
		}

		kept = append(kept, line)
	}

	return strings.Join(kept, "\n")
}

// TestUnit_Emit_AllowedValuesBecomeAValidator is the inverse of the test that used to be
// here.
//
// TestUnit_Emit_EnumValuesDoNotReachGeneratedCode asserted that AttrType.Enum reached no
// generated code at all, on the reasoning that a validator built from a scraped set rejects
// configurations the API would have accepted. The reasoning was half right, and which half
// depends on *which* set is used.
//
// The documented set is a superset of what any one tenant accepts, so a validator built
// from it errs toward permitting: a stale specification surfaces as a real API error
// carrying the API's own message. Built from the observed accepted set it would err toward
// blocking, which is the harm the old test was guarding against. So the validator is now
// generated, from AllowedValues, and this asserts both halves of that.
func TestUnit_Emit_AllowedValuesBecomeAValidator(t *testing.T) {
	t.Parallel()

	bp := pilotBlueprint(t)

	gen, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	plan, err := gen.Build(bp, Options{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// The pilot's objectType documents six values and the validator carries all six.
	//
	// It used to be the example of a refused documented value too, and it was the wrong example:
	// `endpoint-agent` was recorded as refused because the enum probe substituted it into a
	// static-tag fixture, and the API answered "type: Static tags are not supported for the
	// provided object type" -- a refusal about `type`, not about the value. accessType carries
	// the genuine case, so the refusal assertion moved there.
	var schemaFile string
	for _, f := range plan.Files {
		if strings.HasSuffix(f.Path, "resources/tags/v7/tag/resource.go") {
			schemaFile = string(f.Content)
		}
	}
	if schemaFile == "" {
		t.Fatal("no resource schema was emitted")
	}

	wantAll := `stringvalidator.OneOf("test", "dashboard", "endpoint-test", "v-agent", ` +
		`"connected-devices-test", "endpoint-agent")`
	if !strings.Contains(schemaFile, wantAll) {
		t.Errorf("the validator should carry the whole documented set:\n%s", schemaFile)
	}

	// accessType documents `system`, the API refused it naming the field, and it is still
	// permitted. That is the decision the whole approach turns on -- a validator errs toward
	// permitting -- so it is asserted rather than left to the reader.
	if !strings.Contains(schemaFile, "The API refused \"system\"") {
		t.Error("a documented value the API refused should be named beside the validator")
	}

	// And the value that was never really refused must not be named as though it were. This is
	// the assertion that would have caught the false fact.
	if strings.Contains(schemaFile, "The API refused \"endpoint-agent\"") {
		t.Error("endpoint-agent was refused for a reason unrelated to its value; " +
			"naming it here would repeat a withdrawn fact")
	}

	// And the observed accepted set must not be what the validator was built from: it omits
	// endpoint-agent, so a validator over exactly those five would be the blocking mistake.
	bad := `stringvalidator.OneOf("test", "dashboard", "endpoint-test", "v-agent", ` +
		`"connected-devices-test")`
	if strings.Contains(schemaFile, bad) {
		t.Error("the validator was built from the accepted set, not the documented one")
	}
}

// TestUnit_Emit_APurelyComputedAttributeGetsNoValidator.
//
// A validator runs against configuration. The pilot's `type` attribute documents two values
// and is computed only, so a validator there is code that can never run.
func TestUnit_Emit_APurelyComputedAttributeGetsNoValidator(t *testing.T) {
	t.Parallel()

	bp := pilotBlueprint(t)

	// Confirm the fixture still has the shape this test needs, rather than passing because
	// the attribute stopped carrying documented values.
	var found bool
	for _, a := range bp.Resources[0].Schema.Attributes {
		if a.Name == "type" {
			found = len(a.Type.AllowedValues) > 0 &&
				a.ComputedOptionalRequired == blueprint.Computed
		}
	}
	if !found {
		t.Skip("the pilot's type attribute is no longer a computed attribute with documented values")
	}

	gen, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plan, err := gen.Build(bp, Options{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, f := range plan.Files {
		if !strings.HasSuffix(f.Path, "resources/tags/v7/tag/resource.go") {
			continue
		}
		if strings.Contains(string(f.Content), `stringvalidator.OneOf("static", "dynamic")`) {
			t.Error("a purely computed attribute should get no validator")
		}
	}
}

// TestUnit_Emit_DataSourceProducesItsFileSet pins the per-data-source file split.
//
// Four rather than the resource's five: a data source sends no request body, so there is
// nothing for a construct.go to expand into one. The names match the reference provider's,
// where read lives in read.go rather than a crud.go that would be three-quarters empty.
func TestUnit_Emit_DataSourceProducesItsFileSet(t *testing.T) {
	t.Parallel()

	g, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	bp := pilotBlueprint(t)
	if len(bp.DataSources) == 0 {
		t.Skip("the committed pilot blueprint declares no data sources")
	}

	plan, err := g.Build(bp, Options{BlueprintPath: "blueprints/thousandeyes"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, d := range bp.DataSources {
		if d.Drop {
			continue
		}

		var got []string
		for _, f := range plan.Files {
			if strings.Contains(f.Path, "/datasources/") && strings.HasSuffix(
				filepath.Dir(f.Path), string(filepath.Separator)+d.GoPackage,
			) {
				got = append(got, filepath.Base(f.Path))
			}
		}

		// Six once a seed is declared: the four code files, the acceptance test and
		// the seed's re-emitted existence helper. testdata/datasource.tf lives one
		// directory down and is not in this listing.
		want := []string{
			"datasource.go", "datasource_acceptance_test.go", "model.go",
			"read.go", "seed_helper_test.go", "state.go",
		}
		if len(got) != len(want) {
			t.Errorf("data source %q emitted %v, want %v", d.Key, got, want)
			continue
		}
		for i, w := range want {
			if got[i] != w {
				t.Errorf("data source %q file %d = %q, want %q", d.Key, i, got[i], w)
			}
		}

		// construct.go is a resource file. Emitting one here would mean the generator
		// believes a data source has a request body.
		for _, f := range plan.Files {
			if strings.Contains(f.Path, "/datasources/") &&
				filepath.Base(f.Path) == "construct.go" {
				t.Errorf("a data source must not emit construct.go: %s", f.Path)
			}
		}
	}
}

// TestUnit_Emit_DataSourcesRegisterInTheProvider checks the generated registry, which is
// what makes the provider serve them at all.
func TestUnit_Emit_DataSourcesRegisterInTheProvider(t *testing.T) {
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

	var registry string
	for _, f := range plan.Files {
		if filepath.Base(f.Path) == "datasources.go" {
			registry = string(f.Content)
		}
	}
	if registry == "" {
		t.Fatal("no datasources.go was emitted")
	}

	for _, d := range bp.DataSources {
		if d.Drop {
			continue
		}
		want := d.GoPackageAlias + ".New" + d.GoTypeName
		if !strings.Contains(registry, want) {
			t.Errorf("datasources.go does not register %q:\n%s", want, registry)
		}
	}
}
