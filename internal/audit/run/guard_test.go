package run

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/quirkserver"
)

// TestUnit_Guard_RefusesWithoutTheSandboxAcknowledgement is the first
// tier: no TFPFGEN_SANDBOX_OK, no request of any kind for a mutating
// plan.
func TestUnit_Guard_RefusesWithoutTheSandboxAcknowledgement(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})

	env := testEnv()
	delete(env, SandboxEnv)
	_, _, err := Run(context.Background(), testOptions(t, s, thingPlan(resourceSteps(), 60), env, nil))
	if err == nil || !strings.Contains(err.Error(), SandboxEnv) {
		t.Fatalf("err = %v, want a refusal naming %s", err, SandboxEnv)
	}
	if s.Requests() != 0 {
		t.Fatalf("the server saw %d request(s) from a refused run", s.Requests())
	}
}

// TestUnit_Guard_ReadOnlyPlanNeedsNoSandboxAcknowledgement: a plan that
// never mutates — a lookup-only audit — runs without the acknowledgement.
func TestUnit_Guard_ReadOnlyPlanNeedsNoSandboxAcknowledgement(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	seeded := s.Seed(map[string]any{"name": "target"})

	p := thingPlan(nil, 10)
	p.Entities[0].Role = "lookup"
	p.Entities[0].Steps = []plan.Step{{
		Kind: plan.StepRead, Method: "GET", Path: "/things/{thingId}",
		PathValues: map[string]string{"thingId": seeded},
	}}

	env := testEnv()
	delete(env, SandboxEnv)
	opts := testOptions(t, s, p, env, nil)
	opts.WorkDir = ""
	_, sum := mustRun(t, opts)
	if got := entityStatus(t, sum, "thing"); got.Status != StatusAudited {
		t.Fatalf("lookup entity = %+v, want audited without the sandbox variable", got)
	}
}

// TestUnit_Guard_SharedTenantRefusalBlocksMutation seeds more foreign
// objects than the object ceiling: the pre-flight must refuse the
// entity's mutating steps, record them blocked, and leave the tenant
// untouched — unless AllowSharedTenant.
func TestUnit_Guard_SharedTenantRefusalBlocksMutation(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	for range 3 {
		s.Seed(map[string]any{"name": "somebody-elses-object"})
	}

	p := thingPlan(resourceSteps(), 60)
	opts := testOptions(t, s, p, testEnv(), nil)
	opts.Budgets = Budgets{Objects: 2}

	obs, sum, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("a shared-tenant refusal blocks the entity, not the run: %v", err)
	}
	blocked := entityStatus(t, sum, "thing")
	if blocked.Status != StatusBlocked || !strings.Contains(blocked.Reason, "--allow-shared-tenant") {
		t.Fatalf("thing = %+v, want blocked pointing at the flag", blocked)
	}
	if o := findObs(obs, "thing", "", observe.KindDeleteNotFoundOK); o == nil || o.Outcome != observe.OutcomeBlocked {
		t.Errorf("deleteNotFoundOK = %+v, want a blocked record", o)
	}
	if len(s.Objects()) != 3 {
		t.Errorf("the tenant changed: %v", s.Objects())
	}
}

// TestUnit_Guard_AllowSharedTenantProceeds is the explicit override.
func TestUnit_Guard_AllowSharedTenantProceeds(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	for range 3 {
		s.Seed(map[string]any{"name": "somebody-elses-object"})
	}

	opts := testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil)
	opts.Budgets = Budgets{Objects: 2}
	opts.AllowSharedTenant = true

	_, sum := mustRun(t, opts)
	if got := entityStatus(t, sum, "thing"); got.Status != StatusAudited {
		t.Fatalf("thing = %+v, want audited under --allow-shared-tenant", got)
	}
	if len(s.Objects()) != 3 {
		t.Errorf("foreign objects were touched: %v", s.Objects())
	}
}

// TestUnit_Guard_HostAllowlistRefusesAForeignHost: the mutating-request
// guard admits only the host the base URL names.
func TestUnit_Guard_HostAllowlistRefusesAForeignHost(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	r, err := newRunner(testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://somewhere-else.invalid/things")
	if err := r.guardMutation(u); err == nil || !strings.Contains(err.Error(), "somewhere-else.invalid") {
		t.Fatalf("guardMutation = %v, want a refusal naming the foreign host", err)
	}
	own, _ := url.Parse(s.BaseURL() + "/things")
	if err := r.guardMutation(own); err != nil {
		t.Fatalf("guardMutation refused the base host: %v", err)
	}
}

// TestUnit_Guard_NamePrefixBounds: a prefix too short or missing the
// toolkit token cannot bound a cleanup pass and is refused up front.
func TestUnit_Guard_NamePrefixBounds(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})

	for _, tc := range []struct{ prefix, want string }{
		{"tf", "shorter"},
		{"mycompanyprefix", "tfpfgen"},
	} {
		opts := testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil)
		opts.NamePrefix = tc.prefix
		if _, _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("prefix %q: err = %v, want a refusal mentioning %q", tc.prefix, err, tc.want)
		}
	}
}

// TestUnit_Guard_MutatingRunNeedsAWorkDir: no durable ledger, no
// mutating run.
func TestUnit_Guard_MutatingRunNeedsAWorkDir(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	opts := testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil)
	opts.WorkDir = ""
	if _, _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "ledger") {
		t.Fatalf("err = %v, want a refusal about the ledger", err)
	}
}

// TestUnit_Guard_OptionValidation exercises the remaining refusals.
func TestUnit_Guard_OptionValidation(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
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
