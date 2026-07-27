// Package client builds the ThousandEyes SDK client and hands it to resources.
//
// It is hand-written and owned by the provider. Authentication is the one part of
// a provider that is always bespoke, and the generator makes no attempt at it: a
// generated resource only ever asks this package for a configured client.
package client

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	te "github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes"
	"github.com/deploymenttheory/go-sdk-thousandeyes/thousandeyes/config"
)

// Config is what the provider block resolves to, independent of the framework's
// value types, so building a client is testable without a Terraform plan.
type Config struct {
	BearerToken    string
	AccountGroupID string
	APIEndpoint    string
}

// New builds an SDK client.
func New(cfg Config) (*te.Client, error) {
	authCfg := &config.AuthConfig{
		BearerToken:    cfg.BearerToken,
		AccountGroupID: cfg.AccountGroupID,
		APIEndpoint:    cfg.APIEndpoint,
		// The token must never reach a log file. A ThousandEyes token does not
		// expire and cannot be revoked through the API, so one that leaks stays
		// valid until somebody deletes it by hand in the UI.
		HideSensitiveData: true,
	}

	client, err := te.NewClient(authCfg)
	if err != nil {
		return nil, fmt.Errorf("building the ThousandEyes client: %w", err)
	}

	return client, nil
}

// ForResource retrieves the configured client during a resource's Configure.
//
// It tolerates a nil ProviderData because the framework calls Configure once with
// no data, before the provider itself has been configured. Treating that as an
// error produces a spurious diagnostic on every run.
func ForResource(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse, resourceType string) *te.Client {
	if req.ProviderData == nil {
		return nil
	}

	client, ok := req.ProviderData.(*te.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("%s expected a *thousandeyes.Client but got %T. This is a bug in the provider.",
				resourceType, req.ProviderData),
		)
		return nil
	}

	return client
}

// ForDataSource is the data source counterpart of ForResource.
func ForDataSource(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse, resourceType string) *te.Client {
	if req.ProviderData == nil {
		return nil
	}

	client, ok := req.ProviderData.(*te.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("%s expected a *thousandeyes.Client but got %T. This is a bug in the provider.",
				resourceType, req.ProviderData),
		)
		return nil
	}

	return client
}
