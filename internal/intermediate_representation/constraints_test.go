package intermediate_representation

import "testing"

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
