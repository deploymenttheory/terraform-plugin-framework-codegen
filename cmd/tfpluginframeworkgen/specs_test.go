package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/snapshot"
)

// minimalSpec is the smallest document the ingester parses.
const minimalSpec = `openapi: 3.0.3
info:
  title: Test
  version: 1.2.3
paths:
  /things:
    get:
      operationId: listThings
      responses:
        "200":
          description: ok
`

func specServer(t *testing.T, body string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	return srv
}

// TestUnit_CLI_Specs_PinsAndThenRefreshesFromItsOwnRecord is the refresh loop end to end.
//
// The first pin needs -url; the second does not, because the snapshot that would go
// stale is the thing that records where it came from. An unchanged upstream pins
// nothing, so the weekly run is quiet when there is nothing to review.
func TestUnit_CLI_Specs_PinsAndThenRefreshesFromItsOwnRecord(t *testing.T) {
	t.Parallel()

	srv := specServer(t, minimalSpec)
	root := filepath.Join(t.TempDir(), "specs")

	if err := runSpecs([]string{"-url", srv.URL + "/api.yaml", "-output-dir", root}); err != nil {
		t.Fatalf("first pin: %v", err)
	}

	snaps, err := snapshot.List(root)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots = %v, %v", snaps, err)
	}
	if snaps[0].Version != "1.2.3" {
		t.Errorf("version = %q", snaps[0].Version)
	}
	if err := snaps[0].Verify(); err != nil {
		t.Errorf("the pinned snapshot must verify: %v", err)
	}

	meta, err := snaps[0].LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.SourceURL == "" || meta.PathCount != 1 || meta.OperationCount != 1 {
		t.Errorf("metadata = %+v", meta)
	}

	// Second run, no -url: unchanged upstream, nothing pinned.
	if err := runSpecs([]string{"-output-dir", root}); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if snaps, _ = snapshot.List(root); len(snaps) != 1 {
		t.Errorf("an unchanged upstream must pin nothing, got %d snapshots", len(snaps))
	}
}

// TestUnit_CLI_Specs_RefusesABrokenDownload: a document the ingester cannot read is not a
// snapshot, it is a broken download wearing one's name.
func TestUnit_CLI_Specs_RefusesABrokenDownload(t *testing.T) {
	t.Parallel()

	srv := specServer(t, "]] not yaml [[")
	root := filepath.Join(t.TempDir(), "specs")

	err := runSpecs([]string{"-url", srv.URL, "-output-dir", root})
	if err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("error = %v, want a parse refusal", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("nothing may be written for a broken download: %v", entries)
	}
}

// TestUnit_CLI_Specs_FirstPinNeedsAURL: there is no recorded source to reuse yet, and
// guessing one would be worse than asking.
func TestUnit_CLI_Specs_FirstPinNeedsAURL(t *testing.T) {
	t.Parallel()

	err := runSpecs([]string{"-output-dir", filepath.Join(t.TempDir(), "specs")})

	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("error = %v, want a usage error naming -url", err)
	}
}
