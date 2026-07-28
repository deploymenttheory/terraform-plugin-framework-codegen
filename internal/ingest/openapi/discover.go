// Package openapi turns an OpenAPI document into blueprint candidates.
//
// The hard part is not parsing. It is that an OpenAPI document describes an HTTP
// API and Terraform needs resources, and the two do not correspond. A document
// contains reporting endpoints, bulk operations, instant tests, sub-resource
// collections and actions, and only some of it is a thing with a lifecycle that
// Terraform can own.
//
// So discovery is deliberately split from inference. This file answers "what
// might be a resource, and what does the API offer for it", and reports what it
// rejected and why. Deciding which candidates to adopt is curation, and belongs
// to a person: 315 operations is not 315 resources, and a generator that assumed
// otherwise would produce a provider nobody wants to maintain.
package openapi

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/pb33f/libopenapi"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
)

// Document is a parsed specification.
type Document struct {
	// Version is the API version the document declares.
	Version string
	// Title is the document's title.
	Title string

	model *v3.Document
}

// Load parses an OpenAPI document from disk.
func Load(path string) (*Document, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	doc, err := libopenapi.NewDocument(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	// A unified specification assembled from many component documents can produce
	// several resolution errors at once; libopenapi joins them into one error, so
	// the whole set reaches the caller rather than just the first.
	model, err := doc.BuildV3Model()
	if err != nil {
		return nil, fmt.Errorf("building the model for %s: %w", path, err)
	}

	out := &Document{model: &model.Model}
	if model.Model.Info != nil {
		out.Version = model.Model.Info.Version
		out.Title = model.Model.Info.Title
	}

	return out, nil
}

// Operation is one HTTP operation in the document.
type Operation struct {
	Method      string
	Path        string
	OperationID string
	Summary     string
	Tags        []string
}

// Candidate is a group of operations that might become one Terraform resource.
//
// It is a candidate rather than a resource because the decision is editorial. The
// fields here are the evidence for that decision.
type Candidate struct {
	// Key is the derived resource key, from the collection path.
	Key string
	// CollectionPath is the path without an identifier, e.g. "/tags".
	CollectionPath string
	// ItemPath is the path with an identifier, e.g. "/tags/{id}".
	ItemPath string
	// Tag is the primary OpenAPI tag, which usually names the service.
	Tag string

	Create *Operation
	Read   *Operation
	Update *Operation
	Delete *Operation
	List   *Operation

	// Extra holds operations on these paths that are not plain CRUD: bulk
	// endpoints, actions, sub-resources. They are recorded rather than discarded
	// so curation can see what else the API offers.
	Extra []Operation
}

// Kind classifies what a candidate can become.
type Kind string

const (
	// KindResource has enough of a lifecycle for Terraform to own it.
	KindResource Kind = "resource"
	// KindDataSource can be read but not managed.
	KindDataSource Kind = "dataSource"
	// KindNeither is reporting, actions or something else Terraform cannot model.
	KindNeither Kind = "neither"
)

// Classify decides what a candidate can become, and says why.
//
// The rule for a resource is create plus read. Update and delete are desirable
// but not required: an API with no update needs RequiresReplace on everything,
// and one with no delete is unusual but real. Without create there is nothing to
// manage; without read there is no way to refresh state, so a plan can never be
// correct.
func (c Candidate) Classify() (Kind, string) {
	switch {
	case c.Create != nil && c.Read != nil:
		var missing []string
		if c.Update == nil {
			missing = append(missing, "update")
		}
		if c.Delete == nil {
			missing = append(missing, "delete")
		}
		if len(missing) > 0 {
			return KindResource, "no " + strings.Join(missing, " or ")
		}
		return KindResource, "full lifecycle"

	case c.Read != nil || c.List != nil:
		return KindDataSource, "readable but not creatable"

	case c.Create != nil:
		// Create with no read is a job submission, not a resource: Terraform
		// could make one and would never be able to see it again.
		return KindNeither, "create with no read, which Terraform cannot refresh"

	default:
		return KindNeither, "no create and no read"
	}
}

// pathParam matches a trailing path parameter, which is what distinguishes an
// item path from its collection.
var pathParam = regexp.MustCompile(`\{[^}]+\}$`)

// Discover groups the document's operations into candidates.
//
// Pairing is by path: "/tags" and "/tags/{id}" are one candidate. That is the
// convention REST APIs follow, and where an API does not follow it the candidate
// simply comes out incomplete and is classified accordingly, rather than being
// guessed at.
func (d *Document) Discover() []Candidate {
	if d.model == nil || d.model.Paths == nil {
		return nil
	}

	byCollection := map[string]*Candidate{}

	for pair := d.model.Paths.PathItems.First(); pair != nil; pair = pair.Next() {
		path := pair.Key()
		item := pair.Value()

		collection, isItem := splitPath(path)

		c, ok := byCollection[collection]
		if !ok {
			c = &Candidate{Key: keyForPath(collection), CollectionPath: collection}
			byCollection[collection] = c
		}
		if isItem {
			c.ItemPath = path
		}

		for _, op := range operationsOf(path, item) {
			c.assign(op, isItem)
		}
	}

	out := make([]Candidate, 0, len(byCollection))
	for _, c := range byCollection {
		if c.Tag == "" {
			c.Tag = firstTag(*c)
		}
		out = append(out, *c)
	}

	// Sorted so that listing and any derived output does not depend on map
	// iteration order.
	sort.Slice(out, func(i, j int) bool { return out[i].CollectionPath < out[j].CollectionPath })

	return out
}

// assign files an operation into the CRUD slot it belongs in.
//
// Which slot a method means depends on whether the path carries an identifier: a
// POST to a collection creates, a GET on a collection lists, and a GET on an item
// reads.
func (c *Candidate) assign(op Operation, isItem bool) {
	if c.Tag == "" && len(op.Tags) > 0 {
		c.Tag = op.Tags[0]
	}

	slot := func(dst **Operation) {
		if *dst == nil {
			*dst = &op
			return
		}
		// A second operation for the same slot is not a conflict to resolve
		// silently; it is something curation should see.
		c.Extra = append(c.Extra, op)
	}

	switch {
	case op.Method == "POST" && !isItem:
		slot(&c.Create)
	case op.Method == "GET" && !isItem:
		slot(&c.List)
	case op.Method == "GET" && isItem:
		slot(&c.Read)
	case (op.Method == "PUT" || op.Method == "PATCH") && isItem:
		slot(&c.Update)
	case op.Method == "DELETE" && isItem:
		slot(&c.Delete)
	default:
		c.Extra = append(c.Extra, op)
	}
}

func operationsOf(path string, item *v3.PathItem) []Operation {
	if item == nil {
		return nil
	}

	var out []Operation

	for opPair := item.GetOperations().First(); opPair != nil; opPair = opPair.Next() {
		op := opPair.Value()
		if op == nil {
			continue
		}
		out = append(out, Operation{
			Method:      strings.ToUpper(opPair.Key()),
			Path:        path,
			OperationID: op.OperationId,
			Summary:     op.Summary,
			Tags:        op.Tags,
		})
	}

	// Sorted by method so the order does not depend on the document's ordering.
	sort.Slice(out, func(i, j int) bool { return out[i].Method < out[j].Method })

	return out
}

// splitPath reduces an item path to its collection, reporting which it was.
func splitPath(path string) (collection string, isItem bool) {
	if !pathParam.MatchString(path) {
		return path, false
	}
	trimmed := strings.TrimRight(pathParam.ReplaceAllString(path, ""), "/")
	if trimmed == "" {
		return path, false
	}
	return trimmed, true
}

// keyForPath derives a resource key from a collection path.
//
//	"/tags"              -> "tag"
//	"/v7/tests/http-server" -> "tests_http_server"
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

	// The last segment is the noun, and a collection names it in the plural.
	last := len(parts) - 1
	parts[last] = singular(parts[last])

	return strings.Join(parts, "_")
}

