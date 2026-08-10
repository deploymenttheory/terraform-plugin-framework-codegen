package run

import (
	"context"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/quirkserver"
)

// TestUnit_Steps_RefusedMinimalCreateBlocksTheEntity: an API that
// refuses the smallest valid body leaves nothing to audit; the pending
// claims record blocked with the refusal as proof.
func TestUnit_Steps_RefusedMinimalCreateBlocksTheEntity(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{
		// A required field the plan's minimal body does not carry.
		RequiredButUndeclared: []string{"serial"},
	})

	obs, sum, err := Run(context.Background(), testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := entityStatus(t, sum, "thing")
	if got.Status != StatusBlocked || !strings.Contains(got.Reason, "refused with status 400") {
		t.Fatalf("thing = %+v, want blocked on the refused create", got)
	}
	blocked := findObs(obs, "thing", "", observe.KindDeleteNotFoundOK)
	if blocked == nil || blocked.Outcome != observe.OutcomeBlocked {
		t.Fatalf("deleteNotFoundOK = %+v, want blocked", blocked)
	}
	if len(blocked.Excerpts) == 0 || blocked.Excerpts[0].Status != 400 {
		t.Errorf("the blocked claim carries no refusal excerpt: %+v", blocked.Excerpts)
	}
}

// TestUnit_Steps_UnreadableObjectBlocksDownstream: an object that never
// becomes readable within the poll window leaves readAfterWrite
// inconclusive and blocks what needed the read.
func TestUnit_Steps_UnreadableObjectBlocksDownstream(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{EventuallyConsistentReads: 100000})

	p := thingPlan(resourceSteps(), 60)
	p.Entities[0].Steps[1].Poll = &plan.Poll{Interval: "10ms", Timeout: "50ms"}

	obs, sum, err := Run(context.Background(), testOptions(t, s, p, testEnv(), nil))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := entityStatus(t, sum, "thing")
	if got.Status != StatusBlocked || !strings.Contains(got.Reason, "never became readable") {
		t.Fatalf("thing = %+v", got)
	}
	ra := findObs(obs, "thing", "", observe.KindReadAfterWrite)
	if ra == nil || ra.Outcome != observe.OutcomeInconclusive {
		t.Fatalf("readAfterWrite = %+v, want inconclusive", ra)
	}
	// The boundary cleanup still removed the unread object.
	if len(s.Objects()) != 0 {
		t.Errorf("objects remain: %v", s.Objects())
	}
}

// TestUnit_Steps_CleanupDeleteEndsTheEntityAtZero: with no
// deleteWithConfirmation in the plan, cleanupDelete alone must remove the
// entity's creations.
func TestUnit_Steps_CleanupDeleteEndsTheEntityAtZero(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	item := map[string]string{"thingId": "$created:thing"}
	steps := []plan.Step{
		{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/things",
			Body: map[string]any{"name": "tfpfgen-<runid>-thing-name"}},
		{Kind: plan.StepCleanupDelete, Method: "DELETE", Path: "/things/{thingId}", PathValues: item},
	}
	_, sum := mustRun(t, testOptions(t, s, thingPlan(steps, 60), testEnv(), nil))
	if got := entityStatus(t, sum, "thing"); got.Status != StatusAudited {
		t.Fatalf("thing = %+v", got)
	}
	if len(s.Objects()) != 0 {
		t.Fatalf("cleanupDelete left objects: %v", s.Objects())
	}
	if sum.CleanupEnd.LedgerDeletes+sum.CleanupEnd.PrefixDeletes != 0 {
		t.Errorf("the boundary cleanup had to mop up after cleanupDelete: %+v", sum.CleanupEnd)
	}
}

// TestUnit_Steps_FailingDeletesLeaveOrphansReported: an API whose
// deletes all fail must end with the orphans named, not with a claim of
// a clean removal.
func TestUnit_Steps_FailingDeletesLeaveOrphansReported(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{DeleteFails: true})

	obs, sum, err := Run(context.Background(), testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sum.CleanupEnd.Orphans) == 0 {
		t.Fatal("failing deletes must surface as orphans")
	}
	if len(s.Objects()) == 0 {
		t.Fatal("the quirk did not hold objects back")
	}
	// The delete claim could not be confirmed.
	o := findObs(obs, "thing", "", observe.KindDeleteNotFoundOK)
	if o == nil || o.Outcome != observe.OutcomeInconclusive {
		t.Errorf("deleteNotFoundOK = %+v, want inconclusive under failing deletes", o)
	}
}

// TestUnit_Steps_LookupReadOfAMissingKeyBlocks: the operator-supplied
// key fetched nothing, so the entity blocks with the status in the
// reason.
func TestUnit_Steps_LookupReadOfAMissingKeyBlocks(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	p := thingPlan(nil, 10)
	p.Entities[0].Role = "lookup"
	p.Entities[0].Steps = []plan.Step{{
		Kind: plan.StepRead, Method: "GET", Path: "/things/{thingId}",
		PathValues: map[string]string{"thingId": "does-not-exist"},
	}}
	opts := testOptions(t, s, p, testEnv(), nil)
	opts.WorkDir = ""
	_, sum, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := entityStatus(t, sum, "thing")
	if got.Status != StatusBlocked || !strings.Contains(got.Reason, "404") {
		t.Fatalf("thing = %+v, want blocked with the status", got)
	}
}

