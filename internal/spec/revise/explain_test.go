package revise

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
)

// TestUnit_Revise_ExplanationsCoverEveryCompilableKind holds the explanation
// table and the compilable-kind vocabulary to each other in both directions,
// the same way the config reference test holds descriptions and schema. A new
// kind cannot ship without prose, and prose cannot outlive its kind.
func TestUnit_Revise_ExplanationsCoverEveryCompilableKind(t *testing.T) {
	t.Parallel()
	for _, k := range CompilableKinds() {
		if _, ok := Explain(observe.Kind(k)); !ok {
			t.Errorf("observation kind %q has no explanation; a reviewer would meet it with no prose", k)
		}
	}
	known := map[string]bool{}
	for _, k := range CompilableKinds() {
		known[k] = true
	}
	for kind := range explanations {
		if !known[string(kind)] {
			t.Errorf("explanation for %q matches no compilable kind — dead prose", kind)
		}
	}
}

func TestUnit_Revise_EveryExplanationFieldIsFilled(t *testing.T) {
	t.Parallel()
	for kind, e := range explanations {
		fields := map[string]string{
			"Title": e.Title, "Plural": e.Plural, "Expected": e.Expected,
			"Observed": e.Observed, "Means": e.Means, "Merging": e.Merging, "Closing": e.Closing,
		}
		for name, v := range fields {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s: %s is empty", kind, name)
			}
		}
		if !strings.HasPrefix(e.Merging, "Merging ") {
			t.Errorf("%s: Merging must start with \"Merging \", got %q", kind, e.Merging)
		}
		if !strings.HasPrefix(e.Closing, "Closing ") {
			t.Errorf("%s: Closing must start with \"Closing \", got %q", kind, e.Closing)
		}
	}
}

// TestUnit_Revise_ExplanationsAreProseNotJargon is the rule the owner set
// after reading a body that said only "a readAfterWrite observation on X":
// the sentences may mention an extension key in passing but may never lead
// with one, and they may not name a vendor.
func TestUnit_Revise_ExplanationsAreProseNotJargon(t *testing.T) {
	t.Parallel()
	for kind, e := range explanations {
		for name, v := range map[string]string{
			"Expected": e.Expected, "Observed": e.Observed, "Means": e.Means,
			"Merging": e.Merging, "Closing": e.Closing,
		} {
			if strings.HasPrefix(v, "x-tfpfgen-") {
				t.Errorf("%s: %s leads with an extension key, which explains nothing", kind, name)
			}
			if !strings.HasSuffix(strings.TrimSpace(v), ".") {
				t.Errorf("%s: %s is not a sentence: %q", kind, name, v)
			}
			// Four sentences is the ceiling; past that nobody reads it.
			if n := strings.Count(v, ". "); n > 3 {
				t.Errorf("%s: %s runs to %d sentences", kind, name, n+1)
			}
		}
		// Merging and Closing head a whole group, so a per-finding
		// placeholder in them would be a lie about every member but one —
		// and, worse, would render as a hole where a value should be.
		for name, v := range map[string]string{"Merging": e.Merging, "Closing": e.Closing} {
			for _, ph := range []string{"{attribute}", "{value}"} {
				if strings.Contains(v, ph) {
					t.Errorf("%s: %s uses %s, but it speaks for a whole group of findings", kind, name, ph)
				}
			}
		}
		// The title is read mid-sentence, after a count, so it starts lower
		// case — an acronym further in is fine.
		for _, title := range []string{e.Title, e.Plural} {
			if first := title[:1]; first != strings.ToLower(first) {
				t.Errorf("%s: %q starts upper case; it is read mid-sentence after a count", kind, title)
			}
		}
	}
}

// entityLevelKinds are the kinds whose observations carry no attribute, so
// their prose must never reach for one.
var entityLevelKinds = []observe.Kind{
	observe.KindUpdateStyle, observe.KindDeleteNotFoundOK,
	observe.KindReadAfterWrite, observe.KindMutuallyExclusive,
	observe.KindListWrapper,
}

func TestUnit_Revise_EntityLevelExplanationsNameNoAttribute(t *testing.T) {
	t.Parallel()
	for _, kind := range entityLevelKinds {
		e, ok := Explain(kind)
		if !ok {
			t.Fatalf("%s has no explanation", kind)
		}
		for name, v := range map[string]string{
			"Expected": e.Expected, "Observed": e.Observed, "Means": e.Means,
			"Merging": e.Merging, "Closing": e.Closing,
		} {
			if strings.Contains(v, "{attribute}") {
				t.Errorf("%s: %s uses {attribute}, but the kind is entity-level and has none", kind, name)
			}
		}
	}
}

func TestUnit_Revise_RenderFillsEveryPlaceholder(t *testing.T) {
	t.Parallel()
	for kind, e := range explanations {
		got := e.Render("tag", "color", "`#A7EB10`")
		for name, v := range map[string]string{
			"Expected": got.Expected, "Observed": got.Observed, "Means": got.Means,
			"Merging": got.Merging, "Closing": got.Closing,
		} {
			if strings.Contains(v, "{") {
				t.Errorf("%s: %s still holds a placeholder after Render: %q", kind, name, v)
			}
		}
	}
}

func TestUnit_Revise_SummaryCounts(t *testing.T) {
	t.Parallel()
	e, ok := Explain(observe.KindServerDefault)
	if !ok {
		t.Fatal("serverDefault has no explanation")
	}
	if got, want := e.Summary(1), "1 server-assigned default"; got != want {
		t.Errorf("Summary(1) = %q, want %q", got, want)
	}
	if got, want := e.Summary(25), "25 server-assigned defaults"; got != want {
		t.Errorf("Summary(25) = %q, want %q", got, want)
	}
}

func TestUnit_Revise_ExplainRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()
	if _, ok := Explain(observe.Kind("noSuchKind")); ok {
		t.Error("Explain answered an explanation for a kind that does not exist")
	}
}
