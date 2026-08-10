package emit

import (
	"strings"
	"testing"
	"time"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/sdkbind"
)

// The openapi-generator-flavoured fixture: value-typed accessors, string
// enums with FromValue companions, scalar slices of every width, a
// value-typed nested object, concrete list elements, and Execute-style
// result shapes carrying *http.Response.

func oagAccess(base, sdkType, convGet, convSet, parse string) sdkbind.FieldAccess {
	return sdkbind.FieldAccess{
		Get: "Get" + base, Set: "Set" + base,
		SDKType: sdkType, ConvertGet: convGet, ConvertSet: convSet, ParseFunc: parse,
	}
}

func oagModel() *ir.Model {
	opt := func(name string, kind ir.TypeKind) ir.Attribute {
		return ir.Attribute{Name: name, WireName: name, Kind: kind, Presence: ir.PresenceOptional}
	}
	list := func(name string, elem ir.TypeKind) ir.Attribute {
		return ir.Attribute{Name: name, WireName: name, Kind: ir.TypeList, ElemKind: elem, Presence: ir.PresenceOptional}
	}

	return &ir.Model{
		Provider: ir.Provider{Name: "petstore"},
		Resources: []ir.Resource{{
			Names: names("tag", "Tag", "tags"),
			Ops: ir.Ops{
				Create: &ir.Op{Kind: ir.OpCreate, Method: "POST", PathTemplate: "/tags", SuccessCode: 201},
				Read: &ir.Op{Kind: ir.OpRead, Method: "GET", PathTemplate: "/tags/{tagId}",
					PathParams: []ir.Param{{Name: "tagId", Type: ir.TypeString}}, SuccessCode: 200},
				Update: &ir.Op{Kind: ir.OpUpdate, Method: "PUT", PathTemplate: "/tags/{tagId}",
					PathParams: []ir.Param{{Name: "tagId", Type: ir.TypeString}}, SuccessCode: 200},
				Delete: &ir.Op{Kind: ir.OpDelete, Method: "DELETE", PathTemplate: "/tags/{tagId}",
					PathParams: []ir.Param{{Name: "tagId", Type: ir.TypeString}}, SuccessCode: 204},
			},
			Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
				{Name: "id", WireName: "id", Kind: ir.TypeString, Presence: ir.PresenceComputed},
				{Name: "name", WireName: "name", Kind: ir.TypeString, Presence: ir.PresenceRequired},
				opt("enabled", ir.TypeBool),
				opt("count", ir.TypeInt64),
				opt("big", ir.TypeInt64),
				opt("score", ir.TypeFloat64),
				opt("weight", ir.TypeFloat64),
				opt("status", ir.TypeString),
				opt("phase", ir.TypeString),
				opt("when", ir.TypeString),
				list("levels", ir.TypeInt64),
				list("flags", ir.TypeBool),
				list("ratios", ir.TypeFloat64),
				list("sizes", ir.TypeInt64),
				list("modes", ir.TypeString),
				{Name: "meta", WireName: "meta", Kind: ir.TypeObject, Presence: ir.PresenceOptional,
					Nested: &ir.AttributeTree{Attributes: []ir.Attribute{opt("note", ir.TypeString)}}},
				{Name: "rules", WireName: "rules", Kind: ir.TypeList, ElemKind: ir.TypeObject, Presence: ir.PresenceOptional,
					Nested: &ir.AttributeTree{Attributes: []ir.Attribute{opt("pattern", ir.TypeString)}}},
			}},
			UpdateStyle: ir.UpdateStylePutFull,
			Timeouts: ir.Timeouts{
				Create: 90 * time.Second, Read: time.Hour,
				Update: 30 * time.Minute, Delete: 500 * time.Millisecond,
			},
		}},
		Actions: []ir.Action{{
			Names: names("tag_rotate", "TagRotate", "tags"),
			InvokeOp: ir.Op{Kind: ir.OpInvoke, Method: "POST", PathTemplate: "/tags/{tagId}/rotate",
				PathParams: []ir.Param{{Name: "tagId", Type: ir.TypeInt64}}, SuccessCode: 200},
			RequestSchema: &ir.AttributeTree{Attributes: []ir.Attribute{
				{Name: "force", WireName: "force", Kind: ir.TypeBool, Presence: ir.PresenceRequired},
				opt("factor", ir.TypeFloat64),
				list("labels", ir.TypeString),
				{Name: "window", WireName: "window", Kind: ir.TypeObject, Presence: ir.PresenceOptional,
					Nested: &ir.AttributeTree{Attributes: []ir.Attribute{opt("hours", ir.TypeInt64)}}},
				{Name: "steps", WireName: "steps", Kind: ir.TypeList, ElemKind: ir.TypeObject, Presence: ir.PresenceOptional,
					Nested: &ir.AttributeTree{Attributes: []ir.Attribute{opt("order", ir.TypeInt64)}}},
			}},
		}},
	}
}

