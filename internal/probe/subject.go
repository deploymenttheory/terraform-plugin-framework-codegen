package probe

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// ErrNotProbeable is returned for a resource the prober cannot work on at all.
var ErrNotProbeable = errors.New("resource cannot be probed")

// Subject is one resource, flattened into what a probe needs.
//
// A probe never sees a blueprint. It sees paths, JSON keys and a fixture, because it
// works at the level of HTTP and JSON -- and because the join key between what it
// observes and what merge writes is WireBinding.JSONPath, not the Terraform attribute
// name. Keeping Terraform naming out of the probe layer is what stops a probe from
// accidentally reporting a fact against the wrong field.
type Subject struct {
	// Resource is the blueprint key, used to address facts and notes.
	Resource string

	// CollectionTemplate and ItemTemplate are the blueprint's PathTemplate values,
	// e.g. "/v7/tags" and "/v7/tags/{id}".
	CollectionTemplate string
	ItemTemplate       string

	// Create, Read, Update and Delete are the HTTP methods and success codes the
	// blueprint records, or nil where the API has no such operation. A resource with
	// no read cannot be probed at all; one with no update simply has fewer probes
	// that apply to it.
	Create *Op
	Read   *Op
	Update *Op
	Delete *Op

	// Fields is every attribute flattened to a JSON path, ordered so a report reads
	// the same way twice.
	Fields []Field

	// Policy is what the blueprint currently claims. The prober does not trust it --
	// settling it is the job - but it is carried so a probe can report agreement or
	// disagreement rather than just an observation.
	Policy blueprint.ResourcePolicy

	// IDField is the JSON key a created object's identifier arrives under.
	//
	// Derived from the blueprint's ID binding *attribute*, mapped through that attribute's
	// WireBinding.JSONPath. Binding.ID.FromCreate is unusable here: it is an accessor on an SDK
	// type ("created.ID"), and the prober speaks raw JSON.
	//
	// Without it neither pass of the sweeper can build a DELETE URL, which is why CanMutate
	// refuses when it is empty.
	IDField string

	// NameField is the writable string field a created object's name prefix goes in.
	//
	// Empty means the resource cannot carry a sweepable marker, which is why
	// mutating probes refuse it: no name means no prefix sweep means no way to
	// guarantee cleanup, and creating objects that cannot be found again is not a
	// trade worth making.
	NameField string
}

// Op is one HTTP operation, as much as a probe needs of it.
type Op struct {
	Method       string
	PathTemplate string
	SuccessCodes []int
}

// Succeeded reports whether a status is one this operation declares as success.
//
// Falls back to the 2xx range when the blueprint records no codes, because an
// unpopulated SuccessCodes is common in an inferred blueprint and treating it as
// "nothing succeeds" would make every probe fail for the wrong reason.
func (o *Op) Succeeded(status int) bool {
	if o == nil {
		return false
	}
	if len(o.SuccessCodes) == 0 {
		return status >= 200 && status < 300
	}
	for _, c := range o.SuccessCodes {
		if c == status {
			return true
		}
	}
	return false
}

// Field is one attribute, addressed the way the API addresses it.
type Field struct {
	// JSONPath is the API's name, dotted for a field inside a nested object, e.g.
	// "assignments.type". This is the fact join key.
	JSONPath string
	// Attribute is the Terraform name, carried only so a report can be read by
	// somebody looking at the schema. No probe logic keys on it.
	Attribute string

	Kind                     blueprint.TypeKind
	ComputedOptionalRequired blueprint.ComputedOptionalRequired

	// Writable is false for an attribute the blueprint already knows is read-only,
	// either because it is Computed or because SkipExpand is set. Those are excluded
	// from the probes that send values, which is both a correctness matter and the
	// single biggest saving in request count.
	Writable bool

	// Enum holds the values the specification documents, if any. The prober checks
	// them; it never turns them into a validator.
	Enum []string

	// Behavior is what is already recorded. A probe may skip a field whose fact it
	// would only be re-deriving, and merge needs to know what it is overwriting.
	Behavior blueprint.Behavior
}

