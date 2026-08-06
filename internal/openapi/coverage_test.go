package openapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

func loadSpec(t *testing.T, body string) *Document {
	t.Helper()

	path := filepath.Join(t.TempDir(), "api.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return doc
}

func TestUnit_OpenAPI_LoadFailures(t *testing.T) {
	t.Parallel()

	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("a missing file should fail")
	}

	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("this: is: not: openapi"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("a document that is not OpenAPI should fail")
	}
}

func TestUnit_OpenAPI_DiscoverHandlesAnEmptyDocument(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t, `
openapi: 3.0.3
info: {title: Empty, version: "1"}
paths: {}
`)

	if got := doc.Discover(); len(got) != 0 {
		t.Errorf("an empty document should yield no candidates, got %v", keys(got))
	}

	// A zero Document must not panic either; Load is not the only way to get one.
	var zero Document
	if got := zero.Discover(); got != nil {
		t.Errorf("a zero document should yield nothing, got %v", got)
	}
}

// TestUnit_OpenAPI_TagFallsBackToAnyOperation covers a candidate whose CRUD slots
// carry no tag but whose extra operations do.
func TestUnit_OpenAPI_TagFallsBackToAnyOperation(t *testing.T) {
	t.Parallel()

	c := Candidate{Extra: []Operation{{Tags: []string{"Fallback"}}}}
	if got := firstTag(c); got != "Fallback" {
		t.Errorf("firstTag = %q, want Fallback", got)
	}

	if got := firstTag(Candidate{}); got != "" {
		t.Errorf("firstTag with no tags = %q, want empty", got)
	}
}

// TestUnit_OpenAPI_ContentTypeFallback: a vendor JSON media type must still be
// found, or the resource silently comes out with no attributes.
func TestUnit_OpenAPI_ContentTypeFallback(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t, `
openapi: 3.0.3
info: {title: Vendor, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [Things]
      requestBody:
        content:
          application/vnd.example.v2+json:
            schema:
              type: object
              required: [name]
              properties:
                name: {type: string}
      responses:
        '201':
          content:
            application/vnd.example.v2+json:
              schema:
                type: object
                properties:
                  id: {type: string, readOnly: true}
                  name: {type: string}
  /things/{id}:
    get:
      operationId: getThing
      tags: [Things]
      responses:
        '200':
          content:
            application/vnd.example.v2+json:
              schema:
                type: object
                properties:
                  id: {type: string, readOnly: true}
                  name: {type: string}
`)

	res, _, err := doc.Infer(find(t, doc.Discover(), "thing"), inferOptions())
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	if len(res.Schema.Attributes) != 2 {
		t.Fatalf("got %d attributes, want 2: a vendor JSON media type must still be read", len(res.Schema.Attributes))
	}
}

// TestUnit_OpenAPI_AllOfIsMerged covers composition, which is how specifications
// express "this, plus the common envelope".
func TestUnit_OpenAPI_AllOfIsMerged(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t, `
openapi: 3.0.3
info: {title: Composed, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [Things]
      requestBody:
        content:
          application/json:
            schema:
              allOf:
                - type: object
                  properties:
                    base: {type: string}
                - type: object
                  required: [name]
                  properties:
                    name: {type: string}
      responses:
        '201':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string, readOnly: true}
                  base: {type: string}
                  name: {type: string}
  /things/{id}:
    get:
      operationId: getThing
      tags: [Things]
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string, readOnly: true}
                  base: {type: string}
                  name: {type: string}
`)

	res, _, err := doc.Infer(find(t, doc.Discover(), "thing"), inferOptions())
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	names := attrNames(res)
	for _, want := range []string{"base", "name", "id"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("composed property %q was lost; got %v", want, names)
		}
	}
}

func TestUnit_OpenAPI_SkipField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		f    Field
		skip bool
		why  string
	}{
		{"ordinary", Field{Name: "colour", Kind: blueprint.KindString}, false, ""},
		{"hal links", Field{Name: "_links", Kind: blueprint.KindSingleNested}, true, "hypermedia"},
		{"hal embedded", Field{Name: "_embedded", Kind: blueprint.KindSingleNested}, true, "hypermedia"},
		{"unmapped kind", Field{Name: "x"}, true, "no framework type"},
		{
			"nested with no schema name",
			Field{Name: "x", Kind: blueprint.KindSetNested},
			true, "no named schema",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			skip, why := skipField(tc.f)
			if skip != tc.skip {
				t.Fatalf("skipField = %v (%s), want %v", skip, why, tc.skip)
			}
			if tc.why != "" && !strings.Contains(why, tc.why) {
				t.Errorf("reason = %q, want it to mention %q", why, tc.why)
			}
		})
	}
}

