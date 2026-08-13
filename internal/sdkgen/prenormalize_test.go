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

// unionSample carries every union shape the reduction has to answer: a union
// inline on a response (the shape that broke a real build), a union of $refs
// beside a discriminator, a union whose parent already declares one of the
// branch's keys, and a branch that is itself a union.
const unionSample = `openapi: 3.0.3
info:
  title: unions
  version: 1.0.0
paths:
  /starred:
    get:
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                anyOf:
                  - type: array
                    items:
                      $ref: '#/components/schemas/Starred'
                  - type: array
                    items:
                      $ref: '#/components/schemas/Repo'
components:
  schemas:
    Starred:
      type: object
      properties:
        at:
          type: string
    Repo:
      type: object
      properties:
        name:
          type: string
    Gate:
      description: kept from the parent
      discriminator:
        propertyName: kind
      oneOf:
        - description: dropped with the branch
          type: object
          properties:
            a:
              type: string
        - type: object
          properties:
            b:
              type: string
    Nested:
      anyOf:
        - oneOf:
            - type: string
            - type: integer
`

func TestUnit_Prenormalize_ReducesAUnionToItsFirstBranch(t *testing.T) {
	out, _, _, err := Prenormalize([]byte(unionSample))
	if err != nil {
		t.Fatalf("Prenormalize: %v", err)
	}
	got := string(out)

	if strings.Contains(got, "anyOf") || strings.Contains(got, "oneOf") {
		t.Fatalf("no union keyword may survive:\n%s", got)
	}
	// The response keeps the first branch's array-of-Starred, not Repo.
	if !strings.Contains(got, "Starred") {
		t.Fatalf("the first branch must survive:\n%s", got)
	}
	if strings.Contains(got, "'#/components/schemas/Repo'") ||
		strings.Contains(got, "\"#/components/schemas/Repo\"") {
		t.Fatalf("the losing branch must not be referenced by the response:\n%s", got)
	}
}

func TestUnit_Prenormalize_UnionReductionKeepsTheParentAndDropsTheDiscriminator(t *testing.T) {
	out, _, _, err := Prenormalize([]byte(unionSample))
	if err != nil {
		t.Fatalf("Prenormalize: %v", err)
	}
	got := string(out)

	if !strings.Contains(got, "kept from the parent") {
		t.Fatalf("a key the parent declares must win over the branch's:\n%s", got)
	}
	if strings.Contains(got, "dropped with the branch") {
		t.Fatalf("the branch's own spelling of a parent key must not overwrite it:\n%s", got)
	}
	if strings.Contains(got, "discriminator") {
		t.Fatalf("the discriminator must go with the union it selected between:\n%s", got)
	}
}

func TestUnit_Prenormalize_UnionReductionFoldsUntilNoUnionRemains(t *testing.T) {
	out, _, _, err := Prenormalize([]byte(unionSample))
	if err != nil {
		t.Fatalf("Prenormalize: %v", err)
	}
	// Nested's anyOf branch is itself a oneOf; one pass would leave it.
	if strings.Contains(string(out), "oneOf") {
		t.Fatalf("a union exposed by folding must be folded too:\n%s", out)
	}
}

func TestUnit_Prenormalize_UnionReductionIsDeterministic(t *testing.T) {
	first, _, _, err := Prenormalize([]byte(unionSample))
	if err != nil {
		t.Fatalf("Prenormalize: %v", err)
	}
	for i := range 5 {
		again, _, _, err := Prenormalize([]byte(unionSample))
		if err != nil {
			t.Fatalf("Prenormalize (run %d): %v", i, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("run %d differs; the reduction must be byte-stable", i)
		}
	}
}

func TestUnit_Prenormalize_LeavesADocumentWithoutUnionsAlone(t *testing.T) {
	out, _, _, err := Prenormalize([]byte(prenormalizeSample))
	if err != nil {
		t.Fatalf("Prenormalize: %v", err)
	}
	// Zero hits is not an error: the sample carries no union, and reducing
	// nothing must not perturb what the other passes produced.
	again, _, _, err := Prenormalize(out)
	if err != nil {
		t.Fatalf("Prenormalize (second): %v", err)
	}
	if !bytes.Equal(out, again) {
		t.Fatal("prenormalizing an already-prenormalized document must be a fixed point")
	}
}