// pathParam matches a path parameter, e.g. "{id}".
var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// SubjectOf flattens a blueprint resource.
//
// It refuses rather than degrades for the two cases where probing is meaningless: a
// resource with no read operation cannot be observed at all, and one with no item path
// has nothing to address. Both are better reported here, once, than as a pile of
// identical failures from every probe in the catalogue.
func SubjectOf(bp blueprint.Blueprint, res blueprint.Resource) (Subject, error) {
	if res.Binding.Read == nil {
		return Subject{}, fmt.Errorf("%w: %s has no read operation, so nothing can be observed",
			ErrNotProbeable, res.Key)
	}

	item := res.Binding.Read.PathTemplate
	if item == "" {
		return Subject{}, fmt.Errorf("%w: %s records no path template on its read operation; "+
			"run `tfpluginframeworkgen ingest` against a snapshot, or author binding.read.pathTemplate",
			ErrNotProbeable, res.Key)
	}

	subj := Subject{
		Resource:     res.Key,
		ItemTemplate: item,
		Create:       opOf(res.Binding.Create),
		Read:         opOf(res.Binding.Read),
		Update:       opOf(res.Binding.Update),
		Delete:       opOf(res.Binding.Delete),
		Policy:       res.Policy,
	}

	// The collection is the item path with its trailing parameter removed, or the
	// create operation's own path where one exists. Preferring create is deliberate:
	// an API whose create posts somewhere other than the item path's parent is
	// unusual but real, and deriving it would get that case wrong silently.
	switch {
	case res.Binding.Create != nil && res.Binding.Create.PathTemplate != "":
		subj.CollectionTemplate = res.Binding.Create.PathTemplate
	default:
		subj.CollectionTemplate = collectionOf(item)
	}

	subj.Fields = fieldsOf(res.Attributes, "")
	subj.NameField = nameFieldOf(subj.Fields)
	subj.IDField = idFieldOf(res, subj.Fields)

	return subj, nil
}

func opOf(o *blueprint.Operation) *Op {
	if o == nil {
		return nil
	}
	return &Op{Method: o.HTTPMethod, PathTemplate: o.PathTemplate, SuccessCodes: o.SuccessCodes}
}

// collectionOf strips a trailing path parameter.
func collectionOf(item string) string {
	trimmed := strings.TrimRight(pathParam.ReplaceAllString(item, ""), "/")
	if trimmed == "" {
		return item
	}
	return trimmed
}

// fieldsOf flattens attributes, descending one level into nested objects.
//
// NestedAttributeObject children are addressed with a dotted JSON path, which is how a probe reports
// a fact about a field inside an object without needing to know Terraform's nesting
// rules. Dropped attributes are skipped: they are not in the schema, so a fact about
// one would have nowhere to go.
func fieldsOf(attrs []blueprint.Attribute, prefix string) []Field {
	var out []Field

	for _, a := range attrs {
		if a.Drop {
			continue
		}

		path := a.Wire.JSONPath
		if path == "" {
			// Without a JSON path there is no join key, so a fact about this
			// attribute could not be merged back. Skipped rather than guessed at
			// from the Terraform name, which would silently address the wrong field
			// on any API that does not use snake_case on the wire.
			continue
		}
		if prefix != "" {
			path = prefix + "." + path
		}

		out = append(out, Field{
			JSONPath:                 path,
			Attribute:                a.Name,
			Kind:                     a.Type.Kind,
			ComputedOptionalRequired: a.ComputedOptionalRequired,
			Writable:                 writable(a),
			Enum:                     a.Type.Enum,
			Behavior:                 a.Behavior,
		})

		if a.Type.NestedObject != nil {
			out = append(out, fieldsOf(a.Type.NestedObject.Attributes, path)...)
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].JSONPath < out[j].JSONPath })

	return out
}

// writable reports whether the blueprint thinks an attribute is sent on write.
//
// Computed-only attributes and those with SkipExpand are excluded from every probe
// that sends a value. That is not an optimisation: sending a value for a field the
// generated provider will never send would produce a fact about a code path that does
// not exist.
func writable(a blueprint.Attribute) bool {
	if a.Wire.SkipExpand {
		return false
	}
	return a.ComputedOptionalRequired != blueprint.Computed
}

