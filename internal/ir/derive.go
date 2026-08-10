package ir

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// configExcludedReason is the reason recorded for entities services.exclude
// drops, distinguishable at a glance from a classification exclusion.
const configExcludedReason = "excluded by configuration"

// Derive computes the model from the revised document and the config. It is
// pure: the same inputs yield the same value every run, byte-for-byte under
// JSON. Nothing here reads a clock, the environment or the filesystem, and
// nothing writes — the IR exists only in memory, for exactly one run.
func Derive(doc *specmodel.Document, cfg *config.Config) (*Model, error) {
	if doc == nil {
		return nil, errors.New("ir: the document is nil")
	}
	if cfg == nil {
		return nil, errors.New("ir: the config is nil")
	}
	if cfg.Provider.Name == "" {
		return nil, errors.New("ir: provider.name is empty; every terraform type name derives from it")
	}

	d := &deriver{index: indexOperations(doc)}
	m := &Model{Provider: Provider{Name: cfg.Provider.Name}}

	excluded := map[string]bool{}
	for _, e := range cfg.Services.Exclude {
		excluded[e] = true
	}

	cls := specmodel.Classify(doc)

	// Decide inclusion and names first: actions need every kept entity's
	// key to resolve their parent, and key collisions must be refused
	// before any entity is built against an ambiguous name.
	type kept struct {
		c     specmodel.Classification
		names Names
	}
	var keep []kept
	keyOwner := map[string]string{}
	parentKey := map[string]string{}
	for _, c := range cls.Entities {
		names := deriveNames(cfg.Provider.Name, c.Key, c.CollectionPath)
		if excluded[names.Service] || excluded[names.Key] {
			m.Excluded = append(m.Excluded, Exclusion{Key: names.Key, Reason: configExcludedReason})
			continue
		}
		// Two collection paths can derive one key: sibling API versions
		// once the version prefix factors out, or paths that differ only
		// in an embedded parameter. One key can generate only once, so
		// the first entity in collection-path order wins and the rest are
		// excluded with a reason naming the winner — recorded, never
		// silent, and resolvable through services.exclude.
		if owner, taken := keyOwner[names.Key]; taken {
			m.Excluded = append(m.Excluded, Exclusion{
				Key: names.Key,
				Reason: fmt.Sprintf(
					"entity key collides with %s (this entity is %s); exclude one via services.exclude",
					owner, c.CollectionPath),
			})
			continue
		}
		keyOwner[names.Key] = c.CollectionPath
		parentKey[c.CollectionPath] = names.Key
		keep = append(keep, kept{c: c, names: names})
	}
	for _, e := range cls.Excluded {
		names := deriveNames(cfg.Provider.Name, e.Key, e.CollectionPath)
		m.Excluded = append(m.Excluded, Exclusion{Key: names.Key, Reason: e.Reason})
	}

	for _, k := range keep {
		for _, kind := range k.c.Kinds {
			switch kind {
			case specmodel.KindResource:
				m.Resources = append(m.Resources, d.resource(k.c, k.names))
			case specmodel.KindDatasource:
				m.Datasources = append(m.Datasources, d.datasource(k.c, k.names))
			case specmodel.KindListResource:
				m.ListResources = append(m.ListResources, d.listResource(k.c, k.names))
			case specmodel.KindAction:
				m.Actions = append(m.Actions, d.action(k.c, k.names, parentKey))
			}
		}
	}

	sortModel(m)
	return m, nil
}

// sortModel fixes every slice's order by entity key, so document order and
// classification order can never leak into the model's shape.
func sortModel(m *Model) {
	sort.Slice(m.Resources, func(i, j int) bool { return m.Resources[i].Names.Key < m.Resources[j].Names.Key })
	sort.Slice(m.Datasources, func(i, j int) bool { return m.Datasources[i].Names.Key < m.Datasources[j].Names.Key })
	sort.Slice(m.ListResources, func(i, j int) bool { return m.ListResources[i].Names.Key < m.ListResources[j].Names.Key })
	sort.Slice(m.Actions, func(i, j int) bool { return m.Actions[i].Names.Key < m.Actions[j].Names.Key })
	sort.Slice(m.Excluded, func(i, j int) bool {
		if m.Excluded[i].Key != m.Excluded[j].Key {
			return m.Excluded[i].Key < m.Excluded[j].Key
		}
		return m.Excluded[i].Reason < m.Excluded[j].Reason
	})
}

