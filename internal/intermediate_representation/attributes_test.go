package intermediate_representation

import (
	"reflect"
	"strings"
	"testing"
)

// thingTree derives the main fixture's resource tree once per test.
func thingTree(t *testing.T) *AttributeTree {
	t.Helper()
	return resourceByKey(t, mustDerive(t, thingSpec, testConfig()), "thing").Schema
}

func TestAttributes_TypeMapping(t *testing.T) {
	tree := thingTree(t)
	for _, tc := range []struct {
		name string
		kind TypeKind
	}{
		{"name", TypeString},
		{"count", TypeInt64},
		{"ratio", TypeFloat64},
		{"enabled", TypeBool},
		{"labels", TypeList},
		{"rules", TypeList},
		{"settings", TypeObject},
	} {
		if a := attribute(t, tree, tc.name); a.Kind != tc.kind {
			t.Errorf("%s: kind = %q, want %q", tc.name, a.Kind, tc.kind)
		}
	}

	labels := attribute(t, tree, "labels")
	if labels.ElemKind != TypeString || labels.Nested != nil {
		t.Errorf("labels = %+v, want a list of strings", labels)
	}
	rules := attribute(t, tree, "rules")
	if rules.ElemKind != TypeObject || rules.Nested == nil {
		t.Fatalf("rules = %+v, want a list of objects", rules)
	}
	if kind := attribute(t, rules.Nested, "kind"); kind.Presence != PresenceRequired {
		t.Errorf("kind inside rules = %+v, want required", kind)
	}
	if limit := attribute(t, rules.Nested, "limit"); limit.Presence != PresenceOptional || limit.Kind != TypeInt64 {
		t.Errorf("limit inside rules = %+v", limit)
	}
	settings := attribute(t, tree, "settings")
	if settings.Nested == nil {
		t.Fatalf("settings has no nested tree")
	}
	if retries := attribute(t, settings.Nested, "retries"); retries.Kind != TypeInt64 {
		t.Errorf("retries = %+v", retries)
	}
}

func TestAttributes_FreeFormObjectIsRefused(t *testing.T) {
	extras := attribute(t, thingTree(t), "extras")
	if !extras.Unsupported {
		t.Fatalf("a free-form object derived a type: %+v", extras)
	}
	if !strings.Contains(extras.UnsupportedReason, "free-form") {
		t.Errorf("reason = %q", extras.UnsupportedReason)
	}
	if extras.Kind != "" {
		t.Errorf("an unsupported attribute still claims kind %q", extras.Kind)
	}
}

func TestAttributes_Presence(t *testing.T) {
	tree := thingTree(t)
	for _, tc := range []struct {
		name string
		want Presence
	}{
		{"name", PresenceRequired},         // required and writable
		{"region", PresenceOptional},       // writable, not required
		{"mode", PresenceOptionalComputed}, // writable, server fills it: required only in the response
		{"stamp", PresenceComputed},        // readOnly
		{"etag", PresenceComputed},         // response-only
		{"forced", PresenceComputed},       // x-tfpfgen-server-forced
		{"flaky", PresenceComputed},        // x-tfpfgen-volatile
		{"id", PresenceComputed},           // always
	} {
		if a := attribute(t, tree, tc.name); a.Presence != tc.want {
			t.Errorf("%s: presence = %q, want %q", tc.name, a.Presence, tc.want)
		}
	}
}

func TestAttributes_CreateOnlyRequiresReplace(t *testing.T) {
	tree := thingTree(t)
	if a := attribute(t, tree, "region"); !a.RequiresReplace {
		t.Errorf("x-tfpfgen-create-only did not set RequiresReplace")
	}
	if a := attribute(t, tree, "name"); a.RequiresReplace {
		t.Errorf("RequiresReplace leaked onto %+v", a)
	}
}

func TestAttributes_Enums(t *testing.T) {
	tree := thingTree(t)

	mode := attribute(t, tree, "mode")
	if want := []string{"standard", "custom"}; !reflect.DeepEqual(mode.OneOf, want) {
		t.Errorf("mode.OneOf = %v, want %v in document order", mode.OneOf, want)
	}
	if mode.AdvisoryValues != nil {
		t.Errorf("a closed enum recorded advisory values")
	}

	tier := attribute(t, tree, "tier")
	if tier.OneOf != nil {
		t.Errorf("x-tfpfgen-values-open still produced a validator: %v", tier.OneOf)
	}
	if want := []string{"gold", "silver"}; !reflect.DeepEqual(tier.AdvisoryValues, want) {
		t.Errorf("tier.AdvisoryValues = %v, want %v", tier.AdvisoryValues, want)
	}
}

