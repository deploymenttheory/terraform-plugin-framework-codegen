// Package blueprint defines the provider blueprint: the intermediate
// representation the generator emits from.
//
// It is a superset of HashiCorp's Provider Code Specification. That format can
// describe a schema and nothing else — it has no representation for CRUD wiring,
// for the SDK symbols a resource calls, for observed API behaviour, or for test
// scaffolding. Those are most of what a working provider is, so the blueprint
// carries them and the official format is something internal/interop reads and
// writes rather than this package's model.
//
// Three conventions run through the types and are worth knowing before reading
// them:
//
// Absence is representable everywhere. Optional scalars are pointers and
// optional collections are nil-able, because a merge layer has to be able to say
// "I have no opinion about this field" distinctly from "this field is false".
// Without that distinction the layered merge is not decidable.
//
// JSON keys are camelCase. The house golangci configuration enables tagliatelle,
// which defaults to camelCase, and the sibling SDK repository's metadata files
// already follow it.
//
// Nothing here carries a timestamp, an absolute path or a tool version. A
// blueprint is committed and diffed by CI, so any value that changes without an
// input changing would make the drift check useless.
package blueprint

import "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"

// FormatVersion is the blueprint format version. It is deliberately unrelated to
// the Provider Code Specification's version, which this format does not track.
const FormatVersion = "3"

// Blueprint is one provider.
type Blueprint struct {
	FormatVersion string `json:"formatVersion"`
	// Provider is omitzero because a blueprint is routinely split across files, and a
	// resource-only fragment has no provider to declare. Without it, every part written by
	// ingest, merge or interop import carries an empty provider block -- noise in a committed
	// file, and a fragment quietly claiming a provider it does not describe.
	Provider Provider `json:"provider,omitzero"`

	Resources   []Resource   `json:"resources,omitempty"`
	DataSources []DataSource `json:"dataSources,omitempty"`

	Source SourceInfo `json:"source,omitzero"`
}

// SourceInfo identifies the inputs a blueprint was derived from.
//
// It records a base filename rather than a path, and no timestamp, so that
// regenerating from the same snapshot produces a byte-identical document.
type SourceInfo struct {
	SpecFile    string `json:"specFile,omitempty"`
	SpecVersion string `json:"specVersion,omitempty"`
	SpecSHA256  string `json:"specSha256,omitempty"`
	// SnapshotDir is repo-relative, e.g. "7.0.97-t1785152261691".
	SnapshotDir string `json:"snapshotDir,omitempty"`
	// SDKVersion pins the SDK module the bindings were resolved against. A
	// dependency bump on the SDK can invalidate every binding in the blueprint,
	// and without this recorded there is no way to notice.
	SDKVersion string `json:"sdkVersion,omitempty"`
}

// Provider is the provider-wide configuration.
type Provider struct {
	// Name is the registry name, e.g. "thousandeyes".
	Name string `json:"name"`
	// GoModule is the generated provider's module path.
	GoModule string `json:"goModule"`
	// TypePrefix prefixes every resource type name. Usually equal to Name.
	TypePrefix string `json:"typePrefix"`

	SDK         SDKModule   `json:"sdk"`
	Conventions Conventions `json:"conventions,omitzero"`
	Support     SupportPkgs `json:"support,omitzero"`
}

// TerraformType composes the registry-visible type name for a block from its short
// Name, the way the framework composes it: Metadata sets
// req.ProviderTypeName + "_" + Name.
//
// Every caller that needs the composed name goes through here, so there is exactly
// one place the composition rule lives. That is the whole reason the blocks store
// only the short name — see the note on Resource.Name.
func (p Provider) TerraformType(name string) string {
	return naming.TerraformTypeName(p.TypePrefix, name)
}

// SDKDialect distinguishes the call shapes a generated provider must produce.
type SDKDialect string

