package specmodel

import (
	"strings"
	"testing"
)

// tagSpec is a complete small document exercising most of what Load reads:
// path and operation parameters, request bodies, responses, references,
// enums, formats, readOnly, required, and a self-referencing schema.
const tagSpec = `openapi: 3.0.3
info:
  title: Tag API
  version: "1.2.3"
servers:
  - url: https://api.example.invalid/v1
paths:
  /tags/{tagId}:
    parameters:
      - name: tagId
        in: path
        schema:
          type: string
      - name: expand
        in: query
        schema:
          type: string
    get:
      operationId: getTag
      parameters:
        - name: expand
          in: query
          required: true
          schema:
            type: string
        - name: filter
          in: query
          schema:
            type: string
      responses:
        "200":
          description: the tag
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Tag'
    delete:
      operationId: deleteTag
      responses:
        "204":
          description: gone
    patch:
      operationId: updateTag
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Tag'
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Tag'
  /tags:
    get:
      operationId: listTags
      responses:
        200:
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Tag'
    post:
      operationId: createTag
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Tag'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Tag'
components:
  schemas:
    Tag:
      type: object
      required: [name]
      properties:
        id:
          type: string
          readOnly: true
        name:
          type: string
        color:
          type: string
          format: color-hex
          enum: [red, blue, 7]
        parent:
          $ref: '#/components/schemas/Tag'
`

