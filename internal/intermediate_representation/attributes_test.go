package intermediate_representation

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// thingTree derives the main fixture's resource tree once per test.
func thingTree(t *testing.T) *AttributeTree {
	t.Helper()
	return resourceByKey(t, mustDerive(t, thingSpec, testConfig()), "thing").Attributes
}

func TestUnit_Attributes_TypeMapping(t *testing.T) {
	tree := thingTree(t)
	for _, testCase := range []struct {
		name string
		kind AttributeType
	}{
		{"name", TypeString},
		{"quantity", TypeInt64},
		{"ratio", TypeFloat64},
		{"enabled", TypeBool},
		{"labels", TypeList},
		{"rules", TypeList},
		{"settings", TypeObject},
	} {
		if a := attribute(t, tree, testCase.name); a.Type != testCase.kind {
			t.Errorf("%s: kind = %q, want %q", testCase.name, a.Type, testCase.kind)
		}
	}

	labels := attribute(t, tree, "labels")
	if labels.ElementType != TypeString || labels.NestedAttributes != nil {
		t.Errorf("labels = %+v, want a list of strings", labels)
	}
	rules := attribute(t, tree, "rules")
	if rules.ElementType != TypeObject || rules.NestedAttributes == nil {
		t.Fatalf("rules = %+v, want a list of objects", rules)
	}
	// rules is server-filled, so its members are computed as well, the
	// required one included: terraform core reads a non-computed member
	// holding a value as one the configuration set, and clears the whole
	// attribute when the configuration omits it.
	if kind := attribute(t, rules.NestedAttributes, "kind"); kind.ComputedOptionalRequired != ComputedOptional {
		t.Errorf("kind inside rules = %+v, want computed-optional under a server-filled parent", kind)
	}
	// Nested attributes take the same presence rule as top-level ones, and
	// this response describes the create schema wholesale.
	if limit := attribute(t, rules.NestedAttributes, "limit"); limit.ComputedOptionalRequired != ComputedOptional ||
		limit.Type != TypeInt64 {
		t.Errorf("limit inside rules = %+v", limit)
	}
	settings := attribute(t, tree, "settings")
	if settings.NestedAttributes == nil {
		t.Fatalf("settings has no nested tree")
	}
	if retries := attribute(t, settings.NestedAttributes, "retries"); retries.Type != TypeInt64 {
		t.Errorf("retries = %+v", retries)
	}
}

func TestUnit_Attributes_AnObjectWithoutPropertiesOrAdditionalPropertiesIsExcluded(t *testing.T) {
	extras := attribute(t, thingTree(t), "extras")
	if !extras.Unsupported {
		t.Fatalf("an object with no declared shape derived a type: %+v", extras)
	}
	if !strings.Contains(extras.UnsupportedReason, "no declared shape") {
		t.Errorf("reason = %q", extras.UnsupportedReason)
	}
	if extras.Type != "" {
		t.Errorf("an unsupported attribute still claims kind %q", extras.Type)
	}
}

// TestUnit_DeriveMapType_TypesEveryValueShape proves an object that
// declares additionalProperties becomes a map of that value type, and that
// each shape the toolkit will not model names itself rather than sharing
// one blanket reason.
func TestUnit_DeriveMapType_TypesEveryValueShape(t *testing.T) {
	object := func(additional *specmodel.Schema, declared bool) *specmodel.Schema {
		return &specmodel.Schema{Type: "object", AdditionalProperties: additional, AdditionalPropertiesDeclared: declared}
	}

	for _, testCase := range []struct {
		name        string
		schema      *specmodel.Schema
		wantKind    AttributeType
		wantElement AttributeType
		wantReason  string
	}{
		{"string values", object(&specmodel.Schema{Type: "string"}, false), TypeMap, TypeString, ""},
		{"boolean values", object(&specmodel.Schema{Type: "boolean"}, false), TypeMap, TypeBool, ""},
		{"integer values", object(&specmodel.Schema{Type: "integer"}, false), TypeMap, TypeInt64, ""},
		{"number values", object(&specmodel.Schema{Type: "number"}, false), TypeMap, TypeFloat64, ""},
		{"nothing declared", object(nil, false), "", "", "no declared shape"},
		{"bare boolean", object(nil, true), "", "", "bare boolean"},
		{"object values", object(&specmodel.Schema{Type: "object", Properties: []specmodel.Property{
			{Name: "x", Schema: &specmodel.Schema{Type: "string"}}}}, false), TypeMap, TypeObject, ""},
		{"object values with no properties", object(&specmodel.Schema{Type: "object"}, false), "", "", "gives no properties"},
		{"map values", object(&specmodel.Schema{Type: "object",
			AdditionalProperties: &specmodel.Schema{Type: "string"}}, false), "", "", "values are themselves maps"},
		{"array values", object(&specmodel.Schema{Type: "array"}, false), "", "", `map of "array" values`},
	} {
		tree := buildAttributeTree(&specmodel.Schema{Type: "object", Properties: []specmodel.Property{
			{Name: "bag", Schema: testCase.schema},
		}}, nil, nil, false)
		got := attribute(t, tree, "bag")

		if testCase.wantReason != "" {
			if !got.Unsupported {
				t.Errorf("%s: derived kind %q, want a refusal", testCase.name, got.Type)
				continue
			}
			if !strings.Contains(got.UnsupportedReason, testCase.wantReason) {
				t.Errorf("%s: reason = %q, want it to mention %q", testCase.name, got.UnsupportedReason, testCase.wantReason)
			}
			continue
		}
		if got.Unsupported {
			t.Errorf("%s: refused with %q", testCase.name, got.UnsupportedReason)
			continue
		}
		if got.Type != testCase.wantKind || got.ElementType != testCase.wantElement {
			t.Errorf("%s: kind/element = %q/%q, want %q/%q",
				testCase.name, got.Type, got.ElementType, testCase.wantKind, testCase.wantElement)
		}
	}
}

