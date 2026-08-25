package intermediate_representation

import (
	"strings"
	"testing"
)

// constrainedSpec is one resource whose create body declares every
// constraint keyword, so one derivation proves the whole set reaches the
// attribute.
const constrainedSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /keys:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Key'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Key'}
  /keys/{keyId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Key'}
    patch:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Key'}
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Key'}
    delete:
      responses:
        "204": {description: gone}
components:
  schemas:
    Key:
      type: object
      properties:
        secret:
          type: string
          format: password
          writeOnly: true
          minLength: 8
          maxLength: 64
          pattern: "^[a-z]+$"
        legacy:
          type: string
          deprecated: true
        weight:
          type: integer
          minimum: 1
          maximum: 10
        tags:
          type: array
          uniqueItems: true
          minItems: 1
          maxItems: 5
          items: {type: string}
        plain:
          type: string
`

// TestUnit_Attribute_CarriesTheDeclaredConstraints proves the derivation
// carries every declared constraint onto the attribute. Nothing reads them
// yet; they are what the emitted validators, the sensitivity and the
// deprecation notice are decided from.
func TestUnit_Attribute_CarriesTheDeclaredConstraints(t *testing.T) {
	r := resourceByKey(t, mustDerive(t, constrainedSpec, testConfig()), "key")

	secret := attribute(t, r.Schema, "secret")
	if !secret.WriteOnly {
		t.Error("WriteOnly not carried")
	}
	if secret.Format != "password" {
		t.Errorf("Format = %q", secret.Format)
	}
	if secret.Pattern != "^[a-z]+$" {
		t.Errorf("Pattern = %q", secret.Pattern)
	}
	assertBound(t, "MinLength", secret.MinLength, 8)
	assertBound(t, "MaxLength", secret.MaxLength, 64)

	if !attribute(t, r.Schema, "legacy").Deprecated {
		t.Error("Deprecated not carried")
	}

	weight := attribute(t, r.Schema, "weight")
	if weight.Minimum == nil || *weight.Minimum != 1 {
		t.Errorf("Minimum = %v", weight.Minimum)
	}
	if weight.Maximum == nil || *weight.Maximum != 10 {
		t.Errorf("Maximum = %v", weight.Maximum)
	}

	tags := attribute(t, r.Schema, "tags")
	if !tags.UniqueItems {
		t.Error("UniqueItems not carried")
	}
	assertBound(t, "MinItems", tags.MinItems, 1)
	assertBound(t, "MaxItems", tags.MaxItems, 5)

	plain := attribute(t, r.Schema, "plain")
	if plain.WriteOnly || plain.Deprecated || plain.UniqueItems ||
		plain.Format != "" || plain.Pattern != "" ||
		plain.MinLength != nil || plain.MaxLength != nil {
		t.Errorf("a property declaring nothing carries something: %+v", plain)
	}
}

// TestUnit_Attribute_MarksADeclaredSecretSensitive proves either declaration
// is enough on its own, and that neither marks an ordinary value.
func TestUnit_Attribute_MarksADeclaredSecretSensitive(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /accounts:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Account'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Account'}
  /accounts/{accountId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Account'}
    delete:
      responses:
        "204": {description: gone}
components:
  schemas:
    Account:
      type: object
      properties:
        formatted:
          type: string
          format: password
        writeOnly:
          type: string
          writeOnly: true
        both:
          type: string
          format: password
          writeOnly: true
        plain:
          type: string
`
	r := resourceByKey(t, mustDerive(t, spec, testConfig()), "account")
	for _, name := range []string{"formatted", "write_only", "both"} {
		if !attribute(t, r.Schema, name).Sensitive {
			t.Errorf("%q is a declared secret and is not marked sensitive", name)
		}
	}
	if attribute(t, r.Schema, "plain").Sensitive {
		t.Error("an ordinary attribute is marked sensitive")
	}
}

