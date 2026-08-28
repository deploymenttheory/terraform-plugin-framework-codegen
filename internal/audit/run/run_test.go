package run

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/testapiserver"
)

// TestUnit_Run_HappyPathDerivesTheExpectedObservations is the package's
// backbone: a full derived plan against a test API server whose behaviour is
// known by construction, asserting that each configured quirk comes out
// as exactly the observation it should.
func TestUnit_Run_HappyPathDerivesTheExpectedObservations(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{
		SilentlyDiscards:         []string{"notes"},
		ImmutableAfterCreate:     []string{"color"},
		ConstantDefaults:         map[string]any{"retention": 30},
		VolatileFields:           []string{"etag"},
		NormalisesCase:           []string{"code"},
		RequiredButUndeclared:    []string{"name"},
		ClosedEnum:               map[string][]string{"mode": {"basic", "advanced"}},
		ConditionallyRequired:    &testapiserver.Conditional{WhenField: "mode", WhenValue: "advanced", Then: "query"},
		SilentlyDiscardsOnUpdate: []string{"label"},
	})

	p := derivedPlan(t)
	obs, summary := mustRun(t, testOptions(t, s, p, testEnv(), nil))

	// The lifecycle completed and left nothing behind.
	if got := entityStatus(t, summary, "thing"); got.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("thing status = %+v, want audited", got)
	}
	if left := s.Objects(); len(left) != 0 {
		t.Errorf("the tenant still holds %d object(s) after the run: %v", len(left), left)
	}

	// SilentlyDiscards: sent on create, absent on read.
	if o := wantConfirmed(t, obs, "thing", "notes", observe.KindWritable); o.Value != false {
		t.Errorf("writable(notes) = %v, want false", o.Value)
	}
	// A stored field round-trips.
	if o := wantConfirmed(t, obs, "thing", "name", observe.KindWritable); o.Value != true {
		t.Errorf("writable(name) = %v, want true", o.Value)
	}
	// ConstantDefaults: omitted on create, read back stable.
	if o := wantConfirmed(t, obs, "thing", "retention", observe.KindServerDefault); o.Value != float64(30) {
		t.Errorf("serverDefault(retention) = %v (%T), want 30", o.Value, o.Value)
	}
	// NormalisesCase: MiXeD went in, mixed came back.
	if o := wantConfirmed(t, obs, "thing", "code", observe.KindNormalisation); o.Value != "mixed" {
		t.Errorf("normalisation(code) = %v, want %q", o.Value, "mixed")
	}
	// VolatileFields: differs between two identical reads.
	if o := wantConfirmed(t, obs, "thing", "etag", observe.KindVolatile); o.Value != true {
		t.Errorf("volatile(etag) = %v, want true", o.Value)
	}
	if findObs(obs, "thing", "etag", observe.KindServerDefault) != nil {
		t.Error("the volatile etag was also written down as a server default")
	}
	// ImmutableAfterCreate: the update was refused naming the field.
	if o := wantConfirmed(t, obs, "thing", "color", observe.KindImmutable); o.Value != true {
		t.Errorf("immutable(color) = %v, want true", o.Value)
	}
	// SilentlyDiscardsOnUpdate: 2xx and not applied — not immutability.
	if o := wantConfirmed(t, obs, "thing", "label", observe.KindIgnoredOnUpdate); o.Value != true {
		t.Errorf("ignoredOnUpdate(label) = %v, want true", o.Value)
	}
	// RequiredButUndeclared: omitting name was refused naming it.
	if o := wantConfirmed(t, obs, "thing", "name", observe.KindRequiredByAPI); o.Value != true {
		t.Errorf("requiredByAPI(name) = %v, want true", o.Value)
	}
	// The documented requirement the API does not enforce.
	if o := wantConfirmed(t, obs, "thing", "mode", observe.KindRequiredByAPI); o.Value != false {
		t.Errorf("requiredByAPI(mode) = %v, want false", o.Value)
	}
	// ConditionallyRequired: present-under-advanced accepted, omitted
	// refused.
	rw := wantConfirmed(t, obs, "thing", "query", observe.KindRequiredWhen)
	if rw.Value != true || rw.Condition == nil || rw.Condition.Attribute != "mode" || rw.Condition.Equals != "advanced" {
		t.Errorf("requiredWhen(query) = %+v, want true when mode=advanced", rw)
	}
	// ClosedEnum plus ConditionallyRequired: basic accepted; advanced
	// refused while query rides nowhere, which names query rather than the
	// value and so rejects nothing; the undocumented value refused.
	values := wantConfirmed(t, obs, "thing", "mode", observe.KindValues)
	raw, _ := json.Marshal(values.Value)
	var v observe.Values
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("values(mode) does not decode: %v", err)
	}
	if len(v.Accepted) != 1 || v.Accepted[0] != "basic" {
		t.Errorf("values(mode).accepted = %v, want [basic]", v.Accepted)
	}
	if len(v.Rejected) != 0 {
		t.Errorf("values(mode).rejected = %v, want none: the refusal named a sibling, not the value", v.Rejected)
	}
	if v.Closed == nil || !*v.Closed {
		t.Errorf("values(mode).closed = %v, want true", v.Closed)
	}
	// Delete answered 404 the second time.
	if o := wantConfirmed(t, obs, "thing", "", observe.KindDeleteNotFoundOK); o.Value != true {
		t.Errorf("deleteNotFoundOK = %v, want true", o.Value)
	}
	// The read showed the object immediately.
	ra := wantConfirmed(t, obs, "thing", "", observe.KindReadAfterWrite)
	if d, err := time.ParseDuration(ra.Value.(string)); err != nil || d > time.Second {
		t.Errorf("readAfterWrite = %v, want a small duration", ra.Value)
	}
	// PUT preserved what the update request bodies omitted.
	if o := wantConfirmed(t, obs, "thing", "", observe.KindUpdateStyle); o.Value != "patch-merge" {
		t.Errorf("updateStyle = %v, want patch-merge", o.Value)
	}
	// The unknown-field probe's finding landed on the summary: this API
	// accepts and ignores a body field no schema declares.
	if got, ok := summary.RejectsUnknownFields["thing"]; !ok || got {
		t.Errorf("rejectsUnknownFields = %v (recorded %v), want false", got, ok)
	}
	// VolatileFields("etag") is also a field thingSpec never declares: the
	// reads demonstrably carry it, so it is an undocumented field with the
	// stable JSON type string.
	if o := wantConfirmed(t, obs, "thing", "etag", observe.KindUndocumentedFieldInSpec); o.Value != "string" {
		t.Errorf("undocumentedFieldInSpec(etag) = %v, want %q", o.Value, "string")
	}
	// Every declared field stays out of the undocumented findings.
	if o := findObs(obs, "thing", "retention", observe.KindUndocumentedFieldInSpec); o != nil {
		t.Errorf("the declared retention field was written down as undocumented: %+v", o)
	}

	// Budgets were respected and reported.
	if summary.Requests == 0 || summary.Requests > summary.RequestBudget {
		t.Errorf("requests = %d of %d", summary.Requests, summary.RequestBudget)
	}
	if summary.ObjectsCreated == 0 {
		t.Error("no objects were created by a full lifecycle run")
	}
	// Every observation carries the run's stamps.
	for _, o := range obs {
		if o.RunID != "testrun1" || o.SpecHash != "testspechash" {
			t.Fatalf("observation %s is missing its run stamps: %+v", o.ID, o)
		}
	}
	// The whole set is committable as-is.
	if err := observe.Write(t.TempDir(), obs); err != nil {
		t.Fatalf("the observations do not survive observe.Write: %v", err)
	}
}

