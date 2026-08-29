package emit

import (
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// Explanation is what a reader of the report is told about one cause.
//
// The report is read by somebody who knows Terraform and their own API and
// has never seen this toolkit, so it says what happened in their words, not
// in the vocabulary the code uses among itself. That is `docs/naming-
// standard.md` R7 — emitted artefacts speak the reader's language — applied
// to a page rather than to generated Go.
//
// It follows the same shape `internal/spec/revise` uses to narrate a
// correction to a reviewer, and for the same reason: a code and a subject
// are the mechanism, and a reader needs what it cost them.
type Explanation struct {
	// Title says what happened, as a heading a reader can scan.
	Title string
	// Means says what it costs them, in the provider they now have.
	Means string
	// Fix says whether anything can be done, and by whom. It promises
	// nothing it cannot deliver: where the answer is that nothing here can
	// change it, it says so.
	Fix string
}

// Step names one point in the workflow that produced the provider, in the
// order they run. The report shows the whole list once so a reader has the
// shape of it, then refers to a step by name rather than by the stage the
// record calls it.
type Step struct {
	Number int
	Name   string
	Detail string
}

// Workflow is the whole generation, in the order it happens.
var Workflow = []Step{
	{1, "Read the API description", "The vendor's OpenAPI description is fetched and recorded by checksum, so every later step works from exactly those bytes."},
	{2, "Apply recorded adjustments", "Corrections committed against the description are applied to it — each one a recorded change with a reason, usually something the live API does that the description does not say."},
	{3, "Prepare the description for the client generator", "A working copy is adjusted to what the client generator can model. The copy is discarded afterwards; this report is the only account of how it differed."},
	{4, "Generate the API client", "The client library the provider calls the API through is generated from that copy."},
	{5, "Decide what each API path can become", "Paths are grouped into things, and each is judged against what Terraform can manage: a resource, a data source, a list, an action, or nothing."},
	{6, "Turn each schema into Terraform attributes", "Every field the API describes becomes a Terraform attribute, or is set aside where Terraform has no equivalent."},
	{7, "Match every attribute to the generated client", "Each attribute is checked against the client that was actually generated. Anything the client cannot carry is set aside here."},
	{8, "Write the provider code", "The Terraform provider is written from what survived."},
}

// stepOf answers the workflow step a stage belongs to, and the plain phrase
// the report uses in place of the stage's own name.
func stepOf(stage string) (int, string) {
	switch stage {
	case StageConfiguration:
		return 0, "you asked for it to be skipped"
	case StageClassification:
		return 5, "deciding what this API path could become"
	case StageDerivation:
		return 6, "turning the described schema into Terraform attributes"
	case StageBinding:
		return 7, "matching attributes to the generated API client"
	case StageEmission:
		return 8, "writing the provider code"
	}
	return 0, stage
}

// explanations is the table, one entry per cause. A cause with no entry
// would reach a reader as a bare identifier, which is the whole thing this
// exists to prevent; a test holds the table and the causes to each other in
// both directions.
var explanations = map[string]Explanation{
	// What the operator asked for.
	ir.CauseExcludedByConfiguration: {
		Title: "You asked for this to be skipped",
		Means: "Your configuration lists it under the services to exclude, so nothing was generated for it.",
		Fix:   "Remove it from the exclude list in tfpfgen.yaml and generate again.",
	},

	// Deciding what an API path can become.
	specmodel.CauseUnwritableFixedPath: {
		Title: "Read-only endpoint, so there is nothing to manage",
		Means: "The API offers one object at a fixed address and no operation that changes it. Terraform would own nothing, so no resource was generated.",
		Fix:   "Nothing here can change this. The API would have to offer a way to write it.",
	},
	specmodel.CauseSchemalessLifecycle: {
		Title: "The operations are described but the data is not",
		Means: "The API can create, read and delete this, but the description does not say what the request or the response looks like — so there is no shape to build a resource from.",
		Fix:   "A correction can supply the missing schema, or the vendor can describe it.",
	},
	specmodel.CauseSchemalessDatasource: {
		Title: "The operations are described but the data is not",
		Means: "The API can list and read this, but the description declares no schema for the response, so there is nothing to read into Terraform state.",
		Fix:   "A correction can supply the missing schema, or the vendor can describe it.",
	},
	specmodel.CauseSchemalessList: {
		Title: "The list operation describes no response",
		Means: "The API can list these, but the description does not say what comes back, so no data source could be generated.",
		Fix:   "A correction can supply the missing schema, or the vendor can describe it.",
	},
	specmodel.CauseSchemalessRead: {
		Title: "The read operation describes no response",
		Means: "The API can return one of these by its identifier, but the description does not say what comes back.",
		Fix:   "A correction can supply the missing schema, or the vendor can describe it.",
	},
	specmodel.CauseNoClassifiableOperation: {
		Title: "The API paths form nothing Terraform can manage",
		Means: "None of the operations sits where a create, read, update, delete or list would, so this became neither a resource nor a data source.",
		Fix:   "Nothing here can change this. It usually means the endpoint is not a managed object at all.",
	},
	specmodel.CausePartialLifecycle: {
		Title: "Only part of a lifecycle is offered",
		Means: "The API offers some of create, read, update and delete but not the combination any Terraform shape needs — most often something that can be created but never read back.",
		Fix:   "Nothing here can change this. The API would have to offer the missing operation.",
	},

	// Turning a described schema into Terraform attributes.
	ir.CauseUndeclaredType: {
		Title: "The description does not say what type this is",
		Means: "The field is described with no type at all, so there is no Terraform attribute it could become.",
		Fix:   "A correction can declare the type, or the vendor can describe it.",
	},
	ir.CauseUnsupportedType: {
		Title: "A type Terraform has no equivalent for",
		Means: "The field is described as a type that does not map onto anything Terraform can hold.",
		Fix:   "Nothing here can change this without changing what the API accepts.",
	},
	ir.CauseWritableUnion: {
		Title: "A choice between shapes that Terraform would have to write",
		Means: "The description offers several alternative shapes for something the practitioner sets. Terraform cannot express that only one may be given, so the field was set aside rather than generated wrongly.",
		Fix:   "Nothing here can change this yet. A description that separates the alternatives into distinct fields would generate.",
	},
	ir.CauseUnnamedUnionBranch: {
		Title: "A choice between shapes, one of which is unnamed",
		Means: "The description offers alternatives, and at least one is written inline rather than named. An unnamed alternative has nothing to become, and half a choice would be worse than none.",
		Fix:   "A correction naming each alternative as its own schema would let this generate.",
	},
	ir.CauseEmptyUnion: {
		Title: "A choice between no alternatives",
		Means: "The description declares a choice and then lists nothing to choose between.",
		Fix:   "A correction can remove the empty choice, or the vendor can.",
	},
	ir.CauseUntypedAdditionalProperties: {
		Title: "An open map with no value type",
		Means: "The description says this object may carry any properties, without saying what their values look like. There is nothing to give the Terraform attribute a type.",
		Fix:   "A correction can declare the value type, or the vendor can describe it.",
	},
	ir.CauseShapelessObject: {
		Title: "An object with no fields described",
		Means: "The description says this is an object and then describes nothing inside it, so there is no attribute to generate.",
		Fix:   "A correction can describe the object's fields, or the vendor can.",
	},
	ir.CauseMapOfObjects: {
		Title: "A map whose values are objects",
		Means: "Only maps of simple values are generated. A map of objects was set aside rather than flattened into something that would not round-trip.",
		Fix:   "Nothing here can change this yet.",
	},
	ir.CauseUnsupportedMapValue: {
		Title: "A map of values Terraform has no equivalent for",
		Means: "The map's values are described as a type that does not map onto anything Terraform can hold.",
		Fix:   "Nothing here can change this without changing what the API accepts.",
	},
	ir.CauseItemlessArray: {
		Title: "A list with no item type",
		Means: "The description says this is a list and never says what it is a list of.",
		Fix:   "A correction can declare the item type, or the vendor can describe it.",
	},
	ir.CauseFreeFormArrayElement: {
		Title: "A list of free-form objects",
		Means: "The list's items are described as objects with no fixed shape, so there is no attribute type for them.",
		Fix:   "A correction can describe the item's fields, or the vendor can.",
	},
	ir.CauseUnsupportedArrayElement: {
		Title: "A list of values Terraform has no equivalent for",
		Means: "The list's items are described as a type that does not map onto anything Terraform can hold.",
		Fix:   "Nothing here can change this without changing what the API accepts.",
	},
	ir.CauseReservedRootName: {
		Title: "A name Terraform reserves",
		Means: "Terraform refuses to load a provider whose schema declares this name at the top level, so the field was left out rather than break the whole provider.",
		Fix:   "A correction renaming the property lets it generate under the new name.",
	},

	// Matching attributes to the client that was generated.
	sdkbind.CauseNoAccessor: {
		Title: "The generated API client cannot read these fields",
		Means: "The client was generated without any way to read these values, so the provider could not put them into Terraform state.",
		Fix:   "This follows from what the client generator made of the description. A different generator version may differ; the description itself is usually where it starts.",
	},
	sdkbind.CauseNoSetter: {
		Title: "The generated API client cannot write these fields",
		Means: "The client was generated without any way to set these values, so the provider could not send them to the API.",
		Fix:   "This follows from what the client generator made of the description. A different generator version may differ.",
	},
	sdkbind.CauseNotAnAccessor: {
		Title: "The client's method does not read a value",
		Means: "The client has a method by the expected name, but it does not answer a single value, so it cannot be used to read the field.",
		Fix:   "This follows from what the client generator produced.",
	},
	sdkbind.CauseNotASetter: {
		Title: "The client's method does not set a value",
		Means: "The client has a method by the expected name, but it does not take a single value, so it cannot be used to write the field.",
		Fix:   "This follows from what the client generator produced.",
	},
	sdkbind.CauseUnbridgeableType: {
		Title: "The client carries this as a type that will not convert",
		Means: "The client holds the value as something no safe conversion turns into the Terraform attribute it would have to be.",
		Fix:   "This follows from what the client generator made of the described type.",
	},
	sdkbind.CauseNoNestedModel: {
		Title: "The client does not model this nested object",
		Means: "The client returns something with no fields to map, so the nested object could not be built.",
		Fix:   "This follows from what the client generator produced.",
	},
	sdkbind.CauseNoConstructor: {
		Title: "The client offers no way to build this object",
		Means: "The provider has to construct this object to send it, and the client declares nothing that constructs one.",
		Fix:   "This follows from what the client generator produced.",
	},
	sdkbind.CauseEmptyNestedObject: {
		Title: "Nothing inside this object survived",
		Means: "Every field of the nested object was set aside for its own reason, leaving an object with nothing in it.",
		Fix:   "Look at the fields' own reasons; this is the consequence of them, not a separate problem.",
	},
	sdkbind.CauseUnresolvableCall: {
		Title: "The generated client has no such call",
		Means: "The call the provider would make does not exist on the client that was generated, so the whole thing was set aside rather than emit code that cannot compile.",
		Fix:   "This follows from what the client generator made of the described paths.",
	},
	sdkbind.CauseNoRequestBodyType: {
		Title: "The client has no type for this request body",
		Means: "The API takes a body the client declares no type for, so there is nothing for the provider to fill in and send.",
		Fix:   "This follows from what the client generator made of the described request.",
	},
	sdkbind.CauseAmbiguousListShape: {
		Title: "No single way to reach the list's items",
		Means: "The client's list response carries more than one candidate collection, and guessing between them would be invention.",
		Fix:   "Recording which key the live API wraps its list in resolves this; that is what an API audit records.",
	},
	sdkbind.CauseNoResponsePayload: {
		Title: "None of the calls returns anything to read",
		Means: "Terraform state is built from what the API answers, and none of this thing's calls answers with a body.",
		Fix:   "Nothing here can change this. The API would have to return the object.",
	},
	sdkbind.CauseUnbuildableEntity: {
		Title: "Nothing survived that could be read or written",
		Means: "Every field was set aside for its own reason, so there was nothing left to generate.",
		Fix:   "Look at the fields' own reasons above; this is the consequence of them.",
	},
	sdkbind.CauseNoListCall: {
		Title: "There is no list call to build this from",
		Means: "A list needs a call that returns the collection, and none was bound.",
		Fix:   "This follows from the described paths and what the client generator made of them.",
	},
	sdkbind.CauseNoInvokeCall: {
		Title: "There is no call to invoke",
		Means: "An action is one call, and none was bound.",
		Fix:   "This follows from the described paths and what the client generator made of them.",
	},

	// Writing the provider code.
	CauseNoBoundInvokeCall: {
		Title: "The action has no call behind it",
		Means: "There was nothing for the generated action to invoke, so it was not written.",
		Fix:   "Look at the client-matching step above for why the call went.",
	},
	CauseNoBoundReadCall: {
		Title: "The data source has no read call behind it",
		Means: "A data source addressed by key needs a call that fetches one object, and none survived.",
		Fix:   "Look at the client-matching step above for why the call went.",
	},
	CauseNoBoundListCall: {
		Title: "The list has no call behind it",
		Means: "Terraform refuses to load a provider whose list names no resource, so this was left out rather than break the whole provider.",
		Fix:   "Look at the reason the resource it lists was set aside.",
	},
	CauseNoBoundLifecycleCall: {
		Title: "The resource is missing one of its lifecycle calls",
		Means: "A resource needs create, read and delete — or read and update where the API offers one fixed object — and at least one did not survive.",
		Fix:   "Look at the client-matching step above for why the call went.",
	},
	CauseNoMappablePayload: {
		Title: "The call returns nothing to read into state",
		Means: "The call the provider would read from answers with no body, or with a collection where one object was needed.",
		Fix:   "Nothing here can change this. The API would have to return the object.",
	},
	CauseNoItemsAttribute: {
		Title: "The data source has nothing to return",
		Means: "A data source that lists needs an attribute to put the results in, and the schema carries none.",
		Fix:   "Look at the schema step above for why the fields went.",
	},
	CauseNoElementType: {
		Title: "The list's item type is not known",
		Means: "The provider could not tell what one item of this list looks like, so the list was not written.",
		Fix:   "This follows from what the client generator produced.",
	},
	CauseUnmatchedPathArgument: {
		Title: "A value in the URL matches nothing the caller supplies",
		Means: "The API path contains a placeholder, and there is no attribute and no identifier the provider could fill it from.",
		Fix:   "A correction naming which property fills it lets this generate.",
	},
	CauseUnconvertiblePathType: {
		Title: "A value in the URL is described as one type and generated as another",
		Means: "The placeholder in the API path is one type in the description and another in the generated client, and no conversion between them is safe.",
		Fix:   "A correction aligning the described type with what the API takes resolves this.",
	},
	CauseNoIdentity: {
		Title: "Nothing identifies one of these",
		Means: "Terraform needs a stable identifier to import and to track an object. Nothing the API returns is recognisable as one.",
		Fix:   "Recording which property carries the identifier resolves this; that is what an API audit records.",
	},
	CauseUnvalidatableAttribute: {
		Title: "A rule names something that is not an attribute",
		Means: "A rule recorded about this object refers to a field the generated schema does not have, so the rule could not be written.",
		Fix:   "The recorded rule and the schema disagree; re-running the API audit usually settles it.",
	},
	CauseNoKeyedReadPath: {
		Title: "The read path has nothing to key a test on",
		Means: "The generated tests key their responses on a value in the URL, and this path has none.",
		Fix:   "Nothing here can change this; it affects the generated tests, not the provider.",
	},
}

// Explain answers what a reader is told about one cause. The second result
// is false for a cause with no entry, which the caller shows as the bare
// code rather than inventing prose for it.
func Explain(code string) (Explanation, bool) {
	e, ok := explanations[code]
	return e, ok
}
