// Package templates holds the file templates provider generation renders.
//
// Templates carry shape, Go carries logic: these files contain no decisions.
// Every value a template interpolates is a finished string computed in
// internal/emit, and the only conditionals are presence branches over
// booleans the render context precomputed. If a template needs to decide
// something, the decision belongs in the renderer.
//
// Each template's path under provider-core/ mirrors the path it is emitted
// to, with .tmpl appended, so finding the template for an emitted file
// needs no lookup table. Files whose base name starts with an underscore
// are shared partials, parsed alongside every template and never emitted
// themselves. A base name ending in _kiota.go.tmpl or
// _openapigenerator.go.tmpl is backend-specific: exactly one of the pair
// is emitted, selected by the render context's backend booleans.
package templates

import "embed"

// ProviderCore is the provider core: the shared runtime plumbing every
// generated provider repo carries, rendered once per repo by `provider
// generate`. The all: prefix keeps dot-prefixed outputs (.gitignore,
// .golangci.yml) and the underscore-prefixed partials in the embedded tree.
//
//go:embed all:provider-core
var ProviderCore embed.FS

// Services holds the per-entity templates: the four service-kind file
// sets `provider generate` renders once per derived entity. Unlike the
// provider core, these templates interpolate computed declarations —
// schemas, mappings, call plans — every one of which arrives as a
// finished string from internal/emit.
//
//go:embed all:services
var Services embed.FS
