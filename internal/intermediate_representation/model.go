// Package intermediate_representation derives the representation the
// emitter and the SDK binders consume: every entity the revised document
// classifies, reduced to the decisions generation acts on — names,
// operations, attribute trees, lifecycle behaviour.
//
// The name is owner-mandated, underscores included, and the package is
// ephemeral by design. Derive is a pure function of the revised document
// and tfpfgen.yaml, recomputed on every generation run, and the result is
// never written to the repository. v1's fatal flaw was a committed
// intermediate representation: the moment the derived file lived in git it
// became a second source of truth, hand adjustments to it fought every
// regeneration, and the document stopped being what generation actually
// read. Here the revised document is the only committed truth; anything a
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
	ListResources []ListResource `json:"list_resources,omitempty"`
	Actions       []Action       `json:"actions,omitempty"`
	// Excluded lists every entity that yields nothing, with the reason:
	// classification exclusions pass through, and entities dropped by
	// services.exclude are recorded as "excluded by configuration".
	Excluded []Exclusion `json:"excluded,omitempty"`
}

// Provider is the identity the generated provider publishes under.
type Provider struct {
	Name string `json:"name"`
}

// Exclusion is one entity that became nothing, and why. The reasons are
// for people: a surprising omission traces to the document or the config,
// never to a hunch.
type Exclusion struct {
	Key    string `json:"key"`
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
	// Schema is the attribute tree: the create request schema combined
	// with the read response schema, response-only fields computed.
	Schema *AttributeTree `json:"schema"`
	// MissingUpdate means the API declares no update operation, so every
	// writable attribute carries RequiresReplace.
	MissingUpdate bool `json:"missing_update,omitempty"`
	// Singleton means the entity is one object at a fixed path rather than a
	// member of a collection. It has no create and no delete: the generated
	// create writes through the api update (put / patch) operation, and the generated delete
	// forgets the object without calling the API, because there is nothing
	// to destroy.
	Singleton bool `json:"singleton,omitempty"`
	// UpdateStyle is how update treats omitted fields. patch-merge is the
	// default whenever an update operation exists; empty when none does.
	UpdateStyle string `json:"update_style,omitempty"`
	// EventualConsistency is how long a read may lag a write, the largest
	// x-tfpfgen-eventual-consistency declared on any lifecycle operation.
	EventualConsistency time.Duration `json:"eventual_consistency,omitempty"`
	// DeleteNotFoundOK means a 404 on delete reads as "already gone".
	DeleteNotFoundOK bool `json:"delete_not_found_ok,omitempty"`
	// Timeouts are the generated timeout defaults.
	Timeouts Timeouts `json:"timeouts"`
	// CoManagementNote is set when sibling entities derive from the same
	// underlying collection path family: prose every emitter appends to
	// the generated schema description verbatim. Empty for an entity with
	// no siblings.
	CoManagementNote string `json:"co_management_note,omitempty"`
	// ListEnvelopeKey is the wire property the list response wraps its item
	// array under, read from the list operation's response schema; empty when
	// the response is a bare array. It drives the generated list-mock
	// envelope, replacing the assumption that every API wraps under "value".
	ListEnvelopeKey string `json:"list_envelope_key,omitempty"`
}

// Datasource is one entity readable outside Terraform's ownership. Every
// resource yields a companion datasource; an entity whose only access is
// the item GET yields a lookup-by-key one.
type Datasource struct {
	Names      Names      `json:"names"`
	Operations Operations `json:"operations"`
	// Schema is the datasource's attribute tree. A companion datasource
	// follows the filter_type/filter_value/items pattern: two inputs and
	// a computed list of the entity's objects. A lookup-by-key one is the
	// entity's object with the key parameter as its single required
	// argument.
	Schema *AttributeTree `json:"schema"`
	// LookupByKey means there is no list operation: the caller supplies
	// the item path parameter and reads the object it identifies.
	LookupByKey bool `json:"lookup_by_key,omitempty"`
	// KeyParameter is the item path parameter's wire name when
	// LookupByKey is set, empty otherwise.
	KeyParameter string `json:"key_parameter,omitempty"`
	// CoManagementNote is the sibling-entity prose; see Resource.
	CoManagementNote string `json:"co_management_note,omitempty"`
	// ListEnvelopeKey is the list response's item-array wrapper key; see
	// Resource. Empty for a bare array or a lookup-by-key datasource.
	ListEnvelopeKey string `json:"list_envelope_key,omitempty"`
}

// ListResource is a list-only entity: enumerable but not addressable.
type ListResource struct {
	Names         Names     `json:"names"`
	ListOperation Operation `json:"list_operation"`
	// Schema is the element's attribute tree, everything computed.
	Schema *AttributeTree `json:"schema"`
	// CoManagementNote is the sibling-entity prose; see Resource.
	CoManagementNote string `json:"co_management_note,omitempty"`
	// ListEnvelopeKey is the list response's item-array wrapper key; see
	// Resource. Empty for a bare array.
	ListEnvelopeKey string `json:"list_envelope_key,omitempty"`
}

