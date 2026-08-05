package openapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnit_Exclusions_MatchByPrefixAndKey(t *testing.T) {
	t.Parallel()

	e := Exclusions{Exclusions: []Exclusion{
		{PathPrefix: "/test-results", Reason: "telemetry"},
		{Key: "event", Reason: "activity log"},
	}}

	if reason, ok := e.Match(Candidate{Key: "test_results_bgp", CollectionPath: "/test-results/{testId}/bgp"}); !ok || reason != "telemetry" {
		t.Errorf("prefix match = %q, %v", reason, ok)
	}
	if reason, ok := e.Match(Candidate{Key: "event", CollectionPath: "/events"}); !ok || reason != "activity log" {
		t.Errorf("key match = %q, %v", reason, ok)
	}
	if _, ok := e.Match(Candidate{Key: "tag", CollectionPath: "/tags"}); ok {
		t.Error("an unexcluded candidate matched")
	}
}

func TestUnit_Exclusions_LoadRefusesUnreasonedEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ExclusionsFileName)
	if err := os.WriteFile(path, []byte(`{"exclusions":[{"pathPrefix":"/x"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadExclusions(path); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Errorf("an unexplained exclusion must refuse to load, got: %v", err)
	}

	if _, err := LoadExclusions(filepath.Join(dir, "absent.json")); err != nil {
		t.Errorf("a missing sidecar is simply no exclusions, got: %v", err)
	}
}
