package intermediate_representation

import (
	"reflect"
	"testing"
)

func TestDeriveNames(t *testing.T) {
	for _, tc := range []struct {
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
				TerraformType: "acme_tag", Package: "tag",
				Service: "tags", APIVersionDir: "v7",
			},
		},
		{
			name:           "multi-segment key uppercases known acronyms",
			key:            "v7_tests_http_server",
			collectionPath: "/v7/tests/http-server",
			want: Names{
				Key: "tests_http_server", Pascal: "TestsHTTPServer", Camel: "testsHTTPServer",
				TerraformType: "acme_tests_http_server", Package: "testshttpserver",
				Service: "tests", APIVersionDir: "v7",
			},
		},
		{
			name:           "no version defaults to v1",
			key:            "note",
			collectionPath: "/notes",
			want: Names{
				Key: "note", Pascal: "Note", Camel: "note",
				TerraformType: "acme_note", Package: "note",
				Service: "notes", APIVersionDir: "v1",
			},
		},
		{
			name:           "a mid-path version segment is not a version prefix",
			key:            "tests_v2_run",
			collectionPath: "/tests/v2/runs",
			want: Names{
				Key: "tests_v2_run", Pascal: "TestsV2Run", Camel: "testsV2Run",
				TerraformType: "acme_tests_v2_run", Package: "testsv2run",
				Service: "tests", APIVersionDir: "v1",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveNames("acme", tc.key, tc.collectionPath)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("deriveNames(%q, %q):\n got %+v\nwant %+v", tc.key, tc.collectionPath, got, tc.want)
			}
		})
	}
}

// The Go spellings uppercase known acronyms whole, and a leading acronym
// in the camel spelling lowers whole — Go-idiomatic, per the owner ruling.
func TestAcronymCasing(t *testing.T) {
	for _, tc := range []struct {
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
		if got := pascalCase(tc.key); got != tc.pascal {
			t.Errorf("pascalCase(%q) = %q, want %q", tc.key, got, tc.pascal)
		}
		if got := camelCase(tc.key); got != tc.camel {
			t.Errorf("camelCase(%q) = %q, want %q", tc.key, got, tc.camel)
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
