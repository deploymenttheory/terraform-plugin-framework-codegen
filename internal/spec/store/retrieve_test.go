package store

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnit_SpecStore_RetrieveReadsALocalFile(t *testing.T) {
	path := writeSample(t, sampleYAML)
	document, err := Retrieve(path)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if string(document) != sampleYAML {
		t.Fatalf("doc = %q", document)
	}
}

func TestUnit_SpecStore_RetrieveRefusesAMissingFile(t *testing.T) {
	_, err := Retrieve(filepath.Join(t.TempDir(), "nowhere.yaml"))
	if err == nil || !strings.Contains(err.Error(), "nowhere.yaml") {
		t.Fatalf("err = %v, want a failure naming the path", err)
	}
}

func TestUnit_SpecStore_RetrieveRefusesAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Retrieve(path)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want an emptiness refusal", err)
	}
}

func TestUnit_SpecStore_RetrieveDownloadsOverHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleYAML))
	}))
	defer srv.Close()

	document, err := Retrieve(srv.URL + "/openapi.yaml")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if string(document) != sampleYAML {
		t.Fatalf("doc = %q", document)
	}
}

func TestUnit_SpecStore_RetrieveRefusesANonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := Retrieve(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want the status quoted", err)
	}
}

func TestUnit_SpecStore_RetrieveRefusesAnEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	_, err := Retrieve(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want an emptiness refusal", err)
	}
}

func TestUnit_SpecStore_RetrieveReportsAnUnreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	if _, err := Retrieve(url); err == nil {
		t.Fatal("retrieving from a closed server succeeded")
	}
}

func TestUnit_SpecStore_RetrieveReportsAnUnbuildableRequest(t *testing.T) {
	// A control character fails URL parsing inside the request constructor,
	// before anything could touch a network.
	_, err := Retrieve("http://vendor.example/\x7f")
	if err == nil || !strings.Contains(err.Error(), "building the request") {
		t.Fatalf("err = %v, want a request-building failure", err)
	}
}
