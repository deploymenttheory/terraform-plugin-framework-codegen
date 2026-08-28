package specmodel

import (
	"reflect"
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
      x-tfpfgen-read-after-write: 90s
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
    get:
      operationId: listThings
      x-tfpfgen-list-wrapper:
        wrapped: true
        key: things
      x-tfpfgen-list-pagination: cursor
      responses: {}
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
          x-tfpfgen-immutable: true
        tier:
          type: string
          enum: [basic, pro]
          x-tfpfgen-values: true
        lastSeen:
          type: string
          x-tfpfgen-volatile: true
        etag:
          type: string
          x-tfpfgen-server-forced: true
        notes:
          type: string
          x-tfpfgen-ignored-on-update: true
        matchValue:
          type: string
          x-tfpfgen-required-when:
            property: matchType
            equals: custom
        matchType:
          type: string
        proxyHost:
          type: string
          x-tfpfgen-valid-when:
            property: mode
            equals: custom
        clientSecret:
          type: string
          x-tfpfgen-depends-on:
            requires: clientId
        clientId:
          type: string
        mode:
          type: string
      x-tfpfgen-mutually-exclusive: [alpha, beta]
      x-tfpfgen-valid-configuration:
        discriminator: mode
        variants:
          custom: [proxyHost, beta]
          basic: [alpha]
`

func TestUnit_Specmodel_ExtensionAccessors(t *testing.T) {
	document, err := Load([]byte(annotated))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	thing := document.Schemas["Thing"]
	property := func(name string) Extensions {
		s, ok := thing.Property(name)
		if !ok {
			t.Fatalf("no property %q", name)
		}
		return s.Extensions
	}

	if v, ok := property("name").Immutable(); !ok || !v {
		t.Errorf("CreateOnly = %v, %v", v, ok)
	}
	if v, ok := property("tier").Values(); !ok || !v {
		t.Errorf("ValuesOpen = %v, %v", v, ok)
	}
	if v, ok := property("lastSeen").Volatile(); !ok || !v {
		t.Errorf("Volatile = %v, %v", v, ok)
	}
	if v, ok := property("etag").ServerForced(); !ok || !v {
		t.Errorf("ServerForced = %v, %v", v, ok)
	}
	if v, ok := property("notes").IgnoredOnUpdate(); !ok || !v {
		t.Errorf("SilentlyIgnoredOnUpdate = %v, %v", v, ok)
	}
	if rw, ok := property("matchValue").RequiredWhen(); !ok || rw.Property != "matchType" || rw.Equals != "custom" {
		t.Errorf("RequiredWhen = %+v, %v", rw, ok)
	}
	if vw, ok := property("proxyHost").ValidWhen(); !ok || vw.Property != "mode" || vw.Equals != "custom" {
		t.Errorf("ValidWhen = %+v, %v", vw, ok)
	}
	if do, ok := property("clientSecret").DependsOn(); !ok || do.Requires != "clientId" {
		t.Errorf("DependsOn = %+v, %v", do, ok)
	}
	if names, ok := thing.Extensions.MutuallyExclusive(); !ok || !reflect.DeepEqual(names, []string{"alpha", "beta"}) {
		t.Errorf("MutuallyExclusive = %+v, %v", names, ok)
	}
	if vc, ok := thing.Extensions.ValidConfiguration(); !ok || vc.Discriminator != "mode" ||
		!reflect.DeepEqual(vc.Variants, []ValidVariant{
			{Value: "basic", Fields: []string{"alpha"}},
			{Value: "custom", Fields: []string{"beta", "proxyHost"}},
		}) {
		t.Errorf("ValidConfiguration = %+v, %v", vc, ok)
	}

	byID := map[string]Operation{}
	for _, operation := range document.Operations() {
		byID[operation.OperationID] = operation
	}
	if d, ok := byID["createThing"].Extensions.ReadAfterWrite(); !ok || d != 90*time.Second {
		t.Errorf("EventualConsistency = %v, %v", d, ok)
	}
	if s, ok := byID["updateThing"].Extensions.UpdateStyle(); !ok || s != "patch-merge" {
		t.Errorf("UpdateStyle = %v, %v", s, ok)
	}
	if v, ok := byID["deleteThing"].Extensions.DeleteNotFoundOK(); !ok || !v {
		t.Errorf("DeleteNotFoundOK = %v, %v", v, ok)
	}
	if s, ok := byID["listThings"].Extensions.ListWrapper(); !ok || !s.Wrapped ||
		s.Key != "things" || false {
		t.Errorf("ListWrapper = %+v, %v", s, ok)
	}

	// Absence is distinguishable from an explicit false everywhere.
	unannotated, _ := thing.Property("matchType")
	e := unannotated.Extensions
	if _, ok := e.Immutable(); ok {
		t.Errorf("CreateOnly should be absent")
	}
	if _, ok := e.RequiredWhen(); ok {
		t.Errorf("RequiredWhen should be absent")
	}
	if _, ok := e.ReadAfterWrite(); ok {
		t.Errorf("EventualConsistency should be absent")
	}
	if _, ok := e.UpdateStyle(); ok {
		t.Errorf("UpdateStyle should be absent")
	}
	if _, ok := e.DeleteNotFoundOK(); ok {
		t.Errorf("DeleteNotFoundOK should be absent")
	}
	if _, ok := e.Values(); ok {
		t.Errorf("ValuesOpen should be absent")
	}
	if _, ok := e.Volatile(); ok {
		t.Errorf("Volatile should be absent")
	}
	if _, ok := e.ServerForced(); ok {
		t.Errorf("ServerForced should be absent")
	}
	if _, ok := e.IgnoredOnUpdate(); ok {
		t.Errorf("SilentlyIgnoredOnUpdate should be absent")
	}
	if _, ok := e.ValidWhen(); ok {
		t.Errorf("ValidWhen should be absent")
	}
	if _, ok := e.DependsOn(); ok {
		t.Errorf("DependsOn should be absent")
	}
	if _, ok := e.MutuallyExclusive(); ok {
		t.Errorf("MutuallyExclusive should be absent")
	}
	if _, ok := e.ValidConfiguration(); ok {
		t.Errorf("ValidConfiguration should be absent")
	}
	if _, ok := e.ListWrapper(); ok {
		t.Errorf("ListWrapper should be absent")
	}
	if _, ok := e.ListPagination(); ok {
		t.Errorf("ListPagination should be absent")
	}
}

// x-tfpfgen-list-wrapper and x-tfpfgen-list-pagination round-trip through the
// loader in each of their legal forms. They are separate keys because
// wrapping and pagination are unrelated facts about one response: an API can
// change how it pages without changing how it wraps.
func TestUnit_Specmodel_ListWrapperForms(t *testing.T) {
	listOp := func(body string) string {
		return minimal("paths:\n  /a:\n    get:\n" + indent(body, "      ") + "      responses: {}\n")
	}
	cases := []struct {
		name       string
		body       string
		want       ListWrapper
		pagination string
	}{
		{"wrapped", "x-tfpfgen-list-wrapper:\n  wrapped: true\n  key: items\nx-tfpfgen-list-pagination: cursor",
			ListWrapper{Wrapped: true, Key: "items"}, "cursor"},
		{"wrapped without pagination", "x-tfpfgen-list-wrapper:\n  wrapped: true\n  key: value",
			ListWrapper{Wrapped: true, Key: "value"}, ""},
		{"unwrapped", "x-tfpfgen-list-wrapper:\n  wrapped: false\nx-tfpfgen-list-pagination: none",
			ListWrapper{}, "none"},
		{"unwrapped without pagination", "x-tfpfgen-list-wrapper:\n  wrapped: false",
			ListWrapper{}, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			document, err := Load([]byte(listOp(testCase.body)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			operations := document.Operations()
			if len(operations) != 1 {
				t.Fatalf("operations = %d, want 1", len(operations))
			}
			got, ok := operations[0].Extensions.ListWrapper()
			if !ok || got != testCase.want {
				t.Errorf("ListWrapper = %+v, %v; want %+v", got, ok, testCase.want)
			}
			style, ok := operations[0].Extensions.ListPagination()
			if testCase.pagination == "" {
				if ok {
					t.Errorf("ListPagination = %q, want absent", style)
				}
				return
			}
			if !ok || style != testCase.pagination {
				t.Errorf("ListPagination = %q, %v; want %q", style, ok, testCase.pagination)
			}
		})
	}
}

// Keys outside the x-tfpfgen- namespace belong to other tools and are
// neither refused nor recorded.
func TestUnit_Specmodel_ForeignExtensionsPassBy(t *testing.T) {
	document, err := Load([]byte(minimal(`components:
  schemas:
    A:
      type: object
      x-ms-enum: {name: whatever}
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(document.Schemas["A"].Extensions) != 0 {
		t.Errorf("Extensions = %+v; foreign keys should not be recorded", document.Schemas["A"].Extensions)
	}
}

