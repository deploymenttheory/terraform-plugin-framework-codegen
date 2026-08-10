// Package correction applies committed corrections to an imported OpenAPI
// document before anything is generated from it.
//
// The imported document is immutable evidence of what the vendor published,
// and it stays that way. But a published specification is sometimes provably
// wrong about the API it describes — an audit observation shows the live API
// accepting and echoing an enum value the document omits — and a generator
// that trusts the document then builds an SDK that cannot express reality.
//
// A correction is the durable record of one such divergence: a JSON file
// holding a justification (what proves the document wrong), an optional
// evidence pointer into the committed audit observations, and RFC 6902
// operations scoped to what the evidence supports. Corrections apply to a
// copy — the imported document's bytes and hash never change — and
// generation reads the revised copy. When the vendor fixes the document, an
// add finding its value already present refuses with "stale", which is the
// prompt to delete the correction rather than let it rot.
//
// Application works on the YAML node tree, not a decoded map, because
// document order is load-bearing downstream: SDK generators resolve naming
// collisions between structurally identical inline schemas by encounter
// order, so a revised copy must differ from the imported document by exactly
// the corrected nodes and nothing else.
package correction

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Suffix is what names a correction file inside a corrections directory.
const Suffix = ".correction.json"

// DirName is the conventional corrections directory beside an imported
// document, e.g. spec/corrections/.
const DirName = "corrections"

// Operation is one RFC 6902 operation. Value is required for add, replace
// and test; forbidden for remove.
type Operation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// Correction is one committed correction file.
type Correction struct {
	// Justification says what proves the published document wrong. Required —
	// an unjustified correction is indistinguishable from wishful thinking.
	Justification string `json:"justification"`

	// Evidence points into the committed audit observations that prove the
	// justification, e.g. "audit/observations/tag.observations.json#a1b2c3".
	// Optional: a hand-authored correction may rest on a vendor statement the
	// justification cites instead.
	Evidence string `json:"evidence,omitempty"`

	Operations []Operation `json:"operations"`

	// File is the path the correction was loaded from, for error messages.
	File string `json:"-"`
}

// Load reads every *.correction.json in dir, sorted by name so application
// order is stable. A missing directory is simply no corrections.
func Load(dir string) ([]Correction, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var corrections []Correction
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Suffix) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path) //nolint:gosec // enumerated from the operator-supplied dir
		if err != nil {
			return nil, err
		}
		var c Correction
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("%s is not a usable correction: %w", path, err)
		}
		if c.Justification == "" {
			return nil, fmt.Errorf("%s has no justification; a correction must say what "+
				"(an audit observation, a vendor statement) proves the document wrong", path)
		}
		if len(c.Operations) == 0 {
			return nil, fmt.Errorf("%s declares no operations", path)
		}
		c.File = path
		corrections = append(corrections, c)
	}

	sort.Slice(corrections, func(i, j int) bool { return corrections[i].File < corrections[j].File })
	return corrections, nil
}

// Apply applies every correction in order to the YAML document and
// re-encodes it, preserving document order and node styles so the revised
// copy differs from the imported document by exactly the corrected nodes.
func Apply(specYAML []byte, corrections []Correction) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(specYAML, &root); err != nil {
		return nil, fmt.Errorf("the imported document is not usable YAML: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, fmt.Errorf("the imported document is not a single YAML document")
	}

	for _, c := range corrections {
		for i, op := range c.Operations {
			if err := apply(root.Content[0], op); err != nil {
				return nil, fmt.Errorf("%s operation %d (%s %s): %w", c.File, i+1, op.Op, op.Path, err)
			}
		}
	}

	forceBlockStyle(&root)

	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// apply performs one operation against the document's top node.
func apply(top *yaml.Node, op Operation) error {
	switch op.Op {
	case "strip-schema-defaults":
		// Not expressible in RFC 6902: every schema in the document loses its
		// `default`, wherever schemas nest. Whole-document by definition.
		return stripSchemaDefaults(top)
	case "add", "replace", "remove", "test":
	default:
		return fmt.Errorf("unsupported op %q (add, replace, remove, test and strip-schema-defaults exist)", op.Op)
	}

	tokens, err := pointerTokens(op.Path)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return fmt.Errorf("the whole-document path %q is not correctable", op.Path)
	}

	parent, err := descend(top, tokens[:len(tokens)-1])
	if err != nil {
		return err
	}
	last := tokens[len(tokens)-1]

	switch parent.Kind {
	case yaml.MappingNode:
		return applyToMapping(parent, last, op)
	case yaml.SequenceNode:
		return applyToSequence(parent, last, op)
	default:
		return fmt.Errorf("the parent of %q is neither an object nor an array", last)
	}
}