// singular removes the plural from a collection segment.
//
// Deliberately naive: it handles the endings REST collections actually use, and
// anything else passes through. A wrong guess produces a slightly odd key, which
// curation renames -- the key is explicit in the blueprint precisely so that
// inference does not have to be right about English.
func singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return strings.TrimSuffix(s, "ies") + "y"

	case strings.HasSuffix(s, "sses"), strings.HasSuffix(s, "shes"), strings.HasSuffix(s, "ches"):
		return strings.TrimSuffix(s, "es")

	// Singular words that merely end in "s". Without these, "status" becomes
	// "statu" and "analysis" becomes "analysi" -- both plausible-looking enough
	// to survive review and wrong.
	case strings.HasSuffix(s, "ss"), strings.HasSuffix(s, "us"), strings.HasSuffix(s, "is"):
		return s

	case strings.HasSuffix(s, "s") && len(s) > 1:
		return strings.TrimSuffix(s, "s")

	default:
		return s
	}
}

func firstTag(c Candidate) string {
	for _, op := range []*Operation{c.Create, c.Read, c.Update, c.Delete, c.List} {
		if op != nil && len(op.Tags) > 0 {
			return op.Tags[0]
		}
	}
	for _, op := range c.Extra {
		if len(op.Tags) > 0 {
			return op.Tags[0]
		}
	}
	return ""
}
