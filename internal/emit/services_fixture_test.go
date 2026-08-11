package emit

import (
	"time"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/sdkbind"
)

// The fictional petstore: one deliberately small model exercising every
// emission feature — scalar, enum, nested-object and list attributes, a
// create-only attribute, a silently-ignored-on-update attribute, a
// missing-update resource, a conditional requirement, eventual
// consistency, a companion and a lookup-by-key datasource, a list
// resource and an action. The bindings are hand-written in the shape
// pruning leaves them: post-prune spellings against the stub SDK under
// testdata/entitysdk.

func names(key, pascal, service string) ir.Names {
	return ir.Names{
		Key:           key,
		Pascal:        pascal,
		Camel:         "x",
		TerraformType: "petstore_" + key,
		Package:       "pkg" + service,
		Service:       service,
		APIVersionDir: "v7",
	}
}

func fictionalModel() *ir.Model {
	httpServerSchema := &ir.AttributeTree{
		Attributes: []ir.Attribute{
			{Name: "id", WireName: "id", Kind: ir.TypeString, Presence: ir.PresenceComputed},
			{Name: "name", WireName: "name", Kind: ir.TypeString, Presence: ir.PresenceRequired},
			{Name: "enabled", WireName: "enabled", Kind: ir.TypeBool, Presence: ir.PresenceOptional},
			{Name: "port", WireName: "port", Kind: ir.TypeInt64, Presence: ir.PresenceOptional, RequiresReplace: true},
			{Name: "ratio", WireName: "ratio", Kind: ir.TypeFloat64, Presence: ir.PresenceOptional},
			{Name: "kind", WireName: "kind", Kind: ir.TypeString, Presence: ir.PresenceOptional,
				OneOf: []string{"basic", "advanced"}},
			{Name: "tags", WireName: "tags", Kind: ir.TypeList, ElemKind: ir.TypeString, Presence: ir.PresenceOptional},
			{Name: "protocols", WireName: "protocols", Kind: ir.TypeList, ElemKind: ir.TypeString, Presence: ir.PresenceOptional,
				OneOf: []string{"http", "https"}},
			{Name: "seed", WireName: "seed", Kind: ir.TypeString, Presence: ir.PresenceOptional, SilentlyIgnoredOnUpdate: true},
			{Name: "settings", WireName: "settings", Kind: ir.TypeObject, Presence: ir.PresenceOptional,
				Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "retries", WireName: "retries", Kind: ir.TypeInt64, Presence: ir.PresenceOptional},
					{Name: "trace", WireName: "trace", Kind: ir.TypeBool, Presence: ir.PresenceOptional},
				}}},
			{Name: "rules", WireName: "rules", Kind: ir.TypeList, ElemKind: ir.TypeObject, Presence: ir.PresenceOptional,
				Nested: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "pattern", WireName: "pattern", Kind: ir.TypeString, Presence: ir.PresenceRequired},
				}}},
			{Name: "blob", WireName: "blob", Presence: ir.PresenceOptional,
				Unsupported: true, UnsupportedReason: "free-form object declares no properties"},
			{Name: "description", WireName: "description", Kind: ir.TypeString, Presence: ir.PresenceOptionalComputed},
		},
		ConditionalRequirements: []ir.ConditionalRequirement{
			{Property: "kind", Equals: "advanced", Required: []string{"settings"}},
		},
		ConditionalValidities: []ir.ConditionalValidity{
			{Property: "kind", Equals: "advanced", Valid: []string{"rules"}},
		},
		Dependencies: []ir.Dependency{
			{Attribute: "ratio", Requires: []string{"port"}},
		},
		MutuallyExclusiveGroups: [][]string{{"protocols", "tags"}},
		ValidConfigurations: []ir.ValidConfiguration{{
			Discriminator: "kind",
			Variants: []ir.ConfigVariant{
				{Value: "advanced", Valid: []string{"settings"}},
				{Value: "basic", Valid: []string{"port"}},
			},
		}},
	}

	itemTree := &ir.AttributeTree{Attributes: []ir.Attribute{
		{Name: "id", WireName: "id", Kind: ir.TypeString, Presence: ir.PresenceComputed},
		{Name: "name", WireName: "name", Kind: ir.TypeString, Presence: ir.PresenceComputed},
		{Name: "enabled", WireName: "enabled", Kind: ir.TypeBool, Presence: ir.PresenceComputed},
	}}

	return &ir.Model{
		Provider: ir.Provider{Name: "petstore"},
		Resources: []ir.Resource{
			{
				Names: names("http_server", "HTTPServer", "servers"),
				Ops: ir.Ops{
					Create: &ir.Op{Kind: ir.OpCreate, Method: "POST", PathTemplate: "/v7/http-servers", SuccessCode: 201},
					Read: &ir.Op{Kind: ir.OpRead, Method: "GET", PathTemplate: "/v7/http-servers/{httpServerId}",
						PathParams: []ir.Param{{Name: "httpServerId", Type: ir.TypeString}}, SuccessCode: 200},
					Update: &ir.Op{Kind: ir.OpUpdate, Method: "PATCH", PathTemplate: "/v7/http-servers/{httpServerId}",
						PathParams: []ir.Param{{Name: "httpServerId", Type: ir.TypeString}}, SuccessCode: 200},
					Delete: &ir.Op{Kind: ir.OpDelete, Method: "DELETE", PathTemplate: "/v7/http-servers/{httpServerId}",
						PathParams: []ir.Param{{Name: "httpServerId", Type: ir.TypeString}}, SuccessCode: 204},
					List: &ir.Op{Kind: ir.OpList, Method: "GET", PathTemplate: "/v7/http-servers", SuccessCode: 200},
				},
				Schema:              httpServerSchema,
				UpdateStyle:         ir.UpdateStylePatchMerge,
				EventualConsistency: 30 * time.Second,
				DeleteNotFoundOK:    true,
				ListEnvelopeKey:     "value",
				Timeouts: ir.Timeouts{
					Create: 30 * time.Minute, Read: 5 * time.Minute,
					Update: 30 * time.Minute, Delete: 30 * time.Minute,
				},
				CoManagementNote: "Fields of this entity may also be managed by petstore_http_server_restart.",
			},
			{
				Names: names("alert_rule", "AlertRule", "alerts"),
				Ops: ir.Ops{
					Create: &ir.Op{Kind: ir.OpCreate, Method: "POST", PathTemplate: "/v7/alert-rules", SuccessCode: 201},
					Read: &ir.Op{Kind: ir.OpRead, Method: "GET", PathTemplate: "/v7/alert-rules/{alertRuleId}",
						PathParams: []ir.Param{{Name: "alertRuleId", Type: ir.TypeString}}, SuccessCode: 200},
					Delete: &ir.Op{Kind: ir.OpDelete, Method: "DELETE", PathTemplate: "/v7/alert-rules/{alertRuleId}",
						PathParams: []ir.Param{{Name: "alertRuleId", Type: ir.TypeString}}, SuccessCode: 204},
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "id", WireName: "id", Kind: ir.TypeString, Presence: ir.PresenceComputed},
					{Name: "name", WireName: "name", Kind: ir.TypeString, Presence: ir.PresenceRequired, RequiresReplace: true},
					{Name: "severity", WireName: "severity", Kind: ir.TypeInt64, Presence: ir.PresenceOptional, RequiresReplace: true},
				}},
				MissingUpdate: true,
				Timeouts: ir.Timeouts{
					Create: 30 * time.Minute, Read: 5 * time.Minute,
					Update: 30 * time.Minute, Delete: 30 * time.Minute,
				},
			},
		},
		Datasources: []ir.Datasource{
			{
				Names: names("http_server", "HTTPServer", "servers"),
				Ops: ir.Ops{
					Read: &ir.Op{Kind: ir.OpRead, Method: "GET", PathTemplate: "/v7/http-servers/{httpServerId}",
						PathParams: []ir.Param{{Name: "httpServerId", Type: ir.TypeString}}, SuccessCode: 200},
					List: &ir.Op{Kind: ir.OpList, Method: "GET", PathTemplate: "/v7/http-servers", SuccessCode: 200},
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "filter_type", WireName: "filter_type", Kind: ir.TypeString, Presence: ir.PresenceRequired},
					{Name: "filter_value", WireName: "filter_value", Kind: ir.TypeString, Presence: ir.PresenceOptional},
					{Name: "items", WireName: "items", Kind: ir.TypeList, ElemKind: ir.TypeObject,
						Presence: ir.PresenceComputed, Nested: itemTree},
				}},
				ListEnvelopeKey: "value",
			},
			{
				Names: names("license", "License", "licenses"),
				Ops: ir.Ops{
					Read: &ir.Op{Kind: ir.OpRead, Method: "GET", PathTemplate: "/v7/licenses/{licenseKey}",
						PathParams: []ir.Param{{Name: "licenseKey", Type: ir.TypeString}}, SuccessCode: 200},
				},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "license_key", WireName: "licenseKey", Kind: ir.TypeString, Presence: ir.PresenceRequired},
					{Name: "seats", WireName: "seats", Kind: ir.TypeInt64, Presence: ir.PresenceComputed},
					{Name: "expires", WireName: "expires", Kind: ir.TypeString, Presence: ir.PresenceComputed},
				}},
				LookupByKey:  true,
				KeyParameter: "licenseKey",
			},
		},
		ListResources: []ir.ListResource{
			{
				Names:  names("audit_event", "AuditEvent", "audit"),
				ListOp: ir.Op{Kind: ir.OpList, Method: "GET", PathTemplate: "/v7/audit-events", SuccessCode: 200},
				Schema: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "id", WireName: "id", Kind: ir.TypeString, Presence: ir.PresenceComputed},
					{Name: "name", WireName: "name", Kind: ir.TypeString, Presence: ir.PresenceComputed},
				}},
				ListEnvelopeKey: "value",
			},
		},
		Actions: []ir.Action{
			{
				Names: names("http_server_restart", "HTTPServerRestart", "servers"),
				InvokeOp: ir.Op{Kind: ir.OpInvoke, Method: "POST",
					PathTemplate: "/v7/http-servers/{httpServerId}/restart",
					PathParams:   []ir.Param{{Name: "httpServerId", Type: ir.TypeString}}, SuccessCode: 204},
				RequestSchema: &ir.AttributeTree{Attributes: []ir.Attribute{
					{Name: "mode", WireName: "mode", Kind: ir.TypeString, Presence: ir.PresenceRequired},
				}},
				ParentEntity: "http_server",
			},
		},
	}
}

