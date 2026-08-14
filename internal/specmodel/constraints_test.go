package specmodel

import "testing"

// constrainedSpec declares every constraint keyword the model reads, so one
// load proves the whole set arrives.
const constrainedSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
components:
  schemas:
    A:
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

// property fetches one property of the constrained schema, failing the test
// when the document does not declare it.
func property(t *testing.T, schema *Schema, name string) *Schema {
	t.Helper()
	got, ok := schema.Property(name)
	if !ok {
		t.Fatalf("no %s property", name)
	}
	return got
}

// TestUnit_Specmodel_ReadsTheDeclaredConstraints proves every keyword the
// emitted schema needs survives the load, and that a schema declaring none
// carries none rather than a zero that reads as a bound.
func TestUnit_Specmodel_ReadsTheDeclaredConstraints(t *testing.T) {
	doc, err := Load([]byte(constrainedSpec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := doc.Schemas["A"]
	if a == nil {
		t.Fatal("no A schema")
	}

	secret := property(t, a, "secret")
	if !secret.WriteOnly {
		t.Error("writeOnly not read")
	}
	if secret.Format != "password" {
		t.Errorf("format = %q", secret.Format)
	}
	if secret.Pattern != "^[a-z]+$" {
		t.Errorf("pattern = %q", secret.Pattern)
	}
	assertInt64(t, "minLength", secret.MinLength, 8)
	assertInt64(t, "maxLength", secret.MaxLength, 64)

	if !property(t, a, "legacy").Deprecated {
		t.Error("deprecated not read")
	}

	weight := property(t, a, "weight")
	assertFloat64(t, "minimum", weight.Minimum, 1)
	assertFloat64(t, "maximum", weight.Maximum, 10)

	tags := property(t, a, "tags")
	if !tags.UniqueItems {
		t.Error("uniqueItems not read")
	}
	assertInt64(t, "minItems", tags.MinItems, 1)
	assertInt64(t, "maxItems", tags.MaxItems, 5)

	// A property declaring nothing carries nothing: a nil bound and a zero
	// bound mean different things, and only nil means the document is silent.
	plain := property(t, a, "plain")
	if plain.WriteOnly || plain.Deprecated || plain.UniqueItems {
		t.Errorf("undeclared flags set: %+v", plain)
	}
	if plain.MinLength != nil || plain.MaxLength != nil ||
		plain.MinItems != nil || plain.MaxItems != nil {
		t.Errorf("undeclared bounds set: %+v", plain)
	}
}

func assertInt64(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s not read", name)
		return
	}
	if *got != want {
		t.Errorf("%s = %d, want %d", name, *got, want)
	}
}

func assertFloat64(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s not read", name)
		return
	}
	if *got != want {
		t.Errorf("%s = %v, want %v", name, *got, want)
	}
}
