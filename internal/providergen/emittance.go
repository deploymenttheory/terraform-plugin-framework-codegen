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
		name, why, cost string
		from            sdkgen.Rewrite
	}{
		{
			"Default values removed from schemas",
			"The specification gives some fields a default value, for example an interval declared with a default of 60. That is a statement about what the API does when you send nothing. kiota reads it as a value to build in, so every object the provider created would arrive with the interval already set to 60 whether or not you asked for it, and a response that said nothing about the interval would still read back as 60. The provider could not tell a field the API never mentioned from one it really returned.",
			"Nothing. The field is still there and you can still set it. If you do not, the API applies its own default, which is what the specification was describing in the first place.",
			r.SchemaDefaultsStripped,
		},
		{
			"Single-member allOf compositions flattened",
			"Where the specification wraps one unnamed schema in an allOf with nothing else in it, the wrapper adds nothing the inner schema did not already say. kiota has to invent a name for the unnamed schema, and it picks a different one depending on what else it has seen, so generated type names would change between runs even though the specification had not.",
			"Nothing. The wrapper described exactly what it wrapped.",
			r.AnonymousAllOfsCollapsed,
		},
		{
			"format: byte removed from list items",
			"Where the specification describes a list whose items are strings carrying the byte format, kiota generates code its own runtime does not implement, and the API client does not compile at all.",
			"Nothing on the wire. Base64 text is sent and received either way; only the Go type the client uses to hold it differs.",
			r.ByteArrayCollectionsWidened,
		},
		{
			"oneOf and anyOf reduced to their first option",
			"Where the specification says a field is one of several different shapes, Go has no type that is either of two shapes. Asked to merge alternatives it cannot reconcile, kiota produces a model with no fields at all, which breaks the whole client.",
			"Nothing for this provider. A field described as a choice between shapes is already left out of the Terraform schema for the same reason, so the copy and the specification produce the same provider.",
			r.UnionsReduced,
		},
		{
			"Content removed from error responses",
			"kiota decides what to put in a request's Accept header from the response formats an operation declares. Where an operation declares no format for its success response, kiota falls through to the error responses and asks the server for the format it produces only when refusing a request. Servers answer that with a 406.",
			"Nothing. Only the error responses lose their declared content, and the provider reads a status code and a message from a refusal either way.",
			r.ErrorContentDropped,
		},
	}
	out := make([]emit.EmittanceRewrite, 0, len(named))
	for _, n := range named {
		sites := make([]emit.EmittanceSite, 0, len(n.from.Sites))
		for _, s := range n.from.Sites {
			sites = append(sites, emit.EmittanceSite{Where: s.Where, Count: s.Count})
		}
		out = append(out, emit.EmittanceRewrite{Name: n.name, Why: n.why, Cost: n.cost, Count: n.from.Count, Sites: sites})
	}
	return out
}
