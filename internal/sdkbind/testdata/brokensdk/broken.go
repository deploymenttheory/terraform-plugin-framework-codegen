// Package brokensdk does not type-check, deliberately: the loader must
// refuse it with the tool's reason rather than answer lookups from a
// half-loaded surface.
package brokensdk

// Client references a type that does not exist.
type Client struct {
	Missing NoSuchType
}
