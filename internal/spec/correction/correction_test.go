package correction

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

func TestUnit_Correction_AddsAnEnumValue(t *testing.T) {
	t.Parallel()

	out, err := Apply([]byte(spec), []Correction{{
		Justification: "observation x",
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

func TestUnit_Correction_RefusesAStaleAdd(t *testing.T) {
	t.Parallel()

	_, err := Apply([]byte(spec), []Correction{{
		Justification: "observation x",
		Operations: []Operation{
			{Op: "add", Path: "/components/schemas/RepeatType/enum/-", Value: "week"},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("an add of an already-present value must refuse as stale, got: %v", err)
	}
}

func TestUnit_Correction_RefusesAStaleMappingAdd(t *testing.T) {
	t.Parallel()

	_, err := Apply([]byte(spec), []Correction{{
		Justification: "observation x",
		Operations: []Operation{
			{Op: "add", Path: "/components/schemas/RepeatType/type", Value: "string"},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("a mapping add of an already-present value must refuse as stale, got: %v", err)
	}
}

func TestUnit_Correction_TestOpGuardsTheShape(t *testing.T) {
	t.Parallel()

	_, err := Apply([]byte(spec), []Correction{{
		Justification: "observation x",
		Operations: []Operation{
			{Op: "test", Path: "/components/schemas/RepeatType/enum/0", Value: "month"},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "test failed") {
		t.Errorf("a failed test op must refuse, got: %v", err)
	}
}

func TestUnit_Correction_ReplaceAndRemove(t *testing.T) {
	t.Parallel()

	out, err := Apply([]byte(spec), []Correction{{
		Justification: "observation x",
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

func TestUnit_Correction_MappingLevelOperations(t *testing.T) {
	t.Parallel()

	out, err := Apply([]byte(spec), []Correction{{
		Justification: "observation x",
		Operations: []Operation{
			{Op: "test", Path: "/components/schemas/Window/type", Value: "object"},
			{Op: "add", Path: "/components/schemas/Window/properties/name/maxLength", Value: 64},
			{Op: "replace", Path: "/components/schemas/Window/type", Value: "object"},
			{Op: "remove", Path: "/openapi"},
		},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	doc := decode(t, out)
	if _, ok := doc["openapi"]; ok {
		t.Error("remove left the top-level key behind")
	}
}

func TestUnit_Correction_SequenceInsertAtIndex(t *testing.T) {
	t.Parallel()

	out, err := Apply([]byte(spec), []Correction{{
		Justification: "observation x",
		Operations: []Operation{
			{Op: "add", Path: "/components/schemas/RepeatType/enum/0", Value: "hour"},
			{Op: "remove", Path: "/components/schemas/RepeatType/enum/2"},
		},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	enum := enumOf(t, decode(t, out))
	if len(enum) != 2 || enum[0] != "hour" || enum[1] != "day" {
		t.Errorf("enum = %v, want hour, day", enum)
	}
}

func TestUnit_Correction_RefusalTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		op      Operation
		wantErr string
	}{
		{"unsupported op", Operation{Op: "move", Path: "/a"}, `unsupported op "move"`},
		{"whole-document path", Operation{Op: "add", Path: "", Value: 1}, "not correctable"},
		{"non-pointer path", Operation{Op: "add", Path: "a/b", Value: 1}, "not a JSON pointer"},
		{"replace of nothing", Operation{Op: "replace", Path: "/components/schemas/Missing", Value: 1}, "nothing exists"},
		{"remove of nothing", Operation{Op: "remove", Path: "/components/schemas/Missing"}, "nothing exists"},
		{"test of nothing", Operation{Op: "test", Path: "/components/schemas/Missing", Value: 1}, "test failed"},
		{"descend through nothing", Operation{Op: "add", Path: "/components/absent/deeper", Value: 1}, "nothing exists"},
		{"bad array index", Operation{Op: "replace", Path: "/components/schemas/RepeatType/enum/9", Value: "x"},
			"not an index inside an array"},
		{"descend through bad index", Operation{Op: "add", Path: "/components/schemas/RepeatType/enum/9/x", Value: 1},
			"not an index inside an array"},
		{"append with non-add", Operation{Op: "remove", Path: "/components/schemas/RepeatType/enum/-"},
			"only add can address the end"},
		{"child of a scalar", Operation{Op: "add", Path: "/openapi/child/deeper", Value: 1}, "child of a scalar"},
		{"scalar parent", Operation{Op: "add", Path: "/openapi/child", Value: 1}, "neither an object nor an array"},
		{"test failed on sequence index", Operation{Op: "test", Path: "/components/schemas/RepeatType/enum/0", Value: "no"},
			"test failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Apply([]byte(spec), []Correction{{Justification: "observation x", Operations: []Operation{tc.op}}})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestUnit_Correction_ApplyRefusesUnusableYAML(t *testing.T) {
	t.Parallel()

	if _, err := Apply([]byte("a: [b\n"), nil); err == nil || !strings.Contains(err.Error(), "not usable YAML") {
		t.Errorf("unusable YAML must refuse, got: %v", err)
	}
	if _, err := Apply(nil, nil); err == nil || !strings.Contains(err.Error(), "single YAML document") {
		t.Errorf("an empty document must refuse, got: %v", err)
	}
}

func TestUnit_Correction_LoadRequiresJustification(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c := `{"operations":[{"op":"add","path":"/x","value":1}]}`
	if err := os.WriteFile(filepath.Join(dir, "a.correction.json"), []byte(c), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "justification") {
		t.Errorf("a correction without justification must refuse to load, got: %v", err)
	}
}

func TestUnit_Correction_LoadRequiresOperations(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c := `{"justification":"observation x"}`
	if err := os.WriteFile(filepath.Join(dir, "a.correction.json"), []byte(c), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "no operations") {
		t.Errorf("a correction without operations must refuse to load, got: %v", err)
	}
}

func TestUnit_Correction_LoadRefusesMalformedJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.correction.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "not a usable correction") {
		t.Errorf("malformed JSON must refuse to load, got: %v", err)
	}
}

func TestUnit_Correction_LoadCarriesTheEvidencePointer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c := `{"justification":"the live API echoes none",
	      "evidence":"audit/observations/alert_rule.observations.json#a1b2c3",
	      "operations":[{"op":"add","path":"/x","value":1}]}`
	if err := os.WriteFile(filepath.Join(dir, "a.correction.json"), []byte(c), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Evidence != "audit/observations/alert_rule.observations.json#a1b2c3" {
		t.Errorf("evidence pointer did not survive the load: %+v", loaded)
	}
}

func TestUnit_Correction_LoadOrdersByNameAndSkipsStrangers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"b.correction.json", "a.correction.json"} {
		c := `{"justification":"observation x","operations":[{"op":"add","path":"/x","value":1}]}`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "rejected"), 0o755); err != nil {
		t.Fatal(err)
	}

	corrections, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(corrections) != 2 || !strings.HasSuffix(corrections[0].File, "a.correction.json") {
		t.Errorf("corrections must apply in name order and skip non-correction files, got %+v", corrections)
	}
}

func TestUnit_Correction_LoadToleratesAMissingDirectory(t *testing.T) {
	t.Parallel()

	corrections, err := Load(filepath.Join(t.TempDir(), "absent"))
	if err != nil || corrections != nil {
		t.Errorf("a missing corrections directory is simply no corrections, got %v, %v", corrections, err)
	}
}

func TestUnit_Correction_StripSchemaDefaults(t *testing.T) {
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
	out, err := Apply([]byte(doc), []Correction{{
		Justification: "live 400",
		Operations:    []Operation{{Op: "strip-schema-defaults", Path: "/"}},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
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

func TestUnit_Correction_StripSchemaDefaultsRefusesWhenNoneExist(t *testing.T) {
	t.Parallel()

	doc := "openapi: 3.0.1\ncomponents:\n  schemas:\n    Thing:\n      type: object\n"
	_, err := Apply([]byte(doc), []Correction{{
		Justification: "live 400",
		Operations:    []Operation{{Op: "strip-schema-defaults", Path: "/"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("a document with no defaults must refuse the strip as stale, got: %v", err)
	}
}

// A JSON document is valid YAML in flow style, so without forceBlockStyle a
// corrected JSON-published document would come back as one enormous line —
// the shape that silently broke a 7 MB document in v1.
func TestUnit_Correction_JSONSourceComesBackAsBlockYAML(t *testing.T) {
	t.Parallel()

	doc := []byte(`{"openapi": "3.0.3", "info": {"title": "T", "version": "1"},
	  "paths": {"/a": {"get": {"responses": {"200": {"description": "ok"}}}},
	            "/b": {"get": {"responses": {"200": {"description": "ok"}}}}},
	  "components": {"schemas": {"S": {"type": "object",
	    "properties": {"x": {"type": "string"}, "y": {"type": "integer"}}}}}}`)

	out, err := Apply(doc, []Correction{{
		Justification: "observation x",
		Operations:    []Operation{{Op: "add", Path: "/components/schemas/S/properties/x/maxLength", Value: 10}},
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	s := string(out)
	if lines := strings.Count(s, "\n"); lines < 10 {
		t.Errorf("output has %d line(s); a flow-style document was emitted as one line:\n%s", lines, s)
	}
	for _, line := range strings.Split(s, "\n") {
		if len(line) > 200 {
			t.Errorf("a line is %d characters long, which is flow style leaking through", len(line))
		}
	}

	var got map[string]any
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("the emitted document does not parse: %v", err)
	}
	if paths, _ := got["paths"].(map[string]any); len(paths) != 2 {
		t.Errorf("paths = %d, want the 2 that went in", len(paths))
	}
}
