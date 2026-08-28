package specmodel

import (
	"fmt"
	"sort"
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
	// ExtImmutable marks a property the API accepts on create and
	// refuses on update.
	ExtImmutable = "x-tfpfgen-immutable"
	// ExtRequiredWhen records a value-conditional requirement: this
	// property is required when a sibling equals a value.
	ExtRequiredWhen = "x-tfpfgen-required-when"
	// ExtReadAfterWrite records how long a read may lag a write.
	ExtReadAfterWrite = "x-tfpfgen-read-after-write"
	// ExtUpdateStyle records how the update operation treats omitted
	// fields: "patch-merge", "put-full" or "replace-only".
	ExtUpdateStyle = "x-tfpfgen-update-style"
	// ExtDeleteNotFoundOK marks a delete whose 404 means "already gone".
	ExtDeleteNotFoundOK = "x-tfpfgen-delete-not-found-ok"
	// ExtValues marks an enum the API accepts values beyond.
	ExtValues = "x-tfpfgen-values"
	// ExtVolatile marks a property that differs between identical reads.
	ExtVolatile = "x-tfpfgen-volatile"
	// ExtServerForced marks a property the server overwrites regardless
	// of what was sent.
	ExtServerForced = "x-tfpfgen-server-forced"
	// ExtServerDefault carries the value the server stores for a writable
	// property the request omitted. It is deliberately not OpenAPI's own
	// `default`, which declares what the document says should happen; this
	// records what the API was observed to do, and it is what makes the
	// attribute Optional and Computed — the practitioner may set it, and
	// Terraform must accept the server's value when they do not.
	ExtServerDefault = "x-tfpfgen-server-default"
	// ExtIgnoredOnUpdate marks a property updates accept and
	// discard.
	ExtIgnoredOnUpdate = "x-tfpfgen-ignored-on-update"
	// ExtValidWhen records a value-conditional validity: this property is
	// valid only when a sibling gate equals a value.
	ExtValidWhen = "x-tfpfgen-valid-when"
	// ExtDependsOn records a co-requirement: this property is settable only
	// when the named sibling is also present, whatever its value.
	ExtDependsOn = "x-tfpfgen-depends-on"
	// ExtMutuallyExclusive records, at the schema level, a set of sibling
	// properties of which at most one may be set.
	ExtMutuallyExclusive = "x-tfpfgen-mutually-exclusive"
	// ExtValidConfiguration records, at the schema level, a discriminator
	// property and the per-value sets of properties valid under each value.
	ExtValidConfiguration = "x-tfpfgen-valid-configuration"
	// ExtListWrapper records, on a list operation, whether the live
	// collection response wraps its items under a key of an object, and
	// which key. Read from the wire, never from the document.
	ExtListWrapper = "x-tfpfgen-list-wrapper"
	// ExtListPagination records the pagination style a list operation's
	// live response advertises.
	ExtListPagination = "x-tfpfgen-list-pagination"
	// ExtIdentifierProperty records, on a read operation, which response
	// property carries the value the item path addresses the object by.
	// A document whose path says {id} while its body spells the same
	// identifier "aid" states the correspondence nowhere else.
	ExtIdentifierProperty = "x-tfpfgen-identifier-property"
)

// The pagination styles x-tfpfgen-list-pagination admits.
const ()

// listPaginations are the pagination styles the extension admits, matching
// the observation vocabulary the audit records them in.
var listPaginations = map[string]bool{
	"cursor": true, "offset": true, "page": true, "none": true,
}

// ValidListPagination reports whether s is a pagination style
// x-tfpfgen-list-pagination admits. Whatever compiles the key must be
// able to ask before writing it, or a vocabulary drift would land as a
// document this package then refuses to load.
func ValidListPagination(s string) bool { return listPaginations[s] }

// RequiredWhen is one value-conditional requirement: the annotated property
// is required when the named sibling property equals the value.
type RequiredWhen struct {
	// Property is the gating sibling's wire name.
	Property string
	// Equals is the gating value, in its literal spelling.
	Equals string
}

