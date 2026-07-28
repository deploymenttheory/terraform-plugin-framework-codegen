// Package interop reads and writes HashiCorp's Provider Code Specification.
//
// The blueprint is a superset of that format, so export is a projection: the
// schema slice crosses, and everything the format cannot express -- CRUD wiring,
// SDK bindings, observed behavior, timeouts -- is reported rather than dropped in
// silence. Import is the reverse, and produces a draft (see DraftExt) because a
// document with a schema and no bindings is not something the emitter can use.
//
// # Why this package exists
//
// Nothing in this toolkit's pipeline consumes the exported document. The pipeline
// runs blueprint to Go, and the official format sits outside it. The value is as a
// conformance oracle: exporting the pilot blueprint and validating it against
// HashiCorp's own embedded JSON schema is the only check that the blueprint's
// schema slice describes a schema Terraform can actually have, performed by a
// party that does not share this repository's assumptions. That is a real but
// modest return, and it is worth stating plainly so nobody mistakes this package
// for interoperability with a consumer that does not exist.
//
// # Upstream types are used verbatim
//
// The official JSON is snake_case and the house golangci configuration enables
// tagliatelle with a camelCase default, so redeclaring the upstream structs here
// would put a linter exclusion on every field. Using HashiCorp's types directly
// means there are no local tags to lint, and no second definition to drift.
package interop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-codegen-spec/spec"
)

// SpecVersion is the Provider Code Specification version this package reads and
// writes.
//
// It is taken from upstream rather than written out here, because a literal would
// drift silently. The value matters more than it looks: spec.Validate switches on
// the document's version string exactly, so "0.1" is accepted and "0.1.0" is
// rejected -- despite the published documentation showing the three-part form.
// TestUnit_Interop_Version pins both halves of that.
const SpecVersion = spec.Version0_1

var (
	// ErrUnrepresentable is returned for a value the official format has no way to
	// carry and that cannot honestly be coarsened into one it does. It is an error
	// rather than a note because the alternative is writing a document that says
	// something the blueprint did not.
	ErrUnrepresentable = errors.New(
		"cannot be represented in Provider Code Specification " + SpecVersion,
	)

	// ErrInvalidSpec is returned for input that is not a valid specification.
	ErrInvalidSpec = errors.New("invalid provider code specification")

	// ErrDowngraded is returned by Report.Err under strict mode.
	ErrDowngraded = errors.New("the exported specification is a downgrade")
)

// jsonIndent matches internal/blueprint's on-disk indentation. The exported
// document is committed and reviewed by hand, so it is written indented.
const jsonIndent = "  "

// Marshal renders a specification as canonical JSON: two-space indented, one
// trailing newline, HTML escaping off.
//
// The spec package has no marshalling helper, so this is encoding/json with the
// same settings blueprint.Marshal uses. Canonical means byte-stable for a given
// value, which is what lets CI commit the export and fail on any diff.
func Marshal(s spec.Specification) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetIndent("", jsonIndent)
	// Go escapes <, > and & by default, which mangles descriptions and URLs for no
	// benefit here.
	enc.SetEscapeHTML(false)

	if err := enc.Encode(s); err != nil {
		return nil, fmt.Errorf("encoding the specification: %w", err)
	}

	return buf.Bytes(), nil
}

// Parse validates a document against the embedded JSON schema for its declared
// version, then decodes it.
//
// spec.Parse already does both, in that order; this wraps it so the failure
// carries ErrInvalidSpec and the caller can tell "your input is wrong" from "I
// could not write the output". Note that upstream's schema-error collection keeps
// only the last violation, so a document with several problems reports one -- the
// message is still specific enough to act on, but it is not exhaustive.
func Parse(ctx context.Context, data []byte) (spec.Specification, error) {
	s, err := spec.Parse(ctx, data)
	if err != nil {
		return spec.Specification{}, fmt.Errorf("%w: %w", ErrInvalidSpec, err)
	}

	return s, nil
}

// Validate reports whether a document satisfies the embedded JSON schema.
func Validate(ctx context.Context, data []byte) error {
	if err := spec.Validate(ctx, data); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidSpec, err)
	}

	return nil
}

// strPtr returns a pointer to s, or nil when s is empty.
//
// Every optional string in the official format is a *string, and the difference
// between nil and a pointer to "" is visible in the JSON: the first omits the key,
// the second writes `"description": ""`. Seventeen empty descriptions would be
// seventeen lines of noise in a committed document, so absence is always nil.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// boolPtr returns a pointer to b, or nil when b is false.
//
// Same reasoning as strPtr: the official format's Sensitive is a *bool, and
// `"sensitive": false` says nothing that omitting the key does not.
func boolPtr(b bool) *bool {
	if !b {
		return nil
	}
	return &b
}

// derefStr reads an optional string from the official format.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// derefBool reads an optional bool from the official format.
func derefBool(b *bool) bool {
	return b != nil && *b
}
