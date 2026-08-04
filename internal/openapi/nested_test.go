package openapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/generate"
)

// nestedSpec is a document whose widget carries every nested shape inference has to
// handle: a collection of objects, a single object, an object nested inside an object, a
// scalar collection inside an object, and a schema that contains itself.
const nestedSpec = `
openapi: 3.0.3
info: {title: Nested API, version: "1.0"}
paths:
  /widgets:
    post:
      operationId: createWidget
      tags: [Widgets]
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/WidgetInfo'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Widget'}
  /widgets/{id}:
    get:
      operationId: getWidget
      tags: [Widgets]
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Widget'}
    put:
      operationId: updateWidget
      tags: [Widgets]
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/WidgetInfo'}
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Widget'}
    delete:
      operationId: deleteWidget
      tags: [Widgets]
      responses:
        "204": {description: gone}
components:
  schemas:
    WidgetInfo:
      type: object
      required: [name]
      properties:
        name: {type: string}
        parts:
          type: array
          items: {$ref: '#/components/schemas/Part'}
        placement: {$ref: '#/components/schemas/Placement'}
        tree: {$ref: '#/components/schemas/Node'}
    Widget:
      type: object
      properties:
        id: {type: string, readOnly: true}
        name: {type: string}
        parts:
          type: array
          items: {$ref: '#/components/schemas/Part'}
        placement: {$ref: '#/components/schemas/Placement'}
        tree: {$ref: '#/components/schemas/Node'}
        audit:
          type: array
          readOnly: true
          items: {$ref: '#/components/schemas/AuditEntry'}
    Part:
      type: object
      required: [sku]
      properties:
        sku: {type: string}
        tags:
          type: array
          items: {type: string}
        origin: {$ref: '#/components/schemas/Origin'}
    Origin:
      type: object
      properties:
        country: {type: string}
    Placement:
      type: object
      properties:
        row: {type: integer}
    AuditEntry:
      type: object
      properties:
        at: {type: string}
    Node:
      type: object
      properties:
        label: {type: string}
        children:
          type: array
          items: {$ref: '#/components/schemas/Node'}
`

func inferNested(t *testing.T) (blueprint.Resource, []Caveat) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "api.yaml")
	if err := os.WriteFile(path, []byte(nestedSpec), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	res, notes, err := doc.Infer(find(t, doc.Discover(), "widget"), inferOptions())
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	return res, notes
}

// TestUnit_Infer_NestedCollectionAndSingleObject covers the two nested kinds and the SDK
// type each implies.
//
// The SDK holds many objects as a slice and one behind a pointer. That decides the
// generated helper's signature, so an inferred draft that got it the wrong way round
// would produce a helper that does not compile against its own model.
func TestUnit_Infer_NestedCollectionAndSingleObject(t *testing.T) {
	t.Parallel()

	res, _ := inferNested(t)

	parts := attrByName(t, res, "parts")
	if parts.Type.Kind != blueprint.KindSetNested {
		t.Errorf("parts kind = %q, want set_nested", parts.Type.Kind)
	}
	if got := parts.Wire.SDKGoType; got != "[]widgets.Part" {
		t.Errorf("parts sdkGoType = %q, want []widgets.Part", got)
	}
	if n := parts.Type.NestedObject; n == nil || n.SDKType != "widgets.Part" {
		t.Errorf("parts sdkType = %+v, want widgets.Part", n)
	}

	placement := attrByName(t, res, "placement")
	if placement.Type.Kind != blueprint.KindSingleNested {
		t.Errorf("placement kind = %q, want single_nested", placement.Type.Kind)
	}
	if got := placement.Wire.SDKGoType; got != "*widgets.Placement" {
		t.Errorf("placement sdkGoType = %q, want *widgets.Placement", got)
	}

	// A collection's helpers read plural, a single object's singular.
	if got := parts.Type.NestedObject.ExpandFunc; got != "expandWidgetParts" {
		t.Errorf("parts expandFunc = %q, want expandWidgetParts", got)
	}
	if got := placement.Type.NestedObject.ExpandFunc; got != "expandWidgetPlacement" {
		t.Errorf("placement expandFunc = %q, want expandWidgetPlacement", got)
	}
}

// TestUnit_Infer_NestedObjectsRecurse checks that inference descends, not just that it
// stops refusing.
func TestUnit_Infer_NestedObjectsRecurse(t *testing.T) {
	t.Parallel()

	res, _ := inferNested(t)

	part := attrByName(t, res, "parts").Type.NestedObject
	if part == nil {
		t.Fatal("parts has no nested object")
	}

	var origin, tags *blueprint.Attribute
	for i := range part.Attributes {
		switch part.Attributes[i].Name {
		case "origin":
			origin = &part.Attributes[i]
		case "tags":
			tags = &part.Attributes[i]
		}
	}

	// An object inside an object: level two, which the emitter now generates.
	if origin == nil || origin.Type.NestedObject == nil {
		t.Fatalf("parts.origin should be a nested object: %+v", origin)
	}
	if got := origin.Type.NestedObject.GoTypeName; got != "WidgetOriginModel" {
		t.Errorf("parts.origin goTypeName = %q, want WidgetOriginModel", got)
	}

	// And a scalar collection inside an object still maps to a set with an element type.
	if tags == nil || tags.Type.Kind != blueprint.KindSet {
		t.Fatalf("parts.tags should be a set: %+v", tags)
	}
	if tags.Type.ElementType == nil || tags.Type.ElementType.Kind != blueprint.KindString {
		t.Errorf("parts.tags element type = %+v, want string", tags.Type.ElementType)
	}
}

