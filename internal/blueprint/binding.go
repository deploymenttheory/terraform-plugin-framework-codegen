package blueprint

// This file holds the part of the IR the Provider Code Specification has no
// counterpart for at all: which SDK symbols a resource calls, with what
// arguments, and how the results move between the SDK's types and Terraform's.
//
// The design goal is that a call is *data*, never a dialect the emitter branches
// on. Adding an SDK whose methods look different should be a blueprint change,
// not an emitter change. So a call records its receiver, its method name, an
// ordered argument list and its return arity, and the emitter renders whatever it
// is told.

// ResourceBinding is the set of SDK calls one resource makes.
type ResourceBinding struct {
	Service ServiceRef `json:"service"`

	Create *Operation `json:"create,omitempty"`
	Read   *Operation `json:"read,omitempty"`
	Update *Operation `json:"update,omitempty"`
	Delete *Operation `json:"delete,omitempty"`

	ID   IDBinding  `json:"id"`
	Body BodyModels `json:"body"`
}

// DataSourceBinding is the SDK calls one data source makes: a read, and nothing else.
//
// This is a separate type from ResourceBinding rather than a reuse of it. A data source
// has no create, no update, no delete and no request body, and modelling it as a
// ResourceBinding with most of the fields refused would put the refusal in a validation
// rule somebody has to remember to keep. Here those fields simply do not exist, which is
// the same move BlockKind makes for per-kind attribute fields.
type DataSourceBinding struct {
	Service ServiceRef `json:"service"`

	// Read is the only operation. It is a pointer for symmetry with ResourceBinding's
	// operations and so that "not yet authored" is distinguishable from an empty call,
	// which is what an imported draft needs; Validate requires it.
	Read *Operation `json:"read,omitempty"`

	Response ResponseModel `json:"response"`
}

// EphemeralBinding wires an ephemeral resource to the SDK.
//
// Open is the one operation this toolkit renders: it runs when Terraform needs the value,
// and its response populates the result. Renew and Close are modelled now so a blueprint
// written today needs no reshaping when they are rendered -- the same reasoning as
// DataSourceBinding's pointer operations -- but render refuses them until a pilot needs
// one, because shipping lifecycle codegen with no live acceptance run behind it would
// claim support nothing has proven.
type EphemeralBinding struct {
	Service ServiceRef `json:"service"`

	// Open is required; Validate enforces it.
	Open *Operation `json:"open,omitempty"`
	// Renew re-obtains a value that expires. Accepted, not yet rendered.
	Renew *Operation `json:"renew,omitempty"`
	// Close releases the value at the end of the run. Accepted, not yet rendered.
	Close *Operation `json:"close,omitempty"`

	Response ResponseModel `json:"response"`
}

// ResponseModel is the SDK type a data source reads back, and how its fields are reached.
//
// The resource equivalent is BodyModels, which also carries a request type and a
// constructor for it. A data source sends no body, so it carries neither.
type ResponseModel struct {
	// Type is the Go type read back, e.g. "tags.Tag", written as it appears at the use
	// site including any package qualifier.
	Type string `json:"type"`

	AccessStyle AccessStyle `json:"accessStyle"`
}

// ServiceRef locates the SDK symbols for a resource.
type ServiceRef struct {
	// ImportPath is the SDK package, e.g.
	// ".../thousandeyes/thousandeyes_api/tags".
	ImportPath string `json:"importPath"`
	// Alias is the import alias, needed because SDK package names collide with
	// provider identifiers often enough to be the default rather than the
	// exception.
	Alias string `json:"alias,omitempty"`
	// TypeName is the service type, e.g. "Tags".
	TypeName string `json:"typeName"`
	// Accessor is the expression reaching the service from the resource
	// receiver, e.g. "r.client.Tags". This is how a root client that exposes
	// services as fields is bound without the emitter knowing that shape.
	Accessor string `json:"accessor"`
}

// CallStyle is the shape of a generated call.
type CallStyle string

const (
	// CallStyleMethod is a single method call on a service value.
	CallStyleMethod CallStyle = "method"
	// CallStyleFluent is a request-builder chain. Reserved; not yet emitted.
	CallStyleFluent CallStyle = "fluent"
)

// ReturnArity is what an SDK call returns.
//
// This exists because it determines the arity of every error return in the
// generated function body, which is the direct analogue of the SDK generator
// precomputing a nil prefix. Getting it wrong produces code that does not
// compile, so it is recorded rather than inferred.
type ReturnArity string

const (
	// ReturnResultTransportError is (*T, *resty.Response, error), the common
	// case in the resty dialect.
	ReturnResultTransportError ReturnArity = "resultTransportError"
	// ReturnTransportError is (*resty.Response, error), with no result. The
	// ThousandEyes delete operation has this shape, and assuming otherwise is
	// the most likely single cause of a non-compiling generated delete.
	ReturnTransportError ReturnArity = "transportError"
	// ReturnResultError is (T, error).
	ReturnResultError ReturnArity = "resultError"
	// ReturnError is error alone.
	ReturnError ReturnArity = "error"
)