func TestUnit_Attributes_ComputedOptionalRequired(t *testing.T) {
	tree := thingTree(t)
	for _, testCase := range []struct {
		name string
		want ComputedOptionalRequired
	}{
		// Every attribute reaches exactly one of these; a fifth outcome,
		// omitted from the schema entirely, is the unsupported-type path.
		{"name", Required}, // required and writable
		// The routes to Optional+Computed. `mode` takes the weak one — the
		// response schema happens to list it as required — `filled` takes the
		// one the audit measures, and `region` takes the one that catches a
		// document declaring nothing required in its responses: this entity's
		// response is an allOf over the create schema, so it describes region
		// and the server may fill it. Plain Optional is left for a writable
		// property no response describes at all, covered in constraints_test.
		{"region", ComputedOptional},
		{"mode", ComputedOptional},
		{"filled", ComputedOptional}, // x-tfpfgen-server-default
		{"stamp", Computed},          // readOnly
		{"etag", Computed},           // response-only
		{"forced", Computed},         // x-tfpfgen-server-forced
		{"flaky", Computed},          // x-tfpfgen-volatile
		{"id", Computed},             // always
	} {
		if a := attribute(t, tree, testCase.name); a.ComputedOptionalRequired != testCase.want {
			t.Errorf("%s: presence = %q, want %q", testCase.name, a.ComputedOptionalRequired, testCase.want)
		}
	}
}

func TestUnit_Attributes_CreateOnlyRequiresReplace(t *testing.T) {
	tree := thingTree(t)
	if a := attribute(t, tree, "region"); !a.RequiresReplace {
		t.Errorf("x-tfpfgen-immutable did not set RequiresReplace")
	}
	if a := attribute(t, tree, "name"); a.RequiresReplace {
		t.Errorf("RequiresReplace leaked onto %+v", a)
	}
}

func TestUnit_Attributes_Enums(t *testing.T) {
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
		t.Errorf("x-tfpfgen-values still produced a validator: %v", tier.OneOf)
	}
	if want := []string{"gold", "silver"}; !reflect.DeepEqual(tier.AdvisoryValues, want) {
		t.Errorf("tier.AdvisoryValues = %v, want %v", tier.AdvisoryValues, want)
	}
}

func TestUnit_Attributes_ConditionalRequirement(t *testing.T) {
	tree := thingTree(t)
	want := []ConditionalRequirement{{Property: "mode", WhenPropertyEquals: "custom", Required: []string{"proxy_host"}}}
	if !reflect.DeepEqual(tree.ConditionalRequirements, want) {
		t.Errorf("conditional requirements = %+v, want %+v", tree.ConditionalRequirements, want)
	}
}

// TestDerive_ListWrapperKey_BareArray: the main fixture's list is a bare
// array, so the derived envelope key is empty — the mock emits a bare array,
// not a wrapper.
func TestUnit_Derive_ListWrapperKey_BareArray(t *testing.T) {
	resource := resourceByKey(t, mustDerive(t, thingSpec, testConfig()), "thing")
	if resource.ListWrapperKey != "" {
		t.Errorf("bare-array list derived envelope key %q, want empty", resource.ListWrapperKey)
	}
}

