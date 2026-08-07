// Package models is the Nimbus fixture SDK's model surface.
//
// It is written by hand in the shape kiota generates -- pointer-typed
// properties behind Get/Set pairs, an -able interface per model, a New
// constructor, int-backed enumerations with a String method and a Parse
// function -- because the fixture's whole purpose is to prove that generated
// provider code compiles against the symbols a blueprint names. A real kiota
// SDK for three resources would be several thousand lines of serialisation
// nobody would read; none of it is what the generator binds to.
//
// Nothing here talks to a network. The methods exist to be typechecked.
package models

import "fmt"

// LabelScope is the fixture's first enumeration: the shape kiota mints for a
// documented value set, which the generated expand reaches through
// ParseLabelScope and the generated flatten reaches through String.
type LabelScope int

const (
	// GLOBAL_LABELSCOPE is the zero value, as kiota's iota block makes the
	// first declared member.
	GLOBAL_LABELSCOPE LabelScope = iota
	PROJECT_LABELSCOPE
	TEAM_LABELSCOPE
)

func (l LabelScope) String() string {
	return []string{"global", "project", "team"}[l]
}

// ParseLabelScope is the companion the generated code passes alongside the type
// argument. It returns any, which is kiota's own signature.
func ParseLabelScope(v string) (any, error) {
	switch v {
	case "global":
		return GLOBAL_LABELSCOPE, nil
	case "project":
		return PROJECT_LABELSCOPE, nil
	case "team":
		return TEAM_LABELSCOPE, nil
	}
	return nil, fmt.Errorf("%s is not a known LabelScope", v)
}

// Label is the flat model: every property a scalar.
type Label struct {
	id          *string
	name        *string
	colour      *string
	scope       *LabelScope
	description *string
	archived    *bool
}

// NewLabel is the constructor the blueprint names as constructorExpr.
func NewLabel() *Label { return &Label{} }

func (m *Label) GetId() *string           { return m.id }
func (m *Label) SetId(v *string)          { m.id = v }
func (m *Label) GetName() *string         { return m.name }
func (m *Label) SetName(v *string)        { m.name = v }
func (m *Label) GetColour() *string       { return m.colour }
func (m *Label) SetColour(v *string)      { m.colour = v }
func (m *Label) GetScope() *LabelScope    { return m.scope }
func (m *Label) SetScope(v *LabelScope)   { m.scope = v }
func (m *Label) GetDescription() *string  { return m.description }
func (m *Label) SetDescription(v *string) { m.description = v }
func (m *Label) GetArchived() *bool       { return m.archived }
func (m *Label) SetArchived(v *bool)      { m.archived = v }

// Labelable is what a request builder hands back and takes.
type Labelable interface {
	GetId() *string
	SetId(v *string)
	GetName() *string
	SetName(v *string)
	GetColour() *string
	SetColour(v *string)
	GetScope() *LabelScope
	SetScope(v *LabelScope)
	GetDescription() *string
	SetDescription(v *string)
	GetArchived() *bool
	SetArchived(v *bool)
}

// LabelCollection is the envelope a collection GET answers with, which is not
// the element type: the list facet and the data source's resolver both have to
// reach through it.
type LabelCollection struct {
	value []Labelable
}

func NewLabelCollection() *LabelCollection { return &LabelCollection{} }

func (m *LabelCollection) GetValue() []Labelable  { return m.value }
func (m *LabelCollection) SetValue(v []Labelable) { m.value = v }

type LabelCollectionable interface {
	GetValue() []Labelable
	SetValue(v []Labelable)
}

// ReleaseChannel is the second enumeration, on the shaped resource.
type ReleaseChannel int

const (
	STABLE_RELEASECHANNEL ReleaseChannel = iota
	BETA_RELEASECHANNEL
	CANARY_RELEASECHANNEL
)

func (r ReleaseChannel) String() string {
	return []string{"stable", "beta", "canary"}[r]
}

func ParseReleaseChannel(v string) (any, error) {
	switch v {
	case "stable":
		return STABLE_RELEASECHANNEL, nil
	case "beta":
		return BETA_RELEASECHANNEL, nil
	case "canary":
		return CANARY_RELEASECHANNEL, nil
	}
	return nil, fmt.Errorf("%s is not a known ReleaseChannel", v)
}

// ReleaseAsset is a nested collection element.
type ReleaseAsset struct {
	name      *string
	sizeBytes *int64
	platforms []string
}

func NewReleaseAsset() *ReleaseAsset { return &ReleaseAsset{} }

func (m *ReleaseAsset) GetName() *string        { return m.name }
func (m *ReleaseAsset) SetName(v *string)       { m.name = v }
func (m *ReleaseAsset) GetSizeBytes() *int64    { return m.sizeBytes }
func (m *ReleaseAsset) SetSizeBytes(v *int64)   { m.sizeBytes = v }
func (m *ReleaseAsset) GetPlatforms() []string  { return m.platforms }
func (m *ReleaseAsset) SetPlatforms(v []string) { m.platforms = v }

type ReleaseAssetable interface {
	GetName() *string
	SetName(v *string)
	GetSizeBytes() *int64
	SetSizeBytes(v *int64)
	GetPlatforms() []string
	SetPlatforms(v []string)
}

// ReleaseAuthor is a nested single object, and read-only: the fixture's
// blueprint declares no expand for it, so only its getters are ever reached.
type ReleaseAuthor struct {
	login     *string
	accountId *string
}

func NewReleaseAuthor() *ReleaseAuthor { return &ReleaseAuthor{} }

