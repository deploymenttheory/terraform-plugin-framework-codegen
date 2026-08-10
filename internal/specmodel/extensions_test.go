package specmodel

import (
	"strings"
	"testing"
	"time"
)

// annotated is a document using every x-tfpfgen-* key the contract defines,
// on the object each is meant for: property-level behaviour keys on
// properties, operation-level keys on operations.
const annotated = `openapi: 3.0.3
info: {title: A, version: "1"}
paths:
  /things:
    post:
      operationId: createThing
      x-tfpfgen-eventual-consistency: 90s
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Thing'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
  /things/{thingId}:
    patch:
      operationId: updateThing
      x-tfpfgen-update-style: patch-merge
      responses: {}
    delete:
      operationId: deleteThing
      x-tfpfgen-delete-not-found-ok: true
      responses: {}
components:
  schemas:
    Thing:
      type: object
      properties:
        name:
          type: string
          x-tfpfgen-create-only: true
        tier:
          type: string
          enum: [basic, pro]
          x-tfpfgen-values-open: true
        lastSeen:
          type: string
          x-tfpfgen-volatile: true
        etag:
          type: string
          x-tfpfgen-server-forced: true
        notes:
          type: string
          x-tfpfgen-silently-ignored-on-update: true
        matchValue:
          type: string
          x-tfpfgen-required-when:
            property: matchType
            equals: custom
        matchType:
          type: string
`

func TestUnit_Specmodel_ExtensionAccessors(t *testing.T) {
	doc, err := Load([]byte(annotated))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	thing := doc.Schemas["Thing"]
	prop := func(name string) Extensions {
		s, ok := thing.Property(name)
		if !ok {
			t.Fatalf("no property %q", name)
		}
		return s.Extensions
	}

	if v, ok := prop("name").CreateOnly(); !ok || !v {
		t.Errorf("CreateOnly = %v, %v", v, ok)
	}
	if v, ok := prop("tier").ValuesOpen(); !ok || !v {
		t.Errorf("ValuesOpen = %v, %v", v, ok)
	}
	if v, ok := prop("lastSeen").Volatile(); !ok || !v {
		t.Errorf("Volatile = %v, %v", v, ok)
	}
	if v, ok := prop("etag").ServerForced(); !ok || !v {
		t.Errorf("ServerForced = %v, %v", v, ok)
	}
	if v, ok := prop("notes").SilentlyIgnoredOnUpdate(); !ok || !v {
		t.Errorf("SilentlyIgnoredOnUpdate = %v, %v", v, ok)
	}
	if rw, ok := prop("matchValue").RequiredWhen(); !ok || rw.Property != "matchType" || rw.Equals != "custom" {
		t.Errorf("RequiredWhen = %+v, %v", rw, ok)
	}

	byID := map[string]Operation{}
	for _, op := range doc.Operations() {
		byID[op.OperationID] = op
	}
	if d, ok := byID["createThing"].Extensions.EventualConsistency(); !ok || d != 90*time.Second {
		t.Errorf("EventualConsistency = %v, %v", d, ok)
	}
	if s, ok := byID["updateThing"].Extensions.UpdateStyle(); !ok || s != "patch-merge" {
		t.Errorf("UpdateStyle = %v, %v", s, ok)
	}
	if v, ok := byID["deleteThing"].Extensions.DeleteNotFoundOK(); !ok || !v {
		t.Errorf("DeleteNotFoundOK = %v, %v", v, ok)
	}

	// Absence is distinguishable from an explicit false everywhere.
	unannotated, _ := thing.Property("matchType")
	e := unannotated.Extensions
	if _, ok := e.CreateOnly(); ok {
		t.Errorf("CreateOnly should be absent")
	}
	if _, ok := e.RequiredWhen(); ok {
		t.Errorf("RequiredWhen should be absent")
	}
	if _, ok := e.EventualConsistency(); ok {
		t.Errorf("EventualConsistency should be absent")
	}
	if _, ok := e.UpdateStyle(); ok {
		t.Errorf("UpdateStyle should be absent")
	}
	if _, ok := e.DeleteNotFoundOK(); ok {
		t.Errorf("DeleteNotFoundOK should be absent")
	}
	if _, ok := e.ValuesOpen(); ok {
		t.Errorf("ValuesOpen should be absent")
	}
	if _, ok := e.Volatile(); ok {
		t.Errorf("Volatile should be absent")
	}
	if _, ok := e.ServerForced(); ok {
		t.Errorf("ServerForced should be absent")
	}
	if _, ok := e.SilentlyIgnoredOnUpdate(); ok {
		t.Errorf("SilentlyIgnoredOnUpdate should be absent")
	}
}

