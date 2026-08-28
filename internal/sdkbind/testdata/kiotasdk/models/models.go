// Package models is the kiota-shaped model surface: pointer-typed
// properties behind Get/Set pairs, read-side interfaces the builders
// return, constructors for the write side, a keyword-mangled accessor,
// and an inline-minted enumeration with its Parse companion.
package models

import "time"

// Tag is the concrete model the constructor yields.
type Tag struct {
	id            *string
	name          *string
	errorEscaped  *string
	vendorEscaped *string
	count         *int32
	enabled       *bool
	createdAt     *time.Time
	kind          *Tag_kind
	kinds         []Tag_kind
	slug          *Slug
	labels        []string
	detail        TagDetailable
	weird         *TagDetail
	ownerId       *string
	owners        []string
}

// NewTag constructs a settable Tag.
func NewTag() *Tag { return &Tag{} }

// GetId reads the identifier.
func (t *Tag) GetId() *string { return t.id }

// SetId writes the identifier.
func (t *Tag) SetId(v *string) { t.id = v }

// GetName reads the name.
func (t *Tag) GetName() *string { return t.name }

// SetName writes the name.
func (t *Tag) SetName(v *string) { t.name = v }

// GetErrorEscaped reads the property named "error", which kiota escapes.
func (t *Tag) GetErrorEscaped() *string { return t.errorEscaped }

// SetErrorEscaped writes the escaped property.
func (t *Tag) SetErrorEscaped(v *string) { t.errorEscaped = v }

// GetVendorEscaped reads the property named "vendor" — a name kiota
// escapes that Go does not reserve as an identifier.
func (t *Tag) GetVendorEscaped() *string { return t.vendorEscaped }

// SetVendorEscaped writes the escaped property.
func (t *Tag) SetVendorEscaped(v *string) { t.vendorEscaped = v }

// GetCount reads a scalar the SDK carries narrower than the document.
func (t *Tag) GetCount() *int32 { return t.count }

// SetCount writes the narrow scalar.
func (t *Tag) SetCount(v *int32) { t.count = v }

// GetEnabled reads the flag.
func (t *Tag) GetEnabled() *bool { return t.enabled }

// SetEnabled writes the flag.
func (t *Tag) SetEnabled(v *bool) { t.enabled = v }

// GetCreatedAt reads a date-time property, carried as time.Time.
func (t *Tag) GetCreatedAt() *time.Time { return t.createdAt }

// SetCreatedAt writes the timestamp.
func (t *Tag) SetCreatedAt(v *time.Time) { t.createdAt = v }

// GetKind reads the minted enumeration.
func (t *Tag) GetKind() *Tag_kind { return t.kind }

// SetKind writes the enumeration.
func (t *Tag) SetKind(v *Tag_kind) { t.kind = v }

// GetKinds reads the enumeration slice.
func (t *Tag) GetKinds() []Tag_kind { return t.kinds }

// SetKinds writes the enumeration slice.
func (t *Tag) SetKinds(v []Tag_kind) { t.kinds = v }

// GetSlug reads a named string with no enumeration companion.
func (t *Tag) GetSlug() *Slug { return t.slug }

// SetSlug writes it.
func (t *Tag) SetSlug(v *Slug) { t.slug = v }

// GetAlias reads one shape and SetAlias takes another — the API that
// writes rule identifiers and reads rule objects.
func (t *Tag) GetAlias() *string { return t.name }

// SetAlias takes a different shape than GetAlias yields.
func (t *Tag) SetAlias(v []string) { t.labels = v }

// GetLabels reads the scalar slice.
func (t *Tag) GetLabels() []string { return t.labels }

// SetLabels writes the scalar slice.
func (t *Tag) SetLabels(v []string) { t.labels = v }

// GetDetail reads the nested object.
func (t *Tag) GetDetail() TagDetailable { return t.detail }

// SetDetail writes the nested object.
func (t *Tag) SetDetail(v TagDetailable) { t.detail = v }

// SetOwnerId writes a property the request model carries and the
// read-side interface never answers — the request-only field.
func (t *Tag) SetOwnerId(v *string) { t.ownerId = v }

// GetOwners reads owners as objects where SetOwners takes their ids — the
// API that writes identifiers and reads what they identify.
func (t *Tag) GetOwners() []Ownerable { return nil }

// SetOwners writes the owner identifiers.
func (t *Tag) SetOwners(v []string) { t.owners = v }

// Owner is what an owner reads back as.
type Owner struct {
	id *string
}