// ValidWhen is one value-conditional validity: the annotated property is
// valid only when the named sibling property equals the value.
type ValidWhen struct {
	// Property is the gating sibling's wire name.
	Property string
	// Equals is the gating value, in its literal spelling.
	Equals string
}

// DependsOn is one co-requirement: the annotated property may be set only
// when the required sibling property is also present.
type DependsOn struct {
	// Requires is the sibling property's wire name that must be present.
	Requires string
}

// ValidConfiguration is a schema-level variant structure: a discriminator
// property whose value selects which further properties are valid, and the
// per-value sets of valid property names. Variants are sorted by Value and
// each variant's Fields are sorted, so two loads present identically.
type ValidConfiguration struct {
	// Discriminator is the gate property's wire name.
	Discriminator string
	// Variants lists, per discriminator value, the property names valid
	// under that value.
	Variants []ValidVariant
}

// ValidVariant is one discriminator value and the property names valid
// under it.
type ValidVariant struct {
	Value  string
	Fields []string
}

// ListWrapper is one list operation's collection-response wrapping, as the
// audit found it on the wire rather than as the document declares it.
type ListWrapper struct {
	// Wrapped is true when the items sit under a key of an object, false
	// when the response is the item array itself.
	Wrapped bool
	// Key is the wrapping key when Wrapped, empty otherwise.
	Key string
}

// extensionParsers maps each known key to its value parser. The shape is
// checked once at load, against the document, where the error can carry a
// location — not at access time, where it could only panic or lie.
var extensionParsers = map[string]func(n *yaml.Node, at string) (any, error){
	ExtImmutable:          extBool,
	ExtRequiredWhen:       extRequiredWhen,
	ExtReadAfterWrite:     extDuration,
	ExtUpdateStyle:        extUpdateStyle,
	ExtDeleteNotFoundOK:   extBool,
	ExtValues:             extBool,
	ExtVolatile:           extBool,
	ExtServerForced:       extBool,
	ExtServerDefault:      extScalar,
	ExtIgnoredOnUpdate:    extBool,
	ExtValidWhen:          extValidWhen,
	ExtDependsOn:          extDependsOn,
	ExtMutuallyExclusive:  extMutuallyExclusive,
	ExtValidConfiguration: extValidConfiguration,
	ExtListWrapper:        extListWrapper,
	ExtListPagination:     extListPagination,
	ExtIdentifierProperty: extNonEmptyString,
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
		parse, ok := extensionParsers[key]
		if !ok {
			return nil, fmt.Errorf("%s: unknown extension %q%s", at, key, suggestExtension(key))
		}
		value, err := parse(deref(n.Content[i+1]), at+"."+key)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = Extensions{}
		}
		out[key] = value
	}
	return out, nil
}

// suggestExtension renders a did-you-mean suffix for a mistyped key, or
// nothing when no known key is a plausible target.
func suggestExtension(got string) string {
	best, bestDist := "", len(got)/2+1
	for k := range extensionParsers {
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

// extScalar accepts any single value and keeps the type YAML decoded it as.
// The value is a reading, not a vocabulary: whatever the API stored is what
// belongs here, and the type is load-bearing because it is compared against
// what a live response carries.
func extScalar(n *yaml.Node, at string) (any, error) {
	if n.Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("%s: must be a single value, got a %s", at, nodeKind(n))
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, fmt.Errorf("%s: %w", at, err)
	}
	return v, nil
}

// extNonEmptyString accepts a single non-empty string, for an extension whose
// value names something in the document.
func extNonEmptyString(n *yaml.Node, at string) (any, error) {
	if n.Kind != yaml.ScalarNode || n.Value == "" {
		return nil, fmt.Errorf("%s: must be a non-empty name, got %q", at, n.Value)
	}
	return n.Value, nil
}

// nodeKind names a YAML node kind for an error message.
func nodeKind(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "mapping"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.AliasNode:
		return "alias"
	default:
		return "value"
	}
}

