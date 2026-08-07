// Package models is a miniature kiota-shaped model surface: pointer-typed
// properties behind Get/Set pairs, an interface the builders return, a
// constructor, and one keyword-mangled accessor -- everything the checker's
// method-access paths have to resolve.
package models

type Tag struct {
	name         *string
	errorEscaped *string
	id           *string
}

func NewTag() *Tag { return &Tag{} }

func (t *Tag) GetName() *string          { return t.name }
func (t *Tag) SetName(v *string)         { t.name = v }
func (t *Tag) GetErrorEscaped() *string  { return t.errorEscaped }
func (t *Tag) SetErrorEscaped(v *string) { t.errorEscaped = v }
func (t *Tag) GetId() *string            { return t.id }

type Tagable interface {
	GetName() *string
	GetErrorEscaped() *string
	GetId() *string
}

// TagSummary_kind is a kiota enumeration: an int-backed named type with a Parse
// function and a String method, which is the shape a generated SDK mints for a
// documented value set -- including one the document declared inline and never
// named.
type TagSummary_kind int

const (
	SIMPLE_TAGSUMMARY_KIND TagSummary_kind = iota
)

func (i TagSummary_kind) String() string { return []string{"SIMPLE"}[i] }

func ParseTagSummary_kind(v string) (any, error) { return SIMPLE_TAGSUMMARY_KIND, nil }

// TagSummary is a list element: the model a collection hands back, which is not
// always the model the by-id read returns.
type TagSummary struct {
	name *string
	kind *TagSummary_kind
}

func (t *TagSummary) GetName() *string          { return t.name }
func (t *TagSummary) GetKind() *TagSummary_kind { return t.kind }

type TagSummaryable interface {
	GetName() *string
	GetKind() *TagSummary_kind
}

// TagDetail_kind is a second enumeration, on a nested model: kiota mints one of
// these per inline value set, named for the model that declared it.
type TagDetail_kind int

const (
	SIMPLE_TAGDETAIL_KIND TagDetail_kind = iota
)

func (i TagDetail_kind) String() string { return []string{"SIMPLE"}[i] }

func ParseTagDetail_kind(v string) (any, error) { return SIMPLE_TAGDETAIL_KIND, nil }

// TagDetail is a nested model, in the two halves kiota generates: the concrete
// struct a constructor yields and the setters hang off, and the interface a
// getter reads out of. Its accessors carry the three shapes nested
// reconciliation has to resolve -- an inline enumeration, a slice of one, and a
// name kiota escaped.
type TagDetail struct {
	kind         *TagDetail_kind
	labels       []TagDetail_kind
	vendorEscape *string
	make         *string
}

func NewTagDetail() *TagDetail { return &TagDetail{} }

func (t *TagDetail) GetKind() *TagDetail_kind     { return t.kind }
func (t *TagDetail) SetKind(v *TagDetail_kind)    { t.kind = v }
func (t *TagDetail) GetLabels() []TagDetail_kind  { return t.labels }
func (t *TagDetail) SetLabels(v []TagDetail_kind) { t.labels = v }
func (t *TagDetail) GetVendorEscaped() *string    { return t.vendorEscape }
func (t *TagDetail) SetVendorEscaped(v *string)   { t.vendorEscape = v }
func (t *TagDetail) GetMake() *string             { return t.make }
func (t *TagDetail) SetMake(v *string)            { t.make = v }

type TagDetailable interface {
	GetKind() *TagDetail_kind
	GetLabels() []TagDetail_kind
	GetVendorEscaped() *string
	GetMake() *string
}

// HrefResponse is what a create answers with when the API returns a reference
// rather than the object: its identifier is a string where the created model's
// own is not.
type HrefResponse struct {
	id *string
}

func (h *HrefResponse) GetId() *string { return h.id }

type HrefResponseable interface {
	GetId() *string
}
