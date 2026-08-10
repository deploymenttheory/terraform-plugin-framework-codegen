package sdk

// Tag is a generated model: exported fields with Get/Set helper pairs
// that deref on the way out, the openapi-generator shape.
type Tag struct {
	Id     *string    `json:"id,omitempty"`
	Name   string     `json:"name"`
	Count  *int32     `json:"count,omitempty"`
	Labels []string   `json:"labels,omitempty"`
	Kind   *TagKind   `json:"kind,omitempty"`
	Detail *TagDetail `json:"detail,omitempty"`
}

// NewTagWithDefaults constructs a Tag with defaults applied.
func NewTagWithDefaults() *Tag { return &Tag{} }

// GetId reads the identifier, zero when unset.
func (o *Tag) GetId() string {
	if o.Id == nil {
		return ""
	}
	return *o.Id
}

// SetId writes the identifier.
func (o *Tag) SetId(v string) { o.Id = &v }

// GetName reads the required name.
func (o *Tag) GetName() string { return o.Name }

// SetName writes the name.
func (o *Tag) SetName(v string) { o.Name = v }

// GetCount reads a scalar the SDK carries narrower than the document.
func (o *Tag) GetCount() int32 {
	if o.Count == nil {
		return 0
	}
	return *o.Count
}

// SetCount writes the narrow scalar.
func (o *Tag) SetCount(v int32) { o.Count = &v }

// GetLabels reads the scalar slice.
func (o *Tag) GetLabels() []string { return o.Labels }

// SetLabels writes the scalar slice.
func (o *Tag) SetLabels(v []string) { o.Labels = v }

// GetKind reads the generated enumeration.
func (o *Tag) GetKind() TagKind {
	if o.Kind == nil {
		return ""
	}
	return *o.Kind
}

// SetKind writes the enumeration.
func (o *Tag) SetKind(v TagKind) { o.Kind = &v }

// GetDetail reads the nested object.
func (o *Tag) GetDetail() TagDetail {
	if o.Detail == nil {
		return TagDetail{}
	}
	return *o.Detail
}

// SetDetail writes the nested object.
func (o *Tag) SetDetail(v TagDetail) { o.Detail = &v }

// TagKind is a generated enumeration: a string-backed type with a
// FromValue companion.
type TagKind string

// TAGKIND_SIMPLE is the one value.
const TAGKIND_SIMPLE TagKind = "SIMPLE"

// NewTagKindFromValue is the companion the converters bridge through.
func NewTagKindFromValue(v string) (*TagKind, error) {
	k := TagKind(v)
	return &k, nil
}

// TagDetail is a nested model.
type TagDetail struct {
	Note *string `json:"note,omitempty"`
}

// NewTagDetailWithDefaults constructs a TagDetail.
func NewTagDetailWithDefaults() *TagDetail { return &TagDetail{} }

// GetNote reads the note.
func (o *TagDetail) GetNote() string {
	if o.Note == nil {
		return ""
	}
	return *o.Note
}

// SetNote writes the note.
func (o *TagDetail) SetNote(v string) { o.Note = &v }

// GroupList is a paged envelope with exactly one slice field.
type GroupList struct {
	Items []Group `json:"items"`
}

// Group is the envelope's element.
type Group struct {
	Name string `json:"name"`
}

// GetName reads the group's name.
func (o *Group) GetName() string { return o.Name }

// SetName writes the group's name.
func (o *Group) SetName(v string) { o.Name = v }
