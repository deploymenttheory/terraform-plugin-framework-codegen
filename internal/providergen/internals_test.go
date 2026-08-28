package providergen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/emit"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/version"
)

func TestUnit_FirstServerURL_EmptyWithoutServers(t *testing.T) {
	if got := firstServerURL(&specmodel.Document{}); got != "" {
		t.Errorf("firstServerURL on a serverless document = %q", got)
	}
	document := &specmodel.Document{Servers: []specmodel.Server{{URL: "https://one.example.test"}, {URL: "https://two.example.test"}}}
	if got := firstServerURL(document); got != "https://one.example.test" {
		t.Errorf("firstServerURL = %q; the first declared server wins", got)
	}
}

func TestUnit_RegisterServices_MissingRegistryFileIsRefused(t *testing.T) {
	_, err := registerServices(nil, emit.Registry{})
	if err == nil || !strings.Contains(err.Error(), "internal/provider/resources.go") {
		t.Fatalf("err = %v; a core without its registry file must refuse by name", err)
	}
}

func TestUnit_RegisterServices_SentinellessFileIsRefused(t *testing.T) {
	core := make([]emit.File, 0, len(registryFiles))
	for _, path := range registryFiles {
		core = append(core, emit.File{Path: path, Content: []byte("package provider\n")})
	}
	_, err := registerServices(core, emit.Registry{})
	if err == nil || !strings.Contains(err.Error(), "sentinel") {
		t.Fatalf("err = %v; a registry file without its sentinel must refuse", err)
	}
}

func TestUnit_Install_MissingStagedFileFails(t *testing.T) {
	err := install(t.TempDir(), t.TempDir(), []string{"never/rendered.go"})
	if err == nil || !strings.Contains(err.Error(), "never/rendered.go") {
		t.Fatalf("err = %v; a missing staged file names itself", err)
	}
}

func TestUnit_RemoveUnproducedFiles_AMissingFileIsQuietlySkipped(t *testing.T) {
	root := t.TempDir()
	removeUnproducedFiles(root, []string{"already/gone.go"})
	if _, err := os.Stat(filepath.Join(root, "already")); !os.IsNotExist(err) {
		t.Error("removeUnproducedFiles invented a directory")
	}
}

func TestUnit_CopyTree_MissingSourceFails(t *testing.T) {
	if err := copyTree(filepath.Join(t.TempDir(), "nowhere"), t.TempDir()); err == nil {
		t.Fatal("copyTree copied from nowhere")
	}
}

func TestUnit_Run_CorruptManifestFails(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run generated over an unreadable manifest")
	}
}

func TestUnit_Verify_CorruptManifestFails(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), opts); err == nil {
		t.Fatal("Verify answered from an unreadable manifest")
	}
}

func TestUnit_Verify_UnreadableCommittedFileFails(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// A produced path that is now a directory cannot be digested.
	target := filepath.Join(root, "main.go")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(context.Background(), opts); err == nil {
		t.Fatal("Verify answered despite an undigestable file")
	}
}

func TestUnit_LoadModel_UnreadableRevisedFails(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	revised := filepath.Join(root, "spec", "revised.yaml")
	if err := os.Remove(revised); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(revised, 0o750); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), opts)
	if err == nil || strings.Contains(err.Error(), "tfpfgen spec revise") {
		t.Fatalf("err = %v; an unreadable document is not a missing one", err)
	}
}

func TestUnit_ResolveSDK_UnstatableSDKFails(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	// A path routed through a regular file cannot be statted.
	opts.SDKDir = filepath.Join("spec", "revised.yaml", "sdk")
	_, err := Run(context.Background(), opts)
	if err == nil || strings.Contains(err.Error(), "tfpfgen sdk generate") {
		t.Fatalf("err = %v; an unstatable SDK path is not a missing tree", err)
	}
	_ = root
}

func TestUnit_BindContext_CopiesTheRepoGoSumIntoTheHarness(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	// A committed go.sum without a go.mod (first generate after a manual
	// tidy) rides into the harness so cached modules verify.
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run with a committed go.sum: %v", err)
	}
}

func TestUnit_Run_UncreatableStagingFails(t *testing.T) {
	_, opts := curatedRepo(t, "kiota")
	// Point the SDK somewhere real but the root somewhere that does not
	// exist: the pipeline stages fine, and the staging directory cannot.
	opts.SDKDir = filepath.Join(curatedDir(t), "kiota", "sdk")
	opts.Root = filepath.Join(t.TempDir(), "never", "made")

	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run staged into a root that does not exist")
	}
}

