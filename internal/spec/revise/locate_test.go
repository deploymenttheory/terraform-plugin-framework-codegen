package revise

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// locatorFor parses a document and wraps its top node.
func locatorFor(t *testing.T, doc string) *locator {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		t.Fatalf("fixture is not YAML: %v", err)
	}
	return &locator{top: root.Content[0]}
}

func TestUnit_Locator_NodeAtWalksMappingsAndSequences(t *testing.T) {
	t.Parallel()
	loc := locatorFor(t, tagSpec)

	if n := loc.nodeAt("/components/schemas/Tag/properties/color/enum/1"); n == nil || n.Value != "blue" {
		t.Errorf("enum/1 = %v, want blue", n)
	}
	if n := loc.nodeAt(""); n == nil {
		t.Error("the empty pointer must address the document itself")
	}
	for _, ptr := range []string{
		"no-leading-slash",
		"/components/schemas/Missing",
		"/components/schemas/Tag/properties/color/enum/9",
		"/components/schemas/Tag/properties/color/enum/x",
		"/openapi/child-of-a-scalar",
	} {
		if n := loc.nodeAt(ptr); n != nil {
			t.Errorf("nodeAt(%q) = %v, want nil", ptr, n)
		}
	}
}

func TestUnit_Locator_FollowSchemaRefsRefusesWhatItCannotResolve(t *testing.T) {
	t.Parallel()
	loc := locatorFor(t, `
external:
  $ref: 'other.yaml#/components/schemas/X'
dangling:
  $ref: '#/components/schemas/Nowhere'
`)
	if _, _, ok := loc.followSchemaRefs(loc.nodeAt("/external"), "/external"); ok {
		t.Error("a reference outside the document must not resolve")
	}
	if node, _, ok := loc.followSchemaRefs(loc.nodeAt("/dangling"), "/dangling"); ok || node != nil {
		t.Error("a dangling reference must not resolve")
	}
}

func TestUnit_Locator_ResponseSchemaFallsBackToDefault(t *testing.T) {
	t.Parallel()
	loc := locatorFor(t, `
paths:
  /items/{id}:
    get:
      responses:
        "204":
          description: no body
        default:
          content:
            application/json:
              schema:
                type: object
`)
	op := &specmodel.Op{Method: "GET", Path: "/items/{id}"}
	node, ptr, ok := loc.responseSchema(op)
	if !ok || node == nil {
		t.Fatal("the default response's schema must be found")
	}
	if want := "/paths/~1items~1{id}/get/responses/default/content/application~1json/schema"; ptr != want {
		t.Errorf("ptr = %q, want %q", ptr, want)
	}
}

func TestUnit_Locator_ResponseSchemaReportsAnOperationWithoutOne(t *testing.T) {
	t.Parallel()
	loc := locatorFor(t, `
paths:
  /items/{id}:
    get:
      responses:
        "204":
          description: no body
    delete: {}
`)
	if _, _, ok := loc.responseSchema(&specmodel.Op{Method: "GET", Path: "/items/{id}"}); ok {
		t.Error("no response declares a schema; nothing must be found")
	}
	if _, _, ok := loc.responseSchema(&specmodel.Op{Method: "DELETE", Path: "/items/{id}"}); ok {
		t.Error("an operation with no responses must find nothing")
	}
	if _, _, ok := loc.responseSchema(nil); ok {
		t.Error("a missing operation must find nothing")
	}
	if _, _, ok := loc.requestSchema(nil); ok {
		t.Error("a missing operation has no request schema")
	}
}

func TestUnit_Locator_ResponseContentRefusesAForeignReference(t *testing.T) {
	t.Parallel()
	loc := locatorFor(t, `
paths:
  /items/{id}:
    get:
      responses:
        "200":
          $ref: 'other.yaml#/responses/X'
`)
	if _, _, ok := loc.responseSchema(&specmodel.Op{Method: "GET", Path: "/items/{id}"}); ok {
		t.Error("a response reference outside the document must not resolve")
	}
}

func TestUnit_Locator_RequestSchemaRefusesAForeignBodyReference(t *testing.T) {
	t.Parallel()
	loc := locatorFor(t, `
paths:
  /items:
    post:
      requestBody:
        $ref: 'other.yaml#/requestBodies/X'
`)
	if _, _, ok := loc.requestSchema(&specmodel.Op{Method: "POST", Path: "/items"}); ok {
		t.Error("a request-body reference outside the document must not resolve")
	}
}

func TestUnit_Locator_IsJSONMediaMatchesSuffixedVariants(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"application/json":     true,
		"application/hal+json": true,
		"application/xml":      false,
		"text/plain":           false,
		"application/foo+xml":  false,
	}
	for mt, want := range cases {
		if got := isJSONMedia(mt); got != want {
			t.Errorf("isJSONMedia(%q) = %v, want %v", mt, got, want)
		}
	}
}
