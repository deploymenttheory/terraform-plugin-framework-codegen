package sdkbind

import (
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
)

// factFor finds one static fact by resource and path.
func factFor(facts []probe.Fact, resource, path string) *probe.Fact {
	for i := range facts {
		if facts[i].Resource == resource && facts[i].JSONPath == path {
			return &facts[i]
		}
	}
	return nil
}

// TestUnit_SDKBind_StaticFactsFindTheOmitemptyClass re-derives, from the pinned SDK
// alone, the finding the classic-tests wave paid ten live acceptance rounds for:
// networkMeasurements is a value-typed omitempty bool, so a configured false never
// travels.
func TestUnit_SDKBind_StaticFactsFindTheOmitemptyClass(t *testing.T) {
	t.Parallel()

	bp, l := loadPilot(t)

	facts, err := StaticFacts(l, bp)
	if err != nil {
		t.Fatalf("StaticFacts: %v", err)
	}
	if len(facts) == 0 {
		t.Fatal("the pinned SDK is full of value-typed omitempty scalars; zero facts proves nothing ran")
	}

	// tests_api still models networkMeasurements as configurable; the other test
	// families have since been curated computed for exactly the reason this fact
	// records, which removes them from the scan's domain.
	f := factFor(facts, "tests_api", "networkMeasurements")
	if f == nil {
		t.Fatal("networkMeasurements on tests_api is the proven live case and must be found")
	}
	if f.Confidence != probe.ConfidenceCorroborated {
		t.Errorf("a mechanical claim about the pinned SDK is Corroborated, got %s", f.Confidence)
	}
	if err := f.Validate(); err != nil {
		t.Errorf("every static fact must pass fact validation: %v", err)
	}

	// The tag resource's name field is a plain string without omitempty on its
	// request type -- absence here is what proves the scan discriminates rather
	// than flagging everything.
	if f := factFor(facts, "tag", "key"); f != nil {
		t.Errorf("tag.key is not omitempty and must not be claimed unsendable: %+v", f)
	}
}

// TestUnit_SDKBind_StaticFactsAreDeterministic: the committed document is a drift
// gate, so two derivations must be byte-stable in content and order.
func TestUnit_SDKBind_StaticFactsAreDeterministic(t *testing.T) {
	t.Parallel()

	bp, l := loadPilot(t)

	first, err := StaticFacts(l, bp)
	if err != nil {
		t.Fatalf("StaticFacts: %v", err)
	}
	second, err := StaticFacts(l, bp)
	if err != nil {
		t.Fatalf("StaticFacts: %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("derivations disagree on count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].String() != second[i].String() {
			t.Fatalf("fact %d differs between derivations:\n%s\n%s",
				i, first[i], second[i])
		}
	}
}
