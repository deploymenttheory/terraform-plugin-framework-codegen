package quirkserver

import (
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
)

// specDoc is the OpenAPI 3 document the quirk server implements — the
// /things surface, described the way a vendor would describe it. It lives
// under testdata because it is fixture data, not machinery.
//
//go:embed testdata/openapi.yaml
var specDoc []byte

// Spec returns the OpenAPI 3 document describing the surface this server
// serves, for a pipeline that needs an upstream document to import. The
// returned slice is the caller's to keep.
func Spec() []byte {
	out := make([]byte, len(specDoc))
	copy(out, specDoc)
	return out
}

// Standalone starts a quirk server listening on addr, for running the
// fixture as a real HTTP process rather than inside a test — the stand-in
// live API when the whole pipeline is exercised without credentials. The
// caller owns the returned server and must Close it.
func Standalone(addr string, q Quirks) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("quirkserver cannot listen on %s: %w", addr, err)
	}

	s := &Server{
		quirks:  q,
		objects: map[string]map[string]any{},
		nextID:  1,
		reads:   map[string]int{},
	}
	s.initCollections()

	// httptest carries the URL bookkeeping New already relies on; only the
	// listener is swapped for the caller's address.
	ts := httptest.NewUnstartedServer(http.HandlerFunc(s.handle))
	_ = ts.Listener.Close()
	ts.Listener = ln
	ts.Start()
	s.Server = ts

	return s, nil
}

// StandaloneQuirks is the profile the standalone server runs with: each
// switch deviates from the embedded document (Spec) in a way the audit
// exists to catch, so a pipeline exercised against it has real findings to
// fold back into the spec.
func StandaloneQuirks() Quirks {
	return Quirks{
		// The document calls notes writable; the server discards it.
		SilentlyDiscards: []string{"notes"},
		// The document says nothing about color being fixed; updates refuse it.
		ImmutableAfterCreate: []string{"color"},
		// The document says retention defaults to 30; the server assigns 45.
		ConstantDefaults: map[string]any{"retention": 45},
		// The reads carry an etag the document never declares, and it changes
		// on every read.
		VolatileFields: []string{"etag"},
		// The document's MiXeD example comes back lower-cased.
		NormalisesCase: []string{"code"},
		// name is genuinely enforced; the equally documented mode is not.
		RequiredButUndeclared: []string{"name"},
		// mode's documented values, actually closed.
		ClosedEnum: map[string][]string{"mode": {"basic", "advanced"}},
		// query matters exactly when mode is advanced, as the document's
		// x-tfpfgen-required-when claims.
		ConditionallyRequired: &Conditional{WhenField: "mode", WhenValue: "advanced", Then: "query"},
		// Updates answer 2xx and quietly leave label alone.
		SilentlyDiscardsOnUpdate: []string{"label"},
	}
}
