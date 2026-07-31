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

import (
	"fmt"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/naming"
)

// FormatVersion is the blueprint format version. It is deliberately unrelated to
// the Provider Code Specification's version, which this format does not track.
//
// Version 4 added ephemerals, behaviour variants, list filter schemas and acceptance
// seeds in one step, so the committed pilot re-serialized once rather than four times.
const FormatVersion = "4"

// Blueprint is one provider.
type Blueprint struct {
	FormatVersion string `json:"formatVersion"`
	// Provider is omitzero because a blueprint is routinely split across files, and a
	// resource-only fragment has no provider to declare. Without it, every part written by
	// ingest, merge or interop import carries an empty provider block -- noise in a committed
	// file, and a fragment quietly claiming a provider it does not describe.
	Provider Provider `json:"provider,omitzero"`

	Resources   []Resource   `json:"resources,omitempty"`
	Actions     []Action     `json:"actions,omitempty"`
	DataSources []DataSource `json:"dataSources,omitempty"`
	Ephemerals  []Ephemeral  `json:"ephemerals,omitempty"`

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
	// ActionRoot is where action packages live. Defaults to internal/services/actions.
	ActionRoot string `json:"actionRoot,omitempty"`
	// EphemeralRoot is where ephemeral packages live. Defaults to
	// internal/services/ephemerals.
	EphemeralRoot  string `json:"ephemeralRoot,omitempty"`
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

	// Sweep overrides the prober's name-field inference for this resource.
	//
	// On the resource rather than the probe plan, because `probe -mode sweep` builds its
	// subject from the blueprint alone -- a sweep is exactly the situation where no plan
	// may be to hand, and the field that finds stranded objects cannot depend on one.
	// Absent means the prober infers the field itself.
	Sweep *SweepNaming `json:"sweep,omitempty"`

	// Identity is the resource identity schema, which makes the generated resource satisfy
	// ResourceWithIdentity. Absent means the resource declares no identity.
	//
	// A facet of the resource rather than a sibling block, because that is what it is: the
	// framework reaches it through a method on the resource, and it is meaningless without
	// one.
	Identity *ResourceIdentity `json:"identity,omitempty"`

	// ConfigValidators are cross-attribute rules, which no per-attribute validator can
	// express: they compare one attribute against another.
	//
	// Rendered into the resource's own ConfigValidators method, so they run once per plan
	// rather than once per attribute -- and the diagnostic names the block rather than a
	// field, which is what a rule about two fields should do.
	ConfigValidators []ConfigValidator `json:"configValidators,omitempty"`

	// Hooks are the hand-written seams this resource opts into.
	Hooks Hooks `json:"hooks,omitzero"`

	// List is the list-resource facet, which backs `terraform query` and feeds
	// `terraform plan -generate-config-out`.
	//
	// A facet rather than a sibling block, and that is not a modelling preference. The
	// framework requires a list resource's type name to equal the managed resource's --
	// that string is the entire linkage between them, there is no import from one package
	// to the other -- and it requires the managed resource to declare an identity, because
	// ListResult.Identity is mandatory. Modelled here, both of those are unrepresentable if
	// violated rather than validated after the fact.
	List *ListFacet `json:"list,omitempty"`

	// AccFixture curates fixture values the generator cannot derive.
	//
	// The generator refuses to synthesise a nested object's members -- they are
	// frequently identifiers of objects that must already exist -- and a required
	// attribute it cannot fill makes the whole fixture refused. Both refusals are
	// right, and this is the declared way past them: a human states the HCL, the
	// generator emits it verbatim, and the provenance is a blueprint diff rather
	// than an edit to a generated file.
	AccFixture *AccFixture `json:"accFixture,omitempty"`

	// Drop excludes the resource from emission. Only a hand-authored override
	// layer may set it, so that a probe run against a tenant which can see
	// nothing cannot quietly delete half a provider.
	Drop bool `json:"drop,omitempty"`
}

