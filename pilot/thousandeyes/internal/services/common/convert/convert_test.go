package convert

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// testEnum stands in for an SDK enumeration: a named string type, held by value,
// open to values its specification does not list.
type testEnum string

const (
	testEnumAll     testEnum = "all"
	testEnumPartner testEnum = "partner"
)

func ptr[T any](v T) *T { return &v }

// TestUnit_Convert_NilPointerFlattensToNull pins the policy that matters most.
// Flattening an absent field to the zero value rather than to null is the single
// most common cause of a provider with a permanent diff: configuration says
// nothing, state says empty string, and no configuration change can reconcile them.
func TestUnit_Convert_NilPointerFlattensToNull(t *testing.T) {
	t.Parallel()

	if got := PtrStringToFramework[string](nil); !got.IsNull() {
		t.Errorf("PtrStringToFramework[string](nil) = %v, want null", got)
	}
	if got := PtrBoolToFramework[bool](nil); !got.IsNull() {
		t.Errorf("PtrBoolToFramework[bool](nil) = %v, want null", got)
	}
	if got := PtrInt64ToFramework[int64](nil); !got.IsNull() {
		t.Errorf("PtrInt64ToFramework[int64](nil) = %v, want null", got)
	}
	if got := PtrInt32ToFramework[int32](nil); !got.IsNull() {
		t.Errorf("PtrInt32ToFramework[int32](nil) = %v, want null", got)
	}
	if got := PtrFloat64ToFramework[float64](nil); !got.IsNull() {
		t.Errorf("PtrFloat64ToFramework[float64](nil) = %v, want null", got)
	}
}

// TestUnit_Convert_EmptyStringIsPreservedNotNulled is the counterpart: a pointer
// to the empty string is a value the API actually returned, and must survive as
// an empty string rather than collapsing to null.
func TestUnit_Convert_EmptyStringIsPreservedNotNulled(t *testing.T) {
	t.Parallel()

	got := PtrStringToFramework(ptr(""))
	if got.IsNull() {
		t.Fatal("a pointer to the empty string must not flatten to null")
	}
	if got.ValueString() != "" {
		t.Errorf("got %q, want the empty string", got.ValueString())
	}
}

func TestUnit_Convert_PointerScalarsRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("string", func(t *testing.T) {
		t.Parallel()
		got := FrameworkToPtrString(PtrStringToFramework(ptr("hello")))
		if got == nil || *got != "hello" {
			t.Errorf("round trip = %v, want \"hello\"", got)
		}
	})

	t.Run("bool false survives", func(t *testing.T) {
		t.Parallel()
		// false is the zero value, so a naive implementation loses it.
		got := FrameworkToPtrBool(PtrBoolToFramework(ptr(false)))
		if got == nil {
			t.Fatal("round trip of false yielded nil; the zero value was lost")
		}
		if *got {
			t.Errorf("round trip = true, want false")
		}
	})

	t.Run("int64 zero survives", func(t *testing.T) {
		t.Parallel()
		got := FrameworkToPtrInt64(PtrInt64ToFramework(ptr(int64(0))))
		if got == nil {
			t.Fatal("round trip of 0 yielded nil; the zero value was lost")
		}
		if *got != 0 {
			t.Errorf("round trip = %d, want 0", *got)
		}
	})

	t.Run("float64", func(t *testing.T) {
		t.Parallel()
		got := FrameworkToPtrFloat64(PtrFloat64ToFramework(ptr(1.5)))
		if got == nil || *got != 1.5 {
			t.Errorf("round trip = %v, want 1.5", got)
		}
	})
}

// TestUnit_Convert_UnknownExpandsToNil matters on create: a computed attribute is
// unknown in the plan, and sending its unresolved value would write a literal
// placeholder to the API.
func TestUnit_Convert_UnknownExpandsToNil(t *testing.T) {
	t.Parallel()

	if got := FrameworkToPtrString(types.StringUnknown()); got != nil {
		t.Errorf("FrameworkToPtrString(unknown) = %v, want nil", got)
	}
	if got := FrameworkToPtrBool(types.BoolUnknown()); got != nil {
		t.Errorf("FrameworkToPtrBool(unknown) = %v, want nil", got)
	}
	if got := FrameworkToPtrInt64(types.Int64Unknown()); got != nil {
		t.Errorf("FrameworkToPtrInt64(unknown) = %v, want nil", got)
	}
	if got := FrameworkToPtrFloat64(types.Float64Unknown()); got != nil {
		t.Errorf("FrameworkToPtrFloat64(unknown) = %v, want nil", got)
	}
	if got := FrameworkToPtrInt32(types.Int32Unknown()); got != nil {
		t.Errorf("FrameworkToPtrInt32(unknown) = %v, want nil", got)
	}
}

func TestUnit_Convert_NullExpandsToNil(t *testing.T) {
	t.Parallel()

	if got := FrameworkToPtrString(types.StringNull()); got != nil {
		t.Errorf("FrameworkToPtrString(null) = %v, want nil", got)
	}
	if got := FrameworkToPtrBool(types.BoolNull()); got != nil {
		t.Errorf("FrameworkToPtrBool(null) = %v, want nil", got)
	}
}