const (
	// DialectRestyService is one Go package per API area, a service struct
	// holding a client, and methods returning (*Result, *resty.Response, error).
	// Shared by go-sdk-thousandeyes and go-sdk-jamfpro-v2.
	DialectRestyService SDKDialect = "restyService"
	// DialectKiotaFluent is a request-builder chain, as the Microsoft Graph SDK
	// generates. Reserved: the emitter does not implement it yet.
	DialectKiotaFluent SDKDialect = "kiotaFluent"
)

// SDKModule describes the SDK the emitted provider binds to.
type SDKModule struct {
	Dialect    SDKDialect `json:"dialect"`
	ModulePath string     `json:"modulePath"`

	// ClientType is the field type on every generated resource struct, written
	// as it appears at the use site, e.g. "*thousandeyes.Client".
	ClientType string `json:"clientType"`
	// ClientImport is the import that provides ClientType.
	ClientImport Import `json:"clientImport,omitzero"`
}

// Conventions are the emitter's per-target editorial choices. They are data
// because two providers in the same organisation legitimately differ.
type Conventions struct {
	ResourceRoot   string `json:"resourceRoot,omitempty"`
	DataSourceRoot string `json:"dataSourceRoot,omitempty"`
	ProviderPkgDir string `json:"providerPkgDir,omitempty"`

	DefaultTimeouts Timeouts `json:"defaultTimeouts,omitzero"`
}

// SupportPkgs names the hand-written packages in the target provider that
// generated code calls into. They belong to the provider, not the generator, so
// they are data rather than constants.
type SupportPkgs struct {
	Convert      Import `json:"convert,omitzero"`
	CRUD         Import `json:"crud,omitzero"`
	CommonSchema Import `json:"commonSchema,omitzero"`
	Errors       Import `json:"errors,omitzero"`
	Client       Import `json:"client,omitzero"`
}

// Import is one Go import. Alias is empty when the package name suffices.
type Import struct {
	Path  string `json:"path"`
	Alias string `json:"alias,omitempty"`
}

// Timeouts are the per-operation timeouts, in seconds, that the generated
// resource declares as constants and passes to the shared timeout helper.
type Timeouts struct {
	CreateSeconds int `json:"createSeconds,omitempty"`
	ReadSeconds   int `json:"readSeconds,omitempty"`
	UpdateSeconds int `json:"updateSeconds,omitempty"`
	DeleteSeconds int `json:"deleteSeconds,omitempty"`
}

// Resource is one Terraform managed resource.
type Resource struct {
	// Key is the stable merge key. Probe facts and hand-authored overrides join
	// on it, so it is the one field a human must never casually rename.
	Key string `json:"key"`

	// Name is the type name without the provider prefix, e.g. "tag".
	//
	// The registry-visible type is composed from Provider.TypePrefix at render time, exactly as the
	// framework composes it: Metadata sets req.ProviderTypeName + "_" + Name. Storing the composed
	// string as well would denormalise it, and a denormalised field is one that can disagree with
	// itself.
	Name string `json:"name"`
	// GoPackage is the directory and package name, e.g. "tag".
	GoPackage string `json:"goPackage"`
	// GoPackageAlias is the import alias the provider registration uses. It must
	// be unique across the whole provider.
	GoPackageAlias string `json:"goPackageAlias"`
	// GoTypeName is the resource struct, e.g. "TagResource".
	GoTypeName string `json:"goTypeName"`
	// ModelTypeName is the tfsdk model struct, e.g. "TagResourceModel".
	ModelTypeName string `json:"modelTypeName"`

	// ServiceGroup and APIVersionDir place the package on disk, giving
	// <resourceRoot>/<serviceGroup>/<apiVersionDir>/<goPackage>.
	ServiceGroup  string `json:"serviceGroup,omitempty"`
	APIVersionDir string `json:"apiVersionDir,omitempty"`

	// DocRefURL becomes the "// REF: <url>" comment above the model's package
	// clause, as the archetype provider does.
	DocRefURL string `json:"docRefUrl,omitempty"`

	Schema Schema `json:"schema"`

	Binding  ResourceBinding `json:"binding"`
	Policy   ResourcePolicy  `json:"policy,omitzero"`
	Import   ImportPolicy    `json:"import,omitzero"`
	Timeouts Timeouts        `json:"timeouts,omitzero"`

	// Drop excludes the resource from emission. Only a hand-authored override
	// layer may set it, so that a probe run against a tenant which can see
	// nothing cannot quietly delete half a provider.
	Drop bool `json:"drop,omitempty"`
}

