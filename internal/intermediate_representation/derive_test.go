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

func TestDerive_RefusesMissingInputs(t *testing.T) {
	doc := mustLoad(t, thingSpec)
	if _, err := Derive(nil, testConfig()); err == nil {
		t.Errorf("a nil document derived")
	}
	if _, err := Derive(doc, nil); err == nil {
		t.Errorf("a nil config derived")
	}
	empty := testConfig()
	empty.Provider.Name = ""
	if _, err := Derive(doc, empty); err == nil {
		t.Errorf("an empty provider name derived")
	}
}

func TestDerive_ModelShape(t *testing.T) {
	m := mustDerive(t, thingSpec, testConfig())

	if m.Provider.Name != "acme" {
		t.Errorf("provider = %q", m.Provider.Name)
	}
	if got := len(m.Resources); got != 1 {
		t.Fatalf("%d resources, want 1", got)
	}
	// thing's companion, the stream list+read entity, and the lookup-only
	// setting entity all yield datasources.
	if got := len(m.Datasources); got != 3 {
		t.Fatalf("%d datasources, want 3", got)
	}
	// Only events is list-only; streams has a read so it becomes a
	// datasource instead.
	if got := len(m.ListResources); got != 1 {
		t.Fatalf("%d list resources, want 1 (events)", got)
	}
	if got := len(m.Actions); got != 1 {
		t.Fatalf("%d actions, want 1", got)
	}

	// The junk entity classifies as nothing and passes through with the
	// classifier's reason.
	found := false
	for _, e := range m.Excluded {
		if e.Key == "junk" {
			found = true
			if !strings.Contains(e.Reason, "partial lifecycle") {
				t.Errorf("junk excluded for %q", e.Reason)
			}
		}
	}
	if !found {
		t.Errorf("the junk entity is not in Excluded: %+v", m.Excluded)
	}
}

