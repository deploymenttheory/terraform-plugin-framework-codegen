// classifies, reduced to the decisions generation acts on — names,
// operations, attribute trees, lifecycle behaviour.
//
// The name is owner-mandated, underscores included, and the package is
// ephemeral by design. Derive is a pure function of the revised document
// and tfpfgen.yaml, recomputed on every generation run, and the result is
// never written to the repository. A committed intermediate representation
// is a second source of truth: hand adjustments to the derived file fight
// every regeneration, and the document stops being what generation actually
// reads. Here the revised document is the only committed truth; anything a
// human wants changed goes through a correction to that document, and the
// derivation follows.
//
// The model is JSON-marshalable so a debug flag can dump it for
// inspection, but a dump is a debugging aid, not an artifact — nothing may
// read one back in.
package intermediate_representation

import (
	"strings"
	"time"
)

// Model is the complete derived representation of one provider.
type Model struct {
	Provider      Provider       `json:"provider"`
	Resources     []Resource     `json:"resources,omitempty"`
	Datasources   []Datasource   `json:"datasources,omitempty"`
	ListResources []ListResource `json:"listResources,omitempty"`
	Actions       []Action       `json:"actions,omitempty"`
	// ExcludedByConfiguration is what services.exclude dropped, and
	// ExcludedByClassification what fit no kind. Two decisions taken at
	// different points and remedied differently — one is an entry the
	// operator wrote, the other a shape the document does not offer — so
	// one slice holding both would report them under one label.
	ExcludedByConfiguration  []UnsupportedEntity `json:"excludedByConfiguration,omitempty"`
	ExcludedByClassification []UnsupportedEntity `json:"excludedByClassification,omitempty"`
}

// Provider is the identity the generated provider publishes under.
type Provider struct {
	Name string `json:"name"`
}

// Cause is the one fact behind a refusal, and behind every other refusal
// that shares it. Two refusals belong to the same cause when their code and
// subject match, so grouping is an exact comparison of fields rather than a
// guess at which prose reasons mean the same thing.
//
// A datasource that loses twenty-four attributes because the SDK models the
// search envelope rather than the record has one cause and twenty-four
// consequences. Reported without it that is twenty-four findings, and a
// reader has to notice for themselves that they are one.
type Cause struct {
	// Code names what went wrong, from a closed set. It does not repeat the
	// stage, which the refusal already carries.
	Code string `json:"code"`
	// Subject is what it went wrong about — an SDK type, a declared type —
	// and is empty where the code is the whole account.
	Subject string `json:"subject,omitempty"`
}

// The causes the derivation refuses an attribute for. The set is closed: a
// refusal with no code cannot be grouped with the refusals that share its
// fact, which is the whole reason a cause is recorded.
const (
	CauseUndeclaredType                                = "undeclaredType"
	CauseUnsupportedType                               = "unsupportedType"
	CauseWritableUnion                                 = "writableUnion"
	CauseUnnamedUnionBranch                            = "unnamedUnionBranch"
	CauseEmptyUnion                                    = "emptyUnion"
	CauseUntypedAdditionalProperties                   = "untypedAdditionalProperties"
	CauseObjectWithoutPropertiesOrAdditionalProperties = "objectWithoutPropertiesOrAdditionalProperties"
	CauseMapOfObjects                                  = "mapOfObjects"
	CauseNestedCollectionElement                       = "nestedCollectionElement"
	CauseUnsupportedMapValue                           = "unsupportedMapValue"
	CauseItemlessArray                                 = "itemlessArray"
	CauseFreeFormArrayElement                          = "freeFormArrayElement"
	CauseUnsupportedArrayElement                       = "unsupportedArrayElement"
	CauseReservedRootName                              = "reservedRootName"

	// CauseExcludedByConfiguration is the operator's own services.exclude
	// entry, which is the one refusal they already know about.
	CauseExcludedByConfiguration = "excludedByConfiguration"
)