// SweepNaming names the wire field a probe run's name prefix travels through.
//
// The prober infers this field when it can (a field literally called "name", or one
// ending in it), but inference has limits a real API exceeds: an API may carry the
// name in a field inference ranks below another candidate, and one may even return
// the value under a different key than it accepted it -- ThousandEyes dashboard
// snapshots take displayName and answer with snapshotName. The override exists for
// exactly those resources, and validation holds it to the same rules inference
// follows.
type SweepNaming struct {
	// NameField is the JSON key the stamped prefix is written into on create. It must
	// be a top-level writable string attribute -- the same bar inference applies.
	NameField string `json:"nameField"`

	// ReadNameField is the key the same value comes back under on a list read, when
	// the API renames it. Empty means NameField round-trips unchanged. The sweeper's
	// prefix pass matches against this key, so getting it wrong is the difference
	// between finding a stranded object and reporting an orphan.
	ReadNameField string `json:"readNameField,omitempty"`
}

// AccFixture is the curated part of a generated acceptance fixture.
type AccFixture struct {
	// DataBlocks are HCL blocks emitted verbatim above the resource block, in
	// every fixture and in every seed embedding. They exist so a hinted value can
	// reference live tenant data -- `data "thousandeyes_agents" "test" {}` under a
	// hint of `data.thousandeyes_agents.test.agents[0].agent_id` -- instead of a
	// literal that goes stale.
	DataBlocks []string `json:"dataBlocks,omitempty"`

	// Values map attributes to curated HCL expressions, emitted exactly as
	// written and never salted: salting exists to de-collide synthesised strings,
	// and a curated expression is not one.
	Values []FixtureHint `json:"values,omitempty"`
}

// FixtureHint is one curated fixture value.
type FixtureHint struct {
	// Attr is the Terraform attribute name the value is for.
	Attr string `json:"attr"`
	// HCL is the right-hand side, verbatim -- a literal, a reference into a
	// declared data block, an object expression.
	HCL string `json:"hcl"`
}

// Hint returns the curated HCL for an attribute, if any.
func (f *AccFixture) Hint(attr string) (string, bool) {
	if f == nil {
		return "", false
	}
	for _, h := range f.Values {
		if h.Attr == attr {
			return h.HCL, true
		}
	}
	return "", false
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

	// AccTest seeds the generated acceptance test. A data source reads an object it does
	// not create, so which resource creates one is judgement declared here, not inferred;
	// absence is a stated refusal to generate the test, never a broken one.
	AccTest *AccSeed `json:"accTest,omitempty"`

	Drop bool `json:"drop,omitempty"`
}

// Ephemeral is one ephemeral resource: a value opened for the duration of a plan or
// apply and never persisted to state.
//
// The natural case is a secret -- a credential's decrypted value handed to another
// provider's configuration -- which is why the kind exists in the framework at all.
// Structurally it is a data source whose result lives outside state: config attributes
// reach the API as call arguments, result attributes are flattened from the response,
// and BlockEphemeral already refuses Default and plan modifiers on both.
type Ephemeral struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	GoPackage      string `json:"goPackage"`
	GoPackageAlias string `json:"goPackageAlias"`
	GoTypeName     string `json:"goTypeName"`
	// ModelTypeName is the struct the ephemeral decodes its configuration into and
	// populates its result from.
	ModelTypeName string `json:"modelTypeName"`

	ServiceGroup  string `json:"serviceGroup,omitempty"`
	APIVersionDir string `json:"apiVersionDir,omitempty"`

	DocRefURL string `json:"docRefUrl,omitempty"`

	Schema Schema `json:"schema"`

	Binding EphemeralBinding `json:"binding"`

	// Timeouts carries only a read deadline that is ever used; Open is a read.
	Timeouts Timeouts `json:"timeouts,omitzero"`

	// AccTest seeds the generated acceptance test, exactly as a data source's does.
	AccTest *AccSeed `json:"accTest,omitempty"`

	Drop bool `json:"drop,omitempty"`
}

// AccSeed links a read-only block's generated acceptance test to the resource that
// creates the object it reads.
//
// The linkage is declared rather than inferred because it is judgement: nothing in a
// schema says which resource's minimal fixture produces an object this block can find.
type AccSeed struct {
	// SeedResourceKey names a Resource in the same blueprint whose minimal fixture
	// creates the object.
	SeedResourceKey string `json:"seedResourceKey"`
	// Args maps this block's config attributes onto the seed resource's attributes,
	// rendered in the fixture as HCL references: id = thousandeyes_tag.test.id.
	Args []SeedArg `json:"args,omitempty"`
}

