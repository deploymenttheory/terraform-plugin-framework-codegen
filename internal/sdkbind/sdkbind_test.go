package sdkbind

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// pilotModule is the module whose go.mod pins the SDK these tests check against.
const pilotModule = "../../pilot/thousandeyes"

// res finds a resource by key. The committed blueprint's resources are sorted by key,
// so positional access broke the day a second resource sorted ahead of the first.
func res(b *blueprint.Blueprint, key string) *blueprint.Resource {
	for i := range b.Resources {
		if b.Resources[i].Key == key {
			return &b.Resources[i]
		}
	}
	panic("no resource " + key + " in the committed blueprint")
}

func loadPilot(t *testing.T) (blueprint.Blueprint, *Loader) {
	t.Helper()

	bp, err := blueprint.LoadDir(filepath.Join("..", "..", "blueprints", "thousandeyes"))
	if err != nil {
		t.Fatalf("loading the committed pilot blueprint: %v", err)
	}

	return bp, NewLoader(pilotModule)
}

// TestUnit_SDKBind_CommittedBlueprintMatchesTheSDK is the check that earns this
// package's keep in CI: the committed blueprint's bindings all resolve against the
// SDK version the pilot pins, not against whatever is in a local checkout.
func TestUnit_SDKBind_CommittedBlueprintMatchesTheSDK(t *testing.T) {
	t.Parallel()

	bp, l := loadPilot(t)

	report := Verify(l, bp)
	if err := report.Err(); err != nil {
		t.Fatalf("the committed blueprint does not match the pinned SDK:\n%v", err)
	}

	// A run that verified nothing must not look like a clean pass.
	if report.Checked == 0 {
		t.Error("no bindings were verified, so this test proves nothing")
	}
}

// TestUnit_SDKBind_CatchesTheAccessorMistakeThatHappened reproduces the actual bug
// this package was written for.
//
// The binding was authored from the SDK's working tree, where services hang
// directly off the root client. The pinned version groups them under a nested API
// struct, so "r.client.Tags" resolves to nothing -- and the only signal was four
// identical compile errors in generated code.
func TestUnit_SDKBind_CatchesTheAccessorMistakeThatHappened(t *testing.T) {
	t.Parallel()

	bp, l := loadPilot(t)
	res(&bp, "tag").Binding.Service.Accessor = "r.client.Tags"

	report := Verify(l, bp)

	err := report.Err()
	if err == nil {
		t.Fatal("expected the wrong accessor to be caught")
	}
	if !errors.Is(err, ErrBindings) {
		t.Errorf("error should wrap ErrBindings: %v", err)
	}

	msg := err.Error()
	// The message has to name the blueprint field to edit, and say what the type
	// actually has, or it is no better than the compile error it replaces.
	for _, want := range []string{"binding.service.accessor", `has no field "Tags"`, "API"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message should mention %q:\n%v", want, msg)
		}
	}
}

