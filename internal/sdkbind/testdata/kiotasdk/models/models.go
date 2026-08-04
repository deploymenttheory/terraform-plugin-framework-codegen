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
