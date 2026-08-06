package openapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// inferSpec exercises the shapes inference has to get right: readOnly versus
// writable, required versus not, an enumeration behind a $ref, a scalar
// collection, a nested object, and the HAL envelope.
const inferSpec = `
openapi: 3.0.3
info:
  title: Test API
  version: 1.0.0
paths:
  /widgets:
    post:
      operationId: createWidget
      summary: create widget
      tags: [Widgets]
      requestBody:
        content:
          application/hal+json:
            schema:
              $ref: '#/components/schemas/Widgets_API_WidgetInfo'
      responses:
        '201':
          content:
            application/hal+json:
              schema:
                $ref: '#/components/schemas/Widgets_API_Widget'
  /widgets/{id}:
    get:
      operationId: getWidget
      summary: retrieve widget
      tags: [Widgets]
      responses:
        '200':
          content:
            application/hal+json:
              schema:
                $ref: '#/components/schemas/Widgets_API_Widget'
    put:
      operationId: updateWidget
      tags: [Widgets]
      responses:
        '200':
          content:
            application/hal+json:
              schema:
                $ref: '#/components/schemas/Widgets_API_Widget'
    delete:
      operationId: deleteWidget
      tags: [Widgets]
      responses:
        '204': {}
components:
  schemas:
    Widgets_API_Mode:
      type: string
      enum: [fast, slow]
    Widgets_API_Part:
      type: object
      properties:
        partId:
          type: string
    Widgets_API_WidgetInfo:
      type: object
      required: [name]
      properties:
        name:
          type: string
          description: The widget's name.
        enabled:
          type: boolean
        weight:
          type: number
        count:
          type: integer
        mode:
          $ref: '#/components/schemas/Widgets_API_Mode'
        labelIds:
          type: array
          items:
            type: string
        legacyRef:
          type: string
          deprecated: true
    Widgets_API_Widget:
      type: object
      required: [name]
      properties:
        id:
          type: string
          readOnly: true
        createdAt:
          type: string
          readOnly: true
        name:
          type: string
        enabled:
          type: boolean
        weight:
          type: number
        count:
          type: integer
        mode:
          $ref: '#/components/schemas/Widgets_API_Mode'
        labelIds:
          type: array
          items:
            type: string
        legacyRef:
          type: string
          deprecated: true
        parts:
          type: array
          items:
            $ref: '#/components/schemas/Widgets_API_Part'
        _links:
          type: object
          properties:
            self:
              type: string
`

func inferOptions() InferOptions {
	return InferOptions{
		Provider:          "example",
		SDKServiceRoot:    "example.com/sdk/api",
		SDKAccessorPrefix: "r.client.API",
		APIVersionDir:     "v1",
	}
}

func inferWidget(t *testing.T) (blueprint.Resource, []Caveat) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "api.yaml")
	if err := os.WriteFile(path, []byte(inferSpec), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	c := find(t, doc.Discover(), "widget")

	res, notes, err := doc.Infer(c, inferOptions())
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	return res, notes
}