// DataSource is one Terraform data source. Phase 1 does not emit these; the type
// exists so a blueprint written now does not need reshaping later.
type DataSource struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	GoPackage      string `json:"goPackage"`
	GoPackageAlias string `json:"goPackageAlias"`
	GoTypeName     string `json:"goTypeName"`
	ModelTypeName  string `json:"modelTypeName"`
	ServiceGroup   string `json:"serviceGroup,omitempty"`
	APIVersionDir  string `json:"apiVersionDir,omitempty"`

	DocRefURL string `json:"docRefUrl,omitempty"`

	Schema Schema `json:"schema"`

	Binding DataSourceBinding `json:"binding"`

	// Timeouts carries only a read deadline that is ever used, since read is the only
	// operation a data source has. The other three are accepted and ignored rather than
	// modelled separately, so that Conventions.DefaultTimeouts stays one type.
	Timeouts Timeouts `json:"timeouts,omitzero"`

	Drop bool `json:"drop,omitempty"`
}

// ComputedOptionalRequired is how Terraform treats an attribute. The four values are spelled
// exactly as the Provider Code Specification spells them, so interop needs no
// mapping table.
type ComputedOptionalRequired string

const (
	Required         ComputedOptionalRequired = "required"
	Optional         ComputedOptionalRequired = "optional"
	Computed         ComputedOptionalRequired = "computed"
	ComputedOptional ComputedOptionalRequired = "computed_optional"
)

// IsComputed reports whether the framework schema sets Computed.
func (p ComputedOptionalRequired) IsComputed() bool {
	return p == Computed || p == ComputedOptional
}

// IsOptional reports whether the framework schema sets Optional.
func (p ComputedOptionalRequired) IsOptional() bool {
	return p == Optional || p == ComputedOptional
}

// IsRequired reports whether the framework schema sets Required.
func (p ComputedOptionalRequired) IsRequired() bool { return p == Required }

// TypeKind is the framework type an attribute maps to.
type TypeKind string

const (
	KindBool    TypeKind = "bool"
	KindString  TypeKind = "string"
	KindInt32   TypeKind = "int32"
	KindInt64   TypeKind = "int64"
	KindFloat32 TypeKind = "float32"
	KindFloat64 TypeKind = "float64"
	KindNumber  TypeKind = "number"

	KindList TypeKind = "list"
	KindSet  TypeKind = "set"
	KindMap  TypeKind = "map"

	// The nested kinds hold an object shape rather than an element type.
	//
	// These are nested *attributes*, not blocks. Blocks are the older syntax and
	// the choice between them is permanent for a published provider, so it is made
	// deliberately here rather than inferred: HashiCorp's own OpenAPI generator
	// emits no blocks at all, which means there is no upstream signal to follow.
	KindListNested   TypeKind = "list_nested"
	KindSetNested    TypeKind = "set_nested"
	KindSingleNested TypeKind = "single_nested"
)

// IsCollection reports whether the kind needs an element type.
func (k TypeKind) IsCollection() bool {
	return k == KindList || k == KindSet || k == KindMap
}

// IsNested reports whether the kind holds a nested object shape.
func (k TypeKind) IsNested() bool {
	return k == KindListNested || k == KindSetNested || k == KindSingleNested
}

