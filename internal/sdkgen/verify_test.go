package sdkgen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/manifest"
)

// generatedRoot builds a provider-repo root whose SDK tree and manifest a
// real Run committed — the drift-free baseline every verify test perturbs.
func generatedRoot(t *testing.T) string {
	t.Helper()
	installStub(t, "kiota", kiotaStub, "1.2.3")
	root := repoRoot(t)
	if _, err := Run(context.Background(), runOptions(root)); err != nil {
		t.Fatal(err)
	}
	return root
}

// drifts renders a report the way the CLI prints it, one line per finding.
func drifts(rep Report) []string {
	var out []string
	for _, d := range rep.Drifts {
		out = append(out, d.String())
	}
	return out
}

func TestUnit_Verify_PassesOnAFreshlyGeneratedTree(t *testing.T) {
	root := generatedRoot(t)

	rep, err := Verify(context.Background(), runOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Clean() {
		t.Fatalf("a freshly generated tree must verify clean, got %v", drifts(rep))
	}
	if rep.Backend != config.BackendKiota || rep.Version != "1.2.3" {
		t.Errorf("report names %s %s, want kiota 1.2.3", rep.Backend, rep.Version)
	}
	if rep.Files != 2 {
		t.Errorf("rep.Files = %d, want 2 (client.go and the lock)", rep.Files)
	}

	// Verify is read-only: the committed tree and manifest are untouched.
	if _, err := os.Stat(filepath.Join(root, "internal", "sdk", "client.go")); err != nil {
		t.Errorf("the committed tree was disturbed: %v", err)
	}
	if _, ok, err := manifest.Load(root); err != nil || !ok {
		t.Errorf("the manifest was disturbed: %v, %v", ok, err)
	}
}

func TestUnit_Verify_ReportsAnEditedFileAsChangedAndHandEdited(t *testing.T) {
	root := generatedRoot(t)
	edited := filepath.Join(root, "internal", "sdk", "client.go")
	if err := os.WriteFile(edited, []byte("package sdk // edited by hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(context.Background(), runOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"changed: internal/sdk/client.go",
		"hand-edited: internal/sdk/client.go",
	}
	if got := drifts(rep); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("drifts = %v, want %v — the byte-compare and the manifest each report, sorted", got, want)
	}
}

func TestUnit_Verify_ReportsAnExtraFileSorted(t *testing.T) {
	root := generatedRoot(t)
	for _, name := range []string{"zz_extra.go", "aa_extra.go"} {
		if err := os.WriteFile(filepath.Join(root, "internal", "sdk", name), []byte("package sdk\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := Verify(context.Background(), runOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"extra: internal/sdk/aa_extra.go",
		"extra: internal/sdk/zz_extra.go",
	}
	if got := drifts(rep); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("drifts = %v, want %v", got, want)
	}
}

func TestUnit_Verify_ReportsAMissingFileOnce(t *testing.T) {
	root := generatedRoot(t)
	if err := os.Remove(filepath.Join(root, "internal", "sdk", "client.go")); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(context.Background(), runOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	// The manifest also records the file; a deleted file must not be
	// reported a second time as hand-edited.
	want := []string{"missing: internal/sdk/client.go"}
	if got := drifts(rep); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("drifts = %v, want %v", got, want)
	}
}

func TestUnit_Verify_ReportsATreeNeverGeneratedAsAllMissing(t *testing.T) {
	installStub(t, "kiota", kiotaStub, "1.2.3")
	root := repoRoot(t) // revised spec present, no Run, no manifest

	rep, err := Verify(context.Background(), runOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"missing: internal/sdk/client.go",
		"missing: internal/sdk/kiota-lock.json",
	}
	if got := drifts(rep); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("drifts = %v, want %v", got, want)
	}
}

func TestUnit_Verify_ReportsAManifestDigestMismatchAsHandEditedAlone(t *testing.T) {
	root := generatedRoot(t)
	m, _, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := range m.Files {
		if m.Files[i].Path == "internal/sdk/client.go" {
			m.Files[i].SHA256 = strings.Repeat("f", 64)
		}
	}
	if err := manifest.Save(root, m); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify(context.Background(), runOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	// The file still matches regeneration byte for byte, so the only finding
	// is the manifest's — hand-edited, not changed.
	want := []string{"hand-edited: internal/sdk/client.go"}
	if got := drifts(rep); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("drifts = %v, want %v", got, want)
	}
}

func TestUnit_Verify_PropagatesTheToolVersionGate(t *testing.T) {
	root := generatedRoot(t)
	installStub(t, "kiota", kiotaStub, "9.9.9")

	_, err := Verify(context.Background(), runOptions(root))
	if err == nil {
		t.Fatal("a mismatched tool must refuse before comparing anything")
	}
	for _, want := range []string{"9.9.9", "1.2.3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name both versions, missing %q: %v", want, err)
		}
	}
}

func TestUnit_Verify_PropagatesAFailedGeneration(t *testing.T) {
	root := generatedRoot(t)
	t.Setenv("STUB_FAIL", "1")

	_, err := Verify(context.Background(), runOptions(root))
	if err == nil || !strings.Contains(err.Error(), "stub exploded") {
		t.Fatalf("a failed generation must surface the tool's own explanation, got %v", err)
	}
}

func TestUnit_Verify_RefusesAManifestItCannotRead(t *testing.T) {
	root := generatedRoot(t)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"),
		[]byte(`{"formatVersion": "999", "toolVersion": "dev", "files": []}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Verify(context.Background(), runOptions(root))
	if err == nil || !strings.Contains(err.Error(), "format version") {
		t.Fatalf("an unreadable manifest must refuse rather than verify against nothing, got %v", err)
	}
}

func TestUnit_Verify_SurfacesAManifestEntryItCannotDigest(t *testing.T) {
	root := generatedRoot(t)
	m, _, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	// A recorded path that is a directory: readable as a path, unreadable as
	// a file.
	m.Files = append(m.Files, manifest.Entry{Path: "internal/sdk", Origin: manifest.OriginSDK})
	if err := manifest.Save(root, m); err != nil {
		t.Fatal(err)
	}

	if _, err := Verify(context.Background(), runOptions(root)); err == nil {
		t.Fatal("an undigestible recorded file should refuse, not be skipped")
	}
}