// TestUnit_Infer_ASplitUpdateBodyIsInferred: an update whose request schema is its own
// named type must land in the blueprint as updateRequestType, so curation starts from
// the truth instead of discovering the split as a bindings failure.
func TestUnit_Infer_ASplitUpdateBodyIsInferred(t *testing.T) {
	t.Parallel()

	// The fixture's PUT gains its own named request schema -- the
	// agent-to-server/bgp shape -- and the schema itself rides the components
	// block's indentation.
	spec := strings.Replace(inferSpec,
		`    put:
      operationId: updateWidget
      tags: [Widgets]`,
		`    put:
      operationId: updateWidget
      tags: [Widgets]
      requestBody:
        content:
          application/hal+json:
            schema:
              $ref: '#/components/schemas/Widgets_API_WidgetUpdate'`,
		1)
	spec += `    Widgets_API_WidgetUpdate:
      type: object
      properties:
        name:
          type: string
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

	res, _, err := doc.Infer(find(t, doc.Discover(), "widget"), inferOptions())
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	if got := res.Binding.Body.UpdateRequestType; got != "widgets.WidgetUpdate" {
		t.Errorf("UpdateRequestType = %q, want widgets.WidgetUpdate", got)
	}
	if got := res.Binding.Body.UpdateConstructorExpr; got != "&widgets.WidgetUpdate{}" {
		t.Errorf("UpdateConstructorExpr = %q", got)
	}

	// And the common shape stays silent: the unmodified fixture's update carries no
	// request body, which must not be recorded as a split.
	plain, _ := inferWidget(t)
	if plain.Binding.Body.UpdateRequestType != "" {
		t.Errorf("an update with no body of its own inferred a split: %q",
			plain.Binding.Body.UpdateRequestType)
	}
}

func attrByName(t *testing.T, r blueprint.Resource, name string) blueprint.Attribute {
	t.Helper()
	for _, a := range r.Schema.Attributes {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no attribute %q; got %v", name, attrNames(r))
	return blueprint.Attribute{}
}

func attrNames(r blueprint.Resource) []string {
	out := make([]string, 0, len(r.Schema.Attributes))
	for _, a := range r.Schema.Attributes {
		out = append(out, a.Name)
	}
	return out
}

// TestUnit_Infer_ProducesAValidBlueprint is the assertion that matters most: the
// emitter is entitled to assume a validated blueprint, so inference producing one
// that fails validation would push the failure into generation.
func TestUnit_Infer_ProducesAValidBlueprint(t *testing.T) {
	t.Parallel()

	res, _ := inferWidget(t)

	bp := blueprint.Blueprint{
		FormatVersion: blueprint.FormatVersion,
		Provider: blueprint.Provider{
			Name: "example", GoModule: "example.com/p", TypePrefix: "example",
			SDK: blueprint.SDKModule{
				Dialect: blueprint.DialectRestyService, ModulePath: "example.com/sdk", ClientType: "*sdk.Client",
			},
		},
		Resources: []blueprint.Resource{res},
	}

	if err := bp.Validate(); err != nil {
		t.Fatalf("inference produced a blueprint that does not validate:\n%v", err)
	}
}

// TestUnit_Infer_PresenceComesFromReadOnlyAndRequired pins the whole of presence
// inference, which is the merge of what may be written with what can be read.
func TestUnit_Infer_PresenceComesFromReadOnlyAndRequired(t *testing.T) {
	t.Parallel()

	res, _ := inferWidget(t)

	tests := []struct {
		attr string
		want blueprint.ComputedOptionalRequired
		why  string
	}{
		{"name", blueprint.Required, "in the request body and listed as required"},
		{"enabled", blueprint.Optional, "in the request body, not required"},
		{"mode", blueprint.Optional, "an enumeration in the request body"},
		{"id", blueprint.Computed, "readOnly in the response"},
		{"created_at", blueprint.Computed, "readOnly in the response"},
	}

	for _, tc := range tests {
		t.Run(tc.attr, func(t *testing.T) {
			t.Parallel()
			got := attrByName(t, res, tc.attr)
			if got.ComputedOptionalRequired != tc.want {
				t.Errorf("%s is %q, want %q: %s", tc.attr, got.ComputedOptionalRequired, tc.want, tc.why)
			}
		})
	}
}

// TestUnit_Infer_ComputedAttributesAreNotSent: a computed attribute must be
// excluded from the request body, or the generated construct function refers to a
// field the request model may not have.
func TestUnit_Infer_ComputedAttributesAreNotSent(t *testing.T) {
	t.Parallel()

	res, _ := inferWidget(t)

	id := attrByName(t, res, "id")
	if !id.Wire.SkipExpand {
		t.Error("a computed attribute must set skipExpand")
	}
	if id.Wire.Expand != nil {
		t.Error("a computed attribute must have no expand conversion")
	}
	if id.Wire.Flatten == nil {
		t.Error("a computed attribute must still be flattened, or state never sees it")
	}

	name := attrByName(t, res, "name")
	if name.Wire.SkipExpand || name.Wire.Expand == nil {
		t.Error("a writable attribute must be expanded")
	}
}

func TestUnit_Infer_TypeAndConversionAgree(t *testing.T) {
	t.Parallel()

	res, _ := inferWidget(t)

	tests := []struct {
		attr      string
		kind      blueprint.TypeKind
		sdkType   string
		flattenFn string
	}{
		{"name", blueprint.KindString, "*string", "convert.PtrStringToFramework"},
		{"enabled", blueprint.KindBool, "*bool", "convert.PtrBoolToFramework"},
		{"count", blueprint.KindInt64, "*int64", "convert.PtrInt64ToFramework"},
		// An unformatted "number" maps to float64 because that is what the SDK
		// dialect represents it as. Inferring types.Number here would pair a
		// Number model field with a Float64 conversion, which does not compile.
		{"weight", blueprint.KindFloat64, "*float64", "convert.PtrFloat64ToFramework"},
		{"label_ids", blueprint.KindSet, "[]string", "convert.StringSliceToFrameworkSet"},
	}

	for _, tc := range tests {
		t.Run(tc.attr, func(t *testing.T) {
			t.Parallel()

			a := attrByName(t, res, tc.attr)
			if a.Type.Kind != tc.kind {
				t.Errorf("kind = %q, want %q", a.Type.Kind, tc.kind)
			}
			if a.Wire.SDKGoType != tc.sdkType {
				t.Errorf("sdkGoType = %q, want %q", a.Wire.SDKGoType, tc.sdkType)
			}
			if a.Wire.Flatten == nil || a.Wire.Flatten.Func != tc.flattenFn {
				t.Errorf("flatten = %+v, want %s", a.Wire.Flatten, tc.flattenFn)
			}
		})
	}
}

// TestUnit_Infer_EnumsBecomeNamedStringTypes: generated SDKs hold enumerations by
// value as a named type, so the conversion needs a type argument and the field is
// not a pointer.
func TestUnit_Infer_EnumsBecomeNamedStringTypes(t *testing.T) {
	t.Parallel()

	res, _ := inferWidget(t)
	mode := attrByName(t, res, "mode")

	if mode.Type.Kind != blueprint.KindString {
		t.Errorf("an enumeration should surface as a string attribute, got %q", mode.Type.Kind)
	}
	// The specification's component namespace is stripped, as the SDK does.
	if mode.Wire.SDKGoType != "widgets.Mode" {
		t.Errorf("sdkGoType = %q, want widgets.Mode", mode.Wire.SDKGoType)
	}
	if mode.Wire.Expand == nil || len(mode.Wire.Expand.TypeArgs) != 1 {
		t.Fatalf("an enumeration expand needs a type argument: %+v", mode.Wire.Expand)
	}
	if mode.Wire.Expand.TypeArgs[0] != "widgets.Mode" {
		t.Errorf("type argument = %q", mode.Wire.Expand.TypeArgs[0])
	}

	// No validator is generated: these enumerations are open, and rejecting an
	// undocumented value would turn a routine upstream addition into a failure.
	if len(mode.Validators) != 0 {
		t.Errorf("no validator should be generated for an open enumeration: %v", mode.Validators)
	}
}

func TestUnit_Infer_BindsOperationsWithTheRightArity(t *testing.T) {
	t.Parallel()

	res, _ := inferWidget(t)
	b := res.Binding

	if b.Service.TypeName != "Widgets" || b.Service.Accessor != "r.client.API.Widgets" {
		t.Errorf("service = %+v", b.Service)
	}
	if b.Body.RequestType != "widgets.WidgetInfo" || b.Body.ResponseType != "widgets.Widget" {
		t.Errorf("request/response = %q / %q; a split request and response model must survive",
			b.Body.RequestType, b.Body.ResponseType)
	}

	for _, tc := range []struct {
		name   string
		op     *blueprint.Operation
		method string
		arity  blueprint.ReturnArity
	}{
		{"create", b.Create, "CreateWidget", blueprint.ReturnResultTransportError},
		{"read", b.Read, "GetWidget", blueprint.ReturnResultTransportError},
		{"update", b.Update, "UpdateWidget", blueprint.ReturnResultTransportError},
		// Delete returns no body, and the arity decides every error return in the
		// generated function.
		{"delete", b.Delete, "DeleteWidget", blueprint.ReturnTransportError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.op == nil {
				t.Fatalf("%s was not bound", tc.name)
			}
			if tc.op.Method != tc.method {
				t.Errorf("method = %q, want %q", tc.op.Method, tc.method)
			}
			if tc.op.Return != tc.arity {
				t.Errorf("return = %q, want %q", tc.op.Return, tc.arity)
			}
		})
	}
}

// TestUnit_Infer_UpdateStyleComesFromTheVerb guards the difference that silently
// erases data: PUT clears fields the request omits, PATCH leaves them alone.
func TestUnit_Infer_UpdateStyleComesFromTheVerb(t *testing.T) {
	t.Parallel()

	res, _ := inferWidget(t)
	if res.Policy.UpdateStyle != blueprint.UpdatePutFull {
		t.Errorf("a PUT update must infer putFull, got %q", res.Policy.UpdateStyle)
	}

	tests := map[string]struct {
		op   *Operation
		want blueprint.UpdateStyle
	}{
		"patch":     {&Operation{Method: "PATCH"}, blueprint.UpdatePatchMerge},
		"put":       {&Operation{Method: "PUT"}, blueprint.UpdatePutFull},
		"no update": {nil, blueprint.UpdateReplaceOnly},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := updateStyleOf(tc.op); got != tc.want {
				t.Errorf("updateStyleOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnit_Infer_ReportsWhatItSkipped is the other half of the output. Silently
// dropping a field produces a provider that looks complete and cannot express
// part of the API.
func TestUnit_Infer_ReportsWhatItSkipped(t *testing.T) {
	t.Parallel()

	res, notes := inferWidget(t)

	joined := make([]string, 0, len(notes))
	for _, n := range notes {
		joined = append(joined, n.String())
	}
	all := strings.Join(joined, "\n")

	// The HAL envelope describes the transport, not the resource; surfacing it
	// would put self-referential URLs into state.
	if !strings.Contains(all, "_links") {
		t.Errorf("the hypermedia envelope should be reported as skipped:\n%s", all)
	}
	for _, name := range []string{"links", "_links"} {
		for _, a := range res.Schema.Attributes {
			if a.Name == name {
				t.Errorf("%q should not have become an attribute", name)
			}
		}
	}

	// The nested collection is inferred now, so it must not appear in the notes at all.
	// It was a reported gap; a note about it would mean the inference silently regressed.
	if strings.Contains(all, "parts") {
		t.Errorf("parts is inferred and should not be reported as skipped:\n%s", all)
	}
	if a := attrByName(t, res, "parts"); a.Type.NestedObject == nil {
		t.Error("parts should have become a nested attribute")
	}
}

func TestUnit_Infer_CarriesDescriptionsAndDeprecation(t *testing.T) {
	t.Parallel()

	res, _ := inferWidget(t)

	if got := attrByName(t, res, "name").MarkdownDescription; got != "The widget's name." {
		t.Errorf("description = %q", got)
	}
	if got := attrByName(t, res, "legacy_ref").DeprecationMessage; got == "" {
		t.Error("a deprecated property should carry a deprecation message")
	}
}

func TestUnit_Infer_NamesFollowTheConventions(t *testing.T) {
	t.Parallel()

	res, _ := inferWidget(t)

	tests := map[string]string{
		"key":            res.Key,
		"name":           res.Name,
		"goPackage":      res.GoPackage,
		"goPackageAlias": res.GoPackageAlias,
		"goTypeName":     res.GoTypeName,
		"modelTypeName":  res.ModelTypeName,
	}
	want := map[string]string{
		"key":            "widget",
		"name":           "widget",
		"goPackage":      "widget",
		"goPackageAlias": "v1Widget",
		"goTypeName":     "WidgetResource",
		"modelTypeName":  "WidgetResourceModel",
	}

	for field, got := range tests {
		if got != want[field] {
			t.Errorf("%s = %q, want %q", field, got, want[field])
		}
	}

	// camelCase JSON becomes snake_case Terraform and exported Go.
	a := attrByName(t, res, "label_ids")
	if a.GoField != "LabelIDs" {
		t.Errorf("goField = %q, want LabelIDs", a.GoField)
	}
	if a.Wire.JSONPath != "labelIds" {
		t.Errorf("jsonPath = %q, want the API's own spelling", a.Wire.JSONPath)
	}
}

func TestUnit_Infer_RefusesNonResources(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "api.yaml")
	if err := os.WriteFile(path, []byte(miniSpec), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// "usage" is read-only, so there is nothing for Terraform to manage.
	_, _, err = doc.Infer(find(t, doc.Discover(), "usage"), inferOptions())
	if err == nil {
		t.Fatal("expected a read-only candidate to be refused")
	}
	if !strings.Contains(err.Error(), "not a resource") {
		t.Errorf("error should say why: %v", err)
	}
}

func TestUnit_Infer_IsDeterministic(t *testing.T) {
	t.Parallel()

	first, _ := inferWidget(t)

	for i := range 10 {
		again, _ := inferWidget(t)
		if len(again.Schema.Attributes) != len(first.Schema.Attributes) {
			t.Fatalf("run %d produced %d attributes, first produced %d",
				i, len(again.Schema.Attributes), len(first.Schema.Attributes))
		}
		for j := range again.Schema.Attributes {
			if again.Schema.Attributes[j].Name != first.Schema.Attributes[j].Name {
				t.Fatalf("run %d differs at %d: %q vs %q",
					i, j, again.Schema.Attributes[j].Name, first.Schema.Attributes[j].Name)
			}
		}
	}
}

// TestUnit_Infer_AgainstTheCommittedSpecification checks the real document, and
// records where inference and the curated blueprint deliberately disagree.
func TestUnit_Infer_AgainstTheCommittedSpecification(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "..", "openapi", "thousandeyes",
		"7.0.97-t1785152261691", "api.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("the pinned snapshot is not present at %s", path)
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	res, notes, err := doc.Infer(find(t, doc.Discover(), "tag"), InferOptions{
		Provider:          "thousandeyes",
		SDKServiceRoot:    "github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/thousandeyes_api",
		SDKAccessorPrefix: "r.client.API",
		APIVersionDir:     "v7",
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	// The mechanical parts must match the curated blueprint exactly.
	if res.Name != "tag" {
		t.Errorf("name = %q", res.Name)
	}
	if res.Binding.Service.Accessor != "r.client.API.Tags" {
		t.Errorf("accessor = %q; the pinned SDK groups services under API", res.Binding.Service.Accessor)
	}
	if res.Binding.Body.RequestType != "tags.TagInfo" || res.Binding.Body.ResponseType != "tags.Tag" {
		t.Errorf("body models = %q / %q, want tags.TagInfo / tags.Tag",
			res.Binding.Body.RequestType, res.Binding.Body.ResponseType)
	}
	if res.Policy.UpdateStyle != blueprint.UpdatePutFull {
		t.Errorf("updateStyle = %q; the tag endpoint is PUT", res.Policy.UpdateStyle)
	}

	// legacyId is declared as an unformatted number and the SDK holds it as
	// *float64. Inferring types.Number would not compile against that.
	legacy := attrByName(t, res, "legacy_id")
	if legacy.Type.Kind != blueprint.KindFloat64 {
		t.Errorf("legacy_id kind = %q, want float64 to match the SDK's *float64", legacy.Type.Kind)
	}

	// Both nested collections are inferred, and their generated identifiers match the
	// curated blueprint exactly. That agreement is the point: it says the naming rule is
	// the one a person reached for by hand, so a curated blueprint and an inferred draft
	// differ only where judgement was applied.
	assertNestedIdentifiers(t, res, nestedIdentifiers{
		attr: "assignments", goType: "TagAssignmentModel", sdkType: "tags.Assignment",
		attrTypes: "tagAssignmentAttrTypes", objectType: "tagAssignmentObjectType",
		expand: "expandTagAssignments", flatten: "flattenTagAssignments",
	})
	// TagFilter already carries the resource's name, so the prefix is elided rather than
	// doubled into TagTagFilterModel.
	assertNestedIdentifiers(t, res, nestedIdentifiers{
		attr: "filters", goType: "TagFilterModel", sdkType: "tags.TagFilter",
		attrTypes: "tagFilterAttrTypes", objectType: "tagFilterObjectType",
		expand: "expandTagFilters", flatten: "flattenTagFilters",
	})

	// The only thing still reported is the hypermedia envelope.
	reported := strings.Join(noteStrings(notes), "\n")
	for _, field := range []string{"assignments", "filters"} {
		if strings.Contains(reported, field) {
			t.Errorf("%s is inferred and should not be reported:\n%s", field, reported)
		}
	}
}

// nestedIdentifiers is the five generated identifiers a nested object declares, plus the
// SDK type it binds to.
type nestedIdentifiers struct {
	attr, goType, sdkType, attrTypes, objectType, expand, flatten string
}

// assertNestedIdentifiers checks one inferred nested object against the curated blueprint.
//
// Extracted from the caller rather than inlined: the assertions are a flat table, and
// keeping them here is what stops the test that reads the real specification from growing
// a cyclomatic complexity the house linter refuses.
func assertNestedIdentifiers(t *testing.T, res blueprint.Resource, want nestedIdentifiers) {
	t.Helper()

	n := attrByName(t, res, want.attr).Type.NestedObject
	if n == nil {
		t.Errorf("%s should have become a nested attribute", want.attr)
		return
	}

	for _, got := range []struct{ field, have, want string }{
		{"goTypeName", n.GoTypeName, want.goType},
		{"sdkType", n.SDKType, want.sdkType},
		{"attrTypesVar", n.AttrTypesVar, want.attrTypes},
		{"objectTypeVar", n.ObjectTypeVar, want.objectType},
		{"expandFunc", n.ExpandFunc, want.expand},
		{"flattenFunc", n.FlattenFunc, want.flatten},
	} {
		if got.have != got.want {
			t.Errorf("%s.%s = %q, want %q", want.attr, got.field, got.have, got.want)
		}
	}
}

func noteStrings(notes []Caveat) []string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		out = append(out, n.String())
	}
	return out
}

func TestUnit_Infer_NoteString(t *testing.T) {
	t.Parallel()

	if got := (Caveat{Resource: "tag", Field: "colour", Message: "why"}).String(); got != "tag.colour: why" {
		t.Errorf("String = %q", got)
	}
	if got := (Caveat{Resource: "tag", Message: "why"}).String(); got != "tag: why" {
		t.Errorf("String = %q", got)
	}
}

// TestUnit_OpenAPI_AdoptsThePathParameterAsID covers the identifier adoption:
// an API that spells its identifier widgetId is still imported and refreshed
// through an attribute named id, whose wire binding keeps the API's spelling.
func TestUnit_OpenAPI_AdoptsThePathParameterAsID(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t, `
openapi: 3.0.3
info: {title: Widgets, version: "1"}
paths:
  /widgets:
    post:
      operationId: createWidget
      tags: [Widgets]
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
                  widgetId: {type: string, readOnly: true}
                  name: {type: string}
  /widgets/{widgetId}:
    get:
      operationId: getWidget
      tags: [Widgets]
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  widgetId: {type: string, readOnly: true}
                  name: {type: string}
    delete:
      operationId: deleteWidget
      tags: [Widgets]
      responses:
        '204': {description: gone}
`)

	res, notes, err := doc.Infer(find(t, doc.Discover(), "widget"), inferOptions())
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	var id *blueprint.Attribute
	for i := range res.Schema.Attributes {
		if res.Schema.Attributes[i].Name == "id" {
			id = &res.Schema.Attributes[i]
		}
		if res.Schema.Attributes[i].Name == "widget_id" {
			t.Errorf("the identifier attribute must be renamed, but widget_id survives")
		}
	}
	if id == nil {
		t.Fatalf("no id attribute was adopted; attributes: %v, notes: %v",
			res.Schema.Attributes, noteStrings(notes))
	}

	if id.GoField != "ID" {
		t.Errorf("id.goField = %q, want ID", id.GoField)
	}
	if id.Wire.JSONPath != "widgetId" {
		t.Errorf("id.wire.jsonPath = %q, want the API's spelling widgetId", id.Wire.JSONPath)
	}
	if res.Binding.ID.FromCreate != "created.WidgetID" {
		t.Errorf("binding.id.fromCreate = %q, want created.WidgetID", res.Binding.ID.FromCreate)
	}

	for _, n := range noteStrings(notes) {
		if strings.Contains(n, "cannot be imported") {
			t.Errorf("the adopted identifier must clear the no-id caveat, got: %s", n)
		}
	}
}

// TestUnit_OpenAPI_AdoptsThePathParameterAsIDKiota is the kiota spelling of the
// same adoption: the create read is an accessor call.
func TestUnit_OpenAPI_AdoptsThePathParameterAsIDKiota(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t, `
openapi: 3.0.3
info: {title: Widgets, version: "1"}
paths:
  /widgets:
    post:
      operationId: createWidget
      tags: [Widgets]
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
                  widgetId: {type: string, readOnly: true}
                  name: {type: string}
  /widgets/{widgetId}:
    get:
      operationId: getWidget
      tags: [Widgets]
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  widgetId: {type: string, readOnly: true}
                  name: {type: string}
    delete:
      operationId: deleteWidget
      tags: [Widgets]
      responses:
        '204': {description: gone}
`)

	opts := InferOptions{
		Provider:        "example",
		APIVersionDir:   "v1",
		SDKDialect:      blueprint.DialectKiotaFluent,
		SDKModelsImport: "example.com/sdk/models",
	}

	res, _, err := doc.Infer(find(t, doc.Discover(), "widget"), opts)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	if res.Binding.ID.FromCreate != "created.GetWidgetId()" {
		t.Errorf("binding.id.fromCreate = %q, want created.GetWidgetId()", res.Binding.ID.FromCreate)
	}
}

// TestUnit_OpenAPI_UUIDFormatMapsToStringer covers the kiota uuid shape: the
// SDK holds format: uuid as *uuid.UUID, and a string conversion against it
// would not compile.
func TestUnit_OpenAPI_UUIDFormatMapsToStringer(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t, `
openapi: 3.0.3
info: {title: Keys, version: "1"}
paths:
  /keys:
    post:
      operationId: createKey
      tags: [Keys]
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
                  id: {type: string, format: uuid, readOnly: true}
                  name: {type: string}
  /keys/{id}:
    get:
      operationId: getKey
      tags: [Keys]
      responses:
        '200':
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string, format: uuid, readOnly: true}
                  name: {type: string}
    delete:
      operationId: deleteKey
      tags: [Keys]
      responses:
        '204': {description: gone}
`)

	opts := InferOptions{
		Provider:        "example",
		APIVersionDir:   "v1",
		SDKDialect:      blueprint.DialectKiotaFluent,
		SDKModelsImport: "example.com/sdk/models",
	}

	res, _, err := doc.Infer(find(t, doc.Discover(), "key"), opts)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	for _, a := range res.Schema.Attributes {
		if a.Name != "id" {
			continue
		}
		if a.Wire.SDKGoType != "*uuid.UUID" {
			t.Errorf("id.wire.sdkGoType = %q, want *uuid.UUID", a.Wire.SDKGoType)
		}
		if a.Wire.Flatten == nil || a.Wire.Flatten.Func != "convert.PtrStringerToFramework" {
			t.Errorf("id.wire.flatten = %+v, want convert.PtrStringerToFramework", a.Wire.Flatten)
		}
		if !a.Wire.SkipExpand {
			t.Errorf("a computed uuid must not expand")
		}
		return
	}
	t.Fatalf("no id attribute inferred")
}