// deriver carries the operation index every entity builder reads.
type deriver struct {
	index map[string]*specmodel.Operation
}

// indexOperations keys every operation by path and method, so a
// classification's operation reference finds its full operation back.
func indexOperations(doc *specmodel.Document) map[string]*specmodel.Operation {
	index := map[string]*specmodel.Operation{}
	for pi := range doc.Paths {
		for oi := range doc.Paths[pi].Operations {
			op := &doc.Paths[pi].Operations[oi]
			index[op.Path+" "+op.Method] = op
		}
	}
	return index
}

// full resolves a classification operation reference to the document's
// operation, nil for a nil reference.
func (d *deriver) full(ref *specmodel.Op) *specmodel.Operation {
	if ref == nil {
		return nil
	}
	return d.index[ref.Path+" "+ref.Method]
}

// op renders one operation reference into the dialect-neutral form binders
// key on, nil for a nil reference.
func (d *deriver) op(ref *specmodel.Op, kind OpKind) *Op {
	if ref == nil {
		return nil
	}
	full := d.full(ref)
	return &Op{
		Kind:         kind,
		Method:       ref.Method,
		PathTemplate: ref.Path,
		OperationID:  ref.OperationID,
		PathParams:   pathParams(ref.Path, full),
		SuccessCode:  successCode(full),
	}
}

func (d *deriver) resource(c specmodel.Classification, names Names) Resource {
	createFull, readFull := d.full(c.Create), d.full(c.Read)
	updateFull, deleteFull := d.full(c.Update), d.full(c.Delete)

	var createBody, readBody *specmodel.Schema
	if createFull != nil {
		createBody = createFull.RequestBody
	}
	if readFull != nil {
		readBody = readFull.SuccessSchema()
	}
	tree := buildTree(createBody, readBody, c.MissingUpdate)
	keyParam, keyType := itemKeyParam(c.ItemPath, readFull)
	ensureID(tree, keyParam, keyType)

	updateStyle := ""
	if c.Update != nil {
		updateStyle = UpdateStylePatchMerge
		if updateFull != nil {
			if s, ok := updateFull.Extensions.UpdateStyle(); ok {
				updateStyle = s
			}
		}
	}

	deleteNotFoundOK := false
	if deleteFull != nil {
		deleteNotFoundOK, _ = deleteFull.Extensions.DeleteNotFoundOK()
	}

	return Resource{
		Names: names,
		Ops: Ops{
			Create: d.op(c.Create, OpCreate),
			Read:   d.op(c.Read, OpRead),
			Update: d.op(c.Update, OpUpdate),
			Delete: d.op(c.Delete, OpDelete),
			List:   d.op(c.List, OpList),
		},
		Schema:              tree,
		MissingUpdate:       c.MissingUpdate,
		UpdateStyle:         updateStyle,
		EventualConsistency: maxEventualConsistency(createFull, readFull, updateFull, deleteFull),
		DeleteNotFoundOK:    deleteNotFoundOK,
		Timeouts:            defaultTimeouts(),
	}
}

func (d *deriver) datasource(c specmodel.Classification, names Names) Datasource {
	readFull := d.full(c.Read)
	var readBody *specmodel.Schema
	if readFull != nil {
		readBody = readFull.SuccessSchema()
	}
	itemTree := buildTree(nil, readBody, false)
	keyParam, keyType := itemKeyParam(c.ItemPath, readFull)
	ensureID(itemTree, keyParam, keyType)

	if c.LookupByKey {
		requireKey(itemTree, keyParam, keyType)
		return Datasource{
			Names:        names,
			Ops:          Ops{Read: d.op(c.Read, OpRead)},
			Schema:       itemTree,
			LookupByKey:  true,
			KeyParameter: keyParam,
		}
	}

	// The companion datasource: filter_type and filter_value select which
	// objects come back, items carries them. The filter attributes are the
	// toolkit's own vocabulary, not the API's, so they take no wire names
	// beyond their own.
	return Datasource{
		Names: names,
		Ops:   Ops{Read: d.op(c.Read, OpRead), List: d.op(c.List, OpList)},
		Schema: &AttributeTree{Attributes: []Attribute{
			{Name: "filter_type", WireName: "filter_type", Kind: TypeString, Presence: PresenceRequired},
			{Name: "filter_value", WireName: "filter_value", Kind: TypeString, Presence: PresenceOptional},
			{Name: "items", WireName: "items", Kind: TypeList, ElemKind: TypeObject, Presence: PresenceComputed, Nested: itemTree},
		}},
	}
}

