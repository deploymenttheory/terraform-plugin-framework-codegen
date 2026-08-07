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

func TestUnit_DocPatch_StripSchemaDefaults(t *testing.T) {
	t.Parallel()

	doc := `
openapi: 3.0.1
paths:
  /things:
    post:
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                inline:
                  type: boolean
                  default: true
              example:
                default: kept-in-example
components:
  schemas:
    Thing:
      type: object
      default: whole-schema-default
      properties:
        enabled:
          type: boolean
          default: true
        default:
          type: string
          description: a property literally named default, which must survive
      allOf:
        - type: object
          properties:
            nested:
              type: integer
              default: 7
      items:
        type: string
        default: x
`
	out, err := Apply([]byte(doc), []Patch{{
		Justification: "live 400",
		Operations:    []Operation{{Op: "strip-schema-defaults", Path: "/"}},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var decoded map[string]any
	if err := yaml.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, gone := range []string{"default: true", "default: 7", "default: x", "default: whole-schema-default"} {
		if strings.Contains(text, gone) {
			t.Errorf("a schema default survived: %q", gone)
		}
	}
	if !strings.Contains(text, "a property literally named default") {
		t.Error("the property named default was lost")
	}
	if !strings.Contains(text, "kept-in-example") {
		t.Error("an example's default key was stripped; examples are data")
	}
}

func TestUnit_DocPatch_StripSchemaDefaultsRefusesWhenNoneExist(t *testing.T) {
	t.Parallel()

	doc := "openapi: 3.0.1\ncomponents:\n  schemas:\n    Thing:\n      type: object\n"
	_, err := Apply([]byte(doc), []Patch{{
		Justification: "live 400",
		Operations:    []Operation{{Op: "strip-schema-defaults", Path: "/"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("a document with no defaults must refuse the strip as stale, got: %v", err)
	}
}

// TestUnit_DocPatch_NormalizeStripsAndCollapses covers the built-in
// normalisations: defaults gone everywhere, a single-member anonymous allOf
// hoisted into its parent, and both counted.
func TestUnit_DocPatch_NormalizeStripsAndCollapses(t *testing.T) {
	t.Parallel()

	doc := []byte(`openapi: 3.0.3
components:
  schemas:
    Transfer:
      allOf:
        - type: object
          properties:
            agentId: {type: string, default: abc}
    Composed:
      allOf:
        - $ref: '#/components/schemas/Transfer'
    Wide:
      allOf:
        - type: object
        - type: object
`)

	out, stripped, collapsed, err := Normalize(doc)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if stripped != 1 {
		t.Errorf("stripped = %d, want 1", stripped)
	}
	if collapsed != 1 {
		t.Errorf("collapsed = %d, want 1", collapsed)
	}

	s := string(out)
	if strings.Contains(s, "default:") {
		t.Errorf("a default survived:\n%s", s)
	}
	// The anonymous member was hoisted: its property survives on Transfer.
	if !strings.Contains(s, "agentId") {
		t.Errorf("the hoisted member lost its properties:\n%s", s)
	}
	// A $ref member is a real composition and stays; a multi-member list stays.
	if got := strings.Count(s, "allOf:"); got != 2 {
		t.Errorf("allOf count = %d, want the $ref and multi-member compositions kept (2):\n%s", got, s)
	}
}

// TestUnit_DocPatch_NormalizeAcceptsAQuietDocument pins the difference from the
// patch form of the same operations: zero hits is a fine answer for a built-in.
func TestUnit_DocPatch_NormalizeAcceptsAQuietDocument(t *testing.T) {
	t.Parallel()

	out, stripped, collapsed, err := Normalize([]byte("openapi: 3.0.3\npaths: {}\n"))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if stripped != 0 || collapsed != 0 {
		t.Errorf("counts = %d, %d, want 0, 0", stripped, collapsed)
	}
	if len(out) == 0 {
		t.Errorf("the document should round-trip")
	}
}

// TestUnit_DocPatch_CollapseRefusesAnOverlap: a member key already declared on
// the parent would force a merge decision the pass must not guess.
func TestUnit_DocPatch_CollapseRefusesAnOverlap(t *testing.T) {
	t.Parallel()

	doc := []byte(`openapi: 3.0.3
components:
  schemas:
    Clash:
      type: string
      allOf:
        - type: object
`)

	_, _, collapsed, err := Normalize(doc)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if collapsed != 0 {
		t.Errorf("collapsed = %d, want the overlapping member left alone", collapsed)
	}
}

// TestUnit_DocPatch_NormalizeWidensByteArrayCollections covers the shape a
// second API found: kiota generates WriteCollectionOfByteArrayValues for an
// array of format: byte strings, and its own runtime does not implement that
// method, so the generated SDK does not compile.
func TestUnit_DocPatch_NormalizeWidensByteArrayCollections(t *testing.T) {
	t.Parallel()

	doc := []byte(`openapi: 3.0.3
components:
  schemas:
    Certificate:
      type: object
      properties:
        data:
          type: array
          items: {type: string, format: byte}
        single:
          type: string
          format: byte
`)

	out, _, _, err := Normalize(doc)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	s := string(out)

	// The element of the collection loses its byte format...
	if strings.Contains(s, "items:") && strings.Contains(s, "items:\n                    type: string\n                    format: byte") {
		t.Errorf("the array element kept format: byte:\n%s", s)
	}
	if strings.Count(s, "format: byte") != 1 {
		t.Errorf("exactly the standalone byte string should keep its format, got %d occurrence(s):\n%s",
			strings.Count(s, "format: byte"), s)
	}
	// ...and the standalone one keeps it, because WriteByteArrayValue exists.
	if !strings.Contains(s, "single:") {
		t.Errorf("the standalone byte field was lost entirely:\n%s", s)
	}
}