// extUpdateStyle accepts the closed set of update styles and nothing else.
// The value steers what generated update logic sends for omitted fields, so
// an unlisted spelling is refused at load, where the error can name the
// document location, rather than surfacing as a silent wrong request later.
func extUpdateStyle(n *yaml.Node, at string) (any, error) {
	if n.Kind == yaml.ScalarNode {
		switch n.Value {
		case "patch-merge", "put-full", "replace-only":
			return n.Value, nil
		}
	}
	return nil, fmt.Errorf("%s: must be one of \"patch-merge\", \"put-full\" or \"replace-only\", got %q", at, n.Value)
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
		key, value := n.Content[i].Value, deref(n.Content[i+1])
		switch key {
		case "property":
			rw.Property = value.Value
		case "equals":
			rw.Equals = value.Value
		default:
			return nil, fmt.Errorf("%s: unknown key %q; only \"property\" and \"equals\" are allowed", at, key)
		}
		if value.Kind != yaml.ScalarNode || value.Value == "" {
			return nil, fmt.Errorf("%s.%s: must be a non-empty scalar", at, key)
		}
	}
	if rw.Property == "" || rw.Equals == "" {
		return nil, fmt.Errorf("%s: both \"property\" and \"equals\" are required", at)
	}
	return rw, nil
}

// extValidWhen parses x-tfpfgen-valid-when: a mapping with "property" and
// "equals", the same shape as required-when.
func extValidWhen(n *yaml.Node, at string) (any, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: must be a mapping with \"property\" and \"equals\"", at)
	}
	var vw ValidWhen
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i].Value, deref(n.Content[i+1])
		switch key {
		case "property":
			vw.Property = value.Value
		case "equals":
			vw.Equals = value.Value
		default:
			return nil, fmt.Errorf("%s: unknown key %q; only \"property\" and \"equals\" are allowed", at, key)
		}
		if value.Kind != yaml.ScalarNode || value.Value == "" {
			return nil, fmt.Errorf("%s.%s: must be a non-empty scalar", at, key)
		}
	}
	if vw.Property == "" || vw.Equals == "" {
		return nil, fmt.Errorf("%s: both \"property\" and \"equals\" are required", at)
	}
	return vw, nil
}

// extDependsOn parses x-tfpfgen-depends-on: a mapping with a single
// "requires" naming the property that must be present.
func extDependsOn(n *yaml.Node, at string) (any, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: must be a mapping with \"requires\"", at)
	}
	var do DependsOn
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i].Value, deref(n.Content[i+1])
		switch key {
		case "requires":
			do.Requires = value.Value
		default:
			return nil, fmt.Errorf("%s: unknown key %q; only \"requires\" is allowed", at, key)
		}
		if value.Kind != yaml.ScalarNode || value.Value == "" {
			return nil, fmt.Errorf("%s.%s: must be a non-empty scalar", at, key)
		}
	}
	if do.Requires == "" {
		return nil, fmt.Errorf("%s: \"requires\" is required", at)
	}
	return do, nil
}

// extMutuallyExclusive parses x-tfpfgen-mutually-exclusive: a non-empty
// sequence of at least two distinct property names, sorted for determinism.
func extMutuallyExclusive(n *yaml.Node, at string) (any, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s: must be a list of property names", at)
	}
	seen := map[string]bool{}
	var names []string
	for _, item := range n.Content {
		item = deref(item)
		if item.Kind != yaml.ScalarNode || item.Value == "" {
			return nil, fmt.Errorf("%s: each entry must be a non-empty property name", at)
		}
		if seen[item.Value] {
			return nil, fmt.Errorf("%s: property %q is listed twice", at, item.Value)
		}
		seen[item.Value] = true
		names = append(names, item.Value)
	}
	if len(names) < 2 {
		return nil, fmt.Errorf("%s: a mutually-exclusive set needs at least two properties", at)
	}
	sort.Strings(names)
	return names, nil
}