func TestDerive_ResourceLifecycle(t *testing.T) {
	r := resourceByKey(t, mustDerive(t, thingSpec, testConfig()), "thing")

	if r.MissingUpdate {
		t.Errorf("MissingUpdate on an entity with PATCH")
	}
	if r.UpdateStyle != UpdateStylePutFull {
		t.Errorf("UpdateStyle = %q, want the declared put-full", r.UpdateStyle)
	}
	if r.EventualConsistency != 30*time.Second {
		t.Errorf("EventualConsistency = %v, want 30s", r.EventualConsistency)
	}
	if !r.DeleteNotFoundOK {
		t.Errorf("DeleteNotFoundOK not carried from the delete operation")
	}
	if r.Timeouts.Create == 0 || r.Timeouts.Read == 0 || r.Timeouts.Update == 0 || r.Timeouts.Delete == 0 {
		t.Errorf("timeout defaults missing: %+v", r.Timeouts)
	}

	create, deleteOperation := r.Operations.Create, r.Operations.Delete
	if create == nil || deleteOperation == nil || r.Operations.Read == nil || r.Operations.Update == nil || r.Operations.List == nil {
		t.Fatalf("lifecycle ops missing: %+v", r.Operations)
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
	want := []Parameter{{Name: "thingId", Type: TypeString}}
	if !reflect.DeepEqual(deleteOperation.PathParameters, want) {
		t.Errorf("delete path params = %+v, want %+v", deleteOperation.PathParameters, want)
	}
}

func TestDerive_UpdateStyleDefaultsToPatchMerge(t *testing.T) {
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
	r := resourceByKey(t, mustDerive(t, spec, testConfig()), "note")
	if r.UpdateStyle != UpdateStylePatchMerge {
		t.Errorf("UpdateStyle = %q, want the patch-merge default", r.UpdateStyle)
	}
	if r.Names.APIVersionDirectory != "v1" {
		t.Errorf("APIVersionDirectory = %q, want the v1 default", r.Names.APIVersionDirectory)
	}
	if r.EventualConsistency != 0 {
		t.Errorf("EventualConsistency = %v with none declared", r.EventualConsistency)
	}
	if r.DeleteNotFoundOK {
		t.Errorf("DeleteNotFoundOK true with none declared")
	}
}

func TestDerive_MissingUpdateForcesReplacement(t *testing.T) {
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
	r := resourceByKey(t, mustDerive(t, spec, testConfig()), "mark")
	if !r.MissingUpdate {
		t.Fatalf("MissingUpdate not set on an entity with no update operation")
	}
	if r.UpdateStyle != "" {
		t.Errorf("UpdateStyle = %q on a resource with no update", r.UpdateStyle)
	}
	for _, name := range []string{"label", "color"} {
		if a := attribute(t, r.Schema, name); !a.RequiresReplace {
			t.Errorf("writable %q lacks RequiresReplace under MissingUpdate", name)
		}
	}
	for _, name := range []string{"serial", "id"} {
		if a := attribute(t, r.Schema, name); a.RequiresReplace {
			t.Errorf("computed %q carries RequiresReplace", name)
		}
	}
}

func TestDerive_CompanionDatasource(t *testing.T) {
	ds := datasourceByKey(t, mustDerive(t, thingSpec, testConfig()), "thing")

	if ds.LookupByKey {
		t.Fatalf("a resource companion marked LookupByKey")
	}
	if ds.Operations.Read == nil || ds.Operations.List == nil {
		t.Fatalf("companion ops incomplete: %+v", ds.Operations)
	}

	ft := attribute(t, ds.Schema, "filter_type")
	if ft.Kind != TypeString || ft.ComputedOptionalRequired != Required {
		t.Errorf("filter_type = %+v", ft)
	}
	fv := attribute(t, ds.Schema, "filter_value")
	if fv.ComputedOptionalRequired != Optional {
		t.Errorf("filter_value = %+v", fv)
	}
	items := attribute(t, ds.Schema, "items")
	if items.Kind != TypeList || items.ElementType != TypeObject || items.ComputedOptionalRequired != Computed {
		t.Errorf("items = %+v", items)
	}
	// Inside items everything is computed: the datasource never writes.
	for _, a := range items.Nested.Attributes {
		if a.ComputedOptionalRequired != Computed {
			t.Errorf("companion item attribute %q is %s, want computed", a.Name, a.ComputedOptionalRequired)
		}
	}
	if a := attribute(t, items.Nested, "name"); a.ComputedOptionalRequired != Computed {
		t.Errorf("name inside items = %+v", a)
	}
}

func TestDerive_LookupByKeyDatasource(t *testing.T) {
	ds := datasourceByKey(t, mustDerive(t, thingSpec, testConfig()), "setting")

	if !ds.LookupByKey {
		t.Fatalf("the read-only-by-id entity is not LookupByKey")
	}
	if ds.KeyParameter != "settingName" {
		t.Errorf("KeyParameter = %q", ds.KeyParameter)
	}
	if ds.Operations.Read == nil || ds.Operations.List != nil {
		t.Errorf("lookup ops = %+v", ds.Operations)
	}
	key := attribute(t, ds.Schema, "setting_name")
	if key.ComputedOptionalRequired != Required || key.Kind != TypeString || key.WireName != "settingName" {
		t.Errorf("key attribute = %+v", key)
	}
	if a := attribute(t, ds.Schema, "id"); a.ComputedOptionalRequired != Computed {
		t.Errorf("id = %+v", a)
	}
	if a := attribute(t, ds.Schema, "value"); a.ComputedOptionalRequired != Computed {
		t.Errorf("value = %+v", a)
	}
}

func TestDerive_ListReadEntityYieldsDatasource(t *testing.T) {
	m := mustDerive(t, thingSpec, testConfig())
	ds := datasourceByKey(t, m, "stream")
	if ds.LookupByKey {
		t.Errorf("a list+read entity marked LookupByKey")
	}
	if ds.Operations.List == nil || ds.Operations.Read == nil {
		t.Errorf("stream ops = %+v", ds.Operations)
	}
	items := attribute(t, ds.Schema, "items")
	if a := attribute(t, items.Nested, "topic"); a.ComputedOptionalRequired != Computed {
		t.Errorf("topic = %+v", a)
	}
}

func TestDerive_ListResource(t *testing.T) {
	m := mustDerive(t, thingSpec, testConfig())
	for _, lr := range m.ListResources {
		if lr.Names.Key != "event" {
			continue
		}
		if lr.ListOperation.Kind != OperationList || lr.ListOperation.Method != "GET" || lr.ListOperation.SuccessCode != 200 {
			t.Errorf("list op = %+v", lr.ListOperation)
		}
		for _, name := range []string{"at", "level"} {
			if a := attribute(t, lr.Schema, name); a.ComputedOptionalRequired != Computed {
				t.Errorf("%q = %+v", name, a)
			}
		}
		return
	}
	t.Fatalf("no event list resource in %+v", m.ListResources)
}

// TestUnit_AddressingSchema_TakesEveryPathParameter proves a collection
// path's parameters all become required attributes of the list block: a
// collection path carries no item key, so none of them is absorbed by an id,
// and none carries RequiresReplace because a list block has no plan.
func TestUnit_AddressingSchema_TakesEveryPathParameter(t *testing.T) {
	if tree := addressingSchema(nil); tree != nil {
		t.Errorf("a path with no parameters declares no configuration, got %+v", tree)
	}

	tree := addressingSchema([]Parameter{
		{Name: "tenantId", Type: TypeString},
		{Name: "groupId", Type: TypeInt64},
	})
	if tree == nil || len(tree.Attributes) != 2 {
		t.Fatalf("addressingSchema = %+v", tree)
	}
	for index, want := range []Attribute{
		{Name: "tenant_id", WireName: "tenantId", Kind: TypeString, ComputedOptionalRequired: Required},
		{Name: "group_id", WireName: "groupId", Kind: TypeInt64, ComputedOptionalRequired: Required},
	} {
		got := tree.Attributes[index]
		if got.Name != want.Name || got.WireName != want.WireName || got.Kind != want.Kind ||
			got.ComputedOptionalRequired != want.ComputedOptionalRequired {
			t.Errorf("attribute %d = %+v, want %+v", index, got, want)
		}
		if got.RequiresReplace {
			t.Errorf("attribute %d carries RequiresReplace; a list block has no plan to modify", index)
		}
	}
}

func TestDerive_Action(t *testing.T) {
	m := mustDerive(t, thingSpec, testConfig())
	if len(m.Actions) != 1 {
		t.Fatalf("%d actions", len(m.Actions))
	}
	a := m.Actions[0]
	if a.Names.Key != "things_restart" {
		t.Errorf("action key = %q", a.Names.Key)
	}
	if a.ParentEntity != "thing" {
		t.Errorf("ParentEntity = %q, want the enclosing thing entity", a.ParentEntity)
	}
	if a.InvokeOperation.Kind != OperationInvoke || a.InvokeOperation.SuccessCode != 202 {
		t.Errorf("invoke op = %+v", a.InvokeOperation)
	}
	force := attribute(t, a.RequestSchema, "force")
	if force.Kind != TypeBool || force.ComputedOptionalRequired != Required {
		t.Errorf("force = %+v", force)
	}
}

func TestDerive_ActionWithoutBodyOrParent(t *testing.T) {
	const spec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /pings:
    post:
      responses:
        "202": {description: accepted}
`
	m := mustDerive(t, spec, testConfig())
	if len(m.Actions) != 1 {
		t.Fatalf("%d actions", len(m.Actions))
	}
	a := m.Actions[0]
	if a.RequestSchema != nil {
		t.Errorf("a bodiless action carries a request schema: %+v", a.RequestSchema)
	}
	if a.ParentEntity != "" {
		t.Errorf("ParentEntity = %q with nothing enclosing", a.ParentEntity)
	}
}

func TestDerive_ConfigExcludesByService(t *testing.T) {
	m := mustDerive(t, thingSpec, testConfig("things"))

	if len(m.Resources) != 0 {
		t.Errorf("the things service still yields resources: %+v", m.Resources)
	}
	// The action lives under /v7/things too, so the service exclusion
	// takes it with the resource.
	if len(m.Actions) != 0 {
		t.Errorf("the things service still yields actions: %+v", m.Actions)
	}
	var reasons []string
	for _, e := range m.Excluded {
		if e.Key == "thing" || e.Key == "things_restart" {
			reasons = append(reasons, e.Reason)
		}
	}
	if len(reasons) != 2 {
		t.Fatalf("expected both things entities excluded, got %+v", m.Excluded)
	}
	for _, r := range reasons {
		if r != configExcludedReason {
			t.Errorf("exclusion reason = %q", r)
		}
	}
	// Other services are untouched.
	datasourceByKey(t, m, "stream")
}

func TestDerive_ConfigExcludesByEntityKey(t *testing.T) {
	m := mustDerive(t, thingSpec, testConfig("event"))
	for _, lr := range m.ListResources {
		if lr.Names.Key == "event" {
			t.Errorf("the excluded event entity still derived")
		}
	}
	found := false
	for _, e := range m.Excluded {
		if e.Key == "event" && e.Reason == configExcludedReason {
			found = true
		}
	}
	if !found {
		t.Errorf("no configuration exclusion for event: %+v", m.Excluded)
	}
}

func TestDerive_DisambiguatesEntityKeyCollisions(t *testing.T) {
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
	m := mustDerive(t, spec, testConfig())
	// Both versions generate: the later collection path takes a key
	// extended by its distinguishing segment, and nothing is excluded.
	if got := len(m.ListResources); got != 2 {
		t.Fatalf("%d list resources from two colliding versions, want 2", got)
	}
	first, second := m.ListResources[0], m.ListResources[1]
	if first.Names.Key != "tag" || second.Names.Key != "tag_v7" {
		t.Fatalf("colliding keys = %q, %q; want tag, tag_v7", first.Names.Key, second.Names.Key)
	}
	if len(m.Excluded) != 0 {
		t.Errorf("a key collision produced exclusions: %+v", m.Excluded)
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

func TestDerive_DisambiguatesByEmbeddedParameter(t *testing.T) {
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
	m := mustDerive(t, spec, testConfig())
	if got := len(m.Actions); got != 2 {
		t.Fatalf("%d actions from two colliding paths, want 2", got)
	}
	if m.Actions[0].Names.Key != "tags_assign" || m.Actions[1].Names.Key != "tags_assign_by_id" {
		t.Fatalf("action keys = %q, %q; want tags_assign, tags_assign_by_id",
			m.Actions[0].Names.Key, m.Actions[1].Names.Key)
	}
	for _, a := range m.Actions {
		if a.CoManagementNote == "" {
			t.Errorf("action %s lacks a co-management note", a.Names.Key)
		}
	}
	if len(m.Excluded) != 0 {
		t.Errorf("a key collision produced exclusions: %+v", m.Excluded)
	}
}

func TestDerive_CoManagedResources(t *testing.T) {
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
	m := mustDerive(t, spec, testConfig())
	older := resourceByKey(t, m, "tag")
	newer := resourceByKey(t, m, "tag_v7")
	if !strings.Contains(older.CoManagementNote, "acme_tag_v7") {
		t.Errorf("older resource note does not name the sibling: %q", older.CoManagementNote)
	}
	if !strings.Contains(newer.CoManagementNote, "acme_tag,") {
		t.Errorf("newer resource note does not name the sibling: %q", newer.CoManagementNote)
	}
	// The companion datasources carry the same prose.
	for _, key := range []string{"tag", "tag_v7"} {
		if ds := datasourceByKey(t, m, key); ds.CoManagementNote == "" {
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
func TestDisambiguateKey(t *testing.T) {
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
func TestDerive_IsDeterministic(t *testing.T) {
	a := mustDerive(t, thingSpec, testConfig())
	b := mustDerive(t, thingSpec, testConfig())

	if !reflect.DeepEqual(a, b) {
		t.Fatalf("two derivations of the same document differ")
	}
	aj, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bj, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(aj, bj) {
		t.Fatalf("two derivations marshal differently:\n%s\n%s", aj, bj)
	}
}

// The model's slices arrive sorted by entity key whatever order the
// document declared the paths in.
func TestDerive_SlicesAreSortedByKey(t *testing.T) {
	m := mustDerive(t, thingSpec, testConfig())
	for i := 1; i < len(m.Datasources); i++ {
		if m.Datasources[i-1].Names.Key > m.Datasources[i].Names.Key {
			t.Errorf("datasources out of order: %q before %q",
				m.Datasources[i-1].Names.Key, m.Datasources[i].Names.Key)
		}
	}
	for i := 1; i < len(m.Excluded); i++ {
		if m.Excluded[i-1].Key > m.Excluded[i].Key {
			t.Errorf("exclusions out of order: %q before %q",
				m.Excluded[i-1].Key, m.Excluded[i].Key)
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
