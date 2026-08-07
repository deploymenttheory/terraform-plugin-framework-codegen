package sdk

import (
	"context"

	abstractions "github.com/microsoft/kiota-abstractions-go"

	"example.com/nimbus/terraform-provider-nimbus/internal/sdk/models"
)

// LabelsRequestBuilder is the collection hop: it both indexes into one label
// and carries the collection's own verbs.
type LabelsRequestBuilder struct {
	adapter abstractions.RequestAdapter
}

// ByLabelId is the typed indexer.
func (b *LabelsRequestBuilder) ByLabelId(labelId string) *LabelItemRequestBuilder {
	return &LabelItemRequestBuilder{adapter: b.adapter, labelId: labelId}
}

func (b *LabelsRequestBuilder) Get(
	ctx context.Context, cfg *RequestConfiguration,
) (models.LabelCollectionable, error) {
	return models.NewLabelCollection(), nil
}

func (b *LabelsRequestBuilder) Post(
	ctx context.Context, body models.Labelable, cfg *RequestConfiguration,
) (models.Labelable, error) {
	return body, nil
}

// LabelItemRequestBuilder addresses one label.
type LabelItemRequestBuilder struct {
	adapter abstractions.RequestAdapter
	labelId string
}

func (b *LabelItemRequestBuilder) Get(
	ctx context.Context, cfg *RequestConfiguration,
) (models.Labelable, error) {
	return models.NewLabel(), nil
}

func (b *LabelItemRequestBuilder) Patch(
	ctx context.Context, body models.Labelable, cfg *RequestConfiguration,
) (models.Labelable, error) {
	return body, nil
}

func (b *LabelItemRequestBuilder) Delete(ctx context.Context, cfg *RequestConfiguration) error {
	return nil
}

// ReleasesRequestBuilder anchors the release chains.
type ReleasesRequestBuilder struct {
	adapter abstractions.RequestAdapter
}

func (b *ReleasesRequestBuilder) ByReleaseId(releaseId string) *ReleaseItemRequestBuilder {
	return &ReleaseItemRequestBuilder{adapter: b.adapter, releaseId: releaseId}
}

func (b *ReleasesRequestBuilder) Post(
	ctx context.Context, body models.Releaseable, cfg *RequestConfiguration,
) (models.Releaseable, error) {
	return body, nil
}

// ReleaseItemRequestBuilder addresses one release. Its update verb is Put
// rather than Patch, which is what makes the resource's putFull update style
// honest rather than decorative.
type ReleaseItemRequestBuilder struct {
	adapter   abstractions.RequestAdapter
	releaseId string
}

func (b *ReleaseItemRequestBuilder) Get(
	ctx context.Context, cfg *RequestConfiguration,
) (models.Releaseable, error) {
	return models.NewRelease(), nil
}

func (b *ReleaseItemRequestBuilder) Put(
	ctx context.Context, body models.Releaseable, cfg *RequestConfiguration,
) (models.Releaseable, error) {
	return body, nil
}

func (b *ReleaseItemRequestBuilder) Delete(ctx context.Context, cfg *RequestConfiguration) error {
	return nil
}

// RunnerPoolsRequestBuilder anchors the runner-pool chains.
type RunnerPoolsRequestBuilder struct {
	adapter abstractions.RequestAdapter
}

func (b *RunnerPoolsRequestBuilder) ByRunnerPoolId(runnerPoolId string) *RunnerPoolItemRequestBuilder {
	return &RunnerPoolItemRequestBuilder{adapter: b.adapter, runnerPoolId: runnerPoolId}
}

func (b *RunnerPoolsRequestBuilder) Post(
	ctx context.Context, body models.RunnerPoolable, cfg *RequestConfiguration,
) (models.RunnerPoolable, error) {
	return body, nil
}

// RunnerPoolItemRequestBuilder addresses one pool and hangs the sub-resources
// the action and the ephemeral reach through off it.
type RunnerPoolItemRequestBuilder struct {
	adapter      abstractions.RequestAdapter
	runnerPoolId string
}

func (b *RunnerPoolItemRequestBuilder) Get(
	ctx context.Context, cfg *RequestConfiguration,
) (models.RunnerPoolable, error) {
	return models.NewRunnerPool(), nil
}

func (b *RunnerPoolItemRequestBuilder) Delete(ctx context.Context, cfg *RequestConfiguration) error {
	return nil
}

// Retire is the action's hop.
func (b *RunnerPoolItemRequestBuilder) Retire() *RunnerPoolRetireRequestBuilder {
	return &RunnerPoolRetireRequestBuilder{adapter: b.adapter}
}

// Resume reverses Retire, and is what the action's generated acceptance test
// calls to put the tenant back the way it found it.
func (b *RunnerPoolItemRequestBuilder) Resume() *RunnerPoolResumeRequestBuilder {
	return &RunnerPoolResumeRequestBuilder{adapter: b.adapter}
}

// RegistrationToken is the ephemeral's hop.
func (b *RunnerPoolItemRequestBuilder) RegistrationToken() *RunnerPoolRegistrationTokenRequestBuilder {
	return &RunnerPoolRegistrationTokenRequestBuilder{adapter: b.adapter}
}

type RunnerPoolRetireRequestBuilder struct {
	adapter abstractions.RequestAdapter
}

func (b *RunnerPoolRetireRequestBuilder) Post(ctx context.Context, cfg *RequestConfiguration) error {
	return nil
}

type RunnerPoolResumeRequestBuilder struct {
	adapter abstractions.RequestAdapter
}

func (b *RunnerPoolResumeRequestBuilder) Post(ctx context.Context, cfg *RequestConfiguration) error {
	return nil
}

type RunnerPoolRegistrationTokenRequestBuilder struct {
	adapter abstractions.RequestAdapter
}

func (b *RunnerPoolRegistrationTokenRequestBuilder) Get(
	ctx context.Context, cfg *RequestConfiguration,
) (models.RunnerTokenable, error) {
	return models.NewRunnerToken(), nil
}
