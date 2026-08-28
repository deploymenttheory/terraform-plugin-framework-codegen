package revise

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/store"
)

const upstreamYAML = `openapi: 3.0.3
info:
  title: sample
  version: 1.2.3
paths: {}
components:
  schemas:
    RepeatType:
      type: string
      enum:
        - day
        - week
`

// The same document as the vendor would publish it in JSON: one long line,
// which revision must normalize to block-style YAML.
const upstreamJSON = `{"openapi":"3.0.3","info":{"title":"sample","version":"1.2.3"},"paths":{},` +
	`"components":{"schemas":{"RepeatType":{"type":"string","enum":["day","week"]}}}}`

// pinned imports doc into a fresh spec directory and returns it.
func pinned(t *testing.T, document string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "spec")
	if _, err := store.Import(dir, []byte(document), "published.yaml"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	return dir
}

// writeFile writes content, creating every parent directory first.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// addEnumValue is a correction file body appending value to the enum.
func addEnumValue(value string) string {
	return `{
  "justification": "the live API accepts and echoes this value",
  "operations": [
    {"op": "add", "path": "/components/schemas/RepeatType/enum/-", "value": "` + value + `"}
  ]
}`
}

// revisedEnum decodes dir/revised.yaml and returns the RepeatType enum.
func revisedEnum(t *testing.T, dir string) []any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, OutputName))
	if err != nil {
		t.Fatalf("reading %s: %v", OutputName, err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("%s is not usable YAML: %v", OutputName, err)
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	return schemas["RepeatType"].(map[string]any)["enum"].([]any)
}

func TestUnit_Revise_WritesTheUpstreamRoundTripWithNoCorrections(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)

	res, err := Materialize(dir)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(res.Applied) != 0 {
		t.Errorf("Applied = %v, want none", res.Applied)
	}
	if res.OutputPath != filepath.Join(dir, OutputName) {
		t.Errorf("OutputPath = %q", res.OutputPath)
	}
	if res.Lock.OpenAPI != "3.0.3" {
		t.Errorf("Lock.OpenAPI = %q, want the verified pin", res.Lock.OpenAPI)
	}
	if enum := revisedEnum(t, dir); len(enum) != 2 {
		t.Errorf("enum = %v, want the upstream document unchanged", enum)
	}
}

func TestUnit_Revise_NormalizesAJSONPublishedDocumentToBlockYAML(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamJSON)

	if _, err := Materialize(dir); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, OutputName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		t.Fatalf("revised.yaml is still flow-style JSON:\n%s", data)
	}
	if enum := revisedEnum(t, dir); len(enum) != 2 || enum[0] != "day" {
		t.Errorf("enum = %v after normalization", enum)
	}
}

func TestUnit_Revise_AppliesAcceptedCorrectionsInOrder(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)
	corrections := filepath.Join(dir, correction.DirName)
	writeFile(t, filepath.Join(corrections, "20-add-none.correction.json"), addEnumValue("none"))
	writeFile(t, filepath.Join(corrections, "10-add-month.correction.json"), addEnumValue("month"))

	res, err := Materialize(dir)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	want := []string{
		filepath.Join(corrections, "10-add-month.correction.json"),
		filepath.Join(corrections, "20-add-none.correction.json"),
	}
	if len(res.Applied) != 2 || res.Applied[0] != want[0] || res.Applied[1] != want[1] {
		t.Errorf("Applied = %v, want %v", res.Applied, want)
	}
	enum := revisedEnum(t, dir)
	if len(enum) != 4 || enum[2] != "month" || enum[3] != "none" {
		t.Errorf("enum = %v, want day, week, month, none", enum)
	}
}

