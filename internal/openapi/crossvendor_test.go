package openapi

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/corpus"
)

// Every ingestion defect this generator has had was found by a second vendor's
// document disagreeing with the first, never by a synthetic fixture written
// alongside the code it exercises. Jamf Pro alone produced six: relative token
// URLs, arrays of format:byte, identifiers present only on create, accessor
// mangling in both directions, Go-keyword schema names, and nested-attribute
// wire reconciliation. Every one of them was invisible against ThousandEyes.
//
// So these tests run discovery and inference across every pinned document. They
// assert the properties that must hold whatever the vendor did -- determinism,
// no panic, plausible scale, honest refusals -- rather than counts that would
// have to be re-blessed on every refresh.

// vendors lists the documents to sweep. GitHub is 13 MB and 808 paths, so it is
// skipped under -short: it is the scale case, and scale is what makes it slow.
var vendors = []struct {
	id       string
	short    bool
	minPaths int
}{
	{id: corpus.ThousandEyes, short: true, minPaths: 150},
	{id: corpus.JamfPro, short: true, minPaths: 400},
	{id: corpus.GitHub, short: false, minPaths: 700},
}

// TestUnit_CrossVendor_DiscoveryIsPlausibleAndDeterministic sweeps every pinned
// document.
//
// Determinism is the load-bearing assertion. Discovery walks maps, and Go
// randomises map iteration, so an ordering bug is invisible in a single run and
// shows up later as a blueprint that differs between two identical generations.
func TestUnit_CrossVendor_DiscoveryIsPlausibleAndDeterministic(t *testing.T) {
	t.Parallel()

	for _, v := range vendors {
		t.Run(v.id, func(t *testing.T) {
			t.Parallel()

			if !v.short && testing.Short() {
				t.Skipf("%s is the scale case; skipped under -short", v.id)
			}

			pin := corpus.MustPin(t, v.id)

			doc, err := Load(corpus.SpecPath(t, v.id))
			if err != nil {
				t.Fatalf("loading the pinned %s document: %v", v.id, err)
			}
			if doc.Version != pin.Version {
				t.Errorf("Version = %q, want %q from the lock", doc.Version, pin.Version)
			}

			paths, operations := doc.Stats()
			if paths != pin.PathCount {
				t.Errorf("%d path(s), but the lock pins %d", paths, pin.PathCount)
			}
			if operations != pin.OperationCount {
				t.Errorf("%d operation(s), but the lock pins %d", operations, pin.OperationCount)
			}
			if paths < v.minPaths {
				t.Fatalf("only %d paths; this is not the whole document", paths)
			}

			first := doc.Discover()
			if len(first) == 0 {
				t.Fatalf("discovery found nothing in %d paths", paths)
			}

			second := doc.Discover()
			if len(first) != len(second) {
				t.Fatalf("two discoveries of one document found %d then %d candidates",
					len(first), len(second))
			}
			for i := range first {
				if first[i].Key != second[i].Key {
					t.Fatalf("discovery is not deterministic: candidate %d was %q then %q",
						i, first[i].Key, second[i].Key)
				}
			}

			// Every candidate must classify without panicking and give a reason.
			// A generator that cannot explain its own decision cannot be argued
			// with when it is wrong.
			for _, c := range first {
				kind, why := c.Classify()
				if why == "" {
					t.Errorf("candidate %q classified as %s with no reason", c.Key, kind)
				}
			}
		})
	}
}

// TestUnit_CrossVendor_InferenceRefusesRatherThanGuesses runs inference over
// every resource-shaped candidate in every document.
//
// The claim is not that inference succeeds -- plenty of real API shapes cannot
// be expressed, and saying so is correct behaviour. The claim is that it either
// produces something a validator would accept or refuses by name, and never
// panics or returns a half-built resource. Across three vendors and roughly
// 1,500 paths, that is a wide net for the crash-and-silently-skip class.
func TestUnit_CrossVendor_InferenceRefusesRatherThanGuesses(t *testing.T) {
	t.Parallel()

	for _, v := range vendors {
		t.Run(v.id, func(t *testing.T) {
			t.Parallel()

			if !v.short && testing.Short() {
				t.Skipf("%s is the scale case; skipped under -short", v.id)
			}

			doc, err := Load(corpus.SpecPath(t, v.id))
			if err != nil {
				t.Fatalf("loading the pinned %s document: %v", v.id, err)
			}

			inferred, failed, refusals := 0, 0, 0

			for _, c := range doc.Discover() {
				if kind, _ := c.Classify(); kind != CandidateKindResource {
					continue
				}

				res, notes, err := doc.Infer(c, InferOptions{Provider: v.id})
				if err != nil {
					if err.Error() == "" {
						t.Errorf("%s: inference failed on %q with an empty reason", v.id, c.Key)
					}
					failed++
					continue
				}

				// Refusals are the interesting half. A field the generator
				// cannot express is dropped with a note rather than guessed at,
				// and a note with no text is a silent drop wearing a refusal's
				// clothes -- which is the failure this is really watching for.
				for _, n := range notes {
					if strings.TrimSpace(n.Message) == "" {
						t.Errorf("%s: %q dropped %q with an empty note", v.id, c.Key, n.Field)
					}
					refusals++
				}

				inferred++

				if res.Key == "" {
					t.Errorf("%s: inferred a resource from %q with no key", v.id, c.Key)
				}
				if len(res.Schema.Attributes) == 0 {
					t.Errorf("%s: inferred %q with no attributes", v.id, res.Key)
				}
			}

			if inferred == 0 {
				t.Fatalf("%s: inference produced nothing from %d candidates; it is not running",
					v.id, len(doc.Discover()))
			}

			t.Logf("%s: %d resource(s) inferred, %d failed, %d named refusal(s)",
				v.id, inferred, failed, refusals)
		})
	}
}

// TestUnit_CrossVendor_VendorsDisagreeAboutIdentifiers pins the specific
// disagreement that cost the most to find.
//
// ThousandEyes names identifiers with a vendor prefix and types them as strings;
// Jamf Pro uses bare integers, and returns some only on create. Inference
// hardcoded the ThousandEyes shape until Jamf arrived. This asserts the two
// documents really do still differ here, so a future simplification that quietly
// re-hardcodes one shape fails on the other.
func TestUnit_CrossVendor_VendorsDisagreeAboutIdentifiers(t *testing.T) {
	t.Parallel()

	shapes := map[string]map[string]bool{}

	for _, id := range []string{corpus.ThousandEyes, corpus.JamfPro} {
		doc, err := Load(corpus.SpecPath(t, id))
		if err != nil {
			t.Fatalf("loading the pinned %s document: %v", id, err)
		}

		seen := map[string]bool{}

		for _, c := range doc.Discover() {
			if kind, _ := c.Classify(); kind != CandidateKindResource {
				continue
			}
			res, _, err := doc.Infer(c, InferOptions{Provider: id})
			if err != nil {
				continue
			}
			for _, a := range res.Schema.Attributes {
				if a.Name == "id" {
					seen[string(a.Type.Kind)] = true
				}
			}
		}

		shapes[id] = seen
	}

	for id, seen := range shapes {
		if len(seen) == 0 {
			t.Errorf("%s: no resource inferred an id attribute at all", id)
		}
		t.Logf("%s: id attribute kinds %v", id, keysOf(seen))
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
