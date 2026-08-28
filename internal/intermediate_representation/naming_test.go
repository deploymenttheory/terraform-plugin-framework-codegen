package intermediate_representation

import (
	"reflect"
	"testing"
)

func TestDeriveNames(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		key            string
		collectionPath string
		want           Names
	}{
		{
			name:           "version prefix factors out",
			key:            "v7_tag",
			collectionPath: "/v7/tags",
			want: Names{
				Key: "tag", Pascal: "Tag", Camel: "tag",
				TerraformType: "acme_tag", Package: "acmetag",
				Service: "tags", APIVersionDirectory: "v7",
			},
		},
		{
			name:           "multi-segment key uppercases known acronyms",
			key:            "v7_tests_http_server",
			collectionPath: "/v7/tests/http-server",
			want: Names{
				Key: "tests_http_server", Pascal: "TestsHTTPServer", Camel: "testsHTTPServer",
				TerraformType: "acme_tests_http_server", Package: "acmetestshttpserver",
				Service: "tests", APIVersionDirectory: "v7",
			},
		},
		{
			name:           "no version defaults to v1",
			key:            "note",
			collectionPath: "/notes",
			want: Names{
				Key: "note", Pascal: "Note", Camel: "note",
				TerraformType: "acme_note", Package: "acmenote",
				Service: "notes", APIVersionDirectory: "v1",
			},
		},
		{
			name:           "a mid-path version segment is not a version prefix",
			key:            "tests_v2_run",
			collectionPath: "/tests/v2/runs",
			want: Names{
				Key: "tests_v2_run", Pascal: "TestsV2Run", Camel: "testsV2Run",
				TerraformType: "acme_tests_v2_run", Package: "acmetestsv2run",
				Service: "tests", APIVersionDirectory: "v1",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := deriveNames("acme", testCase.key, testCase.collectionPath)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Errorf("deriveNames(%q, %q):\n got %+v\nwant %+v", testCase.key, testCase.collectionPath, got, testCase.want)
			}
		})
	}
}

// The Go spellings uppercase known acronyms whole, and a leading acronym
// in the camel spelling lowers whole — Go-idiomatic, per the owner ruling.
func TestAcronymCasing(t *testing.T) {
	for _, testCase := range []struct {
		key, pascal, camel string
	}{
		{"http_server", "HTTPServer", "httpServer"},
		{"api_key", "APIKey", "apiKey"},
		{"id", "ID", "id"},
		{"user_id", "UserID", "userID"},
		{"url", "URL", "url"},
		{"oauth_client", "OAuthClient", "oauthClient"},
		{"dns_record_ip", "DNSRecordIP", "dnsRecordIP"},
		{"plain_name", "PlainName", "plainName"},
	} {
		if got := pascalCase(testCase.key); got != testCase.pascal {
			t.Errorf("pascalCase(%q) = %q, want %q", testCase.key, got, testCase.pascal)
		}
		if got := camelCase(testCase.key); got != testCase.camel {
			t.Errorf("camelCase(%q) = %q, want %q", testCase.key, got, testCase.camel)
		}
	}
}

func TestSnakeCase(t *testing.T) {
	for in, want := range map[string]string{
		"filterType": "filter_type",
		"IPAddress":  "ip_address",
		"tagId":      "tag_id",
		"HTMLBody":   "html_body",
		"kebab-case": "kebab_case",
		"dotted.key": "dotted_key",
		"already":    "already",
		"v2Beta":     "v2_beta",
	} {
		if got := snakeCase(in); got != want {
			t.Errorf("snakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnit_Names_PackageCarriesTheProviderPrefix(t *testing.T) {
	for _, testCase := range []struct{ provider, key, want string }{
		{"jamfpro", "computer_group", "jamfprocomputergroup"},
		{"thousandeyes", "http_server", "thousandeyeshttpserver"},
		{"github", "repository", "githubrepository"},
		// A provider name may carry a hyphen; a package name may not.
		{"my-api", "widget", "myapiwidget"},
	} {
		if got := packageName(testCase.provider, testCase.key); got != testCase.want {
			t.Fatalf("packageName(%q, %q) = %q, want %q", testCase.provider, testCase.key, got, testCase.want)
		}
	}
}

func TestUnit_Names_PackageIsNeverAGoKeyword(t *testing.T) {
	// The prefix is what makes this true for every key at once, rather than
	// escaping the reserved words one at a time.
	keywords := []string{
		"break", "case", "chan", "const", "continue", "default", "defer", "else",
		"fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
		"map", "package", "range", "return", "select", "struct", "switch", "type", "var",
	}
	reserved := map[string]bool{}
	for _, k := range keywords {
		reserved[k] = true
	}
	for _, k := range keywords {
		if got := packageName("jamfpro", k); reserved[got] {
			t.Fatalf("packageName(jamfpro, %q) = %q, which is a reserved word", k, got)
		}
	}
}

// TestUnit_TerraformName_StartsWithALetter proves an attribute name never
// begins with what a tfsdk tag may not. The framework's schema rule admits a
// leading underscore and the reflect layer that decodes a model does not, so
// a property named _links would reach the schema and then fail to decode.
func TestUnit_TerraformName_StartsWithALetter(t *testing.T) {
	for _, c := range []struct{ wire, want string }{
		{"_links", "links"},
		{"__internal", "internal"},
		{"_2fa", "fa"},
		{"links", "links"},
		{"createdAt", "created_at"},
		{"___", "___"},
	} {
		if got := TerraformName(c.wire); got != c.want {
			t.Errorf("TerraformName(%q) = %q, want %q", c.wire, got, c.want)
		}
	}
}
