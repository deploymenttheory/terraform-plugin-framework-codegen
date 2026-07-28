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

// FormatVersion is the blueprint format version. It is deliberately unrelated to
// the Provider Code Specification's version, which this format does not track.
const FormatVersion = "1"

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

	// TerraformType is the registry-visible type, e.g. "thousandeyes_tag".
	TerraformType string `json:"terraformType"`
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

	MarkdownDescription string `json:"markdownDescription,omitempty"`
	// DocRefURL becomes the "// REF: <url>" comment above the model's package
	// clause, as the archetype provider does.
	DocRefURL          string `json:"docRefUrl,omitempty"`
	DeprecationMessage string `json:"deprecationMessage,omitempty"`

	Attributes []Attribute `json:"attributes"`

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
	Key            string      `json:"key"`
	TerraformType  string      `json:"terraformType"`
	GoPackage      string      `json:"goPackage"`
	GoPackageAlias string      `json:"goPackageAlias"`
	GoTypeName     string      `json:"goTypeName"`
	ModelTypeName  string      `json:"modelTypeName"`
	ServiceGroup   string      `json:"serviceGroup,omitempty"`
	APIVersionDir  string      `json:"apiVersionDir,omitempty"`
	Attributes     []Attribute `json:"attributes"`
	Drop           bool        `json:"drop,omitempty"`
}

// Presence is how Terraform treats an attribute. The four values are spelled
// exactly as the Provider Code Specification spells them, so interop needs no
// mapping table.
type Presence string

const (
	Required         Presence = "required"
	Optional         Presence = "optional"
	Computed         Presence = "computed"
	ComputedOptional Presence = "computed_optional"
)

// IsComputed reports whether the framework schema sets Computed.
func (p Presence) IsComputed() bool {
	return p == Computed || p == ComputedOptional
}

// IsOptional reports whether the framework schema sets Optional.
func (p Presence) IsOptional() bool {
	return p == Optional || p == ComputedOptional
}

// IsRequired reports whether the framework schema sets Required.
func (p Presence) IsRequired() bool { return p == Required }

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
	// Elem is the element type of a list, set or map of scalars.
	Elem *AttrType `json:"elem,omitempty"`
	// Nested is the object shape of a nested kind.
	Nested *Nested `json:"nested,omitempty"`

	// Enum is the value set the *specification* documents, and it is documentation only.
	//
	// Explicitly not a validator input. A generated OneOf built from a scraped enum rejects
	// configurations the API would have accepted, and the practitioner has no way around it --
	// which is the specific harm the README forbids, and which TestUnit_Emit_EnumValuesDoNot-
	// ReachGeneratedCode pins.
	//
	// Its purpose is the opposite: it is the *claim* the enum probe refutes. A documented value
	// the API rejects means the specification is stale, and that fact is only worth anything
	// when the values provably came from the specification rather than from somebody's
	// transcription. The pilot is already a case in point -- its committed description lists
	// five object types and the specification declares six.
	Enum []string `json:"enum,omitempty"`
}

// Nested is the object shape a nested attribute holds.
type Nested struct {
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

// Attribute is one Terraform schema attribute.
type Attribute struct {
	// Name is the tfsdk name, snake_case.
	Name string `json:"name"`
	// GoField is the model struct field.
	GoField string `json:"goField"`

	Type     AttrType `json:"type"`
	Presence Presence `json:"presence"`

	Sensitive bool `json:"sensitive,omitempty"`

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