// Post-prune access helpers for the kiota-shaped stub SDK.
func kAccess(base, sdkType, convGet, convSet, parse string) sdkbind.FieldAccess {
	return sdkbind.FieldAccess{
		Get: "Get" + base, Set: "Set" + base,
		SDKType: sdkType, ConvertGet: convGet, ConvertSet: convSet, ParseFunc: parse,
	}
}

func readOnly(a sdkbind.FieldAccess) sdkbind.FieldAccess {
	a.Set = ""
	a.ConvertSet = ""
	return a
}

func httpServerFields() []sdkbind.FieldBinding {
	return []sdkbind.FieldBinding{
		{Attr: "id", Wire: "id", Kind: ir.TypeString,
			Access: readOnly(kAccess("Id", "*string", "FromPtrString", "ToPtrString", ""))},
		{Attr: "name", Wire: "name", Kind: ir.TypeString,
			Access: kAccess("Name", "*string", "FromPtrString", "ToPtrString", "")},
		{Attr: "enabled", Wire: "enabled", Kind: ir.TypeBool,
			Access: kAccess("Enabled", "*bool", "FromPtrBool", "ToPtrBool", "")},
		{Attr: "port", Wire: "port", Kind: ir.TypeInt64,
			Access: kAccess("Port", "*int32", "FromPtrInt32", "ToPtrInt32", "")},
		{Attr: "ratio", Wire: "ratio", Kind: ir.TypeFloat64,
			Access: kAccess("Ratio", "*float64", "FromPtrFloat64", "ToPtrFloat64", "")},
		{Attr: "kind", Wire: "kind", Kind: ir.TypeString,
			Access: kAccess("Kind", "*models.HttpServerKind", "FromPtrEnum", "ToPtrEnum", "models.ParseHttpServerKind")},
		{Attr: "tags", Wire: "tags", Kind: ir.TypeList, ElemKind: ir.TypeString,
			Access: kAccess("Tags", "[]string", "FromStringSlice", "ToStringSlice", "")},
		{Attr: "protocols", Wire: "protocols", Kind: ir.TypeList, ElemKind: ir.TypeString,
			Access: kAccess("Protocols", "[]models.HttpServerProtocol", "FromEnumSlice", "ToEnumSlice", "models.ParseHttpServerProtocol")},
		{Attr: "seed", Wire: "seed", Kind: ir.TypeString,
			Access: kAccess("Seed", "*string", "FromPtrString", "ToPtrString", "")},
		{Attr: "settings", Wire: "settings", Kind: ir.TypeObject,
			Access:            sdkbind.FieldAccess{Get: "GetSettings", Set: "SetSettings", SDKType: "models.HttpServerSettingsable"},
			NestedModel:       "models.HttpServerSettings",
			NestedWriteModel:  "models.HttpServerSettings",
			NestedConstructor: "models.NewHttpServerSettings()",
			Nested: []sdkbind.FieldBinding{
				{Attr: "retries", Wire: "retries", Kind: ir.TypeInt64,
					Access: kAccess("Retries", "*int64", "FromPtrInt64", "ToPtrInt64", "")},
				{Attr: "trace", Wire: "trace", Kind: ir.TypeBool,
					Access: kAccess("Trace", "*bool", "FromPtrBool", "ToPtrBool", "")},
			}},
		{Attr: "rules", Wire: "rules", Kind: ir.TypeList, ElemKind: ir.TypeObject,
			Access:            sdkbind.FieldAccess{Get: "GetRules", Set: "SetRules", SDKType: "[]models.HttpServerRuleable"},
			NestedModel:       "models.HttpServerRule",
			NestedWriteModel:  "models.HttpServerRule",
			NestedConstructor: "models.NewHttpServerRule()",
			Nested: []sdkbind.FieldBinding{
				{Attr: "pattern", Wire: "pattern", Kind: ir.TypeString,
					Access: kAccess("Pattern", "*string", "FromPtrString", "ToPtrString", "")},
			}},
		{Attr: "description", Wire: "description", Kind: ir.TypeString,
			Access: kAccess("Description", "*string", "FromPtrString", "ToPtrString", "")},
	}
}

