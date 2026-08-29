package emit

import (
	"strings"
	"testing"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// allCauses is every cause any stage can record, named by its constant so a
// rename fails to compile rather than quietly leaving prose behind. A new
// cause has to reach this list and the explanation table together; the
// curated fixture catches one that reaches neither, because a cause with no
// explanation shows in the page as a bare identifier.
var allCauses = []string{
	ir.CauseExcludedByConfiguration,
	specmodel.CauseUnwritableFixedPath, specmodel.CauseSchemalessLifecycle,
	specmodel.CauseSchemalessDatasource, specmodel.CauseSchemalessList,
	specmodel.CauseSchemalessRead, specmodel.CauseNoClassifiableOperation,
	specmodel.CausePartialLifecycle,
	ir.CauseUndeclaredType, ir.CauseUnsupportedType, ir.CauseWritableUnion,
	ir.CauseUnnamedUnionBranch, ir.CauseEmptyUnion, ir.CauseUntypedAdditionalProperties,
	ir.CauseShapelessObject, ir.CauseMapOfObjects, ir.CauseUnsupportedMapValue,
	ir.CauseItemlessArray, ir.CauseFreeFormArrayElement, ir.CauseUnsupportedArrayElement,
	ir.CauseReservedRootName,
	sdkbind.CauseNoAccessor, sdkbind.CauseNoSetter, sdkbind.CauseNotAnAccessor,
	sdkbind.CauseNotASetter, sdkbind.CauseUnbridgeableType, sdkbind.CauseNoNestedModel,
	sdkbind.CauseNoConstructor, sdkbind.CauseEmptyNestedObject, sdkbind.CauseUnresolvableCall,
	sdkbind.CauseNoRequestBodyType, sdkbind.CauseAmbiguousListShape,
	sdkbind.CauseNoResponsePayload, sdkbind.CauseUnbuildableEntity,
	sdkbind.CauseNoListCall, sdkbind.CauseNoInvokeCall,
	CauseNoBoundInvokeCall, CauseNoBoundReadCall, CauseNoBoundListCall,
	CauseNoBoundLifecycleCall, CauseNoMappablePayload, CauseNoItemsAttribute,
	CauseNoElementType, CauseUnmatchedPathArgument, CauseUnconvertiblePathType,
	CauseNoIdentity, CauseUnvalidatableAttribute, CauseNoKeyedReadPath,
}

// TestUnit_Explain_EveryCauseIsExplained holds the table and the causes to
// each other in both directions. A cause without prose reaches the reader as
// a bare identifier, and prose without a cause is text nothing can ever
// show.
func TestUnit_Explain_EveryCauseIsExplained(t *testing.T) {
	t.Parallel()
	for _, code := range allCauses {
		if _, ok := Explain(code); !ok {
			t.Errorf("cause %q has no explanation; a reader would meet it as an identifier", code)
		}
	}
	known := map[string]bool{}
	for _, code := range allCauses {
		known[code] = true
	}
	for code := range explanations {
		if !known[code] {
			t.Errorf("explanation for %q matches no cause any stage records", code)
		}
	}
}

// TestUnit_Explain_NoExplanationSpeaksTheToolkitsVocabulary is the point of
// the table. The report's reader knows Terraform and their own API and has
// never seen this toolkit, so an explanation that names our stages or our
// internals has failed at the only job it has.
func TestUnit_Explain_NoExplanationSpeaksTheToolkitsVocabulary(t *testing.T) {
	t.Parallel()
	// Words that mean something here and nothing to a provider engineer.
	// Matched whole, because several of them sit inside ordinary words —
	// "mapping" contains "pin".
	//
	// tfpfgen.yaml is deliberately absent: the reader wrote that file, so
	// naming it tells them exactly where to go.
	ours := map[string]bool{
		"binding": true, "derivation": true, "emission": true,
		"classification": true, "prenormalise": true, "emittance": true,
		"tfpfgen": true, "sdkbind": true, "pruned": true, "prune": true,
		"refusal": true, "refusals": true, "unbound": true,
		"reconciliation": true, "pin": true, "pinned": true, "entity": true,
	}
	for code, e := range explanations {
		// The config file's name is the one place the toolkit's own name
		// belongs: the reader wrote that file.
		text := strings.ReplaceAll(strings.ToLower(e.Title+" "+e.Means+" "+e.Fix), "tfpfgen.yaml", "the config file")
		for _, word := range strings.FieldsFunc(text,
			func(r rune) bool { return !('a' <= r && r <= 'z') },
		) {
			if ours[word] {
				t.Errorf("the explanation for %q says %q, which means nothing to the reader", code, word)
			}
		}
		if e.Title == "" || e.Means == "" || e.Fix == "" {
			t.Errorf("the explanation for %q is incomplete: %+v", code, e)
		}
	}
}

// TestUnit_Workflow_ReadsInOrder pins the list a reader is given to orient
// themselves. A step out of order describes a workflow nothing ran.
func TestUnit_Workflow_ReadsInOrder(t *testing.T) {
	t.Parallel()
	for i, step := range Workflow {
		if step.Number != i+1 {
			t.Errorf("step %d is numbered %d", i+1, step.Number)
		}
		if step.Name == "" || step.Detail == "" {
			t.Errorf("step %d is not described: %+v", step.Number, step)
		}
	}
	for _, stage := range []string{StageConfiguration, StageClassification, StageDerivation, StageBinding, StageEmission} {
		if _, phrase := stepOf(stage); phrase == stage {
			t.Errorf("stage %q reaches the reader under its own name", stage)
		}
	}
}
