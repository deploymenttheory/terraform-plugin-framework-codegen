package observe

import (
	"path/filepath"
	"testing"
)

// TestUnit_Observe_RecordedBodiesRoundTrip proves a recorded body survives the
// trip to disk unchanged. A generated configuration is built from these, so a
// value that shifts in the file is a configuration that no longer matches the
// request the API accepted.
func TestUnit_Observe_RecordedBodiesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := []Bodies{
		{
			Entity: "tag",
			Minimal: &AcceptedBody{
				Status:   201,
				Request:  map[string]any{"key": "branch", "value": "sfo"},
				Response: map[string]any{"key": "branch", "value": "sfo", "id": "7"},
			},
		},
		{
			Entity:  "role",
			Maximal: &AcceptedBody{Status: 200, Request: map[string]any{"name": "n"}},
		},
		// Neither shape accepted: nothing to record, and nothing written.
		{Entity: "user"},
	}
	if err := WriteBodies(dir, in); err != nil {
		t.Fatalf("WriteBodies: %v", err)
	}

	out, err := ReadBodies(dir)
	if err != nil {
		t.Fatalf("ReadBodies: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("read %d entities, want the two that had an accepted create", len(out))
	}
	tag := out["tag"]
	if tag.Minimal == nil || tag.Minimal.Status != 201 {
		t.Fatalf("tag minimal = %#v", tag.Minimal)
	}
	if tag.Minimal.Request["value"] != "sfo" {
		t.Errorf("request value = %#v, want the value that was sent", tag.Minimal.Request["value"])
	}
	if _, recorded := out["user"]; recorded {
		t.Error("an entity with no accepted create was written")
	}
	if got := filepath.Base(dir); got == "" {
		t.Fatal("temp dir vanished")
	}
}

// TestUnit_Observe_EchoedReadsTheResponse pins the check a configuration
// depends on: a property the API took and never returned cannot be held in
// terraform state.
func TestUnit_Observe_EchoedReadsTheResponse(t *testing.T) {
	b := &AcceptedBody{
		Request:  map[string]any{"name": "n", "matchType": "and"},
		Response: map[string]any{"name": "n"},
	}
	if !b.Echoed("name") {
		t.Error("a property the response carried reads as not echoed")
	}
	if b.Echoed("matchType") {
		t.Error("a property the response omitted reads as echoed")
	}
	// No response recorded says nothing about any property.
	var none *AcceptedBody
	if none.Echoed("name") {
		t.Error("an absent record claimed an echo")
	}
}

// TestUnit_Observe_ReadBodiesToleratesNoRun proves a tree the probe has never
// run against reads as empty rather than as an error: generation falls back to
// deriving values from the document.
func TestUnit_Observe_ReadBodiesToleratesNoRun(t *testing.T) {
	out, err := ReadBodies(filepath.Join(t.TempDir(), "never-written"))
	if err != nil {
		t.Fatalf("a missing directory is a normal state: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("read %d entities from nothing", len(out))
	}
}
