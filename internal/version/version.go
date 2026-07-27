// Package version holds the toolkit's version string.
//
// It is deliberately the only place a version appears. Generated files carry
// neither a version nor a timestamp: a version string in emitted output would
// make every release rewrite every generated file, and a timestamp would make
// the drift check fail on a run that changed nothing. A version reaches
// generated artefacts only through the emission manifest, where a bump is a
// one-line diff.
package version

// Version is the toolkit version. Releases override it at build time:
//
//	-ldflags "-X github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/version.Version=v0.1.0"
var Version = "dev"