// extValidConfiguration parses x-tfpfgen-valid-configuration: a mapping with
// "discriminator" (a property name) and "variants" (a mapping from a
// discriminator value to the list of property names valid under it).
func extValidConfiguration(n *yaml.Node, at string) (any, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: must be a mapping with \"discriminator\" and \"variants\"", at)
	}
	var vc ValidConfiguration
	var variantsNode *yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i].Value, deref(n.Content[i+1])
		switch key {
		case "discriminator":
			if value.Kind != yaml.ScalarNode || value.Value == "" {
				return nil, fmt.Errorf("%s.discriminator: must be a non-empty property name", at)
			}
			vc.Discriminator = value.Value
		case "variants":
			variantsNode = value
		default:
			return nil, fmt.Errorf("%s: unknown key %q; only \"discriminator\" and \"variants\" are allowed", at, key)
		}
	}
	if vc.Discriminator == "" {
		return nil, fmt.Errorf("%s: \"discriminator\" is required", at)
	}
	if variantsNode == nil || variantsNode.Kind != yaml.MappingNode || len(variantsNode.Content) == 0 {
		return nil, fmt.Errorf("%s.variants: must be a non-empty mapping from value to property names", at)
	}
	for i := 0; i+1 < len(variantsNode.Content); i += 2 {
		value := variantsNode.Content[i].Value
		list := deref(variantsNode.Content[i+1])
		if list.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("%s.variants.%s: must be a list of property names", at, value)
		}
		var fields []string
		for _, item := range list.Content {
			item = deref(item)
			if item.Kind != yaml.ScalarNode || item.Value == "" {
				return nil, fmt.Errorf("%s.variants.%s: each entry must be a non-empty property name", at, value)
			}
			fields = append(fields, item.Value)
		}
		sort.Strings(fields)
		vc.Variants = append(vc.Variants, ValidVariant{Value: value, Fields: fields})
	}
	sort.Slice(vc.Variants, func(i, j int) bool { return vc.Variants[i].Value < vc.Variants[j].Value })
	return vc, nil
}

// extListWrapper parses x-tfpfgen-list-wrapper: a mapping with "wrapped"
// (a boolean) and "key" (the wrapping key, required by and only meaningful
// for a wrapped response). A wrapped response with no key is refused here, at
// the document, rather than downstream where it could only be read as
// unwrapped and quietly unwrap a response that is not an array.
func extListWrapper(n *yaml.Node, at string) (any, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: must be a mapping with \"wrapped\" and, when wrapped, \"key\"", at)
	}
	var w ListWrapper
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i].Value, deref(n.Content[i+1])
		if value.Kind != yaml.ScalarNode || value.Value == "" {
			return nil, fmt.Errorf("%s.%s: must be a non-empty scalar", at, key)
		}
		switch key {
		case "wrapped":
			switch value.Value {
			case "true":
				w.Wrapped = true
			case "false":
				w.Wrapped = false
			default:
				return nil, fmt.Errorf("%s.wrapped: must be true or false, got %q", at, value.Value)
			}
		case "key":
			w.Key = value.Value
		default:
			return nil, fmt.Errorf("%s: unknown key %q; only \"wrapped\" and \"key\" are allowed", at, key)
		}
	}
	switch {
	case w.Wrapped && w.Key == "":
		return nil, fmt.Errorf("%s: a wrapped response needs the \"key\" its items sit under", at)
	case !w.Wrapped && w.Key != "":
		return nil, fmt.Errorf("%s: an unwrapped response wraps nothing, so \"key\" is meaningless", at)
	}
	return w, nil
}

// extListPagination parses x-tfpfgen-list-pagination: one of the advertised
// styles, or "none".
func extListPagination(n *yaml.Node, at string) (any, error) {
	if n.Kind != yaml.ScalarNode || n.Value == "" {
		return nil, fmt.Errorf("%s: must be a non-empty scalar", at)
	}
	if !listPaginations[n.Value] {
		return nil, fmt.Errorf("%s: must be one of \"cursor\", \"offset\", \"page\" or \"none\", got %q", at, n.Value)
	}
	return n.Value, nil
}

// The accessors all return (value, ok) so a consumer can tell an explicit
// false from an absent key — the audit planner treats "observed false" and
// "never observed" differently, and collapsing them here would lose that.

