package sdkgen

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/yamlwalk"
)

// extractRequestBodyEnums moves every inline enum schema declared under a
// request body to a named component schema, leaving a $ref where it stood,
// and records each path it rewrote under.
//
// kiota, given two operations whose bodies declare same-named inline enums,
// drops the property from every request model: the enum types themselves
// are still emitted, orphaned in the operations' packages, and no warning
// is printed. A named component generates one shared type that every
// referencing body carries, so the property survives.
//
// The minted name is the operation joined to the property path: operationId
// "teams/create" and property "privacy" name TeamsCreatePrivacy, and a
// property reached through another names every property on the way down.
// A document that declares no operationId names the operation by its path
// segments and method instead. An enum not reached through any property —
// a body that is itself an enum — is left alone: it has no property name
// to be named by, and a request model has no property to lose.
func extractRequestBodyEnums(top *yaml.Node, into *Rewrite) {
	eachPathItem(top, func(site string, item *yaml.Node) {
		if item.Kind != yaml.MappingNode {
			return
		}
		path := strings.TrimPrefix(site, "paths.")
		count := 0
		for _, method := range httpMethods {
			operation := yamlwalk.ChildValue(item, method)
			if operation == nil || operation.Kind != yaml.MappingNode {
				continue
			}
			content := yamlwalk.ChildValue(yamlwalk.ChildValue(operation, "requestBody"), "content")
			if content == nil || content.Kind != yaml.MappingNode {
				continue
			}
			prefix := operationName(operation, method, path)
			for i := 0; i+1 < len(content.Content); i += 2 {
				schema := yamlwalk.ChildValue(content.Content[i+1], "schema")
				count += liftInlineEnums(top, schema, prefix, "")
			}
		}
		into.record(site, count)
	})
}

// liftInlineEnums walks one request-body schema, accumulating the pascal-cased
// property names passed through in trail, and lifts every enum schema
// reached through at least one property. A $ref schema is left whole: its
// target is a component already, which is the shape being aimed for.
func liftInlineEnums(top, schema *yaml.Node, prefix, trail string) int {
	if schema == nil || schema.Kind != yaml.MappingNode {
		return 0
	}
	if yamlwalk.ChildValue(schema, "$ref") != nil {
		return 0
	}
	if trail != "" && yamlwalk.ChildValue(schema, "enum") != nil {
		liftEnum(top, schema, prefix+trail)
		return 1
	}
	count := 0
	for i := 0; i+1 < len(schema.Content); i += 2 {
		key, value := schema.Content[i].Value, schema.Content[i+1]
		switch key {
		case "properties":
			if value.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(value.Content); j += 2 {
				count += liftInlineEnums(top, value.Content[j+1], prefix, trail+pascalWords(value.Content[j].Value))
			}
		case "items", "additionalProperties":
			count += liftInlineEnums(top, value, prefix, trail)
		case "allOf", "anyOf", "oneOf":
			if value.Kind != yaml.SequenceNode {
				continue
			}
			for _, member := range value.Content {
				count += liftInlineEnums(top, member, prefix, trail)
			}
		}
	}
	return count
}

// liftEnum moves one enum schema under components.schemas and rewrites the
// node it came from into a $ref to it.
func liftEnum(top, schema *yaml.Node, name string) {
	component := &yaml.Node{Kind: schema.Kind, Tag: schema.Tag, Content: schema.Content}
	placed := placeComponentSchema(top, name, component)
	schema.Content = []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "$ref"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "#/components/schemas/" + placed},
	}
}

// placeComponentSchema adds one schema under components.schemas and answers
// the name it was placed under. A taken name whose schema is byte-identical
// is reused — the same enum lifted from a second body needs one component,
// not a copy per site — and a taken name whose schema differs is suffixed
// deterministically until a free or identical name is found.
func placeComponentSchema(top *yaml.Node, name string, schema *yaml.Node) string {
	schemas := componentSchemasCreating(top)
	candidate := name
	for suffix := 1; ; suffix++ {
		existing := yamlwalk.ChildValue(schemas, candidate)
		if existing == nil {
			schemas.Content = append(schemas.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: candidate}, schema)
			return candidate
		}
		if sameYAML(existing, schema) {
			return candidate
		}
		candidate = name + "Enum"
		if suffix > 1 {
			candidate = fmt.Sprintf("%sEnum%d", name, suffix)
		}
	}
}

// componentSchemasCreating answers the components.schemas mapping, creating
// either level if the document lacks it.
func componentSchemasCreating(top *yaml.Node) *yaml.Node {
	components := yamlwalk.ChildValue(top, "components")
	if components == nil {
		components = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		top.Content = append(top.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "components"}, components)
	}
	schemas := yamlwalk.ChildValue(components, "schemas")
	if schemas == nil {
		schemas = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		components.Content = append(components.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "schemas"}, schemas)
	}
	return schemas
}

// sameYAML reports whether two nodes marshal to the same bytes, which is
// the document's own idea of identity.
func sameYAML(a, b *yaml.Node) bool {
	ab, aerr := yaml.Marshal(a)
	bb, berr := yaml.Marshal(b)
	return aerr == nil && berr == nil && bytes.Equal(ab, bb)
}

// operationName names one operation for minting: the operationId
// pascal-cased on its separators, or the path's fixed segments followed by
// the method when the document declares no operationId. Parameter segments
// carry a name the neighbouring fixed segment already gives, so they are
// left out.
func operationName(operation *yaml.Node, method, path string) string {
	if id := yamlwalk.ChildValue(operation, "operationId"); id != nil && id.Value != "" {
		return pascalWords(id.Value)
	}
	var b strings.Builder
	for segment := range strings.SplitSeq(path, "/") {
		if segment == "" || strings.HasPrefix(segment, "{") {
			continue
		}
		b.WriteString(pascalWords(segment))
	}
	b.WriteString(pascalWords(method))
	return b.String()
}

// pascalWords upper-cases the first letter of every word in a name, where a
// word begins at the start or after any character that is neither a letter
// nor a digit. Interior capitals are kept, so a camelCased property keeps
// its own humps.
func pascalWords(name string) string {
	var b strings.Builder
	startOfWord := true
	for _, r := range name {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			startOfWord = true
		case startOfWord:
			b.WriteRune(unicode.ToUpper(r))
			startOfWord = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