func applyToMapping(node *yaml.Node, key string, op Operation) error {
	at := -1
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			at = i
			break
		}
	}

	switch op.Op {
	case "add", "replace":
		if op.Op == "replace" && at < 0 {
			return fmt.Errorf("nothing exists at %q to replace", key)
		}
		if op.Op == "add" && at >= 0 && nodeEqual(node.Content[at+1], op.Value) {
			return fmt.Errorf("the document already contains this value; the correction is stale — " +
				"the vendor has fixed the specification, so delete the correction")
		}
		value, err := encode(op.Value)
		if err != nil {
			return err
		}
		if at >= 0 {
			node.Content[at+1] = value
			return nil
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
	case "remove":
		if at < 0 {
			return fmt.Errorf("nothing exists at %q to remove", key)
		}
		node.Content = append(node.Content[:at], node.Content[at+2:]...)
	case "test":
		if at < 0 || !nodeEqual(node.Content[at+1], op.Value) {
			return fmt.Errorf("test failed: %q does not hold the expected value", key)
		}
	}
	return nil
}

func applyToSequence(node *yaml.Node, token string, op Operation) error {
	if token == "-" {
		if op.Op != "add" {
			return fmt.Errorf("only add can address the end of an array")
		}
		for _, v := range node.Content {
			if nodeEqual(v, op.Value) {
				return fmt.Errorf("the array already contains %v; the correction is stale — "+
					"the vendor has fixed the specification, so delete the correction", op.Value)
			}
		}
		value, err := encode(op.Value)
		if err != nil {
			return err
		}
		node.Content = append(node.Content, value)
		return nil
	}

	idx, err := strconv.Atoi(token)
	if err != nil || idx < 0 || idx >= len(node.Content) {
		return fmt.Errorf("%q is not an index inside an array of %d", token, len(node.Content))
	}
	switch op.Op {
	case "add", "replace":
		value, encErr := encode(op.Value)
		if encErr != nil {
			return encErr
		}
		if op.Op == "replace" {
			node.Content[idx] = value
			return nil
		}
		node.Content = append(node.Content, nil)
		copy(node.Content[idx+1:], node.Content[idx:])
		node.Content[idx] = value
	case "remove":
		node.Content = append(node.Content[:idx], node.Content[idx+1:]...)
	case "test":
		if !nodeEqual(node.Content[idx], op.Value) {
			return fmt.Errorf("test failed: index %d does not hold the expected value", idx)
		}
	}
	return nil
}

// stripSchemaDefaults removes the `default` key from every schema in the
// document: the named schemas under components, and every inline `schema`
// under paths. It walks the schema grammar rather than matching keys
// blindly, because "default" is also a legitimate property *name* and a
// legitimate key inside an example — both of which must survive.
//
// Exists because a generated model constructor that stamps every
// spec-declared default onto itself leaks unwired fields into every request
// body, and on responses the default masks absence: the getter answers the
// default where the wire said nothing. A wire-faithful provider needs
// neither, in either direction.
func stripSchemaDefaults(top *yaml.Node) error {
	if stripAllDefaults(top) == 0 {
		return fmt.Errorf("the document declares no schema defaults; the correction is stale — delete it")
	}
	return nil
}

// stripAllDefaults is the lenient core of strip-schema-defaults: zero hits
// is a fine answer when it runs as a built-in normalisation rather than as a
// correction somebody committed.
func stripAllDefaults(top *yaml.Node) int {
	stripped := 0

	if schemas := childValue(childValue(top, "components"), "schemas"); schemas != nil {
		for i := 1; i < len(schemas.Content); i += 2 {
			stripFromSchema(schemas.Content[i], &stripped)
		}
	}
	if paths := childValue(top, "paths"); paths != nil {
		stripUnderPaths(paths, &stripped)
	}

	return stripped
}

