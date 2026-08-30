package intermediate_representation

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

func TestUnit_Derive_RefusesMissingInputs(t *testing.T) {
	document := mustLoad(t, thingSpec)
	if _, err := Derive(nil, testConfig()); err == nil {
		t.Errorf("a nil document derived")
	}
	if _, err := Derive(document, nil); err == nil {
		t.Errorf("a nil config derived")
	}
	empty := testConfig()
	empty.Provider.Name = ""
	if _, err := Derive(document, empty); err == nil {
		t.Errorf("an empty provider name derived")
	}
}

func TestUnit_Derive_ModelShape(t *testing.T) {
	model := mustDerive(t, thingSpec, testConfig())

	if model.Provider.Name != "acme" {
		t.Errorf("provider = %q", model.Provider.Name)
	}
	if got := len(model.Resources); got != 1 {
		t.Fatalf("%d resources, want 1", got)
	}
	// thing's companion, the stream list+read entity, the lookup-only
	// setting entity, and events — which the API enumerates and cannot
	// address, so it is a datasource rather than a list resource.
	if got := len(model.Datasources); got != 4 {
		t.Fatalf("%d datasources, want 4", got)
	}
	// thing is the only entity that is both a resource and enumerable, so
	// it is the only one whose list capability terraform can match.
	if got := len(model.ListResources); got != 1 {
		t.Fatalf("%d list resources, want 1 (thing)", got)
	}
	if got := len(model.Actions); got != 1 {
		t.Fatalf("%d actions, want 1", got)
	}

	// The junk entity classifies as nothing and passes through with the
	// classifier's reason.
	found := false
	for _, excludedEntity := range model.ExcludedByClassification {
		if excludedEntity.Key == "junk" {
			found = true
			if !strings.Contains(excludedEntity.Reason, "partial lifecycle") {
				t.Errorf("junk excluded for %q", excludedEntity.Reason)
			}
		}
	}
	if !found {
		t.Errorf("the junk entity is not in ExcludedByClassification: %+v", model.ExcludedByClassification)
	}
}

func TestUnit_Derive_ResourceLifecycle(t *testing.T) {
	resource := resourceByKey(t, mustDerive(t, thingSpec, testConfig()), "thing")

	if resource.MissingUpdate {
		t.Errorf("MissingUpdate on an entity with PATCH")
	}
	if resource.UpdateStyle != UpdateStylePutFull {
		t.Errorf("UpdateStyle = %q, want the declared put-full", resource.UpdateStyle)
	}
	if resource.ReadAfterWriteDelay != 30*time.Second {
		t.Errorf("EventualConsistency = %v, want 30s", resource.ReadAfterWriteDelay)
	}
	if !resource.DeleteNotFoundOK {
		t.Errorf("DeleteNotFoundOK not carried from the delete operation")
	}
	if resource.Timeouts.Create == 0 || resource.Timeouts.Read == 0 || resource.Timeouts.Update == 0 || resource.Timeouts.Delete == 0 {
		t.Errorf("timeout defaults missing: %+v", resource.Timeouts)
	}

	create, deleteOperation := resource.Operations.Create, resource.Operations.Delete
	if create == nil || deleteOperation == nil || resource.Operations.Read == nil || resource.Operations.Update == nil || resource.Operations.List == nil {
		t.Fatalf("lifecycle ops missing: %+v", resource.Operations)
	}
	if create.Kind != OperationCreate || create.Method != "POST" || create.SuccessCode != 201 {
		t.Errorf("create op = %+v", create)
	}
	if create.OperationID != "createThing" {
		t.Errorf("create operation id = %q", create.OperationID)
	}
	if deleteOperation.PathTemplate != "/v7/things/{thingId}" || deleteOperation.SuccessCode != 204 {
		t.Errorf("delete op = %+v", deleteOperation)
	}
	want := []URLPathParameter{{Name: "thingId", Type: TypeString}}
	if !reflect.DeepEqual(deleteOperation.PathParameters, want) {
		t.Errorf("delete path parameters = %+v, want %+v", deleteOperation.PathParameters, want)
	}
}