func TestUnit_Run_AnUnstatableFileFails(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	seedManifestEntry(t, root, "main.go/impossible")

	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run answered despite a file it cannot examine")
	}
}

func TestUnit_Verify_UnreadableRecordedFileFails(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// A recorded path regeneration does not produce, standing as a
	// directory: the manifest sweep cannot digest it.
	seedManifestEntry(t, root, "internal/extradir")
	if err := os.MkdirAll(filepath.Join(root, "internal", "extradir"), 0o750); err != nil {
		t.Fatal(err)
	}

	if _, err := Verify(context.Background(), opts); err == nil {
		t.Fatal("Verify answered despite an undigestable recorded path")
	}
}

func TestUnit_BindContext_UnreadableGoModFails(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if err := os.MkdirAll(filepath.Join(root, "go.mod"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run bound despite an unreadable go.mod")
	}
}

// TestUnit_Run_ResourceWithoutListCannotEmitItsCompanion pins a known gap
// in the chain: classification gives every resource a companion datasource
// whether or not the API can list, and emission requires the list call — so a
// resource whose API declares no collection GET yields no companion. The gap
// is reported as an exclusion naming the datasource, and the resource beside
// it still emits. The curated fixture gives every resource a list operation
// for exactly this reason.
func TestUnit_Run_ResourceWithoutListCannotEmitItsCompanion(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	document := `openapi: 3.0.3
info:
  title: listless
  version: 1.0.0
paths:
  /beacons:
    post:
      operationId: CreateBeacon
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Beacon'
      responses:
        "201":
          description: created
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Beacon'
  /beacons/{beaconId}:
    parameters:
      - name: beaconId
        in: path
        required: true
        schema:
          type: string
    get:
      operationId: GetBeacon
      responses:
        "200":
          description: one
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Beacon'
    delete:
      operationId: DeleteBeacon
      responses:
        "204":
          description: gone
components:
  schemas:
    Beacon:
      type: object
      required:
        - callsign
      properties:
        id:
          type: string
          readOnly: true
        callsign:
          type: string
`
	if err := os.WriteFile(filepath.Join(root, "spec", "revised.yaml"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("one unservable companion must not fail the run: %v", err)
	}
	var reason string
	for _, e := range res.Excluded {
		if e.Key == "beacon" && strings.Contains(e.Reason, "companion datasource needs a bound list call") {
			reason = e.Reason
		}
	}
	if reason == "" {
		t.Fatalf("the listless companion must be excluded with the emit reason, got %+v", res.Excluded)
	}
	if res.Resources != 1 {
		t.Fatalf("the resource beside the refused companion must still emit, got %d", res.Resources)
	}
	if res.Datasources != 0 {
		t.Fatalf("the refused companion must not be counted as generated, got %d", res.Datasources)
	}
}

func TestUnit_LoadModel_EmptyProviderNameFailsDerivation(t *testing.T) {
	_, opts := curatedRepo(t, "kiota")
	opts.Config.Provider.Name = ""
	_, err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "provider.name") {
		t.Fatalf("err = %v; derivation without a provider name must refuse by key", err)
	}
}

func TestUnit_Install_BlockedDirectoryFails(t *testing.T) {
	staging, root := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(staging, "blocked"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "blocked", "x.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A regular file stands where the target directory must go.
	if err := os.WriteFile(filepath.Join(root, "blocked"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := install(staging, root, []string{"blocked/x.go"}); err == nil {
		t.Fatal("install wrote through a file standing where a directory must go")
	}
}

func TestUnit_Run_PostcheckFailureSurfacesVerbatim(t *testing.T) {
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	_, opts := curatedRepo(t, "kiota")
	opts.VerifyTree = true

	// A toolchain that answers everything except `go mod` normally, so
	// binding still type-checks and only the postcheck's first step fails.
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = \"mod\" ]; then echo 'the reactor is offline'; exit 1; fi\nexec " + realGo + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0o700); err != nil { //nolint:gosec // an executable test stub
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err = Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "the reactor is offline") {
		t.Fatalf("err = %v; a postcheck failure carries the toolchain's words", err)
	}
}

func TestUnit_BindContext_UncreatableHarnessFails(t *testing.T) {
	_, opts := curatedRepo(t, "kiota")
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "never", "made"))
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run bound through a harness that cannot exist")
	}
}

// seedManifestEntry appends one empty-origin entry to the committed
// manifest.
func seedManifestEntry(t *testing.T, root, path string) {
	t.Helper()
	current, _, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	seeded := manifest.New(version.Version(), append(current.Files,
		manifest.Entry{Path: path, SHA256: "cc"},
	))
	if err := manifest.Save(root, seeded); err != nil {
		t.Fatal(err)
	}
}
