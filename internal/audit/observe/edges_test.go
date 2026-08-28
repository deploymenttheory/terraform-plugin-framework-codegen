package observe

import (
	"os"
	"path/filepath"
	"testing"
)

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // test temp path
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return raw
}

// TestUnit_Observe_EdgeKindsRefuseMalformedValues pins the value shapes the
// inferred edge kinds accept and refuse.
func TestUnit_Observe_EdgeKindsRefuseMalformedValues(t *testing.T) {
	cases := []struct {
		name      string
		kind      Kind
		attribute string
		value     any
		cond      *Condition
		ok        bool
	}{
		{"validWhen non-bool", KindValidWhen, "f", "yes", &Condition{Attribute: "k", Equals: "v"}, false},
		{"validWhen no condition", KindValidWhen, "f", true, nil, false},
		{"validConfiguration empty list", KindValidConfiguration, "kind", []string{}, nil, false},
		{"validConfiguration non-strings", KindValidConfiguration, "kind", []any{1, 2}, nil, false},
		{"validConfiguration not a list", KindValidConfiguration, "kind", "a", nil, false},
		{"dependsOn empty", KindDependsOn, "x", "", nil, false},
		{"dependsOn non-string", KindDependsOn, "x", 3, nil, false},
		{"mutuallyExclusive not a list", KindMutuallyExclusive, "", "a", nil, false},
		{"listShape bad envelope", KindListWrapper, "", map[string]any{"envelope": "weird", "pagination": "none"}, nil, false},
		{"listShape bad pagination", KindListWrapper, "", map[string]any{"envelope": "bare", "pagination": "spiral"}, nil, false},
		{"listShape unknown key", KindListWrapper, "", map[string]any{"envelope": "bare", "pagination": "none", "junk": 1}, nil, false},
		{"listShape wrong type", KindListWrapper, "", 42, nil, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			o := valid()
			o.Kind, o.Attribute, o.Value, o.Condition = testCase.kind, testCase.attribute, testCase.value, testCase.cond
			o.ID = ComputeID(o.Entity, o.Attribute, o.Kind, o.Condition)
			err := o.Validate()
			if testCase.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !testCase.ok && err == nil {
				t.Fatalf("Validate() = nil, want an error for %s", testCase.name)
			}
		})
	}
}

// TestUnit_Observe_ProvenanceIsValidated: a known provenance is accepted, an
// unknown one refused, and an empty one (the scalar kinds) accepted.
func TestUnit_Observe_ProvenanceIsValidated(t *testing.T) {
	for _, p := range []Provenance{ProvenanceStructural, ProvenanceProse, ProvenanceDerived, ""} {
		o := valid()
		o.Provenance = p
		if err := o.Validate(); err != nil {
			t.Errorf("Validate() with provenance %q = %v, want nil", p, err)
		}
	}
	o := valid()
	o.Provenance = "vibes"
	if o.Validate() == nil {
		t.Error("an unknown provenance must be refused")
	}
}

// TestUnit_Observe_EdgeKindsRoundTrip writes and reads the inferred edge kinds,
// proving their values survive the deterministic file layout unchanged and
// their IDs stay stable.
func TestUnit_Observe_EdgeKindsRoundTrip(t *testing.T) {
	closedID := ComputeID("monitor", "target_host", KindValidWhen, &Condition{Attribute: "kind", Equals: "ping"})
	in := []Observation{
		{Entity: "monitor", Attribute: "kind", Kind: KindValidConfiguration,
			Value: []string{"dns", "ping", "web"}, Provenance: ProvenanceDerived, Outcome: OutcomeConfirmed},
		{Entity: "monitor", Attribute: "target_host", Kind: KindValidWhen,
			Value: true, Condition: &Condition{Attribute: "kind", Equals: "ping"},
			Provenance: ProvenanceDerived, Outcome: OutcomeConfirmed},
		{Entity: "monitor", Attribute: "dnssec", Kind: KindDependsOn,
			Value: "domain", Provenance: ProvenanceStructural, Outcome: OutcomeConfirmed},
		{Entity: "monitor", Attribute: "", Kind: KindMutuallyExclusive,
			Value: []string{"a", "b"}, Provenance: ProvenanceProse, Outcome: OutcomeConfirmed},
		{Entity: "monitor", Attribute: "", Kind: KindListWrapper,
			Value:      ListWrapper{Wrapped: true, Key: "monitors"},
			Provenance: ProvenanceDerived, Outcome: OutcomeConfirmed},
	}
	dir := t.TempDir()
	if err := Write(dir, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("round trip changed the count: %d -> %d", len(in), len(out))
	}
	var vw *Observation
	for i := range out {
		if out[i].Kind == KindValidWhen {
			vw = &out[i]
		}
	}
	if vw == nil || vw.ID != closedID {
		t.Fatalf("validWhen id drifted through the round trip: %+v (want %s)", vw, closedID)
	}

	// A second write of the read-back observations must be byte-identical.
	dir2 := t.TempDir()
	if err := Write(dir2, out); err != nil {
		t.Fatalf("second Write: %v", err)
	}
	a := mustRead(t, filepath.Join(dir, "monitor"+FileSuffix))
	b := mustRead(t, filepath.Join(dir2, "monitor"+FileSuffix))
	if string(a) != string(b) {
		t.Fatal("the file layout is not a fixed point for the edge kinds")
	}
}