// TestUnit_Convert_EnumUsesRawWireValue is the trap this package exists to avoid.
// The SDK's enumerations are open, and their String method renders an unlisted
// value as "TypeName(raw)" for logging. Putting that rendering into Terraform
// state would corrupt it, so the underlying string is what must travel.
func TestUnit_Convert_EnumUsesRawWireValue(t *testing.T) {
	t.Parallel()

	t.Run("known value", func(t *testing.T) {
		t.Parallel()
		if got := EnumToFramework(testEnumAll); got.ValueString() != "all" {
			t.Errorf("got %q, want %q", got.ValueString(), "all")
		}
	})

	t.Run("value outside the enumeration survives verbatim", func(t *testing.T) {
		t.Parallel()
		// The API adding a value must not corrupt state or fail the provider.
		got := EnumToFramework(testEnum("newly-added-upstream"))
		if got.ValueString() != "newly-added-upstream" {
			t.Errorf("got %q, want the raw value verbatim", got.ValueString())
		}
	})

	t.Run("empty enum flattens to null", func(t *testing.T) {
		t.Parallel()
		// These fields are omitempty in the SDK, so an absent value decodes to
		// "" and must present as null rather than as an empty string.
		if got := EnumToFramework(testEnum("")); !got.IsNull() {
			t.Errorf("got %v, want null", got)
		}
	})
}

func TestUnit_Convert_EnumRoundTrips(t *testing.T) {
	t.Parallel()

	for _, want := range []testEnum{testEnumAll, testEnumPartner, "unlisted"} {
		t.Run(string(want), func(t *testing.T) {
			t.Parallel()
			if got := FrameworkToEnum[testEnum](EnumToFramework(want)); got != want {
				t.Errorf("round trip = %q, want %q", got, want)
			}
		})
	}
}

func TestUnit_Convert_EnumExpandsAbsentToEmpty(t *testing.T) {
	t.Parallel()

	// The SDK holds enumerations by value with omitempty, so the empty string is
	// how "not set" is expressed on the wire.
	if got := FrameworkToEnum[testEnum](types.StringNull()); got != "" {
		t.Errorf("null expanded to %q, want the empty string", got)
	}
	if got := FrameworkToEnum[testEnum](types.StringUnknown()); got != "" {
		t.Errorf("unknown expanded to %q, want the empty string", got)
	}
}

// TestUnit_Convert_NilSliceAndEmptySliceAreDistinct guards a distinction that is
// easy to lose and impossible for a practitioner to work around once lost: a
// field the API omits and a field it returns as [] mean different things.
func TestUnit_Convert_NilSliceAndEmptySliceAreDistinct(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	nullSet, diags := StringSliceToFrameworkSet(ctx, nil)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if !nullSet.IsNull() {
		t.Error("a nil slice must flatten to a null set")
	}

	emptySet, diags := StringSliceToFrameworkSet(ctx, []string{})
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if emptySet.IsNull() {
		t.Error("an empty slice must flatten to an empty set, not null")
	}
	if len(emptySet.Elements()) != 0 {
		t.Errorf("empty set has %d elements", len(emptySet.Elements()))
	}
}

func TestUnit_Convert_StringCollectionsRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	want := []string{"a", "b", "c"}

	t.Run("set", func(t *testing.T) {
		t.Parallel()

		set, diags := StringSliceToFrameworkSet(ctx, want)
		if diags.HasError() {
			t.Fatalf("flatten: %v", diags)
		}

		got, diags := FrameworkSetToStringSlice(ctx, set)
		if diags.HasError() {
			t.Fatalf("expand: %v", diags)
		}
		if len(got) != len(want) {
			t.Fatalf("round trip = %v, want %v", got, want)
		}
	})

	t.Run("list preserves order", func(t *testing.T) {
		t.Parallel()

		list, diags := StringSliceToFrameworkList(ctx, want)
		if diags.HasError() {
			t.Fatalf("flatten: %v", diags)
		}

		got, diags := FrameworkListToStringSlice(ctx, list)
		if diags.HasError() {
			t.Fatalf("expand: %v", diags)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("round trip = %v, want %v (order matters for a list)", got, want)
			}
		}
	})
}

func TestUnit_Convert_AbsentCollectionsExpandToNil(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := map[string]func() ([]string, bool){
		"null set": func() ([]string, bool) {
			got, d := FrameworkSetToStringSlice(ctx, types.SetNull(types.StringType))
			return got, d.HasError()
		},
		"unknown set": func() ([]string, bool) {
			got, d := FrameworkSetToStringSlice(ctx, types.SetUnknown(types.StringType))
			return got, d.HasError()
		},
		"null list": func() ([]string, bool) {
			got, d := FrameworkListToStringSlice(ctx, types.ListNull(types.StringType))
			return got, d.HasError()
		},
		"unknown list": func() ([]string, bool) {
			got, d := FrameworkListToStringSlice(ctx, types.ListUnknown(types.StringType))
			return got, d.HasError()
		},
	}

	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, hasErr := fn()
			if hasErr {
				t.Fatal("unexpected diagnostics")
			}
			if got != nil {
				t.Errorf("got %v, want nil so the field is omitted from the request", got)
			}
		})
	}
}

// TestUnit_Convert_EmptyCollectionExpandsToEmptySlice completes the pairing: an
// explicitly empty collection must travel as [] so the API is told to clear the
// field, rather than being omitted and left alone.
func TestUnit_Convert_EmptyCollectionExpandsToEmptySlice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	emptySet, diags := types.SetValueFrom(ctx, types.StringType, []string{})
	if diags.HasError() {
		t.Fatalf("building the fixture: %v", diags)
	}

	got, diags := FrameworkSetToStringSlice(ctx, emptySet)
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	if got == nil {
		t.Fatal("an explicitly empty set must expand to an empty slice, not nil")
	}
	if len(got) != 0 {
		t.Errorf("got %v, want an empty slice", got)
	}
}
