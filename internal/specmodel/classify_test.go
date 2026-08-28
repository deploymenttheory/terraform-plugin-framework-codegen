package specmodel

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// specWith wraps path fragments into a valid document with one Thing
// schema, so each classification case states only the operation shape it
// is about.
func specWith(paths string) string {
	return `openapi: 3.0.3
info: {title: C, version: "1"}
paths:
` + paths + `components:
  schemas:
    Thing:
      type: object
      properties:
        id: {type: string}
`
}

// Fragment builders: each is one operation with or without a schema, so a
// case reads as the list of what the API offers.
const (
	withBody = `      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Thing'
`
	withResponse = `      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
`
	withCollectionResponse = `      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Thing'
`
	noContent = `      responses:
        "204":
          description: no content
`
)

// crud assembles a path block. Methods are given as "post!", where the
// bang suffix means "with schema" (request body for post/put/patch,
// response for get) and its absence means schemaless.
func crud(path string, methods ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s:\n", path)
	for _, m := range methods {
		withSchema := strings.HasSuffix(m, "!")
		m = strings.TrimSuffix(m, "!")
		fmt.Fprintf(&b, "    %s:\n", m)
		fmt.Fprintf(&b, "      operationId: %s_%s\n", m, pathID(path))
		switch m {
		case "get":
			switch {
			case !withSchema:
				b.WriteString(noContent)
			case collectionPath(path):
				// A GET on a collection path answers with a collection. The
				// distinction is load-bearing: a collection-path GET whose
				// response is one object is a singleton, not a list.
				b.WriteString(withCollectionResponse)
			default:
				b.WriteString(withResponse)
			}
		case "post", "put", "patch":
			if withSchema {
				b.WriteString(withBody)
			}
			b.WriteString(withResponse)
		default:
			b.WriteString(noContent)
		}
	}
	return b.String()
}

// collectionPath reports whether a path addresses a collection rather than
// one member of it: no identifier segment at the end.
func collectionPath(path string) bool {
	return !strings.HasSuffix(strings.TrimSuffix(path, "/"), "}")
}

func pathID(path string) string {
	clean := strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(strings.Trim(path, "/"))
	return clean
}

func kinds(c Classification) string {
	var out []string
	for _, k := range c.Kinds {
		out = append(out, string(k))
	}
	return strings.Join(out, ",")
}

