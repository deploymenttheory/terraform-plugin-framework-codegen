package emit

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// The binding layer records conversions as dialect-settled shorthand names
// — FromPtrString, ToEnumSlice — read off the SDK's real types by pruning.
// This file is the one place those names meet the provider-core conversion
// catalog: each shorthand resolves to a finished APIToFramework* or
// FrameworkToAPI* call, and a shorthand the catalog cannot bridge is a
// render error naming the attribute, never a guess.

// kiotaEnum reports whether an enum binding's parse companion is kiota's
// Parse function rather than openapi-generator's NewXFromValue — the one
// fact that decides which catalog family bridges the enum.
func kiotaEnum(parseFunc string) bool {
	return strings.Contains(parseFunc, ".Parse")
}

// readConvert resolves one read-direction shorthand to its catalog
// function name.
func readConvert(fb *sdkbind.FieldBinding) (string, error) {
	simple := map[string]string{
		"FromPtrString":    "APIToFrameworkString",
		"FromString":       "APIToFrameworkStringValue",
		"FromPtrBool":      "APIToFrameworkBool",
		"FromBool":         "APIToFrameworkBoolValue",
		"FromPtrInt64":     "APIToFrameworkInt64",
		"FromInt64":        "APIToFrameworkInt64Value",
		"FromPtrInt32":     "APIToFrameworkInt32AsInt64",
		"FromInt32":        "APIToFrameworkInt32ValueAsInt64",
		"FromPtrFloat64":   "APIToFrameworkFloat64",
		"FromFloat64":      "APIToFrameworkFloat64Value",
		"FromPtrFloat32":   "APIToFrameworkFloat32AsFloat64",
		"FromFloat32":      "APIToFrameworkFloat32ValueAsFloat64",
		"FromPtrTime":      "APIToFrameworkTime",
		"FromPtrDateOnly":  "APIToFrameworkDateOnly",
		"FromTimeSlice":    "APIToFrameworkTimeList",
		"FromBytesBase64":  "APIToFrameworkBytesAsBase64",
		"FromTime":         "APIToFrameworkTimeValue",
		"FromStringSlice":  "APIToFrameworkStringList",
		"FromBoolSlice":    "APIToFrameworkBoolList",
		"FromInt64Slice":   "APIToFrameworkInt64List",
		"FromInt32Slice":   "APIToFrameworkInt32ListAsInt64",
		"FromFloat64Slice": "APIToFrameworkFloat64List",
	}
	if fn, ok := simple[fb.Access.ConvertGet]; ok {
		return fn, nil
	}
	switch fb.Access.ConvertGet {
	case "FromPtrEnum":
		if kiotaEnum(fb.Access.ParseFunc) {
			return "APIToFrameworkEnum", nil
		}
		return "APIToFrameworkEnumString", nil
	case "FromEnum":
		if kiotaEnum(fb.Access.ParseFunc) {
			return "APIToFrameworkEnumValue", nil
		}
		return "APIToFrameworkEnumStringValue", nil
	case "FromEnumSlice":
		if kiotaEnum(fb.Access.ParseFunc) {
			return "APIToFrameworkEnumSlice", nil
		}
		return "APIToFrameworkEnumStringSlice", nil
	}
	return "", fmt.Errorf("attribute %s: the conversion catalog has no read bridge for %q (SDK type %s)",
		fb.Attr, fb.Access.ConvertGet, fb.Access.SDKType)
}

// writePlan is one resolved write-direction conversion.
type writePlan struct {
	fn         string
	needsCtx   bool
	returnsErr bool
	// parser is the enum parse companion argument, empty when the
	// function takes none.
	parser string
}

// writeConvert resolves one write-direction shorthand.
func writeConvert(fb *sdkbind.FieldBinding) (writePlan, error) {
	simple := map[string]writePlan{
		"ToPtrString":    {fn: "FrameworkToAPIString"},
		"ToString":       {fn: "FrameworkToAPIStringValue"},
		"ToPtrBool":      {fn: "FrameworkToAPIBool"},
		"ToBool":         {fn: "FrameworkToAPIBoolValue"},
		"ToPtrInt64":     {fn: "FrameworkToAPIInt64"},
		"ToInt64":        {fn: "FrameworkToAPIInt64Value"},
		"ToPtrInt32":     {fn: "FrameworkToAPIInt64AsInt32", returnsErr: true},
		"ToInt32":        {fn: "FrameworkToAPIInt64ValueAsInt32", returnsErr: true},
		"ToPtrFloat64":   {fn: "FrameworkToAPIFloat64"},
		"ToFloat64":      {fn: "FrameworkToAPIFloat64Value"},
		"ToPtrFloat32":   {fn: "FrameworkToAPIFloat64AsFloat32"},
		"ToFloat32":      {fn: "FrameworkToAPIFloat64ValueAsFloat32"},
		"ToPtrTime":      {fn: "FrameworkToAPITime", returnsErr: true},
		"ToPtrDateOnly":  {fn: "FrameworkToAPIDateOnly", returnsErr: true},
		"ToTimeSlice":    {fn: "FrameworkToAPITimeList", needsCtx: true, returnsErr: true},
		"ToBytesBase64":  {fn: "FrameworkToAPIBytesAsBase64", returnsErr: true},
		"ToTime":         {fn: "FrameworkToAPITimeValue", returnsErr: true},
		"ToStringSlice":  {fn: "FrameworkToAPIStringList", needsCtx: true, returnsErr: true},
		"ToBoolSlice":    {fn: "FrameworkToAPIBoolList", needsCtx: true, returnsErr: true},
		"ToInt64Slice":   {fn: "FrameworkToAPIInt64List", needsCtx: true, returnsErr: true},
		"ToInt32Slice":   {fn: "FrameworkToAPIInt64ListAsInt32", needsCtx: true, returnsErr: true},
		"ToFloat64Slice": {fn: "FrameworkToAPIFloat64List", needsCtx: true, returnsErr: true},
	}
	if plan, ok := simple[fb.Access.ConvertSet]; ok {
		return plan, nil
	}
	switch fb.Access.ConvertSet {
	case "ToPtrEnum":
		if kiotaEnum(fb.Access.ParseFunc) {
			return writePlan{fn: "FrameworkToAPIEnum", returnsErr: true, parser: fb.Access.ParseFunc}, nil
		}
		return writePlan{fn: "FrameworkToAPIEnumString"}, nil
	case "ToEnum":
		if !kiotaEnum(fb.Access.ParseFunc) {
			return writePlan{fn: "FrameworkToAPIEnumStringValue"}, nil
		}
	case "ToEnumSlice":
		if kiotaEnum(fb.Access.ParseFunc) {
			return writePlan{fn: "FrameworkToAPIEnumSlice", needsCtx: true, returnsErr: true, parser: fb.Access.ParseFunc}, nil
		}
		return writePlan{fn: "FrameworkToAPIEnumStringSlice", needsCtx: true, returnsErr: true}, nil
	}
	return writePlan{}, fmt.Errorf("attribute %s: the conversion catalog has no write bridge for %q (SDK type %s)",
		fb.Attr, fb.Access.ConvertSet, fb.Access.SDKType)
}

// call renders the finished conversion call for one write.
func (p writePlan) call(value, setter string) string {
	args := []string{value}
	if p.needsCtx {
		args = []string{"ctx", value}
	}
	if p.parser != "" {
		args = append(args, p.parser)
	}
	args = append(args, setter)
	return "convert." + p.fn + "(" + strings.Join(args, ", ") + ")"
}