// stripFromSchema removes `default` from one schema node and recurses into
// the positions of its grammar that hold further schemas.
func stripFromSchema(schema *yaml.Node, stripped *int) {
	if schema == nil || schema.Kind != yaml.MappingNode {
		return
	}

	for i := 0; i+1 < len(schema.Content); i += 2 {
		if schema.Content[i].Value == "default" {
			schema.Content = append(schema.Content[:i], schema.Content[i+2:]...)
			*stripped++
			break
		}
	}

	for i := 0; i+1 < len(schema.Content); i += 2 {
		key, value := schema.Content[i].Value, schema.Content[i+1]
		switch key {
		case "properties", "patternProperties":
			// A map of property NAME to schema: the names are data (one may
			// literally be "default"), the values are schemas.
			for j := 1; j < len(value.Content); j += 2 {
				stripFromSchema(value.Content[j], stripped)
			}
		case "items", "additionalProperties", "not":
			stripFromSchema(value, stripped)
		case "allOf", "anyOf", "oneOf":
			for _, member := range value.Content {
				stripFromSchema(member, stripped)
			}
		}
	}
}

// stripUnderPaths finds every `schema` key beneath paths — request bodies,
// responses, parameters — and strips its value as a schema. Examples are
// data, not schemas, and are not entered.
func stripUnderPaths(node *yaml.Node, stripped *int) {
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			switch key {
			case "schema":
				stripFromSchema(value, stripped)
			case "example", "examples":
				continue
			default:
				stripUnderPaths(value, stripped)
			}
		}
	case yaml.SequenceNode:
		for _, member := range node.Content {
			stripUnderPaths(member, stripped)
		}
	}
}

// childValue returns the value node of a mapping entry, or nil.
func childValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// descend walks the node tree to the node a pointer prefix names.
func descend(node *yaml.Node, tokens []string) (*yaml.Node, error) {
	for _, t := range tokens {
		switch node.Kind {
		case yaml.MappingNode:
			found := false
			for i := 0; i+1 < len(node.Content); i += 2 {
				if node.Content[i].Value == t {
					node = node.Content[i+1]
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("nothing exists at %q", t)
			}
		case yaml.SequenceNode:
			idx, err := strconv.Atoi(t)
			if err != nil || idx < 0 || idx >= len(node.Content) {
				return nil, fmt.Errorf("%q is not an index inside an array of %d", t, len(node.Content))
			}
			node = node.Content[idx]
		default:
			return nil, fmt.Errorf("%q names a child of a scalar, which has none", t)
		}
	}
	return node, nil
}

// encode turns a correction value into a node, styled the way the rest of
// the document reads.
func encode(v any) (*yaml.Node, error) {
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	return n, nil
}

// nodeEqual compares a node's decoded value with a correction value through
// a JSON normalisation, because the correction arrived as JSON (numbers are
// float64) and the document as YAML (numbers are int where they look like
// one).
func nodeEqual(n *yaml.Node, v any) bool {
	var got any
	if err := n.Decode(&got); err != nil {
		return false
	}
	return reflect.DeepEqual(jsonNormal(got), jsonNormal(v))
}

func jsonNormal(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if json.Unmarshal(data, &out) != nil {
		return v
	}
	return out
}

// pointerTokens splits an RFC 6901 JSON pointer into unescaped tokens.
func pointerTokens(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("the path %q is not a JSON pointer (it must start with /)", pointer)
	}
	parts := strings.Split(pointer[1:], "/")
	for i, p := range parts {
		p = strings.ReplaceAll(p, "~1", "/")
		p = strings.ReplaceAll(p, "~0", "~")
		parts[i] = p
	}
	return parts, nil
}

// forceBlockStyle clears the flow-style flag from every collection node
// before the document is written back out.
//
// A JSON document is valid YAML in flow style, so a parse-then-emit round
// trip faithfully reproduces it as flow — which for a large specification
// means one line several million characters long. A 7 MB JSON-published
// document emerged as a single line in v1; the SDK generator read it,
// reported success, and generated nothing at all. Block style is what every
// YAML consumer expects; this changes formatting only, never the tree.
func forceBlockStyle(n *yaml.Node) {
	if n == nil {
		return
	}
	// Quoting styles on scalars are meaningful — a quoted "true" is a string
	// and a bare one is not — so only the flow flags on collections go.
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode || n.Kind == yaml.DocumentNode {
		n.Style &^= yaml.FlowStyle
	}
	for _, c := range n.Content {
		forceBlockStyle(c)
	}
}
