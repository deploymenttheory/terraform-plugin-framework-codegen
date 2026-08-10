package specmodel

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Extensions holds an object's x-tfpfgen-* keys with their values already
// shape-checked, so the typed accessors below cannot fail. Keys outside the
// x-tfpfgen- namespace (x-ms-*, x-github-*, …) belong to other tools and
// pass by unrecorded.
type Extensions map[string]any

// The x-tfpfgen-* keys the contract defines. An x-tfpfgen-* key outside this
// set is refused at load, mirroring config's unknown-key stance: this
// namespace is ours, so a stray key in it is a typo or a version skew, and
// both should die loudly rather than be silently ignored.
const (
	// ExtCreateOnly marks a property the API accepts on create and
	// refuses on update.
	ExtCreateOnly = "x-tfpfgen-create-only"
	// ExtRequiredWhen records a value-conditional requirement: this
	// property is required when a sibling equals a value.
	ExtRequiredWhen = "x-tfpfgen-required-when"
	// ExtEventualConsistency records how long a read may lag a write.
	ExtEventualConsistency = "x-tfpfgen-eventual-consistency"
	// ExtUpdateStyle records how the update operation treats omitted
	// fields.
	ExtUpdateStyle = "x-tfpfgen-update-style"
	// ExtDeleteNotFoundOK marks a delete whose 404 means "already gone".
	ExtDeleteNotFoundOK = "x-tfpfgen-delete-not-found-ok"
	// ExtValuesOpen marks an enum the API accepts values beyond.
	ExtValuesOpen = "x-tfpfgen-values-open"
	// ExtVolatile marks a property that differs between identical reads.
	ExtVolatile = "x-tfpfgen-volatile"
	// ExtServerForced marks a property the server overwrites regardless
	// of what was sent.
	ExtServerForced = "x-tfpfgen-server-forced"
	// ExtSilentlyIgnoredOnUpdate marks a property updates accept and
	// discard.
	ExtSilentlyIgnoredOnUpdate = "x-tfpfgen-silently-ignored-on-update"
)

// RequiredWhen is one value-conditional requirement: the annotated property
// is required when the named sibling property equals the value.
type RequiredWhen struct {
	// Property is the gating sibling's wire name.
	Property string
	// Equals is the gating value, in its literal spelling.
	Equals string
}

// extensionShapes maps each known key to its value parser. The shape is
// checked once at load, against the document, where the error can carry a
// location — not at access time, where it could only panic or lie.
var extensionShapes = map[string]func(n *yaml.Node, at string) (any, error){
	ExtCreateOnly:              extBool,
	ExtRequiredWhen:            extRequiredWhen,
	ExtEventualConsistency:     extDuration,
	ExtUpdateStyle:             extString,
	ExtDeleteNotFoundOK:        extBool,
	ExtValuesOpen:              extBool,
	ExtVolatile:                extBool,
	ExtServerForced:            extBool,
	ExtSilentlyIgnoredOnUpdate: extBool,
}

// parseExtensions collects and shape-checks a mapping node's x-tfpfgen-*
// keys. It is also run where extensions are not retained, purely for the
// unknown-key refusal.
func parseExtensions(n *yaml.Node, at string) (Extensions, error) {
	var out Extensions
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		if !strings.HasPrefix(key, "x-tfpfgen-") {
			continue
		}
		parse, ok := extensionShapes[key]
		if !ok {
			return nil, fmt.Errorf("%s: unknown extension %q%s", at, key, suggestExtension(key))
		}
		val, err := parse(deref(n.Content[i+1]), at+"."+key)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = Extensions{}
		}
		out[key] = val
	}
	return out, nil
}