// IsNestedCollection reports whether the kind holds many nested objects, as
// opposed to exactly one.
func (k TypeKind) IsNestedCollection() bool {
	return k == KindListNested || k == KindSetNested
}

// AttrType is an attribute's type.
type AttrType struct {
	Kind TypeKind `json:"kind"`
	// ElementType is the element type of a list, set or map of scalars.
	ElementType *AttrType `json:"elementType,omitempty"`
	// NestedObject is the object shape of a nested kind.
	NestedObject *NestedAttributeObject `json:"nestedObject,omitempty"`

	// AllowedValues is the value set the *specification* documents.
	//
	// It is the declared half of a pair, and Behaviour.AcceptedValues is the observed half --
	// the same split the IR already uses for ComputedOptionalRequired against
	// Behaviour.RequiredByAPI, and Default against Behaviour.ServerDefault. At a use site the
	// two are never bare, so a.Type.AllowedValues and a.Behaviour.AcceptedValues say which is
	// which without the reader having to remember.
	//
	// A generated OneOf comes from *this* set rather than from what was observed, which reads
	// backwards until you consider which way each errs. The documented set is a superset of
	// what any one tenant accepts, so a validator built from it errs toward permitting: a stale
	// specification surfaces as a real API error carrying the API's own message. Built from the
	// observed accepted set it would err toward blocking, and the pilot is the case in point --
	// accessType documents "system", this sandbox refused it, and another licence may allow it.
	//
	// It is also the claim the enum probe exists to refute, which is why it must provably come
	// from the specification rather than from somebody's transcription: the pilot's committed
	// description listed five object types where the specification declares six.
	AllowedValues []string `json:"allowedValues,omitempty"`
}

// NestedAttributeObject is the object shape a nested attribute holds.
type NestedAttributeObject struct {
	// GoTypeName is the generated tfsdk model for this level, e.g.
	// "TagAssignmentModel". It is a sibling of the parent model, not an inner type,
	// because the framework needs a named type to decode elements into.
	GoTypeName string `json:"goTypeName"`

	// SDKType is the SDK struct one element maps to, e.g. "tags.Assignment".
	SDKType string `json:"sdkType"`

	// AttrTypesVar is the generated package-level variable holding this shape's
	// attr.Type map. Both the expand and the flatten helper reference it, so it is
	// declared once rather than repeated and allowed to drift.
	AttrTypesVar string `json:"attrTypesVar"`
	// ObjectTypeVar is the generated variable holding the types.ObjectType.
	ObjectTypeVar string `json:"objectTypeVar"`

	// ExpandFunc and FlattenFunc are the generated per-shape conversion helpers.
	ExpandFunc  string `json:"expandFunc"`
	FlattenFunc string `json:"flattenFunc"`

	Attributes []Attribute `json:"attributes"`
}

// Schema is a block's schema, mirroring the framework's own schema.Schema.
//
// Every block kind has its own schema package -- resource/schema, datasource/schema,
// ephemeral/schema, action/schema, list/schema -- whose types are structurally identical and differ
// only in which fields they carry. So one Schema serves every kind, and which of an Attribute's
// fields are legal is a function of the kind rendering it: see Attribute.
//
// Deliberately no Blocks. Blocks are the older configuration syntax and the choice is permanent for
// a published provider. The reference provider surveyed for phase 5 --
// terraform-provider-microsoft365, 170 resources -- uses ListNestedBlock exactly once, with an empty
// validator slice and its cardinality enforced by hand elsewhere, against 366 SingleNestedAttribute,
// 178 ListNestedAttribute and 110 SetNestedAttribute. It should have been a nested attribute. That
// is the evidence for nested attributes only, replacing an earlier argument from the absence of an
// upstream signal.
type Schema struct {
	Attributes []Attribute `json:"attributes"`

	// Version is the schema version, bumped when an attribute change needs a state upgrader. Zero
	// is the framework's default and the common case.
	Version int64 `json:"version,omitempty"`

	Description         string `json:"description,omitempty"`
	MarkdownDescription string `json:"markdownDescription,omitempty"`
	DeprecationMessage  string `json:"deprecationMessage,omitempty"`
}

