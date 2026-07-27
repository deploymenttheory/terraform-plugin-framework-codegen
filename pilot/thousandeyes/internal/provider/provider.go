// Package provider is the provider server.
//
// The schema and configuration plumbing here are hand-written, because
// authentication is always bespoke. The resource and data source registration
// lists are generated: see resources.go and datasources.go, which carry a
// generated header and must not be edited.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	teclient "github.com/deploymenttheory/terraform-plugin-framework-codegen/pilot/thousandeyes/internal/client"
)

// Environment variables the provider falls back to, matching the SDK's own
// names so a practitioner does not have to learn two sets.
const (
	envBearerToken    = "THOUSANDEYES_BEARER_TOKEN"
	envAccountGroupID = "THOUSANDEYES_ACCOUNT_GROUP_ID"
	envAPIEndpoint    = "THOUSANDEYES_API_ENDPOINT"
)

var _ provider.Provider = (*Provider)(nil)

// Provider is the ThousandEyes provider.
type Provider struct {
	// version is set at build time and reported to Terraform.
	version string
	// client, when non-nil, is used instead of building one from configuration.
	// Unit tests set it so they can run against an interception layer without
	// credentials.
	client any
}

// New returns a provider factory for the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &Provider{version: version}
	}
}

// NewWithClient returns a provider that uses a pre-built client. It exists for
// unit tests, which must never depend on credentials being present.
func NewWithClient(version string, client any) func() provider.Provider {
	return func() provider.Provider {
		return &Provider{version: version, client: client}
	}
}

func (p *Provider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "thousandeyes"
	resp.Version = p.version
}

// Model is the provider block's configuration.
type Model struct {
	BearerToken    types.String `tfsdk:"bearer_token"`
	AccountGroupID types.String `tfsdk:"account_group_id"`
	APIEndpoint    types.String `tfsdk:"api_endpoint"`
}

func (p *Provider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages ThousandEyes resources through the v7 API.",
		Attributes: map[string]schema.Attribute{
			"bearer_token": schema.StringAttribute{
				MarkdownDescription: "A ThousandEyes user API token, created under " +
					"**Account Settings > Users and Roles > Profile**. May also be set with the `" +
					envBearerToken + "` environment variable.\n\n" +
					"ThousandEyes has no service-account identity type: this token belongs to a " +
					"user, does not expire, and cannot be revoked through the API.",
				Optional:  true,
				Sensitive: true,
			},
			"account_group_id": schema.StringAttribute{
				MarkdownDescription: "Scopes every request to an account group, sent as the `aid` " +
					"query parameter. When unset, the API uses the token owner's default account " +
					"group, which means an object can appear absent rather than merely invisible. " +
					"May also be set with the `" + envAccountGroupID + "` environment variable.",
				Optional: true,
			},
			"api_endpoint": schema.StringAttribute{
				MarkdownDescription: "Overrides the API base URL. Defaults to the SDK's v7 endpoint. " +
					"May also be set with the `" + envAPIEndpoint + "` environment variable.",
				Optional: true,
			},
		},
	}
}

func (p *Provider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	// A test-supplied client short-circuits configuration entirely, so unit
	// tests need no credentials and cannot reach the network by accident.
	if p.client != nil {
		resp.ResourceData = p.client
		resp.DataSourceData = p.client
		return
	}

	var cfg Model
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resolved := teclient.Config{
		BearerToken:    firstNonEmpty(cfg.BearerToken, envBearerToken),
		AccountGroupID: firstNonEmpty(cfg.AccountGroupID, envAccountGroupID),
		APIEndpoint:    firstNonEmpty(cfg.APIEndpoint, envAPIEndpoint),
	}

	if resolved.BearerToken == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("bearer_token"),
			"Missing ThousandEyes bearer token",
			"Set the bearer_token provider attribute or the "+envBearerToken+" environment variable. "+
				"Create a token under Account Settings > Users and Roles > Profile.",
		)
		return
	}

	client, err := teclient.New(resolved)
	if err != nil {
		resp.Diagnostics.AddError("Unable to configure the ThousandEyes client", err.Error())
		return
	}

	tflog.Debug(ctx, "configured the ThousandEyes client", map[string]any{
		"account_group_id_set": resolved.AccountGroupID != "",
		"custom_endpoint":      resolved.APIEndpoint != "",
	})

	resp.ResourceData = client
	resp.DataSourceData = client
}

// firstNonEmpty prefers the configured value, then the environment variable.
// Configuration winning over the environment is the convention practitioners
// expect: an explicit value in a file should not be silently overridden by a
// stray shell export.
func firstNonEmpty(configured types.String, envVar string) string {
	if !configured.IsNull() && !configured.IsUnknown() && configured.ValueString() != "" {
		return configured.ValueString()
	}
	return os.Getenv(envVar)
}

// Resources and DataSources are implemented in the generated resources.go and
// datasources.go, so adding a resource is a regeneration rather than an edit here.