// suggestExtension renders a did-you-mean suffix for a mistyped key, or
// nothing when no known key is a plausible target.
func suggestExtension(got string) string {
	best, bestDist := "", len(got)/2+1
	for k := range extensionShapes {
		if d := levenshtein(got, k); d < bestDist {
			best, bestDist = k, d
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

func extBool(n *yaml.Node, at string) (any, error) {
	var b bool
	if n.Kind != yaml.ScalarNode || n.Decode(&b) != nil {
		return nil, fmt.Errorf("%s: must be true or false, got %q", at, n.Value)
	}
	return b, nil
}

func extString(n *yaml.Node, at string) (any, error) {
	if n.Kind != yaml.ScalarNode || n.Value == "" {
		return nil, fmt.Errorf("%s: must be a non-empty string", at)
	}
	return n.Value, nil
}

func extDuration(n *yaml.Node, at string) (any, error) {
	if n.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("%s: must be a duration string such as \"30s\"", at)
	}
	d, err := time.ParseDuration(n.Value)
	if err != nil || d < 0 {
		return nil, fmt.Errorf("%s: %q is not a non-negative duration such as \"30s\"", at, n.Value)
	}
	return d, nil
}

func extRequiredWhen(n *yaml.Node, at string) (any, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: must be a mapping with \"property\" and \"equals\"", at)
	}
	var rw RequiredWhen
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, deref(n.Content[i+1])
		switch key {
		case "property":
			rw.Property = val.Value
		case "equals":
			rw.Equals = val.Value
		default:
			return nil, fmt.Errorf("%s: unknown key %q; only \"property\" and \"equals\" are allowed", at, key)
		}
		if val.Kind != yaml.ScalarNode || val.Value == "" {
			return nil, fmt.Errorf("%s.%s: must be a non-empty scalar", at, key)
		}
	}
	if rw.Property == "" || rw.Equals == "" {
		return nil, fmt.Errorf("%s: both \"property\" and \"equals\" are required", at)
	}
	return rw, nil
}

// The accessors all return (value, ok) so a consumer can tell an explicit
// false from an absent key — the audit planner treats "observed false" and
// "never observed" differently, and collapsing them here would lose that.

// CreateOnly reads x-tfpfgen-create-only.
func (e Extensions) CreateOnly() (bool, bool) { return e.boolKey(ExtCreateOnly) }

// RequiredWhen reads x-tfpfgen-required-when.
func (e Extensions) RequiredWhen() (RequiredWhen, bool) {
	rw, ok := e[ExtRequiredWhen].(RequiredWhen)
	return rw, ok
}

// EventualConsistency reads x-tfpfgen-eventual-consistency.
func (e Extensions) EventualConsistency() (time.Duration, bool) {
	d, ok := e[ExtEventualConsistency].(time.Duration)
	return d, ok
}

// UpdateStyle reads x-tfpfgen-update-style.
func (e Extensions) UpdateStyle() (string, bool) {
	s, ok := e[ExtUpdateStyle].(string)
	return s, ok
}

// DeleteNotFoundOK reads x-tfpfgen-delete-not-found-ok.
func (e Extensions) DeleteNotFoundOK() (bool, bool) { return e.boolKey(ExtDeleteNotFoundOK) }

// ValuesOpen reads x-tfpfgen-values-open.
func (e Extensions) ValuesOpen() (bool, bool) { return e.boolKey(ExtValuesOpen) }

// Volatile reads x-tfpfgen-volatile.
func (e Extensions) Volatile() (bool, bool) { return e.boolKey(ExtVolatile) }

// ServerForced reads x-tfpfgen-server-forced.
func (e Extensions) ServerForced() (bool, bool) { return e.boolKey(ExtServerForced) }

// SilentlyIgnoredOnUpdate reads x-tfpfgen-silently-ignored-on-update.
func (e Extensions) SilentlyIgnoredOnUpdate() (bool, bool) {
	return e.boolKey(ExtSilentlyIgnoredOnUpdate)
}

func (e Extensions) boolKey(key string) (bool, bool) {
	b, ok := e[key].(bool)
	return b, ok
}

// levenshtein is the standard two-row edit distance, for typo suggestions.
func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, min(curr[j-1]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