func TestUnit_OpenAPI_ResourceWithNoInferableAttributesFails(t *testing.T) {
	t.Parallel()

	// Both bodies are bare objects with no properties, so nothing can be mapped.
	doc := loadSpec(t, `
openapi: 3.0.3
info: {title: Bare, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [Things]
      responses:
        '201': {}
  /things/{id}:
    get:
      operationId: getThing
      tags: [Things]
      responses:
        '200': {}
`)

	_, _, err := doc.Infer(find(t, doc.Discover(), "thing"), inferOptions())
	if err == nil {
		t.Fatal("a resource with no attributes must fail rather than emit an empty schema")
	}
	if !strings.Contains(err.Error(), "no attributes") {
		t.Errorf("error should say why: %v", err)
	}
}

// TestUnit_OpenAPI_MissingIdIsReported: without an identifier there is nothing to
// read, import or delete by, and that has to reach the operator.
func TestUnit_OpenAPI_MissingIdIsReported(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t, `
openapi: 3.0.3
info: {title: NoID, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [Things]
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                name: {type: string}
      responses:
        '201':
          content:
            application/json:
              schema:
                type: object
                properties:
                  name: {type: string}
  /things/{id}:
    get:
      operationId: getThing
      tags: [Things]
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  name: {type: string}
`)

	_, notes, err := doc.Infer(find(t, doc.Discover(), "thing"), inferOptions())
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	if !strings.Contains(strings.Join(noteStrings(notes), "\n"), "id") {
		t.Errorf("a missing identifier should be reported: %v", noteStrings(notes))
	}
}

func TestUnit_OpenAPI_KindOfEdgeCases(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t, `
openapi: 3.0.3
info: {title: Kinds, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [Things]
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                plainMap:
                  type: object
                  additionalProperties: {type: string}
                nullableName:
                  type: string
                  nullable: true
                doubleValue:
                  type: number
                  format: double
                emptyArray:
                  type: array
                untypedThing: {}
      responses:
        '201':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string, readOnly: true}
  /things/{id}:
    get:
      operationId: getThing
      tags: [Things]
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string, readOnly: true}
`)

	res, notes, err := doc.Infer(find(t, doc.Discover(), "thing"), inferOptions())
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	byName := map[string]blueprint.TypeKind{}
	for _, a := range res.Schema.Attributes {
		byName[a.Name] = a.Type.Kind
	}

	// A free-form map is refused by name rather than drafted: no conversion is
	// generated for the additional-data shape, and both curated pilots dropped
	// every such field.
	if _, drafted := byName["plain_map"]; drafted {
		t.Errorf("an object with only additionalProperties must be skipped, but plain_map was drafted")
	}
	// OpenAPI 3.0 nullability must not turn a string into an unmappable type.
	if got := byName["nullable_name"]; got != blueprint.KindString {
		t.Errorf("a nullable string should stay a string, got %q", got)
	}
	if got := byName["double_value"]; got != blueprint.KindFloat64 {
		t.Errorf("a double should be float64, got %q", got)
	}

	// Shapes with no mapping are reported rather than dropped in silence.
	reported := strings.Join(noteStrings(notes), "\n")
	if !strings.Contains(reported, "plainMap") {
		t.Errorf("the skipped map should be reported by name:\n%s", reported)
	}
	for _, field := range []string{"empty_array", "emptyArray", "untyped", "untypedThing"} {
		if strings.Contains(reported, field) {
			return
		}
	}
	t.Errorf("an unmappable shape should be reported:\n%s", reported)
}

func TestUnit_OpenAPI_ReplaceOrAppend(t *testing.T) {
	t.Parallel()

	in := []Field{{Name: "a", Description: "first"}, {Name: "b"}}

	got := replaceOrAppend(in, Field{Name: "a", Description: "second"})
	if len(got) != 2 {
		t.Fatalf("replacing should not grow the slice: %d", len(got))
	}
	if got[0].Description != "second" {
		t.Errorf("the later field should win: %q", got[0].Description)
	}

	got = replaceOrAppend(got, Field{Name: "c"})
	if len(got) != 3 {
		t.Errorf("a new name should append: %d", len(got))
	}
}

func TestUnit_OpenAPI_FieldsOfNilSchema(t *testing.T) {
	t.Parallel()

	if got := Fields(nil); got != nil {
		t.Errorf("Fields(nil) = %v, want nil", got)
	}
	if got := resolve(nil); got != nil {
		t.Errorf("resolve(nil) = %v, want nil", got)
	}
}

func TestUnit_OpenAPI_SummaryOf(t *testing.T) {
	t.Parallel()

	if got := summaryOf(Candidate{Read: &Operation{Summary: "retrieve tag"}}); got != "Retrieve tag." {
		t.Errorf("summaryOf = %q", got)
	}
	if got := summaryOf(Candidate{}); got != "" {
		t.Errorf("summaryOf with nothing = %q", got)
	}
}