// Attribute is one Terraform schema attribute.
//
// One type serves every block kind, and not every field is legal in every one of them. The
// framework's per-kind schema packages differ: an action or list attribute has no Computed or
// Sensitive, and only a resource attribute has PlanModifiers or Default. Validate refuses a field
// the target kind has no home for rather than emitting code that will not compile.
type Attribute struct {
	// Name is the tfsdk name, snake_case.
	Name string `json:"name"`
	// GoField is the model struct field.
	GoField string `json:"goField"`

	Type                     AttrType                 `json:"type"`
	ComputedOptionalRequired ComputedOptionalRequired `json:"computedOptionalRequired"`

	Sensitive bool `json:"sensitive,omitempty"`
	// WriteOnly marks a value the practitioner supplies that is never persisted to state. Legal on
	// resource and action attributes only.
	WriteOnly bool `json:"writeOnly,omitempty"`

	MarkdownDescription string `json:"markdownDescription,omitempty"`
	DeprecationMessage  string `json:"deprecationMessage,omitempty"`

	Validators    []CustomCode `json:"validators,omitempty"`
	PlanModifiers []CustomCode `json:"planModifiers,omitempty"`
	Default       *Default     `json:"default,omitempty"`

	Behaviour Behaviour   `json:"behaviour,omitzero"`
	Wire      WireBinding `json:"wire,omitzero"`

	Drop bool `json:"drop,omitempty"`
}

// CustomCode is a rendered Go expression plus the imports it needs. It matches
// the only form the Provider Code Specification allows for validators and plan
// modifiers, so interop is a straight copy.
type CustomCode struct {
	SchemaDefinition string   `json:"schemaDefinition"`
	Imports          []Import `json:"imports,omitempty"`
}

// Default is an attribute default: either a static primitive or custom code.
type Default struct {
	Static *Literal    `json:"static,omitempty"`
	Custom *CustomCode `json:"custom,omitempty"`
}

// Literal is a scalar that knows its own Go rendering, so that a default of 0 or
// false is distinguishable from an absent default.
type Literal struct {
	Kind TypeKind `json:"kind"`
	// Raw is the Go literal as it will be emitted: `"devices"`, `0`, `false`.
	Raw string `json:"raw"`
}

// Behaviour is what the API actually does, as opposed to what its specification
// claims. It is populated chiefly by the prober.
//
// Every field is a pointer because "not observed" must be distinguishable from
// "observed to be false". That distinction is what makes the layered merge
// decidable, and it is why this struct looks more verbose than it needs to.
type Behaviour struct {
	// Writable is false for a field the API accepts and discards, which is the
	// difference between Computed and Optional+Computed.
	Writable *bool `json:"writable,omitempty"`
	// Immutable drives RequiresReplace. A false positive here destroys real
	// infrastructure, so the prober only asserts it under a strict protocol.
	Immutable *bool `json:"immutable,omitempty"`
	// RequiredByAPI records what the API enforces, which is frequently not what
	// the specification declares, in both directions.
	RequiredByAPI *bool `json:"requiredByApi,omitempty"`
	// ServerDefault is the value the API applies when the field is omitted.
	ServerDefault *Literal `json:"serverDefault,omitempty"`
	// ReturnedOnRead is false for a field accepted on write and never read back,
	// which must not be flattened or state blanks on every read.
	ReturnedOnRead *bool `json:"returnedOnRead,omitempty"`
	// Volatile marks a field that differs between two identical reads, which
	// must be Computed or every plan reports drift.
	Volatile *bool `json:"volatile,omitempty"`

	// AcceptedValues and RejectedValues are the documented values this API took and refused.
	// They are the observed half of AttrType.AllowedValues.
	//
	// Recorded structurally rather than only in prose because a rejected documented value is
	// evidence the specification is stale, and the generated schema names those values in a
	// comment beside the validator -- which it can only do if they survive the merge as data.
	AcceptedValues []string `json:"acceptedValues,omitempty"`
	RejectedValues []string `json:"rejectedValues,omitempty"`

	// ValuesClosed is false when the API accepted a value from outside the documented set.
	//
	// That is the one case with direct evidence that a generated OneOf would be harmful: it
	// would reject configurations this API demonstrably takes. Render suppresses the validator
	// on it, which is why merge must carry it per attribute rather than discarding it as
	// resource-level information.
	ValuesClosed *bool `json:"valuesClosed,omitempty"`
}