// Keys outside the x-tfpfgen- namespace belong to other tools and are
// neither refused nor recorded.
func TestUnit_Specmodel_ForeignExtensionsPassBy(t *testing.T) {
	doc, err := Load([]byte(minimal(`components:
  schemas:
    A:
      type: object
      x-ms-enum: {name: whatever}
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(doc.Schemas["A"].Extensions) != 0 {
		t.Errorf("Extensions = %+v; foreign keys should not be recorded", doc.Schemas["A"].Extensions)
	}
}

func TestUnit_Specmodel_ExtensionRefusals(t *testing.T) {
	schemaWith := func(lines string) string {
		return minimal("components:\n  schemas:\n    A:\n      type: object\n" + indent(lines, "      "))
	}

	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"unknown key near a real one suggests it",
			schemaWith("x-tfpfgen-create-onl: true"),
			`unknown extension "x-tfpfgen-create-onl" (did you mean "x-tfpfgen-create-only"?)`},
		{"unknown key near nothing gets no suggestion",
			schemaWith("x-tfpfgen-fully-imagined-behaviour-nobody-defined: true"),
			`unknown extension "x-tfpfgen-fully-imagined-behaviour-nobody-defined"`},
		{"unknown key on an operation is refused too", minimal(`paths:
  /a:
    get:
      x-tfpfgen-values-openn: true
      responses: {}
`), "unknown extension"},
		{"unknown key at the document root is refused",
			"openapi: 3.0.3\ninfo: {title: T, version: \"1\"}\nx-tfpfgen-mystery: 1\n",
			"unknown extension"},
		{"unknown key on a parameter is refused", minimal(`paths:
  /a:
    get:
      parameters:
        - name: q
          in: query
          x-tfpfgen-volatille: true
      responses: {}
`), "unknown extension"},

		{"create-only must be a bool", schemaWith("x-tfpfgen-create-only: definitely"), "must be true or false"},
		{"create-only must not be a mapping", schemaWith("x-tfpfgen-create-only: {on: create}"), "must be true or false"},
		{"eventual-consistency must parse as a duration",
			minimal("paths:\n  /a:\n    get:\n      x-tfpfgen-eventual-consistency: banana\n      responses: {}\n"),
			`"banana" is not a non-negative duration`},
		{"eventual-consistency must not be negative",
			minimal("paths:\n  /a:\n    get:\n      x-tfpfgen-eventual-consistency: -5s\n      responses: {}\n"),
			"is not a non-negative duration"},
		{"eventual-consistency must be a scalar",
			minimal("paths:\n  /a:\n    get:\n      x-tfpfgen-eventual-consistency: [30s]\n      responses: {}\n"),
			"must be a duration string"},
		{"update-style refuses a value outside the closed set",
			minimal("paths:\n  /a:\n    patch:\n      x-tfpfgen-update-style: merge\n      responses: {}\n"),
			`must be one of "patch-merge", "put-full" or "replace-only", got "merge"`},
		{"update-style refuses an empty value",
			minimal("paths:\n  /a:\n    patch:\n      x-tfpfgen-update-style: \"\"\n      responses: {}\n"),
			`must be one of "patch-merge", "put-full" or "replace-only"`},
		{"update-style must be a scalar",
			minimal("paths:\n  /a:\n    patch:\n      x-tfpfgen-update-style: [patch-merge]\n      responses: {}\n"),
			`must be one of "patch-merge", "put-full" or "replace-only"`},

		{"required-when must be a mapping", schemaWith("x-tfpfgen-required-when: matchType"),
			`must be a mapping with "property" and "equals"`},
		{"required-when refuses unknown keys", schemaWith("x-tfpfgen-required-when: {property: a, equals: b, unless: c}"),
			`unknown key "unless"`},
		{"required-when needs property", schemaWith("x-tfpfgen-required-when: {equals: b}"),
			`both "property" and "equals" are required`},
		{"required-when needs equals", schemaWith("x-tfpfgen-required-when: {property: a}"),
			`both "property" and "equals" are required`},
		{"required-when values must be scalars", schemaWith("x-tfpfgen-required-when: {property: a, equals: [b]}"),
			"must be a non-empty scalar"},
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

// x-tfpfgen-update-style is a closed enum: each approved value loads and
// reads back verbatim. The rejection cases live in the refusals table.
func TestUnit_Specmodel_UpdateStyleAcceptsEachApprovedValue(t *testing.T) {
	for _, style := range []string{"patch-merge", "put-full", "replace-only"} {
		t.Run(style, func(t *testing.T) {
			doc, err := Load([]byte(minimal(
				"paths:\n  /a/{id}:\n    patch:\n      x-tfpfgen-update-style: " + style + "\n      responses: {}\n")))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			ops := doc.Operations()
			if len(ops) != 1 {
				t.Fatalf("operations = %+v", ops)
			}
			if s, ok := ops[0].Extensions.UpdateStyle(); !ok || s != style {
				t.Errorf("UpdateStyle = %q, %v; want %q, true", s, ok, style)
			}
		})
	}
}

// indent prefixes every line, for embedding fragments into fixtures.
func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}
