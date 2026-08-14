package run

import (
	"encoding/json"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/infer"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/quirkserver"
)

// findWhenObs locates a value-conditional observation by its gate value.
func findWhenObs(obs []observe.Observation, entity, attr string, kind observe.Kind, gateVal string) *observe.Observation {
	for i := range obs {
		o := &obs[i]
		if o.Entity == entity && o.Attribute == attr && o.Kind == kind &&
			o.Condition != nil && o.Condition.Equals == gateVal {
			return o
		}
	}
	return nil
}

// TestUnit_Infer_MonitorEdgesFromAStrategyRun drives the whole chain against
// the quirk server: the executor probes the multi-variant monitor, the
// triangulating inference reads all of the evidence at once, and the
// conditional edges no single probe could justify come out confirmed — the
// discriminator configuration and the per-kind validWhen edges — while the
// list shape is read from the live response, not the document.
func TestUnit_Infer_MonitorEdgesFromAStrategyRun(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	obs, sum := mustRun(t, strategyOptions(t, s, nil))

	vc := findObs(obs, "monitor", "kind", observe.KindValidConfiguration)
	if vc == nil || vc.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("validConfiguration(monitor.kind) = %+v, want confirmed", vc)
	}
	raw, _ := json.Marshal(vc.Value)
	var values []string
	_ = json.Unmarshal(raw, &values)
	if len(values) < 2 {
		t.Errorf("validConfiguration lists %v, want at least two gate values", values)
	}

	// Each field the kind gates is valid only under its own value — asserted
	// from a positive acceptance and reproduced negative removals.
	for _, tc := range []struct{ field, gate string }{
		{"target_host", "ping"}, {"domain", "dns"}, {"dnssec", "dns"},
	} {
		o := findWhenObs(obs, "monitor", tc.field, observe.KindValidWhen, tc.gate)
		if o == nil || o.Outcome != observe.OutcomeConfirmed || o.Value != true {
			t.Errorf("validWhen(monitor.%s, kind=%s) = %+v, want confirmed true", tc.field, tc.gate, o)
		}
	}

	// dnssec requires domain: the value-conditional co-requirement, surfaced
	// here as the validWhen(dnssec, kind=dns) above and (from the perValue
	// creates) requiredWhen(domain, kind=dns).
	if o := findWhenObs(obs, "monitor", "domain", observe.KindRequiredWhen, "dns"); o == nil {
		t.Errorf("requiredWhen(monitor.domain, kind=dns) not found; the dns co-requirement was missed")
	}

	// interval: optional in the document, required by every kind — the add was
	// forced with no gate, so it is unconditional.
	if o := findObs(obs, "monitor", "interval", observe.KindRequiredByAPI); o == nil || o.Value != true {
		t.Errorf("requiredByAPI(monitor.interval) = %+v, want true", o)
	}

	// The list response is wrapped under "monitors", learned from the body.
	ls := findObs(obs, "monitor", "", observe.KindListResponseShape)
	if ls == nil || ls.Outcome != observe.OutcomeConfirmed {
		t.Fatalf("listResponseShape(monitor) = %+v, want confirmed", ls)
	}
	shape, _ := json.Marshal(ls.Value)
	if string(shape) != `{"envelope":"wrapped","key":"monitors","pagination":"none"}` {
		t.Errorf("listResponseShape(monitor) = %s, want wrapped under monitors", shape)
	}

	if sum.EdgesConfirmed < 1 {
		t.Errorf("summary reports %d confirmed edges, want at least one", sum.EdgesConfirmed)
	}

	// Every inferred observation must be committable.
	for i := range obs {
		if err := obs[i].Validate(); err != nil {
			t.Fatalf("run produced an invalid observation: %v (%+v)", err, obs[i])
		}
	}
}

// TestUnit_Infer_AssignmentHasNoSpuriousEdges: a flat, gate-less resource must
// yield no conditional edges however much the executor had to adjust its body
// to borrow a real reference.
func TestUnit_Infer_AssignmentHasNoSpuriousEdges(t *testing.T) {
	t.Parallel()
	s := quirkserver.New(t, quirkserver.Quirks{})
	obs, _ := mustRun(t, strategyOptions(t, s, nil))

	for i := range obs {
		o := &obs[i]
		if o.Entity == "assignment" && infer.EdgeKinds[o.Kind] {
			t.Errorf("assignment grew a spurious edge: %s.%s = %+v", o.Attribute, o.Kind, o)
		}
	}
}