// HasResult reports whether the call returns a result value to assign.
func (r ReturnArity) HasResult() bool {
	return r == ReturnResultTransportError || r == ReturnResultError
}

// HasTransport reports whether the call returns a transport response, which the
// generated error handling passes to the provider's error mapper.
func (r ReturnArity) HasTransport() bool {
	return r == ReturnResultTransportError || r == ReturnTransportError
}

// ChainSegment is one hop in a fluent request-builder chain.
//
// The emitter renders segments generically -- accessor.Seg1(args).Seg2(args)...
// -- without knowing which generator produced the SDK; the chain is the call,
// as data, which is the same doctrine the flat method style follows. A
// mid-chain identifier is an ordinary stateField or planField argument on its
// segment; the trailing request-configuration a builder verb takes is a
// literal nil.
type ChainSegment struct {
	Method string     `json:"method"`
	Args   []Argument `json:"args,omitempty"`
}

// Operation is one SDK call.
type Operation struct {
	Style CallStyle `json:"style"`
	// Method is the SDK method name. An SDK that encodes an API version as a
	// method suffix, as go-sdk-jamfpro-v2 does with ListV1, needs nothing
	// special here: the version is simply part of the name.
	Method string `json:"method"`

	// Chain is the request-builder chain for CallStyleFluent. The final
	// segment is the verb; Method and Args must be empty when Chain is set,
	// because a fluent call's verb is its last segment.
	Chain []ChainSegment `json:"chain,omitempty"`

	Args   []Argument  `json:"args,omitempty"`
	Return ReturnArity `json:"return"`

	// ResultType is the Go type of the result, written as it appears at the use
	// site including any package qualifier.
	ResultType string `json:"resultType,omitempty"`

	// HTTPMethod, PathTemplate and SuccessCodes do not build the call: the SDK
	// owns the wire. They are carried because generated mocks, fixtures and doc
	// comments need them, and reconstructing them from a method name at emit time
	// would be guesswork.
	HTTPMethod   string `json:"httpMethod,omitempty"`
	PathTemplate string `json:"pathTemplate,omitempty"`
	SuccessCodes []int  `json:"successCodes,omitempty"`
}

// ArgKind is the sort of value passed to an SDK call. The emitter renders each
// kind one way, so a new argument shape is a new kind rather than a special case
// somewhere in a template.
type ArgKind string

const (
	// ArgContext is the request context.
	ArgContext ArgKind = "ctx"
	// ArgStateField reads a model field from prior state, as a read or delete
	// does for the resource ID.
	ArgStateField ArgKind = "stateField"
	// ArgPlanField reads a model field from the plan.
	ArgPlanField ArgKind = "planField"
	// ArgConfigField reads a model field from configuration.
	//
	// Used by a data source and by an action, which are the two kinds with nowhere else to
	// read an argument from: neither has prior state and neither has a plan. Both decode
	// into a variable named data, so one kind serves both rather than each having its own.
	ArgConfigField ArgKind = "configField"
	// ArgBody passes the constructed request body.
	ArgBody ArgKind = "body"
	// ArgLiteral passes a verbatim Go expression.
	ArgLiteral ArgKind = "literal"
)

// Argument is one value passed to an SDK call.
type Argument struct {
	Kind ArgKind `json:"kind"`
	// Field names the model field for the state and plan kinds.
	Field string `json:"field,omitempty"`
	// Expr is a verbatim Go expression, required for ArgLiteral and otherwise an
	// override the emitter would have derived from Field.
	Expr string `json:"expr,omitempty"`
	// Imports are the packages Expr references -- a request option like
	// client.WithQueryParam("expand", "filters") names the SDK's client package,
	// which crud.go otherwise never imports.
	Imports []Import `json:"imports,omitempty"`
}

// IDBinding says where a resource's identifier comes from after create, and what
// Terraform attribute holds it.
type IDBinding struct {
	// Attribute is the Terraform attribute, almost always "id".
	Attribute string `json:"attribute"`
	// GoField is the model field for that attribute.
	GoField string `json:"goField"`
	// FromCreate is the accessor on the create result, e.g. "created.ID".
	FromCreate string `json:"fromCreate"`
	// FromCreateIsPointer records whether that accessor yields a pointer, which
	// decides whether the generated code must dereference it and nil-check first.
	FromCreateIsPointer bool `json:"fromCreateIsPointer,omitempty"`
}