func (m *ReleaseAuthor) GetLogin() *string      { return m.login }
func (m *ReleaseAuthor) SetLogin(v *string)     { m.login = v }
func (m *ReleaseAuthor) GetAccountId() *string  { return m.accountId }
func (m *ReleaseAuthor) SetAccountId(v *string) { m.accountId = v }

type ReleaseAuthorable interface {
	GetLogin() *string
	SetLogin(v *string)
	GetAccountId() *string
	SetAccountId(v *string)
}

// Release carries the escaped accessor kiota produces for a property whose name
// collides with a Go keyword or a generated method: draft becomes DraftEscaped.
type Release struct {
	id           *string
	tagName      *string
	name         *string
	body         *string
	draftEscaped *bool
	channel      *ReleaseChannel
	publishToken *string
	assets       []ReleaseAssetable
	author       ReleaseAuthorable
}

func NewRelease() *Release { return &Release{} }

func (m *Release) GetId() *string                 { return m.id }
func (m *Release) SetId(v *string)                { m.id = v }
func (m *Release) GetTagName() *string            { return m.tagName }
func (m *Release) SetTagName(v *string)           { m.tagName = v }
func (m *Release) GetName() *string               { return m.name }
func (m *Release) SetName(v *string)              { m.name = v }
func (m *Release) GetBody() *string               { return m.body }
func (m *Release) SetBody(v *string)              { m.body = v }
func (m *Release) GetDraftEscaped() *bool         { return m.draftEscaped }
func (m *Release) SetDraftEscaped(v *bool)        { m.draftEscaped = v }
func (m *Release) GetChannel() *ReleaseChannel    { return m.channel }
func (m *Release) SetChannel(v *ReleaseChannel)   { m.channel = v }
func (m *Release) GetPublishToken() *string       { return m.publishToken }
func (m *Release) SetPublishToken(v *string)      { m.publishToken = v }
func (m *Release) GetAssets() []ReleaseAssetable  { return m.assets }
func (m *Release) SetAssets(v []ReleaseAssetable) { m.assets = v }
func (m *Release) GetAuthor() ReleaseAuthorable   { return m.author }
func (m *Release) SetAuthor(v ReleaseAuthorable)  { m.author = v }

type Releaseable interface {
	GetId() *string
	SetId(v *string)
	GetTagName() *string
	SetTagName(v *string)
	GetName() *string
	SetName(v *string)
	GetBody() *string
	SetBody(v *string)
	GetDraftEscaped() *bool
	SetDraftEscaped(v *bool)
	GetChannel() *ReleaseChannel
	SetChannel(v *ReleaseChannel)
	GetPublishToken() *string
	SetPublishToken(v *string)
	GetAssets() []ReleaseAssetable
	SetAssets(v []ReleaseAssetable)
	GetAuthor() ReleaseAuthorable
	SetAuthor(v ReleaseAuthorable)
}

// RunnerSize is the third enumeration.
type RunnerSize int

const (
	SMALL_RUNNERSIZE RunnerSize = iota
	MEDIUM_RUNNERSIZE
	LARGE_RUNNERSIZE
)

func (r RunnerSize) String() string {
	return []string{"small", "medium", "large"}[r]
}

func ParseRunnerSize(v string) (any, error) {
	switch v {
	case "small":
		return SMALL_RUNNERSIZE, nil
	case "medium":
		return MEDIUM_RUNNERSIZE, nil
	case "large":
		return LARGE_RUNNERSIZE, nil
	}
	return nil, fmt.Errorf("%s is not a known RunnerSize", v)
}

// RunnerPool is the replace-only resource's model.
type RunnerPool struct {
	id             *string
	name           *string
	size           *RunnerSize
	maximumRunners *int64
	status         *string
}

func NewRunnerPool() *RunnerPool { return &RunnerPool{} }

func (m *RunnerPool) GetId() *string             { return m.id }
func (m *RunnerPool) SetId(v *string)            { m.id = v }
func (m *RunnerPool) GetName() *string           { return m.name }
func (m *RunnerPool) SetName(v *string)          { m.name = v }
func (m *RunnerPool) GetSize() *RunnerSize       { return m.size }
func (m *RunnerPool) SetSize(v *RunnerSize)      { m.size = v }
func (m *RunnerPool) GetMaximumRunners() *int64  { return m.maximumRunners }
func (m *RunnerPool) SetMaximumRunners(v *int64) { m.maximumRunners = v }
func (m *RunnerPool) GetStatus() *string         { return m.status }
func (m *RunnerPool) SetStatus(v *string)        { m.status = v }

type RunnerPoolable interface {
	GetId() *string
	SetId(v *string)
	GetName() *string
	SetName(v *string)
	GetSize() *RunnerSize
	SetSize(v *RunnerSize)
	GetMaximumRunners() *int64
	SetMaximumRunners(v *int64)
	GetStatus() *string
	SetStatus(v *string)
}

// RunnerToken is what the ephemeral opens: a value that must never reach state.
type RunnerToken struct {
	value     *string
	expiresAt *string
}

func NewRunnerToken() *RunnerToken { return &RunnerToken{} }

func (m *RunnerToken) GetValue() *string      { return m.value }
func (m *RunnerToken) SetValue(v *string)     { m.value = v }
func (m *RunnerToken) GetExpiresAt() *string  { return m.expiresAt }
func (m *RunnerToken) SetExpiresAt(v *string) { m.expiresAt = v }

type RunnerTokenable interface {
	GetValue() *string
	SetValue(v *string)
	GetExpiresAt() *string
	SetExpiresAt(v *string)
}