// SeedArg is one config attribute filled from one seed resource attribute.
type SeedArg struct {
	// Attr is the attribute on this block.
	Attr string `json:"attr"`
	// FromSeedAttr is the attribute on the seed resource it references.
	FromSeedAttr string `json:"fromSeedAttr"`
}

// Action is one Terraform action: an imperative operation with no state.
//
// The third most-used block kind in terraform-provider-microsoft365 -- 43 of them, against
// 4 list resources -- and the simplest, because an action has nothing to reconcile. It reads
// configuration, calls the API once and reports what happened; InvokeRequest carries only a
// config and InvokeResponse returns no value at all.
//
// That absence of state is why an action attribute cannot be Computed or Sensitive, which
// BlockAction already encodes: there is no state for a computed value to live in, and no
// stored value to mark sensitive.
type Action struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	GoPackage      string `json:"goPackage"`
	GoPackageAlias string `json:"goPackageAlias"`
	GoTypeName     string `json:"goTypeName"`
	// ModelTypeName is the struct the action decodes its configuration into. Named for what
	// it is rather than "state", which an action does not have.
	ModelTypeName string `json:"modelTypeName"`

	ServiceGroup  string `json:"serviceGroup,omitempty"`
	APIVersionDir string `json:"apiVersionDir,omitempty"`

	DocRefURL string `json:"docRefUrl,omitempty"`

	Schema Schema `json:"schema"`

	Binding ActionBinding `json:"binding"`

	// Timeouts carries only an invoke deadline that is ever read. Modelled with the shared
	// Timeouts type rather than a bespoke one so Conventions.DefaultTimeouts stays a single
	// shape; CreateSeconds is the field it uses, since an invoke is a write.
	Timeouts Timeouts `json:"timeouts,omitzero"`

	// AccTest configures the generated acceptance test. An action's subject frequently
	// cannot be created by Terraform -- disabling an endpoint agent needs an enrolled
	// agent -- so its identifier arrives from the environment and cleanup is an explicit
	// SDK call. Absence is a stated refusal to generate the test.
	AccTest *ActionAccTest `json:"accTest,omitempty"`

	Drop bool `json:"drop,omitempty"`
}

// ActionAccTest seeds an action's generated acceptance test.
type ActionAccTest struct {
	// EnvArgs fills config attributes from environment variables at test run time. The
	// generated test skips, with the variable named, when one is unset -- an action whose
	// subject cannot be created must not fail the run for a missing fixture.
	EnvArgs []EnvArg `json:"envArgs,omitempty"`
	// Cleanup is the SDK call that reverses the action after the test -- re-enabling what
	// was disabled. Nil means the action is not reversed, and the generated test says so.
	Cleanup *Operation `json:"cleanup,omitempty"`
}

// EnvArg fills one action config attribute from one environment variable.
type EnvArg struct {
	// Attr is the attribute on the action's schema.
	Attr string `json:"attr"`
	// EnvVar names the variable the test reads.
	EnvVar string `json:"envVar"`
}

// ActionBinding is the single SDK call an action makes.
//
// One operation, and no response model: an action returns nothing to Terraform, so whatever
// the SDK hands back is discarded. That is not a simplification -- InvokeResponse has no
// field to put a result in.
type ActionBinding struct {
	Service ServiceRef `json:"service"`

	// Invoke is the call. Required.
	Invoke *Operation `json:"invoke,omitempty"`
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

	// Constraints are the bounds the specification declares on the value.
	//
	// On the type rather than the attribute, because they are properties of the type: a
	// collection's element type carries its own, which is what lets a set of strings get a
	// per-element validator rather than one applied to the set.
	Constraints Constraints `json:"constraints,omitzero"`
}

