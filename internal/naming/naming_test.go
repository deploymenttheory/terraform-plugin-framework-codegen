package naming

import (
	"regexp"
	"testing"
)

func testOptions() Options {
	return Options{StripPrefix: DefaultStripPrefix}
}

// TestUnit_Naming_TerraformName is the most valuable table here. Acronym runs
// are where every camel-to-snake implementation breaks, so the cases are seeded
// from property names that actually appear in the ThousandEyes specification
// plus the acronym shapes known to be hard.
func TestUnit_Naming_TerraformName(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		// Straightforward camelCase, the common case.
		{"displayName", "display_name"},
		{"testName", "test_name"},
		{"createDate", "create_date"},
		{"modifiedDate", "modified_date"},
		{"accessType", "access_type"},
		{"matchType", "match_type"},
		{"objectType", "object_type"},
		{"followRedirects", "follow_redirects"},
		{"httpTimeLimit", "http_time_limit"},

		// Already snake or single words.
		{"color", "color"},
		{"key", "key"},
		{"value", "value"},
		{"type", "type"},
		{"already_snake", "already_snake"},

		// Trailing acronyms.
		{"legacyId", "legacy_id"},
		{"id", "id"},
		{"aid", "aid"},
		{"testId", "test_id"},
		{"agentId", "agent_id"},
		{"accountGroupId", "account_group_id"},

		// Leading and embedded acronym runs: the hard cases. An acronym run
		// followed by a lower-case letter must split before the final capital.
		{"HTTPProxy", "http_proxy"},
		{"URLString", "url_string"},
		{"DNSServer", "dns_server"},
		{"BGPMonitor", "bgp_monitor"},
		{"OSVersion", "os_version"},
		{"iOSVersion", "i_os_version"},
		{"APIEndpoint", "api_endpoint"},

		// A pure acronym has no internal split.
		{"URL", "url"},
		{"ID", "id"},
		{"HTTP", "http"},

		// Digits attach to the preceding word rather than starting a new one.
		{"ipv6Address", "ipv6_address"},
		{"sha256Digest", "sha256_digest"},

		// Non-alphanumeric input: HAL envelopes, dotted paths, hyphens.
		{"_links", "links"},
		{"tag.color", "tag_color"},
		{"agent-to-agent", "agent_to_agent"},
		{"a..b", "a_b"},
		{"  spaced  name  ", "spaced_name"},

		// Degenerate input must not panic or produce a leading separator.
		{"", ""},
		{"_", ""},
		{"___", ""},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := TerraformName(tc.in); got != tc.want {
				t.Errorf("TerraformName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestUnit_Naming_TerraformName_IsIdempotent matters because a name may pass
// through conversion more than once as it moves between layers, and a second
// pass must not mangle it.
func TestUnit_Naming_TerraformName_IsIdempotent(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"displayName", "legacyId", "HTTPProxy", "iOSVersion", "_links",
		"tag.color", "ipv6Address", "URL", "agent-to-agent", "",
	}

	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			once := TerraformName(in)
			if twice := TerraformName(once); twice != once {
				t.Errorf("TerraformName(%q) = %q, but applying it again gave %q", in, once, twice)
			}
		})
	}
}

func TestUnit_Naming_GoFieldName(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"legacyId", "LegacyID"},
		{"id", "ID"},
		{"aid", "AID"},
		{"testId", "TestID"},
		{"displayName", "DisplayName"},
		{"createDate", "CreateDate"},
		{"url", "URL"},
		{"apiEndpoint", "APIEndpoint"},
		{"targetUrl", "TargetURL"},
		{"someUri", "SomeURI"},
		// UUID must win over ID, which is a suffix of it. Order in the
		// Initialisms table is what guarantees this.
		{"resourceUuid", "ResourceUUID"},
		{"accessType", "AccessType"},
		{"_links", "Links"},
		{"", "Value"},
	}

	o := testOptions()
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := o.GoFieldName(tc.in); got != tc.want {
				t.Errorf("GoFieldName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUnit_Naming_GoTypeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
	}{
		{"simple", "tag", "Tag"},
		{"acronym word is capitalised", "bgpTest", "BGPTest"},
		{"separators are removed", "http-server_test", "HTTPServerTest"},
		// The specification namespace prefix carries no information inside a
		// package that already belongs to that domain.
		{"api namespace stripped", "Tests_API_BgpTest", "BGPTest"},
		{"nested api namespace stripped", "Alerts_Rules_API_Link", "Link"},
		{"leading digit is prefixed", "7layer", "N7layer"},
		{"empty falls back", "", "Value"},
		{"punctuation only falls back", "...", "Value"},
	}

	o := testOptions()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := o.GoTypeName(tc.in); got != tc.want {
				t.Errorf("GoTypeName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestUnit_Naming_GoTypeName_NeverProducesInvalidGo is the property that matters
// more than any single case: whatever a specification contains, the result must
// be usable as a Go identifier.
func TestUnit_Naming_GoTypeName_NeverProducesInvalidGo(t *testing.T) {
	t.Parallel()

	valid := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	inputs := []string{
		"", " ", "...", "123", "1a", "_", "a-b", "a.b.c", "@!#", "9lives",
		"Tests_API_", "type", "func", "-", "é", "tag/color", "___", "9",
	}

	o := testOptions()
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			for _, got := range []string{o.GoTypeName(in), o.GoFieldName(in)} {
				if !valid.MatchString(got) {
					t.Errorf("naming %q produced %q, which is not a valid Go identifier", in, got)
				}
			}
		})
	}
}