func TestUnit_Specmodel_ClassifyTable(t *testing.T) {
	cases := []struct {
		name  string
		paths string
		// wantKinds maps entity key -> comma-joined kinds; entities not
		// listed must not classify.
		wantKinds map[string]string
		// wantExcluded maps entity key -> a substring of its reason.
		wantExcluded  map[string]string
		missingUpdate map[string]bool
		lookupByKey   map[string]bool
	}{
		{
			name:          "a full lifecycle the api can enumerate is a resource, a datasource and a list resource",
			paths:         crud("/tags", "get!", "post!") + crud("/tags/{tagId}", "get!", "patch!", "delete"),
			wantKinds:     map[string]string{"tag": "resource,datasource,list-resource"},
			missingUpdate: map[string]bool{"tag": false},
		},
		{
			name:          "missing update stays a resource, flagged",
			paths:         crud("/tags", "post!") + crud("/tags/{tagId}", "get!", "delete"),
			wantKinds:     map[string]string{"tag": "resource,datasource"},
			missingUpdate: map[string]bool{"tag": true},
			// A resource's by-id datasource is its normal companion,
			// even with no list operation — not a key lookup.
			lookupByKey: map[string]bool{"tag": false},
		},
		{
			name:        "create and read without delete is not a resource but lists as a datasource",
			paths:       crud("/tags", "get!", "post!") + crud("/tags/{tagId}", "get!"),
			wantKinds:   map[string]string{"tag": "datasource"},
			lookupByKey: map[string]bool{"tag": false},
		},
		{
			name:        "create and read without delete or list is a datasource looked up by key",
			paths:       crud("/tags", "post!") + crud("/tags/{tagId}", "get!"),
			wantKinds:   map[string]string{"tag": "datasource"},
			lookupByKey: map[string]bool{"tag": true},
		},
		{
			name:        "list plus item get is a datasource",
			paths:       crud("/regions", "get!") + crud("/regions/{regionId}", "get!"),
			wantKinds:   map[string]string{"region": "datasource"},
			lookupByKey: map[string]bool{"region": false},
		},
		{
			name:      "a collection with no item get is a datasource, because no resource can match a list resource",
			paths:     crud("/metrics", "get!"),
			wantKinds: map[string]string{"metric": "datasource"},
		},
		{
			name:        "item get without a list is a datasource looked up by the item path key",
			paths:       crud("/settings/{settingId}", "get!"),
			wantKinds:   map[string]string{"setting": "datasource"},
			lookupByKey: map[string]bool{"setting": true},
		},
		{
			name:         "item get without a list or a response schema is excluded",
			paths:        crud("/settings/{settingId}", "get"),
			wantExcluded: map[string]string{"setting": "readable by id but the read success response declares no schema"},
		},
		{
			name:      "a post under an item path is an action",
			paths:     crud("/agents/{agentId}/restart", "post!"),
			wantKinds: map[string]string{"agents_restart": "action"},
		},
		{
			name:      "a bare post-only collection is an action",
			paths:     crud("/notifications", "post!"),
			wantKinds: map[string]string{"notification": "action"},
		},
		{
			name:         "a resource shape without schemas is excluded",
			paths:        crud("/ghosts", "post") + crud("/ghosts/{ghostId}", "get", "delete"),
			wantExcluded: map[string]string{"ghost": "declares no schema"},
		},
		{
			name:         "a list without a response schema is excluded",
			paths:        crud("/blips", "get"),
			wantExcluded: map[string]string{"blip": "success response declares no schema"},
		},
		{
			// A list resource is matched to a resource by type name, so it
			// cannot exist without one. An entity the API enumerates but
			// cannot create is a datasource and nothing more.
			name:      "a collection the api cannot create yields no list resource",
			paths:     crud("/motes", "get!") + crud("/motes/{moteId}", "get!"),
			wantKinds: map[string]string{"mote": "datasource"},
		},
		{
			name:      "a resource the api cannot enumerate yields no list resource",
			paths:     crud("/specks", "post!") + crud("/specks/{speckId}", "get!", "delete"),
			wantKinds: map[string]string{"speck": "resource,datasource"},
		},
		{
			name:         "list and read without response schemas are excluded",
			paths:        crud("/dims", "get") + crud("/dims/{dimId}", "get"),
			wantExcluded: map[string]string{"dim": "success response schema is missing"},
		},
		{
			// Nothing reads it back, lists it or removes it, so there is no
			// object for terraform to own — only a call to make.
			name:      "a lone item patch is an action",
			paths:     crud("/knobs/{knobId}", "patch!"),
			wantKinds: map[string]string{"knob": "action"},
		},
		{
			name:      "a lone collection put is an action",
			paths:     crud("/odd", "put!"),
			wantKinds: map[string]string{"odd": "action"},
		},
		{
			// Two candidate invocations and no way to choose between them.
			name:         "a write in both positions and nothing else is excluded",
			paths:        crud("/dials", "put!") + crud("/dials/{dialId}", "patch!"),
			wantExcluded: map[string]string{"dial": "partial lifecycle (update) fits no kind"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Load([]byte(specWith(tc.paths)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			got := Classify(doc)

			classified := map[string]Classification{}
			for _, c := range got.Entities {
				classified[c.Key] = c
			}
			excluded := map[string]Exclusion{}
			for _, e := range got.Excluded {
				excluded[e.Key] = e
			}

			for key, want := range tc.wantKinds {
				c, ok := classified[key]
				if !ok {
					t.Fatalf("entity %q did not classify; excluded = %+v", key, got.Excluded)
				}
				if kinds(c) != want {
					t.Errorf("entity %q kinds = %s, want %s", key, kinds(c), want)
				}
			}
			for key, want := range tc.missingUpdate {
				if c := classified[key]; c.MissingUpdate != want {
					t.Errorf("entity %q MissingUpdate = %v, want %v", key, c.MissingUpdate, want)
				}
			}
			for key, want := range tc.lookupByKey {
				if c := classified[key]; c.LookupByKey != want {
					t.Errorf("entity %q LookupByKey = %v, want %v", key, c.LookupByKey, want)
				}
			}
			for key, want := range tc.wantExcluded {
				e, ok := excluded[key]
				if !ok {
					t.Fatalf("entity %q was not excluded; entities = %+v", key, got.Entities)
				}
				if !strings.Contains(e.Reason, want) {
					t.Errorf("entity %q reason = %q, want it to contain %q", key, e.Reason, want)
				}
			}
			if len(tc.wantKinds) != len(got.Entities) {
				t.Errorf("classified %d entities, want %d: %+v", len(got.Entities), len(tc.wantKinds), got.Entities)
			}
			if len(tc.wantExcluded) != len(got.Excluded) {
				t.Errorf("excluded %d entities, want %d: %+v", len(got.Excluded), len(tc.wantExcluded), got.Excluded)
			}
		})
	}
}

func TestUnit_Specmodel_ClassificationCarriesTheOperations(t *testing.T) {
	doc, err := Load([]byte(specWith(
		crud("/tags", "get!", "post!") + crud("/tags/{tagId}", "get!", "patch!", "delete"))))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Classify(doc)
	if len(got.Entities) != 1 {
		t.Fatalf("entities = %+v", got.Entities)
	}
	c := got.Entities[0]

	if c.CollectionPath != "/tags" || c.ItemPath != "/tags/{tagId}" {
		t.Errorf("paths = %q, %q", c.CollectionPath, c.ItemPath)
	}
	want := map[string]*OperationReference{
		"create": {Method: "POST", Path: "/tags", OperationID: "post_tags"},
		"read":   {Method: "GET", Path: "/tags/{tagId}", OperationID: "get_tags_tagId"},
		"update": {Method: "PATCH", Path: "/tags/{tagId}", OperationID: "patch_tags_tagId"},
		"delete": {Method: "DELETE", Path: "/tags/{tagId}", OperationID: "delete_tags_tagId"},
		"list":   {Method: "GET", Path: "/tags", OperationID: "get_tags"},
	}
	for role, wantOp := range want {
		var gotOp *OperationReference
		switch role {
		case "create":
			gotOp = c.Create
		case "read":
			gotOp = c.Read
		case "update":
			gotOp = c.Update
		case "delete":
			gotOp = c.Delete
		case "list":
			gotOp = c.List
		}
		if gotOp == nil || *gotOp != *wantOp {
			t.Errorf("%s = %+v, want %+v", role, gotOp, wantOp)
		}
	}
}

// A second claimant for a role, and a method in no role's position, land in
// Extra instead of being dropped or silently overriding.
func TestUnit_Specmodel_SurplusOperationsLandInExtra(t *testing.T) {
	paths := crud("/tags", "get!", "post!") + crud("/tags/{tagId}", "get!", "patch!", "delete") +
		`  /tags/bulk:
    post:
      operationId: bulkCreate
` + withBody + withResponse

	doc, err := Load([]byte(specWith(paths + `  /tags/{tagId}/clone:
    post:
      operationId: cloneTag
` + withBody + withResponse)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Classify(doc)

	// /tags/bulk and /tags/{tagId}/clone are their own collections by the
	// path convention, and both are post-only, so both come out actions.
	byKey := map[string]Classification{}
	for _, c := range got.Entities {
		byKey[c.Key] = c
	}
	if _, ok := byKey["tags_bulk"]; !ok {
		t.Errorf("tags_bulk missing: %+v", got.Entities)
	}
	if _, ok := byKey["tags_clone"]; !ok {
		t.Errorf("tags_clone missing: %+v", got.Entities)
	}

	// Two genuine surplus operations: a PUT on the collection fits no
	// role, and a second item path's GET loses the read slot to the
	// first. Both land in Extra, sorted by path then method.
	doc2, err := Load([]byte(specWith(
		crud("/tags", "get!", "post!", "put!") +
			crud("/tags/{tagId}", "get!", "patch!", "delete") +
			crud("/tags/{tagName}", "get!"))))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got2 := Classify(doc2)
	if len(got2.Entities) != 1 {
		t.Fatalf("entities = %+v", got2.Entities)
	}
	extra := got2.Entities[0].Extra
	if len(extra) != 2 ||
		extra[0].Method != "PUT" || extra[0].Path != "/tags" ||
		extra[1].Method != "GET" || extra[1].Path != "/tags/{tagName}" {
		t.Errorf("Extra = %+v", extra)
	}
}

// A path that is nothing but a parameter is its own collection: there is no
// prefix to pair it with, so it gets the fallback key.
func TestUnit_Specmodel_ABareParameterPathIsItsOwnEntity(t *testing.T) {
	doc, err := Load([]byte(specWith(`  /{id}:
    get:
      operationId: get_id
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Thing'
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Classify(doc)
	if len(got.Entities) != 1 || got.Entities[0].Key != "root" ||
		kinds(got.Entities[0]) != "datasource" {
		t.Fatalf("entities = %+v, excluded = %+v", got.Entities, got.Excluded)
	}
}

func TestUnit_Specmodel_ClassificationIsDeterministic(t *testing.T) {
	paths := crud("/zebras", "get!") + crud("/apples", "get!") + crud("/bears", "get!")
	doc, err := Load([]byte(specWith(paths)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var keys []string
	for _, c := range Classify(doc).Entities {
		keys = append(keys, c.Key)
	}
	if want := []string{"apple", "bear", "zebra"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v (sorted by collection path)", keys, want)
	}
}

// A "default" response schema counts as a success schema when no 2xx
// declares one.
func TestUnit_Specmodel_DefaultResponseBacksSuccess(t *testing.T) {
	doc, err := Load([]byte(specWith(`  /flags:
    get:
      operationId: listFlags
      responses:
        default:
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Thing'
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Classify(doc)
	if len(got.Entities) != 1 || kinds(got.Entities[0]) != "datasource" {
		t.Fatalf("entities = %+v, excluded = %+v", got.Entities, got.Excluded)
	}
}

func TestUnit_Specmodel_KeyDerivation(t *testing.T) {
	cases := []struct{ path, want string }{
		{"/tags", "tag"},
		{"/categories", "category"},
		{"/branches", "branch"},
		{"/addresses", "address"},
		{"/dishes", "dish"},
		{"/status", "status"},
		{"/analysis", "analysis"},
		{"/v7/tests/http-server", "v7_tests_http_server"},
		{"/agents/{agentId}/restart", "agents_restart"},
		{"/", "root"},
	}
	for _, tc := range cases {
		if got := keyForPath(tc.path); got != tc.want {
			t.Errorf("keyForPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// A collection-path GET whose response is one object is not a collection: it
// is a singleton, and a writable one is a resource with no create and no
// delete.
func TestUnit_Specmodel_AWritableSingletonIsAResource(t *testing.T) {
	doc, err := Load([]byte(specWith(`  /account-preferences:
    get:
      operationId: getPreferences
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
    patch:
      operationId: updatePreferences
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Thing'
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := Classify(doc)
	if len(got.Entities) != 1 {
		t.Fatalf("entities = %+v, excluded = %+v", got.Entities, got.Excluded)
	}
	c := got.Entities[0]
	if !c.Singleton {
		t.Fatalf("the entity must be a singleton: %+v", c)
	}
	if kinds(c) != "resource" {
		t.Fatalf("kinds = %q, want resource", kinds(c))
	}
	if c.Read == nil || c.Read.Method != "GET" {
		t.Fatalf("a singleton reads through its collection GET, got %+v", c.Read)
	}
	if c.Update == nil || c.Update.Method != "PATCH" {
		t.Fatalf("a singleton updates through its collection write, got %+v", c.Update)
	}
	if c.Create != nil || c.Delete != nil || c.List != nil {
		t.Fatalf("a singleton has no create, delete or list: %+v", c)
	}
}

// An action's operation sits in the create slot whichever method the
// document spelled it with, so a consumer finds it in one place.
func TestUnit_Specmodel_ALoneWriteInvokesFromTheCreateSlot(t *testing.T) {
	for _, tc := range []struct {
		name, paths, wantMethod, wantKey string
	}{
		{"collection put", crud("/rotate-keys", "put!"), "PUT", "rotate_key"},
		{"collection patch", crud("/reindex", "patch!"), "PATCH", "reindex"},
		{"item put", crud("/knobs/{knobId}", "put!"), "PUT", "knob"},
		{"item patch", crud("/knobs/{knobId}", "patch!"), "PATCH", "knob"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Load([]byte(specWith(tc.paths)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			got := Classify(doc)
			if len(got.Entities) != 1 {
				t.Fatalf("entities = %+v, excluded = %+v", got.Entities, got.Excluded)
			}
			c := got.Entities[0]
			if c.Key != tc.wantKey {
				t.Fatalf("key = %q, want %q", c.Key, tc.wantKey)
			}
			if kinds(c) != "action" {
				t.Fatalf("kinds = %q, want action", kinds(c))
			}
			if c.Create == nil || c.Create.Method != tc.wantMethod {
				t.Fatalf("the invocation must be in the create slot as %s, got %+v", tc.wantMethod, c.Create)
			}
			if c.Read != nil || c.Update != nil || c.Delete != nil || c.List != nil {
				t.Fatalf("an action fills no other role: %+v", c)
			}
			if len(c.Extra) != 0 {
				t.Fatalf("the invocation must not also be surplus: %+v", c.Extra)
			}
		})
	}
}

func TestUnit_Specmodel_AReadOnlySingletonIsNotAResource(t *testing.T) {
	// Nothing writes it, so there is no resource to own. It is excluded
	// rather than emitted as a list of one.
	doc, err := Load([]byte(specWith(`  /health:
    get:
      operationId: getHealth
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Thing'
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := Classify(doc)
	if len(got.Entities) != 0 {
		t.Fatalf("a read-only singleton must not classify: %+v", got.Entities)
	}
}

func TestUnit_CarriesCollection_SeparatesACollectionFromASingleton(t *testing.T) {
	element := &Schema{Type: "object"}
	for _, tc := range []struct {
		name string
		in   *Schema
		want bool
	}{
		{"bare array", &Schema{Type: "array", Items: element}, true},
		{"wrapped envelope", &Schema{Type: "object", Properties: []Property{
			{Name: "totalCount", Schema: &Schema{Type: "integer"}},
			{Name: "results", Schema: &Schema{Type: "array", Items: element}},
		}}, true},
		{"composed envelope", &Schema{AllOf: []*Schema{
			{Type: "object", Properties: []Property{{Name: "items", Schema: &Schema{Type: "array", Items: element}}}},
		}}, true},
		{"singleton object", &Schema{Type: "object", Properties: []Property{
			{Name: "timezone", Schema: &Schema{Type: "string"}},
		}}, false},
		{"nothing", nil, false},
	} {
		if got := carriesCollection(tc.in); got != tc.want {
			t.Errorf("%s: carriesCollection = %v, want %v", tc.name, got, tc.want)
		}
	}
}