func call(expr string, params []sdkbind.CallParam, responseType string, results ...string) *sdkbind.Call {
	return &sdkbind.Call{
		Expr:         expr,
		Params:       params,
		ResponseType: responseType,
		Results:      results,
	}
}

func serverParam() []sdkbind.CallParam {
	return []sdkbind.CallParam{{Local: "httpServerId", GoType: "string", Wire: "httpServerId"}}
}

func fictionalBindings() *sdkbind.Bindings {
	info := sdkbind.SDKInfo{
		ImportPath:       "example.test/provider/internal/sdk",
		ModelsImportPath: "example.test/provider/internal/sdk/models",
		ClientTypeName:   "APIClient",
	}

	able := "models.HttpServerable"
	return &sdkbind.Bindings{
		SDK: info,
		Resources: map[string]*sdkbind.ResourceBinding{
			"http_server": {
				Key:              "http_server",
				Create:           call("client.HttpServers().Post(ctx, body, nil)", nil, able, able, "error"),
				Read:             call("client.HttpServers().ByHttpServerId(httpServerId).Get(ctx, nil)", serverParam(), able, able, "error"),
				Update:           call("client.HttpServers().ByHttpServerId(httpServerId).Patch(ctx, body, nil)", serverParam(), able, able, "error"),
				Delete:           call("client.HttpServers().ByHttpServerId(httpServerId).Delete(ctx, nil)", serverParam(), "", "error"),
				ReadModel:        able,
				WriteModel:       "models.HttpServer",
				WriteConstructor: "models.NewHttpServer()",
				Fields:           httpServerFields(),
			},
			"alert_rule": {
				Key:    "alert_rule",
				Create: call("client.AlertRules().Post(ctx, body, nil)", nil, "models.AlertRuleable", "models.AlertRuleable", "error"),
				Read: call("client.AlertRules().ByAlertRuleId(alertRuleId).Get(ctx, nil)",
					[]sdkbind.CallParam{{Local: "alertRuleId", GoType: "string", Wire: "alertRuleId"}},
					"models.AlertRuleable", "models.AlertRuleable", "error"),
				Delete: call("client.AlertRules().ByAlertRuleId(alertRuleId).Delete(ctx, nil)",
					[]sdkbind.CallParam{{Local: "alertRuleId", GoType: "string", Wire: "alertRuleId"}},
					"", "error"),
				ReadModel:        "models.AlertRuleable",
				WriteModel:       "models.AlertRule",
				WriteConstructor: "models.NewAlertRule()",
				Fields: []sdkbind.FieldBinding{
					{Attr: "id", Wire: "id", Kind: ir.TypeString,
						Access: readOnly(kAccess("Id", "*string", "FromPtrString", "ToPtrString", ""))},
					{Attr: "name", Wire: "name", Kind: ir.TypeString,
						Access: kAccess("Name", "*string", "FromPtrString", "ToPtrString", "")},
					{Attr: "severity", Wire: "severity", Kind: ir.TypeInt64,
						Access: kAccess("Severity", "*int64", "FromPtrInt64", "ToPtrInt64", "")},
				},
			},
		},
		Datasources: map[string]*sdkbind.DatasourceBinding{
			"http_server": {
				Key:              "http_server",
				Read:             call("client.HttpServers().ByHttpServerId(httpServerId).Get(ctx, nil)", serverParam(), able, able, "error"),
				List:             call("client.HttpServers().Get(ctx, nil)", nil, "", "models.HttpServerCollectionResponseable", "error"),
				ReadModel:        able,
				ElementType:      able,
				CollectionAccess: "GetValue()",
				Fields: []sdkbind.FieldBinding{
					{Attr: "id", Wire: "id", Kind: ir.TypeString,
						Access: readOnly(kAccess("Id", "*string", "FromPtrString", "", ""))},
					{Attr: "name", Wire: "name", Kind: ir.TypeString,
						Access: readOnly(kAccess("Name", "*string", "FromPtrString", "", ""))},
					{Attr: "enabled", Wire: "enabled", Kind: ir.TypeBool,
						Access: readOnly(kAccess("Enabled", "*bool", "FromPtrBool", "", ""))},
				},
			},
			"license": {
				Key: "license",
				Read: call("client.Licenses().ByLicenseKey(licenseKey).Get(ctx, nil)",
					[]sdkbind.CallParam{{Local: "licenseKey", GoType: "string", Wire: "licenseKey"}},
					"models.Licenseable", "models.Licenseable", "error"),
				ReadModel: "models.Licenseable",
				Fields: []sdkbind.FieldBinding{
					{Attr: "seats", Wire: "seats", Kind: ir.TypeInt64,
						Access: readOnly(kAccess("Seats", "*int64", "FromPtrInt64", "", ""))},
					{Attr: "expires", Wire: "expires", Kind: ir.TypeString,
						Access: readOnly(kAccess("Expires", "*time.Time", "FromPtrTime", "", ""))},
				},
			},
		},
		ListResources: map[string]*sdkbind.ListResourceBinding{
			"audit_event": {
				Key:              "audit_event",
				List:             call("client.AuditEvents().Get(ctx, nil)", nil, "", "models.AuditEventCollectionResponseable", "error"),
				ElementType:      "models.AuditEventable",
				CollectionAccess: "GetValue()",
				Fields: []sdkbind.FieldBinding{
					{Attr: "id", Wire: "id", Kind: ir.TypeString,
						Access: readOnly(kAccess("Id", "*string", "FromPtrString", "", ""))},
					{Attr: "name", Wire: "name", Kind: ir.TypeString,
						Access: readOnly(kAccess("Name", "*string", "FromPtrString", "", ""))},
				},
			},
		},
		Actions: map[string]*sdkbind.ActionBinding{
			"http_server_restart": {
				Key: "http_server_restart",
				Invoke: call("client.HttpServers().ByHttpServerId(httpServerId).Restart().Post(ctx, body, nil)",
					serverParam(), "", "error"),
				WriteModel:       "models.HttpServerRestartRequest",
				WriteConstructor: "models.NewHttpServerRestartRequest()",
				Fields: []sdkbind.FieldBinding{
					{Attr: "mode", Wire: "mode", Kind: ir.TypeString,
						Access: sdkbind.FieldAccess{Set: "SetMode", SDKType: "*string", ConvertSet: "ToPtrString"}},
				},
			},
		},
	}
}

// fictionalProviderCore is the provider-core context the entity renders
// pair with: the same fictional provider, kiota backend, bearer auth.
func fictionalProviderCore() ProviderCore {
	return ProviderCore{
		Module:            "example.test/provider",
		ProviderName:      "petstore",
		RegistryAddress:   "registry.terraform.io/example/petstore",
		EnvPrefix:         "TF_PETSTORE",
		ClientType:        "APIClient",
		ClientConstructor: "NewAPIClient",
		SDKImport:         "example.test/provider/internal/sdk",
		GoVersion:         DefaultGoVersion,
		BackendKiota:      true,
		AuthBearerToken:   true,
	}
}