// UnsupportedEntity is one entity that became nothing, and why. The reasons are
// for people: a surprising omission traces to the document or the config,
// never to a hunch.
type UnsupportedEntity struct {
	// Key is the entity key it would have had.
	Key string `json:"key"`
	// TerraformBlockType is what it would have become, empty for an entity that became
	// nothing at all — which is the honest answer for one refused before a
	// kind was decided.
	TerraformBlockType string `json:"terraformBlockType,omitempty"`
	// CollectionPath is the path it was derived from, and Service and Tag
	// are where that path and the document place it. An entity refused
	// before it became anything still belongs somewhere, and a refusal
	// carrying no location is one nothing can group or act on.
	CollectionPath string `json:"collectionPath,omitempty"`
	Service        string `json:"service,omitempty"`
	Tag            string `json:"tag,omitempty"`
	// Cause is the fact behind the refusal, shared with every other entity
	// refused for the same fact.
	Cause Cause `json:"cause,omitempty"`
	// Reason is why it was refused, verbatim from the stage that refused.
	Reason string `json:"reason"`
}

// coManagementNote renders the prose the generated schema description of a
// co-managed entity must carry, given the sibling entities' terraform type
// names. It is written once, here, so every emitter renders exactly the
// same words; siblings arrive sorted so the note is deterministic.
func coManagementNote(siblings []string) string {
	return "Fields of this entity may also be managed by " + strings.Join(siblings, ", ") +
		", generated from the same underlying API collection family. " +
		"Managing the same object through more than one of them concurrently causes drift, " +
		"and the last terraform apply wins."
}

// Update styles, mirroring the x-tfpfgen-update-style contract values.
const (
	UpdateStylePatchMerge  = "patch-merge"
	UpdateStylePutFull     = "put-full"
	UpdateStyleReplaceOnly = "replace-only"
)

// Resource is one entity with the full lifecycle Terraform can own.
type Resource struct {
	Names      Names      `json:"names"`
	Operations Operations `json:"operations"`
	// Attributes is the attribute tree: the create request schema combined
	// with the read response schema, response-only fields computed.
	Attributes *AttributeTree `json:"attributes"`
	// MissingUpdate means the API declares no update operation, so every
	// writable attribute carries RequiresReplace.
	MissingUpdate bool `json:"missingUpdate,omitempty"`
	// Singleton means the entity is one object at a fixed path rather than a
	// member of a collection. It has no create and no delete: the generated
	// create writes through the api update (put / patch) operation, and the generated delete
	// forgets the object without calling the API, because there is nothing
	// to destroy.
	Singleton bool `json:"singleton,omitempty"`
	// UpdateStyle is how update treats omitted fields. patch-merge is the
	// default whenever an update operation exists; empty when none does.
	UpdateStyle string `json:"updateStyle,omitempty"`
	// ReadAfterWriteDelay is how long a read may lag a write, the largest
	// x-tfpfgen-read-after-write declared on any lifecycle operation.
	ReadAfterWriteDelay time.Duration `json:"readAfterWriteDelay,omitempty"`
	// DeleteNotFoundOK means a 404 on delete reads as "already gone".
	DeleteNotFoundOK bool `json:"deleteNotFoundOK,omitempty"`
	// Timeouts are the generated timeout defaults.
	Timeouts Timeouts `json:"timeouts"`
	// CoManagementNote is set when sibling entities derive from the same
	// underlying collection path family: prose every emitter appends to
	// the generated schema description verbatim. Empty for an entity with
	// no siblings.
	CoManagementNote string `json:"coManagementNote,omitempty"`
	// ListWrapperKey is the wire property the list response wraps its item
	// array under, read from the list operation's response schema; empty when
	// the response is a bare array. It drives the generated list-mock
	// envelope, replacing the assumption that every API wraps under "value".
	ListWrapperKey string `json:"listWrapperKey,omitempty"`
	// ParentEntity is the key of the entity whose collection path encloses
	// this one's; empty at the top level. A singleton's path parameters all
	// address that parent, and the attribute answering a parameter the
	// document spells `id` is named after it.
	ParentEntity string `json:"parentEntity,omitempty"`
}

