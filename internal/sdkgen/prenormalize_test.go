package sdkgen

import (
	"bytes"
	"strings"
	"testing"
)

const prenormalizeSample = `openapi: 3.0.3
info:
  title: sample
  version: 1.0.0
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
components:
  schemas:
    Widget:
      type: object
      properties:
        size:
          type: integer
          default: 3
        blobs:
          type: array
          items:
            type: string
            format: byte
        avatar:
          type: string
          format: byte
    Wrapper:
      allOf:
        - type: object
          properties:
            name:
              type: string
    Composed:
      allOf:
        - $ref: '#/components/schemas/Widget'
`

func TestUnit_Prenormalize_IsDeterministicByteForByte(t *testing.T) {
	first, _, _, err := Prenormalize([]byte(prenormalizeSample))
	if err != nil {
		t.Fatal(err)
	}
	second, _, _, err := Prenormalize([]byte(prenormalizeSample))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two pre-normalizations of the same input differ")
	}

	// And it is a fixed point: pre-normalizing the output changes nothing.
	third, _, _, err := Prenormalize(first)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, third) {
		t.Fatal("pre-normalization is not a fixed point")
	}
}

func TestUnit_Prenormalize_StripsDefaultsCollapsesAllOfsWidensByteArrays(t *testing.T) {
	out, stripped, collapsed, err := Prenormalize([]byte(prenormalizeSample))
	if err != nil {
		t.Fatal(err)
	}
	if stripped != 2 {
		t.Errorf("stripped %d defaults, want 2", stripped)
	}
	if collapsed != 1 {
		t.Errorf("collapsed %d allOfs, want 1", collapsed)
	}

	text := string(out)
	if strings.Contains(text, "default:") {
		t.Errorf("a schema default survived:\n%s", text)
	}
	// The anonymous single-member allOf is hoisted; the $ref composition stays.
	if strings.Count(text, "allOf:") != 1 {
		t.Errorf("expected exactly the $ref allOf to survive:\n%s", text)
	}
	// The array of byte strings is widened; the single byte string stays.
	if strings.Count(text, "format: byte") != 1 {
		t.Errorf("expected exactly the non-collection byte format to survive:\n%s", text)
	}
}

func TestUnit_Prenormalize_EmitsBlockStyleFromAJSONDocument(t *testing.T) {
	json := `{"openapi": "3.0.3", "info": {"title": "t", "version": "1"}, "paths": {}}`

	out, _, _, err := Prenormalize([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(out)), "\n"); lines < 3 {
		t.Fatalf("a JSON document should emerge in block style, got:\n%s", out)
	}
}

func TestUnit_Prenormalize_RefusesUnusableInput(t *testing.T) {
	if _, _, _, err := Prenormalize([]byte(":\n bad\n :")); err == nil {
		t.Error("unparseable YAML should refuse")
	}
	if _, _, _, err := Prenormalize([]byte("")); err == nil {
		t.Error("an empty document should refuse")
	}
}

func TestUnit_FilterPaths_NoGlobsMeansUntouched(t *testing.T) {
	doc := []byte(sampleRevised)
	out, err := FilterPaths(doc, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, doc) {
		t.Fatal("no globs should leave the document untouched")
	}
}

func TestUnit_FilterPaths_IncludeAndExcludeGlobs(t *testing.T) {
	out, err := FilterPaths([]byte(sampleRevised), []string{"/widgets/**", "/widgets"}, []string{"/widgets/{id}"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "/widgets:") {
		t.Errorf("/widgets should be kept:\n%s", text)
	}
	if strings.Contains(text, "/widgets/{id}") {
		t.Errorf("/widgets/{id} should be excluded:\n%s", text)
	}
	if strings.Contains(text, "/internal/health") {
		t.Errorf("/internal/health matches no include and should be gone:\n%s", text)
	}
}

func TestUnit_FilterPaths_SingleStarStaysWithinASegment(t *testing.T) {
	out, err := FilterPaths([]byte(sampleRevised), []string{"/widgets/*"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "/widgets/{id}") {
		t.Fatalf("/widgets/* should match the one-segment child /widgets/{id}:\n%s", text)
	}
	if strings.Contains(text, "  /widgets:") || strings.Contains(text, "/internal/health") {
		t.Fatalf("/widgets/* should match nothing but /widgets/{id}:\n%s", text)
	}
}

func TestUnit_FilterPaths_RefusesWhenNothingSurvives(t *testing.T) {
	_, err := FilterPaths([]byte(sampleRevised), []string{"/nothing/**"}, nil)
	if err == nil || !strings.Contains(err.Error(), "no paths") {
		t.Fatalf("a filter keeping nothing should refuse loudly, got %v", err)
	}
}

func TestUnit_FilterPaths_RefusesUnusableDocuments(t *testing.T) {
	if _, err := FilterPaths([]byte(":\n bad\n :"), []string{"/x"}, nil); err == nil {
		t.Error("unparseable YAML should refuse")
	}
	if _, err := FilterPaths([]byte(""), []string{"/x"}, nil); err == nil {
		t.Error("an empty document should refuse")
	}
	if _, err := FilterPaths([]byte("openapi: 3.0.3\n"), []string{"/x"}, nil); err == nil {
		t.Error("a document without a paths object should refuse")
	}
}
