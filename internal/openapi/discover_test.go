package openapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// miniSpec is a small document exercising the shapes discovery has to get right:
// a full-lifecycle collection, a read-only one, a create-with-no-read, and a
// non-CRUD operation hanging off a resource path.
const miniSpec = `
openapi: 3.0.3
info:
  title: Test API
  version: 1.2.3
paths:
  /tags:
    get:
      operationId: getTags
      tags: [Tags]
    post:
      operationId: createTag
      tags: [Tags]
  /tags/{id}:
    get:
      operationId: getTag
      tags: [Tags]
    put:
      operationId: updateTag
      tags: [Tags]
    delete:
      operationId: deleteTag
      tags: [Tags]
  /tags/{id}/assign:
    post:
      operationId: assignTag
      tags: [Tags]
  /usage:
    get:
      operationId: getUsage
      tags: [Usage]
  /instant-tests:
    post:
      operationId: runInstantTest
      tags: [Instant Tests]
`

func loadMini(t *testing.T) *Document {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "api.yaml")
	if err := os.WriteFile(path, []byte(miniSpec), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	doc, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return doc
}

func find(t *testing.T, cs []Candidate, key string) Candidate {
	t.Helper()
	for _, c := range cs {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("no candidate keyed %q; got %v", key, keys(cs))
	return Candidate{}
}

func keys(cs []Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Key)
	}
	return out
}

func TestUnit_Discover_PairsCollectionAndItemPaths(t *testing.T) {
	t.Parallel()

	got := find(t, loadMini(t).Discover(), "tag")

	if got.CollectionPath != "/tags" || got.ItemPath != "/tags/{id}" {
		t.Errorf("paths = %q and %q", got.CollectionPath, got.ItemPath)
	}
	if got.Tag != "Tags" {
		t.Errorf("Tag = %q", got.Tag)
	}

	// Which slot a method means depends on whether the path carries an
	// identifier: POST to a collection creates, GET on a collection lists.
	for name, op := range map[string]*Operation{
		"create": got.Create, "read": got.Read,
		"update": got.Update, "delete": got.Delete, "list": got.List,
	} {
		if op == nil {
			t.Errorf("%s was not discovered", name)
		}
	}
	if got.Create != nil && got.Create.OperationID != "createTag" {
		t.Errorf("create = %q, want createTag", got.Create.OperationID)
	}
	if got.List != nil && got.List.OperationID != "getTags" {
		t.Errorf("list = %q, want getTags", got.List.OperationID)
	}
}

// TestUnit_Discover_NonCrudOperationsAreKeptNotDiscarded matters for curation: an
// action hanging off a resource path is something a person needs to see, not
// something to silently drop.
func TestUnit_Discover_NonCrudOperationsAreKeptNotDiscarded(t *testing.T) {
	t.Parallel()

	all := loadMini(t).Discover()

	// "/tags/{id}/assign" has no trailing parameter, so it is its own collection
	// rather than part of the tag candidate.
	c := find(t, all, "tags_assign")
	if c.Create == nil || c.Create.OperationID != "assignTag" {
		t.Errorf("the assign operation was lost: %+v", c)
	}

	kind, why := c.Classify()
	if kind != CandidateKindNeither {
		t.Errorf("classify = %s (%s), want neither: it can be created and never read", kind, why)
	}
}

func TestUnit_Discover_Classify(t *testing.T) {
	t.Parallel()

	all := loadMini(t).Discover()

	tests := []struct {
		key  string
		want CandidateKind
	}{
		{"tag", CandidateKindResource},
		{"usage", CandidateKindDataSource},
		// Create with no read is a job submission: Terraform could make one and
		// never see it again.
		{"instant_test", CandidateKindNeither},
	}

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			got, why := find(t, all, tc.key).Classify()
			if got != tc.want {
				t.Errorf("Classify() = %s (%s), want %s", got, why, tc.want)
			}
		})
	}
}

// TestUnit_Discover_ClassifyReportsPartialLifecycles: a resource with no update
// is still a resource, but the reason has to reach the operator, because it
// implies RequiresReplace on everything.
func TestUnit_Discover_ClassifyReportsPartialLifecycles(t *testing.T) {
	t.Parallel()

	op := &Operation{Method: "GET"}

	kind, why := Candidate{Create: op, Read: op}.Classify()
	if kind != CandidateKindResource {
		t.Fatalf("kind = %s, want resource", kind)
	}
	if why != "no update or delete" {
		t.Errorf("why = %q, want it to name both gaps", why)
	}

	_, why = Candidate{Create: op, Read: op, Delete: op}.Classify()
	if why != "no update" {
		t.Errorf("why = %q", why)
	}
}