// Datasource is one entity readable outside Terraform's ownership. Every
// resource yields a companion datasource; an entity whose only access is
// the item GET yields a lookup-by-key one.
type Datasource struct {
	Names      Names      `json:"names"`
	Operations Operations `json:"operations"`
	// Attributes is the datasource's attribute tree. A companion datasource is
	// one optional filter per scalar field of a listed object, and a
	// computed list of the objects the filters selected. A lookup-by-key
	// one is the entity's object with the key parameter as its single
	// required argument.
	Attributes *AttributeTree `json:"attributes"`
	// LookupByKey means there is no list operation: the caller supplies
	// the item path parameter and reads the object it identifies.
	LookupByKey bool `json:"lookupByKey,omitempty"`
	// KeyParameter is the item path parameter's wire name when
	// LookupByKey is set, empty otherwise.
	KeyParameter string `json:"keyParameter,omitempty"`
	// CoManagementNote is the sibling-entity prose; see Resource.
	CoManagementNote string `json:"coManagementNote,omitempty"`
	// ListWrapperKey is the list response's item-array wrapper key; see
	// Resource. Empty for a bare array or a lookup-by-key datasource.
	ListWrapperKey string `json:"listWrapperKey,omitempty"`
}

// ListResource is the list capability of a managed resource: terraform
// matches it to that resource by type name, so it carries the entity's own
// Names and exists only where the resource does.
type ListResource struct {
	Names         Names     `json:"names"`
	ListOperation Operation `json:"listOperation"`
	// Attributes is the element's attribute tree, everything computed.
	Attributes *AttributeTree `json:"attributes"`
	// AddressingAttributes is the addressing attributes the collection path
	// requires, declared as the list block's own configuration. Nil for a
	// collection path that takes no parameters.
	AddressingAttributes *AttributeTree `json:"addressingAttributes,omitempty"`
	// CoManagementNote is the sibling-entity prose; see Resource.
	CoManagementNote string `json:"coManagementNote,omitempty"`
	// ListWrapperKey is the list response's item-array wrapper key; see
	// Resource. Empty for a bare array.
	ListWrapperKey string `json:"listWrapperKey,omitempty"`
}

// Action is a POST with no lifecycle complement — an invocation.
type Action struct {
	Names           Names     `json:"names"`
	InvokeOperation Operation `json:"invokeOperation"`
	// RequestAttributes is the invocation's argument tree, nil when the POST
	// takes no body.
	RequestAttributes *AttributeTree `json:"requestAttributes,omitempty"`
	// ParentEntity is the key of the entity whose collection path is the
	// longest prefix of the action's, empty when no entity encloses it.
	ParentEntity string `json:"parentEntity,omitempty"`
	// CoManagementNote is the sibling-entity prose; see Resource.
	CoManagementNote string `json:"coManagementNote,omitempty"`
}

// Operations holds an entity's operations by role. Slots an entity lacks are nil.
type Operations struct {
	Create *Operation `json:"create,omitempty"`
	Read   *Operation `json:"read,omitempty"`
	Update *Operation `json:"update,omitempty"`
	Delete *Operation `json:"delete,omitempty"`
	List   *Operation `json:"list,omitempty"`
}

// OperationKind is an operation's role in the entity's lifecycle.
type OperationKind string

// Operation roles.
const (
	OperationCreate OperationKind = "create"
	OperationRead   OperationKind = "read"
	OperationUpdate OperationKind = "update"
	OperationDelete OperationKind = "delete"
	OperationList   OperationKind = "list"
	OperationAction OperationKind = "invoke"
)

// Operation is one operation, dialect-neutral: enough for any SDK binder to find
// the call it must attach, and nothing backend-shaped. Binders produce a
// parallel structure keyed by these fields; no backend type appears here.
type Operation struct {
	Kind           OperationKind      `json:"kind"`
	Method         string             `json:"method"`
	PathTemplate   string             `json:"pathTemplate"`
	OperationID    string             `json:"operationID,omitempty"`
	PathParameters []URLPathParameter `json:"pathParameters,omitempty"`
	// QueryParameters are the query parameters the operation requires,
	// each with the value the document states for it. A generated call
	// sends them as constants: they belong to the operation, not to the
	// object, so no attribute carries them.
	QueryParameters []QueryParameter `json:"queryParameters,omitempty"`
	// SuccessCode is the first declared 2xx status, 0 when only a
	// default response exists.
	SuccessCode int `json:"successCode,omitempty"`
}

// URLPathParameter is one path parameter, in path-template order.
type URLPathParameter struct {
	Name string        `json:"name"`
	Type AttributeType `json:"type"`
}

// QueryParameter is one required query parameter and the value the
// document states for it — the parameter's own example first, then the
// schema's example, then its default — because a required parameter with
// no stated value is one the document leaves a generator unable to send.
type QueryParameter struct {
	Name  string        `json:"name"`
	Type  AttributeType `json:"type"`
	Value any           `json:"value"`
}

