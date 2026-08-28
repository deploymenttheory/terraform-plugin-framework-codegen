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
		"FromPtrString":                "APIToFrameworkString",
		"FromString":                   "APIToFrameworkStringValue",
		"FromPtrBool":                  "APIToFrameworkBool",
		"FromBool":                     "APIToFrameworkBoolValue",
		"FromPtrInt64":                 "APIToFrameworkInt64",
		"FromInt64":                    "APIToFrameworkInt64Value",
		"FromPtrInt32":                 "APIToFrameworkInt32AsInt64",
		"FromInt32":                    "APIToFrameworkInt32ValueAsInt64",
		"FromPtrFloat64":               "APIToFrameworkFloat64",
		"FromFloat64":                  "APIToFrameworkFloat64Value",
		"FromPtrFloat32":               "APIToFrameworkFloat32AsFloat64",
		"FromFloat32":                  "APIToFrameworkFloat32ValueAsFloat64",
		"FromPtrTime":                  "APIToFrameworkTime",
		"FromPtrDateOnly":              "APIToFrameworkDateOnly",
		"FromPtrUUID":                  "APIToFrameworkUUID",
		"FromUUIDSlice":                "APIToFrameworkUUIDList",
		"FromTimeSlice":                "APIToFrameworkTimeList",
		"FromBytesBase64":              "APIToFrameworkBytesAsBase64",
		"FromTime":                     "APIToFrameworkTimeValue",
		"FromStringSlice":              "APIToFrameworkStringList",
		"FromStringMap":                "APIToFrameworkStringMap",
		"FromStringMapAdditionalData":  "APIToFrameworkStringMapAdditionalData",
		"FromBoolMapAdditionalData":    "APIToFrameworkBoolMapAdditionalData",
		"FromInt64MapAdditionalData":   "APIToFrameworkInt64MapAdditionalData",
		"FromFloat64MapAdditionalData": "APIToFrameworkFloat64MapAdditionalData",
		"FromBoolMap":                  "APIToFrameworkBoolMap",
		"FromInt64Map":                 "APIToFrameworkInt64Map",
		"FromFloat64Map":               "APIToFrameworkFloat64Map",
		"FromBoolSlice":                "APIToFrameworkBoolList",
		"FromInt64Slice":               "APIToFrameworkInt64List",
		"FromInt32Slice":               "APIToFrameworkInt32ListAsInt64",
		"FromFloat64Slice":             "APIToFrameworkFloat64List",
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

// writeConversion is one resolved write-direction conversion.
type writeConversion struct {
	fn         string
	needsCtx   bool
	returnsErr bool
	// parser is the enum parse companion argument, empty when the
	// function takes none.
	parser string
}