func oagBindings() *sdkbind.Bindings {
	info := sdkbind.SDKInfo{
		ImportPath:       "example.test/provider/internal/sdk",
		ModelsImportPath: "example.test/provider/internal/sdk",
		ClientTypeName:   "APIClient",
	}
	tagParam := []sdkbind.CallParam{{Local: "tagId", GoType: "string", Wire: "tagId"}}

	return &sdkbind.Bindings{
		SDK: info,
		Resources: map[string]*sdkbind.ResourceBinding{
			"tag": {
				Key: "tag",
				Create: call("client.TagsAPI.CreateTag(ctx).Tag(*body).Execute()", nil,
					"*sdk.Tag", "*sdk.Tag", "*http.Response", "error"),
				Read: call("client.TagsAPI.GetTag(ctx, tagId).Execute()", tagParam,
					"*sdk.Tag", "*sdk.Tag", "*http.Response", "error"),
				Update: call("client.TagsAPI.UpdateTag(ctx, tagId).Tag(*body).Execute()", tagParam,
					"*sdk.Tag", "*sdk.Tag", "*http.Response", "error"),
				Delete: call("client.TagsAPI.DeleteTag(ctx, tagId).Execute()", tagParam,
					"", "*http.Response", "error"),
				ReadModel:        "*sdk.Tag",
				WriteModel:       "sdk.Tag",
				WriteConstructor: "sdk.NewTagWithDefaults()",
				Fields: []sdkbind.FieldBinding{
					{Attr: "id", Wire: "id", Kind: ir.TypeString,
						Access: readOnly(oagAccess("Id", "string", "FromString", "ToString", ""))},
					{Attr: "name", Wire: "name", Kind: ir.TypeString,
						Access: oagAccess("Name", "string", "FromString", "ToString", "")},
					{Attr: "enabled", Wire: "enabled", Kind: ir.TypeBool,
						Access: oagAccess("Enabled", "bool", "FromBool", "ToBool", "")},
					{Attr: "count", Wire: "count", Kind: ir.TypeInt64,
						Access: oagAccess("Count", "int32", "FromInt32", "ToInt32", "")},
					{Attr: "big", Wire: "big", Kind: ir.TypeInt64,
						Access: oagAccess("Big", "int64", "FromInt64", "ToInt64", "")},
					{Attr: "score", Wire: "score", Kind: ir.TypeFloat64,
						Access: oagAccess("Score", "float32", "FromFloat32", "ToFloat32", "")},
					{Attr: "weight", Wire: "weight", Kind: ir.TypeFloat64,
						Access: oagAccess("Weight", "float64", "FromFloat64", "ToFloat64", "")},
					{Attr: "status", Wire: "status", Kind: ir.TypeString,
						Access: oagAccess("Status", "sdk.TagStatus", "FromEnum", "ToEnum", "sdk.NewTagStatusFromValue")},
					{Attr: "phase", Wire: "phase", Kind: ir.TypeString,
						Access: oagAccess("Phase", "*sdk.TagPhase", "FromPtrEnum", "ToPtrEnum", "sdk.NewTagPhaseFromValue")},
					{Attr: "when", Wire: "when", Kind: ir.TypeString,
						Access: oagAccess("When", "time.Time", "FromTime", "ToTime", "")},
					{Attr: "levels", Wire: "levels", Kind: ir.TypeList, ElemKind: ir.TypeInt64,
						Access: oagAccess("Levels", "[]int64", "FromInt64Slice", "ToInt64Slice", "")},
					{Attr: "flags", Wire: "flags", Kind: ir.TypeList, ElemKind: ir.TypeBool,
						Access: oagAccess("Flags", "[]bool", "FromBoolSlice", "ToBoolSlice", "")},
					{Attr: "ratios", Wire: "ratios", Kind: ir.TypeList, ElemKind: ir.TypeFloat64,
						Access: oagAccess("Ratios", "[]float64", "FromFloat64Slice", "ToFloat64Slice", "")},
					{Attr: "sizes", Wire: "sizes", Kind: ir.TypeList, ElemKind: ir.TypeInt64,
						Access: oagAccess("Sizes", "[]int32", "FromInt32Slice", "ToInt32Slice", "")},
					{Attr: "modes", Wire: "modes", Kind: ir.TypeList, ElemKind: ir.TypeString,
						Access: oagAccess("Modes", "[]sdk.Mode", "FromEnumSlice", "ToEnumSlice", "sdk.NewModeFromValue")},
					{Attr: "meta", Wire: "meta", Kind: ir.TypeObject,
						Access:            sdkbind.FieldAccess{Get: "GetMeta", Set: "SetMeta", SDKType: "sdk.TagMeta"},
						NestedModel:       "sdk.TagMeta",
						NestedWriteModel:  "sdk.TagMeta",
						NestedConstructor: "sdk.NewTagMetaWithDefaults()",
						Nested: []sdkbind.FieldBinding{
							{Attr: "note", Wire: "note", Kind: ir.TypeString,
								Access: oagAccess("Note", "string", "FromString", "ToString", "")},
						}},
					{Attr: "rules", Wire: "rules", Kind: ir.TypeList, ElemKind: ir.TypeObject,
						Access:            sdkbind.FieldAccess{Get: "GetRules", Set: "SetRules", SDKType: "[]sdk.TagRule"},
						NestedModel:       "sdk.TagRule",
						NestedWriteModel:  "sdk.TagRule",
						NestedConstructor: "sdk.NewTagRuleWithDefaults()",
						Nested: []sdkbind.FieldBinding{
							{Attr: "pattern", Wire: "pattern", Kind: ir.TypeString,
								Access: oagAccess("Pattern", "string", "FromString", "ToString", "")},
						}},
				},
			},
		},
		Actions: map[string]*sdkbind.ActionBinding{
			"tag_rotate": {
				Key: "tag_rotate",
				Invoke: call("client.TagsAPI.RotateTag(ctx, tagId).RotateRequest(*body).Execute()",
					[]sdkbind.CallParam{{Local: "tagId", GoType: "int64", Wire: "tagId"}},
					"", "*http.Response", "error"),
				WriteModel:       "sdk.RotateRequest",
				WriteConstructor: "sdk.NewRotateRequestWithDefaults()",
				Fields: []sdkbind.FieldBinding{
					{Attr: "force", Wire: "force", Kind: ir.TypeBool,
						Access: sdkbind.FieldAccess{Set: "SetForce", SDKType: "bool", ConvertSet: "ToBool"}},
					{Attr: "factor", Wire: "factor", Kind: ir.TypeFloat64,
						Access: sdkbind.FieldAccess{Set: "SetFactor", SDKType: "float64", ConvertSet: "ToFloat64"}},
					{Attr: "labels", Wire: "labels", Kind: ir.TypeList, ElemKind: ir.TypeString,
						Access: sdkbind.FieldAccess{Set: "SetLabels", SDKType: "[]string", ConvertSet: "ToStringSlice"}},
					{Attr: "window", Wire: "window", Kind: ir.TypeObject,
						Access:            sdkbind.FieldAccess{Set: "SetWindow", SDKType: "sdk.RotateWindow"},
						NestedModel:       "sdk.RotateWindow",
						NestedWriteModel:  "sdk.RotateWindow",
						NestedConstructor: "sdk.NewRotateWindowWithDefaults()",
						Nested: []sdkbind.FieldBinding{
							{Attr: "hours", Wire: "hours", Kind: ir.TypeInt64,
								Access: sdkbind.FieldAccess{Set: "SetHours", SDKType: "int64", ConvertSet: "ToInt64"}},
						}},
					{Attr: "steps", Wire: "steps", Kind: ir.TypeList, ElemKind: ir.TypeObject,
						Access:            sdkbind.FieldAccess{Set: "SetSteps", SDKType: "[]sdk.RotateStep"},
						NestedModel:       "sdk.RotateStep",
						NestedWriteModel:  "sdk.RotateStep",
						NestedConstructor: "sdk.NewRotateStepWithDefaults()",
						Nested: []sdkbind.FieldBinding{
							{Attr: "order", Wire: "order", Kind: ir.TypeInt64,
								Access: sdkbind.FieldAccess{Set: "SetOrder", SDKType: "int64", ConvertSet: "ToInt64"}},
						}},
				},
			},
		},
	}
}