// TestUnit_Naming_SplitWords_IsTheSingleSourceOfTruth pins the word boundaries
// every other conversion depends on. A disagreement here is what produces a
// model field whose tfsdk tag does not match its schema attribute name.
func TestUnit_Naming_SplitWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want []string
	}{
		{"displayName", []string{"display", "name"}},
		{"HTTPProxy", []string{"http", "proxy"}},
		{"iOSVersion", []string{"i", "os", "version"}},
		{"ipv6Address", []string{"ipv6", "address"}},
		{"URL", []string{"url"}},
		{"_links", []string{"links"}},
		{"tag.color", []string{"tag", "color"}},
		{"agent-to-agent", []string{"agent", "to", "agent"}},
		{"already_snake", []string{"already", "snake"}},
		{"", nil},
		{"___", nil},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			got := SplitWords(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("SplitWords(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("SplitWords(%q) = %q, want %q", tc.in, got, tc.want)
				}
			}
		})
	}
}

// TestUnit_Naming_TerraformNameAndGoFieldName_AgreeOnWordBoundaries is the
// cross-check that matters: the tfsdk tag and the Go field are generated from the
// same property name by different joins, so if they ever disagree about word
// boundaries the emitted model is silently wrong.
func TestUnit_Naming_TerraformNameAndGoFieldName_AgreeOnWordBoundaries(t *testing.T) {
	t.Parallel()

	// Every scalar property name on the ThousandEyes Tag model.
	properties := []string{
		"id", "aid", "key", "value", "color", "description", "icon",
		"accessType", "matchType", "objectType", "type", "builtIn",
		"createDate", "modifiedDate", "legacyId",
	}

	o := testOptions()
	for _, p := range properties {
		t.Run(p, func(t *testing.T) {
			t.Parallel()

			tfWords := len(SplitWords(TerraformName(p)))
			goWords := len(SplitWords(o.GoFieldName(p)))
			if tfWords != goWords {
				t.Errorf("property %q split into %d words as a Terraform name (%q) but %d as a Go field (%q)",
					p, tfWords, TerraformName(p), goWords, o.GoFieldName(p))
			}
		})
	}
}

func TestUnit_Naming_TerraformTypeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
		parts  []string
		want   string
	}{
		{"prefix and resource", "thousandeyes", []string{"tag"}, "thousandeyes_tag"},
		{"with a service group", "thousandeyes", []string{"tests", "httpServer"}, "thousandeyes_tests_http_server"},
		// An empty component must not produce a doubled separator, which would
		// be an invalid Terraform type name.
		{"empty components are skipped", "thousandeyes", []string{"", "tag"}, "thousandeyes_tag"},
		{"camel components are converted", "thousandeyes", []string{"alertRule"}, "thousandeyes_alert_rule"},
		{"no prefix", "", []string{"tag"}, "tag"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TerraformTypeName(tc.prefix, tc.parts...); got != tc.want {
				t.Errorf("TerraformTypeName(%q, %q) = %q, want %q", tc.prefix, tc.parts, got, tc.want)
			}
		})
	}
}

