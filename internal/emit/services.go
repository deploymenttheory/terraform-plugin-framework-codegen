package emit

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
	"go/format"
	"io/fs"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/code"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/fixtures"
	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/templates"
)

// ServiceFiles is everything per-entity emission produced: the files to
// write, and the registration lines `provider generate` registers into the
// provider-core registry files.
type ServiceFiles struct {
	Files         []File
	Registrations Registry
	// Excluded lists every entity emission refused, with the reason, in the
	// order the model declares them. It mirrors the model's own Excluded:
	// an entity that yields nothing is reported, never silently absent.
	Excluded []ir.UnsupportedEntity
	// KeptUnbound is every attribute the join kept with no SDK field behind
	// it — the id and the addressing attributes — keyed by keptUnboundKey.
	// Pruning reports removing each of them and each of them reaches the
	// schema anyway, so the refusal report reads this to leave them out.
	KeptUnbound map[string]bool
}

// unrenderableError marks an entity emission cannot serve — a path parameter
// naming nothing in the schema, a call the bindings cannot satisfy.
//
// Refuses one entity rather than the run: an unrenderable entity is a fact
// about that entity, and the rest still generate. Every other error still
// aborts, because an emitter that cannot render something it accepted is a
// bug and must say so.
type unrenderableError struct{ reason string }

func (e *unrenderableError) Error() string { return e.reason }

// unrenderable builds the error entity emission refuses one entity with.
func unrenderable(format string, args ...any) error {
	return &unrenderableError{reason: fmt.Sprintf(format, args...)}
}

// excludes reports whether an error refuses one entity, and the reason it
// gave, unwrapping whatever context the call stack added on the way up.
func excludes(err error) (string, bool) {
	var refusal *unrenderableError
	if errors.As(err, &refusal) {
		return refusal.reason, true
	}
	return "", false
}

// RenderServices renders every entity the model carries and the bindings
// can serve: the four service-kind file sets, their tests, mocks and
// fixtures, and the examples tree. The provider-core context supplies the
// module identity and the backend and auth presence booleans; entities the
// bindings lack (pruned whole) are skipped, because code emitted against a
// deleted binding would name SDK surface that does not exist.
//
// Rendered Go files are gofmt-clean as rendered: the assembled source is
// held to go/format inside the renderer, and a file that does not parse is
// reported against its template, never written.
func RenderServices(pc ProviderCore, m *ir.Model, b *sdkbind.Bindings) (*ServiceFiles, error) {
	if err := pc.check(); err != nil {
		return nil, err
	}
	if m == nil || b == nil {
		return nil, fmt.Errorf("entity emission needs both the intermediate representation and the bindings")
	}

	e := &serviceRenderer{pc: pc, bindings: b, resources: map[string]*ir.Resource{}}
	for i := range m.Resources {
		e.resources[m.Resources[i].Names.Key] = &m.Resources[i]
	}
	out := &ServiceFiles{}

	// The resources that reached the provider. A list resource is matched to
	// one by type name and cannot be registered without it, so this decides
	// which list resources may follow.
	served := map[string]bool{}

	// A list resource's results are identities, and the identity schema they
	// conform to belongs to the resource. Only a resource that is listed
	// declares one, so the resource loop has to know before it renders.
	listed := map[string]bool{}
	for i := range m.ListResources {
		listed[m.ListResources[i].Names.Key] = true
	}
	e.listed, e.identities = listed, map[string][]identityAttribute{}

	for i := range m.Resources {
		r := &m.Resources[i]
		rb := b.Resources[r.Names.Key]
		if rb == nil {
			continue
		}
		files, err := e.resource(r, rb)
		if err != nil {
			if reason, refused := excludes(err); refused {
				out.Excluded = append(out.Excluded, ir.UnsupportedEntity{Key: r.Names.Key, Reason: reason})
				continue
			}
			return nil, fmt.Errorf("resource %s: %w", r.Names.Key, err)
		}
		out.Files = append(out.Files, files...)
		out.Registrations.Resources.add(e.registration(kindResources, r.Names, "New"+r.Names.PascalCase+"Resource"))
		served[r.Names.Key] = true
	}

	for i := range m.Datasources {
		ds := &m.Datasources[i]
		db := b.Datasources[ds.Names.Key]
		if db == nil {
			continue
		}
		files, err := e.datasource(ds, db)
		if err != nil {
			if reason, refused := excludes(err); refused {
				out.Excluded = append(out.Excluded, ir.UnsupportedEntity{Key: ds.Names.Key, Reason: reason})
				continue
			}
			return nil, fmt.Errorf("datasource %s: %w", ds.Names.Key, err)
		}
		out.Files = append(out.Files, files...)
		out.Registrations.Datasources.add(e.registration(kindDatasources, ds.Names, "New"+ds.Names.PascalCase+"Datasource"))
	}

	for i := range m.ListResources {
		lr := &m.ListResources[i]
		lb := b.ListResources[lr.Names.Key]
		if lb == nil {
			continue
		}
		// Terraform refuses to load a provider that offers a list resource
		// with no managed resource of the same type name, and refuses the
		// whole provider rather than that one entity. A resource the
		// bindings or emission already refused therefore takes its list
		// resource with it.
		if !served[lr.Names.Key] {
			out.Excluded = append(out.Excluded, ir.UnsupportedEntity{
				Key:    lr.Names.Key,
				Reason: "list: the resource it lists is not served, and terraform refuses a provider whose list resource names no resource",
			})
			continue
		}
		files, err := e.listResource(lr, lb)
		if err != nil {
			if reason, refused := excludes(err); refused {
				out.Excluded = append(out.Excluded, ir.UnsupportedEntity{Key: lr.Names.Key, Reason: reason})
				continue
			}
			return nil, fmt.Errorf("list resource %s: %w", lr.Names.Key, err)
		}
		out.Files = append(out.Files, files...)
		out.Registrations.ListResources.add(e.registration(kindListResources, lr.Names, "New"+lr.Names.PascalCase+"ListResource"))
	}

	for i := range m.Actions {
		a := &m.Actions[i]
		ab := b.Actions[a.Names.Key]
		if ab == nil {
			continue
		}
		files, err := e.action(a, ab)
		if err != nil {
			if reason, refused := excludes(err); refused {
				out.Excluded = append(out.Excluded, ir.UnsupportedEntity{Key: a.Names.Key, Reason: reason})
				continue
			}
			return nil, fmt.Errorf("action %s: %w", a.Names.Key, err)
		}
		out.Files = append(out.Files, files...)
		out.Registrations.Actions.add(e.registration(kindActions, a.Names, "New"+a.Names.PascalCase+"Action"))
	}

	out.KeptUnbound = e.keptUnbound
	return out, nil
}

