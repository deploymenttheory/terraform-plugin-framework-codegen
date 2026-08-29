package sdkgen

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/yamlwalk"
)

// Prenormalise applies the document rewrites every SDK generation needs,
// whatever the API and whichever the backend. Each answers a standing
// generator behaviour rather than one document's mistake, so they are built
// in rather than committed as corrections. It runs over a copy — the revised
// document on disk never changes — and the same input always yields the same
// bytes.
//
// Five passes, then block style.
//
// Schema defaults are stripped: a generated model constructor stamps every
// declared default onto the model it builds, so a defaulted field the
// provider never wires leaks into every request body, and response-side a
// constructor default masks field absence.
//
// A single-member anonymous allOf is collapsed into its parent: generators
// synthesize names for anonymous schemas and dedupe structurally identical
// ones with an unstable winner, so the name a regeneration picks is not a
// function of the document.
//
// An array of format: byte strings is widened to plain strings: kiota
// generates a collection-of-byte-arrays writer its own runtime does not
// implement, so the SDK does not compile. The wire carries base64 text
// either way.
//
// A union is reduced to its first branch: Go has no type that is either of
// two shapes, and kiota asked to merge incompatible branches emits a model
// with no properties, then declares that model's interface as embedding
// IAdditionalDataHolder without importing it. The reduction costs the
// provider nothing — derivation reads spec/revised.yaml itself and refuses a
// union outright ("oneOf/anyOf union: no single attribute type describes
// it"), so an attribute it narrows was already absent from every generated
// schema.
//
// An operation whose success responses declare no media type has the content
// of its error responses dropped: kiota builds a request's Accept header
// from the media types its responses declare and reaches the error responses
// when no success response offers one, which asks a server for the single
// representation it produces only when refusing.
//
// Every pass accepts zero hits: a document without the shapes needs no
// rewriting, which is not an error.
func Prenormalise(revised []byte) (out []byte, rewrites Rewrites, err error) {
	var root yaml.Node
	if err := yaml.Unmarshal(revised, &root); err != nil {
		return nil, Rewrites{}, fmt.Errorf("the revised document is not usable YAML: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, Rewrites{}, fmt.Errorf("the revised document is not a single YAML document")
	}

	top := root.Content[0]
	rewrites.SchemaDefaultsStripped = yamlwalk.StripSchemaDefaults(top)
	rewrites.AnonymousAllOfsCollapsed = collapseAnonymousAllOfs(top)
	rewrites.ByteArrayCollectionsWidened = widenByteArrayCollections(top)
	rewrites.UnionsReduced = reduceUnions(top)
	rewrites.ErrorContentDropped = dropUnacceptableErrorContent(top)

	yamlwalk.ForceBlockStyle(&root)

	out, err = yaml.Marshal(&root)
	if err != nil {
		return nil, Rewrites{}, err
	}
	return out, rewrites, nil
}

// Rewrites counts what each of the five rewrites changed, one field per
// rewrite in the order Prenormalise applies them. Every count is reported
// because a rewrite that found nothing is a different fact from one that was
// never measured, and the pre-normalised document is never committed — these
// counts are the only account of what the backend was given.
type Rewrites struct {
	// SchemaDefaultsStripped is how many `default` keywords were removed.
	SchemaDefaultsStripped int
	// AnonymousAllOfsCollapsed is how many single-member anonymous allOf
	// compositions were folded into their one member.
	AnonymousAllOfsCollapsed int
	// ByteArrayCollectionsWidened is how many array item schemas lost
	// `format: byte`.
	ByteArrayCollectionsWidened int
	// UnionsReduced is how many anyOf or oneOf compositions were folded to
	// their first branch. Only a union whose branches are all inline is
	// reduced, so this counts the unions a generator could not model
	// rather than every union the document declares.
	UnionsReduced int
	// ErrorContentDropped is how many error responses were inlined without
	// their content.
	ErrorContentDropped int
}

// String renders every count in the order the rewrites are applied, so the
// line reads as the sequence the document went through.
func (r Rewrites) String() string {
	return fmt.Sprintf(
		"%d schema defaults stripped, %d anonymous allOf collapsed, "+
			"%d byte-array collections widened, %d unions reduced, "+
			"%d error responses stripped of content",
		r.SchemaDefaultsStripped, r.AnonymousAllOfsCollapsed,
		r.ByteArrayCollectionsWidened, r.UnionsReduced, r.ErrorContentDropped)
}

// collapseAnonymousAllOfs hoists every single-member anonymous allOf in the
// document's component schemas.
func collapseAnonymousAllOfs(top *yaml.Node) int {
	collapsed := 0
	if schemas := yamlwalk.ChildValue(yamlwalk.ChildValue(top, "components"), "schemas"); schemas != nil {
		for i := 1; i < len(schemas.Content); i += 2 {
			collapsed += collapseAllOf(schemas.Content[i])
		}
	}
	return collapsed
}

// collapseAllOf merges a schema's allOf into the schema itself when the list
// has exactly one member, that member is inline rather than a $ref, and none
// of its keys are already declared on the parent. Anything else is left
// alone: a $ref member is a real composition, and an overlapping key would
// force a merge decision this pass must not guess.
func collapseAllOf(schema *yaml.Node) int {
	if schema == nil || schema.Kind != yaml.MappingNode {
		return 0
	}

	count := 0

	for i := 0; i+1 < len(schema.Content); i += 2 {
		if schema.Content[i].Value != "allOf" {
			continue
		}
		seq := schema.Content[i+1]
		if seq.Kind != yaml.SequenceNode || len(seq.Content) != 1 {
			break
		}
		member := seq.Content[0]
		if member.Kind != yaml.MappingNode || yamlwalk.ChildValue(member, "$ref") != nil {
			break
		}
		overlap := false
		for j := 0; j+1 < len(member.Content); j += 2 {
			if yamlwalk.ChildValue(schema, member.Content[j].Value) != nil {
				overlap = true
				break
			}
		}
		if overlap {
			break
		}
		schema.Content = append(schema.Content[:i], schema.Content[i+2:]...)
		schema.Content = append(schema.Content, member.Content...)
		count++
		break
	}

	// The same shape nests: an inline property schema carries its own allOf,
	// and the generator synthesizes a model for it all the same.
	for i := 0; i+1 < len(schema.Content); i += 2 {
		key, value := schema.Content[i].Value, schema.Content[i+1]
		switch key {
		case "properties", "patternProperties":
			for j := 1; j < len(value.Content); j += 2 {
				count += collapseAllOf(value.Content[j])
			}
		case "items", "additionalProperties", "not":
			count += collapseAllOf(value)
		case "allOf", "anyOf", "oneOf":
			for _, m := range value.Content {
				count += collapseAllOf(m)
			}
		}
	}

	return count
}

// widenByteArrayCollections drops the byte format from an array's item
// schema, in both the component schemas and inline under paths.
func widenByteArrayCollections(top *yaml.Node) int {
	count := 0
	if schemas := yamlwalk.ChildValue(yamlwalk.ChildValue(top, "components"), "schemas"); schemas != nil {
		for i := 1; i < len(schemas.Content); i += 2 {
			count += widenByteArrays(schemas.Content[i])
		}
	}
	if paths := yamlwalk.ChildValue(top, "paths"); paths != nil {
		count += widenByteArrays(paths)
	}
	return count
}

// widenByteArrays walks a node, removing `format: byte` from any schema that
// is the `items` of an array. A single format: byte string is left alone —
// that shape generates a writer the runtimes do implement.
func widenByteArrays(node *yaml.Node) int {
	if node == nil {
		return 0
	}

	count := 0
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			if key == "items" && dropByteFormat(value) {
				count++
			}
			count += widenByteArrays(value)
		}
	case yaml.SequenceNode:
		for _, member := range node.Content {
			count += widenByteArrays(member)
		}
	}
	return count
}

