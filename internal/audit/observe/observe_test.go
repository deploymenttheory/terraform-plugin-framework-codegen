package observe

import (
	"strings"
	"testing"
	"time"
)

func TestUnit_Observe_IDIsStableAndDiscriminating(t *testing.T) {
	base := ComputeID("tag", "color", KindWritable, nil)
	if base != ComputeID("tag", "color", KindWritable, nil) {
		t.Fatal("the same identity hashed twice differs")
	}
	if len(base) != 16 {
		t.Fatalf("ID length = %d, want 16", len(base))
	}

	// Every identity input must move the hash.
	others := []string{
		ComputeID("project", "color", KindWritable, nil),
		ComputeID("tag", "name", KindWritable, nil),
		ComputeID("tag", "color", KindImmutable, nil),
		ComputeID("tag", "color", KindWritable, &Condition{Attribute: "mode", Equals: "advanced"}),
		ComputeID("tag", "", KindWritable, nil),
	}
	seen := map[string]bool{base: true}
	for i, id := range others {
		if seen[id] {
			t.Errorf("identity variant %d collides", i)
		}
		seen[id] = true
	}

	// Structurally equal conditions hash identically however their equals
	// value was built: canonical JSON sorts map keys.
	a := ComputeID("tag", "color", KindWritable, &Condition{Attribute: "m", Equals: map[string]any{"x": 1, "y": 2}})
	b := ComputeID("tag", "color", KindWritable, &Condition{Attribute: "m", Equals: map[string]any{"y": 2, "x": 1}})
	if a != b {
		t.Error("structurally equal conditions hash differently")
	}

	// An unencodable equals value must not collide with the unconditional
	// identity.
	if bad := ComputeID("tag", "color", KindWritable, &Condition{Attribute: "m", Equals: func() {}}); bad == base {
		t.Error("unencodable condition collides with no condition")
	}
}

