package specmodel

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Kind is what an entity can become in the generated provider.
type Kind string

const (
	// KindResource has the lifecycle Terraform needs to own something:
	// create, read and delete, with update desirable but optional.
	KindResource Kind = "resource"
	// KindDatasource can be looked up by id. Every resource also yields
	// one; a list-plus-read entity yields one without being a resource.
	KindDatasource Kind = "datasource"
	// KindListResource is list-only: enumerable but not addressable.
	KindListResource Kind = "list-resource"
	// KindAction is a POST with no CRUD complement — an invocation, not
	// a thing with a lifecycle.
	KindAction Kind = "action"
)

// kindOrder fixes the order kinds appear in on an entity, so the emitted
// classification is byte-stable.
var kindOrder = []Kind{KindResource, KindDatasource, KindListResource, KindAction}

// Op identifies one operation for a classification consumer, which needs
// exactly enough to find it again in the document or in the generated SDK.
type Op struct {
	Method      string
	Path        string
	OperationID string
}

// Classification is one entity and everything decided about it.
type Classification struct {
	// Key is the entity key derived from the collection path:
	// singularized last collection segment, snake_case, prefixed with the
	// path's earlier segments. "/v7/tags/{tagId}" yields "v7_tag".
	Key string
	// CollectionPath is the identifier-free path, e.g. "/tags".
	CollectionPath string
	// ItemPath is the identifier-carrying sibling, e.g. "/tags/{tagId}",
	// empty when the document declares none.
	ItemPath string
	// Kinds lists what the entity yields, in the fixed order resource,
	// datasource, list-resource, action.
	Kinds []Kind
	// The operations by role. An action's POST sits in Create: the role
	// slots describe HTTP position, and kind says what the position
	// amounts to.
	Create *Op
	Read   *Op
	Update *Op
	Delete *Op
	List   *Op
	// Extra holds operations on the entity's paths that fit no role: a
	// second POST, a PATCH on the collection. Recorded so the audit
	// planner sees the whole surface, never silently dropped.
	Extra []Op
	// MissingUpdate is true on a resource with no update operation, which
	// downstream turns into RequiresReplace on every attribute.
	MissingUpdate bool
	// LookupByKey is true on a datasource whose only access is the item
	// GET: there is no list operation, so the generated datasource takes
	// the item path parameter — often a name in name-addressed APIs — as
	// its required argument and returns the object it identifies, id
	// included. HCL supplies the key, gets the object back.
	LookupByKey bool
}

// Exclusion is one entity that classifies as nothing, and why. The reasons
// are for people: they surface in audit-planning output so a surprising
// omission can be traced to the document instead of to a hunch.
type Exclusion struct {
	Key            string
	CollectionPath string
	ItemPath       string
	Reason         string
}

// Classifications is every entity in a document, decided and sorted.
type Classifications struct {
	// Entities is sorted by collection path.
	Entities []Classification
	// Excluded is sorted by collection path.
	Excluded []Exclusion
}

// Classify groups the document's operations into entities and decides what
// each can become.
//
// Grouping is by path convention: a collection path and its item sibling —
// "/tags" and "/tags/{tagId}" — are one entity. Where an API ignores the
// convention the entity comes out incomplete and is excluded with a reason,
// rather than guessed at.
func Classify(d *Document) Classifications {
	byCollection := map[string]*entity{}
	order := make([]string, 0, len(d.Paths))

	for pi := range d.Paths {
		p := &d.Paths[pi]
		collection, isItem := splitPath(p.Path)
		e, ok := byCollection[collection]
		if !ok {
			e = &entity{key: keyForPath(collection), collection: collection}
			byCollection[collection] = e
			order = append(order, collection)
		}
		if isItem && e.item == "" {
			e.item = p.Path
		}
		for oi := range p.Operations {
			e.assign(&p.Operations[oi], isItem)
		}
	}

	sort.Strings(order)
	var out Classifications
	for _, collection := range order {
		c, excluded := byCollection[collection].decide()
		if excluded != nil {
			out.Excluded = append(out.Excluded, *excluded)
			continue
		}
		out.Entities = append(out.Entities, c)
	}
	return out
}

// entity is the working state for one collection/item pair while its
// operations are being gathered.
type entity struct {
	key, collection, item           string
	create, read, update, del, list *Operation
	extra                           []*Operation
}

// assign files an operation into the role its HTTP position means: POST on
// the collection creates, GET on the collection lists, GET/PUT/PATCH/DELETE
// on the item read, update and delete. A second claimant for a role, or a
// method in no role's position, lands in extra rather than being dropped.
func (e *entity) assign(op *Operation, isItem bool) {
	slot := func(dst **Operation) {
		if *dst == nil {
			*dst = op
			return
		}
		e.extra = append(e.extra, op)
	}
	switch {
	case op.Method == "POST" && !isItem:
		slot(&e.create)
	case op.Method == "GET" && !isItem:
		slot(&e.list)
	case op.Method == "GET" && isItem:
		slot(&e.read)
	case (op.Method == "PUT" || op.Method == "PATCH") && isItem:
		slot(&e.update)
	case op.Method == "DELETE" && isItem:
		slot(&e.del)
	default:
		e.extra = append(e.extra, op)
	}
}