// TestUnit_Run_EventuallyConsistentReadsMeasureTheLag drives the quirk
// where a new object 404s for its first reads, and expects the measured
// read-after-write lag to come out non-zero.
func TestUnit_Run_EventuallyConsistentReadsMeasureTheLag(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{EventuallyConsistentReads: 2})

	obs, summary := mustRun(t, testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))

	if got := entityStatus(t, summary, "thing"); got.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("thing status = %+v, want audited", got)
	}
	ra := wantConfirmed(t, obs, "thing", "", observe.KindReadAfterWrite)
	d, err := time.ParseDuration(ra.Value.(string))
	if err != nil {
		t.Fatalf("readAfterWrite value %v is not a duration", ra.Value)
	}
	if d < 10*time.Millisecond {
		t.Errorf("readAfterWrite = %s, want at least two 10ms poll intervals of lag", d)
	}
	if left := s.Objects(); len(left) != 0 {
		t.Errorf("objects remain: %v", left)
	}
}

// TestUnit_Run_MaximalRefusalBisectsToTheCulprit uses an empty error
// envelope — no field named — so the executor must earn the refused field by
// halving the optional set.
func TestUnit_Run_MaximalRefusalBisectsToTheCulprit(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{
		RejectsDocumentedValue: map[string]string{"color": "sample-color"},
		ErrorBody:              testapiserver.ErrorBodyEmpty,
	})

	p := derivedPlan(t)
	obs, summary := mustRun(t, testOptions(t, s, p, testEnv(), nil))

	values := findObs(obs, "thing", "color", observe.KindValues)
	if values == nil || values.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("no confirmed values(color) observation: %v", observationIndex(obs))
	}
	raw, _ := json.Marshal(values.Value)
	var v observe.Values
	_ = json.Unmarshal(raw, &v)
	if len(v.Rejected) != 1 || v.Rejected[0] != "sample-color" {
		t.Errorf("values(color).rejected = %v, want the bisected refused field value", v.Rejected)
	}
	if left := s.Objects(); len(left) != 0 {
		t.Errorf("bisection left objects behind: %v", left)
	}
	_ = summary
}

