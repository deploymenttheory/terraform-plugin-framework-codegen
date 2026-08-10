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

func TestUnit_Register_IsIdempotentSortedAndSentinelPreserving(t *testing.T) {
	set := Registrations{
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

	once, err := Register([]byte(registryFixture), "resources", set)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	twice, err := Register(once, "resources", set)
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("registering is not idempotent:\n%s\nvs\n%s", once, twice)
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
		t.Fatalf("registered lines must sort:\n%s", text)
	}
	if strings.Index(text, "aard.NewAardResource,") > strings.Index(text, "zebra.NewZebraResource,") {
		t.Fatalf("registrations must sort:\n%s", text)
	}

	// Re-registering a smaller set replaces the block rather than appending.
	smaller, err := Register(once, "resources", Registrations{
		Imports:       []string{`aard "example.test/provider/internal/services/resources/a/v1/aard"`},
		Registrations: []string{"aard.NewAardResource,"},
	})
	if err != nil {
		t.Fatalf("Register with a smaller set: %v", err)
	}
	if strings.Contains(string(smaller), "zebra") {
		t.Fatalf("a removed entity's lines must not survive a re-register:\n%s", smaller)
	}
}

func TestUnit_Register_RefusesMissingSentinelsAndUnknownKinds(t *testing.T) {
	if _, err := Register([]byte("package provider\n"), "resources", Registrations{}); err == nil ||
		!strings.Contains(err.Error(), "sentinel") {
		t.Fatalf("a missing sentinel must be named: %v", err)
	}

	if _, err := Register([]byte(registryFixture), "gadgets", Registrations{}); err == nil ||
		!strings.Contains(err.Error(), "gadgets") {
		t.Fatalf("an unknown kind must be named: %v", err)
	}

	unclosed := "package provider\n// tfpfgen:resources:imports\nno closing delimiter"
	if _, err := Register([]byte(unclosed), "resources", Registrations{}); err == nil ||
		!strings.Contains(err.Error(), "closing delimiter") {
		t.Fatalf("a sentinel without a closing delimiter must be refused: %v", err)
	}
}

func TestUnit_Registry_ByKindCoversEveryRegistrySlot(t *testing.T) {
	var r Registry
	r.Resources.add("i1", "r1")
	r.Datasources.add("i2", "r2")
	r.ListResources.add("i3", "r3")
	r.Actions.add("i4", "r4")

	for i, kind := range RegistrySlots {
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
