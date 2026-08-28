package providergen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/manifest"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/version"
)

func TestUnit_Run_MissingRevisedSaysRunSpecRevise(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if err := os.Remove(filepath.Join(root, "spec", "revised.yaml")); err != nil {
		t.Fatal(err)
	}

	_, err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "tfpfgen spec revise") {
		t.Fatalf("err = %v; a missing revised document names the verb to run", err)
	}
}

func TestUnit_Run_MissingSDKSaysRunSDKGenerate(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if err := os.RemoveAll(filepath.Join(root, "internal", "sdk")); err != nil {
		t.Fatal(err)
	}

	_, err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "tfpfgen sdk generate") {
		t.Fatalf("err = %v; a missing SDK names the verb to run", err)
	}
}

func TestUnit_Run_RefusesAuthoredPaths(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	seeded := manifest.New(version.Version(), []manifest.Entry{
		{Path: "main.go", Authored: true},
	})
	if err := manifest.Save(root, seeded); err != nil {
		t.Fatal(err)
	}

	_, err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "refuses to write authored paths") ||
		!strings.Contains(err.Error(), "main.go") {
		t.Fatalf("err = %v; an authored path under the output must refuse by name", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "go.mod")); !os.IsNotExist(statErr) {
		t.Error("the refused run wrote into the repo before refusing")
	}
}

func TestUnit_Run_RemovesUnproducedFilesAndCarriesOtherOriginsForward(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Plant an unproduced file — one a previous run supposedly produced that the
	// fixture no longer does — plus an sdk-origin entry and an authored
	// entry the provider run must carry forward untouched.
	unproduced := "internal/services/resources/retired/v1/thing/resource.go"
	full := filepath.Join(root, filepath.FromSlash(unproduced))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("package thing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	current, _, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	seeded := manifest.New(version.Version(), append(current.Files,
		manifest.Entry{Path: unproduced, SHA256: "aa"},
		manifest.Entry{Path: "internal/sdk/client.go", SHA256: "bb", Origin: manifest.OriginSDK},
		manifest.Entry{Path: "tfpfgen.yaml", Authored: true},
	))
	if err := manifest.Save(root, seeded); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Errorf("the unproduced file %s survived regeneration", unproduced)
	}
	next, _, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := next.Paths()
	if paths[unproduced] {
		t.Errorf("the manifest still records %s", unproduced)
	}
	if !paths["internal/sdk/client.go"] {
		t.Error("the sdk-origin entry was not carried forward")
	}
	if !next.AuthoredPaths()["tfpfgen.yaml"] {
		t.Error("the authored entry was not carried forward")
	}
}

func TestUnit_Run_BadProviderCoreContextIsRefused(t *testing.T) {
	_, opts := curatedRepo(t, "kiota")
	opts.Config.SDK.ClientTypeName = ""

	_, err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "client_type_name") {
		t.Fatalf("err = %v; an unrenderable provider-core context must refuse by name", err)
	}
}

func TestUnit_IR_DumpsTheDerivationWithoutTouchingTheRepo(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	// --print-ir must not need the SDK: it is a look at the derivation.
	if err := os.RemoveAll(filepath.Join(root, "internal", "sdk")); err != nil {
		t.Fatal(err)
	}

	out, err := IR(opts)
	if err != nil {
		t.Fatalf("IR: %v", err)
	}
	var decoded struct {
		Provider  struct{ Name string }
		Resources []struct {
			Names struct{ Key string }
		}
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("the dump is not JSON: %v", err)
	}
	if decoded.Provider.Name != "orbital" || len(decoded.Resources) != 3 {
		t.Errorf("dump = provider %q with %d resources; the fixture derives orbital with 3",
			decoded.Provider.Name, len(decoded.Resources))
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); !os.IsNotExist(err) {
		t.Error("IR generated files")
	}
}

func TestUnit_IR_MissingRevisedFails(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if err := os.Remove(filepath.Join(root, "spec", "revised.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := IR(opts); err == nil {
		t.Fatal("IR answered without a revised document")
	}
}

func TestUnit_Run_UnparseableRevisedFails(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	if err := os.WriteFile(filepath.Join(root, "spec", "revised.yaml"), []byte("openapi: [broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), opts); err == nil {
		t.Fatal("Run generated from an unparseable document")
	}
}

func TestUnit_Run_UnloadableSDKFails(t *testing.T) {
	root, opts := curatedRepo(t, "kiota")
	broken := filepath.Join(root, "internal", "sdk", "broken.go")
	if err := os.WriteFile(broken, []byte("package sdk\n\nfunc broken() { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "cannot load the SDK") {
		t.Fatalf("err = %v; an SDK that does not type-check must say so", err)
	}
}

// digestOf hashes bytes the way the manifest records them.
func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