// Constraints are declared bounds on a value, from which validators are generated.
//
// Every numeric field is a pointer because zero is a meaningful bound: a minimum of 0 and no
// minimum at all are different claims, and conflating them would silently drop the bound the
// specification actually made.
//
// These are the *declared* set, like AttrType.AllowedValues and for the same reason -- a
// validator built from them errs toward permitting what the API documents. Unlike allowed
// values there is no observed counterpart: the prober has no protocol for discovering a length
// limit or a numeric range, so there is nothing to suppress a constraint validator on.
type Constraints struct {
	// Pattern is a regular expression the value must match, in the specification's own
	// syntax. Emitted through regexp.MustCompile, so a pattern Go's regexp cannot parse is a
	// panic at provider start -- which is why ingest refuses one rather than passing it on.
	Pattern string `json:"pattern,omitempty"`

	// MinLength and MaxLength bound a string's length.
	MinLength *int64 `json:"minLength,omitempty"`
	MaxLength *int64 `json:"maxLength,omitempty"`

	// MinItems and MaxItems bound a collection's size.
	MinItems *int64 `json:"minItems,omitempty"`
	MaxItems *int64 `json:"maxItems,omitempty"`

	// Minimum and Maximum bound a number. Held as float64 whatever the attribute's kind,
	// because that is how JSON Schema expresses them; render narrows to the kind's own
	// validator package.
	Minimum *float64 `json:"minimum,omitempty"`
	Maximum *float64 `json:"maximum,omitempty"`
}

// IsZero reports whether the specification declared no bounds at all.
func (c Constraints) IsZero() bool { return c == Constraints{} }

// ConfigValidatorKind is a cross-attribute rule the framework provides.
type ConfigValidatorKind string

const (
	// ConfigConflicting refuses a configuration setting more than one of the attributes.
	ConfigConflicting ConfigValidatorKind = "conflicting"
	// ConfigAtLeastOneOf requires at least one to be set.
	ConfigAtLeastOneOf ConfigValidatorKind = "atLeastOneOf"
	// ConfigExactlyOneOf requires exactly one.
	ConfigExactlyOneOf ConfigValidatorKind = "exactlyOneOf"
	// ConfigRequiredTogether requires all or none.
	ConfigRequiredTogether ConfigValidatorKind = "requiredTogether"
)

// ConfigValidator is one cross-attribute rule.
//
// Deliberately a fixed set of kinds rather than free-form code. The four the framework
// provides cover what a specification can actually state about two fields, and a rule needing
// more than that is judgement rather than schema -- which is what Hooks.ModifyPlan is for.
type ConfigValidator struct {
	Kind ConfigValidatorKind `json:"kind"`

	// Attributes are the top-level attribute names the rule relates, rendered as
	// path.MatchRoot expressions. At least two: a cross-attribute rule over one attribute is
	// a per-attribute validator written in the wrong place.
	Attributes []string `json:"attributes"`
}

// Hooks are the hand-written files a resource opts into.
//
// Each is scaffolded once and then owned by whoever edits it -- `emit` never writes over one
// that exists. The flag lives in the blueprint rather than being inferred from the file being
// present, because generated code has to agree with it: an interface assertion or a call to
// the hook is emitted only when the flag is set, so a scaffold deleted by hand takes the
// generated reference with it instead of leaving a package that does not compile.
type Hooks struct {
	// ModifyPlan scaffolds modify_plan.go and makes the resource assert
	// ResourceWithModifyPlan. The reference provider carries one in 93 of 167 packages,
	// frequently as a deliberate stub.
	ModifyPlan bool `json:"modifyPlan,omitempty"`

	// ReadBackPredicate scaffolds predicate.go and makes the generated read-after-write ask
	// it whether the write has landed, rather than only whether the object is readable.
	//
	// Which field an API touches on write is API-specific and sometimes resource-specific, so
	// it cannot be generated. 28 of the reference provider's 167 resources carry one.
	ReadBackPredicate bool `json:"readBackPredicate,omitempty"`
	// StateUpgrade scaffolds UpgradeState, which a resource needs once its schema version is
	// past zero. The migration itself is judgement no specification carries: only whoever
	// changed the schema knows what an old state's fields meant.
	StateUpgrade bool `json:"stateUpgrade,omitempty"`
}

// IsZero reports whether the resource opts into no hand-written seams.
func (h Hooks) IsZero() bool { return h == Hooks{} }

