// Package sdkbind derives call bindings from the intermediate
// representation, verifies them against the generated SDK with go/types,
// and prunes what the SDK cannot carry.
//
// A binding names SDK symbols as strings — a call chain, an accessor, a
// model type — and nothing about a string stops it naming something that
// does not exist. Left unchecked, the mistake surfaces as a pile of
// identical compile errors in generated provider code, pointing at the
// generated line rather than at the entity that is wrong. This package
// reports it against the intermediate representation instead, using real
// type information from the generated SDK.
//
// The binding model is dialect-neutral data: every value a template will
// consume is a finished Go expression, name or boolean, rendered here by
// the per-backend binders. Nothing downstream branches on which backend
// generated the SDK — that decision is spent inside this package.
//
// The flow is bind, prune, verify. A binder drafts bindings from the
// intermediate representation alone; Prune resolves every draft against
// the real SDK, repairing spellings where the SDK's truth admits exactly
// one answer and deleting, with a recorded reason, what the SDK cannot
// express; Verify is the pure gate that type-checks a resolved set and
// names the entity and attribute at fault. Prune never invents and never
// widens: every repair is read off the loaded SDK, never guessed.
package sdkbind

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
)

// SDKInfo locates the generated SDK inside the provider module. It is
// binder input, not binding output: the finished expressions carry its
// spellings, so nothing downstream needs it to branch.
type SDKInfo struct {
	// ImportPath is the SDK's root package import path — the package the
	// client type lives in.
	ImportPath string `json:"import_path"`
	// ModelsImportPath is where model types live: a kiota SDK keeps them
	// in a models subpackage, an openapi-generator SDK keeps everything
	// flat in the root package.
	ModelsImportPath string `json:"models_import_path"`
	// ClientTypeName is the client struct's type name, e.g. "APIClient".
	ClientTypeName string `json:"client_type_name"`
}

// InfoFor computes the SDKInfo for the configured backend and the import
// path the provider module gives the generated SDK.
func InfoFor(cfg *config.Config, importPath string) (SDKInfo, error) {
	info := SDKInfo{ImportPath: importPath, ClientTypeName: cfg.SDK.ClientTypeName}
	switch cfg.SDK.Backend {
	case config.BackendKiota:
		info.ModelsImportPath = importPath + "/models"
	case config.BackendOpenAPIGenerator:
		info.ModelsImportPath = importPath
	default:
		return SDKInfo{}, fmt.Errorf("sdk.backend %q is not a supported backend (%s | %s)",
			cfg.SDK.Backend, config.BackendKiota, config.BackendOpenAPIGenerator)
	}
	return info, nil
}

// Binder derives bindings for one backend's generated SDK.
type Binder interface {
	// Name is the config value that selects this binder's backend.
	Name() string
	// Bind drafts bindings for every entity in the model. A draft is a
	// best derivation from the intermediate representation alone; Prune
	// resolves it against the real SDK.
	Bind(m *ir.Model, info SDKInfo) (*Bindings, error)
}

// For returns the binder cfg selects, or an error naming the supported set.
func For(cfg *config.Config) (Binder, error) {
	switch cfg.SDK.Backend {
	case config.BackendKiota:
		return kiotaBinder{}, nil
	case config.BackendOpenAPIGenerator:
		return openAPIGeneratorBinder{}, nil
	default:
		return nil, fmt.Errorf("sdk.backend %q is not a supported backend (%s | %s)",
			cfg.SDK.Backend, config.BackendKiota, config.BackendOpenAPIGenerator)
	}
}

// Bindings is the complete binding set for one provider, keyed by entity
// key — the same keys the intermediate representation sorts by.
type Bindings struct {
	SDK           SDKInfo                         `json:"sdk"`
	Resources     map[string]*ResourceBinding     `json:"resources,omitempty"`
	Datasources   map[string]*DatasourceBinding   `json:"datasources,omitempty"`
	ListResources map[string]*ListResourceBinding `json:"list_resources,omitempty"`
	Actions       map[string]*ActionBinding       `json:"actions,omitempty"`
	// Removed records everything Prune deleted, with the SDK's reason.
	Removed []Removal `json:"removed,omitempty"`
}

