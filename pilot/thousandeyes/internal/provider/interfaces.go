// Package provider's compile-time interface assertions.
//
// Hand-written and owned by the provider, so `emit` never touches this file. It exists to
// turn "the generated registration files are what make this provider serve each block kind"
// from a claim into a build failure if it stops being true.
//
// Each of these is satisfied by a *generated* file: resources.go, datasources.go and
// list_resources.go respectively. Adding a resource, a data source or a list facet to a
// blueprint and regenerating is the whole change -- nothing here needs editing, which is
// the property being pinned.
package provider

import "github.com/hashicorp/terraform-plugin-framework/provider"

var (
	_ provider.Provider                  = (*Provider)(nil)
	_ provider.ProviderWithListResources = (*Provider)(nil)
)
