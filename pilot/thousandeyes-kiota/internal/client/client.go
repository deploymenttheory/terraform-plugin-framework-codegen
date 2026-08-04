// Package client builds the ThousandEyes SDK client and hands it to resources.
//
// It is hand-written and owned by the provider. Authentication is the one part of
// a provider that is always bespoke, and the generator makes no attempt at it: a
// generated resource only ever asks this package for a configured client.
package client

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/microsoft/kiota-abstractions-go/authentication"
	kiotahttp "github.com/microsoft/kiota-http-go"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/pilot/thousandeyes-kiota/internal/sdk"
)

// defaultAPIEndpoint is the v7 base URL the SDK itself defaults to, restated
// here because the allowed-hosts validator needs the host before the SDK's
// default has been applied.
const defaultAPIEndpoint = "https://api.thousandeyes.com/v7"

// Config is what the provider block resolves to, independent of the framework's
// value types, so building a client is testable without a Terraform plan.
type Config struct {
	BearerToken    string
	AccountGroupID string
	APIEndpoint    string
}

// staticTokenProvider hands Kiota's authentication pipeline a token that never
// changes. ThousandEyes tokens are created by hand and do not expire, so there
// is nothing to refresh and no flow to run.
//
// The token must never reach a log file: it cannot be revoked through the API,
// so one that leaks stays valid until somebody deletes it by hand in the UI.
// Nothing here or in the Kiota pipeline prints it.
type staticTokenProvider struct {
	token     string
	validator *authentication.AllowedHostsValidator
}

func (p *staticTokenProvider) GetAuthorizationToken(_ context.Context, _ *url.URL, _ map[string]any) (string, error) {
	return p.token, nil
}

func (p *staticTokenProvider) GetAllowedHostsValidator() *authentication.AllowedHostsValidator {
	return p.validator
}

// New builds an SDK client.
func New(cfg Config) (*sdk.ThousandEyesClient, error) {
	endpoint := cfg.APIEndpoint
	if endpoint == "" {
		endpoint = defaultAPIEndpoint
	}

	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("the API endpoint %q is not a usable URL", endpoint)
	}

	// The validator pins the bearer token to the configured host, so a redirect
	// or a mis-set endpoint cannot leak the credential to a host it was never
	// meant for.
	validator, err := authentication.NewAllowedHostsValidatorErrorCheck([]string{parsed.Hostname()})
	if err != nil {
		return nil, fmt.Errorf("building the allowed-hosts validator: %w", err)
	}

	authProvider := authentication.NewBaseBearerTokenAuthenticationProvider(&staticTokenProvider{
		token:     cfg.BearerToken,
		validator: validator,
	})

	// Request-body compression is off, and this is load-bearing rather than a
	// preference: kiota's default middleware gzips every request body and sets
	// Content-Encoding, which the ThousandEyes API answers with a bare 400 --
	// the first live acceptance run of this pilot found exactly that. The
	// resty pilot has always sent plain JSON; this client must match the wire
	// behaviour the recorded evidence was gathered under.
	middlewares, err := kiotahttp.GetDefaultMiddlewaresWithOptions(
		kiotahttp.NewCompressionOptionsReference(false),
	)
	if err != nil {
		return nil, fmt.Errorf("building the HTTP middleware: %w", err)
	}

	adapter, err := kiotahttp.NewNetHttpRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(
		authProvider, nil, nil, kiotahttp.GetDefaultClient(middlewares...),
	)
	if err != nil {
		return nil, fmt.Errorf("building the ThousandEyes client: %w", err)
	}
	adapter.SetBaseUrl(endpoint)

	// AccountGroupID is deliberately unused here. The resty SDK attached it to
	// every request as the aid query parameter; Kiota models aid per request
	// instead, so generated code applies it where an operation declares it
	// rather than this package inventing request middleware.
	_ = cfg.AccountGroupID

	return sdk.NewThousandEyesClient(adapter), nil
}

// ForResource retrieves the configured client during a resource's Configure.
//
// It tolerates a nil ProviderData because the framework calls Configure once with
// no data, before the provider itself has been configured. Treating that as an
// error produces a spurious diagnostic on every run.
func ForResource(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse, resourceType string) *sdk.ThousandEyesClient {
	if req.ProviderData == nil {
		return nil
	}

	client, ok := req.ProviderData.(*sdk.ThousandEyesClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("%s expected a *sdk.ThousandEyesClient but got %T. This is a bug in the provider.",
				resourceType, req.ProviderData),
		)
		return nil
	}

	return client
}

// ForAction is the action counterpart of ForResource.
//
// A third near-identical function rather than one generic helper, because the three
// ConfigureRequest types are distinct structs with no common interface -- the framework gives
// each block kind its own. A generic version would need a type parameter per field it reads,
// which is more machinery than the duplication costs.
func ForAction(_ context.Context, req action.ConfigureRequest, resp *action.ConfigureResponse, actionType string) *sdk.ThousandEyesClient {
	if req.ProviderData == nil {
		return nil
	}

	client, ok := req.ProviderData.(*sdk.ThousandEyesClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("%s expected a *sdk.ThousandEyesClient but got %T. This is a bug in the provider.",
				actionType, req.ProviderData),
		)
		return nil
	}

	return client
}

// ForDataSource is the data source counterpart of ForResource.
func ForDataSource(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse, resourceType string) *sdk.ThousandEyesClient {
	if req.ProviderData == nil {
		return nil
	}

	client, ok := req.ProviderData.(*sdk.ThousandEyesClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("%s expected a *sdk.ThousandEyesClient but got %T. This is a bug in the provider.",
				resourceType, req.ProviderData),
		)
		return nil
	}

	return client
}

// ForEphemeral is the ephemeral resource counterpart of ForResource.
func ForEphemeral(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse, ephemeralType string) *sdk.ThousandEyesClient {
	if req.ProviderData == nil {
		return nil
	}

	client, ok := req.ProviderData.(*sdk.ThousandEyesClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("%s expected a *sdk.ThousandEyesClient but got %T. This is a bug in the provider.",
				ephemeralType, req.ProviderData),
		)
		return nil
	}

	return client
}