// AttributeType is a terraform-plugin-framework attribute type.
type AttributeType string

// Attribute type kinds.
const (
	TypeString  AttributeType = "string"
	TypeBool    AttributeType = "bool"
	TypeInt64   AttributeType = "int64"
	TypeFloat64 AttributeType = "float64"
	TypeList    AttributeType = "list"
	TypeMap     AttributeType = "map"
	TypeObject  AttributeType = "object"
)

// ComputedOptionalRequired is how an attribute participates in plans. The
// name and the four values are terraform-plugin-codegen-spec's, so a
// generated schema and the specification that could describe it agree on
// what to call the same fact.
type ComputedOptionalRequired string

// The four ways an attribute participates in a plan.
const (
	Required ComputedOptionalRequired = "required"
	Optional ComputedOptionalRequired = "optional"
	Computed ComputedOptionalRequired = "computed"
	// ComputedOptional marks a writable attribute the server fills when
	// omitted: optional in the request, always present in the response.
	ComputedOptional ComputedOptionalRequired = "computed_optional"
)

// SchemaAttributeTypeDetermination names which declaration decided a computed_optional presence,
// where four could have. It is empty where none did — see Attribute.
// The set is closed and ordered by how strongly the
// declaration is grounded, which is the order derivation consults them in:
// a measurement of the live API first, then the document asserting the same
// fact, then the document implying it, then the document merely describing
// the property.
//
// docs/contract.md sets out the four and accepts a known risk on
// requestDefault: a default on a $ref'd property is written onto a schema
// every other use of that type shares, so one declaration can move
// attributes that were never meant to move together. Recording the route
// makes that accepted risk something an operator can find and correct
// rather than only be told about.
type SchemaAttributeTypeDetermination string

const (
	// SchemaAttributeTypeDeterminationServerDefault is the audit's own measurement, taken by
	// omitting the attribute and reading what came back. It is the only
	// route that does not depend on the document being diligent.
	SchemaAttributeTypeDeterminationServerDefault SchemaAttributeTypeDetermination = "serverDefault"
	// SchemaAttributeTypeDeterminationResponseRequired is the response schema's required list:
	// the document asserting the server always answers with a value.
	SchemaAttributeTypeDeterminationResponseRequired SchemaAttributeTypeDetermination = "responseRequired"
	// SchemaAttributeTypeDeterminationRequestDefault is a default on the request property: the
	// document stating what the server substitutes for an omitted value.
	SchemaAttributeTypeDeterminationRequestDefault SchemaAttributeTypeDetermination = "requestDefault"
	// SchemaAttributeTypeDeterminationResponseProperty is the response schema describing the
	// property at all — the weakest route, and the one that catches the
	// documents which declare nothing required in their responses.
	SchemaAttributeTypeDeterminationResponseProperty SchemaAttributeTypeDetermination = "responseProperty"
)

// AttributeTree is one object's attributes plus the cross-attribute rules
// declared among them. Attribute order preserves document property order —
// load-bearing for stable output — with response-only attributes appended in
// response order. Every rule slice names attributes in terraform spelling and
// is emitted in a fixed order, so the tree cannot leak map iteration order.
type AttributeTree struct {
	Attributes []Attribute `json:"attributes,omitempty"`
	// Description is the document's own prose for the object the tree
	// describes, empty when it declares none. It leads the generated
	// schema's description, ahead of anything the derivation inferred.
	Description string `json:"description,omitempty"`
	// ConditionalRequirements aggregates x-tfpfgen-required-when: when
	// Property equals Equals, the Required attributes must be set.
	ConditionalRequirements []ConditionalRequirement `json:"conditionalRequirements,omitempty"`
	// ConditionalValidities aggregates x-tfpfgen-valid-when: the Valid
	// attributes may be set only when Property equals Equals.
	ConditionalValidities []ConditionalValidity `json:"conditionalValidities,omitempty"`
	// Dependencies aggregates x-tfpfgen-depends-on and dependentRequired: an
	// attribute may be set only when every attribute it Requires is set too.
	Dependencies []Dependency `json:"dependencies,omitempty"`
	// MutuallyExclusiveGroups aggregates x-tfpfgen-mutually-exclusive: at
	// most one attribute in each group may be set.
	MutuallyExclusiveGroups [][]string `json:"mutuallyExclusiveGroups,omitempty"`
	// ValidConfigurations aggregates x-tfpfgen-valid-configuration: a
	// discriminator attribute whose value selects which attributes are valid.
	ValidConfigurations []ValidConfiguration `json:"validConfigurations,omitempty"`
}