// ListFacet makes a resource list-supporting.
//
// Its purpose is discovery and import rather than data retrieval: `terraform query` reads it
// and `terraform plan -generate-config-out` turns the answer into configuration. That is why
// the reference provider populates only Identity and DisplayName on each result and never
// Resource -- the workflow generates configuration from the identity, it does not read
// objects out of the query.
type ListFacet struct {
	// GoTypeName is the generated list resource struct, e.g. "TagListResource".
	GoTypeName string `json:"goTypeName"`

	Service ServiceRef `json:"service"`

	// Read is the SDK call returning many objects. A list resource needs nothing more of
	// the API than this: if there is a collection GET, that is the whole requirement.
	Read *Operation `json:"read,omitempty"`

	// Response is the SDK type the read returns and how its fields are reached.
	Response ResponseModel `json:"response"`

	// CollectionField is the field on the response holding the elements, e.g. "Tags". Empty
	// when the response is itself the slice.
	CollectionField string `json:"collectionField,omitempty"`
	// ElementType is the Go type of one element, e.g. "tags.Tag".
	//
	// Not consumed by the emitter -- the generated loop infers its variable's type from the
	// slice -- and recorded for the same reason as Operation.HTTPMethod: a reader of the
	// blueprint should be able to see what is being iterated without going to the SDK, and
	// reconstructing it later would be guesswork.
	ElementType string `json:"elementType"`

	// IdentityFrom maps each element onto the resource's identity model. The field names
	// must be the identity's own, which is why validation cross-checks them.
	IdentityFrom []ListIdentityMapping `json:"identityFrom"`

	// DisplayNameFrom is the element field shown in `terraform query` output. Optional: a
	// result with no display name is legal, just less useful.
	DisplayNameFrom string `json:"displayNameFrom,omitempty"`
	// DisplayNameIsPointer records whether that field needs dereferencing, which generated
	// SDKs make the common case for a string.
	DisplayNameIsPointer bool `json:"displayNameIsPointer,omitempty"`

	// Schema is the list resource's filter config schema. Optional: a facet with no
	// filters lists everything, and its config schema is empty. Validated against
	// BlockList, whose refusal of Computed is the structural reason the schema is
	// filter-only -- there is nowhere to put a result. Filter values reach the read call
	// as arguments, so each of Read's ArgConfigField args must name an attribute here.
	Schema *Schema `json:"schema,omitempty"`
}

// ListIdentityMapping fills one identity field from one element field.
type ListIdentityMapping struct {
	// GoField is the field on the identity model.
	GoField string `json:"goField"`
	// FromSDKField is the field on the SDK element.
	FromSDKField string `json:"fromSdkField"`
	// IsPointer records whether the SDK field needs dereferencing before assignment.
	IsPointer bool `json:"isPointer,omitempty"`
}

// ResourceIdentity is a resource's identity schema.
//
// Terraform uses it to address an object independently of configuration, which is what
// makes import by identity and list resources possible. In terraform-provider-microsoft365
// all 154 uses are the same shape -- one required-for-import "id" -- but it is modelled as
// a list because the framework permits a composite identity, and an API keyed by a pair
// would otherwise need the emitter changed rather than a blueprint written.
type ResourceIdentity struct {
	Attributes []IdentityAttribute `json:"attributes"`

	// GoTypeName is the generated struct both the resource and its list facet decode the
	// identity into, e.g. "TagResourceIdentity". One type shared by both, so the two cannot
	// disagree about the shape.
	GoTypeName string `json:"goTypeName"`
}

