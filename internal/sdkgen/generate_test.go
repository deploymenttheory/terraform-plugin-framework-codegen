package sdkgen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/manifest"
)

func TestUnit_For_SelectsEachBackendAndRefusesOthers(t *testing.T) {
	if b, err := For(testConfig(config.BackendKiota, nil, nil)); err != nil || b.Name() != config.BackendKiota {
		t.Errorf("For(kiota) = %v, %v", b, err)
	}
	if b, err := For(testConfig(config.BackendOpenAPIGenerator, nil, nil)); err != nil || b.Name() != config.BackendOpenAPIGenerator {
		t.Errorf("For(openapi-generator) = %v, %v", b, err)
	}

	_, err := For(testConfig("swagger-codegen", nil, nil))
	if err == nil {
		t.Fatal("an unknown backend must refuse")
	}
	for _, want := range []string{"swagger-codegen", config.BackendKiota, config.BackendOpenAPIGenerator} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q: %v", want, err)
		}
	}
}

// repoRoot builds a provider-repo root holding spec/revised.yaml.
func repoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "spec"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spec", "revised.yaml"), []byte(sampleRevised), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func runOptions(root string) Options {
	return Options{
		Config:  testConfig(config.BackendKiota, nil, nil),
		SpecDir: filepath.Join(root, "spec"),
		Out:     filepath.Join("internal", "sdk"),
		Root:    root,
	}
}

func TestUnit_Run_GeneratesNormalizesAndRecordsTheManifest(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	root := repoRoot(t)

	res, err := Run(context.Background(), runOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	if res.Backend != config.BackendKiota || res.Version != "1.2.3" {
		t.Errorf("result names %s %s, want kiota 1.2.3", res.Backend, res.Version)
	}
	if res.Files != 2 {
		t.Errorf("res.Files = %d, want 2 (client.go and the lock)", res.Files)
	}

	// The tree landed and its lock names the durable document, not a temp path.
	lockData, err := os.ReadFile(filepath.Join(root, "internal", "sdk", KiotaLockName))
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]any
	if err := json.Unmarshal(lockData, &lock); err != nil {
		t.Fatal(err)
	}
	if got := lock["descriptionLocation"]; got != "spec/revised.yaml" {
		t.Errorf("descriptionLocation = %v, want spec/revised.yaml", got)
	}

	m, ok, err := manifest.Load(root)
	if err != nil || !ok {
		t.Fatalf("manifest.Load = %v, %v", ok, err)
	}
	byPath := map[string]manifest.Entry{}
	for _, e := range m.Files {
		byPath[e.Path] = e
	}
	entry, there := byPath["internal/sdk/client.go"]
	if !there {
		t.Fatalf("manifest is missing internal/sdk/client.go: %+v", m.Files)
	}
	if entry.Origin != manifest.OriginSDK || len(entry.SHA256) != 64 || entry.Source != "spec/revised.yaml" {
		t.Errorf("entry = %+v, want sdk origin, a sha256, and the revised source", entry)
	}
}

func TestUnit_Run_ReplacesAStaleTreeWholesale(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	root := repoRoot(t)
	stale := filepath.Join(root, "internal", "sdk", "stale.go")
	if err := os.MkdirAll(filepath.Dir(stale), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("package sdk // stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), runOptions(root)); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("the stale file should be gone — the swap replaces the tree, not merges into it")
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "sdk", "client.go")); err != nil {
		t.Errorf("the fresh tree is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "sdk") + ".tfpfgen-previous"); !os.IsNotExist(err) {
		t.Error("the parked previous tree should be cleaned up")
	}
}

func TestUnit_Run_CarriesOtherOriginsForwardAndDropsItsOwnStaleEntries(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	root := repoRoot(t)
	prior := manifest.New("dev", []manifest.Entry{
		{Path: "internal/provider/widget.go", SHA256: strings.Repeat("a", 64), Source: "widget"},
		{Path: "tfpfgen.yaml", Authored: true},
		{Path: "internal/sdk/gone.go", SHA256: strings.Repeat("b", 64), Origin: manifest.OriginSDK},
	})
	if err := manifest.Save(root, prior); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), runOptions(root)); err != nil {
		t.Fatal(err)
	}

	m, _, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := m.Paths()
	for _, want := range []string{"internal/provider/widget.go", "tfpfgen.yaml", "internal/sdk/client.go"} {
		if !paths[want] {
			t.Errorf("manifest lost %s: %v", want, paths)
		}
	}
	if paths["internal/sdk/gone.go"] {
		t.Error("the previous run's sdk entry should be replaced, not accumulated")
	}
}

func TestUnit_Run_RefusesWithoutARevisedSpecNamingTheVerb(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	root := t.TempDir()

	_, err := Run(context.Background(), runOptions(root))
	if err == nil || !strings.Contains(err.Error(), "tfpfgen spec revise") {
		t.Fatalf("a missing revised spec should say what to run, got %v", err)
	}
}

func TestUnit_Run_RefusesAnUnknownBackendAndAFailedGate(t *testing.T) {
	root := repoRoot(t)

	opts := runOptions(root)
	opts.Config = testConfig("swagger-codegen", nil, nil)
	if _, err := Run(context.Background(), opts); err == nil {
		t.Error("an unknown backend must refuse")
	}

	installStub(t, "kiota", kiotaStub, "9.9.9")
	if _, err := Run(context.Background(), runOptions(root)); err == nil || !strings.Contains(err.Error(), "9.9.9") {
		t.Errorf("a failed version gate must refuse before generating, got %v", err)
	}
}

func TestUnit_Run_RefusesAnUnusableRevisedDocument(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	root := repoRoot(t)
	if err := os.WriteFile(filepath.Join(root, "spec", "revised.yaml"), []byte(":\n bad\n :"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), runOptions(root)); err == nil {
		t.Fatal("an unparseable revised document must refuse before invoking the tool")
	}
}

func TestUnit_Run_RefusesToOverwriteAnAuthoredPath(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	root := repoRoot(t)
	prior := manifest.New("dev", []manifest.Entry{
		{Path: "internal/sdk/client.go", Authored: true},
	})
	if err := manifest.Save(root, prior); err != nil {
		t.Fatal(err)
	}

	_, err := Run(context.Background(), runOptions(root))
	if err == nil || !strings.Contains(err.Error(), "authored") {
		t.Fatalf("an authored path under --out must refuse, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "internal", "sdk", "client.go")); !os.IsNotExist(statErr) {
		t.Error("the refusal must land before anything is installed")
	}
}

func TestUnit_Run_RefusesAnOutsideOutputDirectory(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	root := repoRoot(t)
	opts := runOptions(root)
	opts.Out = t.TempDir() // absolute, outside root

	_, err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "outside the repo root") {
		t.Fatalf("an out dir the manifest cannot record must refuse, got %v", err)
	}
}