// TestDerive_ListWrapperKey_Wrapped: a list response wrapping its items
// under a vendor key is read from the schema, not assumed to be "value".
func TestUnit_Derive_ListWrapperKey_Wrapped(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /widgets:
    post:
      requestBody:
        content: {application/json: {schema: {$ref: '#/components/schemas/Gizmo'}}}
      responses:
        "201": {content: {application/json: {schema: {$ref: '#/components/schemas/Gizmo'}}}}
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  nextPage: {type: string}
                  widgets:
                    type: array
                    items: {$ref: '#/components/schemas/Gizmo'}
  /widgets/{widgetId}:
    get:
      responses:
        "200": {content: {application/json: {schema: {$ref: '#/components/schemas/Gizmo'}}}}
    delete:
      responses: {"204": {description: gone}}
components:
  schemas:
    Gizmo:
      type: object
      properties:
        id: {type: string}
        label: {type: string}
`
	resource := resourceByKey(t, mustDerive(t, spec, testConfig()), "widget")
	if resource.ListWrapperKey != "widgets" {
		t.Errorf("wrapped list derived envelope key %q, want %q", resource.ListWrapperKey, "widgets")
	}
}

// TestDerive_ListWrapperKey_ExtensionBeatsTheSchema: where a list operation
// carries x-tfpfgen-list-wrapper, the audit's observed wrapping is the
// answer and the response schema is not consulted — the document's own list
// schema being wrong is the whole reason the key exists.
func TestUnit_Derive_ListWrapperKey_ExtensionBeatsTheSchema(t *testing.T) {
	// The document declares a wrapper keyed "items"; the live API was
	// observed wrapping under "records".
	const wrappedDoc = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /widgets:
    post:
      requestBody:
        content: {application/json: {schema: {$ref: '#/components/schemas/Gizmo'}}}
      responses:
        "201": {content: {application/json: {schema: {$ref: '#/components/schemas/Gizmo'}}}}
    get:
      x-tfpfgen-list-wrapper: {%s}
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  items:
                    type: array
                    items: {$ref: '#/components/schemas/Gizmo'}
  /widgets/{widgetId}:
    get:
      responses:
        "200": {content: {application/json: {schema: {$ref: '#/components/schemas/Gizmo'}}}}
    delete:
      responses: {"204": {description: gone}}
components:
  schemas:
    Gizmo:
      type: object
      properties:
        id: {type: string}
        label: {type: string}
`
	cases := []struct {
		name    string
		wrapper string
		want    string
	}{
		{"a wrapped response names the real key, not the declared one",
			"wrapped: true, key: records", "records"},
		{"an unwrapped response is unwrapped, whatever the document wraps",
			"wrapped: false", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			document := fmt.Sprintf(wrappedDoc, testCase.wrapper)
			resource := resourceByKey(t, mustDerive(t, document, testConfig()), "widget")
			if resource.ListWrapperKey != testCase.want {
				t.Errorf("resource wrapper key = %q, want %q", resource.ListWrapperKey, testCase.want)
			}
			datasource := datasourceByKey(t, mustDerive(t, document, testConfig()), "widget")
			if datasource.ListWrapperKey != testCase.want {
				t.Errorf("datasource wrapper key = %q, want %q", datasource.ListWrapperKey, testCase.want)
			}
		})
	}
}

func TestUnit_Attributes_ConditionalValidity(t *testing.T) {
	tree := thingTree(t)
	want := []ConditionalValidity{{Property: "mode", WhenPropertyEquals: "standard", AttributesValidWhenEqual: []string{"quantity"}}}
	if !reflect.DeepEqual(tree.ConditionalValidities, want) {
		t.Errorf("conditional validities = %+v, want %+v", tree.ConditionalValidities, want)
	}
}

func TestUnit_Attributes_Dependencies(t *testing.T) {
	tree := thingTree(t)
	want := []Dependency{{Attribute: "ratio", Requires: []string{"quantity"}}}
	if !reflect.DeepEqual(tree.Dependencies, want) {
		t.Errorf("dependencies = %+v, want %+v", tree.Dependencies, want)
	}
}

func TestUnit_Attributes_MutuallyExclusiveGroups(t *testing.T) {
	tree := thingTree(t)
	want := [][]string{{"region", "tier"}}
	if !reflect.DeepEqual(tree.MutuallyExclusiveGroups, want) {
		t.Errorf("mutually exclusive groups = %+v, want %+v", tree.MutuallyExclusiveGroups, want)
	}
}

