package sdkgen

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/yamlwalk"
)

// Prenormalize applies the document rewrites every SDK generation needs,
// whatever the API and whichever the backend: they answer generator
// behaviour, not any one document's mistakes, so they are built in rather
// than committed as corrections. It runs over a copy — the revised document
// on disk never changes — and it is deterministic by construction: the same
// input always yields the same bytes.
//
// Four passes, then block style. Schema defaults are stripped because a
// generated model constructor stamps every spec-declared default onto the
// model it builds, so a defaulted field the provider never wires leaks into
// every request body — and response-side, a constructor default masks field
// absence. A single-member anonymous allOf is collapsed into its parent
// because generators synthesize names for anonymous schemas and dedupe
// structurally identical ones with an unstable canonical winner — roughly
// one generation in five picks a different name, which makes byte-identical
// regeneration a coin flip. An array of format: byte strings is widened to
// plain strings because kiota generates a collection-of-byte-arrays writer
// its own runtime does not implement, so the generated SDK does not compile;
// the wire carries base64 text either way, so nothing about any request or
// response changes. A union is reduced to its first branch because Go has no
// type that is either of two shapes: a generator asked to merge incompatible
// branches emits a model with no properties at all, and kiota then declares
// that model's interface as embedding IAdditionalDataHolder without importing
// it, so the SDK does not compile. A large real document carries hundreds of
// such sites, and one of them is enough to break the whole build.
//
// The reduction costs the provider nothing, because it never reaches the
// provider: Prenormalize rewrites a copy handed to the backend, while
// derivation reads spec/revised.yaml itself and refuses a union outright
// ("oneOf/anyOf union: no single attribute type describes it"). An attribute
// the reduction narrows was already omitted from every generated schema.
//
// Every pass accepts zero hits: a document without the shapes needs no
// rewriting, which is not an error.
func Prenormalize(revised []byte) (out []byte, stripped, collapsed int, err error) {
	var root yaml.Node
	if err := yaml.Unmarshal(revised, &root); err != nil {
		return nil, 0, 0, fmt.Errorf("the revised document is not usable YAML: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, 0, 0, fmt.Errorf("the revised document is not a single YAML document")
	}

	top := root.Content[0]
	stripped = yamlwalk.StripSchemaDefaults(top)
	collapsed = collapseAnonymousAllOfs(top)
	widenByteArrayCollections(top)
	reduceUnions(top)

	yamlwalk.ForceBlockStyle(&root)

	out, err = yaml.Marshal(&root)
	if err != nil {
		return nil, 0, 0, err
	}
	return out, stripped, collapsed, nil
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
func widenByteArrayCollections(top *yaml.Node) {
	if schemas := yamlwalk.ChildValue(yamlwalk.ChildValue(top, "components"), "schemas"); schemas != nil {
		for i := 1; i < len(schemas.Content); i += 2 {
			widenByteArrays(schemas.Content[i])
		}
	}
	if paths := yamlwalk.ChildValue(top, "paths"); paths != nil {
		widenByteArrays(paths)
	}
}

// widenByteArrays walks a node, removing `format: byte` from any schema that
// is the `items` of an array. A single format: byte string is left alone —
// that shape generates a writer the runtimes do implement.
func widenByteArrays(node *yaml.Node) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			if key == "items" {
				dropByteFormat(value)
			}
			widenByteArrays(value)
		}
	case yaml.SequenceNode:
		for _, member := range node.Content {
			widenByteArrays(member)
		}
	}
}

// dropByteFormat removes `format: byte` from one schema node.
func dropByteFormat(schema *yaml.Node) {
	if schema == nil || schema.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(schema.Content); i += 2 {
		if schema.Content[i].Value == "format" && schema.Content[i+1].Value == "byte" {
			schema.Content = append(schema.Content[:i], schema.Content[i+2:]...)
			return
		}
	}
}

// reduceUnions rewrites every anyOf and oneOf in the document to its first
// branch, wherever one appears: a component schema, an inline property, a
// request body, a response. The whole document is walked rather than the
// component schemas alone, because the shape that broke the build was inline
// on a response.
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
func FilterPaths(doc []byte, include, exclude []string) ([]byte, error) {
	if len(include) == 0 && len(exclude) == 0 {
		return doc, nil
	}

	var root yaml.Node
	if err := yaml.Unmarshal(doc, &root); err != nil {
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