func TestUnit_Revise_IsByteIdenticalAcrossRuns(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)
	writeFile(t, filepath.Join(dir, correction.DirName, "add.correction.json"), addEnumValue("none"))

	if _, err := Materialize(dir); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, OutputName))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Materialize(dir); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(dir, OutputName))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Fatalf("revise is not deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestUnit_Revise_RefusesWhileProposedCorrectionsAwaitADecision(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)
	proposed := filepath.Join(dir, correction.DirName, ProposedDirName)
	writeFile(t, filepath.Join(proposed, "a.correction.json"), addEnumValue("none"))
	writeFile(t, filepath.Join(proposed, "b.correction.json"), addEnumValue("month"))

	_, err := Materialize(dir)
	if err == nil {
		t.Fatal("Materialize succeeded with proposals pending")
	}
	for _, want := range []string{
		"2 proposed corrections await a decision",
		filepath.Join(proposed, "a.correction.json"),
		filepath.Join(proposed, "b.correction.json"),
		"accept — move the file into " + filepath.Join(dir, correction.DirName),
		"reject — add a marker in " + filepath.Join(dir, correction.DirName, RejectedDirName),
		"no flag skips this",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(dir, OutputName)); !os.IsNotExist(statErr) {
		t.Errorf("revised.yaml was written despite the refusal (stat: %v)", statErr)
	}
}

func TestUnit_Revise_RefusalNamesASingleProposalInTheSingular(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)
	writeFile(t, filepath.Join(dir, correction.DirName, ProposedDirName, "a.correction.json"), addEnumValue("none"))

	_, err := Materialize(dir)
	if err == nil || !strings.Contains(err.Error(), "1 proposed correction awaits a decision") {
		t.Fatalf("error does not count the single proposal: %v", err)
	}
}

func TestUnit_Revise_IgnoresNonCorrectionFilesInProposed(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)
	proposed := filepath.Join(dir, correction.DirName, ProposedDirName)
	writeFile(t, filepath.Join(proposed, ".gitkeep"), "")
	if err := os.MkdirAll(filepath.Join(proposed, "notes"), 0o750); err != nil {
		t.Fatal(err)
	}

	if _, err := Materialize(dir); err != nil {
		t.Fatalf("an empty proposed directory must not block revision: %v", err)
	}
}

func TestUnit_Revise_IgnoresRejectedMarkers(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)
	writeFile(t, filepath.Join(dir, correction.DirName, RejectedDirName, "a.json"),
		`{"rejected": "the observation was a transient API bug"}`)

	res, err := Materialize(dir)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(res.Applied) != 0 {
		t.Errorf("Applied = %v; a rejected marker is not a correction", res.Applied)
	}
}

func TestUnit_Revise_RefusesWhenNothingIsPinned(t *testing.T) {
	t.Parallel()
	_, err := Materialize(filepath.Join(t.TempDir(), "spec"))
	if err == nil || !strings.Contains(err.Error(), "tfpfgen spec import") {
		t.Fatalf("error does not say what to run: %v", err)
	}
}

func TestUnit_Revise_RefusesATamperedPin(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)
	writeFile(t, filepath.Join(dir, store.DocumentName), "openapi: 3.0.3\nedited: true\n")

	_, err := Materialize(dir)
	if err == nil || !strings.Contains(err.Error(), "does not match its lock") {
		t.Fatalf("a tampered tree must refuse to revise: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, OutputName)); !os.IsNotExist(statErr) {
		t.Errorf("revised.yaml was written from a tampered tree (stat: %v)", statErr)
	}
}

func TestUnit_Revise_SurfacesAStaleCorrection(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)
	path := filepath.Join(dir, correction.DirName, "stale.correction.json")
	writeFile(t, path, addEnumValue("week")) // already upstream

	_, err := Materialize(dir)
	if err == nil {
		t.Fatal("a stale correction must refuse")
	}
	for _, want := range []string{path, "stale", "delete the correction"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

func TestUnit_Revise_RefusesAnUnusableCorrection(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)
	writeFile(t, filepath.Join(dir, correction.DirName, "broken.correction.json"), "not json")

	_, err := Materialize(dir)
	if err == nil || !strings.Contains(err.Error(), "not a usable correction") {
		t.Fatalf("a malformed correction must refuse: %v", err)
	}
}

func TestUnit_Revise_RefusesAnUnreadableProposedDirectory(t *testing.T) {
	t.Parallel()
	dir := pinned(t, upstreamYAML)
	// A file where the proposed directory should be: ReadDir fails with
	// something other than not-exist.
	writeFile(t, filepath.Join(dir, correction.DirName, ProposedDirName), "a file, not a directory")

	if _, err := Materialize(dir); err == nil {
		t.Fatal("an unreadable proposed path must surface, not be ignored")
	}
}