func TestAttributes_ConditionalRequirement(t *testing.T) {
	tree := thingTree(t)
	want := []ConditionalRequirement{{Property: "mode", Equals: "custom", Required: []string{"proxy_host"}}}
	if !reflect.DeepEqual(tree.ConditionalRequirements, want) {
		t.Errorf("conditional requirements = %+v, want %+v", tree.ConditionalRequirements, want)
	}
}

func TestAttributes_SilentlyIgnoredOnUpdate(t *testing.T) {
	if a := attribute(t, thingTree(t), "notes"); !a.SilentlyIgnoredOnUpdate {
		t.Errorf("x-tfpfgen-silently-ignored-on-update not carried: %+v", a)
	}
}

func TestAttributes_WireNamesBecomeSnakeCase(t *testing.T) {
	a := attribute(t, thingTree(t), "proxy_host")
	if a.WireName != "proxyHost" {
		t.Errorf("wire name = %q", a.WireName)
	}
}

// Document property order is preserved: create-schema properties first in
// their declared order, response-only ones appended after.
func TestAttributes_OrderFollowsTheDocument(t *testing.T) {
	tree := thingTree(t)
	var got []string
	for _, a := range tree.Attributes {
		got = append(got, a.Name)
	}
	want := []string{
		"name", "mode", "region", "tier", "proxy_host", "notes", "count",
		"ratio", "enabled", "labels", "rules", "settings", "extras",
		"forced", "flaky", "stamp", "id", "etag",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attribute order:\n got %v\nwant %v", got, want)
	}
}

// When the read schema declares no id, one is synthesized from the item
// path parameter.
func TestAttributes_IDSynthesizedFromThePathParameter(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /codes:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Code'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Code'}
  /codes/{codeNumber}:
    parameters:
      - {name: codeNumber, in: path, required: true, schema: {type: integer}}
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Code'}
    delete:
      responses:
        "204": {description: gone}
components:
  schemas:
    Code:
      type: object
      properties:
        text: {type: string}
`
	r := resourceByKey(t, mustDerive(t, spec, testConfig()), "code")
	id := r.Schema.Attributes[0]
	if id.Name != "id" || id.Presence != PresenceComputed {
		t.Fatalf("first attribute = %+v, want the synthesized computed id", id)
	}
	if id.WireName != "codeNumber" {
		t.Errorf("id maps from %q, want the item path parameter", id.WireName)
	}
	if id.Kind != TypeInt64 {
		t.Errorf("id kind = %q, want the parameter's integer", id.Kind)
	}
}

// Shapes derivation refuses to guess at: unions, missing types, arrays
// without a usable element.
func TestAttributes_RefusedShapes(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /shapes:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Shape'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Shape'}
  /shapes/{shapeId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Shape'}
    delete:
      responses:
        "204": {description: gone}
components:
  schemas:
    Shape:
      type: object
      properties:
        either:
          oneOf:
            - {type: string}
            - {type: integer}
        mystery: {}
        bare:
          type: array
        grid:
          type: array
          items:
            type: array
            items: {type: string}
        blobs:
          type: array
          items:
            type: object
`
	r := resourceByKey(t, mustDerive(t, spec, testConfig()), "shape")
	for name, wantReason := range map[string]string{
		"either":  "oneOf/anyOf",
		"mystery": "no type",
		"bare":    "no items",
		"grid":    `array of "array"`,
		"blobs":   "free-form",
	} {
		a := attribute(t, r.Schema, name)
		if !a.Unsupported {
			t.Errorf("%s: derived %q instead of refusing", name, a.Kind)
			continue
		}
		if !strings.Contains(a.UnsupportedReason, wantReason) {
			t.Errorf("%s: reason = %q, want it to mention %q", name, a.UnsupportedReason, wantReason)
		}
	}
}

// allOf composition folds flat: branch properties combine, requireds
// union, and reference-site extensions win.
func TestAttributes_AllOfFoldsFlat(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /parts:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Part'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Part'}
  /parts/{partId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Part'}
    delete:
      responses:
        "204": {description: gone}
components:
  schemas:
    Base:
      type: object
      required: [core]
      properties:
        core: {type: string}
    Part:
      allOf:
        - $ref: '#/components/schemas/Base'
        - type: object
          properties:
            extra: {type: integer}
`
	r := resourceByKey(t, mustDerive(t, spec, testConfig()), "part")
	if a := attribute(t, r.Schema, "core"); a.Presence != PresenceRequired {
		t.Errorf("core = %+v, want the branch's required to hold", a)
	}
	if a := attribute(t, r.Schema, "extra"); a.Kind != TypeInt64 {
		t.Errorf("extra = %+v", a)
	}
}