// writeConvert resolves one write-direction shorthand.
func writeConvert(fb *sdkbind.FieldBinding) (writeConversion, error) {
	simple := map[string]writeConversion{
		"ToPtrString":                {fn: "FrameworkToAPIString"},
		"ToString":                   {fn: "FrameworkToAPIStringValue"},
		"ToPtrBool":                  {fn: "FrameworkToAPIBool"},
		"ToBool":                     {fn: "FrameworkToAPIBoolValue"},
		"ToPtrInt64":                 {fn: "FrameworkToAPIInt64"},
		"ToInt64":                    {fn: "FrameworkToAPIInt64Value"},
		"ToPtrInt32":                 {fn: "FrameworkToAPIInt64AsInt32", returnsErr: true},
		"ToInt32":                    {fn: "FrameworkToAPIInt64ValueAsInt32", returnsErr: true},
		"ToPtrFloat64":               {fn: "FrameworkToAPIFloat64"},
		"ToFloat64":                  {fn: "FrameworkToAPIFloat64Value"},
		"ToPtrFloat32":               {fn: "FrameworkToAPIFloat64AsFloat32"},
		"ToFloat32":                  {fn: "FrameworkToAPIFloat64ValueAsFloat32"},
		"ToPtrTime":                  {fn: "FrameworkToAPITime", returnsErr: true},
		"ToPtrDateOnly":              {fn: "FrameworkToAPIDateOnly", returnsErr: true},
		"ToPtrUUID":                  {fn: "FrameworkToAPIUUID", returnsErr: true},
		"ToUUIDSlice":                {fn: "FrameworkToAPIUUIDList", needsCtx: true, returnsErr: true},
		"ToTimeSlice":                {fn: "FrameworkToAPITimeList", needsCtx: true, returnsErr: true},
		"ToBytesBase64":              {fn: "FrameworkToAPIBytesAsBase64", returnsErr: true},
		"ToTime":                     {fn: "FrameworkToAPITimeValue", returnsErr: true},
		"ToStringSlice":              {fn: "FrameworkToAPIStringList", needsCtx: true, returnsErr: true},
		"ToStringMap":                {fn: "FrameworkToAPIStringMap", needsCtx: true, returnsErr: true},
		"ToStringMapAdditionalData":  {fn: "FrameworkToAPIStringMapAdditionalData", needsCtx: true, returnsErr: true},
		"ToBoolMapAdditionalData":    {fn: "FrameworkToAPIBoolMapAdditionalData", needsCtx: true, returnsErr: true},
		"ToInt64MapAdditionalData":   {fn: "FrameworkToAPIInt64MapAdditionalData", needsCtx: true, returnsErr: true},
		"ToFloat64MapAdditionalData": {fn: "FrameworkToAPIFloat64MapAdditionalData", needsCtx: true, returnsErr: true},
		"ToBoolMap":                  {fn: "FrameworkToAPIBoolMap", needsCtx: true, returnsErr: true},
		"ToInt64Map":                 {fn: "FrameworkToAPIInt64Map", needsCtx: true, returnsErr: true},
		"ToFloat64Map":               {fn: "FrameworkToAPIFloat64Map", needsCtx: true, returnsErr: true},
		"ToBoolSlice":                {fn: "FrameworkToAPIBoolList", needsCtx: true, returnsErr: true},
		"ToInt64Slice":               {fn: "FrameworkToAPIInt64List", needsCtx: true, returnsErr: true},
		"ToInt32Slice":               {fn: "FrameworkToAPIInt64ListAsInt32", needsCtx: true, returnsErr: true},
		"ToFloat64Slice":             {fn: "FrameworkToAPIFloat64List", needsCtx: true, returnsErr: true},
	}
	if plan, ok := simple[fb.Access.ConvertSet]; ok {
		return plan, nil
	}
	switch fb.Access.ConvertSet {
	case "ToPtrEnum":
		if kiotaEnum(fb.Access.ParseFunc) {
			return writeConversion{fn: "FrameworkToAPIEnum", returnsErr: true, parser: fb.Access.ParseFunc}, nil
		}
		return writeConversion{fn: "FrameworkToAPIEnumString"}, nil
	case "ToEnum":
		if !kiotaEnum(fb.Access.ParseFunc) {
			return writeConversion{fn: "FrameworkToAPIEnumStringValue"}, nil
		}
	case "ToEnumSlice":
		if kiotaEnum(fb.Access.ParseFunc) {
			return writeConversion{fn: "FrameworkToAPIEnumSlice", needsCtx: true, returnsErr: true, parser: fb.Access.ParseFunc}, nil
		}
		return writeConversion{fn: "FrameworkToAPIEnumStringSlice", needsCtx: true, returnsErr: true}, nil
	}
	return writeConversion{}, fmt.Errorf("attribute %s: the conversion catalog has no write bridge for %q (SDK type %s)",
		fb.Attr, fb.Access.ConvertSet, fb.Access.SDKType)
}

// call renders the finished conversion call for one write.
func (p writeConversion) call(value, setter string) string {
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
