package run

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/quirkserver"
)

func TestUnit_Client_IdentifierOf(t *testing.T) {
	t.Parallel()
	cases := []struct {
		obj  map[string]any
		want string
	}{
		{map[string]any{"id": "abc"}, "abc"},
		{map[string]any{"id": float64(42)}, "42"},
		{map[string]any{"thing_id": "t1"}, "t1"},
		{map[string]any{"thingId": "t2"}, "t2"},
		{map[string]any{"name": "no id here"}, ""},
		{map[string]any{"id": []any{"not scalar"}}, ""},
	}
	for _, testCase := range cases {
		if got := identifierOf(testCase.obj); got != testCase.want {
			t.Errorf("identifierOf(%v) = %q, want %q", testCase.obj, got, testCase.want)
		}
	}
	if got := scalarString(float64(7.5)); got != "7.5" {
		t.Errorf("scalarString(7.5) = %q", got)
	}
}

func TestUnit_Client_ItemsDiscoversTheEnvelope(t *testing.T) {
	t.Parallel()
	if got := items([]byte(`[{"id":"1"}]`)); len(got) != 1 {
		t.Errorf("bare array: %v", got)
	}
	if got := items([]byte(`{"things":[{"id":"1"},{"id":"2"}]}`)); len(got) != 2 {
		t.Errorf("single envelope key: %v", got)
	}
	if got := items([]byte(`{"meta":{"page":1},"items":[{"id":"1"}],"aaa":[1,2,3]}`)); len(got) != 1 {
		t.Errorf("the preferred items key must win over a sorted-first array: %v", got)
	}
	if got := items([]byte(`{"count":3}`)); got != nil {
		t.Errorf("no array anywhere: %v", got)
	}
	if got := items([]byte(`not json`)); got != nil {
		t.Errorf("non-JSON: %v", got)
	}
}

func TestUnit_Client_FragmentBounds(t *testing.T) {
	t.Parallel()
	if fragment(nil) != nil {
		t.Error("empty body must fragment to nothing")
	}
	if frag := fragment([]byte("<html>")); !strings.Contains(string(frag), "not JSON") {
		t.Errorf("non-JSON body: %s", frag)
	}
	big, _ := json.Marshal(strings.Repeat("x", observe.MaxFragmentBytes+10))
	if frag := fragment(big); !strings.Contains(string(frag), "over") {
		t.Errorf("oversized body: %.60s", frag)
	}
	if frag := fragment([]byte(`{"ok":true}`)); string(frag) != `{"ok":true}` {
		t.Errorf("small JSON must pass through: %s", frag)
	}
}

func TestUnit_Client_ErrRedacted(t *testing.T) {
	t.Parallel()
	err := errRedacted(errors.New(`Get "https://h/x?token=verysecretvalue": refused`), []string{"verysecretvalue", ""})
	if strings.Contains(err.Error(), "verysecretvalue") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("errRedacted leaked: %v", err)
	}
}

func TestUnit_Client_MentionsIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	res := &httpResult{body: []byte(`{"detail":"Field cannot change: ThingName"}`)}
	if !res.mentions("thingname") || res.mentions("other") || res.mentions("") {
		t.Error("mentions() does not match case-insensitively on the attribute only")
	}
}

func TestUnit_Client_SelfParamAndPartialPaths(t *testing.T) {
	t.Parallel()
	if got := selfParameter("/things/{thingId}"); got != "thingId" {
		t.Errorf("selfParam = %q", got)
	}
	if got := selfParameter("/things"); got != "" {
		t.Errorf("selfParam of a collection = %q", got)
	}

	s := quirkserver.New(t, quirkserver.Quirks{})
	r, err := newRunner(testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	rec := &entityLifecycle{
		entity:   "tag",
		itemPath: "/projects/{projectId}/tags/{tagId}",
		itemValues: map[string]string{
			"projectId": "${TFPFGEN_TEST_PARENT_ID}",
			"tagId":     "$created:tag",
		},
	}
	got, err := r.partialItemPath(context.Background(), nil, rec)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/projects/seeded-parent/tags/{tagId}" {
		t.Fatalf("partialItemPath = %q, want the parent substituted and the self parameter kept", got)
	}

	values := itemValuesFor(rec, "42")
	if values["tagId"] != "42" || values["projectId"] != "${TFPFGEN_TEST_PARENT_ID}" {
		t.Fatalf("itemValuesFor = %v", values)
	}
}

// TestUnit_Client_RunBudgetsExhaustRunWide covers the run-wide request
// and duration ceilings: everything left records timeoutExhausted, Run
// reports no error, and cleanup still runs.
func TestUnit_Client_RunBudgetsExhaustRunWide(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	opts := testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil)
	opts.Budgets = Budgets{Requests: 3}
	_, summary, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := entityStatus(t, summary, "thing")
	if got.Outcome != observe.OutcomeTimeoutExhausted || !strings.Contains(got.Reason, "request budget") {
		t.Fatalf("thing = %+v, want the run request budget named", got)
	}
	if len(s.Objects()) != 0 {
		t.Errorf("cleanup did not run after exhaustion: %v", s.Objects())
	}

	opts2 := testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil)
	opts2.Budgets = Budgets{Duration: time.Nanosecond}
	_, sum2, err := Run(context.Background(), opts2)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := entityStatus(t, sum2, "thing"); got.Outcome != observe.OutcomeTimeoutExhausted || !strings.Contains(got.Reason, "time budget") {
		t.Fatalf("thing = %+v, want the time budget named", got)
	}
}

// TestUnit_Client_ObjectBudgetStopsCreates: with a live-object ceiling of
// one, the minimal object occupies the only slot and the extra creates
// exhaust rather than run.
func TestUnit_Client_ObjectBudgetStopsCreates(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	item := map[string]string{"thingId": "$created:thing"}
	steps := []plan.Step{
		{Kind: plan.StepCreateMinimal, Method: "POST", Path: "/things",
			Body: map[string]any{"name": "tfpfgen-<runid>-thing-name"}},
		// The maximal create needs a second live slot that does not exist.
		{Kind: plan.StepCreateMaximal, Method: "POST", Path: "/things",
			Body: map[string]any{"name": "tfpfgen-<runid>-thing-name-max", "color": "red"}},
		{Kind: plan.StepDeleteWithConfirmation, Method: "DELETE", Path: "/things/{thingId}", PathValues: item},
		{Kind: plan.StepCleanupDelete, Method: "DELETE", Path: "/things/{thingId}", PathValues: item},
	}
	opts := testOptions(t, s, thingPlan(steps, 60), testEnv(), nil)
	opts.Budgets = Budgets{Objects: 1}
	_, summary, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := entityStatus(t, summary, "thing")
	if got.Outcome != observe.OutcomeTimeoutExhausted || !strings.Contains(got.Reason, "live-object") {
		t.Fatalf("thing = %+v, want the live-object budget named", got)
	}
	if len(s.Objects()) != 0 {
		t.Errorf("cleanup left objects: %v", s.Objects())
	}
}

func TestUnit_Client_RequestTimeoutDefaultAndOverride(t *testing.T) {
	t.Parallel()
	if (Options{}).RequestTimeoutOrDefault() != defaultRequestTimeout {
		t.Error("zero timeout must default")
	}
	if (Options{RequestTimeout: time.Second}).RequestTimeoutOrDefault() != time.Second {
		t.Error("an explicit timeout must win")
	}
	var b blockedError
	var g budgetError
	b.reason, g.reason = "x", "y"
	if b.Error() != "x" || g.Error() != "y" {
		t.Error("error strings are their reasons")
	}
}
