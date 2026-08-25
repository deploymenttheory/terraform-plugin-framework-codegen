// Package vendor_openapi_specs holds the third-party OpenAPI documents this
// toolkit's own tests parse and derive.
//
// A vendor's document exactly as published, committed and embedded so every
// machine reads the same bytes and no test needs a network. These are only
// ever read: nothing is imported, corrected, revised or generated from them,
// which is what separates them from the spec a provider is built out of.
//
// Replacing one is a deliberate act. Download the vendor's current document
// over the file, then update the version and count a consumer asserts against
// -- those assertions exist so a replacement is noticed rather than absorbed.
package vendor_openapi_specs

import _ "embed"

//go:embed testdata/thousandeyes.yaml
var thousandEyes []byte

// ThousandEyes is the ThousandEyes v7 unified API document, version 7.0.102:
// 208 paths, 327 operations. OpenAPI 3.0.1.
func ThousandEyes() []byte { return thousandEyes }
