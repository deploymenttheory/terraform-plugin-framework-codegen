package revise

import (
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// locator navigates the revised document's YAML node tree by RFC 6901 JSON
// pointer, the same address form the compiled corrections carry. Compilation
// needs both halves at once — the node, to read what the document currently
// says, and the pointer, to write an operation the correction machinery can
// apply — so every walk here returns them together.
//
// Alias nodes are deliberately not dereferenced: correction application does
// not dereference them either, and a pointer that only resolves through an
// alias would compile into an operation that cannot apply.
type locator struct {
	top *yaml.Node
}

// escapeToken spells a pointer token per RFC 6901.
func escapeToken(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

// nodeAt walks a JSON pointer from the document's top mapping, nil when
// anything along the way is missing.
func (l *locator) nodeAt(pointer string) *yaml.Node {
	node := l.top
	if pointer == "" {
		return node
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil
	}
	for _, raw := range strings.Split(pointer[1:], "/") {
		tok := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch {
		case node == nil:
			return nil
		case node.Kind == yaml.MappingNode:
			node = mapValue(node, tok)
		case node.Kind == yaml.SequenceNode:
			index, err := strconv.Atoi(tok)
			if err != nil || index < 0 || index >= len(node.Content) {
				return nil
			}
			node = node.Content[index]
		default:
			return nil
		}
	}
	return node
}

// mapValue finds a mapping key's value node, nil when absent or when the
// node is not a mapping.
func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// opPointer addresses one operation object inside paths.
func opPointer(op *specmodel.OperationReference) string {
	return "/paths/" + escapeToken(op.Path) + "/" + strings.ToLower(op.Method)
}

// followSchemaRefs walks a $ref chain to the schema that actually carries
// fields, moving the pointer into components.schemas as it goes. A reference
// outside the local schema components, a dangling target, or a chain long
// enough to be a cycle all report not-found.
func (l *locator) followSchemaRefs(node *yaml.Node, pointer string) (*yaml.Node, string, bool) {
	for hops := 0; hops < 32; hops++ {
		ref := mapValue(node, "$ref")
		if ref == nil {
			return node, pointer, node != nil
		}
		name, ok := strings.CutPrefix(ref.Value, "#/components/schemas/")
		if !ok {
			return nil, "", false
		}
		pointer = "/components/schemas/" + escapeToken(name)
		node = l.nodeAt(pointer)
	}
	return nil, "", false
}

// requestSchema finds an operation's request body schema, following a
// component request-body reference and the schema's own $ref chain, choosing
// a media type by the same fixed rule the document model uses.
func (l *locator) requestSchema(op *specmodel.OperationReference) (*yaml.Node, string, bool) {
	if op == nil {
		return nil, "", false
	}
	pointer := opPointer(op) + "/requestBody"
	body := l.nodeAt(pointer)
	if ref := mapValue(body, "$ref"); ref != nil {
		name, ok := strings.CutPrefix(ref.Value, "#/components/requestBodies/")
		if !ok {
			return nil, "", false
		}
		pointer = "/components/requestBodies/" + escapeToken(name)
		body = l.nodeAt(pointer)
	}
	return l.contentSchema(body, pointer)
}

// responseSchema finds an operation's success response schema: the first 2xx
// declaring one, falling back to "default" — mirroring the document model's
// SuccessSchema so a correction lands where classification read.
func (l *locator) responseSchema(op *specmodel.OperationReference) (*yaml.Node, string, bool) {
	if op == nil {
		return nil, "", false
	}
	opPtr := opPointer(op)
	responses := l.nodeAt(opPtr + "/responses")
	if responses == nil || responses.Kind != yaml.MappingNode {
		return nil, "", false
	}

	var statuses []string
	for i := 0; i+1 < len(responses.Content); i += 2 {
		statuses = append(statuses, responses.Content[i].Value)
	}
	sort.Strings(statuses)

	hasDefault := false
	for _, status := range statuses {
		if status == "default" {
			hasDefault = true
			continue
		}
		if strings.HasPrefix(status, "2") {
			if node, pointer, ok := l.responseContent(responses, opPtr, status); ok {
				return node, pointer, true
			}
		}
	}
	if hasDefault {
		return l.responseContent(responses, opPtr, "default")
	}
	return nil, "", false
}

// responseContent resolves one response object — through a component
// response reference when that is what the document wrote — to its schema.
func (l *locator) responseContent(responses *yaml.Node, opPtr, status string) (*yaml.Node, string, bool) {
	node := mapValue(responses, status)
	pointer := opPtr + "/responses/" + escapeToken(status)
	if ref := mapValue(node, "$ref"); ref != nil {
		name, ok := strings.CutPrefix(ref.Value, "#/components/responses/")
		if !ok {
			return nil, "", false
		}
		pointer = "/components/responses/" + escapeToken(name)
		node = l.nodeAt(pointer)
	}
	return l.contentSchema(node, pointer)
}

// contentSchema picks a media type's schema out of a content mapping —
// JSON preferred, alphabetically first otherwise, the document model's rule
// — and follows the schema's $ref chain.
func (l *locator) contentSchema(node *yaml.Node, pointer string) (*yaml.Node, string, bool) {
	content := mapValue(node, "content")
	if content == nil || content.Kind != yaml.MappingNode {
		return nil, "", false
	}

	var mediaTypes []string
	for i := 0; i+1 < len(content.Content); i += 2 {
		mediaTypes = append(mediaTypes, content.Content[i].Value)
	}
	sort.Strings(mediaTypes)
	sort.SliceStable(mediaTypes, func(i, j int) bool {
		return isJSONMedia(mediaTypes[i]) && !isJSONMedia(mediaTypes[j])
	})

	for _, mt := range mediaTypes {
		if schema := mapValue(mapValue(content, mt), "schema"); schema != nil {
			return l.followSchemaRefs(schema, pointer+"/content/"+escapeToken(mt)+"/schema")
		}
	}
	return nil, "", false
}

// isJSONMedia matches application/json and its +json suffixed variants.
func isJSONMedia(mediaType string) bool {
	if mediaType == "application/json" {
		return true
	}
	if at := strings.LastIndex(mediaType, "+"); at >= 0 {
		return mediaType[at+1:] == "json"
	}
	return false
}

// propertyLocation is one property's location: the property node itself (where an
// x-tfpfgen-* annotation belongs — an annotation beside a $ref wins over the
// target's own) and the object schema declaring it (whose required list and
// resolved shape standard corrections address).
type propertyLocation struct {
	property           *yaml.Node
	propPtr            string
	declaration        *yaml.Node
	declarationPointer string
}

// findProperty locates a property's declaration inside a schema: its own
// properties first, then allOf branches in order, following $refs — the
// order the flattened downstream view reads them in.
func (l *locator) findProperty(node *yaml.Node, pointer, name string) (propertyLocation, bool) {
	return l.findPropertyGuarded(node, pointer, name, map[string]bool{})
}

func (l *locator) findPropertyGuarded(node *yaml.Node, pointer, name string, seen map[string]bool) (propertyLocation, bool) {
	node, pointer, ok := l.followSchemaRefs(node, pointer)
	if !ok || seen[pointer] {
		return propertyLocation{}, false
	}
	seen[pointer] = true

	if property := mapValue(mapValue(node, "properties"), name); property != nil {
		return propertyLocation{
			property:           property,
			propPtr:            pointer + "/properties/" + escapeToken(name),
			declaration:        node,
			declarationPointer: pointer,
		}, true
	}
	if allOf := mapValue(node, "allOf"); allOf != nil && allOf.Kind == yaml.SequenceNode {
		for i, branch := range allOf.Content {
			if site, ok := l.findPropertyGuarded(branch, pointer+"/allOf/"+strconv.Itoa(i), name, seen); ok {
				return site, true
			}
		}
	}
	return propertyLocation{}, false
}

// requiredHas reports whether the schema — across its $ref chain and allOf
// branches — already requires the named property.
func (l *locator) requiredHas(node *yaml.Node, pointer, name string) bool {
	return l.walkSchema(node, pointer, func(n *yaml.Node) bool {
		request := mapValue(n, "required")
		if request == nil || request.Kind != yaml.SequenceNode {
			return false
		}
		for _, entry := range request.Content {
			if entry.Value == name {
				return true
			}
		}
		return false
	})
}

// readOnlyDeclared reports whether the schema — across refs and allOf —
// already declares readOnly true.
func (l *locator) readOnlyDeclared(node *yaml.Node, pointer string) bool {
	return l.walkSchema(node, pointer, func(n *yaml.Node) bool {
		ro := mapValue(n, "readOnly")
		return ro != nil && ro.Value == "true"
	})
}

// extensionNode finds an x-tfpfgen-* key's value on a schema, the node it
// resolves to, or its allOf branches — the annotation's own site winning,
// matching how the flattened view merges extensions.
func (l *locator) extensionNode(node *yaml.Node, pointer, key string) *yaml.Node {
	var found *yaml.Node
	l.walkSchema(node, pointer, func(n *yaml.Node) bool {
		if v := mapValue(n, key); v != nil {
			found = v
			return true
		}
		return false
	})
	return found
}

// enumSite finds where a property's enum is declared, following $refs and
// allOf branches, returning the schema node holding the enum key.
func (l *locator) enumSite(node *yaml.Node, pointer string) (*yaml.Node, string, bool) {
	var siteNode *yaml.Node
	var sitePtr string
	l.walkSchemaWithPtr(node, pointer, map[string]bool{}, func(n *yaml.Node, p string) bool {
		if e := mapValue(n, "enum"); e != nil && e.Kind == yaml.SequenceNode {
			siteNode, sitePtr = n, p
			return true
		}
		return false
	})
	return siteNode, sitePtr, siteNode != nil
}

// walkSchema visits a schema, the target of its $ref chain, and every allOf
// branch, stopping when visit reports done. The annotation site — the node
// as written, before any $ref hop — is visited first.
func (l *locator) walkSchema(node *yaml.Node, pointer string, visit func(*yaml.Node) bool) bool {
	return l.walkSchemaWithPtr(node, pointer, map[string]bool{}, func(n *yaml.Node, _ string) bool {
		return visit(n)
	})
}

func (l *locator) walkSchemaWithPtr(node *yaml.Node, pointer string, seen map[string]bool, visit func(*yaml.Node, string) bool) bool {
	if node == nil || seen[pointer] {
		return false
	}
	seen[pointer] = true
	if visit(node, pointer) {
		return true
	}
	resolved, resolvedPtr, ok := l.followSchemaRefs(node, pointer)
	if ok && resolved != node {
		if l.walkSchemaWithPtr(resolved, resolvedPtr, seen, visit) {
			return true
		}
	}
	if allOf := mapValue(node, "allOf"); allOf != nil && allOf.Kind == yaml.SequenceNode {
		for i, branch := range allOf.Content {
			if l.walkSchemaWithPtr(branch, pointer+"/allOf/"+strconv.Itoa(i), seen, visit) {
				return true
			}
		}
	}
	return false
}