// TestUnit_Infer_ReadOnlyNestedObjectSendsNothing checks the writability thread.
//
// A field inside a read-only object cannot be written however its own schema marks it, and
// an expand conversion on it would name a helper the emitter never generates -- construct
// skips a shape whose attribute is SkipExpand.
func TestUnit_Infer_ReadOnlyNestedObjectSendsNothing(t *testing.T) {
	t.Parallel()

	res, _ := inferNested(t)

	audit := attrByName(t, res, "audit")
	if !audit.Wire.SkipExpand {
		t.Error("a read-only nested collection must not be expanded")
	}
	if audit.ComputedOptionalRequired != blueprint.Computed {
		t.Errorf("audit presence = %q, want computed", audit.ComputedOptionalRequired)
	}

	for _, child := range audit.Type.NestedObject.Attributes {
		if child.Wire.Expand != nil {
			t.Errorf("audit.%s must not carry an expand: its parent is never sent", child.Name)
		}
		if !child.Wire.SkipExpand {
			t.Errorf("audit.%s should be marked skipExpand", child.Name)
		}
		if child.ComputedOptionalRequired != blueprint.Computed {
			t.Errorf("audit.%s presence = %q, want computed", child.Name,
				child.ComputedOptionalRequired)
		}
	}

	// The writable sibling is the control: its children do carry expands.
	parts := attrByName(t, res, "parts")
	if parts.Wire.Expand == nil {
		t.Fatal("parts is writable and should carry an expand")
	}
	sku := parts.Type.NestedObject.Attributes[0]
	if sku.Wire.Expand == nil {
		t.Errorf("parts.%s should carry an expand", sku.Name)
	}
}

// TestUnit_Infer_SelfReferentialSchemaIsRefusedByName is the case that would otherwise
// recurse forever.
//
// A schema containing itself has a depth decided by the data, not the schema, so it cannot
// become a fixed set of generated types. This is the shape ms365's settings catalogue has,
// and the message says to write that resource by hand rather than implying a flatter form
// would do.
func TestUnit_Infer_SelfReferentialSchemaIsRefusedByName(t *testing.T) {
	t.Parallel()

	res, notes := inferNested(t)

	for _, a := range res.Schema.Attributes {
		if a.Name == "tree" {
			t.Error("a self-referential schema must not become an attribute")
		}
	}

	all := strings.Join(noteStrings(notes), "\n")
	if !strings.Contains(all, "tree") {
		t.Errorf("the self-referential field should be named:\n%s", all)
	}
	if !strings.Contains(all, "contains itself") {
		t.Errorf("the note should say why:\n%s", all)
	}
	if !strings.Contains(all, "by hand") {
		t.Errorf("the note should say what to do instead:\n%s", all)
	}
}

// TestUnit_Infer_NestedIdentifiersAreUnique guards the collision arbitrary depth makes
// likely.
//
// Every nested object contributes five package-level identifiers to one generated package.
// Render refuses a repeat, so resolving it here is the difference between a draft that
// emits and one that does not.
func TestUnit_Infer_NestedIdentifiersAreUnique(t *testing.T) {
	t.Parallel()

	res, _ := inferNested(t)

	seen := map[string]string{}

	var walk func(attrs []blueprint.Attribute, path string)
	walk = func(attrs []blueprint.Attribute, path string) {
		for _, a := range attrs {
			n := a.Type.NestedObject
			if n == nil {
				continue
			}
			at := path + a.Name
			for _, id := range []string{
				n.GoTypeName, n.AttrTypesVar, n.ObjectTypeVar, n.ExpandFunc, n.FlattenFunc,
			} {
				if id == "" {
					t.Errorf("%s: a nested object left a generated identifier empty: %+v", at, n)
					continue
				}
				if first, dup := seen[id]; dup {
					t.Errorf("%s reuses identifier %q, already used by %s", at, id, first)
				}
				seen[id] = at
			}
			walk(n.Attributes, at+".")
		}
	}
	walk(res.Schema.Attributes, "")
}