func TestUnit_SDKBind_CatchesBindingMistakes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// mutate breaks the committed blueprint in exactly one way.
		mutate func(*blueprint.Blueprint)
		// wantPath is the blueprint field the message must name.
		wantPath string
		// wantDetail is a fragment proving the message is specific.
		wantDetail string
	}{
		{
			name: "method that does not exist",
			mutate: func(b *blueprint.Blueprint) {
				res(b, "tag").Binding.Read.Method = "FetchTag"
			},
			wantPath:   "binding.read.method",
			wantDetail: "has no method FetchTag",
		},
		{
			// The dangerous one: DeleteTag returns two values, not three, so a
			// wrong arity generates an assignment that does not compile.
			name: "return arity that does not match the method",
			mutate: func(b *blueprint.Blueprint) {
				res(b, "tag").Binding.Delete.Return = blueprint.ReturnResultTransportError
				res(b, "tag").Binding.Delete.ResultType = "tags.Tag"
			},
			wantPath:   "binding.delete.return",
			wantDetail: "returns 2",
		},
		{
			name: "result type that does not match the method",
			mutate: func(b *blueprint.Blueprint) {
				res(b, "tag").Binding.Read.ResultType = "tags.TagInfo"
			},
			wantPath:   "binding.read.resultType",
			wantDetail: "returns *tags.Tag",
		},
		{
			name: "service type that does not exist",
			mutate: func(b *blueprint.Blueprint) {
				res(b, "tag").Binding.Service.TypeName = "TagsService"
			},
			wantPath:   "binding.service.typeName",
			wantDetail: "no exported type TagsService",
		},
		{
			// Naming a type that exists but is the wrong one is the more likely
			// mistake, since tags.Tag is a real model type. It must fail on the
			// methods rather than pass because the name resolved.
			name: "service type that exists but is the wrong type",
			mutate: func(b *blueprint.Blueprint) {
				res(b, "tag").Binding.Service.TypeName = "Tag"
			},
			wantPath:   "binding.read.method",
			wantDetail: "tags.Tag has no method GetTag",
		},
		{
			name: "request body type that does not exist",
			mutate: func(b *blueprint.Blueprint) {
				res(b, "tag").Binding.Body.RequestType = "tags.TagRequest"
			},
			wantPath:   "binding.body.requestType",
			wantDetail: "no exported type TagRequest",
		},
		{
			// The quietest failure of all: a wrong field name that happens to
			// match another field of the same type compiles and maps the wrong
			// value.
			name: "wire field that does not exist on the model",
			mutate: func(b *blueprint.Blueprint) {
				// Plausible and absent, which is the shape of the mistake. "Colour" would not
				// do: the SDK type really has one, so the binding would be valid and the case
				// would silently stop testing anything.
				res(b, "tag").Schema.Attributes[3].Wire.SDKField = "Shade"
			},
			wantPath:   "wire.sdkField",
			wantDetail: `has no field "Shade"`,
		},
		{
			name: "update body type that does not exist",
			mutate: func(b *blueprint.Blueprint) {
				res(b, "tag").Binding.Body.UpdateRequestType = "tags.TagUpdate"
				res(b, "tag").Binding.Body.UpdateConstructorExpr = "&tags.TagUpdate{}"
			},
			wantPath:   "binding.body.updateRequestType",
			wantDetail: "no exported type TagUpdate",
		},
		{
			// The check that makes reusing one assignment list against a split
			// update body safe: a field on create's type and absent from update's
			// must fail as a named problem, not as a compile error in generated code.
			name: "wire field missing from a split update body",
			mutate: func(b *blueprint.Blueprint) {
				// TagFilter is real and resolves, and carries none of the tag's
				// expanded fields but Key -- the shape of a genuinely divergent clone.
				res(b, "tag").Binding.Body.UpdateRequestType = "tags.TagFilter"
				res(b, "tag").Binding.Body.UpdateConstructorExpr = "&tags.TagFilter{}"
			},
			wantPath:   "wire.sdkField",
			wantDetail: "expand (update body)",
		},
		{
			name: "accessor that is not rooted in the receiver",
			mutate: func(b *blueprint.Blueprint) {
				res(b, "tag").Binding.Service.Accessor = "someGlobal.Tags"
			},
			wantPath:   "binding.service.accessor",
			wantDetail: "cannot be verified",
		},
		{
			name: "client type that does not exist",
			mutate: func(b *blueprint.Blueprint) {
				b.Provider.SDK.ClientType = "*thousandeyes.APIClient"
			},
			wantPath:   "provider.sdk.clientType",
			wantDetail: "no exported type APIClient",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bp, l := loadPilot(t)
			tc.mutate(&bp)

			err := Verify(l, bp).Err()
			if err == nil {
				t.Fatalf("expected a problem mentioning %q", tc.wantPath)
			}

			msg := err.Error()
			if !strings.Contains(msg, tc.wantPath) {
				t.Errorf("message should name the blueprint field %q:\n%v", tc.wantPath, msg)
			}
			if !strings.Contains(msg, tc.wantDetail) {
				t.Errorf("message should contain %q:\n%v", tc.wantDetail, msg)
			}
		})
	}
}

// TestUnit_SDKBind_AcceptsAValidSplitUpdateBody: a split whose clone carries every
// expanded field at the same type verifies cleanly, and the extra checks are counted
// so a pass is demonstrably a pass rather than a skip.
func TestUnit_SDKBind_AcceptsAValidSplitUpdateBody(t *testing.T) {
	t.Parallel()

	bp, l := loadPilot(t)
	baseline := Verify(l, bp)
	if err := baseline.Err(); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// tags.Tag re-declares every TagInfo field at identical types -- exactly the
	// clone shape the split-update SDKs produce.
	res(&bp, "tag").Binding.Body.UpdateRequestType = "tags.Tag"
	res(&bp, "tag").Binding.Body.UpdateConstructorExpr = "&tags.Tag{}"

	report := Verify(l, bp)
	if err := report.Err(); err != nil {
		t.Fatalf("a field-compatible split must verify: %v", err)
	}
	if report.Checked <= baseline.Checked {
		t.Errorf("the split added no checks (%d -> %d), so nothing was proven",
			baseline.Checked, report.Checked)
	}
}