// GetId reads the owner's identifier.
func (o *Owner) GetId() *string { return o.id }

// Ownerable is the owner's read-side interface.
type Ownerable interface {
	GetId() *string
}

// GetWeird carries a model where the document promised a string.
func (t *Tag) GetWeird() *TagDetail { return t.weird }

// SetWeird writes the mispromised property.
func (t *Tag) SetWeird(v *TagDetail) { t.weird = v }

// Tagable is the read-side interface the builders return.
type Tagable interface {
	GetId() *string
	GetName() *string
	GetErrorEscaped() *string
	GetVendorEscaped() *string
	GetCount() *int32
	GetEnabled() *bool
	GetCreatedAt() *time.Time
	GetKind() *Tag_kind
	GetKinds() []Tag_kind
	GetSlug() *Slug
	GetAlias() *string
	GetLabels() []string
	GetDetail() TagDetailable
	GetWeird() *TagDetail
	GetOwners() []Ownerable
}

// Slug is a named string that is not an enumeration: no companion
// function stands beside it.
type Slug string

// Orphan is a model whose interface offers no constructor — the shape a
// binding cannot build a body for.
type Orphan struct {
	name *string
}

// GetName reads the name.
func (o *Orphan) GetName() *string { return o.name }

// SetName writes the name.
func (o *Orphan) SetName(v *string) { o.name = v }

// Orphanable is the read-side interface; NewOrphan deliberately does not
// exist.
type Orphanable interface {
	GetName() *string
}

// Tag_kind is a kiota enumeration: an int-backed named type with a Parse
// companion, minted from an inline value set the document never named.
type Tag_kind int

// SIMPLE_TAG_KIND is the one value.
const SIMPLE_TAG_KIND Tag_kind = 0

// String renders the enumeration.
func (k Tag_kind) String() string { return "SIMPLE" }

// ParseTag_kind is the companion the converters bridge through.
func ParseTag_kind(v string) (any, error) {
	_ = v
	return SIMPLE_TAG_KIND, nil
}

// TagDetail is a nested model in the two halves kiota generates.
type TagDetail struct {
	note   *string
	weight *float32
}

// NewTagDetail constructs a settable TagDetail.
func NewTagDetail() *TagDetail { return &TagDetail{} }

// GetNote reads the note.
func (d *TagDetail) GetNote() *string { return d.note }

// SetNote writes the note.
func (d *TagDetail) SetNote(v *string) { d.note = v }

// GetWeight reads a float the SDK carries narrower than the document.
func (d *TagDetail) GetWeight() *float32 { return d.weight }

// SetWeight writes the narrow float.
func (d *TagDetail) SetWeight(v *float32) { d.weight = v }

// TagDetailable is the nested read-side interface.
type TagDetailable interface {
	GetNote() *string
	GetWeight() *float32
}

// TagCollectionResponse is the paged collection envelope.
type TagCollectionResponse struct {
	value []Tagable
}

// NewTagCollectionResponse constructs the envelope.
func NewTagCollectionResponse() *TagCollectionResponse { return &TagCollectionResponse{} }

// GetValue reads the page's elements.
func (r *TagCollectionResponse) GetValue() []Tagable { return r.value }

// SetValue writes the page's elements.
func (r *TagCollectionResponse) SetValue(v []Tagable) { r.value = v }

// TagCollectionResponseable is the envelope's read-side interface.
type TagCollectionResponseable interface {
	GetValue() []Tagable
}

// Gizmoable is the element of a wrapped list whose envelope is an
// interface with a single slice getter named for the wire key — the
// ThousandEyes Tagsable shape, where GET /gizmos answers
// {"gizmos":[...]} and kiota models it as GetGizmos rather than GetValue.
type Gizmoable interface {
	GetId() *string
	GetName() *string
}

// GizmoCollectionResponseable is the wrapped-list envelope: a single
// slice getter named for the "gizmos" wire key, with no GetValue to fall
// back on. Binding reaches its elements through GetGizmos().
type GizmoCollectionResponseable interface {
	GetGizmos() []Gizmoable
}

// Blobable is the element of a list whose envelope carries several
// equally-plausible slice getters and no wire key to choose between them
// — the ambiguous shape a binding refuses rather than guesses.
type Blobable interface {
	GetName() *string
}

// BlobCollectionResponseable offers two slice getters with no envelope
// key naming either, so resolution stays ambiguous and the entity prunes.
type BlobCollectionResponseable interface {
	GetPrimary() []Blobable
	GetSecondary() []Blobable
}
