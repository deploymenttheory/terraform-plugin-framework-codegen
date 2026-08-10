// Package yamlwalk holds the YAML node-tree helpers every stage that
// rewrites an OpenAPI document shares: correction application and the SDK
// pre-normalization both work on the yaml.Node tree rather than a decoded
// map, because document order is load-bearing downstream — SDK generators
// resolve naming collisions between structurally identical inline schemas by
// encounter order, so a rewritten copy must differ from its input by exactly
// the rewritten nodes and nothing else.
package yamlwalk

import "gopkg.in/yaml.v3"

// ChildValue returns the value node of a mapping entry, or nil.
func ChildValue(node *yaml.Node, key string) *yaml.Node {
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

// StripSchemaDefaults removes the `default` key from every schema in the
// document — the named schemas under components, and every inline `schema`
// under paths — and reports how many it removed. It walks the schema grammar
// rather than matching keys blindly, because "default" is also a legitimate
// property *name* and a legitimate key inside an example, both of which must
// survive.
//
// Exists because a generated model constructor that stamps every
// spec-declared default onto itself leaks unwired fields into every request
// body, and on responses the default masks absence: the getter answers the
// default where the wire said nothing. A wire-faithful provider needs
// neither, in either direction. Zero hits is a fine answer — a document
// without defaults needs no stripping.
func StripSchemaDefaults(top *yaml.Node) int {
	stripped := 0

	if schemas := ChildValue(ChildValue(top, "components"), "schemas"); schemas != nil {
		for i := 1; i < len(schemas.Content); i += 2 {
			stripFromSchema(schemas.Content[i], &stripped)
		}
	}
	if paths := ChildValue(top, "paths"); paths != nil {
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

// ForceBlockStyle clears the flow-style flag from every collection node
// before the document is written back out.
//
// A JSON document is valid YAML in flow style, so a parse-then-emit round
// trip faithfully reproduces it as flow — which for a large specification
// means one line several million characters long. A 7 MB JSON-published
// document once emerged as a single line; the SDK generator read it,
// reported success, and generated nothing at all. Block style is what every
// YAML consumer expects; this changes formatting only, never the tree.
func ForceBlockStyle(n *yaml.Node) {
	if n == nil {
		return
	}
	// Quoting styles on scalars are meaningful — a quoted "true" is a string
	// and a bare one is not — so only the flow flags on collections go.
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode || n.Kind == yaml.DocumentNode {
		n.Style &^= yaml.FlowStyle
	}
	for _, c := range n.Content {
		ForceBlockStyle(c)
	}
}