// TestUnit_Infer_InferredNestedBlueprintRenders is the end-to-end assertion.
//
// Identifiers agreeing with the curated blueprint is reassuring but not proof: what matters
// is that a wholly inferred nested shape passes validation and reaches the renderer, which
// is where a missing helper name or a mismatched SDK type actually bites. The pilot's
// provider block supplies the parts inference does not invent.
func TestUnit_Infer_InferredNestedBlueprintRenders(t *testing.T) {
	t.Parallel()

	res, _ := inferNested(t)

	bp := blueprint.Blueprint{
		FormatVersion: blueprint.FormatVersion,
		Provider: blueprint.Provider{
			Name:       "example",
			TypePrefix: "example",
			GoModule:   "example.com/provider",
			SDK: blueprint.SDKModule{
				Dialect:    blueprint.DialectRestyService,
				ModulePath: "example.com/sdk",
				ClientType: "*sdk.Client",
			},
		},
		Resources: []blueprint.Resource{res},
	}

	if err := bp.Validate(); err != nil {
		t.Fatalf("an inferred blueprint must validate: %v", err)
	}

	v, err := generate.Resource(bp, res, generate.Options{BlueprintPath: "b", BlueprintSHA256: "s"})
	if err != nil {
		t.Fatalf("an inferred blueprint must render: %v", err)
	}

	// One model per nested object, at every depth: Part, Origin, Placement, AuditEntry.
	want := []string{
		"WidgetPartModel", "WidgetOriginModel", "WidgetPlacementModel", "WidgetAuditEntryModel",
	}
	got := map[string]bool{}
	for _, nm := range v.NestedModels {
		got[nm.GoTypeName] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("no generated model %q; got %v", w, got)
		}
	}
}

func TestUnit_Infer_Pluralise(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"Part":       "Parts",
		"Assignment": "Assignments",
		"TagFilter":  "TagFilters",
		// A trailing s, x, z or a ch/sh cluster takes es, so the identifier does not read
		// as a typo.
		"Address": "Addresses",
		"Box":     "Boxes",
		"Match":   "Matches",
		// Consonant plus y becomes ies; a vowel plus y does not.
		"Policy": "Policies",
		"Key":    "Keys",
		"":       "",
	}

	for in, want := range tests {
		if got := pluralise(in); got != want {
			t.Errorf("pluralise(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUnit_Infer_ConstraintsAreExtractedForTheKindThatCanHoldThem.
func TestUnit_Infer_ConstraintsAreExtractedForTheKindThatCanHoldThem(t *testing.T) {
	t.Parallel()

	const spec = `
openapi: 3.0.3
info: {title: Bound API, version: "1.0"}
paths:
  /things:
    post:
      operationId: createThing
      tags: [Things]
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Thing'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Thing'}
  /things/{id}:
    get:
      operationId: getThing
      tags: [Things]
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Thing'}
    put:
      operationId: updateThing
      tags: [Things]
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Thing'}
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Thing'}
    delete:
      operationId: deleteThing
      tags: [Things]
      responses:
        "204": {description: gone}
components:
  schemas:
    Thing:
      type: object
      properties:
        id: {type: string, readOnly: true}
        name: {type: string, pattern: '^[a-z]+$', minLength: 1, maxLength: 63}
        weight: {type: integer, minimum: 1, maximum: 100}
        ratio: {type: number, minimum: 0, maximum: 1}
        labels:
          type: array
          minItems: 1
          maxItems: 8
          items: {type: string, maxLength: 32}
        lookahead: {type: string, pattern: '^(?=x)y$'}
`

	dir := t.TempDir()
	path := filepath.Join(dir, "api.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	res, notes, err := doc.Infer(find(t, doc.Discover(), "thing"), inferOptions())
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	name := attrByName(t, res, "name").Type.Constraints
	if name.Pattern != "^[a-z]+$" {
		t.Errorf("name pattern = %q", name.Pattern)
	}
	if name.MinLength == nil || *name.MinLength != 1 || name.MaxLength == nil || *name.MaxLength != 63 {
		t.Errorf("name length bounds = %+v", name)
	}

	weight := attrByName(t, res, "weight").Type.Constraints
	if weight.Minimum == nil || *weight.Minimum != 1 || weight.Maximum == nil || *weight.Maximum != 100 {
		t.Errorf("weight range = %+v", weight)
	}

	// A JSON number maps to float64 to match the SDK, so its bounds are kept.
	ratio := attrByName(t, res, "ratio").Type.Constraints
	if ratio.Maximum == nil || *ratio.Maximum != 1 {
		t.Errorf("ratio range = %+v", ratio)
	}

	// The collection's own size, and the element's bound on the element type.
	labels := attrByName(t, res, "labels").Type
	if labels.Constraints.MinItems == nil || *labels.Constraints.MinItems != 1 {
		t.Errorf("labels size = %+v", labels.Constraints)
	}
	if labels.ElementType == nil || labels.ElementType.Constraints.MaxLength == nil ||
		*labels.ElementType.Constraints.MaxLength != 32 {
		t.Errorf("label element bound = %+v", labels.ElementType)
	}

	// A pattern RE2 cannot parse is dropped and reported, not passed on: the generated code
	// compiles it with regexp.MustCompile, so it would panic at provider start.
	if got := attrByName(t, res, "lookahead").Type.Constraints.Pattern; got != "" {
		t.Errorf("a pattern Go cannot compile should be dropped, got %q", got)
	}

	all := strings.Join(noteStrings(notes), "\n")
	if !strings.Contains(all, "not a valid Go regular expression") {
		t.Errorf("the dropped pattern should be reported:\n%s", all)
	}
	if !strings.Contains(all, "lookahead") {
		t.Errorf("the note should name the field or the construct:\n%s", all)
	}
}