func TestUnit_Derive_UpdateStyleDefaultsToPatchMerge(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /notes:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Note'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Note'}
  /notes/{noteId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Note'}
    put:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Note'}
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Note'}
    delete:
      responses:
        "204": {description: gone}
components:
  schemas:
    Note:
      type: object
      properties:
        text: {type: string}
`
	resource := resourceByKey(t, mustDerive(t, spec, testConfig()), "note")
	if resource.UpdateStyle != UpdateStylePatchMerge {
		t.Errorf("UpdateStyle = %q, want the patch-merge default", resource.UpdateStyle)
	}
	if resource.Names.APIVersionDirectory != "v1" {
		t.Errorf("APIVersionDirectory = %q, want the v1 default", resource.Names.APIVersionDirectory)
	}
	if resource.ReadAfterWriteDelay != 0 {
		t.Errorf("EventualConsistency = %v with none declared", resource.ReadAfterWriteDelay)
	}
	if resource.DeleteNotFoundOK {
		t.Errorf("DeleteNotFoundOK true with none declared")
	}
}

func TestUnit_Derive_MissingUpdateForcesReplacement(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /marks:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Mark'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Mark'}
  /marks/{markId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Mark'}
    delete:
      responses:
        "204": {description: gone}
components:
  schemas:
    Mark:
      type: object
      required: [label]
      properties:
        label: {type: string}
        color: {type: string}
        serial:
          type: string
          readOnly: true
`
	resource := resourceByKey(t, mustDerive(t, spec, testConfig()), "mark")
	if !resource.MissingUpdate {
		t.Fatalf("MissingUpdate not set on an entity with no update operation")
	}
	if resource.UpdateStyle != "" {
		t.Errorf("UpdateStyle = %q on a resource with no update", resource.UpdateStyle)
	}
	for _, name := range []string{"label", "color"} {
		if a := attribute(t, resource.Attributes, name); !a.RequiresReplace {
			t.Errorf("writable %q lacks RequiresReplace under MissingUpdate", name)
		}
	}
	for _, name := range []string{"serial", "id"} {
		if a := attribute(t, resource.Attributes, name); a.RequiresReplace {
			t.Errorf("computed %q carries RequiresReplace", name)
		}
	}
}

func TestUnit_Derive_CompanionDatasource(t *testing.T) {
	datasource := datasourceByKey(t, mustDerive(t, thingSpec, testConfig()), "thing")

	if datasource.LookupByKey {
		t.Fatalf("a resource companion marked LookupByKey")
	}
	if datasource.Operations.Read == nil || datasource.Operations.List == nil {
		t.Fatalf("companion ops incomplete: %+v", datasource.Operations)
	}

	// Every scalar at the root of a listed object is offered as a filter,
	// optional and typed as the field it selects on. A filter is how a
	// caller names the object it wants instead of counting to it.
	for _, name := range []string{"name", "quantity", "enabled"} {
		derived := attribute(t, datasource.Attributes, name)
		if !derived.IsDatasourceFilterArgument || derived.ComputedOptionalRequired != Optional {
			t.Errorf("%s = %+v, want an optional filter", name, derived)
		}
	}
	if got := attribute(t, datasource.Attributes, "quantity").Type; got != TypeInt64 {
		t.Errorf("a filter takes the type of the field it selects on, got %s", got)
	}
	// Nested fields are not filters: HCL would have to describe the whole
	// object to match one leaf of it.
	for _, candidate := range datasource.Attributes.Attributes {
		if candidate.IsDatasourceFilterArgument && candidate.NestedAttributes != nil {
			t.Errorf("nested attribute %q offered as a filter", candidate.Name)
		}
	}
	items := attribute(t, datasource.Attributes, "items")
	if items.Type != TypeList || items.ElementType != TypeObject || items.ComputedOptionalRequired != Computed {
		t.Errorf("items = %+v", items)
	}
	// Inside items everything is computed: the datasource never writes.
	for _, candidate := range items.NestedAttributes.Attributes {
		if candidate.ComputedOptionalRequired != Computed {
			t.Errorf("companion item attribute %q is %s, want computed", candidate.Name, candidate.ComputedOptionalRequired)
		}
	}
	if candidate := attribute(t, items.NestedAttributes, "name"); candidate.ComputedOptionalRequired != Computed {
		t.Errorf("name inside items = %+v", candidate)
	}
}

