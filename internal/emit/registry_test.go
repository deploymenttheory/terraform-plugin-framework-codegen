package emit

import (
	"bytes"
	"strings"
	"testing"
)

// registryFixture is a rendered resources registry file, as the provider
// core emits it.
const registryFixture = `package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	// tfpfgen:resources:imports
)

func (p *Provider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		// tfpfgen:resources:registrations
	}
}
`

func TestUnit_Splice_IsIdempotentSortedAndSentinelPreserving(t *testing.T) {
	set := RegistrationSet{
		Imports: []string{
			`zebra "example.test/provider/internal/services/resources/z/v1/zebra"`,
			`aard "example.test/provider/internal/services/resources/a/v1/aard"`,
			`aard "example.test/provider/internal/services/resources/a/v1/aard"`, // duplicate
		},
		Registrations: []string{
			"zebra.NewZebraResource,",
			"aard.NewAardResource,",
		},
	}

	once, err := Splice([]byte(registryFixture), "resources", set)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	twice, err := Splice(once, "resources", set)
	if err != nil {
		t.Fatalf("second Splice: %v", err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("splicing is not idempotent:\n%s\nvs\n%s", once, twice)
	}

	text := string(once)
	for _, sentinel := range []string{"// tfpfgen:resources:imports", "// tfpfgen:resources:registrations"} {
		if !strings.Contains(text, sentinel) {
			t.Fatalf("the sentinel %q did not survive:\n%s", sentinel, text)
		}
	}
	if strings.Count(text, "aard \"example.test") != 1 {
		t.Fatalf("duplicate import lines must collapse:\n%s", text)
	}
	if strings.Index(text, `aard "example.test`) > strings.Index(text, `zebra "example.test`) {
		t.Fatalf("spliced lines must sort:\n%s", text)
	}
	if strings.Index(text, "aard.NewAardResource,") > strings.Index(text, "zebra.NewZebraResource,") {
		t.Fatalf("registrations must sort:\n%s", text)
	}

	// Re-splicing a smaller set replaces the block rather than appending.
	smaller, err := Splice(once, "resources", RegistrationSet{
		Imports:       []string{`aard "example.test/provider/internal/services/resources/a/v1/aard"`},
		Registrations: []string{"aard.NewAardResource,"},
	})
	if err != nil {
		t.Fatalf("Splice with a smaller set: %v", err)
	}
	if strings.Contains(string(smaller), "zebra") {
		t.Fatalf("a removed entity's lines must not survive a re-splice:\n%s", smaller)
	}
}

func TestUnit_Splice_RefusesMissingSentinelsAndUnknownKinds(t *testing.T) {
	if _, err := Splice([]byte("package provider\n"), "resources", RegistrationSet{}); err == nil ||
		!strings.Contains(err.Error(), "sentinel") {
		t.Fatalf("a missing sentinel must be named: %v", err)
	}

	if _, err := Splice([]byte(registryFixture), "gadgets", RegistrationSet{}); err == nil ||
		!strings.Contains(err.Error(), "gadgets") {
		t.Fatalf("an unknown kind must be named: %v", err)
	}

	unclosed := "package provider\n// tfpfgen:resources:imports\nno closing delimiter"
	if _, err := Splice([]byte(unclosed), "resources", RegistrationSet{}); err == nil ||
		!strings.Contains(err.Error(), "closing delimiter") {
		t.Fatalf("a sentinel without a closing delimiter must be refused: %v", err)
	}
}

func TestUnit_Registrations_ByKindCoversEverySentinelKind(t *testing.T) {
	var r Registrations
	r.Resources.add("i1", "r1")
	r.Datasources.add("i2", "r2")
	r.ListResources.add("i3", "r3")
	r.Actions.add("i4", "r4")

	for i, kind := range SentinelKinds {
		set, err := r.ByKind(kind)
		if err != nil {
			t.Fatalf("ByKind(%s): %v", kind, err)
		}
		if len(set.Imports) != 1 || len(set.Registrations) != 1 {
			t.Fatalf("ByKind(%s) = %+v", kind, set)
		}
		if set.Imports[0] != "i"+string(rune('1'+i)) {
			t.Fatalf("ByKind(%s) answered the wrong set: %+v", kind, set)
		}
	}

	if _, err := r.ByKind("nope"); err == nil {
		t.Fatal("an unknown kind must error")
	}
}
