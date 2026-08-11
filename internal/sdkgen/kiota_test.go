package sdkgen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
)

func TestUnit_KiotaCheckTool_AcceptsTheMatchingPin(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	cfg := testConfig(config.BackendKiota, nil, nil)

	backend, err := For(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CheckTool(context.Background(), cfg); err != nil {
		t.Fatalf("a matching version should pass the gate: %v", err)
	}
}

func TestUnit_KiotaCheckTool_RefusesAMismatchNamingBothVersions(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "9.9.9")
	cfg := testConfig(config.BackendKiota, nil, nil)

	backend, _ := For(cfg)
	err := backend.CheckTool(context.Background(), cfg)
	if err == nil {
		t.Fatal("a mismatched version must refuse")
	}
	for _, want := range []string{"9.9.9", "1.2.3", "never downloads"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
}

func TestUnit_KiotaCheckTool_RefusesWhenTheToolIsMissing(t *testing.T) {
	emptyPath(t)
	cfg := testConfig(config.BackendKiota, nil, nil)

	backend, _ := For(cfg)
	err := backend.CheckTool(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "not on PATH") {
		t.Fatalf("a missing tool should refuse naming PATH, got %v", err)
	}
	if !strings.Contains(err.Error(), "never downloads") {
		t.Errorf("the refusal should say the toolkit never downloads tools: %v", err)
	}
}

func TestUnit_KiotaCheckTool_RefusesUnreadableVersionOutput(t *testing.T) {
	installStub(t, "kiota", "#!/bin/sh\necho who knows\n", "")
	cfg := testConfig(config.BackendKiota, nil, nil)

	backend, _ := For(cfg)
	err := backend.CheckTool(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "could not read a version") {
		t.Fatalf("unparseable version output should refuse, got %v", err)
	}
}

func TestUnit_KiotaGenerate_PassesTheDocumentedFlagsAndPathGlobs(t *testing.T) {
	argsFile := installStub(t, "kiota", kiotaStub, "1.2.3")
	cfg := testConfig(config.BackendKiota, []string{"/widgets/**"}, []string{"/internal/**"})
	out := filepath.Join(t.TempDir(), "sdk")
	spec := filepath.Join(t.TempDir(), "revised.prenormalized.yaml")
	if err := os.WriteFile(spec, []byte(sampleRevised), 0o600); err != nil {
		t.Fatal(err)
	}

	backend, _ := For(cfg)
	if err := backend.Generate(context.Background(), spec, cfg, out); err != nil {
		t.Fatal(err)
	}

	got := stubArgs(t, argsFile)
	want := []string{
		"generate",
		"--language", "go",
		"--openapi", spec,
		"--output", out,
		"--class-name", "APIClient",
		"--namespace-name", "github.com/exampleco/terraform-provider-petstore/internal/sdk",
		"--exclude-backward-compatible",
		"--clean-output",
		"--include-path", "/widgets/**",
		"--exclude-path", "/internal/**",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("kiota invocation:\n got %q\nwant %q", got, want)
	}

	if _, err := os.Stat(filepath.Join(out, ".kiota.log")); !os.IsNotExist(err) {
		t.Error("the timestamped .kiota.log should be removed")
	}
	if _, err := os.Stat(filepath.Join(out, "client.go")); err != nil {
		t.Errorf("the generated tree is missing: %v", err)
	}
}

func TestUnit_KiotaGenerate_SurfacesTheToolsOwnFailure(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	t.Setenv("STUB_FAIL", "yes")
	cfg := testConfig(config.BackendKiota, nil, nil)

	backend, _ := For(cfg)
	err := backend.Generate(context.Background(), "in.yaml", cfg, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "stub exploded") {
		t.Fatalf("the tool's own stderr should be in the error, got %v", err)
	}
}

func TestUnit_KiotaNormalize_ScrubsTheLockAndDatedHeaders(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	cfg := testConfig(config.BackendKiota, nil, nil)
	out := filepath.Join(t.TempDir(), "sdk")
	backend, _ := For(cfg)
	if err := backend.Generate(context.Background(), "in.yaml", cfg, out); err != nil {
		t.Fatal(err)
	}

	if err := backend.Normalize(out, "spec/revised.yaml"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(out, KiotaLockName))
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]any
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	if got := lock["descriptionLocation"]; got != "spec/revised.yaml" {
		t.Errorf("descriptionLocation = %v, want the durable revised document", got)
	}
	if _, there := lock["generatedAt"]; there {
		t.Error("the timestamp field should be gone")
	}
	if lock["kiotaVersion"] != "1.2.3" || lock["descriptionHash"] != "ABCDEF123456" {
		t.Errorf("the provable evidence fields must survive: %v", lock)
	}

	src, err := os.ReadFile(filepath.Join(out, "client.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "Generated on") {
		t.Errorf("the dated header line should be scrubbed:\n%s", src)
	}
	if !strings.Contains(string(src), "DO NOT EDIT") {
		t.Errorf("the undated header line must survive:\n%s", src)
	}
}

func TestUnit_KiotaNormalize_RefusesWhenTheLockIsMissingOrBroken(t *testing.T) {
	backend := kiotaBackend{}

	if err := backend.Normalize(t.TempDir(), "spec/revised.yaml"); err == nil {
		t.Error("a tree without a lock should refuse — kiota generated nothing")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, KiotaLockName), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := backend.Normalize(dir, "spec/revised.yaml"); err == nil || !strings.Contains(err.Error(), "not usable JSON") {
		t.Errorf("a broken lock should refuse, got %v", err)
	}
}

// TestUnit_KiotaGenerate_RefusesAConfigTheNamespaceCannotDeriveFrom covers
// the namespace derivation's own gate: without a provider name there is no
// module path to root the generated imports at, and the refusal must come
// from the derivation rather than from a half-run tool.
func TestUnit_KiotaGenerate_RefusesAConfigTheNamespaceCannotDeriveFrom(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	cfg := testConfig(config.BackendKiota, nil, nil)
	cfg.Provider.Name = ""

	backend, _ := For(cfg)
	err := backend.Generate(context.Background(), "in.yaml", cfg, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "provider.name") {
		t.Fatalf("err = %v, want a provider.name refusal", err)
	}
}