// valid returns a well-formed observation to mutate per case.
func valid() Observation {
	return Observation{
		Entity:  "tag",
		Kind:    KindDeleteNotFoundOK,
		Value:   true,
		Outcome: OutcomeConfirmed,
		RunID:   "r1",
		Excerpts: []Excerpt{{
			Method: "DELETE", PathTemplate: "/tags/{tagId}", Status: 404,
			ResponseFragment: []byte(`{"error":"not found"}`),
		}},
		SpecHash:   "abc",
		ObservedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestUnit_Observe_ValidateRefusesMalformedObservations(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Observation)
		want string
	}{
		{"bad entity", func(o *Observation) { o.Entity = "../etc" }, "not a valid entity key"},
		{"empty entity", func(o *Observation) { o.Entity = "" }, "not a valid entity key"},
		{"unknown kind", func(o *Observation) { o.Kind = "sideEffect" }, "unknown observation kind"},
		{"unknown outcome", func(o *Observation) { o.Outcome = "maybe" }, "unknown outcome"},
		{"condition without attribute", func(o *Observation) { o.Condition = &Condition{Equals: "x"} }, "constrains nothing"},
		{"requiredWhen without condition", func(o *Observation) {
			o.Kind, o.Attribute = KindRequiredWhen, "query"
		}, "needs the condition"},
		{"drifted id", func(o *Observation) { o.ID = "0000000000000000" }, "does not match the computed"},
		{"bool kind with string", func(o *Observation) { o.Value = "yes" }, "must be a bool"},
		{"updateStyle off the closed set", func(o *Observation) {
			o.Kind, o.Value = KindUpdateStyle, "merge"
		}, "patch-merge"},
		{"updateStyle non-string", func(o *Observation) {
			o.Kind, o.Value = KindUpdateStyle, 3
		}, "patch-merge"},
		{"readAfterWrite non-duration", func(o *Observation) {
			o.Kind, o.Value = KindReadAfterWrite, "fast"
		}, "not a non-negative duration"},
		{"readAfterWrite negative", func(o *Observation) {
			o.Kind, o.Value = KindReadAfterWrite, "-2s"
		}, "not a non-negative duration"},
		{"readAfterWrite non-string", func(o *Observation) {
			o.Kind, o.Value = KindReadAfterWrite, 2
		}, "duration string"},
		{"serverDefault with a slice", func(o *Observation) {
			o.Kind, o.Value = KindServerDefault, []string{"x"}
		}, "must be a scalar"},
		{"normalisation non-string", func(o *Observation) {
			o.Kind, o.Value = KindNormalisation, true
		}, "normalised string"},
		{"values with unknown key", func(o *Observation) {
			o.Kind, o.Value = KindValues, map[string]any{"allowed": []any{"a"}}
		}, "unknown values key"},
		{"values wrong type", func(o *Observation) { o.Kind, o.Value = KindValues, "open" }, "values record"},
		{"values nil pointer", func(o *Observation) { o.Kind, o.Value = KindValues, (*Values)(nil) }, "values record"},
		{"excerpt without method", func(o *Observation) { o.Excerpts[0].Method = "" }, "no method"},
		{"excerpt invalid JSON", func(o *Observation) {
			o.Excerpts[0].ResponseFragment = []byte(`{"truncated`)
		}, "not valid JSON"},
		{"excerpt over the ceiling", func(o *Observation) {
			o.Excerpts[0].ResponseFragment = []byte(`"` + strings.Repeat("x", MaxFragmentBytes) + `"`)
		}, "over the 2048-byte ceiling"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := valid()
			tc.mut(&o)
			err := o.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestUnit_Observe_ValidateAcceptsEveryKindsValueShape(t *testing.T) {
	closed := true
	cases := []struct {
		kind      Kind
		attribute string
		value     any
		cond      *Condition
	}{
		{KindWritable, "name", true, nil},
		{KindImmutable, "type", false, nil},
		{KindRequiredByAPI, "name", true, nil},
		{KindRequiredWhen, "query", true, &Condition{Attribute: "type", Equals: "dynamic"}},
		{KindServerDefault, "retention", 30, nil},
		{KindServerDefault, "mode", "basic", nil},
		{KindDerivedDefault, "color", true, nil},
		{KindNormalisation, "email", "user@example.invalid", nil},
		{KindIgnoredOnUpdate, "notes", true, nil},
		{KindServerForced, "region", true, nil},
		{KindVolatile, "updated_at", true, nil},
		{KindValues, "mode", Values{Accepted: []string{"basic"}, Rejected: []string{"legacy"}, Closed: &closed}, nil},
		{KindValues, "mode", map[string]any{"accepted": []any{"basic"}, "closed": true}, nil},
		{KindUpdateStyle, "", "patch-merge", nil},
		{KindDeleteNotFoundOK, "", true, nil},
		{KindReadAfterWrite, "", "2.5s", nil},
		{KindValidWhen, "target_host", true, &Condition{Attribute: "kind", Equals: "ping"}},
		{KindValidConfiguration, "kind", []string{"dns", "ping", "web"}, nil},
		{KindValidConfiguration, "kind", []any{"a", "b"}, nil},
		{KindDependsOn, "dnssec", "domain", nil},
		{KindMutuallyExclusive, "", []string{"a", "b"}, nil},
		{KindListResponseShape, "", ListResponseShape{Envelope: "wrapped", Key: "items", Pagination: "cursor"}, nil},
		{KindListResponseShape, "", &ListResponseShape{Envelope: "bare", Pagination: "none"}, nil},
		{KindListResponseShape, "", map[string]any{"envelope": "wrapped", "key": "data", "pagination": "offset"}, nil},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			o := valid()
			o.Kind, o.Attribute, o.Value, o.Condition = tc.kind, tc.attribute, tc.value, tc.cond
			if err := o.Validate(); err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestUnit_Observe_NonObservedOutcomesCarryNoValueCheck(t *testing.T) {
	for _, outcome := range []Outcome{OutcomeInconclusive, OutcomeBlocked, OutcomeTimeoutExhausted} {
		o := valid()
		o.Outcome, o.Value = outcome, nil
		if err := o.Validate(); err != nil {
			t.Fatalf("outcome %s with nil value: %v", outcome, err)
		}
	}
}

func TestUnit_Observe_SortIsByIdentityNotDiscoveryOrder(t *testing.T) {
	obs := []Observation{
		{Entity: "tag", Attribute: "color", Kind: KindWritable, Condition: &Condition{Attribute: "type", Equals: "static"}},
		{Entity: "tag", Attribute: "color", Kind: KindWritable, Condition: &Condition{Attribute: "type", Equals: "dynamic"}},
		{Entity: "tag", Attribute: "color", Kind: KindImmutable},
		{Entity: "project", Attribute: "name", Kind: KindWritable},
		{Entity: "tag", Attribute: "", Kind: KindUpdateStyle},
		{Entity: "tag", Attribute: "color", Kind: KindWritable},
	}
	Sort(obs)

	type ident struct {
		entity, attribute string
		kind              Kind
	}
	got := make([]ident, len(obs))
	for i, o := range obs {
		got[i] = ident{o.Entity, o.Attribute, o.Kind}
	}
	want := []ident{
		{"project", "name", KindWritable},
		{"tag", "", KindUpdateStyle},
		{"tag", "color", KindImmutable},
		{"tag", "color", KindWritable}, // unconditional before conditional: "" sorts first
		{"tag", "color", KindWritable}, // type=dynamic
		{"tag", "color", KindWritable}, // type=static
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if obs[4].Condition.Equals != "dynamic" || obs[5].Condition.Equals != "static" {
		t.Error("conditional observations are not ordered by condition key")
	}
}