func TestUnit_Derive_LookupByKeyDatasource(t *testing.T) {
	datasource := datasourceByKey(t, mustDerive(t, thingSpec, testConfig()), "setting")

	if !datasource.LookupByKey {
		t.Fatalf("the read-only-by-id entity is not LookupByKey")
	}
	if datasource.KeyParameter != "settingName" {
		t.Errorf("KeyParameter = %q", datasource.KeyParameter)
	}
	if datasource.Operations.Read == nil || datasource.Operations.List != nil {
		t.Errorf("lookup ops = %+v", datasource.Operations)
	}
	key := attribute(t, datasource.Attributes, "setting_name")
	if key.ComputedOptionalRequired != Required || key.Type != TypeString || key.WireName != "settingName" {
		t.Errorf("key attribute = %+v", key)
	}
	if a := attribute(t, datasource.Attributes, "id"); a.ComputedOptionalRequired != Computed {
		t.Errorf("id = %+v", a)
	}
	if a := attribute(t, datasource.Attributes, "value"); a.ComputedOptionalRequired != Computed {
		t.Errorf("value = %+v", a)
	}
}

func TestUnit_Derive_ListReadEntityYieldsDatasource(t *testing.T) {
	model := mustDerive(t, thingSpec, testConfig())
	datasource := datasourceByKey(t, model, "stream")
	if datasource.LookupByKey {
		t.Errorf("a list+read entity marked LookupByKey")
	}
	if datasource.Operations.List == nil || datasource.Operations.Read == nil {
		t.Errorf("stream ops = %+v", datasource.Operations)
	}
	items := attribute(t, datasource.Attributes, "items")
	if a := attribute(t, items.NestedAttributes, "topic"); a.ComputedOptionalRequired != Computed {
		t.Errorf("topic = %+v", a)
	}
}

// keyedSpec is a full lifecycle whose objects are keyed by "widgetId"
// rather than by "id" — the ordinary case in an API that names its keys
// after the thing they identify.
// and none carries RequiresReplace because a list block has no plan.
func TestUnit_AddressingSchema_TakesEveryPathParameter(t *testing.T) {
	if tree := addressingAttributes(nil); tree != nil {
		t.Errorf("a path with no parameters declares no configuration, got %+v", tree)
	}

	tree := addressingAttributes([]URLPathParameter{
		{Name: "tenantId", Type: TypeString},
		{Name: "groupId", Type: TypeInt64},
	})
	if tree == nil || len(tree.Attributes) != 2 {
		t.Fatalf("addressingSchema = %+v", tree)
	}
	for index, want := range []Attribute{
		{Name: "tenant_id", WireName: "tenantId", Type: TypeString, ComputedOptionalRequired: Required},
		{Name: "group_id", WireName: "groupId", Type: TypeInt64, ComputedOptionalRequired: Required},
	} {
		got := tree.Attributes[index]
		if got.Name != want.Name || got.WireName != want.WireName || got.Type != want.Type ||
			got.ComputedOptionalRequired != want.ComputedOptionalRequired {
			t.Errorf("attribute %d = %+v, want %+v", index, got, want)
		}
		if got.RequiresReplace {
			t.Errorf("attribute %d carries RequiresReplace; a list block has no plan to modify", index)
		}
	}
}

func TestUnit_Derive_Action(t *testing.T) {
	model := mustDerive(t, thingSpec, testConfig())
	if len(model.Actions) != 1 {
		t.Fatalf("%d actions", len(model.Actions))
	}
	action := model.Actions[0]
	if action.Names.Key != "things_restart" {
		t.Errorf("action key = %q", action.Names.Key)
	}
	if action.ParentEntity != "thing" {
		t.Errorf("ParentEntity = %q, want the enclosing thing entity", action.ParentEntity)
	}
	if action.InvokeOperation.Kind != OperationAction || action.InvokeOperation.SuccessCode != 202 {
		t.Errorf("invoke op = %+v", action.InvokeOperation)
	}
	force := attribute(t, action.RequestAttributes, "force")
	if force.Type != TypeBool || force.ComputedOptionalRequired != Required {
		t.Errorf("force = %+v", force)
	}
}

