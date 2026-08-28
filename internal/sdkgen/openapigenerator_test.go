package sdkgen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
)

func TestUnit_OpenAPIGeneratorCheckTool_AcceptsEitherBinaryName(t *testing.T) {
	configuration := testConfig(config.BackendOpenAPIGenerator, nil, nil)
	backend, err := For(configuration)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"openapi-generator-cli", "openapi-generator"} {
		t.Run(name, func(t *testing.T) {
			installStub(t, name, openAPIGeneratorStub, "1.2.3")
			if err := backend.CheckTool(context.Background(), configuration); err != nil {
				t.Fatalf("a matching %s should pass the gate: %v", name, err)
			}
		})
	}
}

func TestUnit_OpenAPIGeneratorCheckTool_RefusesAMismatchNamingBothVersions(t *testing.T) {
	installStub(t, "openapi-generator-cli", openAPIGeneratorStub, "7.6.0")
	configuration := testConfig(config.BackendOpenAPIGenerator, nil, nil)

	backend, _ := For(configuration)
	err := backend.CheckTool(context.Background(), configuration)
	if err == nil {
		t.Fatal("a mismatched version must refuse")
	}
	for _, want := range []string{"7.6.0", "1.2.3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
}

func TestUnit_OpenAPIGeneratorCheckTool_RefusesWhenNeitherBinaryExists(t *testing.T) {
	emptyPath(t)
	configuration := testConfig(config.BackendOpenAPIGenerator, nil, nil)

	backend, _ := For(configuration)
	err := backend.CheckTool(context.Background(), configuration)
	if err == nil {
		t.Fatal("a missing tool must refuse")
	}
	for _, want := range []string{"openapi-generator-cli", "openapi-generator", "never downloads"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
}

func TestUnit_OpenAPIGeneratorGenerate_PassesTheDocumentedFlagsAndWritesTheIgnoreFile(t *testing.T) {
	argsFile := installStub(t, "openapi-generator-cli", openAPIGeneratorStub, "1.2.3")
	configuration := testConfig(config.BackendOpenAPIGenerator, nil, nil)
	out := filepath.Join(t.TempDir(), "sdk")
	spec := filepath.Join(t.TempDir(), "revised.prenormalized.yaml")
	if err := os.WriteFile(spec, []byte(sampleRevised), 0o600); err != nil {
		t.Fatal(err)
	}

	backend, _ := For(configuration)
	if err := backend.Generate(context.Background(), spec, configuration, out); err != nil {
		t.Fatal(err)
	}

	got := strings.Join(stubArgs(t, argsFile), " ")
	for _, want := range []string{
		"generate",
		"-g go",
		"-i " + spec,
		"-o " + out,
		"--additional-properties hideGenerationTimestamp=true,packageName=sdk,withGoMod=false",
		"--global-property apis,models,supportingFiles,apiDocs=false,modelDocs=false,apiTests=false,modelTests=false",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("invocation missing %q:\n%s", want, got)
		}
	}

	ignore, err := os.ReadFile(filepath.Join(out, ignoreFileName))
	if err != nil {
		t.Fatalf("the ignore file must exist before the tool runs: %v", err)
	}
	for _, want := range []string{"docs/", "api/", ".travis.yml", "git_push.sh", "*.mustache"} {
		if !strings.Contains(string(ignore), want) {
			t.Errorf("ignore file missing %q:\n%s", want, ignore)
		}
	}
}

func TestUnit_OpenAPIGeneratorGenerate_FiltersTheDocumentForPathGlobs(t *testing.T) {
	argsFile := installStub(t, "openapi-generator-cli", openAPIGeneratorStub, "1.2.3")
	configuration := testConfig(config.BackendOpenAPIGenerator, []string{"/widgets/**", "/widgets"}, []string{"/widgets/{id}"})
	out := filepath.Join(t.TempDir(), "sdk")
	spec := filepath.Join(t.TempDir(), "revised.prenormalized.yaml")
	if err := os.WriteFile(spec, []byte(sampleRevised), 0o600); err != nil {
		t.Fatal(err)
	}

	backend, _ := For(configuration)
	if err := backend.Generate(context.Background(), spec, configuration, out); err != nil {
		t.Fatal(err)
	}

	// The tool has no path-glob flag, so it must have received a filtered copy.
	var input string
	args := stubArgs(t, argsFile)
	for i, a := range args {
		if a == "-i" && i+1 < len(args) {
			input = args[i+1]
		}
	}
	if !strings.HasSuffix(input, ".filtered.yaml") {
		t.Fatalf("-i should name the filtered copy, got %q", input)
	}

	received, err := os.ReadFile(filepath.Join(out, "received_spec.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(received), "/widgets:") {
		t.Errorf("/widgets should survive the filter:\n%s", received)
	}
	for _, gone := range []string{"/widgets/{id}", "/internal/health"} {
		if strings.Contains(string(received), gone) {
			t.Errorf("%s should be filtered out of the document the tool reads:\n%s", gone, received)
		}
	}
}

func TestUnit_OpenAPIGeneratorGenerate_RefusesAFilterThatKeepsNothing(t *testing.T) {
	installStub(t, "openapi-generator-cli", openAPIGeneratorStub, "1.2.3")
	configuration := testConfig(config.BackendOpenAPIGenerator, []string{"/nothing/**"}, nil)
	spec := filepath.Join(t.TempDir(), "revised.prenormalized.yaml")
	if err := os.WriteFile(spec, []byte(sampleRevised), 0o600); err != nil {
		t.Fatal(err)
	}

	backend, _ := For(configuration)
	err := backend.Generate(context.Background(), spec, configuration, filepath.Join(t.TempDir(), "sdk"))
	if err == nil || !strings.Contains(err.Error(), "no paths") {
		t.Fatalf("an all-filtering config should refuse, got %v", err)
	}
}

func TestUnit_OpenAPIGeneratorNormalize_SortsFILESAndDeletesScaffolding(t *testing.T) {
	installStub(t, "openapi-generator-cli", openAPIGeneratorStub, "1.2.3")
	configuration := testConfig(config.BackendOpenAPIGenerator, nil, nil)
	out := filepath.Join(t.TempDir(), "sdk")
	spec := filepath.Join(t.TempDir(), "revised.prenormalized.yaml")
	if err := os.WriteFile(spec, []byte(sampleRevised), 0o600); err != nil {
		t.Fatal(err)
	}
	backend, _ := For(configuration)
	if err := backend.Generate(context.Background(), spec, configuration, out); err != nil {
		t.Fatal(err)
	}

	if err := backend.Normalize(out, "spec/revised.yaml"); err != nil {
		t.Fatal(err)
	}

	files, err := os.ReadFile(filepath.Join(out, ".openapi-generator", "FILES"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(files), "client.go\nmodel_a.go\nmodel_b.go\n"; got != want {
		t.Errorf("FILES = %q, want sorted and deduplicated %q", got, want)
	}

	if _, err := os.Stat(filepath.Join(out, "git_push.sh")); !os.IsNotExist(err) {
		t.Error("git_push.sh should be deleted")
	}
	if _, err := os.Stat(filepath.Join(out, ".openapi-generator", "VERSION")); err != nil {
		t.Errorf("the VERSION evidence must survive: %v", err)
	}

	source, err := os.ReadFile(filepath.Join(out, "client.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "2026-01-02") {
		t.Errorf("the dated header line should be scrubbed:\n%s", source)
	}
}

func TestUnit_OpenAPIGeneratorNormalize_ToleratesATreeWithoutFILES(t *testing.T) {
	backend := openAPIGeneratorBackend{}
	if err := backend.Normalize(t.TempDir(), "spec/revised.yaml"); err != nil {
		t.Fatalf("nothing to normalize is not an error: %v", err)
	}
}