func TestUnit_Discover_KeyForPath(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"/tags", "tag"},
		{"/v7/tests/http-server", "v7_tests_http_server"},
		{"/dashboards", "dashboard"},
		// Plural endings REST collections actually use.
		{"/policies", "policy"},
		{"/addresses", "address"},
		{"/matches", "match"},

		// Singular words that merely end in "s". "statu" and "analysi" are the
		// kind of wrong that survives review.
		{"/usage", "usage"},
		{"/status", "status"},
		{"/analysis", "analysis"},
		{"/access", "access"},
		{"/", "root"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := keyForPath(tc.in); got != tc.want {
				t.Errorf("keyForPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUnit_Discover_SplitPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in         string
		collection string
		isItem     bool
	}{
		{"/tags", "/tags", false},
		{"/tags/{id}", "/tags", true},
		{"/tests/{testId}", "/tests", true},
		// Only a *trailing* parameter makes an item path; a parameter in the
		// middle means a sub-collection, which is its own candidate.
		{"/tags/{id}/assign", "/tags/{id}/assign", false},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			gotCollection, gotIsItem := splitPath(tc.in)
			if gotCollection != tc.collection || gotIsItem != tc.isItem {
				t.Errorf("splitPath(%q) = (%q, %v), want (%q, %v)",
					tc.in, gotCollection, gotIsItem, tc.collection, tc.isItem)
			}
		})
	}
}

func TestUnit_Discover_IsDeterministic(t *testing.T) {
	t.Parallel()

	doc := loadMini(t)

	first := keys(doc.Discover())
	for i := range 20 {
		again := keys(doc.Discover())
		if len(again) != len(first) {
			t.Fatalf("run %d returned %d candidates, first returned %d", i, len(again), len(first))
		}
		for j := range again {
			if again[j] != first[j] {
				t.Fatalf("run %d differs at %d: %q vs %q", i, j, again[j], first[j])
			}
		}
	}
}

// TestUnit_Discover_AgainstTheCommittedSpecification runs against the real 1.6 MB
// ThousandEyes document rather than a fixture.
//
// The assertion that matters is the tag candidate: its blueprint was written by
// hand from the SDK's method set, so discovery agreeing with it is independent
// evidence that the pairing logic is right, from a different source.
func TestUnit_Discover_AgainstTheCommittedSpecification(t *testing.T) {
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
	if doc.Version != "7.0.97" {
		t.Errorf("Version = %q, want 7.0.97", doc.Version)
	}

	all := doc.Discover()
	if len(all) < 50 {
		t.Fatalf("only %d candidates from a 201-path document; discovery is not working", len(all))
	}

	tag := find(t, all, "tag")

	kind, why := tag.Classify()
	if kind != CandidateKindResource {
		t.Errorf("tag classified as %s (%s), want resource", kind, why)
	}
	if why != "full lifecycle" {
		t.Errorf("tag reason = %q, want full lifecycle", why)
	}

	// The operation IDs the hand-authored blueprint binds to.
	for _, tc := range []struct {
		name string
		op   *Operation
		want string
	}{
		{"create", tag.Create, "createTag"},
		{"read", tag.Read, "getTag"},
		{"update", tag.Update, "updateTag"},
		{"delete", tag.Delete, "deleteTag"},
		{"list", tag.List, "getTags"},
	} {
		switch {
		case tc.op == nil:
			t.Errorf("tag %s was not discovered", tc.name)
		case tc.op.OperationID != tc.want:
			t.Errorf("tag %s = %q, want %q", tc.name, tc.op.OperationID, tc.want)
		}
	}

	// The update verb decides whether an omitted field is preserved or cleared,
	// which the blueprint records as putFull. Discovery must see the same verb.
	if tag.Update != nil && tag.Update.Method != "PUT" {
		t.Errorf("tag update is %s; the blueprint's putFull update style assumes PUT", tag.Update.Method)
	}
}

// TestUnit_OpenAPI_AuthFromSecuritySchemes covers reading the auth method off
// the document, using the shapes a real second API declares: Jamf Pro offers
// bearer, basic and an OAuth client-credentials flow whose token endpoint is a
// path rather than a URL.
func TestUnit_OpenAPI_AuthFromSecuritySchemes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		schemes  string
		want     blueprint.AuthMethod
		tokenURL string
		ok       bool
	}{
		{
			name: "client credentials wins, and carries its token endpoint",
			schemes: `
    Bearer: {type: http, scheme: bearer}
    BasicAuth: {type: http, scheme: basic}
    ApiClient:
      type: oauth2
      flows:
        clientCredentials:
          tokenUrl: /api/v1/oauth/token
          scopes: {}`,
			want: blueprint.AuthClientCredentials, tokenURL: "/api/v1/oauth/token", ok: true,
		},
		{
			name:    "bearer where there is no grant to run",
			schemes: "\n    Bearer: {type: http, scheme: bearer}",
			want:    blueprint.AuthBearerToken, ok: true,
		},
		{
			name:    "basic alone",
			schemes: "\n    BasicAuth: {type: http, scheme: basic}",
			want:    blueprint.AuthUsernamePassword, ok: true,
		},
		{
			name:    "an api-key scheme is not one of the three",
			schemes: "\n    ApiKey: {type: apiKey, in: header, name: X-Key}",
			ok:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := loadSpec(t, `
openapi: 3.0.3
info: {title: T, version: "1"}
paths: {}
components:
  securitySchemes:`+tc.schemes+"\n")

			got, ok := doc.Auth()
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tc.ok, got)
			}
			if !tc.ok {
				return
			}
			if got.Resolved() != tc.want {
				t.Errorf("method = %q, want %q", got.Resolved(), tc.want)
			}
			if got.TokenURL != tc.tokenURL {
				t.Errorf("tokenUrl = %q, want %q", got.TokenURL, tc.tokenURL)
			}
		})
	}
}