func TestUnit_Derive_ActionWithoutBodyOrParent(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /pings:
    post:
      responses:
        "202": {description: accepted}
`
	model := mustDerive(t, spec, testConfig())
	if len(model.Actions) != 1 {
		t.Fatalf("%d actions", len(model.Actions))
	}
	action := model.Actions[0]
	if action.RequestAttributes != nil {
		t.Errorf("a bodiless action carries a request schema: %+v", action.RequestAttributes)
	}
	if action.ParentEntity != "" {
		t.Errorf("ParentEntity = %q with nothing enclosing", action.ParentEntity)
	}
}

func TestUnit_Derive_ConfigExcludesByService(t *testing.T) {
	model := mustDerive(t, thingSpec, testConfig("things"))

	if len(model.Resources) != 0 {
		t.Errorf("the things service still yields resources: %+v", model.Resources)
	}
	// The action lives under /v7/things too, so the service exclusion
	// takes it with the resource.
	if len(model.Actions) != 0 {
		t.Errorf("the things service still yields actions: %+v", model.Actions)
	}
	var reasons []string
	for _, excludedEntity := range model.ExcludedByConfiguration {
		if excludedEntity.Key == "thing" || excludedEntity.Key == "things_restart" {
			reasons = append(reasons, excludedEntity.Reason)
		}
	}
	if len(reasons) != 2 {
		t.Fatalf("expected both things entities excluded, got %+v", model.ExcludedByConfiguration)
	}
	for _, resource := range reasons {
		if resource != configurationExclusionReason {
			t.Errorf("exclusion reason = %q", resource)
		}
	}
	// Other services are untouched.
	datasourceByKey(t, model, "stream")
}

func TestUnit_Derive_ConfigExcludesByEntityKey(t *testing.T) {
	model := mustDerive(t, thingSpec, testConfig("event"))
	for _, listResource := range model.ListResources {
		if listResource.Names.Key == "event" {
			t.Errorf("the excluded event entity still derived")
		}
	}
	found := false
	for _, excludedEntity := range model.ExcludedByConfiguration {
		if excludedEntity.Key == "event" && excludedEntity.Reason == configurationExclusionReason {
			found = true
		}
	}
	if !found {
		t.Errorf("no configuration exclusion for event: %+v", model.ExcludedByConfiguration)
	}
}

func TestUnit_Derive_DisambiguatesEntityKeyCollisions(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /v6/tags:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    id: {type: string}
  /v7/tags:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    id: {type: string}
`
	model := mustDerive(t, spec, testConfig())
	// Both versions generate: the later collection path takes a key
	// extended by its distinguishing segment, and nothing is excluded.
	if got := len(model.Datasources); got != 2 {
		t.Fatalf("%d datasources from two colliding versions, want 2", got)
	}
	first, second := model.Datasources[0], model.Datasources[1]
	if first.Names.Key != "tag" || second.Names.Key != "tag_v7" {
		t.Fatalf("colliding keys = %q, %q; want tag, tag_v7", first.Names.Key, second.Names.Key)
	}
	if len(model.ExcludedByClassification)+len(model.ExcludedByConfiguration) != 0 {
		t.Errorf("a key collision produced exclusions: %+v %+v",
			model.ExcludedByClassification, model.ExcludedByConfiguration)
	}
	// Each family member's note names its sibling and says what
	// co-management costs.
	if !strings.Contains(first.CoManagementNote, "acme_tag_v7") {
		t.Errorf("first note does not name the sibling: %q", first.CoManagementNote)
	}
	if !strings.Contains(second.CoManagementNote, "acme_tag,") {
		t.Errorf("second note does not name the sibling: %q", second.CoManagementNote)
	}
	for _, note := range []string{first.CoManagementNote, second.CoManagementNote} {
		for _, want := range []string{"drift", "last terraform apply wins"} {
			if !strings.Contains(note, want) {
				t.Errorf("co-management note %q does not say %q", note, want)
			}
		}
	}
}