// nameFieldOf picks the field a created object's name prefix goes in.
//
// Preference order is deliberate. A dedicated name field is ideal; a label or key is
// the usual alternative on APIs that have no "name"; a description is the last resort
// because it is nearly always free-text and writable. Anything else risks putting a
// marker in a field with semantics -- a URL, a hostname -- where the API would reject
// it or, worse, accept it and do something.
func nameFieldOf(fields []Field) string {
	preferred := []string{"name", "label", "key", "title", "description"}

	byPath := make(map[string]Field, len(fields))
	for _, f := range fields {
		// Only a top-level writable string can carry the marker: a nested field is
		// not reliably present on every object, and the sweep has to be able to read
		// it back from a list response.
		if strings.Contains(f.JSONPath, ".") || !f.Writable || f.Kind != blueprint.KindString {
			continue
		}
		byPath[f.JSONPath] = f
	}

	for _, want := range preferred {
		if _, ok := byPath[want]; ok {
			return want
		}
	}

	return ""
}

// idFieldOf resolves the JSON key a created object's identifier arrives under.
//
// The blueprint names a Terraform *attribute*; what a probe needs is the wire name, so the
// attribute is looked up and its JSONPath taken. Falling back to a field literally called "id"
// covers an inferred blueprint that never populated the binding, which is the common case
// before anybody has curated one.
func idFieldOf(res blueprint.Resource, fields []Field) string {
	want := res.Binding.ID.Attribute
	if want == "" {
		want = "id"
	}

	// Top-level only, and this is not a refinement -- it is the whole correctness of the lookup.
	// A nested object's child attribute carries its own Terraform name, and "id" is the commonest
	// name there is: the pilot's tag has assignments[].id alongside the resource's own id. Fields
	// are sorted by JSON path, so "assignments.id" sorts first and an unqualified match picked it.
	//
	// The consequence was not a visible failure. Every create read its identifier out of
	// "assignments.id", found nothing, and reported the object as created-without-an-identifier;
	// the sweeper's prefix pass then looked for the same path on every list item, matched none of
	// them, and deleted nothing. A live run against a real tenant is what surfaced it, because no
	// test fixture had a nested object with an id in it.
	for _, f := range fields {
		if strings.Contains(f.JSONPath, ".") {
			continue
		}
		if f.Attribute == want {
			return f.JSONPath
		}
	}

	// The attribute the binding names does not exist, or has no JSON path. A field whose wire
	// name is literally "id" is the overwhelmingly common shape, so it is worth one more look
	// before giving up -- and giving up is safe, because CanMutate refuses on an empty IDField.
	for _, f := range fields {
		if f.JSONPath == "id" {
			return f.JSONPath
		}
	}

	return ""
}

// WritableFields returns the fields a probe may send values for.
func (s Subject) WritableFields() []Field {
	var out []Field
	for _, f := range s.Fields {
		if f.Writable {
			out = append(out, f)
		}
	}
	return out
}

// ReadableFields returns the fields that appear on a read.
//
// Everything not marked SkipFlatten is assumed readable until a probe observes
// otherwise -- which is exactly the assumption R2 and C2 exist to test.
func (s Subject) ReadableFields() []Field {
	return s.Fields
}

// Field finds a field by JSON path.
func (s Subject) Field(jsonPath string) (Field, bool) {
	for _, f := range s.Fields {
		if f.JSONPath == jsonPath {
			return f, true
		}
	}
	return Field{}, false
}

// CanMutate reports whether mutating probes may run against this subject, and why not
// when they may not.
//
// Every refusal is about cleanup rather than capability. Without a create there is nothing to
// exercise; without a delete nothing created could be removed; without a field that can carry
// the name prefix there is no way to find a stranded object again, and the prefix sweep is the
// only backstop that works after a crash between sending a create and seeing its response;
// without an identifier field nothing can be addressed for deletion at all.
func (s Subject) CanMutate() (bool, string) {
	switch {
	case s.Create == nil:
		return false, "the resource has no create operation"
	case s.Delete == nil:
		return false, "the resource has no delete operation, so nothing created could be cleaned up"
	case s.NameField == "":
		return false, "no writable top-level string field can carry the name prefix, " +
			"so a created object could not be found again by the sweeper"
	case s.IDField == "":
		return false, "no attribute carries the created object's identifier, so neither pass " +
			"of the sweeper could build a request to delete it"
	default:
		return true, ""
	}
}
