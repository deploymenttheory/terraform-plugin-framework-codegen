package yamlwalk

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// parse returns the document node and its top mapping.
func parse(t *testing.T, doc string) (*yaml.Node, *yaml.Node) {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		t.Fatal(err)
	}
	return &root, root.Content[0]
}

func TestUnit_ChildValue_FindsAKeyAndAnswersNilOtherwise(t *testing.T) {
	_, top := parse(t, "a: 1\nb:\n  c: 2\n")

	if got := ChildValue(top, "b"); got == nil || ChildValue(got, "c") == nil {
		t.Fatal("ChildValue did not find a nested mapping")
	}
	if got := ChildValue(top, "missing"); got != nil {
		t.Fatalf("ChildValue found %v for a missing key", got)
	}
	if got := ChildValue(nil, "a"); got != nil {
		t.Fatal("ChildValue on nil should answer nil")
	}
	if got := ChildValue(ChildValue(top, "a"), "x"); got != nil {
		t.Fatal("ChildValue on a scalar should answer nil")
	}
}

func TestUnit_StripSchemaDefaults_StripsComponentAndInlineSchemas(t *testing.T) {
	_, top := parse(t, `
openapi: 3.0.3
components:
  schemas:
    Widget:
      type: object
      properties:
        size:
          type: integer
          default: 3
        nested:
          allOf:
            - type: string
              default: deep
paths:
  /widgets:
    post:
      requestBody:
        content:
          application/json:
            schema:
              type: string
              default: inline
      responses:
        "200":
          description: ok
`)

	if got := StripSchemaDefaults(top); got != 3 {
		t.Fatalf("stripped %d defaults, want 3", got)
	}
	out, err := yaml.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "default") {
		t.Fatalf("a default survived:\n%s", out)
	}
}

func TestUnit_StripSchemaDefaults_LeavesDataNamedDefaultAlone(t *testing.T) {
	doc := `
components:
  schemas:
    Widget:
      type: object
      properties:
        default:
          type: string
paths:
  /widgets:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              example:
                default: keep-me
`
	_, top := parse(t, doc)

	if got := StripSchemaDefaults(top); got != 0 {
		t.Fatalf("stripped %d, want 0 — property names and example keys are data", got)
	}
	out, _ := yaml.Marshal(top)
	if !strings.Contains(string(out), "default") {
		t.Fatalf("a legitimate 'default' name was removed:\n%s", out)
	}
}

func TestUnit_StripSchemaDefaults_ReachesParameterSchemas(t *testing.T) {
	_, top := parse(t, `
paths:
  /widgets:
    get:
      parameters:
        - name: limit
          in: query
          schema:
            type: integer
            default: 10
      responses:
        "200":
          description: ok
`)

	if got := StripSchemaDefaults(top); got != 1 {
		t.Fatalf("stripped %d, want the parameter schema's default", got)
	}
}

func TestUnit_StripSchemaDefaults_AnswersZeroWithoutSchemas(t *testing.T) {
	_, top := parse(t, "openapi: 3.0.3\ninfo:\n  title: t\n")
	if got := StripSchemaDefaults(top); got != 0 {
		t.Fatalf("stripped %d in a document with no schemas", got)
	}
}

func TestUnit_ForceBlockStyle_UnfoldsAJSONDocument(t *testing.T) {
	root, _ := parse(t, `{"a": {"b": [1, 2]}, "c": "true"}`)

	ForceBlockStyle(root)
	out, err := yaml.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(out)), "\n"); lines < 3 {
		t.Fatalf("expected block style across lines, got:\n%s", out)
	}
	// The quoted "true" must stay a string, not become a boolean.
	if !strings.Contains(string(out), `"true"`) {
		t.Fatalf("scalar quoting was lost:\n%s", out)
	}
}

func TestUnit_ForceBlockStyle_ToleratesNil(t *testing.T) {
	ForceBlockStyle(nil) // must not panic
}
