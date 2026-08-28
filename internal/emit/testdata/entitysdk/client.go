// A hand-written stand-in for a kiota-generated SDK client, exposing
// exactly the fluent surface the fictional petstore bindings name. The
// method bodies never run — the compile test only proves the rendered
// tree type-checks against a real SDK shape.
package sdk

import (
	"context"
	"errors"

	abstractions "github.com/microsoft/kiota-abstractions-go"

	"example.test/provider/internal/sdk/models"
)

// errStub is what every stub call answers; nothing in the compile test
// ever invokes one.
var errStub = errors.New("the stub SDK carries shape, not behaviour")

// APIClient stands in for the generated client.
type APIClient struct {
	adapter abstractions.RequestAdapter
}

// NewAPIClient stands in for the generated constructor.
func NewAPIClient(adapter abstractions.RequestAdapter) *APIClient {
	return &APIClient{adapter: adapter}
}

// HttpServers reaches the http-servers collection.
func (c *APIClient) HttpServers() *HttpServersRequestBuilder { return &HttpServersRequestBuilder{} }

// AlertRules reaches the alert-rules collection.
func (c *APIClient) AlertRules() *AlertRulesRequestBuilder { return &AlertRulesRequestBuilder{} }

// Licenses reaches the licenses collection.
func (c *APIClient) Licenses() *LicensesRequestBuilder { return &LicensesRequestBuilder{} }

// AuditEvents reaches the audit-events collection.
func (c *APIClient) AuditEvents() *AuditEventsRequestBuilder { return &AuditEventsRequestBuilder{} }

// HttpServersRequestBuilder is the collection builder.
type HttpServersRequestBuilder struct{}

// Post creates one http server.
func (b *HttpServersRequestBuilder) Post(_ context.Context, _ models.HttpServerable, _ any) (models.HttpServerable, error) {
	return nil, errStub
}

// Get lists the collection.
func (b *HttpServersRequestBuilder) Get(_ context.Context, _ any) (models.HttpServerCollectionResponseable, error) {
	return nil, errStub
}

// ByHttpServerId reaches one item.
func (b *HttpServersRequestBuilder) ByHttpServerId(_ string) *HttpServerItemRequestBuilder {
	return &HttpServerItemRequestBuilder{}
}

// HttpServerItemRequestBuilder is the item builder.
type HttpServerItemRequestBuilder struct{}

// Get reads the item.
func (b *HttpServerItemRequestBuilder) Get(_ context.Context, _ any) (models.HttpServerable, error) {
	return nil, errStub
}

// Patch updates the item.
func (b *HttpServerItemRequestBuilder) Patch(_ context.Context, _ models.HttpServerable, _ any) (models.HttpServerable, error) {
	return nil, errStub
}

// Delete removes the item.
func (b *HttpServerItemRequestBuilder) Delete(_ context.Context, _ any) error { return errStub }

// HttpServerItemRequestBuilderDeleteQueryParameters is the query the delete
// takes.
type HttpServerItemRequestBuilderDeleteQueryParameters struct {
	Confirm *bool `uriparametername:"confirm"`
}

// Restart reaches the restart action.
func (b *HttpServerItemRequestBuilder) Restart() *HttpServerRestartRequestBuilder {
	return &HttpServerRestartRequestBuilder{}
}

// HttpServerRestartRequestBuilder is the action builder.
type HttpServerRestartRequestBuilder struct{}

// Post invokes the restart.
func (b *HttpServerRestartRequestBuilder) Post(_ context.Context, _ models.HttpServerRestartRequestable, _ any) error {
	return errStub
}

// AlertRulesRequestBuilder is the collection builder.
type AlertRulesRequestBuilder struct{}

// Post creates one alert rule.
func (b *AlertRulesRequestBuilder) Post(_ context.Context, _ models.AlertRuleable, _ any) (models.AlertRuleable, error) {
	return nil, errStub
}

// ByAlertRuleId reaches one item.
func (b *AlertRulesRequestBuilder) ByAlertRuleId(_ string) *AlertRuleItemRequestBuilder {
	return &AlertRuleItemRequestBuilder{}
}

// AlertRuleItemRequestBuilder is the item builder.
type AlertRuleItemRequestBuilder struct{}

// Get reads the item.
func (b *AlertRuleItemRequestBuilder) Get(_ context.Context, _ any) (models.AlertRuleable, error) {
	return nil, errStub
}

// Delete removes the item.
func (b *AlertRuleItemRequestBuilder) Delete(_ context.Context, _ any) error { return errStub }

// LicensesRequestBuilder is the collection builder.
type LicensesRequestBuilder struct{}

// ByLicenseKey reaches one license.
func (b *LicensesRequestBuilder) ByLicenseKey(_ string) *LicenseItemRequestBuilder {
	return &LicenseItemRequestBuilder{}
}

// LicenseItemRequestBuilder is the item builder.
type LicenseItemRequestBuilder struct{}

// Get reads the license.
func (b *LicenseItemRequestBuilder) Get(_ context.Context, _ any) (models.Licenseable, error) {
	return nil, errStub
}

// AuditEventsRequestBuilder is the collection builder.
type AuditEventsRequestBuilder struct{}

// Get lists the collection.
func (b *AuditEventsRequestBuilder) Get(_ context.Context, _ any) (models.AuditEventCollectionResponseable, error) {
	return nil, errStub
}
