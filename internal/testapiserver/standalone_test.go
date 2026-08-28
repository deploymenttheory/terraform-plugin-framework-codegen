package testapiserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// standaloneClient gives each standalone test its own connection pool.
// The shared http.DefaultClient is unusable here: httptest.Server.Close —
// which every parallel test in this package calls at cleanup — closes
// http.DefaultTransport's idle connections, and a pooled connection torn
// down mid-flight surfaces as "transport connection broken" in whichever
// unlucky test was reusing it.
func standaloneClient(t *testing.T) *http.Client {
	t.Helper()
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}
}

// call performs one request with the test's own client and decodes the
// JSON answer, if any.
func call(t *testing.T, c *http.Client, method, url string, body map[string]any) (int, map[string]any) {
	t.Helper()

	var r io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		r = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, url, r) //nolint:noctx // a test fixture
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}

	var out map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return response.StatusCode, out
}

// TestUnit_Standalone_ServesTheDocumentedLifecycle boots the standalone
// server on a real listener and walks every operation the embedded document
// declares: create, list, read, update, delete. The fixture and its document
// must agree, or a pipeline exercised against the standalone server audits
// a surface the imported spec does not describe.
func TestUnit_Standalone_ServesTheDocumentedLifecycle(t *testing.T) {
	t.Parallel()
	s, err := Standalone("127.0.0.1:0", Quirks{})
	if err != nil {
		t.Fatalf("Standalone: %v", err)
	}
	t.Cleanup(s.Close)
	c := standaloneClient(t)

	body := map[string]any{"name": "one", "mode": "basic", "code": "abc", "notes": "n"}
	status, created := call(t, c, http.MethodPost, s.CollectionURL(), body)
	if status != http.StatusCreated {
		t.Fatalf("POST %s = %d, want 201 (%v)", s.CollectionURL(), status, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("the created object carries no id: %v", created)
	}

	if status, listed := call(t, c, http.MethodGet, s.CollectionURL(), nil); status != http.StatusOK || listed[listWrapperKey] == nil {
		t.Fatalf("GET %s = %d %v, want 200 with %q", s.CollectionURL(), status, listed, listWrapperKey)
	}
	if status, _ := call(t, c, http.MethodGet, s.ItemURL(id), nil); status != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", s.ItemURL(id), status)
	}
	if status, _ := call(t, c, http.MethodPut, s.ItemURL(id), map[string]any{"name": "two"}); status != http.StatusOK {
		t.Fatalf("PUT %s = %d, want 200", s.ItemURL(id), status)
	}
	if status, _ := call(t, c, http.MethodDelete, s.ItemURL(id), nil); status != http.StatusNoContent {
		t.Fatalf("DELETE %s = %d, want 204", s.ItemURL(id), status)
	}
}

// TestUnit_Standalone_RefusesAnUnusableAddress asserts the listen failure
// surfaces as an error naming the address rather than a panic.
func TestUnit_Standalone_RefusesAnUnusableAddress(t *testing.T) {
	t.Parallel()
	if _, err := Standalone("not-an-address", Quirks{}); err == nil {
		t.Fatal("Standalone accepted an address nothing can listen on")
	}
}

// TestUnit_Spec_ClassifiesTheDocumentedSurface holds the embedded document to
// the promise its name makes: specmodel loads it and classifies every surface
// the server serves. /things, /monitors and /assignments are full-lifecycle
// resources; /agents is the read-only pool, a datasource. This is the shape
// the audit planner and both generators need — and it is what lets
// specmodel/strategy meet the v2 shape resources at all.
func TestUnit_Spec_ClassifiesTheDocumentedSurface(t *testing.T) {
	t.Parallel()
	document, err := specmodel.Load(Spec())
	if err != nil {
		t.Fatalf("the embedded document does not load: %v", err)
	}

	cls := specmodel.Classify(document)
	byPath := make(map[string]specmodel.Classification, len(cls.Entities))
	for _, e := range cls.Entities {
		byPath[e.CollectionPath] = e
	}

	// The full-lifecycle resources: every role filled.
	for _, path := range []string{collectionPath, "/monitors", "/assignments"} {
		e, ok := byPath[path]
		if !ok {
			t.Errorf("the document declares no entity at %q", path)
			continue
		}
		for role, operation := range map[string]*specmodel.OperationReference{
			"create": e.Create, "read": e.Read, "update": e.Update, "delete": e.Delete, "list": e.List,
		} {
			if operation == nil {
				t.Errorf("%s: the document declares no %s operation", path, role)
			}
		}
		if !hasKind(e.Kinds, specmodel.KindResource) {
			t.Errorf("%s: classified %v, want a resource", path, e.Kinds)
		}
	}

	// /agents is list + read, no create/update/delete: a datasource, not a
	// resource — the read-only pool assignments borrow from.
	agents, ok := byPath["/agents"]
	if !ok {
		t.Fatalf("the document declares no /agents entity: %+v", cls.Entities)
	}
	if agents.Create != nil || agents.Delete != nil {
		t.Errorf("/agents should be read-only, got create=%v delete=%v", agents.Create, agents.Delete)
	}
	if !hasKind(agents.Kinds, specmodel.KindDatasource) {
		t.Errorf("/agents classified %v, want a datasource", agents.Kinds)
	}
	if hasKind(agents.Kinds, specmodel.KindResource) {
		t.Errorf("/agents classified as a resource; it has no create/delete: %v", agents.Kinds)
	}
}

func hasKind(kinds []specmodel.Kind, want specmodel.Kind) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

// TestUnit_Spec_ReturnsACopy asserts a caller scribbling on the returned
// bytes cannot corrupt what the next caller reads.
func TestUnit_Spec_ReturnsACopy(t *testing.T) {
	t.Parallel()
	a := Spec()
	a[0] = 'X'
	if b := Spec(); b[0] == 'X' {
		t.Fatal("Spec hands out the embedded backing array itself")
	}
}

// TestUnit_StandaloneQuirks_DeviatesFromTheDocument spot-checks that the
// standalone profile actually misbehaves in the ways its comments claim:
// the discarded field, the unserved documented default, and the volatile
// undeclared field. The audit's whole rehearsal value rests on these
// deviations existing.
func TestUnit_StandaloneQuirks_DeviatesFromTheDocument(t *testing.T) {
	t.Parallel()
	s, err := Standalone("127.0.0.1:0", StandaloneQuirks())
	if err != nil {
		t.Fatalf("Standalone: %v", err)
	}
	t.Cleanup(s.Close)
	c := standaloneClient(t)

	status, created := call(t, c, http.MethodPost, s.CollectionURL(),
		map[string]any{"name": "one", "mode": "basic", "code": "MiXeD", "notes": "kept?"})
	if status != http.StatusCreated {
		t.Fatalf("POST = %d, want 201 (%v)", status, created)
	}
	id, _ := created["id"].(string)

	_, read := call(t, c, http.MethodGet, s.ItemURL(id), nil)
	if _, present := read["notes"]; present {
		t.Errorf("notes survived a read; the profile says it is silently discarded: %v", read)
	}
	if got := read["retention"]; got != float64(45) {
		t.Errorf("retention = %v, want the profile's 45 (the document claims 30)", got)
	}
	if got := read["code"]; got != "mixed" {
		t.Errorf("code = %v, want the normalised %q", got, "mixed")
	}
	first, _ := read["etag"].(string)
	_, again := call(t, c, http.MethodGet, s.ItemURL(id), nil)
	if second, _ := again["etag"].(string); first == "" || first == second {
		t.Errorf("etag = %q then %q, want a volatile undeclared field", first, second)
	}
}
