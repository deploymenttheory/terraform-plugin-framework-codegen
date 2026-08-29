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
		name string
		from sdkgen.Rewrite
	}{
		{"schema defaults stripped", r.SchemaDefaultsStripped},
		{"anonymous allOf collapsed", r.AnonymousAllOfsCollapsed},
		{"byte-array collections widened", r.ByteArrayCollectionsWidened},
		{"unions reduced", r.UnionsReduced},
		{"error responses stripped of content", r.ErrorContentDropped},
	}
	out := make([]emit.EmittanceRewrite, 0, len(named))
	for _, n := range named {
		sites := make([]emit.EmittanceSite, 0, len(n.from.Sites))
		for _, s := range n.from.Sites {
			sites = append(sites, emit.EmittanceSite{Where: s.Where, Count: s.Count})
		}
		out = append(out, emit.EmittanceRewrite{Name: n.name, Count: n.from.Count, Sites: sites})
	}
	return out
}