// ConditionalRequirement is one value-conditional rule, attribute names in
// terraform spelling.
type ConditionalRequirement struct {
	Property           string   `json:"property"`
	WhenPropertyEquals string   `json:"whenPropertyEquals"`
	Required           []string `json:"required"`
}

// ConditionalValidity is one value-conditional validity rule: the Valid
// attributes are valid only while Property equals Equals.
type ConditionalValidity struct {
	Property                 string   `json:"property"`
	WhenPropertyEquals       string   `json:"whenPropertyEquals"`
	AttributesValidWhenEqual []string `json:"attributesValidWhenEqual"`
}

// Dependency is one co-requirement: Attribute may be set only when every name
// in Requires is also set.
type Dependency struct {
	Attribute string   `json:"attribute"`
	Requires  []string `json:"requires"`
}

// ValidConfiguration is a discriminator variant structure: Discriminator's
// value selects which attributes each variant admits.
type ValidConfiguration struct {
	Discriminator string                      `json:"discriminator"`
	Variants      []ValidConfigurationVariant `json:"variants"`
}

// ValidConfigurationVariant is one discriminator value and the attributes valid under it.
type ValidConfigurationVariant struct {
	Value                    string   `json:"value"`
	AttributesValidWhenEqual []string `json:"attributesValidWhenEqual"`
}

// Attribute is one schema attribute, fully decided.
type Attribute struct {
	// Name is the terraform attribute name, snake_case.
	Name string `json:"name"`
	// WireName is the property name the API speaks.
	WireName string `json:"wireName"`
	// Description is the document's own prose for the property, empty when
	// it declares none. It is the only human-written text in the whole
	// derivation — everything else a generated schema says about an
	// attribute is inferred — so it leads the rendered description and the
	// inferred facts follow it.
	Description string        `json:"description,omitempty"`
	Type        AttributeType `json:"type,omitempty"`
	// ElementType is the type a list or map ultimately holds: the scalar at
	// the bottom, or object, in which case NestedAttributes carries its tree.
	// It is the one level of a list of strings and the last level of a list
	// of lists of strings.
	ElementType AttributeType `json:"elementType,omitempty"`
	// NestedCollectionElementTypes spells every level beneath a collection
	// whose element is itself a collection, outermost first and ending in
	// the leaf: a list of lists of strings carries [list, string], a map of
	// lists of objects [list, object]. Nil for a collection of scalars or of
	// objects, whose one level ElementType already states.
	NestedCollectionElementTypes []AttributeType `json:"nestedCollectionElementTypes,omitempty"`
	// NestedAttributes is the child tree of an object attribute, or of the
	// object at the bottom of a list or map at any depth.
	NestedAttributes         *AttributeTree           `json:"nestedAttributes,omitempty"`
	ComputedOptionalRequired ComputedOptionalRequired `json:"computedOptionalRequired"`
	// SchemaAttributeTypeDetermination names which declaration decided this attribute's
	// computed_optional presence. Empty where no declaration about this
	// attribute did: any other presence, or a member promoted to
	// computed_optional because its parent is server-filled, which is a
	// fact about the parent.
	SchemaAttributeTypeDetermination SchemaAttributeTypeDetermination `json:"schemaAttributeTypeDetermination,omitempty"`
	// RequiresReplace marks an attribute a change to which forces
	// re-creation: x-tfpfgen-immutable, or every writable attribute of
	// a resource with no update operation.
	RequiresReplace bool `json:"requiresReplace,omitempty"`
	// Format is the document's declared format, which says what a string
	// carries beyond being a string: "password", "date-time", "uuid".
	Format string `json:"format,omitempty"`
	// ServerDefault is the value a run read back for this property when a
	// create omitted it, from x-tfpfgen-server-default; nil when no run has
	// measured one. It is a fact about the API rather than the document.
	ServerDefault any `json:"serverDefault,omitempty"`
	// Example is the document's declared example value. Fixture derivation
	// prefers it to an invented value: a document that declares no format
	// often still states, through an example, that the value has a shape the
	// API enforces — a URL, a dotted identifier — and an invented string of
	// the right type is refused by the API for the wrong reason.
	Example any `json:"example,omitempty"`
	// Normalisation names how the API stores the value in a spelling of
	// its own (x-tfpfgen-normalisation): generated state keeps the
	// configured spelling when the answer is the stored form of it. Empty
	// where the API answers what it was sent.
	Normalisation string `json:"normalisation,omitempty"`
	// WriteOnly marks a property the API accepts on write and never
	// returns.
	WriteOnly bool `json:"writeOnly,omitempty"`
	// Sensitive marks a value terraform must keep out of its output: the
	// document either declares it write-only or formats it as a password.
	Sensitive bool `json:"sensitive,omitempty"`
	// Deprecated marks a property the document declares deprecated.
	Deprecated bool `json:"deprecated,omitempty"`
	// IsDatasourceFilterArgument marks a datasource argument that selects which listed objects
	// come back, rather than describing one. It carries no wire value and
	// binds to no SDK field: the match runs over the items the list already
	// answered with.
	IsDatasourceFilterArgument bool `json:"isDatasourceFilterArgument,omitempty"`
	// UniqueItems marks a collection whose members are a set, so the order
	// they are returned in carries no meaning.
	UniqueItems bool `json:"uniqueItems,omitempty"`
	// The constraints the document declares, nil or empty when it declares
	// none. Each becomes a plan-time validator, so a configuration the API
	// would refuse or silently clamp fails before it is sent.
	Pattern   string   `json:"pattern,omitempty"`
	Minimum   *float64 `json:"minimum,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`
	MinLength *int64   `json:"minLength,omitempty"`
	MaxLength *int64   `json:"maxLength,omitempty"`
	MinItems  *int64   `json:"minItems,omitempty"`
	MaxItems  *int64   `json:"maxItems,omitempty"`
	// OneOf lists a closed enum's values for a validator.
	OneOf []string `json:"oneOf,omitempty"`
	// AdvisoryValues lists an open enum's known values
	// (x-tfpfgen-values): documentation only, never validated.
	AdvisoryValues []string `json:"advisoryValues,omitempty"`
	// IgnoredOnUpdate marks a property updates accept and
	// discard; construct skips it on update.
	IgnoredOnUpdate bool `json:"ignoredOnUpdate,omitempty"`
	// Unsupported marks a shape derivation refuses to guess at (free-form
	// objects, unions, undeclared types); the reason says which.
	Unsupported bool `json:"unsupported,omitempty"`
	// UnsupportedCause is the fact behind the refusal, shared with every
	// other attribute refused for the same fact.
	UnsupportedCause  Cause  `json:"unsupportedCause,omitempty"`
	UnsupportedReason string `json:"unsupportedReason,omitempty"`
}