// BodyModels describes the SDK types a resource's request and response use, and
// how their fields are reached.
//
// The request and response types are separate fields and not one type because
// generated SDKs routinely split them. The ThousandEyes tag endpoint takes a
// TagInfo and returns a Tag: structurally near-identical, distinct Go types, and
// modelling them as one produces a construct function that does not compile.
type BodyModels struct {
	// RequestType is the Go type of the request body, e.g. "tags.TagInfo".
	RequestType string `json:"requestType"`
	// ResponseType is the Go type read back, e.g. "tags.Tag".
	ResponseType string `json:"responseType"`

	// ConstructorExpr yields an empty request body. A struct-field SDK uses a
	// composite literal; a setter-based SDK uses its constructor function.
	ConstructorExpr string `json:"constructorExpr"`

	// UpdateRequestType is the Go type of the update request body, for an SDK that
	// splits it from create's -- ThousandEyes' agent-to-server test creates with
	// AgentToServerTestRequest and updates with UpdateAgentToServerTestRequest.
	// Empty means update sends RequestType, which is the overwhelmingly common
	// shape. The split types are near-identical clones sharing field names, so the
	// emitter reuses one assignment list against both; sdkbind proves every field
	// exists on both types, which is what makes the reuse safe rather than hopeful.
	UpdateRequestType string `json:"updateRequestType,omitempty"`
	// UpdateConstructorExpr yields an empty update body. Required exactly when
	// UpdateRequestType is set.
	UpdateConstructorExpr string `json:"updateConstructorExpr,omitempty"`

	AccessStyle AccessStyle `json:"accessStyle"`
}

// SplitsUpdateBody reports whether update sends a different Go type than create.
func (b BodyModels) SplitsUpdateBody() bool { return b.UpdateRequestType != "" }

// AccessStyle is how a field on an SDK model is read and written.
type AccessStyle string

const (
	// AccessStructField is `body.Color = &v` and `remote.Color`, the resty
	// dialect.
	AccessStructField AccessStyle = "structField"
	// AccessMethod is `body.SetColor(&v)` and `remote.GetColor()`, the Kiota
	// dialect. Reserved; not yet emitted.
	AccessMethod AccessStyle = "method"
)

// WireBinding is how one attribute crosses the boundary between Terraform's
// types and the SDK's.
type WireBinding struct {
	// JSONPath is the API's own name for the field. It is the join key for probe
	// facts: the prober observes JSON and must never need to know Terraform
	// attribute naming.
	JSONPath string `json:"jsonPath,omitempty"`

	// SDKField is the field name on the SDK model.
	SDKField string `json:"sdkField,omitempty"`
	// SDKGoType is the Go type of that field, e.g. "*string", "[]string",
	// "tags.AccessType". The conversion is chosen from this plus the attribute's
	// kind, so it has to be recorded exactly as the SDK declares it.
	SDKGoType string `json:"sdkGoType,omitempty"`

	// Expand converts Terraform to SDK; Flatten converts SDK to Terraform.
	Expand *ConvertCall `json:"expand,omitempty"`
	// UpdateExpand overrides Expand for the update body, for an SDK whose update
	// type declares a field differently from create's -- an endpoint label's Name
	// is string on LabelRequest and *string on Label, and one assignment list
	// cannot fill both. Meaningful only with a split update body.
	UpdateExpand *ConvertCall `json:"updateExpand,omitempty"`
	Flatten      *ConvertCall `json:"flatten,omitempty"`

	// SkipExpand excludes an attribute from the request body. A computed
	// timestamp is read but never sent.
	SkipExpand bool `json:"skipExpand,omitempty"`
	// SkipFlatten excludes an attribute from state mapping. A write-only secret
	// must not be flattened, because the API never returns it and flattening
	// would blank state on every read.
	SkipFlatten bool `json:"skipFlatten,omitempty"`
}

// ConvertCall is a resolved call into the target provider's convert package.
type ConvertCall struct {
	// Func is the qualified helper, e.g. "convert.PtrStringToFramework".
	Func string `json:"func"`
	// TypeArgs instantiate a generic helper.
	TypeArgs []string `json:"typeArgs,omitempty"`
	// NeedsCtx prepends a context argument.
	NeedsCtx bool `json:"needsCtx,omitempty"`
	// ReturnsError makes the generated line an assignment plus an error check
	// rather than a bare assignment.
	ReturnsError bool `json:"returnsError,omitempty"`

	// Deref wraps the call in convert.Deref, for an SDK field declared as a value
	// where the helper produces a pointer. The live case: a suppression window's
	// Repeat is a value-typed struct the API requires, and the expand helper for a
	// single nested attribute returns *Repeat.
	Deref bool `json:"deref,omitempty"`
	// TakesAddress passes the argument by address, the flatten-side mirror of
	// Deref: the response holds a value and the helper wants a pointer.
	TakesAddress bool `json:"takesAddress,omitempty"`

	// Cast wraps the finished call in a Go conversion, for an SDK field whose type
	// is a named slice or named scalar of what the helper produces -- a dashboard
	// filter's Context is ApiContextFilters, a named []ApiDataSourceFilters.
	Cast string `json:"cast,omitempty"`

	// ExtraArgs are verbatim expressions appended after the converted value,
	// for a helper that needs a companion function -- the enum parse function a
	// generated SDK pairs with each enum type.
	ExtraArgs []string `json:"extraArgs,omitempty"`

	Imports []Import `json:"imports,omitempty"`
}
