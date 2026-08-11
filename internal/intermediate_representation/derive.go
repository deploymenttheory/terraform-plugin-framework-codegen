package intermediate_representation

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
// nothing writes — the derivation exists only in memory, for exactly one
// run.
func Derive(doc *specmodel.Document, cfg *config.Config) (*Model, error) {
	if doc == nil {
		return nil, errors.New("intermediate_representation: the document is nil")
	}
	if cfg == nil {
		return nil, errors.New("intermediate_representation: the config is nil")
	}
	if cfg.Provider.Name == "" {
		return nil, errors.New("intermediate_representation: provider.name is empty; every terraform type name derives from it")
	}

	d := &deriver{index: indexOperations(doc)}
	m := &Model{Provider: Provider{Name: cfg.Provider.Name}}

	excluded := map[string]bool{}
	for _, e := range cfg.Services.Exclude {
		excluded[e] = true
	}

	cls := specmodel.Classify(doc)

	// Decide inclusion and names first: actions need every kept entity's
	// key to resolve their parent, and a key collision must be resolved
	// before any entity is built against an ambiguous name.
	type kept struct {
		c     specmodel.Classification
		names Names
	}
	var keep []kept
	claimed := map[string]string{} // final key -> the collection path that claimed it
	family := map[string][]int{}   // pre-disambiguation key -> indices into keep
	parentKey := map[string]string{}
	for _, c := range cls.Entities {
		names := deriveNames(cfg.Provider.Name, c.Key, c.CollectionPath)
		original := names.Key
		// Two collection paths can derive one key: sibling API versions
		// once the version prefix factors out, or paths that differ only
		// in an embedded parameter. Both entities generate: the later one
		// in collection-path order takes a mechanically disambiguated key,
		// and every member of the family carries a co-management note so
		// the generated schema description says the overlap out loud.
		if winner, taken := claimed[names.Key]; taken {
			names = names.withKey(cfg.Provider.Name,
				disambiguateKey(names.Key, c.CollectionPath, winner, claimed))
		}
		claimed[names.Key] = c.CollectionPath
		if excluded[names.Service] || excluded[names.Key] {
			m.Excluded = append(m.Excluded, Exclusion{Key: names.Key, Reason: configExcludedReason})
			continue
		}
		parentKey[c.CollectionPath] = names.Key
		family[original] = append(family[original], len(keep))
		keep = append(keep, kept{c: c, names: names})
	}
	for _, e := range cls.Excluded {
		names := deriveNames(cfg.Provider.Name, e.Key, e.CollectionPath)
		m.Excluded = append(m.Excluded, Exclusion{Key: names.Key, Reason: e.Reason})
	}

	// A collision family that generates more than one entity co-manages
	// one API surface: every member's note names its siblings, so the
	// prose lands on both sides of the overlap.
	notes := map[string]string{}
	for _, members := range family {
		if len(members) < 2 {
			continue
		}
		for _, i := range members {
			siblings := make([]string, 0, len(members)-1)
			for _, j := range members {
				if j != i {
					siblings = append(siblings, keep[j].names.TerraformType)
				}
			}
			sort.Strings(siblings)
			notes[keep[i].names.Key] = coManagementNote(siblings)
		}
	}

	for _, k := range keep {
		note := notes[k.names.Key]
		for _, kind := range k.c.Kinds {
			switch kind {
			case specmodel.KindResource:
				r := d.resource(k.c, k.names)
				r.CoManagementNote = note
				m.Resources = append(m.Resources, r)
			case specmodel.KindDatasource:
				ds := d.datasource(k.c, k.names)
				ds.CoManagementNote = note
				m.Datasources = append(m.Datasources, ds)
			case specmodel.KindListResource:
				lr := d.listResource(k.c, k.names)
				lr.CoManagementNote = note
				m.ListResources = append(m.ListResources, lr)
			case specmodel.KindAction:
				a := d.action(k.c, k.names, parentKey)
				a.CoManagementNote = note
				m.Actions = append(m.Actions, a)
			}
		}
	}

	sortModel(m)
	return m, nil
}

// disambiguateKey computes the later entity's key when two collection
// paths derive the same one. The algorithm is mechanical, never a guess:
//
//  1. Split both collection paths into segments.
//  2. Keep, in path order, every segment of the later path the winning
//     path does not also contain — the distinguishing segments.
//  3. Render a distinguishing parameter segment "{name}" as "by_" plus
//     the name snake_cased, and a distinguishing literal segment as
//     itself snake_cased.
//  4. Append the renderings to the colliding key, underscore-joined:
//     "/tags/{id}/assign" against the winner "/tags/assign" turns
//     "tags_assign" into "tags_assign_by_id".
//  5. Should the result still be claimed, or should no segment
//     distinguish the paths, an ordinal _2, _3 … appends until the key
//     is free.
func disambiguateKey(key, laterPath, winnerPath string, claimed map[string]string) string {
	winner := map[string]bool{}
	for _, seg := range pathSegments(winnerPath) {
		winner[seg] = true
	}
	parts := []string{key}
	for _, seg := range pathSegments(laterPath) {
		if winner[seg] {
			continue
		}
		if strings.HasPrefix(seg, "{") {
			parts = append(parts, "by_"+snakeCase(strings.Trim(seg, "{}")))
			continue
		}
		parts = append(parts, snakeCase(seg))
	}
	candidate := strings.Join(parts, "_")
	if _, taken := claimed[candidate]; !taken && candidate != key {
		return candidate
	}
	for n := 2; ; n++ {
		next := fmt.Sprintf("%s_%d", candidate, n)
		if _, taken := claimed[next]; !taken {
			return next
		}
	}
}

// pathSegments splits a path template into its non-empty segments.
func pathSegments(path string) []string {
	var out []string
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if seg != "" {
			out = append(out, seg)
		}
	}
	return out
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
		ListEnvelopeKey:     listEnvelopeKey(d.full(c.List)),
	}
}

// listEnvelopeKey is the wire property a list response wraps its item array
// under: empty when the response is a bare array (or absent), otherwise the
// wrapping key. The generated list mock keys its envelope on this instead of
// assuming every API wraps under "value" — the SDK's collection accessor is
// derived from the same answer, so the two agree.
//
// x-tfpfgen-list-response-shape wins where it is present: it carries what a
// live collection response actually contained, and it is written onto the
// operation precisely when the document's own list schema was found to be
// wrong. Absent it, the schema is read as before — the first array-typed
// property of a wrapping object — so an unaudited document behaves exactly
// as it did.
func listEnvelopeKey(list *specmodel.Operation) string {
	if list == nil {
		return ""
	}
	if shape, ok := list.Extensions.ListResponseShape(); ok {
		if shape.Wrapped() {
			return shape.Key
		}
		return ""
	}
	f := flatten(list.SuccessSchema())
	if f.typ == "array" {
		return ""
	}
	for _, p := range f.props {
		if flatten(p.Schema).typ == "array" {
			return p.Name
		}
	}
	return ""
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
		ListEnvelopeKey: listEnvelopeKey(d.full(c.List)),
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
		Names:           names,
		ListOp:          *d.op(c.List, OpList),
		Schema:          buildTree(nil, element, false),
		ListEnvelopeKey: listEnvelopeKey(listFull),
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