// dropByteFormat removes `format: byte` from one schema node, and answers
// whether it found one to remove.
func dropByteFormat(schema *yaml.Node) bool {
	if schema == nil || schema.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(schema.Content); i += 2 {
		if schema.Content[i].Value == "format" && schema.Content[i+1].Value == "byte" {
			schema.Content = append(schema.Content[:i], schema.Content[i+2:]...)
			return true
		}
	}
	return false
}

// reduceUnions rewrites every anyOf and oneOf in the document to its first
// branch, wherever one appears: a component schema, an inline property, a
// request body, a response. The whole document is walked rather than the
// component schemas alone, because a union declared inline on a response is
// as unbuildable as one in components.
func reduceUnions(node *yaml.Node) int {
	if node == nil {
		return 0
	}

	count := 0
	switch node.Kind {
	case yaml.MappingNode:
		// Folding a branch in can expose another union the branch itself
		// declared, so keep folding until the schema states none.
		for folding := true; folding; {
			folding = false
			for _, keyword := range []string{"anyOf", "oneOf"} {
				if reduceUnion(node, keyword) {
					count++
					folding = true
				}
			}
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			count += reduceUnions(node.Content[i+1])
		}
	case yaml.SequenceNode:
		for _, member := range node.Content {
			count += reduceUnions(member)
		}
	}
	return count
}

