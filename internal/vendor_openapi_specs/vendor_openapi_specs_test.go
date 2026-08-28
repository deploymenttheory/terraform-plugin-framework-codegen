package vendor_openapi_specs

import (
	"strings"
	"testing"
)

// TestUnit_VendorOpenAPISpecs_TheDocumentIsEmbeddedWhole guards the embed
// itself: a truncated or missing file compiles fine and fails a long way from
// here, in whichever consumer parsed it.
func TestUnit_VendorOpenAPISpecs_TheDocumentIsEmbeddedWhole(t *testing.T) {
	document := ThousandEyes()
	if len(document) == 0 {
		t.Fatal("the embedded thousandeyes document is empty")
	}
	if !strings.HasPrefix(string(document[:32]), "openapi: 3.0.1") {
		t.Errorf("the embedded document does not begin as an OpenAPI 3.0.1 document: %q", document[:32])
	}
	if !strings.Contains(string(document), "7.0.102") {
		t.Error("the embedded document does not declare the version its accessor documents")
	}
}
