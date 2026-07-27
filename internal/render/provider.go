package render

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// RegistrationView is the view for a provider registration file.
//
// These files are generated in full rather than patched. The archetype provider's
// equivalent is 168 hand-maintained aliased imports, which is exactly the sort of
// list a person should not be maintaining; owning the whole file also means the
// drift check can police it, which a partially-generated file makes impossible.
type RegistrationView struct {
	Header  string
	Package string
	// Imports is the aliased import block for the registered packages.
	Imports string
	// Entries are the finished constructor references, e.g. "v7Tag.NewTagResource".
	Entries []string
}

// Registration builds the view for the resource or data source registration file.
func Registration(bp blueprint.Blueprint, kind Kind, opts Options) RegistrationView {
	v := RegistrationView{
		Header:  GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package: providerPackage(bp),
	}

	type entry struct{ alias, importPath, ctor string }
	var entries []entry

	switch kind {
	case KindResources:
		for _, r := range bp.Resources {
			if r.Drop {
				continue
			}
			entries = append(entries, entry{
				alias:      r.GoPackageAlias,
				importPath: resourcePackagePath(bp, r),
				ctor:       "New" + r.GoTypeName,
			})
		}
	case KindDataSources:
		for _, d := range bp.DataSources {
			if d.Drop {
				continue
			}
			entries = append(entries, entry{
				alias:      d.GoPackageAlias,
				importPath: dataSourcePackagePath(bp, d),
				ctor:       "New" + d.GoTypeName,
			})
		}
	}

	// Sorted by alias so the file's contents do not depend on blueprint ordering.
	// Without this the drift check would fire whenever a blueprint was reordered.
	sort.Slice(entries, func(i, j int) bool { return entries[i].alias < entries[j].alias })

	var imports []string
	for _, e := range entries {
		imports = append(imports, fmt.Sprintf("%s %s", e.alias, strconv.Quote(e.importPath)))
		v.Entries = append(v.Entries, e.alias+"."+e.ctor)
	}
	v.Imports = strings.Join(imports, "\n")

	return v
}

// Kind selects which registration file to render.
type Kind string

const (
	KindResources   Kind = "resources"
	KindDataSources Kind = "dataSources"
)

// providerPackage is the Go package name of the provider directory.
func providerPackage(bp blueprint.Blueprint) string {
	dir := bp.Provider.Conventions.ProviderPkgDir
	if dir == "" {
		return "provider"
	}
	return path.Base(dir)
}

// ResourceDir returns the repo-relative directory a resource is emitted into.
func ResourceDir(bp blueprint.Blueprint, r blueprint.Resource) string {
	return path.Join(append([]string{root(bp.Provider.Conventions.ResourceRoot, "internal/services/resources")},
		nonEmpty(r.ServiceGroup, r.APIVersionDir, r.GoPackage)...)...)
}

// DataSourceDir returns the repo-relative directory a data source is emitted into.
func DataSourceDir(bp blueprint.Blueprint, d blueprint.DataSource) string {
	return path.Join(append([]string{root(bp.Provider.Conventions.DataSourceRoot, "internal/services/datasources")},
		nonEmpty(d.ServiceGroup, d.APIVersionDir, d.GoPackage)...)...)
}

// ProviderDir returns the repo-relative provider package directory.
func ProviderDir(bp blueprint.Blueprint) string {
	return root(bp.Provider.Conventions.ProviderPkgDir, "internal/provider")
}

func resourcePackagePath(bp blueprint.Blueprint, r blueprint.Resource) string {
	return path.Join(bp.Provider.GoModule, ResourceDir(bp, r))
}

func dataSourcePackagePath(bp blueprint.Blueprint, d blueprint.DataSource) string {
	return path.Join(bp.Provider.GoModule, DataSourceDir(bp, d))
}

func root(configured, def string) string {
	if configured != "" {
		return configured
	}
	return def
}

// nonEmpty drops empty path components, so a resource with no service group does
// not produce a doubled separator in its directory.
func nonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