func TestUnit_Naming_SafeIdentifier_AvoidsShadowingImports(t *testing.T) {
	t.Parallel()

	// Every one of these is an imported package name or a local variable the
	// generated CRUD skeleton declares. Shadowing any of them produces code that
	// reads as a template bug. The archetype provider's own template shadows
	// "resource" and does not compile, which is why this is a test.
	mustBeRenamed := []string{
		"resource", "schema", "types", "path", "convert", "crud", "client",
		"timeouts", "planmodifier", "validator", "diag", "tflog", "errors",
		"ctx", "req", "resp", "err", "plan", "state", "data", "opts", "r",
		"type", "func", "range", "string", "error", "len", "new",
	}

	for _, in := range mustBeRenamed {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			if got := SafeIdentifier(in); got == in {
				t.Errorf("SafeIdentifier(%q) returned it unchanged; it would shadow an import or a declared local", in)
			}
		})
	}

	// Ordinary names must pass through untouched, or every generated variable
	// would carry a pointless suffix.
	for _, in := range []string{"tagID", "displayName", "filters", "color"} {
		t.Run("unchanged/"+in, func(t *testing.T) {
			t.Parallel()
			if got := SafeIdentifier(in); got != in {
				t.Errorf("SafeIdentifier(%q) = %q, want it unchanged", in, got)
			}
		})
	}
}

func TestUnit_Naming_PackageAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parts []string
		want  string
	}{
		{"version and resource", []string{"v7", "tag"}, "v7Tag"},
		{"multi word resource", []string{"v7", "http_server_test"}, "v7HTTPServerTest"},
		{"single component", []string{"tag"}, "tag"},
		{"empty falls back", nil, "pkg"},
	}

	o := testOptions()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := o.PackageAlias(tc.parts...); got != tc.want {
				t.Errorf("PackageAlias(%q) = %q, want %q", tc.parts, got, tc.want)
			}
		})
	}
}

// TestUnit_Naming_PackageAlias_IsNeverReserved guards the failure mode that an
// alias colliding with an imported package name breaks every file using it.
func TestUnit_Naming_PackageAlias_IsNeverReserved(t *testing.T) {
	t.Parallel()

	o := testOptions()
	for _, parts := range [][]string{{"type"}, {"schema"}, {"client"}, {"path"}, {"error"}} {
		t.Run(parts[0], func(t *testing.T) {
			t.Parallel()
			if got := o.PackageAlias(parts...); IsReserved(got) {
				t.Errorf("PackageAlias(%q) = %q, which is reserved", parts, got)
			}
		})
	}
}

func TestUnit_Naming_Unique_ResolvesCollisions(t *testing.T) {
	t.Parallel()

	taken := map[string]bool{}

	if got := Unique(taken, "Tag"); got != "Tag" {
		t.Errorf("first Unique = %q, want %q", got, "Tag")
	}
	if got := Unique(taken, "Tag"); got != "Tag2" {
		t.Errorf("second Unique = %q, want %q", got, "Tag2")
	}
	if got := Unique(taken, "Tag"); got != "Tag3" {
		t.Errorf("third Unique = %q, want %q", got, "Tag3")
	}
	if got := Unique(taken, "Other"); got != "Other" {
		t.Errorf("unrelated Unique = %q, want %q", got, "Other")
	}
}

// TestUnit_Naming_Unique_HandlesDoubleDigitSuffixes exercises itoa's recursive
// branch, which a three-collision test never reaches.
func TestUnit_Naming_Unique_HandlesDoubleDigitSuffixes(t *testing.T) {
	t.Parallel()

	taken := map[string]bool{}
	for i := 0; i < 12; i++ {
		Unique(taken, "Tag")
	}

	for _, want := range []string{"Tag", "Tag2", "Tag10", "Tag12"} {
		if !taken[want] {
			t.Errorf("expected %q to have been allocated; got %v", want, taken)
		}
	}
	if taken["Tag13"] {
		t.Error("allocated one name too many")
	}
}

func TestUnit_Naming_SnakeDirName(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"tag", "tag"},
		{"httpServer", "http_server"},
		{"BGP Tests", "bgp_tests"},
		{"", "misc"},
		// A Go package name cannot begin with a digit.
		{"7layer", "v7layer"},
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := SnakeDirName(tc.in); got != tc.want {
				t.Errorf("SnakeDirName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUnit_Naming_TestNames(t *testing.T) {
	t.Parallel()

	if got := UnitTestName(1, "Tag", "Schema"); got != "TestUnit_Tag_01_Schema" {
		t.Errorf("UnitTestName = %q", got)
	}
	if got := UnitTestName(12, "Tag", "Minimal"); got != "TestUnit_Tag_12_Minimal" {
		t.Errorf("UnitTestName = %q", got)
	}
	if got := AccTestName(1, "Tag", "Minimal"); got != "TestAcc_Tag_01_Minimal" {
		t.Errorf("AccTestName = %q", got)
	}
}
