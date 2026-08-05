// Package convert moves values between the SDK's Go types and the Terraform
// framework's value types.
//
// It exists so that generated code contains one call per field instead of an
// inlined nil check, and so the null-versus-empty policy for a whole provider
// lives in one reviewable place rather than being restated a thousand times.
//
// Most of the functions were written for the "resty service" SDK dialect, where
// a model is a plain struct: optional scalars are pointers, enumerations are
// named string types held by value, and there are no getters or setters. They
// carry over unchanged to a Kiota-generated SDK, whose getters and setters also
// trade in pointers to scalars; what Kiota adds on top -- closed enumerations
// parsed through a factory, uuid.UUID identifiers and time.Time timestamps --
// has its own helpers at the end of the file.
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
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/microsoft/kiota-abstractions-go/serialization"
)

// -----------------------------------------------------------------------------
// SDK -> framework (flatten)
// -----------------------------------------------------------------------------

// PtrStringToFramework converts an optional string to types.String.
func PtrStringToFramework[T ~string](v *T) types.String {
	if v == nil {
		return types.StringNull()
	}
	return types.StringValue(string(*v))
}

// PtrBoolToFramework converts an optional bool to types.Bool.
func PtrBoolToFramework[T ~bool](v *T) types.Bool {
	if v == nil {
		return types.BoolNull()
	}
	return types.BoolValue(bool(*v))
}

// PtrInt64ToFramework converts an optional int64 to types.Int64.
func PtrInt64ToFramework[T ~int64 | ~int](v *T) types.Int64 {
	if v == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*v))
}

// PtrInt32ToFramework converts an optional int32 to types.Int32.
func PtrInt32ToFramework[T ~int32](v *T) types.Int32 {
	if v == nil {
		return types.Int32Null()
	}
	return types.Int32Value(int32(*v))
}

