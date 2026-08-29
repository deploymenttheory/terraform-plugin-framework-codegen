package sdkgen

// Edge coverage for the failure paths the happy-path tests never walk: the
// swap's restore, the empty-tree refusal, an unreadable manifest, and the
// backends' own error surfaces.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
)

func TestUnit_Swap_InstallsWhereNothingStoodBefore(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(staging, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "a.go"), []byte("package sdk\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := swap(staging, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "a.go")); err != nil {
		t.Errorf("the staged tree did not land: %v", err)
	}
}

func TestUnit_Swap_RestoresThePreviousTreeWhenTheInstallFails(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "previous.go"), []byte("package sdk\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A staging path that does not exist makes the install rename fail after
	// the previous tree was parked.
	err := swap(filepath.Join(dir, "no-such-staging"), out)
	if err == nil {
		t.Fatal("a failed install must report")
	}
	if _, statErr := os.Stat(filepath.Join(out, "previous.go")); statErr != nil {
		t.Errorf("the previous tree was not restored: %v", statErr)
	}
}

func TestUnit_Inventory_RefusesATreeWithNoFiles(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()

	_, err := inventory(staging, root, filepath.Join(root, "internal", "sdk"), "spec/revised.yaml")
	if err == nil || !strings.Contains(err.Error(), "no files") {
		t.Fatalf("an empty tree should refuse — success with nothing generated is the failure mode, got %v", err)
	}
}

func TestUnit_Run_RefusesAManifestItCannotRead(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	root := repoRoot(t)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"),
		[]byte(`{"formatVersion": "999", "toolVersion": "dev", "files": []}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Run(context.Background(), runOptions(root))
	if err == nil || !strings.Contains(err.Error(), "format version") {
		t.Fatalf("an unreadable manifest must refuse rather than be overwritten, got %v", err)
	}
}

func TestUnit_OpenAPIGeneratorGenerate_RefusesWithoutTheToolOnPATH(t *testing.T) {
	emptyPath(t)
	configuration := testConfig(config.BackendOpenAPIGenerator, nil, nil)

	backend, _ := For(configuration)
	err := backend.Generate(context.Background(), "in.yaml", configuration, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "on PATH") {
		t.Fatalf("Generate without the tool should refuse the same way CheckTool does, got %v", err)
	}
}

func TestUnit_OpenAPIGeneratorGenerate_RefusesAnUnreadableDocumentWhenFiltering(t *testing.T) {
	installStub(t, "openapi-generator-cli", openAPIGeneratorStub, "1.2.3")
	configuration := testConfig(config.BackendOpenAPIGenerator, []string{"/widgets/**"}, nil)

	backend, _ := For(configuration)
	err := backend.Generate(context.Background(), filepath.Join(t.TempDir(), "no-such.yaml"), configuration, t.TempDir())
	if err == nil {
		t.Fatal("filtering needs the document; a missing one must refuse")
	}
}

func TestUnit_OpenAPIGeneratorCheckTool_RefusesUnreadableVersionOutput(t *testing.T) {
	installStub(t, "openapi-generator-cli", "#!/bin/sh\necho no digits here\n", "")
	configuration := testConfig(config.BackendOpenAPIGenerator, nil, nil)

	backend, _ := For(configuration)
	err := backend.CheckTool(context.Background(), configuration)
	if err == nil || !strings.Contains(err.Error(), "could not read a version") {
		t.Fatalf("unparseable version output should refuse, got %v", err)
	}
}

func TestUnit_OpenAPIGeneratorNormalize_SurfacesAnUnreadableFILES(t *testing.T) {
	out := t.TempDir()
	// FILES as a directory: readable as a path, unreadable as a file.
	if err := os.MkdirAll(filepath.Join(out, ".openapi-generator", "FILES"), 0o750); err != nil {
		t.Fatal(err)
	}

	backend := openAPIGeneratorBackend{}
	if err := backend.Normalize(out, "spec/revised.yaml"); err == nil {
		t.Fatal("an unreadable FILES inventory should refuse, not be skipped")
	}
}

func TestUnit_OpenAPIGeneratorNormalize_SurfacesUndeletableScaffolding(t *testing.T) {
	out := t.TempDir()
	// git_push.sh as a non-empty directory: os.Remove fails, and not with
	// IsNotExist.
	if err := os.MkdirAll(filepath.Join(out, "git_push.sh", "child"), 0o750); err != nil {
		t.Fatal(err)
	}

	backend := openAPIGeneratorBackend{}
	if err := backend.Normalize(out, "spec/revised.yaml"); err == nil {
		t.Fatal("scaffolding that cannot be deleted should refuse")
	}
}

func TestUnit_CollapseAllOf_LeavesRealCompositionsAlone(t *testing.T) {
	document := `openapi: 3.0.3
components:
  schemas:
    TwoMembers:
      allOf:
        - type: object
        - type: object
    Overlapping:
      type: object
      allOf:
        - type: string
    NonMapping:
      allOf:
        - true
    Nested:
      type: object
      properties:
        inner:
          allOf:
            - type: object
              properties:
                deep:
                  type: string
`
	out, rewrites, err := Prenormalise([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	if rewrites.AnonymousAllOfsCollapsed != 1 {
		t.Errorf("collapsed %d, want only the nested anonymous single-member allOf", rewrites.AnonymousAllOfsCollapsed)
	}
	text := string(out)
	// The two-member, overlapping-key, and non-mapping allOfs all survive.
	if got := strings.Count(text, "allOf:"); got != 3 {
		t.Errorf("%d allOfs survive, want 3:\n%s", got, text)
	}
}