// TestUnit_SDKBind_ReportsEveryProblem matters because a blueprint written against
// the wrong SDK version is usually wrong in several places at once, and fixing them
// one CI run at a time is miserable.
func TestUnit_SDKBind_ReportsEveryProblem(t *testing.T) {
	t.Parallel()

	bp, l := loadPilot(t)
	res(&bp, "tag").Binding.Read.Method = "FetchTag"
	res(&bp, "tag").Binding.Delete.Method = "RemoveTag"

	report := Verify(l, bp)
	if len(report.Problems) < 2 {
		t.Errorf("expected both problems, got %d: %v", len(report.Problems), report.Problems)
	}
}

// TestUnit_SDKBind_DoesNotCascadeOnABadBodyType guards the noise this package
// exists to replace. One unresolvable type must not produce one problem per
// attribute.
func TestUnit_SDKBind_DoesNotCascadeOnABadBodyType(t *testing.T) {
	t.Parallel()

	bp, l := loadPilot(t)
	attributeCount := len(res(&bp, "tag").Schema.Attributes)
	res(&bp, "tag").Binding.Body.ResponseType = "tags.TagResponse"

	report := Verify(l, bp)

	if len(report.Problems) >= attributeCount {
		t.Errorf("one bad response type produced %d problems across %d attributes; it should report the root cause once",
			len(report.Problems), attributeCount)
	}
	if len(report.Problems) != 1 {
		t.Errorf("expected exactly 1 problem, got %d: %v", len(report.Problems), report.Problems)
	}
}

// TestUnit_SDKBind_DroppedResourcesAreSkipped confirms a deliberately excluded
// resource does not have to have valid bindings.
func TestUnit_SDKBind_DroppedResourcesAreSkipped(t *testing.T) {
	t.Parallel()

	bp, l := loadPilot(t)
	res(&bp, "tag").Binding.Service.Accessor = "r.client.Nonexistent"
	res(&bp, "tag").Drop = true

	if err := Verify(l, bp).Err(); err != nil {
		t.Errorf("a dropped resource must not be verified: %v", err)
	}
}

func TestUnit_SDKBind_LoadFailsClearlyForAnUnknownPackage(t *testing.T) {
	t.Parallel()

	_, l := loadPilot(t)

	_, err := l.LookupType("example.invalid/does/not/exist", "Thing")
	if err == nil {
		t.Fatal("expected loading a nonexistent package to fail")
	}
	// Either sentinel is acceptable; what matters is that it fails rather than
	// silently reporting the binding as valid.
	if !errors.Is(err, ErrLoad) && !errors.Is(err, ErrBindings) {
		t.Errorf("error should wrap ErrLoad or ErrBindings: %v", err)
	}
}

func TestUnit_SDKBind_AccessorChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in    string
		want  []string
		valid bool
	}{
		{"r.client.API.Tags", []string{"API", "Tags"}, true},
		{"r.client.Tags", []string{"Tags"}, true},
		{"r.client", nil, false},
		{"client.API.Tags", nil, false},
		{"someGlobal.Tags", nil, false},
		{"", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			got, ok := accessorChain(tc.in)
			if ok != tc.valid {
				t.Fatalf("accessorChain(%q) valid = %v, want %v", tc.in, ok, tc.valid)
			}
			if !tc.valid {
				return
			}
			if strings.Join(got, ".") != strings.Join(tc.want, ".") {
				t.Errorf("accessorChain(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUnit_SDKBind_ArityOf(t *testing.T) {
	t.Parallel()

	tests := map[blueprint.ReturnArity]int{
		blueprint.ReturnResultTransportError: 3,
		blueprint.ReturnResultError:          2,
		blueprint.ReturnTransportError:       2,
		blueprint.ReturnError:                1,
		"nonsense":                           -1,
	}

	for arity, want := range tests {
		t.Run(string(arity), func(t *testing.T) {
			t.Parallel()
			if got := arityOf(arity); got != want {
				t.Errorf("arityOf(%q) = %d, want %d", arity, got, want)
			}
		})
	}
}