func TestUnit_Derive_DisambiguatesByEmbeddedParameter(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /tags/assign:
    post:
      responses:
        "202": {description: accepted}
  /tags/{id}/assign:
    post:
      responses:
        "202": {description: accepted}
`
	model := mustDerive(t, spec, testConfig())
	if got := len(model.Actions); got != 2 {
		t.Fatalf("%d actions from two colliding paths, want 2", got)
	}
	if model.Actions[0].Names.Key != "tags_assign" || model.Actions[1].Names.Key != "tags_assign_by_id" {
		t.Fatalf("action keys = %q, %q; want tags_assign, tags_assign_by_id",
			model.Actions[0].Names.Key, model.Actions[1].Names.Key)
	}
	for _, candidate := range model.Actions {
		if candidate.CoManagementNote == "" {
			t.Errorf("action %s lacks a co-management note", candidate.Names.Key)
		}
	}
	if len(model.ExcludedByClassification)+len(model.ExcludedByConfiguration) != 0 {
		t.Errorf("a key collision produced exclusions: %+v %+v",
			model.ExcludedByClassification, model.ExcludedByConfiguration)
	}
}

func TestUnit_Derive_CoManagedResources(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /v6/tags:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Tag'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Tag'}
  /v6/tags/{tagId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Tag'}
    delete:
      responses:
        "204": {description: gone}
  /v7/tags:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Tag'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Tag'}
  /v7/tags/{tagId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Tag'}
    delete:
      responses:
        "204": {description: gone}
components:
  schemas:
    Tag:
      type: object
      properties:
        name: {type: string}
`
	model := mustDerive(t, spec, testConfig())
	older := resourceByKey(t, model, "tag")
	newer := resourceByKey(t, model, "tag_v7")
	if !strings.Contains(older.CoManagementNote, "acme_tag_v7") {
		t.Errorf("older resource note does not name the sibling: %q", older.CoManagementNote)
	}
	if !strings.Contains(newer.CoManagementNote, "acme_tag,") {
		t.Errorf("newer resource note does not name the sibling: %q", newer.CoManagementNote)
	}
	// The companion datasources carry the same prose.
	for _, key := range []string{"tag", "tag_v7"} {
		if ds := datasourceByKey(t, model, key); ds.CoManagementNote == "" {
			t.Errorf("datasource %s lacks a co-management note", key)
		}
	}
	// An entity with no siblings carries no note.
	solo := resourceByKey(t, mustDerive(t, thingSpec, testConfig()), "thing")
	if solo.CoManagementNote != "" {
		t.Errorf("a collision-free resource carries a note: %q", solo.CoManagementNote)
	}
}

// The disambiguation helper's corners, called directly: renderings of
// literal and parameter segments, and the ordinal fallback for a still-
// taken or unchanged candidate.
func TestUnit_Derive_DisambiguateKey(t *testing.T) {
	if got := disambiguateKey("tag", "/v7/tags", "/v6/tags", map[string]string{}); got != "tag_v7" {
		t.Errorf("literal segment: got %q, want tag_v7", got)
	}
	if got := disambiguateKey("tags_assign", "/tags/{id}/assign", "/tags/assign", map[string]string{}); got != "tags_assign_by_id" {
		t.Errorf("parameter segment: got %q, want tags_assign_by_id", got)
	}
	if got := disambiguateKey("report", "/reports/by-user/{userId}", "/reports", map[string]string{}); got != "report_by_user_by_user_id" {
		t.Errorf("mixed segments: got %q, want report_by_user_by_user_id", got)
	}
	// No distinguishing segment: the later path's segments are all in the
	// winner's, so only the ordinal separates them.
	claimed := map[string]string{"tag": "/v6/tags", "tag_2": "/elsewhere"}
	if got := disambiguateKey("tag", "/tags", "/v6/tags", claimed); got != "tag_3" {
		t.Errorf("ordinal fallback: got %q, want tag_3", got)
	}
}

// Deriving twice from independently loaded documents must agree exactly —
// reflect.DeepEqual over the values and byte equality over the JSON, which
// is what catches a map iteration order leaking into the model.
func TestUnit_Derive_IsDeterministic(t *testing.T) {
	firstModel := mustDerive(t, thingSpec, testConfig())
	secondModel := mustDerive(t, thingSpec, testConfig())

	if !reflect.DeepEqual(firstModel, secondModel) {
		t.Fatalf("two derivations of the same document differ")
	}
	firstJSON, err := json.Marshal(firstModel)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	secondJSON, err := json.Marshal(secondModel)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("two derivations marshal differently:\n%s\n%s", firstJSON, secondJSON)
	}
}