// reduceUnion folds one schema's named union keyword into the schema itself,
// keeping the first branch. A key the schema already declares wins: the
// branches sit beside the parent's own keywords, and a branch may not
// overwrite what the document states directly. The discriminator goes with
// the union it selected between — left behind it would name a property no
// remaining branch varies on, which is what a generator chokes on next.
func reduceUnion(schema *yaml.Node, keyword string) bool {
	for i := 0; i+1 < len(schema.Content); i += 2 {
		if schema.Content[i].Value != keyword {
			continue
		}
		branches := schema.Content[i+1]
		if branches.Kind != yaml.SequenceNode || len(branches.Content) == 0 {
			return false
		}
		first := branches.Content[0]
		if first.Kind != yaml.MappingNode {
			return false
		}
		// A union whose branches are named schemas is polymorphism, and a
		// generator models it: the branches become types, and a
		// discriminator selects between them. Reducing one takes the
		// alternatives out from under a discriminator that still names
		// them, and kiota fails the whole document rather than the schema —
		// "the schema reference is not resolved", having already logged
		// that the discriminator is not inherited from what remains.
		//
		// The shape this pass exists for is the other one: a union of
		// inline schemas, which names nothing a discriminator could select
		// and which the generator merges into a model with no properties.
		for _, branch := range branches.Content {
			if branch.Kind != yaml.MappingNode || yamlwalk.ChildValue(branch, "$ref") != nil {
				return false
			}
		}

		schema.Content = append(schema.Content[:i:i], schema.Content[i+2:]...)
		removeKey(schema, "discriminator")

		for j := 0; j+1 < len(first.Content); j += 2 {
			if yamlwalk.ChildValue(schema, first.Content[j].Value) == nil {
				schema.Content = append(schema.Content, first.Content[j], first.Content[j+1])
			}
		}
		return true
	}
	return false
}

// removeKey deletes one key and its value from a mapping, if present.
func removeKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i:i], mapping.Content[i+2:]...)
			return
		}
	}
}

// httpMethods are the operation keys a path item may carry.
var httpMethods = []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"}

// dropUnacceptableErrorContent inlines an operation's error responses
// without their content when no success response declares any, and answers
// how many responses it rewrote.
//
// kiota builds a request's Accept header from the media types the operation's
// responses declare, and reaches the error responses when no success response
// offers one. An operation answering 204 therefore sends
// Accept: application/problem+json, and a server that produces that
// representation only when refusing answers 406.
//
// The response is inlined rather than the shared component stripped: one
// component answers many operations, and an operation whose success response
// does declare a media type still needs it. Only the description survives,
// which is the one member a Response Object must carry.
//
// The error mappings the content also generates go with it, and cost the
// provider nothing: the generated error handling reads a status code and a
// message, which kiota's untyped error carries as well as a typed one.
func dropUnacceptableErrorContent(top *yaml.Node) int {
	paths := yamlwalk.ChildValue(top, "paths")
	if paths == nil || paths.Kind != yaml.MappingNode {
		return 0
	}
	rewritten := 0
	for i := 1; i < len(paths.Content); i += 2 {
		item := paths.Content[i]
		if item.Kind != yaml.MappingNode {
			continue
		}
		for _, method := range httpMethods {
			operation := yamlwalk.ChildValue(item, method)
			if operation == nil || operation.Kind != yaml.MappingNode {
				continue
			}
			rewritten += dropErrorContent(top, operation)
		}
	}
	return rewritten
}

// dropErrorContent rewrites one operation's responses, and answers how many
// it rewrote. An operation whose success responses declare a media type is
// left alone: kiota builds its Accept from those and never reaches the
// errors.
func dropErrorContent(top *yaml.Node, operation *yaml.Node) int {
	responses := yamlwalk.ChildValue(operation, "responses")
	if responses == nil || responses.Kind != yaml.MappingNode {
		return 0
	}
	for i := 0; i+1 < len(responses.Content); i += 2 {
		if !strings.HasPrefix(responses.Content[i].Value, "2") {
			continue
		}
		if yamlwalk.ChildValue(resolveResponse(top, responses.Content[i+1]), "content") != nil {
			return 0
		}
	}
	rewritten := 0
	for i := 0; i+1 < len(responses.Content); i += 2 {
		node := responses.Content[i+1]
		resolved := resolveResponse(top, node)
		if resolved == nil || yamlwalk.ChildValue(resolved, "content") == nil {
			continue
		}
		describeOnly(node, resolved)
		rewritten++
	}
	return rewritten
}