// TestUnit_Attribute_ADeclaredDefaultFillsTheResponse proves a documented
// default sends a writable attribute to Optional + Computed: the API says it
// substitutes a value when the request omits one, so the response carries a
// value either way and plain Optional would be a perpetual diff.
func TestUnit_Attribute_ADeclaredDefaultFillsTheResponse(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /jobs:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Job'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/JobRead'}
  /jobs/{jobId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/JobRead'}
    patch:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Job'}
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/JobRead'}
    delete:
      responses:
        "204": {description: gone}
components:
  schemas:
    Job:
      type: object
      properties:
        retries: {type: integer, default: 3}
        enabled: {type: boolean, default: false}
        label: {type: string, default: ""}
        note: {type: string}
    JobRead:
      type: object
      properties:
        retries: {type: integer}
        enabled: {type: boolean}
        label: {type: string}
        note: {type: string}
        summary: {type: string, default: "none"}
`
	r := resourceByKey(t, mustDerive(t, spec, testConfig()), "job")

	// A false or an empty default is a declaration like any other: the test
	// is that the document states one, not that the value is truthy.
	for _, name := range []string{"retries", "enabled", "label"} {
		if got := attribute(t, r.Schema, name).ComputedOptionalRequired; got != ComputedOptional {
			t.Errorf("%q with a declared default = %q, want computed_optional", name, got)
		}
	}
	if got := attribute(t, r.Schema, "note").ComputedOptionalRequired; got != Optional {
		t.Errorf("%q declares no default and became %q", "note", got)
	}
	// A default on the response side says nothing about what happens when a
	// request omits the field, and summary is not writable at all.
	if got := attribute(t, r.Schema, "summary").ComputedOptionalRequired; got != Computed {
		t.Errorf("a response-only property with a default = %q, want computed", got)
	}
}

// TestUnit_Attribute_RefusesAReservedRootName proves a root attribute
// terraform reserves is refused rather than emitted. Declaring one costs the
// whole provider: terraform rejects the schema and loads none of it, so the
// entity beside it goes too.
func TestUnit_Attribute_RefusesAReservedRootName(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /groups:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Group'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Group'}
  /groups/{groupId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Group'}
    delete:
      responses:
        "204": {description: gone}
components:
  schemas:
    Group:
      type: object
      properties:
        count: {type: integer}
        lifecycle: {type: string}
        name: {type: string}
        nested:
          type: object
          properties:
            count: {type: integer}
`
	r := resourceByKey(t, mustDerive(t, spec, testConfig()), "group")

	for _, name := range []string{"count", "lifecycle"} {
		got := attribute(t, r.Schema, name)
		if !got.Unsupported {
			t.Errorf("%q is reserved at the root and was emitted anyway", name)
		}
		if !strings.Contains(got.UnsupportedReason, "terraform reserves") {
			t.Errorf("%q refused for %q, which does not say why", name, got.UnsupportedReason)
		}
	}
	if attribute(t, r.Schema, "name").Unsupported {
		t.Error("an ordinary root attribute was refused")
	}

	// The same name nested inside an object is an ordinary field: terraform
	// reserves it only where a practitioner would write a meta-argument.
	nested := attribute(t, r.Schema, "nested")
	if nested.Nested == nil {
		t.Fatalf("nested = %+v", nested)
	}
	if attribute(t, nested.Nested, "count").Unsupported {
		t.Error("a reserved name nested inside an object was refused")
	}
}

func assertBound(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s not carried", name)
		return
	}
	if *got != want {
		t.Errorf("%s = %d, want %d", name, *got, want)
	}
}

// exampleSpec declares its example on a referenced schema rather than on the
// property, which is where a document that names its types puts it.
const exampleSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /streams:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Stream'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Stream'}
  /streams/{streamId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Stream'}
    patch:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Stream'}
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Stream'}
    delete:
      responses:
        "204": {description: gone}
components:
  schemas:
    Stream:
      type: object
      properties:
        endpointUrl: {$ref: '#/components/schemas/EndpointUrl'}
        retries: {type: integer, format: int64, example: 300}
        plain: {type: string}
    EndpointUrl:
      type: string
      description: The URL data is sent to.
      example: https://api.example.otel-collector
`

// TestUnit_Attribute_CarriesTheDeclaredExample proves the example survives
// the reference it is declared behind, which is the only place the fixture
// derivation can learn that a string is more than a string when the document
// declares no format.
func TestUnit_Attribute_CarriesTheDeclaredExample(t *testing.T) {
	r := resourceByKey(t, mustDerive(t, exampleSpec, testConfig()), "stream")

	if got := attribute(t, r.Schema, "endpoint_url").Example; got != "https://api.example.otel-collector" {
		t.Errorf("Example = %#v, want the one declared on the referenced schema", got)
	}
	if got := attribute(t, r.Schema, "retries").Example; got != 300 {
		t.Errorf("Example = %#v, want the declared number", got)
	}
	if got := attribute(t, r.Schema, "plain").Example; got != nil {
		t.Errorf("a property declaring no example carries %#v", got)
	}
}

// identifierPropertySpec addresses its item by {id} while its response spells
// the same identifier "aid" — the shape the extension exists to reconcile.
const identifierPropertySpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /groups:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Group'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Group'}
  /groups/{id}:
    get:
      x-tfpfgen-identifier-property: aid
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Group'}
    patch:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Group'}
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Group'}
    delete:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses:
        "204": {description: gone}
components:
  schemas:
    Group:
      type: object
      properties:
        aid: {type: string}
        groupName: {type: string}
`

// TestUnit_Attribute_TheIdentifierPropertyNamesTheIdsWire proves the id
// attribute reads through the property the response actually carries. Without
// it the id binds to an accessor no model has, the binding is pruned as
// addressing, and the settling read addresses the object by an empty string.
func TestUnit_Attribute_TheIdentifierPropertyNamesTheIdsWire(t *testing.T) {
	r := resourceByKey(t, mustDerive(t, identifierPropertySpec, testConfig()), "group")

	if id := attribute(t, r.Schema, "id"); id.WireName != "aid" {
		t.Errorf("the id attribute reads %q, want the property the response carries", id.WireName)
	}
}

// TestUnit_Attribute_TheIdWireFallsBackToThePathParameter proves the same
// document without the extension still takes the path parameter's name, so
// the extension corrects rather than replaces the derivation.
func TestUnit_Attribute_TheIdWireFallsBackToThePathParameter(t *testing.T) {
	plain := strings.Replace(identifierPropertySpec, "      x-tfpfgen-identifier-property: aid\n", "", 1)
	r := resourceByKey(t, mustDerive(t, plain, testConfig()), "group")

	if id := attribute(t, r.Schema, "id"); id.WireName != "id" {
		t.Errorf("the id attribute reads %q, want the path parameter's name", id.WireName)
	}
}
