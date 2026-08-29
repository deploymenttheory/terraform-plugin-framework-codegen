package providergen

import (
	"os"
	"path/filepath"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/emit"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkgen"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/store"
)

// emittanceReport renders the account of how the pinned document became this
// provider.
//
// The prenormalise rewrites are recomputed here rather than carried from the
// SDK generation that ran them. Prenormalise is a pure function of the
// revised document, and this reads the same document, so recomputing answers
// exactly what the backend was given — and costs no file passed between two
// verbs that would have to be kept in step.
func emittanceReport(opts Options, model *ir.Model, refusals []emit.Unsupported,
	produced emit.EmittanceCounts, reconciled []sdkbind.Reconciliation) (emit.File, error) {

	report := emit.Emittance{
		Provider: opts.Config.Provider.Name,
		SDK: emit.EmittanceSDK{
			Backend:    opts.Config.SDK.Backend,
			Version:    opts.Config.SDK.BackendVersion,
			Reconciled: len(reconciled),
		},
		Produced: produced,
	}

	// The pin says what the run was a fact about. A tree without one still
	// generates, so its absence is silence rather than a refusal.
	if pin, err := store.Verify(opts.SpecDir); err == nil {
		report.Document = emit.EmittanceDocument{
			Source: pin.Source, SHA256: pin.SHA256,
			Version: pin.DocumentVersion, OpenAPI: pin.OpenAPI,
		}
	}
	if corrections, err := correction.Load(filepath.Join(opts.SpecDir, correction.DirName)); err == nil {
		report.Document.Corrections = len(corrections)
	}

	revised, err := os.ReadFile(filepath.Join(opts.SpecDir, "revised.yaml")) //nolint:gosec // the fixed name under the operator-supplied dir
	if err == nil {
		if _, rewrites, err := sdkgen.Prenormalise(revised); err == nil {
			report.Rewrites = rewriteLines(rewrites)
		}
	}

	return emit.RenderEmittance(report, model, refusals)
}

// rewriteLines names each rewrite the way the document's own vocabulary
// does, in the order they are applied.
func rewriteLines(r sdkgen.Rewrites) []emit.EmittanceRewrite {
	named := []struct {
		name, why string
		from      sdkgen.Rewrite
	}{
		{
			"Default values removed from schemas",
			"A generated API client stamps every declared default onto the object it builds. A field nobody set would then be sent on every request, and on the way back the default would hide the fact that the API said nothing at all.",
			r.SchemaDefaultsStripped,
		},
		{
			"Single-member allOf compositions flattened",
			"API client generators invent names for unnamed schemas and merge identical ones with no fixed winner, so the name chosen would change from one generation to the next.",
			r.AnonymousAllOfsCollapsed,
		},
		{
			"format: byte removed from list items",
			"kiota generates a writer for lists of byte strings that its own runtime does not implement, so the API client would not compile. The wire carries the same base64 text either way.",
			r.ByteArrayCollectionsWidened,
		},
		{
			"oneOf and anyOf reduced to their first option",
			"Go has no type that is either of two shapes. Asked to merge alternatives it cannot reconcile, kiota emits a model with no fields at all. This costs the provider nothing: a field described as a choice between shapes is already left out of the generated schema.",
			r.UnionsReduced,
		},
		{
			"Content removed from error responses",
			"kiota builds a request's Accept header from the media types the responses declare, and falls through to the error responses when no success response names one. That asks the server for the format it produces only when refusing.",
			r.ErrorContentDropped,
		},
	}
	out := make([]emit.EmittanceRewrite, 0, len(named))
	for _, n := range named {
		sites := make([]emit.EmittanceSite, 0, len(n.from.Sites))
		for _, s := range n.from.Sites {
			sites = append(sites, emit.EmittanceSite{Where: s.Where, Count: s.Count})
		}
		out = append(out, emit.EmittanceRewrite{Name: n.name, Why: n.why, Count: n.from.Count, Sites: sites})
	}
	return out
}