func (d *deriver) listResource(c specmodel.Classification, names Names) ListResource {
	listFull := d.full(c.List)
	var listBody *specmodel.Schema
	if listFull != nil {
		listBody = listFull.SuccessSchema()
	}
	// A list response is usually the element array itself; an envelope
	// object is taken as-is rather than guessed into an element.
	element := listBody
	if f := flatten(listBody); f.typ == "array" && f.items != nil {
		element = f.items
	}
	return ListResource{
		Names:  names,
		ListOp: *d.op(c.List, OpList),
		Schema: buildTree(nil, element, false),
	}
}

func (d *deriver) action(c specmodel.Classification, names Names, parentKey map[string]string) Action {
	createFull := d.full(c.Create)
	var request *AttributeTree
	if createFull != nil && createFull.RequestBody != nil {
		request = buildTree(createFull.RequestBody, nil, false)
	}
	return Action{
		Names:         names,
		InvokeOp:      *d.op(c.Create, OpInvoke),
		RequestSchema: request,
		ParentEntity:  enclosingEntity(c.CollectionPath, parentKey),
	}
}

// enclosingEntity finds the entity whose collection path is the longest
// proper prefix of the action's, segment-aligned; empty when none is.
func enclosingEntity(collectionPath string, parentKey map[string]string) string {
	path := collectionPath
	for {
		cut := strings.LastIndex(path, "/")
		if cut <= 0 {
			return ""
		}
		path = path[:cut]
		if key, ok := parentKey[path]; ok {
			return key
		}
	}
}

// templateParam matches one {name} segment of a path template.
var templateParam = regexp.MustCompile(`\{([^}]+)\}`)

// pathParams lists an operation's path parameters in path-template order —
// the order any call expression will take them — typed from the declared
// parameter schema, string when the document does not say.
func pathParams(pathTemplate string, op *specmodel.Operation) []Param {
	matches := templateParam.FindAllStringSubmatch(pathTemplate, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]Param, 0, len(matches))
	for _, m := range matches {
		p := Param{Name: m[1], Type: TypeString}
		if op != nil {
			for _, declared := range op.Parameters {
				if declared.In == "path" && declared.Name == m[1] {
					if k := scalarKind(declared.Schema); k != "" {
						p.Type = k
					}
				}
			}
		}
		out = append(out, p)
	}
	return out
}

// itemKeyParam names the item path's trailing parameter and its type,
// which is what the id attribute and a lookup key derive from.
func itemKeyParam(itemPath string, read *specmodel.Operation) (string, TypeKind) {
	matches := templateParam.FindAllStringSubmatch(itemPath, -1)
	if len(matches) == 0 {
		return "", ""
	}
	name := matches[len(matches)-1][1]
	kind := TypeString
	if read != nil {
		for _, p := range read.Parameters {
			if p.In == "path" && p.Name == name {
				if k := scalarKind(p.Schema); k != "" {
					kind = k
				}
			}
		}
	}
	return name, kind
}

// scalarKind maps a scalar schema type onto an attribute kind, empty for
// anything else.
func scalarKind(s *specmodel.Schema) TypeKind {
	switch flatten(s).typ {
	case "string":
		return TypeString
	case "boolean":
		return TypeBool
	case "integer":
		return TypeInt64
	case "number":
		return TypeFloat64
	}
	return ""
}

// successCode is the first declared 2xx status. Responses arrive sorted by
// status string, which orders 2xx codes numerically.
func successCode(op *specmodel.Operation) int {
	if op == nil {
		return 0
	}
	for _, r := range op.Responses {
		if strings.HasPrefix(r.Status, "2") {
			if n, err := strconv.Atoi(r.Status); err == nil {
				return n
			}
		}
	}
	return 0
}

// maxEventualConsistency takes the largest declared read-after-write lag
// across the lifecycle: whichever operation observed the worst lag governs
// how long generated code must be prepared to wait.
func maxEventualConsistency(ops ...*specmodel.Operation) time.Duration {
	var max time.Duration
	for _, op := range ops {
		if op == nil {
			continue
		}
		if d, ok := op.Extensions.EventualConsistency(); ok && d > max {
			max = d
		}
	}
	return max
}