// decide classifies a gathered entity, or explains why it is nothing.
func (e *entity) decide() (Classification, *Exclusion) {
	resourceShape := e.create != nil && e.read != nil && e.del != nil
	resourceOK := resourceShape && e.create.RequestBody != nil && e.read.SuccessSchema() != nil

	dsShape := e.list != nil && e.read != nil
	dsOK := dsShape && e.read.SuccessSchema() != nil && e.list.SuccessSchema() != nil

	lookupShape := e.read != nil && e.list == nil
	lookupOK := lookupShape && e.read.SuccessSchema() != nil

	listOnlyShape := e.list != nil && e.read == nil
	listOK := listOnlyShape && e.list.SuccessSchema() != nil

	actionShape := e.create != nil &&
		e.read == nil && e.list == nil && e.update == nil && e.del == nil

	var kinds []Kind
	for _, k := range kindOrder {
		switch k {
		case KindResource:
			if resourceOK {
				kinds = append(kinds, k)
			}
		case KindDatasource:
			// A resource is always also readable by id, so it yields a
			// datasource whether or not the API can list it. An entity
			// whose only access is the item GET yields one too: the
			// caller supplies the item path key and reads the object.
			if resourceOK || dsOK || lookupOK {
				kinds = append(kinds, k)
			}
		case KindListResource:
			if listOK {
				kinds = append(kinds, k)
			}
		case KindAction:
			if actionShape {
				kinds = append(kinds, k)
			}
		}
	}

	if len(kinds) == 0 {
		return Classification{}, &Exclusion{
			Key:            e.key,
			CollectionPath: e.collection,
			ItemPath:       e.item,
			Reason:         e.exclusionReason(resourceShape, dsShape, listOnlyShape, lookupShape),
		}
	}

	return Classification{
		Key:            e.key,
		CollectionPath: e.collection,
		ItemPath:       e.item,
		Kinds:          kinds,
		Create:         opRef(e.create),
		Read:           opRef(e.read),
		Update:         opRef(e.update),
		Delete:         opRef(e.del),
		List:           opRef(e.list),
		Extra:          extraRefs(e.extra),
		MissingUpdate:  resourceOK && e.update == nil,
		// A resource's by-id datasource is its normal companion, not a
		// key lookup; the flag marks the datasources that exist only
		// because the item GET does.
		LookupByKey: lookupOK && !resourceOK,
	}, nil
}

// exclusionReason says why nothing fit, most specific first: a shape that
// matched but lacked schemas beats a recital of what was missing.
func (e *entity) exclusionReason(resourceShape, dsShape, listOnlyShape, lookupShape bool) string {
	switch {
	case resourceShape:
		return "create, read and delete are present but the create request or read success response declares no schema"
	case dsShape:
		return "list and read are present but a success response schema is missing"
	case listOnlyShape:
		return "list is present but its success response declares no schema"
	case lookupShape:
		// Any read reaching here lacks a schema: with one it would have
		// classified as at least a lookup-by-key datasource.
		return "readable by id but the read success response declares no schema"
	}

	present := e.presentRoles()
	if len(present) == 0 {
		return "no operations in classifiable positions"
	}
	return fmt.Sprintf("partial lifecycle (%s) fits no kind", strings.Join(present, ", "))
}

// presentRoles names the roles this entity has, in a fixed order.
func (e *entity) presentRoles() []string {
	var out []string
	for _, r := range []struct {
		name string
		op   *Operation
	}{
		{"create", e.create}, {"read", e.read}, {"update", e.update},
		{"delete", e.del}, {"list", e.list},
	} {
		if r.op != nil {
			out = append(out, r.name)
		}
	}
	return out
}

func opRef(op *Operation) *Op {
	if op == nil {
		return nil
	}
	return &Op{Method: op.Method, Path: op.Path, OperationID: op.OperationID}
}

func extraRefs(ops []*Operation) []Op {
	if len(ops) == 0 {
		return nil
	}
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		out = append(out, *opRef(op))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// trailingParam matches a trailing path parameter, which is what makes a
// path an item path rather than a collection.
var trailingParam = regexp.MustCompile(`\{[^}]+\}$`)

// splitPath reduces an item path to its collection, reporting which it was.
func splitPath(path string) (collection string, isItem bool) {
	if !trailingParam.MatchString(path) {
		return path, false
	}
	trimmed := strings.TrimRight(trailingParam.ReplaceAllString(path, ""), "/")
	if trimmed == "" {
		return path, false
	}
	return trimmed, true
}

// keyForPath derives an entity key from a collection path:
//
//	"/tags"                 -> "tag"
//	"/v7/tests/http-server" -> "v7_tests_http_server"
//
// Embedded parameters are dropped, hyphens become underscores, and the last
// segment — the noun a collection pluralizes — is singularized.
func keyForPath(path string) string {
	parts := make([]string, 0, 4)
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if seg == "" || strings.HasPrefix(seg, "{") {
			continue
		}
		parts = append(parts, strings.ReplaceAll(seg, "-", "_"))
	}
	if len(parts) == 0 {
		return "root"
	}
	last := len(parts) - 1
	parts[last] = singular(parts[last])
	return strings.Join(parts, "_")
}

// singular removes the plural from a collection segment.
//
// Deliberately naive: it handles the endings REST collections actually use
// and lets anything else pass through. A wrong guess produces an odd key,
// which a person can see and correct downstream — inference does not have
// to be right about English, only consistent.
func singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return strings.TrimSuffix(s, "ies") + "y"

	case strings.HasSuffix(s, "sses"), strings.HasSuffix(s, "shes"), strings.HasSuffix(s, "ches"):
		return strings.TrimSuffix(s, "es")

	// Singular words that merely end in "s". Without these, "status" and
	// "analysis" lose a trailing letter and produce keys plausible enough
	// to survive review while wrong.
	case strings.HasSuffix(s, "ss"), strings.HasSuffix(s, "us"), strings.HasSuffix(s, "is"):
		return s

	case strings.HasSuffix(s, "s") && len(s) > 1:
		return strings.TrimSuffix(s, "s")

	default:
		return s
	}
}
