// Package convert moves values between the SDK's Go types and the Terraform
// framework's value types.
//
// It exists so that generated code contains one call per field instead of an
// inlined nil check, and so the null-versus-empty policy for a whole provider
// lives in one reviewable place rather than being restated a thousand times.
//
// The functions are written for the "resty service" SDK dialect, where a model
// is a plain struct: optional scalars are pointers, enumerations are named string
// types held by value, and there are no getters or setters. That makes this
// package markedly smaller than the equivalent for a Kiota-generated SDK, which
// needs setter-callback plumbing because its models expose no assignable fields.
//
// # Null and unknown
//
// Flattening a nil pointer yields null, not the zero value. Writing
// types.StringValue("") for an absent field is the single most common cause of a
// provider that reports a permanent diff, because the practitioner's
// configuration says nothing and state says empty string.
//
// Expanding null or unknown yields nil, which the SDK's `omitempty` tags then
// omit from the request. On an API whose update is a full-body PUT that means the
// field is cleared, which is the correct reading of an attribute the practitioner
// removed from configuration.
package convert

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// -----------------------------------------------------------------------------
// SDK -> framework (flatten)
// -----------------------------------------------------------------------------

// PtrStringToFramework converts an optional string to types.String.
func PtrStringToFramework(v *string) types.String {
	if v == nil {
		return types.StringNull()
	}
	return types.StringValue(*v)
}

// PtrBoolToFramework converts an optional bool to types.Bool.
func PtrBoolToFramework(v *bool) types.Bool {
	if v == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*v)
}

// PtrInt64ToFramework converts an optional int64 to types.Int64.
func PtrInt64ToFramework(v *int64) types.Int64 {
	if v == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*v)
}

// PtrInt32ToFramework converts an optional int32 to types.Int32.
func PtrInt32ToFramework(v *int32) types.Int32 {
	if v == nil {
		return types.Int32Null()
	}
	return types.Int32Value(*v)
}

// PtrFloat64ToFramework converts an optional float64 to types.Float64.
func PtrFloat64ToFramework(v *float64) types.Float64 {
	if v == nil {
		return types.Float64Null()
	}
	return types.Float64Value(*v)
}

// EnumToFramework converts a named string enumeration to types.String.
//
// It deliberately converts the underlying string rather than calling any String
// method the type may have. The SDK's enumerations are open by design: a value
// the specification does not list still decodes, and the SDK's String method
// renders such a value as "TypeName(raw)" for logging. Putting that rendering
// into Terraform state would corrupt it, so the raw wire value is what travels.
//
// An empty enumeration is null rather than the empty string, because the SDK
// marks these fields omitempty and an absent value decodes to "".
func EnumToFramework[T ~string](v T) types.String {
	if v == "" {
		return types.StringNull()
	}
	return types.StringValue(string(v))
}

// StringSliceToFrameworkSet converts a string slice to types.Set.
//
// A nil slice becomes a null set and an empty slice becomes an empty set, so a
// field the API omits and a field it returns as [] are not conflated. Conflating
// them produces a diff that cannot be resolved from configuration.
func StringSliceToFrameworkSet(ctx context.Context, v []string) (types.Set, diag.Diagnostics) {
	if v == nil {
		return types.SetNull(types.StringType), nil
	}
	return types.SetValueFrom(ctx, types.StringType, v)
}

// StringSliceToFrameworkList converts a string slice to types.List, preserving
// order. Use it when the API's ordering is meaningful; prefer a set when it is
// not, since a reordered list is a spurious diff.
func StringSliceToFrameworkList(ctx context.Context, v []string) (types.List, diag.Diagnostics) {
	if v == nil {
		return types.ListNull(types.StringType), nil
	}
	return types.ListValueFrom(ctx, types.StringType, v)
}

// -----------------------------------------------------------------------------
// framework -> SDK (expand)
// -----------------------------------------------------------------------------

// FrameworkToPtrString converts types.String to an optional string.
//
// Null and unknown both yield nil. Unknown matters on create: a computed
// attribute is unknown in the plan, and sending its unresolved value would write
// a literal placeholder to the API.
func FrameworkToPtrString(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	s := v.ValueString()
	return &s
}

// FrameworkToPtrBool converts types.Bool to an optional bool.
func FrameworkToPtrBool(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	b := v.ValueBool()
	return &b
}

// FrameworkToPtrInt64 converts types.Int64 to an optional int64.
func FrameworkToPtrInt64(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	i := v.ValueInt64()
	return &i
}

// FrameworkToPtrInt32 converts types.Int32 to an optional int32.
func FrameworkToPtrInt32(v types.Int32) *int32 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	i := v.ValueInt32()
	return &i
}

// FrameworkToPtrFloat64 converts types.Float64 to an optional float64.
func FrameworkToPtrFloat64(v types.Float64) *float64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	f := v.ValueFloat64()
	return &f
}

// FrameworkToEnum converts types.String to a named string enumeration.
//
// The value is not checked against the enumeration's known members. These
// enumerations are open, and rejecting an unrecognized value here would turn a
// routine upstream addition into a provider that cannot express it.
func FrameworkToEnum[T ~string](v types.String) T {
	if v.IsNull() || v.IsUnknown() {
		return T("")
	}
	return T(v.ValueString())
}

// FrameworkSetToStringSlice converts types.Set to a string slice. Null and
// unknown both yield nil so the field is omitted from the request.
func FrameworkSetToStringSlice(ctx context.Context, v types.Set) ([]string, diag.Diagnostics) {
	return elementsToStringSlice(ctx, v.IsNull() || v.IsUnknown(), v.Elements(), v)
}

// FrameworkListToStringSlice converts types.List to a string slice.
func FrameworkListToStringSlice(ctx context.Context, v types.List) ([]string, diag.Diagnostics) {
	return elementsToStringSlice(ctx, v.IsNull() || v.IsUnknown(), v.Elements(), v)
}

// collection is the shared surface of the framework's list and set values.
type collection interface {
	ElementsAs(ctx context.Context, target any, allowUnhandled bool) diag.Diagnostics
}

func elementsToStringSlice(ctx context.Context, absent bool, elems []attr.Value, c collection) ([]string, diag.Diagnostics) {
	if absent {
		return nil, nil
	}
	// An explicitly empty collection is distinct from an absent one, and must
	// travel as an empty slice so the API is told to clear the field.
	if len(elems) == 0 {
		return []string{}, nil
	}

	out := make([]string, 0, len(elems))
	diags := c.ElementsAs(ctx, &out, false)

	return out, diags
}
