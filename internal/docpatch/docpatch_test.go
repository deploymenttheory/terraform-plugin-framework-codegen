package docpatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const spec = `
openapi: 3.0.1
components:
  schemas:
    RepeatType:
      type: string
      enum:
        - day
        - week
    Window:
      type: object
      properties:
        name:
          type: string
`

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("Apply returned unusable YAML: %v", err)
	}
	return doc
}

func enumOf(t *testing.T, doc map[string]any) []any {
	t.Helper()
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	return schemas["RepeatType"].(map[string]any)["enum"].([]any)
}

func TestUnit_SpecPatch_AddsAnEnumValue(t *testing.T) {
	t.Parallel()

	out, err := Apply([]byte(spec), []Patch{{
		Justification: "recording x",
		Operations: []Operation{
			{Op: "test", Path: "/components/schemas/RepeatType/enum/0", Value: "day"},
			{Op: "add", Path: "/components/schemas/RepeatType/enum/-", Value: "none"},
		},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	enum := enumOf(t, decode(t, out))
	if len(enum) != 3 || enum[2] != "none" {
		t.Errorf("enum = %v, want day, week, none", enum)
	}
}

func TestUnit_SpecPatch_RefusesAStalePatch(t *testing.T) {
	t.Parallel()

	_, err := Apply([]byte(spec), []Patch{{
		Justification: "recording x",
		Operations: []Operation{
			{Op: "add", Path: "/components/schemas/RepeatType/enum/-", Value: "week"},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("an add of an already-present value must refuse as stale, got: %v", err)
	}
}

func TestUnit_SpecPatch_TestOpGuardsTheShape(t *testing.T) {
	t.Parallel()

	_, err := Apply([]byte(spec), []Patch{{
		Justification: "recording x",
		Operations: []Operation{
			{Op: "test", Path: "/components/schemas/RepeatType/enum/0", Value: "month"},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "test failed") {
		t.Errorf("a failed test op must refuse, got: %v", err)
	}
}

func TestUnit_SpecPatch_ReplaceAndRemove(t *testing.T) {
	t.Parallel()

	out, err := Apply([]byte(spec), []Patch{{
		Justification: "recording x",
		Operations: []Operation{
			{Op: "replace", Path: "/components/schemas/RepeatType/enum/1", Value: "month"},
			{Op: "remove", Path: "/components/schemas/Window/properties/name"},
		},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	doc := decode(t, out)
	if enum := enumOf(t, doc); enum[1] != "month" {
		t.Errorf("enum[1] = %v after replace", enum[1])
	}
	props := doc["components"].(map[string]any)["schemas"].(map[string]any)["Window"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["name"]; ok {
		t.Error("remove left the property behind")
	}
}

func TestUnit_SpecPatch_LoadRequiresJustification(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	patch := `{"operations":[{"op":"add","path":"/x","value":1}]}`
	if err := os.WriteFile(filepath.Join(dir, "a.patch.json"), []byte(patch), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "justification") {
		t.Errorf("a patch without justification must refuse to load, got: %v", err)
	}
}

func TestUnit_SpecPatch_LoadOrdersByName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"b.patch.json", "a.patch.json"} {
		p := `{"justification":"recording x","operations":[{"op":"add","path":"/x","value":1}]}`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(p), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	patches, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(patches) != 2 || !strings.HasSuffix(patches[0].File, "a.patch.json") {
		t.Errorf("patches must apply in name order, got %v", patches)
	}
}

func TestUnit_SpecPatch_LoadToleratesAMissingDirectory(t *testing.T) {
	t.Parallel()

	patches, err := Load(filepath.Join(t.TempDir(), "absent"))
	if err != nil || patches != nil {
		t.Errorf("a missing patches directory is simply no patches, got %v, %v", patches, err)
	}
}
