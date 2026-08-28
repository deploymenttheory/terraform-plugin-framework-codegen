package revise

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// locatorFor parses a document and wraps its top node.
func locatorFor(t *testing.T, document string) *locator {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(document), &root); err != nil {
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
	for _, pointer := range []string{
		"no-leading-slash",
		"/components/schemas/Missing",
		"/components/schemas/Tag/properties/color/enum/9",
		"/components/schemas/Tag/properties/color/enum/x",
		"/openapi/child-of-a-scalar",
	} {
		if n := loc.nodeAt(pointer); n != nil {
			t.Errorf("nodeAt(%q) = %v, want nil", pointer, n)
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
	operation := &specmodel.OperationReference{Method: "GET", Path: "/items/{id}"}
	node, pointer, ok := loc.responseSchema(operation)
	if !ok || node == nil {
		t.Fatal("the default response's schema must be found")
	}
	if want := "/paths/~1items~1{id}/get/responses/default/content/application~1json/schema"; pointer != want {
		t.Errorf("ptr = %q, want %q", pointer, want)
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
	if _, _, ok := loc.responseSchema(&specmodel.OperationReference{Method: "GET", Path: "/items/{id}"}); ok {
		t.Error("no response declares a schema; nothing must be found")
	}
	if _, _, ok := loc.responseSchema(&specmodel.OperationReference{Method: "DELETE", Path: "/items/{id}"}); ok {
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
	if _, _, ok := loc.responseSchema(&specmodel.OperationReference{Method: "GET", Path: "/items/{id}"}); ok {
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
	if _, _, ok := loc.requestSchema(&specmodel.OperationReference{Method: "POST", Path: "/items"}); ok {
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

func TestUnit_Locator_FindPathDescendsObjectsAndArrayItems(t *testing.T) {
	t.Parallel()
	loc := locatorFor(t, `
components:
  schemas:
    Auth:
      type: object
      properties:
        token:
          type: string
    Connector:
      type: object
      properties:
        authentication:
          $ref: '#/components/schemas/Auth'
        headers:
          type: array
          items:
            type: object
            properties:
              value:
                type: string
`)
	root := loc.nodeAt("/components/schemas/Connector")
	site, ok := loc.findPath(root, "/components/schemas/Connector", "authentication.token")
	if !ok || site.propPtr != "/components/schemas/Auth/properties/token" {
		t.Errorf("authentication.token located at %q (%v), want the referenced schema's property", site.propPtr, ok)
	}
	site, ok = loc.findPath(root, "/components/schemas/Connector", "headers.value")
	if !ok || site.propPtr != "/components/schemas/Connector/properties/headers/items/properties/value" {
		t.Errorf("headers.value located at %q (%v), want the array's item property", site.propPtr, ok)
	}
	if _, ok := loc.findPath(root, "/components/schemas/Connector", "authentication.missing"); ok {
		t.Error("a member the schema does not declare was located")
	}
	if site, ok := loc.findPath(root, "/components/schemas/Connector", "authentication"); !ok || site.propPtr != "/components/schemas/Connector/properties/authentication" {
		t.Errorf("a plain name located at %q (%v)", site.propPtr, ok)
	}
}
