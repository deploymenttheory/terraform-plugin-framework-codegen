package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/pilot/thousandeyes-kiota/internal/sdk/models"
)

// TestUnit_Client_SendsPlainJSONBodies pins the wire shape of a write.
//
// Kiota's default middleware gzips every request body and sets
// Content-Encoding, and the ThousandEyes API answers that with a bare 400 --
// which is exactly how the first live acceptance run of this pilot failed,
// with nothing in the error naming the cause. The recorded probe evidence was
// gathered over plain JSON, so plain JSON is the contract this client must
// keep; this test fails if the compression middleware ever comes back.
func TestUnit_Client_SendsPlainJSONBodies(t *testing.T) {
	t.Parallel()

	type captured struct {
		method, path, contentType, encoding, auth string
		body                                      []byte
	}
	var got captured

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = captured{
			method:      r.Method,
			path:        r.URL.String(),
			contentType: r.Header.Get("Content-Type"),
			encoding:    r.Header.Get("Content-Encoding"),
			auth:        r.Header.Get("Authorization"),
			body:        b,
		}
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"11111111-1111-1111-1111-111111111111","key":"k","value":"v"}`))
	}))
	defer srv.Close()

	c, err := New(Config{BearerToken: "tok", APIEndpoint: srv.URL + "/v7"})
	if err != nil {
		t.Fatal(err)
	}

	body := models.NewTags_API_TagInfo()
	k, v := "tfacc-key", "tfacc-value"
	body.SetKey(&k)
	body.SetValue(&v)
	ot, _ := models.ParseTags_API_ObjectType("test")
	body.SetObjectType(ot.(*models.Tags_API_ObjectType))

	if _, err := c.Tags().Post(context.Background(), body, nil); err != nil {
		t.Fatalf("Post: %v", err)
	}

	if got.method != http.MethodPost || got.path != "/v7/tags" {
		t.Errorf("request = %s %s, want POST /v7/tags", got.method, got.path)
	}
	if got.auth != "Bearer tok" {
		t.Errorf("Authorization = %q", got.auth)
	}
	if got.contentType != "application/json" {
		t.Errorf("Content-Type = %q", got.contentType)
	}
	if got.encoding != "" {
		t.Errorf("Content-Encoding = %q; the body must go uncompressed", got.encoding)
	}

	var decoded map[string]any
	if err := json.Unmarshal(got.body, &decoded); err != nil {
		t.Fatalf("the body is not plain JSON (%v):\n%q", err, got.body)
	}
	want := map[string]any{"key": "tfacc-key", "objectType": "test", "value": "tfacc-value"}
	for field, wantVal := range want {
		if decoded[field] != wantVal {
			t.Errorf("body[%s] = %v, want %v (full body: %s)", field, decoded[field], wantVal, got.body)
		}
	}
}

// TestUnit_Client_DeleteAcceptsAJSONForm pins the Accept header of a delete.
//
// The credentials DELETE declares no success media type, so Kiota emits
// `Accept: application/problem+json` alone -- and the ThousandEyes API
// answers exactly that header with 406 Not Acceptable, which is how the
// credential resource failed its first live acceptance run. The client's
// acceptNormalizer widens the header to admit the API's real success forms;
// this test fails if a delete ever goes out refusing them again.
func TestUnit_Client_DeleteAcceptsAJSONForm(t *testing.T) {
	t.Parallel()

	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := New(Config{BearerToken: "tok", APIEndpoint: srv.URL + "/v7"})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Credentials().ById("11111111-1111-1111-1111-111111111111").Delete(context.Background(), nil); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "application/hal+json") {
		t.Errorf("Accept = %q; a delete must admit the API's success media types or the server answers 406", accept)
	}
}