// PtrFloat64ToFramework converts an optional float64 to types.Float64.
func PtrFloat64ToFramework[T ~float64](v *T) types.Float64 {
	if v == nil {
		return types.Float64Null()
	}
	return types.Float64Value(float64(*v))
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

// IntEnumToFramework converts a named integer enumeration to types.Int64.
//
// The classic tests' interval is the motivating case: the SDK declares
// `type TestInterval int`, so the string-enum converters cannot carry it. Zero
// becomes null for the same reason EnumToFramework maps "" to null: these
// fields are omitempty on the wire, and an absent value decodes to the zero.
func IntEnumToFramework[T ~int | ~int64](v T) types.Int64 {
	if v == 0 {
		return types.Int64Null()
	}
	return types.Int64Value(int64(v))
}

// NamedBoolToFramework converts a value-typed named bool to types.Bool.
//
// Always a concrete value, never null: the wire cannot distinguish an absent
// bool from false once omitempty has had its way, and inventing null here would
// claim a distinction the transport does not carry.
func NamedBoolToFramework[T ~bool](v T) types.Bool {
	return types.BoolValue(bool(v))
}

// StringToFramework converts a value-typed wire string to types.String.
//
// For SDK fields declared as plain `string` rather than `*string` -- the
// agents member's agentId is the motivating case. An empty value becomes null,
// matching the pointer converters' treatment of absence.
func StringToFramework(v string) types.String {
	if v == "" {
		return types.StringNull()
	}
	return types.StringValue(v)
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
// enumerations are open, and rejecting an unrecognised value here would turn a
// routine upstream addition into a provider that cannot express it.
func FrameworkToEnum[T ~string](v types.String) T {
	if v.IsNull() || v.IsUnknown() {
		return T("")
	}
	return T(v.ValueString())
}

// FrameworkToIntEnum converts types.Int64 to a named integer enumeration.
//
// Unchecked against the known members for the reason FrameworkToEnum is: the
// sets are open, and refusing an unrecognised value would turn a routine
// upstream addition into a provider that cannot express it.
func FrameworkToIntEnum[T ~int](v types.Int64) T {
	if v.IsNull() || v.IsUnknown() {
		return T(0)
	}
	return T(v.ValueInt64())
}

// FrameworkToString converts types.String to a value-typed wire string.
//
// Null and unknown become "", which the omitempty wire treatment drops; a
// required attribute is never null here, so nothing real is lost.
func FrameworkToString(v types.String) string {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return v.ValueString()
}

// FrameworkToNamedBool converts types.Bool to a value-typed named bool.
//
// Null and unknown become false, which omitempty then drops from the request --
// the value-typed field's structural limit, recorded rather than papered over:
// an explicit false cannot travel either, and the probe is what documents what
// the API does about that.
func FrameworkToNamedBool[T ~bool](v types.Bool) T {
	if v.IsNull() || v.IsUnknown() {
		return T(false)
	}
	return T(v.ValueBool())
}

// FrameworkToPtrNamed converts types.String to an optional named string.
func FrameworkToPtrNamed[T ~string](v types.String) *T {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	t := T(v.ValueString())
	return &t
}

// FrameworkToPtrNamedBool converts types.Bool to an optional named bool.
func FrameworkToPtrNamedBool[T ~bool](v types.Bool) *T {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	t := T(v.ValueBool())
	return &t
}

// FrameworkToPtrNamedInt converts types.Int64 to an optional named integer,
// covering both the SDK's plain *int fields and its named integer types.
func FrameworkToPtrNamedInt[T ~int | ~int64](v types.Int64) *T {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	t := T(v.ValueInt64())
	return &t
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

// Deref reads a pointer field as a plain Go value, treating nil as the zero value.
//
// The exception to this package's null-versus-empty rule, and only because its one caller
// is outside Terraform's value system. A resource identity holds plain Go scalars, not
// framework values, so there is no null to represent: a list result either carries an
// identifier or it is unusable. Yielding the zero value keeps a query that meets one odd
// element from panicking, and an empty identity is visible in the output.
//
// Nothing that writes Terraform state should use this. Flattening a nil pointer to the zero
// value is what produces a provider with a permanent diff, which is why every other
// function here returns null instead.
func Deref[T any](p *T) T {
	if p == nil {
		var zero T

		return zero
	}

	return *p
}

// FrameworkSetToEnumSlice converts a set of strings into a named-string slice --
// a suppression window's daysOfWeek is []DaysOfWeek, not []string.
func FrameworkSetToEnumSlice[T ~string](ctx context.Context, v types.Set) ([]T, diag.Diagnostics) {
	raw, diags := FrameworkSetToStringSlice(ctx, v)
	if raw == nil {
		return nil, diags
	}
	out := make([]T, len(raw))
	for i, s := range raw {
		out[i] = T(s)
	}
	return out, diags
}

// EnumSliceToFrameworkSet is the read-side mirror of FrameworkSetToEnumSlice.
func EnumSliceToFrameworkSet[T ~string](ctx context.Context, v []T) (types.Set, diag.Diagnostics) {
	if v == nil {
		return StringSliceToFrameworkSet(ctx, nil)
	}
	raw := make([]string, len(v))
	for i, s := range v {
		raw[i] = string(s)
	}
	return StringSliceToFrameworkSet(ctx, raw)
}

// -----------------------------------------------------------------------------
// Kiota-specific helpers
// -----------------------------------------------------------------------------

// PtrStringerToFramework converts an optional value that renders itself as a
// string -- a *uuid.UUID identifier or a pointer to a Kiota enumeration -- to
// types.String.
//
// Calling String here is safe where EnumToFramework had to refuse it: a Kiota
// enumeration is closed, so a value that exists at all renders as its wire
// string rather than as a logging decoration.
func PtrStringerToFramework[T fmt.Stringer](p *T) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue((*p).String())
}

// ParseUUID reads a uuid the SDK's typed indexers demand from a string the
// practitioner configured. A malformed value yields the zero uuid rather than
// a panic: the API answers a lookup for the zero uuid with the not-found the
// situation deserves, and a provider must never crash on configuration.
func ParseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}
	}
	return id
}

// StringerToString renders a value through its own String method, for an SDK
// identifier that is a typed value (a uuid) where the identity model wants the
// plain string.
func StringerToString(v fmt.Stringer) string {
	if v == nil {
		return ""
	}
	return v.String()
}

// PtrTimeToFramework converts an optional timestamp to types.String in RFC 3339,
// which is the shape it arrived in on the wire and the only shape Terraform can
// compare without a semantic-equality plugin.
func PtrTimeToFramework(p *time.Time) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(p.Format(time.RFC3339))
}

// FrameworkToKiotaEnum converts types.String to a pointer to a Kiota
// enumeration, through the ParseX function the SDK generates beside each one.
//
// A Kiota enumeration is closed: ParseX answers (nil, nil) for a value outside
// the set, and sending such a value is impossible because the type is an integer
// index into the known members. That is why this helper can reject an
// unrecognised value with a diagnostic where FrameworkToEnum deliberately could
// not -- refusing here is surfacing a certain 400 early, not closing an open set.
//
// ParseX returns its result as a *T wrapped in any, which is what the type
// assertion unwraps.
func FrameworkToKiotaEnum[T any](v types.String, parse func(string) (any, error)) (*T, diag.Diagnostics) {
	var diags diag.Diagnostics

	if v.IsNull() || v.IsUnknown() {
		return nil, diags
	}

	raw := v.ValueString()

	parsed, err := parse(raw)
	if err != nil {
		diags.AddError(
			"Invalid enumeration value",
			fmt.Sprintf("Parsing %q: %s", raw, err),
		)
		return nil, diags
	}
	if parsed == nil {
		var zero T
		diags.AddError(
			"Invalid enumeration value",
			fmt.Sprintf("%q is not a member of %T, and the API would reject it.", raw, zero),
		)
		return nil, diags
	}

	typed, ok := parsed.(*T)
	if !ok {
		diags.AddError(
			"Invalid enumeration value",
			fmt.Sprintf("The parser for %q returned %T where a *%T was expected. This is a bug in the provider.",
				raw, parsed, *new(T)),
		)
		return nil, diags
	}

	return typed, diags
}