// Immutable reads x-tfpfgen-immutable.
func (e Extensions) Immutable() (bool, bool) { return e.boolKey(ExtImmutable) }

// RequiredWhen reads x-tfpfgen-required-when.
func (e Extensions) RequiredWhen() (RequiredWhen, bool) {
	rw, ok := e[ExtRequiredWhen].(RequiredWhen)
	return rw, ok
}

// ValidWhen reads x-tfpfgen-valid-when.
func (e Extensions) ValidWhen() (ValidWhen, bool) {
	vw, ok := e[ExtValidWhen].(ValidWhen)
	return vw, ok
}

// DependsOn reads x-tfpfgen-depends-on.
func (e Extensions) DependsOn() (DependsOn, bool) {
	do, ok := e[ExtDependsOn].(DependsOn)
	return do, ok
}

// MutuallyExclusive reads x-tfpfgen-mutually-exclusive.
func (e Extensions) MutuallyExclusive() ([]string, bool) {
	names, ok := e[ExtMutuallyExclusive].([]string)
	return names, ok
}

// ValidConfiguration reads x-tfpfgen-valid-configuration.
func (e Extensions) ValidConfiguration() (ValidConfiguration, bool) {
	vc, ok := e[ExtValidConfiguration].(ValidConfiguration)
	return vc, ok
}

// ListWrapper reads x-tfpfgen-list-wrapper.
func (e Extensions) ListWrapper() (ListWrapper, bool) {
	w, ok := e[ExtListWrapper].(ListWrapper)
	return w, ok
}

// ListPagination reads x-tfpfgen-list-pagination.
func (e Extensions) ListPagination() (string, bool) {
	p, ok := e[ExtListPagination].(string)
	return p, ok
}

// IdentifierProperty reads x-tfpfgen-identifier-property.
func (e Extensions) IdentifierProperty() (string, bool) {
	name, ok := e[ExtIdentifierProperty].(string)
	return name, ok
}

// ReadAfterWrite reads x-tfpfgen-read-after-write.
func (e Extensions) ReadAfterWrite() (time.Duration, bool) {
	d, ok := e[ExtReadAfterWrite].(time.Duration)
	return d, ok
}

// UpdateStyle reads x-tfpfgen-update-style. The value is one of
// "patch-merge", "put-full" or "replace-only" — anything else was already
// refused at load.
func (e Extensions) UpdateStyle() (string, bool) {
	s, ok := e[ExtUpdateStyle].(string)
	return s, ok
}

// DeleteNotFoundOK reads x-tfpfgen-delete-not-found-ok.
func (e Extensions) DeleteNotFoundOK() (bool, bool) { return e.boolKey(ExtDeleteNotFoundOK) }

// Values reads x-tfpfgen-values.
func (e Extensions) Values() (bool, bool) { return e.boolKey(ExtValues) }

// Volatile reads x-tfpfgen-volatile.
func (e Extensions) Volatile() (bool, bool) { return e.boolKey(ExtVolatile) }

// ServerForced reads x-tfpfgen-server-forced.
func (e Extensions) ServerForced() (bool, bool) { return e.boolKey(ExtServerForced) }

// ServerDefault reads x-tfpfgen-server-default: the value the API stores for
// this property when the request omits it. Present means the attribute is
// Optional and Computed.
func (e Extensions) ServerDefault() (any, bool) {
	v, ok := e[ExtServerDefault]
	return v, ok
}

// IgnoredOnUpdate reads x-tfpfgen-ignored-on-update.
func (e Extensions) IgnoredOnUpdate() (bool, bool) {
	return e.boolKey(ExtIgnoredOnUpdate)
}

func (e Extensions) boolKey(key string) (bool, bool) {
	b, ok := e[key].(bool)
	return b, ok
}

// levenshtein is the standard two-row edit distance, for typo suggestions.
func levenshtein(a, b string) int {
	previous := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(previous[j]+1, min(curr[j-1]+1, previous[j-1]+cost))
		}
		previous, curr = curr, previous
	}
	return previous[len(b)]
}