// TestUnit_Run_PutFullReplaceIsObserved drives the full-replace footgun:
// an update clears what its body omits.
func TestUnit_Run_PutFullReplaceIsObserved(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{
		PutClearsOmitted: true,
		ConstantDefaults: map[string]any{"retention": 30},
	})

	p := derivedPlan(t)
	obs, _ := mustRun(t, testOptions(t, s, p, testEnv(), nil))

	if o := wantConfirmed(t, obs, "thing", "", observe.KindUpdateStyle); o.Value != "put-full" {
		t.Errorf("updateStyle = %v, want put-full", o.Value)
	}
}

// TestUnit_Run_RedactionHoldsEndToEnd runs a full audit and then greps
// everything the run produced — observation files, the summary, the debug
// logs — for the bearer token. Nothing may carry it.
func TestUnit_Run_RedactionHoldsEndToEnd(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{
		ClosedEnum: map[string][]string{"mode": {"basic", "advanced"}},
	})

	var logs bytes.Buffer
	obs, summary := mustRun(t, testOptions(t, s, derivedPlan(t), testEnv(), &logs))

	outDir := t.TempDir()
	if err := observe.Write(outDir, obs); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no observation files were written: %v", err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(outDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(raw, []byte(testToken)) {
			t.Fatalf("%s contains the bearer token", e.Name())
		}
	}
	if strings.Contains(logs.String(), testToken) {
		t.Fatal("the debug log contains the bearer token")
	}
	if raw, _ := json.Marshal(summary); bytes.Contains(raw, []byte(testToken)) {
		t.Fatal("the summary contains the bearer token")
	}
}

// TestUnit_Run_MissingEnvVarBlocksTheEntityAndTheRunContinues gives one
// entity a ${VAR} nothing supplies. That entity blocks with a reason
// naming the variable; the other entity is still audited; Run does not
// fail.
func TestUnit_Run_MissingEnvVarBlocksTheEntityAndTheRunContinues(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})
	seeded := s.Seed(map[string]any{"name": "pre-existing"})

	p := thingPlan(resourceSteps(), 60)
	p.Entities = append(p.Entities, plan.EntityPlan{
		Entity:     "gadget",
		AuditShape: "lookupByKey",
		Budget:     plan.Budget{Requests: 10},
		Steps: []plan.Step{{
			Kind: plan.StepRead, Method: "GET", Path: "/things/{thingId}",
			PathValues: map[string]string{"thingId": "${TFPFGEN_TEST_UNSET_VAR}"},
		}},
	})
	_ = seeded

	obs, summary, err := Run(context.Background(), testOptions(t, s, p, testEnv(), nil))
	if err != nil {
		t.Fatalf("a blocked entity must not fail the run: %v", err)
	}
	if got := entityStatus(t, summary, "thing"); got.Outcome != observe.OutcomeConfirmed {
		t.Errorf("thing status = %+v, want audited", got)
	}
	blocked := entityStatus(t, summary, "gadget")
	if blocked.Outcome != observe.OutcomeBlocked || !strings.Contains(blocked.Reason, "TFPFGEN_TEST_UNSET_VAR") {
		t.Errorf("gadget = %+v, want blocked naming the variable", blocked)
	}
	_ = obs
}