// Service kind directory names under internal/services/, and the matching
// sentinel kinds.
const (
	kindResources     = "resources"
	kindDatasources   = "datasources"
	kindListResources = "list-resources"
	kindActions       = "actions"
)

// serviceRenderer carries what every per-entity render needs.
type serviceRenderer struct {
	pc       ProviderCore
	bindings *sdkbind.Bindings
	// keptUnbound is every attribute the join kept with no SDK field behind
	// it, keyed by keptUnboundKey. Pruning reported removing each of these,
	// and each of them is in the emitted schema regardless, so the refusal
	// report reads this to tell a removal that cost something from one that
	// cost nothing.
	keptUnbound map[string]bool
	// listed names the resources a list resource lists, so only those
	// declare an identity schema.
	listed map[string]bool
	// identities is each listed resource's identity, recorded as the
	// resource renders so its list resource emits results in the same shape.
	identities map[string][]identityAttribute
	// resources is every resource of the model by entity key, so a child's
	// fixture can carry the block of the parent it addresses.
	resources map[string]*ir.Resource
	// parentTypes is the terraform type of each ancestor block the fixture
	// being rendered carries, gathered as the blocks are, nearest first.
	parentTypes []string
}

// Binding kinds, spelled the way sdkbind.Removal spells them so a removal
// and a kept attribute can be matched against each other.
const (
	bindingKindResource     = "resource"
	bindingKindDatasource   = "datasource"
	bindingKindListResource = string(specmodel.KindListResource)
	bindingKindAction       = "action"
)

// keptUnboundKey addresses one attribute of one entity. The NUL separator
// cannot occur in a kind, a key or an attribute name, so no two different
// triples can collide on one key.
func keptUnboundKey(kind, key, attribute string) string {
	return kind + "\x00" + key + "\x00" + attribute
}

// recordKept notes the attributes one join kept with no binding behind them.
func (e *serviceRenderer) recordKept(kind, key string, kept []string) {
	if len(kept) == 0 {
		return
	}
	if e.keptUnbound == nil {
		e.keptUnbound = map[string]bool{}
	}
	for _, attribute := range kept {
		e.keptUnbound[keptUnboundKey(kind, key, attribute)] = true
	}
}