// PtrInt32ToFrameworkInt64 widens an optional int32 -- the Kiota SDK types
// every plain integer property as *int32 -- into the types.Int64 the curated
// schema declares, so the schema does not have to change shape with the SDK.
func PtrInt32ToFrameworkInt64[T ~int32](p *T) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*p))
}

// FrameworkInt64ToPtrInt32 is the write-side mirror of PtrInt32ToFrameworkInt64.
// Not generic: a setter parameter gives Go no way to infer a type argument, and
// the Kiota SDK types every plain integer as exactly *int32.
func FrameworkInt64ToPtrInt32(v types.Int64) *int32 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	out := int32(v.ValueInt64())
	return &out
}

// PtrInt32ToFrameworkString renders an optional int32 as a string attribute --
// for a property the curated schema models as a string (a test interval) where
// the Kiota SDK carries the raw integer.
func PtrInt32ToFrameworkString[T ~int32](p *T) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(strconv.FormatInt(int64(*p), 10))
}

// FrameworkToPtrTime parses an RFC 3339 string attribute into the *time.Time a
// Kiota write model wants. RFC 3339 is the only accepted spelling because it is
// the shape the API echoes back -- accepting a looser format here would create
// a value the read side can never render identically, which Terraform reports
// as a permanent diff.
func FrameworkToPtrTime(v types.String) (*time.Time, diag.Diagnostics) {
	var diags diag.Diagnostics

	if v.IsNull() || v.IsUnknown() {
		return nil, diags
	}

	t, err := time.Parse(time.RFC3339, v.ValueString())
	if err != nil {
		diags.AddError(
			"Invalid timestamp",
			fmt.Sprintf("%q is not an RFC 3339 timestamp (2027-06-01T00:00:00Z): %s", v.ValueString(), err),
		)
		return nil, diags
	}
	return &t, diags
}

// PtrDateOnlyToFramework converts an optional date-only value to types.String
// in the yyyy-mm-dd shape it arrived in on the wire.
func PtrDateOnlyToFramework(p *serialization.DateOnly) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(p.String())
}

// FrameworkToPtrDateOnly parses a yyyy-mm-dd string attribute into the
// *serialization.DateOnly a Kiota write model wants.
func FrameworkToPtrDateOnly(v types.String) (*serialization.DateOnly, diag.Diagnostics) {
	var diags diag.Diagnostics

	if v.IsNull() || v.IsUnknown() {
		return nil, diags
	}

	d, err := serialization.ParseDateOnly(v.ValueString())
	if err != nil {
		diags.AddError(
			"Invalid date",
			fmt.Sprintf("%q is not a yyyy-mm-dd date: %s", v.ValueString(), err),
		)
		return nil, diags
	}
	return d, diags
}

// KiotaEnumSliceToFrameworkSet renders a slice of Kiota enumeration values as
// a set of their wire strings -- a suppression window's daysOfWeek.
func KiotaEnumSliceToFrameworkSet[T fmt.Stringer](ctx context.Context, v []T) (types.Set, diag.Diagnostics) {
	if v == nil {
		return StringSliceToFrameworkSet(ctx, nil)
	}
	raw := make([]string, len(v))
	for i, e := range v {
		raw[i] = e.String()
	}
	return StringSliceToFrameworkSet(ctx, raw)
}

// FrameworkSetToKiotaEnumSlice converts a set of strings into a slice of Kiota
// enumeration values through the SDK's ParseX companion, refusing any string
// outside the closed set for the same reason FrameworkToKiotaEnum does.
func FrameworkSetToKiotaEnumSlice[T any](ctx context.Context, v types.Set, parse func(string) (any, error)) ([]T, diag.Diagnostics) {
	raw, diags := FrameworkSetToStringSlice(ctx, v)
	if raw == nil {
		return nil, diags
	}

	out := make([]T, 0, len(raw))
	for _, s := range raw {
		one, oneDiags := FrameworkToKiotaEnum[T](types.StringValue(s), parse)
		diags.Append(oneDiags...)
		if one != nil {
			out = append(out, *one)
		}
	}
	return out, diags
}