// TestUnit_Run_CreatedChainingRecreatesTheParent hands a second entity a
// $created reference to the first, whose lifecycle has already deleted
// its object by the time the second runs. The executor must re-create the
// parent from its recipe, use it, and remove it at the end.
func TestUnit_Run_CreatedChainingRecreatesTheParent(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})

	p := thingPlan(resourceSteps(), 60)
	p.Entities = append(p.Entities, plan.EntityPlan{
		Entity:     "gadget",
		AuditShape: "lookupByKey",
		Parents:    []string{"thing"},
		Budget:     plan.Budget{Requests: 10},
		Steps: []plan.Step{{
			Kind: plan.StepRead, Method: "GET", Path: "/things/{thingId}",
			PathValues: map[string]string{"thingId": "$created:thing"},
		}},
	})

	_, summary := mustRun(t, testOptions(t, s, p, testEnv(), nil))

	if got := entityStatus(t, summary, "gadget"); got.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("gadget = %+v, want audited via a re-created parent", got)
	}
	if summary.ObjectsCreated < 2 {
		t.Errorf("objects created = %d, want at least 2 (the original and the re-creation)", summary.ObjectsCreated)
	}
	if left := s.Objects(); len(left) != 0 {
		t.Errorf("the re-created parent was not removed: %v", left)
	}
}

// TestUnit_Run_BudgetExhaustionRecordsAndMovesOn caps one entity's
// request budget below its needs: its remaining claims come out
// timeoutExhausted, the next entity still runs, and Run reports no error.
func TestUnit_Run_BudgetExhaustionRecordsAndMovesOn(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})
	seeded := s.Seed(map[string]any{"name": "lookup-target"})

	p := thingPlan(resourceSteps(), 2)
	p.Entities = append(p.Entities, plan.EntityPlan{
		Entity:     "gadget",
		AuditShape: "lookupByKey",
		Budget:     plan.Budget{Requests: 10},
		Steps: []plan.Step{{
			Kind: plan.StepRead, Method: "GET", Path: "/things/{thingId}",
			PathValues: map[string]string{"thingId": seeded},
		}},
	})

	obs, summary, err := Run(context.Background(), testOptions(t, s, p, testEnv(), nil))
	if err != nil {
		t.Fatalf("budget exhaustion must not fail the run: %v", err)
	}
	exhausted := entityStatus(t, summary, "thing")
	if exhausted.Outcome != observe.OutcomeTimeoutExhausted {
		t.Fatalf("thing = %+v, want timeoutExhausted", exhausted)
	}
	o := findObs(obs, "thing", "", observe.KindDeleteNotFoundOK)
	if o == nil || o.Outcome != observe.OutcomeTimeoutExhausted {
		t.Errorf("deleteNotFoundOK = %+v, want a timeoutExhausted record", o)
	}
	if got := entityStatus(t, summary, "gadget"); got.Outcome != observe.OutcomeConfirmed {
		t.Errorf("gadget = %+v, want audited after the exhausted entity", got)
	}
	// The end-of-run cleanup still removed the object the exhausted
	// entity created, spending outside the budgets.
	for id := range s.Objects() {
		if id != seeded {
			t.Errorf("object %s survived the boundary cleanup", id)
		}
	}
}

// TestUnit_Run_CancelledContextStillReportsAnError covers the one
// run-level failure Run does surface.
func TestUnit_Run_CancelledContextStillReportsAnError(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Run(ctx, testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err == nil {
		t.Fatal("a cancelled context must surface as an error")
	}
}