// Timeouts are the generated per-operation timeout defaults.
type Timeouts struct {
	Create time.Duration `json:"create"`
	Read   time.Duration `json:"read"`
	Update time.Duration `json:"update"`
	Delete time.Duration `json:"delete"`
}

// defaultTimeouts is one fixed set for every resource: long enough for
// slow control planes, short enough to fail within a pipeline run.
func defaultTimeouts() Timeouts {
	return Timeouts{
		Create: 30 * time.Minute,
		Read:   5 * time.Minute,
		Update: 30 * time.Minute,
		Delete: 30 * time.Minute,
	}
}

// unsupportedEntity records one refusal with everywhere it belongs, so a
// refusal can be grouped by the same keys a generated entity is.
func unsupportedEntity(names Names, collectionPath, kind string, cause Cause, reason string) UnsupportedEntity {
	return UnsupportedEntity{
		Key:                names.Key,
		TerraformBlockType: kind,
		CollectionPath:     collectionPath,
		Service:            names.Service,
		Tag:                names.Tag,
		Cause:              cause,
		Reason:             reason,
	}
}

// CollectionNestingDepth counts the collection levels wrapping an
// attribute's element: 0 for a scalar or an object, 1 for a list of strings
// or a map of objects, and the number of levels for a collection whose
// element is itself a collection.
func (attribute Attribute) CollectionNestingDepth() int {
	switch {
	case attribute.Type != TypeList && attribute.Type != TypeMap:
		return 0
	case len(attribute.NestedCollectionElementTypes) == 0:
		return 1
	default:
		return len(attribute.NestedCollectionElementTypes)
	}
}