// IdentityAttribute is one member of an identity schema.
//
// Deliberately not blueprint.Attribute. An identity attribute has no presence, no
// validators, no plan modifiers and no default -- the framework's identityschema package
// gives it RequiredForImport and OptionalForImport instead, and reusing Attribute would
// offer a dozen fields that have nowhere to go.
type IdentityAttribute struct {
	// Name is the identity attribute name, almost always "id".
	Name string `json:"name"`
	// GoField is the field on the generated identity struct.
	GoField string `json:"goField"`
	// Kind is the scalar type. The framework's identityschema has no nested or collection
	// attribute beyond a list of scalars, so a nested kind here is refused.
	Kind TypeKind `json:"kind"`

	// RequiredForImport and OptionalForImport are the framework's own two flags. Exactly
	// one must be set: an attribute that is neither can never be supplied, and one that is
	// both is a contradiction the framework rejects at runtime.
	RequiredForImport bool `json:"requiredForImport,omitempty"`
	OptionalForImport bool `json:"optionalForImport,omitempty"`

	Description string `json:"description,omitempty"`

	// FromAttribute names the resource attribute this mirrors, so generated code can copy
	// the value out of the resource model rather than fetching it again.
	FromAttribute string `json:"fromAttribute"`
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
	// Normalises describes a transformation the API applied to a value it accepted, e.g.
	// "lowercases it". Empty when the value came back as it was sent.
	//
	// Structural for exactly the reason AcceptedValues below is, and the reason is worth
	// restating because merge previously recorded this as prose in the description only. A
	// generated acceptance test cannot read prose, and an equality assertion on an attribute
	// the API rewrites fails by design -- so the generator has to be able to see that it does.
	Normalises string `json:"normalises,omitempty"`

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

	// Conditional is the observed behaviour that holds only under a precondition.
	//
	// The unconditional fields above answer "what does the API do with this field"; a variant
	// answers the same question for one branch of the API's own dispatch -- matchType is
	// returned on read when type is "dynamic" and discarded when it is "static", and writing
	// either answer into ReturnedOnRead above would make it a claim about every tag. That is
	// exactly how the pilot came to suppress an attribute's read-back and import verification
	// for every tag on the strength of an observation about static ones.
	//
	// Semantics, which merge enforces and emission must honour:
	//   - A base field above holds unconditionally. A variant's non-zero fields override it
	//     only while every one of the variant's conditions holds; conditions are conjunctive.
	//   - When variants disagree about a dimension, the base field for that dimension stays
	//     nil -- "unknown unconditionally" -- and anything consuming the base must not treat
	//     the absence as evidence.
	//   - Variants are sorted by their canonical WhenKey and unique under it, so the committed
	//     document is byte-stable. A variant's Behaviour never nests Conditional; validation
	//     refuses the recursion.
	Conditional []BehaviourVariant `json:"conditional,omitempty"`
}

// Condition is one precondition on a behaviour variant: a wire field held this value.
//
// JSONPath speaks the schema's wire vocabulary -- the same dotted paths merge uses to
// address attributes -- not Terraform attribute names, because the precondition was
// observed on the wire and the prober never learns Terraform naming. Equals is a string
// because a gate has to be enumerable to be crossed, and the probe plan declares gate
// values as strings.
type Condition struct {
	// JSONPath is the gate field.
	JSONPath string `json:"jsonPath"`
	// Equals is the value it held.
	Equals string `json:"equals"`
}

// String renders a condition for a report and for an attribute's description.
func (c Condition) String() string { return fmt.Sprintf("%s is %q", c.JSONPath, c.Equals) }

// BehaviourVariant is Behaviour under a precondition.
type BehaviourVariant struct {
	// When is the precondition, conjunctive and never empty.
	When []Condition `json:"when"`
	// Behaviour is what was observed while the precondition held. Only its non-zero
	// fields say anything; it never nests Conditional.
	Behaviour Behaviour `json:"behaviour"`
}

// WhenKey renders the precondition as one canonical string.
//
// This is the identity of a variant: two variants with equal keys are the same branch, and
// merge folds facts into the existing one rather than appending a duplicate. Sorted by path
// so the key does not depend on the order conditions were appended in; validation refuses a
// repeated path, so sorting by path alone is total.
func (v BehaviourVariant) WhenKey() string {
	parts := make([]string, 0, len(v.When))
	for _, c := range v.When {
		parts = append(parts, c.JSONPath+"="+c.Equals)
	}
	sort.Strings(parts)

	return strings.Join(parts, ";")
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
		b.Normalises == "" &&
		len(b.AcceptedValues) == 0 &&
		len(b.RejectedValues) == 0 &&
		b.ValuesClosed == nil &&
		len(b.Conditional) == 0
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
