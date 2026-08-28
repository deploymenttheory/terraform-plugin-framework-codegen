package run

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/testapiserver"
)

// TestUnit_Guard_ReadOnlyPlanNeedsNoRunsDir: a plan that never mutates —
// a lookup-only audit — runs without a runs directory, because nothing it
// does needs an activity ledger.
func TestUnit_Guard_ReadOnlyPlanNeedsNoRunsDir(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})
	seeded := s.Seed(map[string]any{"name": "target"})

	p := thingPlan(nil, 10)
	p.Entities[0].AuditShape = "lookupByKey"
	p.Entities[0].Steps = []plan.Step{{
		Kind: plan.StepRead, Method: "GET", Path: "/things/{thingId}",
		PathValues: map[string]string{"thingId": seeded},
	}}

	opts := testOptions(t, s, p, testEnv(), nil)
	opts.RunsDir = ""
	_, summary := mustRun(t, opts)
	if got := entityStatus(t, summary, "thing"); got.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("lookup entity = %+v, want audited without a runs directory", got)
	}
}

// TestUnit_Guard_SharedTenantRefusalBlocksMutation seeds more foreign
// objects than the object ceiling: the pre-flight must refuse the
// entity's mutating steps, record them blocked, and leave the tenant
// untouched — unless ForceAPIAudit.
func TestUnit_Guard_SharedTenantRefusalBlocksMutation(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})
	for range 3 {
		s.Seed(map[string]any{"name": "somebody-elses-object"})
	}

	p := thingPlan(resourceSteps(), 60)
	opts := testOptions(t, s, p, testEnv(), nil)
	opts.Budgets = Budgets{Objects: 2}

	obs, summary, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("a shared-tenant refusal blocks the entity, not the run: %v", err)
	}
	blocked := entityStatus(t, summary, "thing")
	if blocked.Outcome != observe.OutcomeBlocked || !strings.Contains(blocked.Reason, "--force-api-audit") {
		t.Fatalf("thing = %+v, want blocked pointing at the flag", blocked)
	}
	if o := findObs(obs, "thing", "", observe.KindDeleteNotFoundOK); o == nil || o.Outcome != observe.OutcomeBlocked {
		t.Errorf("deleteNotFoundOK = %+v, want a blocked record", o)
	}
	if len(s.Objects()) != 3 {
		t.Errorf("the tenant changed: %v", s.Objects())
	}
}

// TestUnit_Guard_ForceAPIAuditProceeds is the explicit override.
func TestUnit_Guard_ForceAPIAuditProceeds(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})
	for range 3 {
		s.Seed(map[string]any{"name": "somebody-elses-object"})
	}

	opts := testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil)
	opts.Budgets = Budgets{Objects: 2}
	opts.ForceAPIAudit = true

	_, summary := mustRun(t, opts)
	if got := entityStatus(t, summary, "thing"); got.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("thing = %+v, want audited under --force-api-audit", got)
	}
	if len(s.Objects()) != 3 {
		t.Errorf("foreign objects were touched: %v", s.Objects())
	}
}

// TestUnit_Guard_HostAllowlistRefusesAForeignHost: the mutating-request
// guard admits only the host the base URL names.
func TestUnit_Guard_HostAllowlistRefusesAForeignHost(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})
	r, err := newRunner(testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://somewhere-else.invalid/things")
	if err := r.refuseForeignHostWrite(u); err == nil || !strings.Contains(err.Error(), "somewhere-else.invalid") {
		t.Fatalf("refuseForeignHostWrite = %v, want a refusal naming the foreign host", err)
	}
	own, _ := url.Parse(s.BaseURL() + "/things")
	if err := r.refuseForeignHostWrite(own); err != nil {
		t.Fatalf("refuseForeignHostWrite refused the base host: %v", err)
	}
}

// TestUnit_Guard_NamePrefixBounds: a prefix too short or missing the
// toolkit token cannot bound a cleanup pass and is refused up front.
func TestUnit_Guard_NamePrefixBounds(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})

	for _, testCase := range []struct{ prefix, want string }{
		{"tf", "shorter"},
		{"mycompanyprefix", "tfpfgen"},
	} {
		opts := testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil)
		opts.NamePrefix = testCase.prefix
		if _, _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Errorf("prefix %q: err = %v, want a refusal mentioning %q", testCase.prefix, err, testCase.want)
		}
	}
}

// TestUnit_Guard_MutatingRunNeedsARunsDir: no durable activity ledger, no
// mutating run.
func TestUnit_Guard_MutatingRunNeedsARunsDir(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})
	opts := testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil)
	opts.RunsDir = ""
	if _, _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "ledger") {
		t.Fatalf("err = %v, want a refusal about the ledger", err)
	}
}

// TestUnit_Guard_OptionValidation exercises the remaining refusals.
func TestUnit_Guard_OptionValidation(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})
	base := func() Options { return testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil) }

	opts := base()
	opts.Plan = nil
	if _, _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "no plan") {
		t.Errorf("nil plan: %v", err)
	}

	opts = base()
	opts.BaseURL = "not a url"
	if _, _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "absolute URL") {
		t.Errorf("bad base URL: %v", err)
	}

	opts = base()
	env := testEnv()
	delete(env, config.SecretToken)
	opts.Lookup = lookupOf(env)
	if _, _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), config.SecretToken) {
		t.Errorf("missing secret: %v", err)
	}
}
