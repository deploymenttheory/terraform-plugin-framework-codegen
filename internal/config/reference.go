package config

// docs/config.md is generated, not written: Reference walks the Config
// struct by reflection, so the reference can only describe keys the decoder
// actually reads: a documented key nothing consumes is structurally
// impossible, and the bidirectional test over the descriptions map makes a
// description with no key impossible too.

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/spf13/viper"
)

// descriptions is the hand-maintained one-liner for every leaf key in the
// schema. TestUnit_Config_DescriptionsMatchSchemaBothWays holds this map and
// the struct to each other: a leaf key without a description fails, and so
// does a description whose key no longer exists.
var descriptions = map[string]string{
	"version":                     "Schema version of this file; must equal the schema version the installed toolkit understands.",
	"provider.name":               "Provider name as published to the registry; a lowercase DNS label.",
	"provider.registry_namespace": "Registry namespace the provider is published under; a lowercase DNS label.",
	"generator.version":           "Exact toolkit release tag (vX.Y.Z) the pipeline installs; branch names are refused.",
	"spec.document_url":           "http(s) URL of the upstream OpenAPI document that `tfpfgen spec import` pins by hash.",
	"sdk.backend":                 "SDK generator behind the common interface; exactly one per provider repo.",
	"sdk.backend_version":         "Exact version pin of the backend tool to install.",
	"sdk.client_type_name":        "Go type name of the generated SDK client.",
	"sdk.include_paths":           "Spec paths to include in SDK generation; empty means every path.",
	"sdk.exclude_paths":           "Spec paths to exclude from SDK generation.",
	"auth.method":                 "How the audit authenticates against the live API; decides which secrets are required.",
	"auth.api_key_header":         "Header name the API key is sent in; required by and only meaningful for `api_key_header`.",
	"auth.token_url":              "OAuth2 token endpoint; required when the method is `oauth2_client_credentials`.",
	"audit.enabled":               "Whether the credentialed live-API audit stage runs at all.",
	"audit.base_url_override":     "Base URL the audit calls instead of the server named in the spec.",
	"audit.name_prefix":           "Prefix every live object the audit creates carries; cleanup matches on it.",
	"audit.max_objects":           "Budget of live objects one audit run may create.",
	"audit.rate_limit_rps":        "Requests per second the audit may send to the live API.",
	"audit.auto_accept":           "Observation kinds whose compiled corrections `tfpfgen spec revise` accepts without waiting for a human.",
	"services.exclude":            "Spec entities that become no provider code.",
}

// allowedValues names the closed value sets for enum-like keys, sourced from
// the same constants the validator enforces.
func allowedValues() map[string][]string {
	return map[string][]string{
		"sdk.backend":       {BackendKiota, BackendOpenAPIGenerator},
		"auth.method":       authMethods(),
		"audit.auto_accept": autoAcceptKinds(),
	}
}

// leaf is one documentable key: its full dotted path and its rendered type.
type leaf struct {
	path string
	typ  string
}

// section groups the leaves under one top-level key. Root scalars such as
// version form the leading unnamed section.
type section struct {
	name   string
	leaves []leaf
}

// sections walks the Config struct in declaration order. Struct-typed fields
// become named sections; their leaves are collected recursively so a future
// deeper nesting still documents itself.
func sections() []section {
	t := reflect.TypeOf(Config{})
	root := section{}
	var named []section
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key := f.Tag.Get("mapstructure")
		if f.Type.Kind() == reflect.Struct {
			named = append(named, section{name: key, leaves: collectLeaves(f.Type, key)})
		} else {
			root.leaves = append(root.leaves, leaf{path: key, typ: typeName(f.Type)})
		}
	}
	return append([]section{root}, named...)
}

// collectLeaves flattens a struct type into dotted leaf paths.
func collectLeaves(t reflect.Type, prefix string) []leaf {
	var out []leaf
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		path := prefix + "." + f.Tag.Get("mapstructure")
		if f.Type.Kind() == reflect.Struct {
			out = append(out, collectLeaves(f.Type, path)...)
			continue
		}
		out = append(out, leaf{path: path, typ: typeName(f.Type)})
	}
	return out
}

// typeName renders a Go type as the vocabulary the reference uses.
func typeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Slice:
		return "list of " + typeName(t.Elem())
	default:
		return t.Kind().String()
	}
}

// defaultValues renders every registered default through a real viper
// instance, so the documented default is the one a decode actually applies.
func defaultValues() map[string]string {
	v := viper.New()
	setDefaults(v)
	out := make(map[string]string, len(v.AllKeys()))
	for _, key := range v.AllKeys() {
		switch value := v.Get(key).(type) {
		case string:
			out[key] = fmt.Sprintf("`%q`", value)
		default:
			out[key] = fmt.Sprintf("`%v`", value)
		}
	}
	return out
}

// Reference renders the complete markdown reference of tfpfgen.yaml:
// every key grouped by section with type, default, and description, the
// closed enum sets, and the auth-method→secret contract. docs/config.md is
// exactly this string; TestUnit_Config_ReferenceMatchesDocs holds it there.
func Reference() string {
	var b strings.Builder
	b.WriteString("# tfpfgen.yaml reference\n\n")
	b.WriteString("<!-- Generated by internal/config.Reference — do not edit by hand.\n")
	b.WriteString("     Regenerate: UPDATE_DOCS=1 go test ./internal/config -run TestUnit_Config_ReferenceMatchesDocs -->\n\n")
	b.WriteString("The schema is owned by `internal/config`; this page is generated from it, ")
	b.WriteString("so every key below is one the decoder reads. Unknown keys fail the decode ")
	fmt.Fprintf(&b, "with a did-you-mean suggestion. The current schema version is %d.\n", SchemaVersion)

	defaults := defaultValues()
	enums := allowedValues()
	for _, s := range sections() {
		b.WriteString("\n## ")
		if s.name == "" {
			b.WriteString("Top-level keys")
		} else {
			fmt.Fprintf(&b, "`%s`", s.name)
		}
		b.WriteString("\n\n| Key | Type | Default | Description |\n|---|---|---|---|\n")
		for _, l := range s.leaves {
			def, ok := defaults[l.path]
			if !ok {
				def = "—"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", l.path, l.typ, def, descriptions[l.path])
		}
		for _, l := range s.leaves {
			values, ok := enums[l.path]
			switch {
			case !ok:
			case strings.HasPrefix(l.typ, "list of"):
				// A list key's closed set constrains each entry, not the
				// whole value; saying so keeps the reference readable as a
				// rule rather than as a required value.
				fmt.Fprintf(&b, "\nEach `%s` entry is one of: %s.\n", l.path, backtickList(values))
			default:
				fmt.Fprintf(&b, "\n`%s` is a closed set: %s.\n", l.path, backtickList(values))
			}
		}
	}

	b.WriteString("\n## Secrets\n\n")
	b.WriteString("Secret values never appear in tfpfgen.yaml. Each auth method reads a fixed ")
	b.WriteString("set of environment variables — the names are a contract, not configuration:\n\n")
	b.WriteString("| `auth.method` | Required secrets |\n|---|---|\n")
	for _, method := range authMethods() {
		fmt.Fprintf(&b, "| `%s` | %s |\n", method, backtickList(RequiredSecrets(method)))
	}
	return b.String()
}

// backtickList joins values as `a`, `b`, `c`.
func backtickList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "`" + v + "`"
	}
	return strings.Join(quoted, ", ")
}