// TestUnit_Steps_UnknownStepKindBlocks: a plan from a newer toolkit
// carries a kind this build cannot execute; the entity blocks by name
// rather than silently skipping.
func TestUnit_Steps_UnknownStepKindBlocks(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	p := thingPlan([]plan.Step{{Kind: plan.StepKind("teleport"), Method: "GET", Path: "/things"}}, 10)
	opts := testOptions(t, s, p, testEnv(), nil)
	opts.WorkDir = ""
	_, sum, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := entityStatus(t, sum, "thing"); got.Status != StatusBlocked || !strings.Contains(got.Reason, "teleport") {
		t.Fatalf("thing = %+v", got)
	}
}

// TestUnit_Steps_UnknownFieldRejectionCalibrates: an API that refuses a
// body carrying an undeclared field calibrates as rejecting, via a quirk
// keyed to the injected field's value.
func TestUnit_Steps_UnknownFieldRejectionCalibrates(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{
		RejectsDocumentedValue: map[string]string{"tfpfgen_unknown_field": "true"},
	})
	item := map[string]string{"thingId": "$created:thing"}
	steps := []plan.Step{
		{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/things",
			Body: map[string]any{"name": "tfpfgen-<runid>-thing-name"}},
		{Kind: plan.StepUndeclaredSpecField, Method: "POST", Path: "/things",
			Body: map[string]any{"name": "tfpfgen-<runid>-thing-name-u", "tfpfgen_unknown_field": true}},
		{Kind: plan.StepCleanupDelete, Method: "DELETE", Path: "/things/{thingId}", PathValues: item},
	}
	_, sum := mustRun(t, testOptions(t, s, thingPlan(steps, 60), testEnv(), nil))
	if got := sum.Tolerance["thing"]; got != "rejects unknown body fields" {
		t.Fatalf("tolerance = %q, want the rejecting calibration", got)
	}
}

// TestUnit_Steps_RefusalWithoutTheFieldNameIsInconclusive: an empty
// error envelope names nothing, so an omitted-field refusal cannot be
// pinned on the field.
func TestUnit_Steps_RefusalWithoutTheFieldNameIsInconclusive(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{
		RequiredButUndeclared: []string{"serial"},
		ErrorEnvelope:         quirkserver.EnvelopeEmpty,
	})
	item := map[string]string{"thingId": "$created:thing"}
	steps := []plan.Step{
		{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/things",
			Body: map[string]any{"name": "tfpfgen-<runid>-thing-name", "serial": "s1"}},
		{Kind: plan.StepOmitRequired, Method: "POST", Path: "/things", Attribute: "serial",
			Body: map[string]any{"name": "tfpfgen-<runid>-thing-name-o"}},
		{Kind: plan.StepCleanupDelete, Method: "DELETE", Path: "/things/{thingId}", PathValues: item},
	}
	obs, _ := mustRun(t, testOptions(t, s, thingPlan(steps, 60), testEnv(), nil))
	o := findObs(obs, "thing", "serial", observe.KindRequiredByAPI)
	if o == nil || o.Outcome != observe.OutcomeInconclusive {
		t.Fatalf("requiredByAPI(serial) = %+v, want inconclusive when the refusal names nothing", o)
	}
}

// TestUnit_Steps_ForcedValueOnUpdateIsServerForced: the update stored
// neither the sent value nor the old one, and the stored value derives
// from nothing sent.
func TestUnit_Steps_ForcedValueOnUpdateIsServerForced(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{
		Forces: map[string]any{"power": "house-voltage"},
	})
	item := map[string]string{"thingId": "$created:thing"}
	steps := []plan.Step{
		{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/things",
			Body: map[string]any{"name": "tfpfgen-<runid>-thing-name"}},
		{Kind: plan.StepReadWithRetry, Method: "GET", Path: "/things/{thingId}", PathValues: item,
			Poll: &plan.Poll{Interval: "10ms", Timeout: "2s"}},
		{Kind: plan.StepReadConsecutive, Method: "GET", Path: "/things/{thingId}", PathValues: item},
		{Kind: plan.StepUpdateField, Method: "PUT", Path: "/things/{thingId}", PathValues: item,
			Attribute: "power",
			Body:      map[string]any{"name": "tfpfgen-<runid>-thing-name", "power": "high"}},
		{Kind: plan.StepCleanupDelete, Method: "DELETE", Path: "/things/{thingId}", PathValues: item},
	}
	obs, _ := mustRun(t, testOptions(t, s, thingPlan(steps, 60), testEnv(), nil))
	if o := wantConfirmed(t, obs, "thing", "power", observe.KindServerForced); o.Value != true {
		t.Fatalf("serverForced(power) = %v", o.Value)
	}
}
