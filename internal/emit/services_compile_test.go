package emit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// TestUnit_RenderServices_TheRenderedTreeCompiles is the full-strength
// gate for per-entity emission, once per backend dialect: render the
// provider core and the fictional entity tree, register the registrations
// into the registry files, stand the hand-written stub SDK where `sdk
// generate` would put the real one, and hold everything — generated
// tests included — to `go mod tidy`, `go build` and `go vet` against the
// real dependency pins.
//
// Like the provider-core compile test, an offline failure skips rather
// than fails; the render tests already prove everything that does not
// need the module proxy.
func TestUnit_RenderServices_TheRenderedTreeCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the toolchain compile in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}

	cases := []struct {
		name     string
		pc       ProviderCore
		model    *ir.Model
		bindings *sdkbind.Bindings
		stub     map[string]string
	}{
		{
			name: "kiota with bearer", pc: fictionalProviderCore(),
			model: fictionalModel(), bindings: fictionalBindings(),
			stub: map[string]string{
				"testdata/entitysdk/client.go": "internal/sdk/client.go",
				"testdata/entitysdk/models.go": "internal/sdk/models/models.go",
			},
		},
		{
			name: "openapi-generator with basic", pc: openAPIGeneratorProviderCore(),
			model: openAPIGeneratorModel(), bindings: openAPIGeneratorBindings(),
			stub: map[string]string{
				"testdata/entitysdkoag/sdk.go": "internal/sdk/sdk.go",
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			core, err := RenderProviderCore(testCase.pc)
			if err != nil {
				t.Fatalf("RenderProviderCore: %v", err)
			}
			entities, err := RenderServices(testCase.pc, testCase.model, testCase.bindings)
			if err != nil {
				t.Fatalf("RenderServices: %v", err)
			}

			root := t.TempDir()
			if _, err := Write(root, core); err != nil {
				t.Fatalf("writing the provider core: %v", err)
			}
			if _, err := Write(root, entities.Files); err != nil {
				t.Fatalf("writing the entity tree: %v", err)
			}

			// Register every kind's registrations into its registry file,
			// the way `provider generate` will.
			registryFiles := map[string]string{
				"resources":      "internal/provider/resources.go",
				"datasources":    "internal/provider/datasources.go",
				"list_resources": "internal/provider/list_resources.go",
				"actions":        "internal/provider/actions.go",
			}
			for kind, rel := range registryFiles {
				target := filepath.Join(root, filepath.FromSlash(rel))
				content, err := os.ReadFile(target)
				if err != nil {
					t.Fatalf("reading %s: %v", rel, err)
				}
				set, err := entities.Registrations.ByKind(kind)
				if err != nil {
					t.Fatal(err)
				}
				registered, err := Register(content, kind, set)
				if err != nil {
					t.Fatalf("registering %s: %v", rel, err)
				}
				if err := os.WriteFile(target, registered, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			installEntityStubSDK(t, root, testCase.stub)

			runGo(t, root, "mod", "tidy")
			runGo(t, root, "build", "./...")
			runGo(t, root, "vet", "./...")
		})
	}
}

// writeFictionalKiotaModule renders the fictional provider core and entity
// tree, writes them to a temp module, wires the registrations and installs the
// kiota stub SDK — everything the tree needs to build and test bar the
// toolchain run itself. It returns the module root.
func writeFictionalKiotaModule(t *testing.T, pc ProviderCore) string {
	t.Helper()
	core, err := RenderProviderCore(pc)
	if err != nil {
		t.Fatalf("RenderProviderCore: %v", err)
	}
	entities, err := RenderServices(pc, fictionalModel(), fictionalBindings())
	if err != nil {
		t.Fatalf("RenderServices: %v", err)
	}

	root := t.TempDir()
	if _, err := Write(root, core); err != nil {
		t.Fatalf("writing the provider core: %v", err)
	}
	if _, err := Write(root, entities.Files); err != nil {
		t.Fatalf("writing the entity tree: %v", err)
	}

	for kind, rel := range map[string]string{
		"resources":      "internal/provider/resources.go",
		"datasources":    "internal/provider/datasources.go",
		"list_resources": "internal/provider/list_resources.go",
		"actions":        "internal/provider/actions.go",
	} {
		target := filepath.Join(root, filepath.FromSlash(rel))
		content, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		set, err := entities.Registrations.ByKind(kind)
		if err != nil {
			t.Fatal(err)
		}
		registered, err := Register(content, kind, set)
		if err != nil {
			t.Fatalf("registering %s: %v", rel, err)
		}
		if err := os.WriteFile(target, registered, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	installEntityStubSDK(t, root, map[string]string{
		"testdata/entitysdk/client.go": "internal/sdk/client.go",
		"testdata/entitysdk/models.go": "internal/sdk/models/models.go",
	})
	return root
}

// installEntityStubSDK copies the stub SDK files into the tree at the
// paths the SDK import expects.
func installEntityStubSDK(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for source, destination := range files {
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("reading %s: %v", source, err)
		}
		target := filepath.Join(root, filepath.FromSlash(destination))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