// ResourceBinding carries the calls and field accesses one resource's
// generated code interpolates.
type ResourceBinding struct {
	Key    string `json:"key"`
	Create *Call  `json:"create,omitempty"`
	Read   *Call  `json:"read,omitempty"`
	Update *Call  `json:"update,omitempty"`
	Delete *Call  `json:"delete,omitempty"`
	// ReadModel is the finished type expression fields are read from,
	// e.g. "models.Tagable" or "sdk.Tag".
	ReadModel string `json:"read_model,omitempty"`
	// WriteModel is the concrete type the request body is built as.
	WriteModel string `json:"write_model,omitempty"`
	// WriteConstructor is the finished expression that yields a new,
	// settable body value, e.g. "models.NewTag()".
	WriteConstructor string `json:"write_constructor,omitempty"`
	// Fields mirrors the schema attribute order.
	Fields []FieldBinding `json:"fields,omitempty"`
}

// DatasourceBinding carries a datasource's read (and, for a companion
// datasource, list) call plus the field accesses of one element.
type DatasourceBinding struct {
	Key  string `json:"key"`
	Read *Call  `json:"read,omitempty"`
	List *Call  `json:"list,omitempty"`
	// ReadModel is the by-key read's payload type, empty for a companion
	// that only lists.
	ReadModel string `json:"read_model,omitempty"`
	// ElementType is one list element's finished type expression.
	ElementType string `json:"element_type,omitempty"`
	// CollectionAccess is the rendered access from the list call's result
	// to its element slice: a method call like "GetValue()" or "GetTags()",
	// a field name like "Items", or empty when the result is the slice
	// itself.
	CollectionAccess string `json:"collection_access,omitempty"`
	// EnvelopeKey is the observed wire key the list response wraps its item
	// array under (the IR's ListEnvelopeKey), empty for a bare array. Prune
	// reads it to pick the wrapper's getter or field by name rather than
	// guessing among several slices.
	EnvelopeKey string `json:"envelope_key,omitempty"`
	// Fields are the element's field accesses, read direction only.
	Fields []FieldBinding `json:"fields,omitempty"`
}

// ListResourceBinding carries a list-only entity's call and element.
type ListResourceBinding struct {
	Key              string `json:"key"`
	List             *Call  `json:"list,omitempty"`
	ElementType      string `json:"element_type,omitempty"`
	CollectionAccess string `json:"collection_access,omitempty"`
	// EnvelopeKey is the observed list-envelope wire key; see
	// DatasourceBinding.EnvelopeKey.
	EnvelopeKey string         `json:"envelope_key,omitempty"`
	Fields      []FieldBinding `json:"fields,omitempty"`
}

// ActionBinding carries an invocation's call and its argument accesses.
type ActionBinding struct {
	Key              string `json:"key"`
	Invoke           *Call  `json:"invoke,omitempty"`
	WriteModel       string `json:"write_model,omitempty"`
	WriteConstructor string `json:"write_constructor,omitempty"`
	// Fields are the request tree's accesses, write direction only.
	Fields []FieldBinding `json:"fields,omitempty"`
}

// Call is one finished SDK invocation.
//
// The expression contract: Expr references exactly the names "client" (the
// SDK client value), "ctx" (the context), "body" (the value
// WriteConstructor yields, where a request body travels), and one local
// per entry of Params, which the emitter declares with the given Go type.
type Call struct {
	// Expr is the finished Go call expression, e.g.
	// "client.Tags().ByTagId(tagId).Get(ctx, nil)" or
	// "client.TagsAPI.GetTag(ctx, tagId).Execute()".
	Expr string `json:"expr"`
	// Segments is the same call as data — one hop per field selection or
	// method call — kept so verification walks types instead of parsing
	// the rendered string. Expr is always renderExpr(Segments).
	Segments []Segment `json:"segments"`
	// Imports are the packages the expression and its types need.
	Imports []string `json:"imports,omitempty"`
	// Params are the locals the expression references beyond the fixed
	// names, in order of first use.
	Params []CallParam `json:"params,omitempty"`
	// ResponseType is the finished Go type expression of the success
	// payload, empty when the call yields none.
	ResponseType string `json:"response_type,omitempty"`
	// RequestType is the finished type expression the body travels as,
	// empty when the call takes none.
	RequestType string `json:"request_type,omitempty"`
	// Results are the call's result types in order, error included, so
	// the emitter renders the right assignment shape.
	Results []string `json:"results,omitempty"`
}

// Segment is one hop of a call: a field selection or a method call.
type Segment struct {
	Name string `json:"name"`
	// Call distinguishes a method call from a field selection.
	Call bool `json:"call,omitempty"`
	// Args are finished argument expressions, in order.
	Args []string `json:"args,omitempty"`
}