func TestUnit_Attributes_ValidConfigurations(t *testing.T) {
	tree := thingTree(t)
	want := []ValidConfiguration{{
		Discriminator: "mode",
		Variants: []ValidConfigurationVariant{
			{Value: "custom", AttributesValidWhenEqual: []string{"proxy_host"}},
			{Value: "standard", AttributesValidWhenEqual: []string{"quantity"}},
		},
	}}
	if !reflect.DeepEqual(tree.ValidConfigurations, want) {
		t.Errorf("valid configurations = %+v, want %+v", tree.ValidConfigurations, want)
	}
}

func TestUnit_Attributes_SilentlyIgnoredOnUpdate(t *testing.T) {
	if a := attribute(t, thingTree(t), "notes"); !a.IgnoredOnUpdate {
		t.Errorf("x-tfpfgen-ignored-on-update not carried: %+v", a)
	}
}

func TestUnit_Attributes_WireNamesBecomeSnakeCase(t *testing.T) {
	derived := attribute(t, thingTree(t), "proxy_host")
	if derived.WireName != "proxyHost" {
		t.Errorf("wire name = %q", derived.WireName)
	}
}

// Document property order is preserved: create-schema properties first in
// their declared order, response-only ones appended after.
func TestUnit_Attributes_OrderFollowsTheDocument(t *testing.T) {
	tree := thingTree(t)
	var got []string
	for _, candidate := range tree.Attributes {
		got = append(got, candidate.Name)
	}
	want := []string{
		"name", "mode", "region", "filled", "tier", "proxy_host", "notes", "quantity",
		"ratio", "enabled", "labels", "rules", "settings", "extras",
		"forced", "flaky", "stamp", "id", "etag",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attribute order:\n got %v\nwant %v", got, want)
	}
}

// When the read schema declares no id, one is synthesized from the item
// path parameter.
func TestUnit_Attributes_IDSynthesizedFromThePathParameter(t *testing.T) {
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
	resource := resourceByKey(t, mustDerive(t, spec, testConfig()), "code")
	id := resource.Attributes.Attributes[0]
	if id.Name != "id" || id.ComputedOptionalRequired != Computed {
		t.Fatalf("first attribute = %+v, want the synthesized computed id", id)
	}
	if id.WireName != "codeNumber" {
		t.Errorf("id maps from %q, want the item path parameter", id.WireName)
	}
	if id.Type != TypeInt64 {
		t.Errorf("id kind = %q, want the parameter's integer", id.Type)
	}
}

// Shapes derivation refuses to guess at: unions, missing types, arrays
// without a usable element.
func TestUnit_Attributes_ExcludedShapes(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /widgets:
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
  /widgets/{widgetId}:
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
	resource := resourceByKey(t, mustDerive(t, spec, testConfig()), "widget")
	for name, wantReason := range map[string]string{
		"mystery": "no type",
		"bare":    "no items",
		"grid":    `array of "array"`,
		"blobs":   "free-form",
	} {
		derived := attribute(t, resource.Attributes, name)
		if !derived.Unsupported {
			t.Errorf("%s: derived %q instead of refusing", name, derived.Type)
			continue
		}
		if !strings.Contains(derived.UnsupportedReason, wantReason) {
			t.Errorf("%s: reason = %q, want it to mention %q", name, derived.UnsupportedReason, wantReason)
		}
	}
}

// allOf composition folds flat: branch properties combine, requireds
// union, and reference-site extensions win.
func TestUnit_Attributes_AllOfFoldsFlat(t *testing.T) {
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
	resource := resourceByKey(t, mustDerive(t, spec, testConfig()), "part")
	if a := attribute(t, resource.Attributes, "core"); a.ComputedOptionalRequired != Required {
		t.Errorf("core = %+v, want the branch's required to hold", a)
	}
	if a := attribute(t, resource.Attributes, "extra"); a.Type != TypeInt64 {
		t.Errorf("extra = %+v", a)
	}
}

