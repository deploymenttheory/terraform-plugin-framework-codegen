// Package provider's compile-time interface assertions.
//
// Hand-written and owned by the provider, so generation never touches this file. It exists to
// turn "the generated registration files are what make this provider serve each block kind"
// from a claim into a build failure if it stops being true.
//
// Only the core provider.Provider assertion is present for now: the Kiota pilot's generated
// surface starts empty, and the ProviderWithListResources, ProviderWithActions and
// ProviderWithEphemeralResources assertions return alongside the generated files that satisfy
// them -- asserting them earlier would fail the build the assertions exist to protect.
package provider

import "github.com/hashicorp/terraform-plugin-framework/provider"

var _ provider.Provider = (*Provider)(nil)