// joinTree joins one entity's tree and records whatever it kept unbound
// against that entity, so the refusal report can tell the two kinds of
// removal apart.
func (e *serviceRenderer) joinTree(kind, key string, tree *ir.AttributeTree, fbs []sdkbind.FieldBinding, addressing ...map[string]bool) []node {
	nodes, kept := joinTreeKeeping(tree, fbs, addressing...)
	e.recordKept(kind, key, kept)
	return nodes
}

// dir is the entity's directory relative to the provider root.
func (e *serviceRenderer) dir(kind string, n ir.Names) string {
	return path.Join("internal/services", kind, n.Service, n.APIVersionDirectory, n.Key)
}

// packagePath is the entity package's import path.
func (e *serviceRenderer) packagePath(kind string, n ir.Names) string {
	return e.pc.Module + "/" + e.dir(kind, n)
}

// registration builds one entity's import and registration lines for the
// provider-core registry sentinels.
func (e *serviceRenderer) registration(kind string, n ir.Names, constructor string) (string, string) {
	alias := importAlias(n)
	importLine := alias + " " + `"` + e.packagePath(kind, n) + `"`
	return importLine, alias + "." + constructor + ","
}

// importAlias is the deterministic, collision-free alias a registry file
// imports one entity package under: service, version and key, camel-cased
// together.
func importAlias(n ir.Names) string {
	return lowerCamel(n.Service + "_" + n.APIVersionDirectory + "_" + n.Key)
}