func oagProviderCore() ProviderCore {
	pc := fictionalProviderCore()
	pc.BackendKiota = false
	pc.BackendOpenAPIGenerator = true
	pc.AuthBearerToken = false
	pc.AuthBasic = true
	return pc
}

// TestUnit_RenderServices_TheOpenAPIGeneratorDialectRenders drives the
// value-typed accessor and Execute-result shapes through the same
// renderer and holds the conversions to the catalog's value family.
func TestUnit_RenderServices_TheOpenAPIGeneratorDialectRenders(t *testing.T) {
	out, err := RenderServices(oagProviderCore(), oagModel(), oagBindings())
	if err != nil {
		t.Fatalf("RenderServices: %v", err)
	}

	construct := string(fileByPath(t, out, "internal/services/resources/tags/v7/tag/construct.go").Content)
	for _, want := range []string{
		"convert.FrameworkToAPIStringValue(data.Name, body.SetName)",
		"convert.FrameworkToAPIBoolValue(data.Enabled, body.SetEnabled)",
		"convert.FrameworkToAPIInt64ValueAsInt32(data.Count, body.SetCount)",
		"convert.FrameworkToAPIInt64Value(data.Big, body.SetBig)",
		"convert.FrameworkToAPIFloat64ValueAsFloat32(data.Score, body.SetScore)",
		"convert.FrameworkToAPIFloat64Value(data.Weight, body.SetWeight)",
		"convert.FrameworkToAPIEnumStringValue(data.Status, body.SetStatus)",
		"convert.FrameworkToAPIEnumString(data.Phase, body.SetPhase)",
		"convert.FrameworkToAPITimeValue(data.When, body.SetWhen)",
		"convert.FrameworkToAPIInt64List(ctx, data.Levels, body.SetLevels)",
		"convert.FrameworkToAPIBoolList(ctx, data.Flags, body.SetFlags)",
		"convert.FrameworkToAPIFloat64List(ctx, data.Ratios, body.SetRatios)",
		"convert.FrameworkToAPIInt64ListAsInt32(ctx, data.Sizes, body.SetSizes)",
		"convert.FrameworkToAPIEnumStringSlice(ctx, data.Modes, body.SetModes)",
		"body.SetMeta(*metaBody)",
		"rulesList = append(rulesList, *rulesElement)",
	} {
		if !strings.Contains(construct, want) {
			t.Fatalf("construct.go lacks %q:\n%s", want, construct)
		}
	}

	state := string(fileByPath(t, out, "internal/services/resources/tags/v7/tag/state.go").Content)
	for _, want := range []string{
		"data.ID = convert.APIToFrameworkStringValue(remote.GetId())",
		"data.Count = convert.APIToFrameworkInt32ValueAsInt64(remote.GetCount())",
		"data.Score = convert.APIToFrameworkFloat32ValueAsFloat64(remote.GetScore())",
		"data.Status = convert.APIToFrameworkEnumStringValue(remote.GetStatus())",
		"data.Phase = convert.APIToFrameworkEnumString(remote.GetPhase())",
		"data.When = convert.APIToFrameworkTimeValue(remote.GetWhen())",
		"data.Modes = convert.APIToFrameworkEnumStringSlice(remote.GetModes())",
	} {
		if !strings.Contains(state, want) {
			t.Fatalf("state.go lacks %q:\n%s", want, state)
		}
	}
	// The value-typed nested object takes no nil guard.
	if !strings.Contains(state, "value := remote.GetMeta()") || strings.Contains(state, "GetMeta(); value != nil") {
		t.Fatalf("a value-typed nested object must map unguarded:\n%s", state)
	}

	crud := string(fileByPath(t, out, "internal/services/resources/tags/v7/tag/crud.go").Content)
	for _, want := range []string{
		"created, _, err := client.TagsAPI.CreateTag(ctx).Tag(*body).Execute()",
		"_, err := client.TagsAPI.DeleteTag(ctx, tagId).Execute()",
	} {
		if !strings.Contains(crud, want) {
			t.Fatalf("crud.go lacks %q:\n%s", want, crud)
		}
	}

	resource := string(fileByPath(t, out, "internal/services/resources/tags/v7/tag/resource.go").Content)
	for _, want := range []string{
		"createTimeout = 90 * time.Second",
		"readTimeout   = 1 * time.Hour",
		"deleteTimeout = 500000000 * time.Nanosecond",
	} {
		if !strings.Contains(resource, want) {
			t.Fatalf("resource.go lacks %q:\n%s", want, resource)
		}
	}

	invoke := string(fileByPath(t, out, "internal/services/actions/tags/v7/tag_rotate/invoke.go").Content)
	if !strings.Contains(invoke, "tagId := data.TagID.ValueInt64()") {
		t.Fatalf("an int64 path parameter must read through ValueInt64:\n%s", invoke)
	}

	actionTest := string(fileByPath(t, out, "internal/services/actions/tags/v7/tag_rotate/action_test.go").Content)
	for _, want := range []string{
		"tftypes.NewValue(tftypes.Bool, true)",
		"tftypes.NewValue(tftypes.Number, 1.5)",
		"tftypes.List{ElementType: tftypes.String}",
		`tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"hours": tftypes.Number}}`,
		`Username: "unit-test-user"`,
	} {
		if !strings.Contains(actionTest, want) {
			t.Fatalf("action_test.go lacks %q:\n%s", want, actionTest)
		}
	}
}
