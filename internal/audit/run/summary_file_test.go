package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
)

func TestUnit_Run_SummaryKeepsWhyAnEntityProducedNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit", SummaryFile)
	summary := Summary{
		RunID:   "abcd1234",
		Blocked: 1,
		Entities: []EntityResult{{
			Entity: "thing",
			Status: StatusBlocked,
			Reason: "the minimal create was refused with status 400, and adding name did not correct it",
			Refusal: &observe.Excerpt{
				Method: "POST", PathTemplate: "/things", Status: 400,
				ResponseFragment: json.RawMessage(`{"detail":"field serial is required"}`),
			},
		}},
	}

	if err := WriteSummary(path, summary); err != nil {
		t.Fatalf("WriteSummary: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the directory holding the summary was not created: %v", err)
	}
	// The API's own words are the part an operator cannot get anywhere else:
	// an entity that produced no request body leaves no other trace of them.
	if !strings.Contains(string(raw), "field serial is required") {
		t.Errorf("the refusal the API answered was not kept:\n%s", raw)
	}
	var back Summary
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("the summary does not read back: %v", err)
	}
	if len(back.Entities) != 1 || back.Entities[0].Refusal == nil {
		t.Fatalf("the refusal did not round-trip: %+v", back.Entities)
	}
	if back.Entities[0].Refusal.Status != 400 || back.Entities[0].Reason != summary.Entities[0].Reason {
		t.Errorf("entity = %+v, want the status and reason as written", back.Entities[0])
	}
}

func TestUnit_Run_SummaryIsWrittenWhenEveryEntityBlocked(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), SummaryFile)

	// A run that learned nothing is exactly the run whose reasons are worth
	// keeping, so an empty entity list is still a file.
	if err := WriteSummary(path, Summary{RunID: "none"}); err != nil {
		t.Fatalf("WriteSummary: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no summary was written: %v", err)
	}
}

func TestUnit_Run_ABlockedCreateNamesWhatTheSearchAskedFor(t *testing.T) {
	t.Parallel()

	// Nothing to add — the document named no field the search could try — so
	// the status is the whole of what is known.
	if got := minimalRefusedReason(400, nil); got != "the minimal create was refused with status 400" {
		t.Errorf("reason = %q, want the status alone", got)
	}
	// The search widened the body and was refused anyway. Which fields it
	// widened it with is the difference between a document that understates
	// the create and an API refusing it for another reason entirely.
	got := minimalRefusedReason(400, []string{"serial", "kind"})
	want := "the minimal create was refused with status 400, and adding serial, kind did not correct it"
	if got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
}