// lowerCamel renders a snake_case name as a plain lower-camel identifier.
// Deliberately not acronym-aware: aliases are private plumbing, and the
// plain spelling is collision-free either way.
func lowerCamel(snake string) string {
	parts := strings.Split(snake, "_")
	var b strings.Builder
	for i, p := range parts {
		if p == "" {
			continue
		}
		if b.Len() == 0 && i == 0 {
			b.WriteString(p)
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

// renderServiceFile renders one entity template against its view and holds
// Go output to the gofmt fixed point. Unlike the provider core's static
// templates — whose shape is fully known when the template is written —
// entity files interpolate computed declarations, so the assembled source
// is normalised through go/format here and a file that does not parse is
// an error naming the template.
func (e *serviceRenderer) renderServiceFile(templatePath, outPath, source string, view any) (File, error) {
	partial, err := fs.ReadFile(templates.ProviderCore, headerPartial)
	if err != nil {
		return File{}, fmt.Errorf("reading the shared header partial: %w", err)
	}
	body, err := fs.ReadFile(templates.Services, "services/"+templatePath)
	if err != nil {
		return File{}, fmt.Errorf("reading %s: %w", templatePath, err)
	}

	t, err := template.New(path.Base(templatePath)).Parse(string(partial))
	if err != nil {
		return File{}, fmt.Errorf("parsing %s: %w", headerPartial, err)
	}
	if _, err := t.Parse(string(body)); err != nil {
		return File{}, fmt.Errorf("parsing %s: %w", templatePath, err)
	}

	var buffer bytes.Buffer
	if err := t.Execute(&buffer, view); err != nil {
		return File{}, fmt.Errorf("rendering %s: %w", templatePath, err)
	}

	content := squeezeBlankLines(buffer.Bytes())
	if strings.HasSuffix(outPath, ".go") {
		formatted, err := format.Source(content)
		if err != nil {
			return File{}, fmt.Errorf("%s renders Go that does not parse: %w", templatePath, err)
		}
		content = formatted
	}

	return File{Path: outPath, Content: content, Source: source}, nil
}

// rawFile wraps non-template content — fixture JSON, rendered HCL — as an
// emitted file.
func rawFile(outPath, source string, content []byte) File {
	return File{Path: outPath, Content: content, Source: source}
}

// node joins one attribute with its resolved binding. Attributes the
// bindings lack — pruned by the SDK, refused by derivation — take no node:
// an attribute that cannot be mapped must not appear in the schema either,
// or the provider would carry an attribute that never travels.
//
// The root id is the one exception, and it carries a nil fb. Terraform
// requires every resource and datasource to have an id; the API need not
// agree. A read response that names its key something other than id, or omits
// it altogether and leaves it in the URL, is an ordinary REST shape, and both
// are common in real documents. The id node has to survive them both:
// render_mapping's path-parameter lookup reads it, and dropping it aborts the
// whole provider. Its value comes from the path parameter and from whatever
// the create response carries, neither of which needs a read binding.
type node struct {
	attribute ir.Attribute
	fb        *sdkbind.FieldBinding
	children  []node
	// held marks an attribute whose state keeps the planned value because
	// no response answers it: the binding at its root is kept from the
	// plan, and every attribute beneath that root is held with it.
	held bool
}

// joinTree joins an attribute tree with its field bindings, in tree order.
// addressing names the root attributes that exist to fill a path parameter
// rather than to carry a field, and so survive with no binding.
func joinTree(tree *ir.AttributeTree, fbs []sdkbind.FieldBinding, addressing ...map[string]bool) []node {
	nodes, _ := joinTreeKeeping(tree, fbs, addressing...)
	return nodes
}

// joinTreeKeeping is joinTree, and also answers the attributes it kept
// with no SDK field behind them — the id and the addressing attributes.
//
// Pruning removes their bindings correctly: no model carries them, because
// they address the object rather than describe it. The attribute still
// reaches the schema, so reporting that removal as a loss would be wrong,
// and this is the only place that knows which attributes those are.
func joinTreeKeeping(tree *ir.AttributeTree, fbs []sdkbind.FieldBinding, addressing ...map[string]bool) ([]node, []string) {
	names := map[string]bool{idAttributeName: true}
	for _, set := range addressing {
		for name := range set {
			names[name] = true
		}
	}
	var kept []string
	return joinAttributes(tree, fbs, names, true, false, &kept), kept
}

// joinAttributes is joinTree's recursion, tracking whether it is at the root
// so addressing attributes are kept at the top level only. A nested attribute
// that happens to share one of their names is an ordinary API field: unbound,
// it travels nowhere and is dropped like any other.
func joinAttributes(tree *ir.AttributeTree, fbs []sdkbind.FieldBinding, addressing map[string]bool, root, held bool, kept *[]string) []node {
	if tree == nil {
		return nil
	}
	byAttr := make(map[string]*sdkbind.FieldBinding, len(fbs))
	for i := range fbs {
		byAttr[fbs[i].Attr] = &fbs[i]
	}
	var out []node
	for _, a := range tree.Attributes {
		fb, ok := byAttr[a.Name]
		if !ok {
			if !root || !addressing[a.Name] || a.Nested != nil {
				continue
			}
			out = append(out, node{attribute: a})
			*kept = append(*kept, a.Name)
			continue
		}
		n := node{attribute: a, fb: fb, held: held || fb.KeptFromPlan}
		if a.Nested != nil {
			n.children = joinAttributes(a.Nested, fb.Nested, addressing, false, n.held, kept)
		}
		out = append(out, n)
	}
	return out
}

// idAttributeName is the terraform attribute every resource and datasource
// carries, whatever the API calls its key.
const idAttributeName = "id"

// addressingNames is the set of root attribute names that fill an
// operation's path parameters, in terraform spelling.
//
// They survive pruning with no binding for the same reason the id does: they
// address the object rather than describe it, so no request or response body
// declares them and no SDK model can carry them. A parent-scoped API puts
// most of its surface behind them — /repos/{owner}/{repo}/… — and dropping
// them excluded every entity underneath.
//
// A parameter is answered by the root attribute carrying its name, or by
// one carrying its spelling as a wire name — a parent the document spells
// `id`, named after its entity because `id` is the resource's own.
func addressingNames(tree *ir.AttributeTree, operations ...*ir.Operation) map[string]bool {
	names := map[string]bool{}
	for _, operation := range operations {
		if operation == nil {
			continue
		}
		for _, parameter := range operation.PathParameters {
			names[ir.TerraformName(parameter.Name)] = true
			if tree == nil {
				continue
			}
			for _, attribute := range tree.Attributes {
				if attribute.WireName == parameter.Name && attribute.Nested == nil {
					names[attribute.Name] = true
				}
			}
		}
	}
	return names
}

// supportedTree rebuilds an attribute tree containing only the joined
// nodes, so the fixture derivation covers exactly what the schema carries.
// Unsupported attributes are kept: fixtures turns them into omissions with
// their stated reasons.
func supportedTree(tree *ir.AttributeTree, nodes []node) *ir.AttributeTree {
	if tree == nil {
		return nil
	}
	kept := make(map[string]*node, len(nodes))
	for i := range nodes {
		kept[nodes[i].attribute.Name] = &nodes[i]
	}
	// The tree-level conditional-edge facts travel with the pruned tree: the
	// fixture derivation reads them to pick one discriminator variant and keep
	// only the fields valid under it, so the generated configuration satisfies
	// the same value-conditional validators emitted from these edges.
	out := &ir.AttributeTree{
		ConditionalRequirements: tree.ConditionalRequirements,
		ConditionalValidities:   tree.ConditionalValidities,
		Dependencies:            tree.Dependencies,
		MutuallyExclusiveGroups: tree.MutuallyExclusiveGroups,
		ValidConfigurations:     tree.ValidConfigurations,
	}
	for _, a := range tree.Attributes {
		if a.Unsupported {
			out.Attributes = append(out.Attributes, a)
			continue
		}
		n, ok := kept[a.Name]
		if !ok {
			continue
		}
		copied := a
		if a.Nested != nil {
			copied.Nested = supportedTree(a.Nested, n.children)
		}
		out.Attributes = append(out.Attributes, copied)
	}
	return out
}

// deriveFixtures is the one fixture derivation per entity: the joined
// tree, handed to fixtures, with every value the SDK's own type constrains
// pinned to what that type parses.
func deriveFixtures(tree *ir.AttributeTree, nodes []node) fixtures.Fixture {
	spec := fixtures.Derive(supportedTree(tree, nodes))
	pinBySDKType(spec.Entries, nodes)
	return spec
}

// pinBySDKType pins every fixture value the generated SDK parses into a type
// the document did not name — a timestamp or an identifier the schema calls a
// plain string. The document's format answers where it is declared; the
// binding answers everywhere else.
func pinBySDKType(entries []fixtures.Entry, nodes []node) {
	byName := make(map[string]node, len(nodes))
	for _, n := range nodes {
		byName[n.attribute.Name] = n
	}
	for i := range entries {
		n, ok := byName[entries[i].Name]
		if !ok {
			continue
		}
		if len(entries[i].Nested) > 0 {
			pinBySDKType(entries[i].Nested, n.children)
			continue
		}
		if n.fb == nil {
			continue
		}
		if value, demanded := fixtures.ValueForSDKType(n.fb.Access.SDKType); demanded {
			entries[i].Scalar = value
		}
	}
}

// importSet collects the imports of one file, grouped the way the
// rendered file declares them: standard library, third-party, module-local.
//
// The elements are code.Import, so an import can travel attached to the
// expression that needs it — see code.CustomValidator — rather than being
// registered as a side effect somewhere else in the renderer.
type importSet struct {
	module string
	lines  map[string]bool
}

func newImportSet(module string) *importSet {
	return &importSet{module: module, lines: map[string]bool{}}
}

// add records one import; alias may be empty.
func (s *importSet) add(alias, importPath string) {
	s.addImport(code.Import{Alias: &alias, Path: importPath})
}

// addImport records one code.Import.
func (s *importSet) addImport(imported code.Import) {
	line := `"` + imported.Path + `"`
	if imported.HasAlias() {
		line = *imported.Alias + " " + line
	}
	s.lines[line] = true
}

// addImports records every import an expression declared it needs.
func (s *importSet) addImports(imports []code.Import) {
	for _, imported := range imports {
		s.addImport(imported)
	}
}

// render builds the finished import declaration: three sorted groups
// separated by blank lines, in the shape gofmt keeps.
func (s *importSet) render() string {
	var std, extension, local []string
	for line := range s.lines {
		importPath := line[strings.Index(line, `"`)+1 : len(line)-1]
		switch {
		case strings.HasPrefix(importPath, s.module+"/") || importPath == s.module:
			local = append(local, line)
		case !strings.Contains(strings.SplitN(importPath, "/", 2)[0], "."):
			std = append(std, line)
		default:
			extension = append(extension, line)
		}
	}
	sort.Strings(std)
	sort.Strings(extension)
	sort.Strings(local)

	var groups []string
	for _, g := range [][]string{std, extension, local} {
		if len(g) > 0 {
			groups = append(groups, "\t"+strings.Join(g, "\n\t"))
		}
	}
	if len(groups) == 0 {
		return ""
	}
	return "import (\n" + strings.Join(groups, "\n\n") + "\n)"
}

// goDuration renders a time.Duration as the Go expression generated code
// declares it with.
func goDuration(d int64) string {
	const (
		second = int64(1000000000)
		minute = 60 * second
		hour   = 60 * minute
	)
	switch {
	case d == 0:
		return "0"
	case d%hour == 0:
		return fmt.Sprintf("%d * time.Hour", d/hour)
	case d%minute == 0:
		return fmt.Sprintf("%d * time.Minute", d/minute)
	case d%second == 0:
		return fmt.Sprintf("%d * time.Second", d/second)
	default:
		return fmt.Sprintf("%d * time.Nanosecond", d)
	}
}