// IsZero reports whether nothing has been observed about an attribute.
//
// Behaviour stopped being comparable with != when it gained slices, and this is the one
// definition of "empty" rather than a comparison spelled out at each call site.
// encoding/json's omitzero uses it as well, so the JSON and the Go agree by construction.
func (b Behaviour) IsZero() bool {
	return b.Writable == nil &&
		b.Immutable == nil &&
		b.RequiredByAPI == nil &&
		b.ServerDefault == nil &&
		b.ReturnedOnRead == nil &&
		b.Volatile == nil &&
		len(b.AcceptedValues) == 0 &&
		len(b.RejectedValues) == 0 &&
		b.ValuesClosed == nil
}

// UpdateStyle is how the API's update operation treats fields the request omits.
// It decides whether the generated construct function may skip nulls.
type UpdateStyle string

const (
	// UpdateMergePatch leaves omitted fields alone.
	UpdateMergePatch UpdateStyle = "patchMerge"
	// UpdatePutFull clears omitted fields, so the request must carry the whole
	// object. The ThousandEyes tag endpoint is PUT, and getting this wrong
	// silently erases attributes the practitioner did not mention.
	UpdatePutFull UpdateStyle = "putFull"
	// UpdateReplaceOnly means the API has no update, so every writable attribute
	// needs RequiresReplace.
	UpdateReplaceOnly UpdateStyle = "replaceOnly"
)

// ResourcePolicy is the per-resource behaviour the generated CRUD depends on.
type ResourcePolicy struct {
	UpdateStyle UpdateStyle `json:"updateStyle,omitempty"`
	ReadBack    ReadBack    `json:"readBack,omitzero"`
	Delete      Delete      `json:"delete,omitzero"`
}

// ReadBack controls the re-read after create and update. An API that is not
// read-your-writes consistent needs it, and one that is does not.
type ReadBack struct {
	Enabled    bool `json:"enabled,omitempty"`
	MaxRetries int  `json:"maxRetries,omitempty"`
	IntervalMS int  `json:"intervalMs,omitempty"`
	// Reason is rendered as a comment in the generated code. A retry loop with
	// no stated reason is indistinguishable from cargo cult.
	Reason string `json:"reason,omitempty"`
}

// Delete is the delete operation's observed semantics.
type Delete struct {
	// NotFoundIsSuccess treats a 404 as "already gone" rather than an error.
	NotFoundIsSuccess bool `json:"notFoundIsSuccess,omitempty"`
}

// ImportStyle is how terraform import maps an ID onto state.
type ImportStyle string

const (
	// ImportPassthroughID assigns the import ID straight to one attribute.
	ImportPassthroughID ImportStyle = "passthroughId"
	// ImportUnsupported suppresses ImportState entirely.
	ImportUnsupported ImportStyle = "unsupported"
)

// ImportPolicy configures ImportState.
type ImportPolicy struct {
	Style ImportStyle `json:"style,omitempty"`
	// Attribute is the target for a passthrough import, almost always "id".
	Attribute string `json:"attribute,omitempty"`
	// Example is the documented import ID, used in generated documentation.
	Example string `json:"example,omitempty"`
}