func TestUnit_EnsureParentParameters_AddsWhatNoBodyDeclares(t *testing.T) {
	tree := &AttributeTree{Attributes: []Attribute{
		{Name: "id", WireName: "id", Type: TypeString, ComputedOptionalRequired: Computed},
		{Name: "name", WireName: "name", Type: TypeString, ComputedOptionalRequired: Required},
	}}
	ensureParentParameters(tree, []URLPathParameter{
		{Name: "owner", Type: TypeString},
		{Name: "repo", Type: TypeString},
	}, "")

	if len(tree.Attributes) != 4 {
		t.Fatalf("want four attributes, got %d", len(tree.Attributes))
	}
	// Prepended, in path order, ahead of everything the body declares.
	for index, want := range []string{"owner", "repo", "id", "name"} {
		if tree.Attributes[index].Name != want {
			t.Fatalf("attribute %d = %q, want %q", index, tree.Attributes[index].Name, want)
		}
	}
	for _, candidate := range tree.Attributes[:2] {
		if candidate.ComputedOptionalRequired != Required {
			t.Fatalf("%s must be required, got %s", candidate.Name, candidate.ComputedOptionalRequired)
		}
		if !candidate.RequiresReplace {
			t.Fatalf("%s must force replacement: addressing is not editable", candidate.Name)
		}
	}
}

func TestUnit_EnsureParentParameters_LeavesWhatTheBodyAlreadyDeclares(t *testing.T) {
	// The document is a better authority on its own field than the URL is.
	tree := &AttributeTree{Attributes: []Attribute{
		{Name: "owner", WireName: "owner", Type: TypeString, ComputedOptionalRequired: Optional},
	}}
	ensureParentParameters(tree, []URLPathParameter{{Name: "owner", Type: TypeString}}, "")

	if len(tree.Attributes) != 1 {
		t.Fatalf("a declared parent must not be added twice, got %d", len(tree.Attributes))
	}
	if tree.Attributes[0].ComputedOptionalRequired != Optional {
		t.Fatalf("a declared parent keeps its declared presence, got %s", tree.Attributes[0].ComputedOptionalRequired)
	}
}

func TestUnit_ParentParameters_DropsTheItemKey(t *testing.T) {
	parameters := []URLPathParameter{{Name: "owner"}, {Name: "repo"}, {Name: "ruleset_id"}}
	got := parentParameters(parameters)
	if len(got) != 2 || got[0].Name != "owner" || got[1].Name != "repo" {
		t.Fatalf("want owner and repo, got %+v", got)
	}
	if parentParameters(parameters[2:]) != nil {
		t.Fatal("a lone path parameter is the item key, not a parent")
	}
	if parentParameters(nil) != nil {
		t.Fatal("no parameters means no parents")
	}
}

func TestUnit_BuildAttribute_CarriesTheDocumentsDescription(t *testing.T) {
	// A request schema and a response schema describe the same field, and
	// one is routinely annotated where the other is bare.
	described := &specmodel.Schema{Type: "string", Description: "  Name of the alert rule.  "}
	bare := &specmodel.Schema{Type: "string"}

	writable, _ := buildAttribute("ruleName", foldedProperty{create: described, read: bare})
	if writable.Description != "Name of the alert rule." {
		t.Fatalf("the create side's prose must carry, trimmed: %q", writable.Description)
	}

	fromRead, _ := buildAttribute("ruleName", foldedProperty{create: bare, read: described})
	if fromRead.Description != "Name of the alert rule." {
		t.Fatalf("the read side's prose must carry when the create side is bare: %q", fromRead.Description)
	}

	computed, _ := buildAttribute("ruleId", foldedProperty{read: described})
	if computed.Description != "Name of the alert rule." {
		t.Fatalf("a response-only attribute keeps its prose: %q", computed.Description)
	}

	none, _ := buildAttribute("path", foldedProperty{create: bare, read: bare})
	if none.Description != "" {
		t.Fatalf("an undescribed property carries nothing, got %q", none.Description)
	}
}

func TestUnit_BuildTree_CarriesTheObjectsDescription(t *testing.T) {
	read := &specmodel.Schema{Type: "object", Description: "A rule that raises alerts",
		Properties: []specmodel.Property{{Name: "id", Schema: &specmodel.Schema{Type: "string"}}}}
	tree := buildAttributeTree(nil, read, nil, false)
	if tree.Description != "A rule that raises alerts" {
		t.Fatalf("the object's prose must reach the tree, got %q", tree.Description)
	}
}