// CallParam is one local a Call's expression references.
type CallParam struct {
	// Local is the Go identifier the expression uses.
	Local string `json:"local"`
	// GoType is the finished type the emitter declares it with.
	GoType string `json:"go_type"`
	// Wire is the path parameter's wire name, for tracing.
	Wire string `json:"wire"`
}

// renderExpr renders a segment list as the finished expression rooted at
// the client. It is the single spelling site: Expr never disagrees with
// Segments because it is always computed from them.
func renderExpr(segs []Segment) string {
	var b strings.Builder
	b.WriteString("client")
	for _, s := range segs {
		b.WriteString(".")
		b.WriteString(s.Name)
		if s.Call {
			b.WriteString("(")
			b.WriteString(strings.Join(s.Args, ", "))
			b.WriteString(")")
		}
	}
	return b.String()
}

// rerender recomputes Expr after a repair changed a segment.
func (c *Call) rerender() { c.Expr = renderExpr(c.Segments) }

// FieldBinding joins one attribute to its SDK access.
type FieldBinding struct {
	// Attr is the terraform attribute name; Wire the property the API
	// speaks.
	Attr string `json:"attr"`
	Wire string `json:"wire"`
	// Kind and ElemKind mirror the attribute's terraform kinds, so
	// pruning and the emitter know the framework side without
	// re-deriving it.
	Kind     ir.TypeKind `json:"kind,omitempty"`
	ElemKind ir.TypeKind `json:"elem_kind,omitempty"`
	// Access is the rendered accessor and conversion set.
	Access FieldAccess `json:"access"`
	// NestedModel is the SDK type one nested object is read as;
	// NestedWriteModel and NestedConstructor are its write-side twin.
	// All empty for a scalar.
	NestedModel       string `json:"nested_model,omitempty"`
	NestedWriteModel  string `json:"nested_write_model,omitempty"`
	NestedConstructor string `json:"nested_constructor,omitempty"`
	// Nested are the child field bindings of an object attribute or a
	// list of objects.
	Nested []FieldBinding `json:"nested,omitempty"`
}

// FieldAccess is how generated code reads and writes one field. Every
// entry is a finished name: accessors are method names on the read and
// write models, converts are convert-function names bridging the SDK's
// carried type and the framework value.
type FieldAccess struct {
	// Get is the read accessor's method name, e.g. "GetName"; empty when
	// the field is never read.
	Get string `json:"get,omitempty"`
	// Set is the write accessor's method name, e.g. "SetName"; empty when
	// the field is never written (computed attributes, read-only kinds).
	Set string `json:"set,omitempty"`
	// SDKType is the finished Go type the SDK carries the field as, e.g.
	// "*string", "int32", "[]string", "*models.Tag_kind".
	SDKType string `json:"sdk_type,omitempty"`
	// ConvertGet and ConvertSet name the convert functions bridging each
	// direction, e.g. "FromPtrString" / "ToPtrString".
	ConvertGet string `json:"convert_get,omitempty"`
	ConvertSet string `json:"convert_set,omitempty"`
	// ParseFunc is a generated enumeration's parse companion, finished
	// with its package qualifier, e.g. "models.ParseTag_kind"; empty for
	// anything that is not an enumeration.
	ParseFunc string `json:"parse_func,omitempty"`
	// NestedNilable reports whether a nested-object accessor's return can be
	// nil — a pointer, an interface (every kiota model is one) or another
	// reference type — so the state mapping guards the read before it
	// dereferences. False for a value-typed nested accessor, which cannot be
	// nil. Meaningful only on a nested-object field.
	NestedNilable bool `json:"nested_nilable,omitempty"`
}

// Removal is one thing pruning deleted, with the SDK's reason.
type Removal struct {
	// Kind is "resource", "datasource", "list_resource" or "action".
	Kind string `json:"kind"`
	// Key is the entity key.
	Key string `json:"key"`
	// Attribute is the pruned attribute path, empty when the whole entity
	// went.
	Attribute string `json:"attribute,omitempty"`
	// Reason is why the SDK cannot carry it.
	Reason string `json:"reason"`
}

func (r Removal) String() string {
	if r.Attribute == "" {
		return fmt.Sprintf("%s %s: %s", r.Kind, r.Key, r.Reason)
	}
	return fmt.Sprintf("%s %s.%s: %s", r.Kind, r.Key, r.Attribute, r.Reason)
}
