package specmodel

import (
	"fmt"
	"strings"
)

// pendingRef is one schema reference awaiting its target, remembered with
// the location it was written at so a dangling pointer names the exact spot.
type pendingRef struct {
	schema *Schema
	at     string
}

// resolve wires every schema reference to its target. It runs after the
// whole document is parsed, so forward references and self references cost
// nothing, and a reference to a schema the document never declares is
// refused by name.
func (l *loader) resolve() error {
	for _, p := range l.refs {
		target, ok := l.doc.Schemas[p.schema.Ref]
		if !ok {
			return fmt.Errorf("%s: references #/components/schemas/%s, which the document does not declare",
				p.at, p.schema.Ref)
		}
		p.schema.resolved = target
	}
	return nil
}

// componentName extracts the component name from a local reference under
// base, refusing everything this loader cannot honour: remote references,
// because the revised document is the single source of truth and must be
// self-contained; and pointers into parts of the document that are not
// referenceable components here.
func componentName(ref, base, at string) (string, error) {
	if !strings.HasPrefix(ref, "#") {
		return "", fmt.Errorf("%s: remote reference %q is not supported; the revised document must be self-contained",
			at, ref)
	}
	if !strings.HasPrefix(ref, base) {
		return "", fmt.Errorf("%s: reference %q is not supported here; only %s<name> is", at, ref, base)
	}
	raw := strings.TrimPrefix(ref, base)
	if raw == "" || strings.Contains(raw, "/") {
		return "", fmt.Errorf("%s: reference %q does not name a single component", at, ref)
	}
	// RFC 6901 escapes, decoded in the order the RFC requires.
	raw = strings.ReplaceAll(raw, "~1", "/")
	return strings.ReplaceAll(raw, "~0", "~"), nil
}
