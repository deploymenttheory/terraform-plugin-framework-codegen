// Package version carries the toolkit's own version string. It is a leaf
// package so that any other package may name the version without importing
// behavior.
package version

// value is stamped by the release build via
// -ldflags "-X .../internal/version.value=v1.2.3". A source build reports
// "dev" so a pipeline pinned to a tag can refuse it.
var value = "dev"

// Version reports the toolkit version as stamped at build time.
func Version() string {
	return value
}