func TestUnit_Specmodel_LoadReadsTheDocument(t *testing.T) {
	doc, err := Load([]byte(tagSpec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if doc.OpenAPI != "3.0.3" {
		t.Errorf("OpenAPI = %q, want 3.0.3", doc.OpenAPI)
	}
	if doc.Info.Title != "Tag API" || doc.Info.Version != "1.2.3" {
		t.Errorf("Info = %+v", doc.Info)
	}
	if len(doc.Servers) != 1 || doc.Servers[0].URL != "https://api.example.invalid/v1" {
		t.Errorf("Servers = %+v", doc.Servers)
	}

	// Paths sort by template regardless of document order.
	if len(doc.Paths) != 2 || doc.Paths[0].Path != "/tags" || doc.Paths[1].Path != "/tags/{tagId}" {
		t.Fatalf("paths out of order: %+v", doc.Paths)
	}

	// Operations arrive in the fixed method order, not document order.
	var methodsSeen []string
	for _, op := range doc.Paths[1].Operations {
		methodsSeen = append(methodsSeen, op.Method)
	}
	if got := strings.Join(methodsSeen, ","); got != "GET,DELETE,PATCH" {
		t.Errorf("item methods = %s, want GET,DELETE,PATCH", got)
	}

	// The item GET combines path-item and operation parameters, its own
	// spelling of "expand" winning, and the path parameter is required
	// even though the author never said so.
	get := doc.Paths[1].Operations[0]
	if get.OperationID != "getTag" {
		t.Fatalf("operationId = %q", get.OperationID)
	}
	if len(get.Parameters) != 3 {
		t.Fatalf("parameters = %+v", get.Parameters)
	}
	if p := get.Parameters[0]; p.In != "path" || p.Name != "tagId" || !p.Required {
		t.Errorf("path parameter = %+v; a path parameter is required by definition", p)
	}
	if p := get.Parameters[1]; p.In != "query" || p.Name != "expand" || !p.Required {
		t.Errorf("query parameter = %+v; the operation's own spelling must win", p)
	}
	if p := get.Parameters[2]; p.Name != "filter" || p.Required {
		t.Errorf("query parameters must sort by name: %+v", get.Parameters)
	}

	// The unquoted `200:` response key reads as the string "200".
	list := doc.Paths[0].Operations[0]
	if len(list.Responses) != 1 || list.Responses[0].Status != "200" {
		t.Fatalf("list responses = %+v", list.Responses)
	}
	if list.Responses[0].Schema == nil || list.Responses[0].Schema.Type != "array" {
		t.Fatalf("list schema = %+v", list.Responses[0].Schema)
	}
	if items := list.Responses[0].Schema.Items; items == nil || items.Resolved().Name != "Tag" {
		t.Errorf("list items should resolve to Tag")
	}

	// The create request body resolves to the named schema.
	create := doc.Paths[0].Operations[1]
	if create.RequestBody == nil || create.RequestBody.Ref != "Tag" {
		t.Fatalf("create body = %+v", create.RequestBody)
	}
	tag := create.RequestBody.Resolved()
	if tag.Name != "Tag" || tag.Type != "object" {
		t.Fatalf("resolved = %+v", tag)
	}

	// Schema fields survive: required, readOnly, format, enum, property order.
	if !tag.IsRequired("name") || tag.IsRequired("id") {
		t.Errorf("required = %+v", tag.Required)
	}
	if id, ok := tag.Property("id"); !ok || !id.ReadOnly {
		t.Errorf("id should be readOnly")
	}
	color, ok := tag.Property("color")
	if !ok || color.Format != "color-hex" {
		t.Errorf("color = %+v", color)
	}
	if len(color.Enum) != 3 || color.Enum[0] != "red" || color.Enum[2] != 7 {
		t.Errorf("enum = %+v; scalars must keep their YAML types", color.Enum)
	}
	var names []string
	for _, p := range tag.Properties {
		names = append(names, p.Name)
	}
	if got := strings.Join(names, ","); got != "id,name,color,parent" {
		t.Errorf("property order = %s; document order is load-bearing", got)
	}

	// The self reference resolves without looping.
	parent, _ := tag.Property("parent")
	if parent.Resolved() != tag {
		t.Errorf("parent should resolve to Tag itself")
	}
	if _, ok := tag.Property("nope"); ok {
		t.Errorf("Property(nope) should not exist")
	}

	// Flattened accessors.
	if ops := doc.Operations(); len(ops) != 5 {
		t.Errorf("Operations() = %d ops", len(ops))
	}
	if _, ok := doc.Schema("Tag"); !ok {
		t.Errorf("Schema(Tag) not found")
	}
	if _, ok := doc.Schema("Missing"); ok {
		t.Errorf("Schema(Missing) should not exist")
	}
}

func TestUnit_Specmodel_LoadAcceptsJSON(t *testing.T) {
	doc, err := Load([]byte(`{
	  "openapi": "3.1.0",
	  "info": {"title": "J", "version": "9"},
	  "paths": {
	    "/things": {
	      "get": {
	        "operationId": "listThings",
	        "responses": {"200": {"content": {"application/json": {"schema": {"type": "array"}}}}}
	      }
	    }
	  }
	}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if doc.OpenAPI != "3.1.0" || doc.Info.Version != "9" {
		t.Errorf("doc = %+v", doc)
	}
	if len(doc.Paths) != 1 || doc.Paths[0].Operations[0].OperationID != "listThings" {
		t.Errorf("paths = %+v", doc.Paths)
	}
}

func TestUnit_Specmodel_ComponentReferencesAndCompositions(t *testing.T) {
	doc, err := Load([]byte(`openapi: 3.0.3
info: {title: C, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      parameters:
        - $ref: '#/components/parameters/Verbose'
      requestBody:
        $ref: '#/components/requestBodies/ThingBody'
      responses:
        "201":
          $ref: '#/components/responses/ThingResponse'
components:
  parameters:
    Verbose:
      name: verbose
      in: query
      schema:
        type: boolean
  requestBodies:
    ThingBody:
      content:
        application/json:
          schema:
            $ref: '#/components/schemas/Thing'
  responses:
    ThingResponse:
      description: one thing
      content:
        application/hal+json:
          schema:
            $ref: '#/components/schemas/Thing'
  schemas:
    Base:
      type: object
      properties:
        id: {type: string}
    Thing:
      allOf:
        - $ref: '#/components/schemas/Base'
        - type: object
          properties:
            kind:
              oneOf:
                - {type: string}
                - {type: integer}
            note:
              anyOf:
                - {type: string}
            label:
              type: [string, "null"]
            void:
              type: ["null"]
            freeform: true
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	op := doc.Paths[0].Operations[0]
	if len(op.Parameters) != 1 || op.Parameters[0].Name != "verbose" || op.Parameters[0].Schema.Type != "boolean" {
		t.Fatalf("parameters = %+v", op.Parameters)
	}
	if op.RequestBody == nil || op.RequestBody.Resolved().Name != "Thing" {
		t.Fatalf("request body = %+v", op.RequestBody)
	}
	// application/hal+json still counts as JSON content.
	if op.Responses[0].Schema == nil || op.Responses[0].Schema.Resolved().Name != "Thing" {
		t.Fatalf("response = %+v", op.Responses[0])
	}

	thing := doc.Schemas["Thing"]
	if len(thing.AllOf) != 2 || thing.AllOf[0].Resolved().Name != "Base" {
		t.Fatalf("allOf = %+v", thing.AllOf)
	}
	inline := thing.AllOf[1]
	kind, _ := inline.Property("kind")
	if len(kind.OneOf) != 2 || kind.OneOf[0].Type != "string" || kind.OneOf[1].Type != "integer" {
		t.Errorf("oneOf = %+v", kind.OneOf)
	}
	note, _ := inline.Property("note")
	if len(note.AnyOf) != 1 {
		t.Errorf("anyOf = %+v", note.AnyOf)
	}
	// A 3.1 type array collapses to its first non-null entry.
	label, _ := inline.Property("label")
	if label.Type != "string" {
		t.Errorf("label type = %q, want string", label.Type)
	}
	if void, _ := inline.Property("void"); void.Type != "null" {
		t.Errorf("void type = %q, want null", void.Type)
	}
	// A boolean schema reads as an empty schema rather than an error.
	if _, ok := inline.Property("freeform"); !ok {
		t.Errorf("freeform should exist as an empty schema")
	}
}

// minimal wraps one fragment into an otherwise valid document, so each
// refusal below is provoked in isolation.
func minimal(fragment string) string {
	return "openapi: 3.0.3\ninfo: {title: T, version: \"1\"}\n" + fragment
}

func TestUnit_Specmodel_LoadRefusals(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"garbage bytes", "\t{[", "parsing the document"},
		{"empty document", "", "the document is empty"},
		{"scalar root", "just a string", "the document root is not a mapping"},
		{"swagger 2.0", "swagger: \"2.0\"\ninfo: {title: S, version: \"1\"}", "Swagger 2.0 document"},
		{"no openapi field", "info: {title: N, version: \"1\"}", "not an OpenAPI document"},
		{"openapi 4.0", "openapi: \"4.0.0\"\ninfo: {title: F, version: \"1\"}", `version "4.0.0" is not supported`},
		{"missing info.version", "openapi: 3.0.3\ninfo: {title: V}", "declares no info.version"},

		{"remote url reference", minimal(`components:
  schemas:
    A:
      $ref: 'https://example.invalid/openapi.yaml#/components/schemas/B'
`), "remote reference"},
		{"remote file reference", minimal(`components:
  schemas:
    A:
      $ref: 'common.yaml#/components/schemas/B'
`), "remote reference"},
		{"pointer outside components.schemas", minimal(`components:
  schemas:
    A:
      $ref: '#/definitions/B'
`), "only #/components/schemas/<name> is"},
		{"pointer into a schema's interior", minimal(`components:
  schemas:
    A:
      $ref: '#/components/schemas/B/properties/c'
`), "does not name a single component"},
		{"empty pointer", minimal(`components:
  schemas:
    A:
      $ref: '#/components/schemas/'
`), "does not name a single component"},
		{"dangling schema reference", minimal(`components:
  schemas:
    A:
      $ref: '#/components/schemas/Ghost'
`), "references #/components/schemas/Ghost, which the document does not declare"},

		{"component parameter by reference", minimal(`components:
  parameters:
    P:
      $ref: '#/components/parameters/Q'
`), "must be declared inline"},
		{"dangling parameter reference", minimal(`paths:
  /a:
    get:
      parameters:
        - $ref: '#/components/parameters/Ghost'
      responses: {}
`), "references #/components/parameters/Ghost"},
		{"remote request body reference", minimal(`paths:
  /a:
    post:
      requestBody:
        $ref: 'https://example.invalid/bodies.yaml#/B'
      responses: {}
`), "remote reference"},
		{"remote response reference", minimal(`paths:
  /a:
    get:
      responses:
        "200":
          $ref: 'shared.yaml#/responses/OK'
`), "remote reference"},
		{"dangling request body reference", minimal(`paths:
  /a:
    post:
      requestBody:
        $ref: '#/components/requestBodies/Ghost'
      responses: {}
`), "references #/components/requestBodies/Ghost"},
		{"dangling response reference", minimal(`paths:
  /a:
    get:
      responses:
        "200":
          $ref: '#/components/responses/Ghost'
`), "references #/components/responses/Ghost"},

		{"path item is not a mapping", minimal("paths:\n  /a: nope\n"), "a path item must be a mapping"},
		{"parameter without name and in", minimal(`paths:
  /a:
    get:
      parameters:
        - in: query
      responses: {}
`), "needs both name and in"},
		{"parameter required is not a bool", minimal(`paths:
  /a:
    get:
      parameters:
        - name: q
          in: query
          required: sometimes
      responses: {}
`), "required: must be true or false"},
		{"readOnly is not a bool", minimal(`components:
  schemas:
    A:
      type: object
      readOnly: mostly
`), "readOnly: must be true or false"},
		{"schema is a sequence", minimal(`components:
  schemas:
    A:
      - type: object
`), "a schema must be a mapping"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]byte(tc.doc))
			if err == nil {
				t.Fatalf("Load accepted the document; want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// JSON media types are preferred over everything else; within and without
// that preference the alphabetically first media type with a schema wins,
// so the pick never depends on document order.
func TestUnit_Specmodel_ContentTypeSelection(t *testing.T) {
	load := func(t *testing.T, content string) *Schema {
		t.Helper()
		doc, err := Load([]byte(minimal(`paths:
  /a:
    get:
      operationId: getA
      responses:
        "200":
          content:
` + indent(content, "            "))))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		return doc.Paths[0].Operations[0].Responses[0].Schema
	}

	// A +json suffix counts as JSON and beats the alphabetically earlier XML.
	s := load(t, "application/xml:\n  schema: {type: string}\napplication/hal+json:\n  schema: {type: integer}\n")
	if s == nil || s.Type != "integer" {
		t.Errorf("hal+json should win over xml, got %+v", s)
	}

	// Without any JSON, the alphabetically first media type wins.
	s = load(t, "text/plain:\n  schema: {type: string}\napplication/xml:\n  schema: {type: number}\n")
	if s == nil || s.Type != "number" {
		t.Errorf("application/xml should win alphabetically, got %+v", s)
	}

	// A JSON media type without a schema yields to one that has one.
	s = load(t, "application/json: {}\napplication/xml:\n  schema: {type: string}\n")
	if s == nil || s.Type != "string" {
		t.Errorf("schemaless json should yield to xml, got %+v", s)
	}
}

// YAML anchors and aliases read as their targets.
func TestUnit_Specmodel_AliasesDereference(t *testing.T) {
	doc, err := Load([]byte(minimal(`paths:
  /a:
    get:
      operationId: getA
      responses:
        "200": &ok
          content:
            application/json:
              schema:
                type: string
        "201": *ok
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rs := doc.Paths[0].Operations[0].Responses
	if len(rs) != 2 || rs[1].Schema == nil || rs[1].Schema.Type != "string" {
		t.Errorf("responses = %+v; the alias should read as its anchor", rs)
	}
}

// A reference written before its target parses fine: resolution is a
// separate pass over the completed document.
func TestUnit_Specmodel_ForwardReferencesResolve(t *testing.T) {
	doc, err := Load([]byte(minimal(`components:
  schemas:
    Early:
      $ref: '#/components/schemas/Late'
    Late:
      type: object
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if doc.Schemas["Early"].Resolved().Name != "Late" {
		t.Errorf("Early should resolve to Late")
	}
}

// An RFC 6901 escape in a reference decodes to the literal component name.
func TestUnit_Specmodel_EscapedReferenceNamesDecode(t *testing.T) {
	doc, err := Load([]byte(minimal(`components:
  schemas:
    "a/b":
      type: object
    Uses:
      $ref: '#/components/schemas/a~1b'
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if doc.Schemas["Uses"].Resolved().Name != "a/b" {
		t.Errorf("escaped reference did not resolve to a/b")
	}
}