// The model's slices arrive sorted by entity key whatever order the
// document declared the paths in.
func TestUnit_Derive_SlicesAreSortedByKey(t *testing.T) {
	model := mustDerive(t, thingSpec, testConfig())
	for index := 1; index < len(model.Datasources); index++ {
		if model.Datasources[index-1].Names.Key > model.Datasources[index].Names.Key {
			t.Errorf("datasources out of order: %q before %q",
				model.Datasources[index-1].Names.Key, model.Datasources[index].Names.Key)
		}
	}
	for _, excluded := range [][]UnsupportedEntity{model.ExcludedByConfiguration, model.ExcludedByClassification} {
		for index := 1; index < len(excluded); index++ {
			if excluded[index-1].Key > excluded[index].Key {
				t.Errorf("exclusions out of order: %q before %q",
					excluded[index-1].Key, excluded[index].Key)
			}
		}
	}
}

func TestUnit_ListElementSchema_UnwrapsAWrappingEnvelope(t *testing.T) {
	// A collection response that wraps its payload under a key must yield
	// the payload's element, not the envelope. Taking the envelope made the
	// element tree its own fields — results and totalCount — none of which
	// the SDK's element model carries, so every attribute pruned and the
	// entity was removed for having nothing left to map.
	element := &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "id", Schema: &specmodel.Schema{Type: "integer"}},
		{Name: "note", Schema: &specmodel.Schema{Type: "string"}},
	}}
	envelope := &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "totalCount", Schema: &specmodel.Schema{Type: "integer"}},
		{Name: "results", Schema: &specmodel.Schema{Type: "array", Items: element}},
	}}
	list := &specmodel.Operation{Responses: []specmodel.Response{
		{Status: "200", Schema: envelope},
	}}

	got := listElementSchema(list)
	if got != element {
		t.Fatalf("want the envelope's element, got %+v", got)
	}
}

func TestUnit_ListElementSchema_UnwrapsABareArray(t *testing.T) {
	element := &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "id", Schema: &specmodel.Schema{Type: "string"}},
	}}
	list := &specmodel.Operation{Responses: []specmodel.Response{
		{Status: "200", Schema: &specmodel.Schema{Type: "array", Items: element}},
	}}
	if got := listElementSchema(list); got != element {
		t.Fatalf("want the array's element, got %+v", got)
	}
}

func TestUnit_ListElementSchema_LeavesAResponseCarryingNoCollectionAlone(t *testing.T) {
	// A singleton settings object is not a collection. Guessing an element
	// out of it would be worse than deriving nothing, so it comes back
	// unchanged and the entity fails later with the SDK's own reason.
	singleton := &specmodel.Schema{Type: "object", Properties: []specmodel.Property{
		{Name: "timezone", Schema: &specmodel.Schema{Type: "string"}},
	}}
	list := &specmodel.Operation{Responses: []specmodel.Response{
		{Status: "200", Schema: singleton},
	}}
	if got := listElementSchema(list); got != singleton {
		t.Fatalf("want the body unchanged, got %+v", got)
	}
	if listElementSchema(nil) != nil {
		t.Fatal("no operation means no element")
	}
}

func TestUnit_Operation_CarriesTheQueryParametersItRequiresWithTheDocumentsValue(t *testing.T) {
	const document = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /vaults:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Vault'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Vault'}
  /vaults/{vaultId}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Vault'}
    delete:
      parameters:
        - name: confirmDisabledObjects
          in: query
          required: true
          schema: {type: boolean, default: false}
          example: true
        - name: reason
          in: query
          required: true
          schema: {type: string, example: retired}
        - name: dryRun
          in: query
          schema: {type: boolean}
        - name: unstated
          in: query
          required: true
          schema: {type: string}
      responses:
        "204": {description: gone}
components:
  schemas:
    Vault:
      type: object
      properties:
        id: {type: string, readOnly: true}
        name: {type: string}
`
	resource := resourceByKey(t, mustDerive(t, document, testConfig()), "vault")
	got := resource.Operations.Delete.QueryParameters
	want := []QueryParameter{
		// The parameter's own example outranks the schema's default; the
		// schema's example serves where the parameter states none; an
		// optional parameter and a required one with no stated value are
		// left out.
		{Name: "confirmDisabledObjects", Type: TypeBool, Value: true},
		{Name: "reason", Type: TypeString, Value: "retired"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("delete query parameters = %#v, want %#v", got, want)
	}
	if resource.Operations.Read.QueryParameters != nil {
		t.Errorf("the read carries query parameters it does not require: %#v", resource.Operations.Read.QueryParameters)
	}
}