// updateBodySchema builds the three sides of one entity for the
// create-minus-update rule: a create body, a read body, and an update body
// declaring only some of what create takes.
func updateBodySchema() (create, read, update *specmodel.Schema) {
	text := func() *specmodel.Schema { return &specmodel.Schema{Type: "string"} }
	create = &specmodel.Schema{Type: "object", Required: []string{"name"}, Properties: []specmodel.Property{
		{Name: "name", Schema: text()},
		{Name: "region", Schema: text()},
		{Name: "description", Schema: text()},
	}}
	read = &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "id", Schema: text()},
		{Name: "name", Schema: text()},
		{Name: "region", Schema: text()},
		{Name: "description", Schema: text()},
		{Name: "createdAt", Schema: &specmodel.Schema{Type: "string", ReadOnly: true}},
	}}
	update = &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "name", Schema: text()},
		{Name: "description", Schema: text()},
	}}
	return create, read, update
}

// TestUnit_BuildTree_UpdateBodyDifferenceForcesReplacement proves the
// document's own account of immutability reaches the schema: a property the
// create body declares and the update body does not is one the API offers
// no way to change, which is RequiresReplace.
func TestUnit_BuildTree_UpdateBodyDifferenceForcesReplacement(t *testing.T) {
	create, read, update := updateBodySchema()
	tree := buildAttributeTree(create, read, update, false)

	if a := attribute(t, tree, "region"); !a.RequiresReplace {
		t.Error("region is absent from the update body, so it must force replacement")
	}
	for _, name := range []string{"name", "description"} {
		if a := attribute(t, tree, name); a.RequiresReplace {
			t.Errorf("%s is declared by the update body, so it must not force replacement", name)
		}
	}
}

// TestUnit_BuildTree_ComputedAttributesNeverForceReplacement proves the
// existing computed guard still holds under the new rule. A response-only
// or read-only property is absent from the update body for the
// uninteresting reason that it is absent from every request body, and
// forcing replacement on one the practitioner cannot set would be nonsense.
func TestUnit_BuildTree_ComputedAttributesNeverForceReplacement(t *testing.T) {
	create, read, update := updateBodySchema()
	tree := buildAttributeTree(create, read, update, false)

	for _, name := range []string{"id", "created_at"} {
		derived := attribute(t, tree, name)
		if derived.ComputedOptionalRequired != Computed {
			t.Fatalf("%s is %s, want computed — the fixture is wrong", name, derived.ComputedOptionalRequired)
		}
		if derived.RequiresReplace {
			t.Errorf("%s is computed, so it must not force replacement", name)
		}
	}
}

// TestUnit_BuildTree_AbsentUpdateBodyIsSilence proves a nil update side
// asserts nothing. A document that declares no update schema is silent, not
// restrictive, and reading silence as refusal would force replacement on
// every writable attribute of every entity whose update body the document
// happens not to spell.
func TestUnit_BuildTree_AbsentUpdateBodyIsSilence(t *testing.T) {
	create, read, _ := updateBodySchema()

	tree := buildAttributeTree(create, read, nil, false)
	for _, name := range []string{"name", "region", "description"} {
		if a := attribute(t, tree, name); a.RequiresReplace {
			t.Errorf("%s forces replacement with no update body to read; silence is not refusal", name)
		}
	}

	// An update body that resolves to nothing is the same silence.
	empty := buildAttributeTree(create, read, &specmodel.Schema{}, false)
	for _, name := range []string{"name", "region", "description"} {
		if a := attribute(t, empty, name); a.RequiresReplace {
			t.Errorf("%s forces replacement against an empty update body", name)
		}
	}
}

// TestUnit_BuildTree_UpdateDifferenceRecursesIntoNestedObjects proves the
// three sides are folded in parallel all the way down, so a nested property
// the update body's own nested schema omits is found too.
func TestUnit_BuildTree_UpdateDifferenceRecursesIntoNestedObjects(t *testing.T) {
	text := func() *specmodel.Schema { return &specmodel.Schema{Type: "string"} }
	block := func(names ...string) *specmodel.Schema {
		schema := &specmodel.Schema{Type: "object"}
		for _, name := range names {
			schema.Properties = append(schema.Properties, specmodel.Property{Name: name, Schema: text()})
		}
		return schema
	}

	create := &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "server", Schema: block("host", "tenantId")},
	}}
	update := &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "server", Schema: block("host")},
	}}

	tree := buildAttributeTree(create, create, update, false)
	server := attribute(t, tree, "server")
	if server.NestedAttributes == nil {
		t.Fatal("server must derive a nested tree")
	}
	if a := attribute(t, server.NestedAttributes, "tenant_id"); !a.RequiresReplace {
		t.Error("server.tenant_id is absent from the nested update schema, so it must force replacement")
	}
	if a := attribute(t, server.NestedAttributes, "host"); a.RequiresReplace {
		t.Error("server.host is declared by the nested update schema, so it must not force replacement")
	}
}
