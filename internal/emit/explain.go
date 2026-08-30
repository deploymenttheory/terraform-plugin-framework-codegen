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
// standard.md` R7, which says emitted artefacts speak the reader's
// language, applied to a page rather than to generated Go.
//
// It follows the same shape `internal/spec/revise` uses to narrate a
// correction to a reviewer, and for the same reason: a code and a subject
// are the mechanism; a reader needs what it cost them.
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
	{1, "Read the OpenAPI 3 specification", "The specification the API vendor publishes is fetched and recorded by its SHA-256 checksum, so every later step works from exactly those bytes and a changed specification is a visible change."},
	{2, "Apply recorded adjustments", "Corrections committed against the OpenAPI 3 specification are applied to it. Each one is a recorded change with a reason, usually something the live API does that the specification does not say."},
	{3, "Prepare a copy for the API client generator", "A few things an OpenAPI 3 specification can express, the API client generator cannot. It is given an adjusted copy; the specification itself is never changed, and the copy is discarded once the client is built."},
	{4, "Generate the API client", "The Go client library the provider calls the API through is generated from that copy."},
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
		Means: "The API offers one object at a fixed address and no operation that changes it. Terraform would not be able to manage it, so no resource was generated.",
		Fix:   "Nothing here can change this. The API would have to offer a way to write it.",
	},
	specmodel.CauseSchemalessLifecycle: {
		Title: "The operations are described but the data is not",
		Means: "The API can create, read and delete this, but the OpenAPI 3 specification does not say what the request or the response looks like, so there is no shape to build a resource from.",
		Fix:   "A correction can supply the missing schema, or the API vendor can add it to the specification.",
	},
	specmodel.CauseSchemalessDatasource: {
		Title: "The operations are described but the data is not",
		Means: "The API can list and read this, but the OpenAPI 3 specification declares no schema for the response, so there is nothing to read into Terraform state.",
		Fix:   "A correction can supply the missing schema, or the API vendor can add it to the specification.",
	},
	specmodel.CauseSchemalessList: {
		Title: "The list operation describes no response",
		Means: "The API can list these, but the OpenAPI 3 specification does not say what comes back, so no data source could be generated.",
		Fix:   "A correction can supply the missing schema, or the API vendor can add it to the specification.",
	},
	specmodel.CauseSchemalessRead: {
		Title: "The read operation describes no response",
		Means: "The API can return one of these by its identifier, but the OpenAPI 3 specification does not say what comes back.",
		Fix:   "A correction can supply the missing schema, or the API vendor can add it to the specification.",
	},
	specmodel.CauseNoClassifiableOperation: {
		Title: "The API paths form nothing Terraform can manage",
		Means: "None of the operations sits where a create, read, update, delete or list would, so this became neither a resource nor a data source.",
		Fix:   "Nothing here can change this. It usually means the endpoint is not a managed object at all.",
	},
	specmodel.CausePartialLifecycle: {
		Title: "Only part of a lifecycle is offered",
		Means: "The API offers some of create, read, update and delete but not the combination any Terraform shape needs. Most often it is something that can be created but never read back.",
		Fix:   "Nothing here can change this. The API would have to offer the missing operation.",
	},

	// Turning a described schema into Terraform attributes.
	ir.CauseUndeclaredType: {
		Title: "The OpenAPI 3 specification does not say what type this is",
		Means: "The field is described with no type at all, so there is no Terraform attribute it could become.",
		Fix:   "A correction can declare the type, or the API vendor can add it to the specification.",
	},
	ir.CauseUnsupportedType: {
		Title: "A type Terraform has no equivalent for",
		Means: "The field is described as a type that does not map onto anything Terraform can hold.",
		Fix:   "Nothing here can change this without changing what the API accepts.",
	},
	ir.CauseWritableUnion: {
		Title: "A choice between shapes that Terraform would have to write",
		Means: "The OpenAPI 3 specification offers several alternative shapes for something the practitioner sets. Terraform cannot express that only one may be given, so the field was set aside rather than generated wrongly.",
		Fix:   "Nothing here can change this yet. An OpenAPI 3 specification that separates the alternatives into distinct fields would generate.",
	},
	ir.CauseUnnamedUnionBranch: {
		Title: "A choice between shapes, one of which is unnamed",
		Means: "The OpenAPI 3 specification offers alternatives, and at least one is written inline rather than named. An unnamed alternative has nothing to become, and half a choice would be worse than none.",
		Fix:   "A correction naming each alternative as its own schema would let this generate.",
	},
	ir.CauseEmptyUnion: {
		Title: "A choice between no alternatives",
		Means: "The OpenAPI 3 specification declares a choice and then lists nothing to choose between.",
		Fix:   "A correction can remove the empty choice, or the API vendor can.",
	},
	ir.CauseUntypedAdditionalProperties: {
		Title: "An open map with no value type",
		Means: "The OpenAPI 3 specification says this object may carry any properties, without saying what their values look like. There is nothing to give the Terraform attribute a type.",
		Fix:   "A correction can declare the value type, or the API vendor can add it to the specification.",
	},
	ir.CauseObjectWithoutPropertiesOrAdditionalProperties: {
		Title: "An object with no fields described",
		Means: "The OpenAPI 3 specification says this is an object and then describes nothing inside it, so there is no attribute to generate.",
		Fix:   "A correction can describe the object's fields, or the API vendor can add them to the specification.",
	},
	ir.CauseMapOfObjects: {
		Title: "A map whose values have no fields",
		Means: "A map keyed by a name you choose generates where the specification says what fields one value carries. This one says the values are objects and then lists no fields, so there is nothing to build the map's attributes from.",
		Fix:   "A correction can add the value's fields, or the API vendor can add them to the specification.",
	},
	ir.CauseNestedCollectionElement: {
		Title: "A collection of collections with objects at the bottom",
		Means: "The specification says exactly what this holds: a list or map whose elements are lists or maps, with an object at the bottom, such as rows of records keyed by column. Terraform can hold that, and this generator cannot build it yet, so the attribute is not there.",
		Fix:   "Nothing you can change, and nothing is missing from the specification. It is a gap in this generator.",
	},
	ir.CauseUnsupportedMapValue: {
		Title: "A map of values Terraform has no equivalent for",
		Means: "The map's values are described as a type that does not map onto anything Terraform can hold.",
		Fix:   "Nothing here can change this without changing what the API accepts.",
	},
	ir.CauseItemlessArray: {
		Title: "A list with no item type",
		Means: "The OpenAPI 3 specification says this is a list and never says what it is a list of.",
		Fix:   "A correction can declare the item type, or the API vendor can add it to the specification.",
	},
	ir.CauseFreeFormArrayElement: {
		Title: "A list of free-form objects",
		Means: "The list's items are described as objects with no fixed shape, so there is no attribute type for them.",
		Fix:   "A correction can describe the item's fields, or the API vendor can add them to the specification.",
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
		Fix:   "This follows from what the API client generator made of the OpenAPI 3 specification. A different generator version may produce a different client; the specification itself is usually where it starts.",
	},
	sdkbind.CauseNoSetter: {
		Title: "The generated API client cannot write these fields",
		Means: "The client was generated without any way to set these values, so the provider could not send them to the API.",
		Fix:   "This follows from what the API client generator made of the OpenAPI 3 specification. A different generator version may differ.",
	},
	sdkbind.CauseNotAnAccessor: {
		Title: "The client's method does not read a value",
		Means: "The client has a method by the expected name, but it does not answer a single value, so it cannot be used to read the field.",
		Fix:   "This follows from what the API client generator produced.",
	},
	sdkbind.CauseNotASetter: {
		Title: "The client's method does not set a value",
		Means: "The client has a method by the expected name, but it does not take a single value, so it cannot be used to write the field.",
		Fix:   "This follows from what the API client generator produced.",
	},
	sdkbind.CauseUnbridgeableType: {
		Title: "The client carries this as a type that will not convert",
		Means: "The client holds the value as something no safe conversion turns into the Terraform attribute it would have to be.",
		Fix:   "This follows from what the API client generator made of the type the specification declares.",
	},
	sdkbind.CauseNoNestedModel: {
		Title: "The client does not model this nested object",
		Means: "The client returns something with no fields to map, so the nested object could not be built.",
		Fix:   "This follows from what the API client generator produced.",
	},
	sdkbind.CauseNoConstructor: {
		Title: "The client offers no way to build this object",
		Means: "The provider has to construct this object to send it, and the client declares nothing that constructs one.",
		Fix:   "This follows from what the API client generator produced.",
	},
	sdkbind.CauseEmptyNestedObject: {
		Title: "Nothing inside this object survived",
		Means: "Every field of the nested object was set aside for its own reason, leaving an object with nothing in it.",
		Fix:   "Look at the fields' own reasons; this is the consequence of them, not a separate problem.",
	},
	sdkbind.CauseUnresolvableCall: {
		Title: "The generated client has no such call",
		Means: "The call the provider would make does not exist on the client that was generated, so the whole thing was set aside rather than emit code that cannot compile.",
		Fix:   "This follows from what the API client generator made of the paths the specification declares.",
	},
	sdkbind.CauseNoRequestBodyType: {
		Title: "The client has no type for this request body",
		Means: "The API takes a body the client declares no type for, so there is nothing for the provider to fill in and send.",
		Fix:   "This follows from what the API client generator made of the request the specification declares.",
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
	sdkbind.CauseListResourceNoListCall: {
		Title: "There is no list call to build this from",
		Means: "A list needs a call that returns the collection, and none was bound.",
		Fix:   "This follows from the described paths and what the client generator made of them.",
	},
	sdkbind.CauseActionNoInvokeCall: {
		Title: "There is no call to invoke",
		Means: "An action is one call, and none was bound.",
		Fix:   "This follows from the described paths and what the client generator made of them.",
	},

	// Writing the provider code.
	CauseDatasourceNoReadCall: {
		Title: "The data source has no way to fetch one object",
		Means: "A data source addressed by key fetches a single object by that key. No such call survived the earlier steps, so there was nothing for the generated data source to call and it was not written.",
		Fix:   "The call was set aside earlier for its own reason; that reason is the one to act on.",
	},
	CauseDatasourceNoListCall: {
		Title: "The data source has no way to list objects",
		Means: "A data source that returns a collection needs a call that lists it. No such call survived the earlier steps, so the data source was not written.",
		Fix:   "The call was set aside earlier for its own reason; that reason is the one to act on.",
	},
	CauseDatasourceReadYieldsNoPayload: {
		Title: "The data source's read returns nothing to read",
		Means: "Terraform fills a data source from what the API answers with. This one's read call returns no body, so there would be nothing to put in it.",
		Fix:   "Nothing here can change this. The API would have to return the object.",
	},
	CauseDatasourceReadAnswersCollection: {
		Title: "The data source's read returns a collection, not one object",
		Means: "This data source is addressed by a single key, so it must answer with a single object. The API answers with a collection instead, which cannot be mapped into one object's state without choosing between the items.",
		Fix:   "A correction can describe the response as a single object where the API returns one; otherwise the API itself would have to change.",
	},
	CauseDatasourceListYieldsNoPayload: {
		Title: "The data source's list returns nothing to read",
		Means: "The call that lists these returns no body, so there would be nothing to fill the results with.",
		Fix:   "Nothing here can change this. The API would have to return the collection.",
	},
	CauseDatasourceNoItemsAttribute: {
		Title: "The data source has nowhere to put its results",
		Means: "A data source that lists puts the results in an attribute. Every field of this one was set aside at an earlier step, leaving no attribute to hold them.",
		Fix:   "The fields were set aside for their own reasons; those are the ones to act on.",
	},
	CauseDatasourceNoElementType: {
		Title: "It is not known what one result looks like",
		Means: "The generated client does not say what type the items of this collection are, so the results attribute could not be given a shape.",
		Fix:   "This follows from what the API client generator made of the OpenAPI 3 specification.",
	},

	CauseResourceNoLifecycleCall: {
		Title: "The resource is missing one of the calls it needs",
		Means: "Terraform manages a resource by creating, reading and deleting it, or by reading and updating one fixed object. At least one of those calls did not survive the earlier steps, so the resource could not be written; a resource missing one of them would fail on the first apply.",
		Fix:   "The call was set aside earlier for its own reason; that reason is the one to act on.",
	},
	CauseResourceReadYieldsNoPayload: {
		Title: "The resource's read returns nothing to read",
		Means: "Terraform keeps a resource in step with the API by reading it back. This read returns no body, so there would be no way to tell what the API holds.",
		Fix:   "Nothing here can change this. The API would have to return the object.",
	},
	CauseResourceNoKeyedReadPath: {
		Title: "The generated tests have nothing to key a response on",
		Means: "The generated tests answer a read by matching on a value in the URL, and this read's URL carries none. This affects the tests that ship with the provider, not the provider itself.",
		Fix:   "Nothing here can change this, and nothing in the provider is missing because of it.",
	},

	CauseListResourceListYieldsNoPayload: {
		Title: "The list returns nothing to read",
		Means: "A list is filled from what the API answers with, and this call returns no body.",
		Fix:   "Nothing here can change this. The API would have to return the collection.",
	},
	CauseListResourceNoCorrelatingResourceExists: {
		Title: "There is no resource for this list to list",
		Means: "Terraform refuses to load a provider that offers a list for a resource the provider does not also offer, and it refuses the whole provider rather than that one list. The resource was set aside at an earlier step, so this list was left out to keep the rest of the provider loadable.",
		Fix:   "The resource was set aside for its own reason; that reason is the one to act on.",
	},
	CauseListResourceListedResourceHasNoIdentity: {
		Title: "The resource this lists has nothing that identifies it",
		Means: "Every result a list returns has to name the object it stands for, so the resource it lists must have a stable identifier. That resource has none, so there is nothing for a result to carry.",
		Fix:   "Recording which property carries the identifier resolves this; that is one of the things an API audit records.",
	},
	CauseListResourceElementHasNoIdentity: {
		Title: "The listed objects carry nothing that identifies them",
		Means: "Terraform needs a stable identifier for each object in a list, and nothing the API returns for these is recognisable as one.",
		Fix:   "Recording which property carries the identifier resolves this; that is one of the things an API audit records.",
	},
	CauseListResourceIdentityNotConfigurable: {
		Title: "A value that identifies the object cannot be supplied",
		Means: "Part of what identifies one of these objects is not something the list block can be given, so a result could not be assembled that names the object it stands for.",
		Fix:   "A correction that makes the value part of the object, rather than something supplied alongside it, resolves this.",
	},

	CauseUnmatchedPathArgument: {
		Title: "A value in the API URL cannot be filled in",
		Means: "The API address contains a placeholder, and there is no attribute and no identifier the provider could take its value from, so the call could not be built.",
		Fix:   "A correction naming which property fills the placeholder lets this generate.",
	},
	CauseUnconvertiblePathType: {
		Title: "A value in the API URL is described as one type and generated as another",
		Means: "The placeholder in the API address is one type in the OpenAPI 3 specification and a different one in the generated API client, and no conversion between them is safe enough to do silently.",
		Fix:   "A correction aligning the described type with what the API actually takes resolves this.",
	},
	CauseUnvalidatableAttribute: {
		Title: "A rule refers to a field that is not there",
		Means: "A rule recorded about this object refers to a field the generated schema does not have, so the rule could not be written and is not enforced.",
		Fix:   "The recorded rule and the schema disagree; re-running an API audit usually settles which is right.",
	},
}

// Explain answers what a reader is told about one cause. The second result
// is false for a cause with no entry, which the caller shows as the bare
// code rather than inventing prose for it.
func Explain(code string) (Explanation, bool) {
	e, ok := explanations[code]
	return e, ok
}