// resolveResponse answers the Response Object a response node stands for,
// following one $ref into components. A reference the document does not
// declare answers nil, and the caller leaves such a node alone.
func resolveResponse(top *yaml.Node, node *yaml.Node) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	ref := yamlwalk.ChildValue(node, "$ref")
	if ref == nil {
		return node
	}
	name, ok := strings.CutPrefix(ref.Value, "#/components/responses/")
	if !ok {
		return nil
	}
	components := yamlwalk.ChildValue(top, "components")
	if components == nil {
		return nil
	}
	return yamlwalk.ChildValue(yamlwalk.ChildValue(components, "responses"), name)
}

// describeOnly replaces a response node in place with the one member a
// Response Object must carry, taking the description from whatever the node
// resolved to so a referenced response keeps its own words.
func describeOnly(node *yaml.Node, resolved *yaml.Node) {
	text := ""
	if described := yamlwalk.ChildValue(resolved, "description"); described != nil {
		text = described.Value
	}
	node.Kind = yaml.MappingNode
	node.Tag = "!!map"
	node.Value = ""
	node.Style = 0
	node.Content = []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "description"},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: text},
	}
}

// FilterPaths keeps only the operations sdk.include_paths/exclude_paths
// select, using the same glob dialect kiota's --include-path/--exclude-path
// flags speak (`*` within a segment, `**` across segments). kiota filters
// natively; the openapi-generator CLI has no path-glob flag, so its backend
// filters the document itself before invoking the tool — one config key, one
// meaning, whichever backend runs.
//
// An empty include list means everything; excludes are then removed. A
// filter that removes every path refuses, because a generator fed an empty
// paths object would report success and generate nothing.
func FilterPaths(document []byte, include, exclude []string) ([]byte, error) {
	if len(include) == 0 && len(exclude) == 0 {
		return document, nil
	}

	var root yaml.Node
	if err := yaml.Unmarshal(document, &root); err != nil {
		return nil, fmt.Errorf("the document is not usable YAML: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, fmt.Errorf("the document is not a single YAML document")
	}
	paths := yamlwalk.ChildValue(root.Content[0], "paths")
	if paths == nil || paths.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("the document declares no paths object to filter")
	}

	keep := pathKeeper(include, exclude)

	var kept []*yaml.Node
	for i := 0; i+1 < len(paths.Content); i += 2 {
		if keep(paths.Content[i].Value) {
			kept = append(kept, paths.Content[i], paths.Content[i+1])
		}
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf("sdk.include_paths/exclude_paths left no paths at all; a generator fed an empty document would succeed and generate nothing")
	}
	paths.Content = kept

	yamlwalk.ForceBlockStyle(&root)
	return yaml.Marshal(&root)
}

// pathKeeper compiles the globs once and answers per path.
func pathKeeper(include, exclude []string) func(string) bool {
	in := compileGlobs(include)
	out := compileGlobs(exclude)
	return func(path string) bool {
		kept := len(in) == 0
		for _, re := range in {
			if re.MatchString(path) {
				kept = true
				break
			}
		}
		if !kept {
			return false
		}
		for _, re := range out {
			if re.MatchString(path) {
				return false
			}
		}
		return true
	}
}

// compileGlobs turns path globs into anchored regexps: `**` crosses path
// segments, `*` stays within one.
func compileGlobs(globs []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, g := range globs {
		out = append(out, globToRegexp(g))
	}
	return out
}

// globToRegexp compiles one glob. Everything but `*` is quoted literal —
// a spec path's braces (`/widgets/{id}`) need no escaping by the operator,
// and a quoted pattern cannot fail to compile.
func globToRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString(`^`)
	for i := 0; i < len(glob); i++ {
		if glob[i] != '*' {
			b.WriteString(regexp.QuoteMeta(string(glob[i])))
			continue
		}
		if i+1 < len(glob) && glob[i+1] == '*' {
			b.WriteString(`.*`)
			i++
			continue
		}
		b.WriteString(`[^/]*`)
	}
	b.WriteString(`$`)
	return regexp.MustCompile(b.String())
}