// listWrapperWith puts one inline x-tfpfgen-list-wrapper value on a
// list operation, for the refusal table.
func listWrapperWith(value string) string {
	return minimal("paths:\n  /a:\n    get:\n      x-tfpfgen-list-wrapper: " + value + "\n      responses: {}\n")
}

func TestUnit_Specmodel_ExtensionRefusals(t *testing.T) {
	schemaWith := func(lines string) string {
		return minimal("components:\n  schemas:\n    A:\n      type: object\n" + indent(lines, "      "))
	}

	cases := []struct {
		name     string
		document string
		want     string
	}{
		{"unknown key near a real one suggests it",
			schemaWith("x-tfpfgen-volatil: true"),
			`unknown extension "x-tfpfgen-volatil" (did you mean "x-tfpfgen-volatile"?)`},
		{"unknown key near nothing gets no suggestion",
			schemaWith("x-tfpfgen-fully-imagined-behaviour-nobody-defined: true"),
			`unknown extension "x-tfpfgen-fully-imagined-behaviour-nobody-defined"`},
		{"unknown key on an operation is refused too", minimal(`paths:
  /a:
    get:
      x-tfpfgen-valuesn: true
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

		{"create-only must be a bool", schemaWith("x-tfpfgen-immutable: definitely"), "must be true or false"},
		{"create-only must not be a mapping", schemaWith("x-tfpfgen-immutable: {on: create}"), "must be true or false"},
		{"eventual-consistency must parse as a duration",
			minimal("paths:\n  /a:\n    get:\n      x-tfpfgen-read-after-write: banana\n      responses: {}\n"),
			`"banana" is not a non-negative duration`},
		{"eventual-consistency must not be negative",
			minimal("paths:\n  /a:\n    get:\n      x-tfpfgen-read-after-write: -5s\n      responses: {}\n"),
			"is not a non-negative duration"},
		{"eventual-consistency must be a scalar",
			minimal("paths:\n  /a:\n    get:\n      x-tfpfgen-read-after-write: [30s]\n      responses: {}\n"),
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

		{"valid-when must be a mapping", schemaWith("x-tfpfgen-valid-when: mode"),
			`must be a mapping with "property" and "equals"`},
		{"valid-when refuses unknown keys", schemaWith("x-tfpfgen-valid-when: {property: a, equals: b, when: c}"),
			`unknown key "when"`},
		{"valid-when needs both", schemaWith("x-tfpfgen-valid-when: {property: a}"),
			`both "property" and "equals" are required`},

		{"depends-on must be a mapping", schemaWith("x-tfpfgen-depends-on: clientId"),
			`must be a mapping with "requires"`},
		{"depends-on refuses unknown keys", schemaWith("x-tfpfgen-depends-on: {requires: a, when: b}"),
			`unknown key "when"`},
		{"depends-on needs requires", schemaWith("x-tfpfgen-depends-on: {}"),
			`"requires" is required`},

		{"mutually-exclusive must be a list", schemaWith("x-tfpfgen-mutually-exclusive: alpha"),
			"must be a list of property names"},
		{"mutually-exclusive needs at least two", schemaWith("x-tfpfgen-mutually-exclusive: [alpha]"),
			"at least two properties"},
		{"mutually-exclusive refuses duplicates", schemaWith("x-tfpfgen-mutually-exclusive: [alpha, alpha]"),
			`listed twice`},
		{"mutually-exclusive entries must be scalars", schemaWith("x-tfpfgen-mutually-exclusive: [alpha, [beta]]"),
			"each entry must be a non-empty property name"},

		{"valid-configuration must be a mapping", schemaWith("x-tfpfgen-valid-configuration: mode"),
			`must be a mapping with "discriminator" and "variants"`},
		{"valid-configuration refuses unknown keys",
			schemaWith("x-tfpfgen-valid-configuration: {discriminator: mode, variants: {a: [x]}, extra: 1}"),
			`unknown key "extra"`},
		{"valid-configuration needs a discriminator",
			schemaWith("x-tfpfgen-valid-configuration: {variants: {a: [x]}}"),
			`"discriminator" is required`},
		{"valid-configuration needs variants",
			schemaWith("x-tfpfgen-valid-configuration: {discriminator: mode}"),
			"must be a non-empty mapping"},
		{"valid-configuration variant must be a list",
			schemaWith("x-tfpfgen-valid-configuration: {discriminator: mode, variants: {a: x}}"),
			"must be a list of property names"},
		{"valid-configuration variant entries must be scalars",
			schemaWith("x-tfpfgen-valid-configuration: {discriminator: mode, variants: {a: [[x]]}}"),
			"each entry must be a non-empty property name"},

		{"list-wrapper must be a mapping", listWrapperWith("wrapped"),
			`must be a mapping with "wrapped"`},
		{"list-wrapper refuses unknown keys", listWrapperWith("{wrapped: false, cursorParam: after}"),
			`unknown key "cursorParam"`},
		{"list-wrapper refuses a non-boolean wrapped", listWrapperWith("{wrapped: nested, key: items}"),
			`must be true or false, got "nested"`},
		{"a wrapped response needs its key", listWrapperWith("{wrapped: true}"),
			`needs the "key" its items sit under`},
		{"an unwrapped response refuses a key", listWrapperWith("{wrapped: false, key: items}"),
			`wraps nothing, so "key" is meaningless`},
		{"list-wrapper values must be scalars", listWrapperWith("{wrapped: [false]}"),
			"must be a non-empty scalar"},
		{"list-pagination refuses an unknown style",
			minimal("paths:\n  /a:\n    get:\n      x-tfpfgen-list-pagination: spiral\n      responses: {}\n"),
			`must be one of "cursor", "offset", "page" or "none"`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Load([]byte(testCase.document))
			if err == nil {
				t.Fatalf("Load accepted the document; want an error containing %q", testCase.want)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %q, want it to contain %q", err, testCase.want)
			}
		})
	}
}

// x-tfpfgen-update-style is a closed enum: each approved value loads and
// reads back verbatim. The rejection cases live in the refusals table.
func TestUnit_Specmodel_UpdateStyleAcceptsEachApprovedValue(t *testing.T) {
	for _, style := range []string{"patch-merge", "put-full", "replace-only"} {
		t.Run(style, func(t *testing.T) {
			document, err := Load([]byte(minimal(
				"paths:\n  /a/{id}:\n    patch:\n      x-tfpfgen-update-style: " + style + "\n      responses: {}\n")))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			operations := document.Operations()
			if len(operations) != 1 {
				t.Fatalf("operations = %+v", operations)
			}
			if s, ok := operations[0].Extensions.UpdateStyle(); !ok || s != style {
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