// Action is a POST with no lifecycle complement — an invocation.
type Action struct {
	Names           Names     `json:"names"`
	InvokeOperation Operation `json:"invoke_operation"`
	// RequestSchema is the invocation's argument tree, nil when the POST
	// takes no body.
	RequestSchema *AttributeTree `json:"request_schema,omitempty"`
	// ParentEntity is the key of the entity whose collection path is the
	// longest prefix of the action's, empty when no entity encloses it.
	ParentEntity string `json:"parent_entity,omitempty"`
	// CoManagementNote is the sibling-entity prose; see Resource.
	CoManagementNote string `json:"co_management_note,omitempty"`
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
	OperationInvoke OperationKind = "invoke"
)

// Operation is one operation, dialect-neutral: enough for any SDK binder to find
// the call it must attach, and nothing backend-shaped. Binders produce a
// parallel structure keyed by these fields; no backend type appears here.
type Operation struct {
	Kind           OperationKind `json:"kind"`
	Method         string        `json:"method"`
	PathTemplate   string        `json:"path_template"`
	OperationID    string        `json:"operation_id,omitempty"`
	PathParameters []Parameter   `json:"path_parameters,omitempty"`
	// SuccessCode is the first declared 2xx status, 0 when only a
	// default response exists.
	SuccessCode int `json:"success_code,omitempty"`
}

// Parameter is one path parameter, in path-template order.
type Parameter struct {
	Name string        `json:"name"`
	Type AttributeType `json:"type"`
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
	ConditionalRequirements []ConditionalRequirement `json:"conditional_requirements,omitempty"`
	// ConditionalValidities aggregates x-tfpfgen-valid-when: the Valid
	// attributes may be set only when Property equals Equals.
	ConditionalValidities []ConditionalValidity `json:"conditional_validities,omitempty"`
	// Dependencies aggregates x-tfpfgen-depends-on and dependentRequired: an
	// attribute may be set only when every attribute it Requires is set too.
	Dependencies []Dependency `json:"dependencies,omitempty"`
	// MutuallyExclusiveGroups aggregates x-tfpfgen-mutually-exclusive: at
	// most one attribute in each group may be set.
	MutuallyExclusiveGroups [][]string `json:"mutually_exclusive_groups,omitempty"`
	// ValidConfigurations aggregates x-tfpfgen-valid-configuration: a
	// discriminator attribute whose value selects which attributes are valid.
	ValidConfigurations []ValidConfiguration `json:"valid_configurations,omitempty"`
}

// ConditionalRequirement is one value-conditional rule, attribute names in
// terraform spelling.
type ConditionalRequirement struct {
	Property string   `json:"property"`
	Equals   string   `json:"equals"`
	Required []string `json:"required"`
}

// ConditionalValidity is one value-conditional validity rule: the Valid
// attributes are valid only while Property equals Equals.
type ConditionalValidity struct {
	Property string   `json:"property"`
	Equals   string   `json:"equals"`
	Valid    []string `json:"valid"`
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
	Discriminator string          `json:"discriminator"`
	Variants      []ConfigVariant `json:"variants"`
}

// ConfigVariant is one discriminator value and the attributes valid under it.
type ConfigVariant struct {
	Value string   `json:"value"`
	Valid []string `json:"valid"`
}

// Attribute is one schema attribute, fully decided.
type Attribute struct {
	// Name is the terraform attribute name, snake_case.
	Name string `json:"name"`
	// WireName is the property name the API speaks.
	WireName string `json:"wire_name"`
	// Description is the document's own prose for the property, empty when
	// it declares none. It is the only human-written text in the whole
	// derivation — everything else a generated schema says about an
	// attribute is inferred — so it leads the rendered description and the
	// inferred facts follow it.
	Description string        `json:"description,omitempty"`
	Kind        AttributeType `json:"kind,omitempty"`
	// ElementType is the type within a list of scalars; lists of objects
	// carry Nested instead.
	ElementType AttributeType `json:"element_type,omitempty"`
	// Nested is the child tree of an object attribute or a list of
	// objects.
	Nested                   *AttributeTree           `json:"nested,omitempty"`
	ComputedOptionalRequired ComputedOptionalRequired `json:"computed_optional_required"`
	// RequiresReplace marks an attribute a change to which forces
	// re-creation: x-tfpfgen-create-only, or every writable attribute of
	// a resource with no update operation.
	RequiresReplace bool `json:"requires_replace,omitempty"`
	// OneOf lists a closed enum's values for a validator.
	OneOf []string `json:"one_of,omitempty"`
	// AdvisoryValues lists an open enum's known values
	// (x-tfpfgen-values-open): documentation only, never validated.
	AdvisoryValues []string `json:"advisory_values,omitempty"`
	// SilentlyIgnoredOnUpdate marks a property updates accept and
	// discard; construct skips it on update.
	SilentlyIgnoredOnUpdate bool `json:"silently_ignored_on_update,omitempty"`
	// Unsupported marks a shape derivation refuses to guess at (free-form
	// objects, unions, undeclared types); the reason says which.
	Unsupported       bool   `json:"unsupported,omitempty"`
	UnsupportedReason string `json:"unsupported_reason,omitempty"`
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
