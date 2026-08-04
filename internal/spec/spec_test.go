package spec

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-codegen-spec/provider"
	"github.com/hashicorp/terraform-plugin-codegen-spec/resource"
	"github.com/hashicorp/terraform-plugin-codegen-spec/schema"
	hcspec "github.com/hashicorp/terraform-plugin-codegen-spec/spec"
)

// minimalSpec is the smallest document that satisfies the embedded schema, used by
// the tests that are about encoding rather than about mapping.
//
// The provider block is not decoration: the JSON schema lists "provider" and
// "version" as the two required top-level keys, so a document without a provider
// block is invalid however little it has to say about the provider. Only "name" is
// required within it, which is what makes the name-only projection legitimate.
func minimalSpec() hcspec.Specification {
	return hcspec.Specification{
		Version:  SpecVersion,
		Provider: &provider.Provider{Name: "thousandeyes"},
		Resources: resource.Resources{{
			Name: "tag",
			Schema: &resource.Schema{
				Attributes: resource.Attributes{{
					Name: "id",
					String: &resource.StringAttribute{
						ComputedOptionalRequired: schema.Computed,
					},
				}},
			},
		}},
	}
}

// TestUnit_Spec_Version pins the version string.
//
// Four assertions, because each catches a different mistake. The published
// documentation for this format shows "0.1.0"; upstream's validator switches on the
// literal "0.1" and rejects anything else. Anybody who writes the version by hand
// from the docs produces a document no tool will read, and the failure is a bare
// "version is unsupported" a long way from the cause. So the trap is recorded here
// rather than in a comment.
func TestUnit_Spec_Version(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	if SpecVersion != "0.1" {
		t.Fatalf("SpecVersion = %q, want \"0.1\"", SpecVersion)
	}

	data, err := Marshal(minimalSpec())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if !strings.Contains(string(data), `"version": "0.1"`) {
		t.Errorf("the encoded document does not declare the version:\n%s", data)
	}

	if _, err := Parse(ctx, data); err != nil {
		t.Fatalf("our own output must parse: %v", err)
	}

	// The same document with the documented-but-wrong version must be refused, or
	// this test is only asserting that we agree with ourselves.
	wrong := strings.Replace(string(data), `"version": "0.1"`, `"version": "0.1.0"`, 1)
	if wrong == string(data) {
		t.Fatal("the version substitution did not apply, so the rejection case is untested")
	}

	if _, err := Parse(ctx, []byte(wrong)); err == nil {
		t.Error(`"0.1.0" must be rejected: upstream switches on the literal "0.1"`)
	}
}

// TestUnit_Spec_Bytes covers byte-stability and the pointer discipline.
//
// The pointer half is the part worth having: every optional string and bool in the
// official format is a pointer, and writing a zero value rather than nil produces a
// document full of `"sensitive": false` lines that say nothing. On a seventeen
// attribute resource that is the difference between a reviewable document and one
// nobody reads.
func TestUnit_Spec_Bytes(t *testing.T) {
	t.Parallel()

	first, err := Marshal(minimalSpec())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	second, err := Marshal(minimalSpec())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if string(first) != string(second) {
		t.Error("Marshal is not byte-stable for the same value")
	}

	if !strings.HasSuffix(string(first), "\n") {
		t.Error("the encoded document must end with exactly one newline")
	}
	if strings.HasSuffix(string(first), "\n\n") {
		t.Error("the encoded document has a trailing blank line")
	}

	for _, unwanted := range []string{`"sensitive": false`, `"description": ""`, `"deprecation_message": ""`} {
		if strings.Contains(string(first), unwanted) {
			t.Errorf("a zero value was written where the key should have been omitted: %s", unwanted)
		}
	}
}

// TestUnit_Spec_MarshalEscaping: descriptions carry angle brackets and
// ampersands, and Go's encoder escapes them by default. In a document meant to be
// read by a human that turns "a < b" into "a < b" for no benefit.
func TestUnit_Spec_MarshalEscaping(t *testing.T) {
	t.Parallel()

	s := minimalSpec()
	desc := "use <angle> brackets & ampersands"
	s.Resources[0].Schema.MarkdownDescription = &desc

	data, err := Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if !strings.Contains(string(data), desc) {
		t.Errorf("HTML escaping mangled the description:\n%s", data)
	}
}

func TestUnit_Spec_ParseRejectsBadInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"not json", "{not json"},
		{"no version", `{"resources":[]}`},
		{"unknown version", `{"version":"9.9","resources":[]}`},
		{"attribute with no type", `{"version":"0.1","resources":[{"name":"t","schema":{"attributes":[{"name":"x"}]}}]}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := Parse(ctx, []byte(tc.in)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}
