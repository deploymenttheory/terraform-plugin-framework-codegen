package probe

import (
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// pilotResource is a blueprint resource shaped like the committed pilot: a read on an
// item path, a create on a collection, and a nested object.
func pilotResource() blueprint.Resource {
	return blueprint.Resource{
		Key: "tag",
		Binding: blueprint.ResourceBinding{
			Create: &blueprint.Operation{HTTPMethod: "POST", PathTemplate: "/v7/tags", SuccessCodes: []int{201}},
			Read:   &blueprint.Operation{HTTPMethod: "GET", PathTemplate: "/v7/tags/{id}", SuccessCodes: []int{200}},
			Update: &blueprint.Operation{HTTPMethod: "PUT", PathTemplate: "/v7/tags/{id}", SuccessCodes: []int{200}},
			Delete: &blueprint.Operation{HTTPMethod: "DELETE", PathTemplate: "/v7/tags/{id}", SuccessCodes: []int{204}},
		},
		Attributes: []blueprint.Attribute{
			{
				Name: "id", Presence: blueprint.Computed,
				Type: blueprint.AttrType{Kind: blueprint.KindString},
				Wire: blueprint.WireBinding{JSONPath: "id"},
			},
			{
				Name: "key", Presence: blueprint.Required,
				Type: blueprint.AttrType{Kind: blueprint.KindString},
				Wire: blueprint.WireBinding{JSONPath: "key"},
			},
			{
				Name: "assignments", Presence: blueprint.Optional,
				Type: blueprint.AttrType{
					Kind: blueprint.KindSetNested,
					Nested: &blueprint.Nested{
						GoTypeName: "M",
						Attributes: []blueprint.Attribute{{
							Name: "type", Presence: blueprint.Optional,
							Type: blueprint.AttrType{Kind: blueprint.KindString},
							Wire: blueprint.WireBinding{JSONPath: "type"},
						}},
					},
				},
				Wire: blueprint.WireBinding{JSONPath: "assignments"},
			},
		},
	}
}

func TestUnit_Probe_SubjectOf(t *testing.T) {
	t.Parallel()

	subj, err := SubjectOf(blueprint.Blueprint{}, pilotResource())
	if err != nil {
		t.Fatalf("SubjectOf: %v", err)
	}

	if subj.Resource != "tag" {
		t.Errorf("Resource = %q", subj.Resource)
	}
	if subj.CollectionTemplate != "/v7/tags" {
		t.Errorf("CollectionTemplate = %q, want /v7/tags", subj.CollectionTemplate)
	}
	if subj.ItemTemplate != "/v7/tags/{id}" {
		t.Errorf("ItemTemplate = %q", subj.ItemTemplate)
	}

	// A nested child is addressed with a dotted JSON path, which is how a fact about a
	// field inside an object finds its way back without the probe knowing Terraform's
	// nesting rules.
	if _, ok := subj.Field("assignments.type"); !ok {
		t.Errorf("the nested child was not flattened; got %v", paths(subj.Fields))
	}

	// Computed attributes are excluded from the probes that send values: sending one
	// would produce a fact about a code path the generated provider does not have.
	if f, _ := subj.Field("id"); f.Writable {
		t.Error("a computed attribute must not be writable")
	}
	if f, _ := subj.Field("key"); !f.Writable {
		t.Error("a required attribute must be writable")
	}

	if subj.NameField != "key" {
		t.Errorf("NameField = %q, want key", subj.NameField)
	}
}

func paths(fields []Field) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.JSONPath)
	}
	return out
}

// TestUnit_Probe_SubjectOfRefusals: the two cases where probing is meaningless are
// refused once, here, rather than as a pile of identical failures from every probe.
func TestUnit_Probe_SubjectOfRefusals(t *testing.T) {
	t.Parallel()

	noRead := pilotResource()
	noRead.Binding.Read = nil

	_, err := SubjectOf(blueprint.Blueprint{}, noRead)
	if !errors.Is(err, ErrNotProbeable) {
		t.Errorf("error = %v, want ErrNotProbeable", err)
	}
	if err != nil && !strings.Contains(err.Error(), "no read operation") {
		t.Errorf("the error should say why: %v", err)
	}

	noPath := pilotResource()
	noPath.Binding.Read.PathTemplate = ""

	_, err = SubjectOf(blueprint.Blueprint{}, noPath)
	if !errors.Is(err, ErrNotProbeable) {
		t.Errorf("error = %v, want ErrNotProbeable", err)
	}
	// The message has to say what to do about it, not just that it happened.
	if err != nil && !strings.Contains(err.Error(), "pathTemplate") {
		t.Errorf("the error should name the field to author: %v", err)
	}
}

// TestUnit_Probe_SubjectSkipsUnjoinableAttributes: an attribute with no JSON path has no
// join key, so a fact about it could never be merged back.
//
// Skipped rather than guessed at from the Terraform name, which would silently address
// the wrong field on any API that does not use snake_case on the wire.
func TestUnit_Probe_SubjectSkipsUnjoinableAttributes(t *testing.T) {
	t.Parallel()

	res := pilotResource()
	res.Attributes = append(res.Attributes, blueprint.Attribute{
		Name: "orphan", Presence: blueprint.Optional,
		Type: blueprint.AttrType{Kind: blueprint.KindString},
	})

	subj, err := SubjectOf(blueprint.Blueprint{}, res)
	if err != nil {
		t.Fatalf("SubjectOf: %v", err)
	}

	for _, f := range subj.Fields {
		if f.Attribute == "orphan" {
			t.Error("an attribute with no JSON path must be skipped, not carried with an empty key")
		}
	}

	// And a dropped attribute is not in the schema, so a fact about it would have
	// nowhere to go either.
	dropped := pilotResource()
	dropped.Attributes[1].Drop = true

	subj, err = SubjectOf(blueprint.Blueprint{}, dropped)
	if err != nil {
		t.Fatalf("SubjectOf: %v", err)
	}
	if _, ok := subj.Field("key"); ok {
		t.Error("a dropped attribute must be skipped")
	}
}

// TestUnit_Probe_NameFieldPreference: the marker must go somewhere free-text, never in a
// field with semantics.
func TestUnit_Probe_NameFieldPreference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		paths []string
		want  string
	}{
		{"prefers name", []string{"description", "key", "name"}, "name"},
		{"falls back to label", []string{"description", "label"}, "label"},
		{"then key", []string{"description", "key"}, "key"},
		{"description is the last resort", []string{"description"}, "description"},
		{"nothing suitable", []string{"hostname", "url"}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var fields []Field
			for _, p := range tc.paths {
				fields = append(fields, Field{
					JSONPath: p, Kind: blueprint.KindString, Writable: true,
				})
			}

			if got := nameFieldOf(fields); got != tc.want {
				t.Errorf("nameFieldOf(%v) = %q, want %q", tc.paths, got, tc.want)
			}
		})
	}

	// A nested field cannot carry the marker: it is not reliably present on every
	// object, and the sweeper has to read it back from a list response.
	nested := []Field{{JSONPath: "meta.name", Kind: blueprint.KindString, Writable: true}}
	if got := nameFieldOf(nested); got != "" {
		t.Errorf("a nested field must not be chosen, got %q", got)
	}

	// Nor a read-only one, obviously.
	computed := []Field{{JSONPath: "name", Kind: blueprint.KindString, Writable: false}}
	if got := nameFieldOf(computed); got != "" {
		t.Errorf("a non-writable field must not be chosen, got %q", got)
	}
}

// TestUnit_Probe_CanMutate: all three refusals are about cleanup rather than capability.
func TestUnit_Probe_CanMutate(t *testing.T) {
	t.Parallel()

	ok := testSubject()
	if can, why := ok.CanMutate(); !can {
		t.Errorf("a full subject should be mutable: %s", why)
	}

	tests := []struct {
		name   string
		mutate func(*Subject)
		want   string
	}{
		{"no create", func(s *Subject) { s.Create = nil }, "no create operation"},
		{"no delete", func(s *Subject) { s.Delete = nil }, "cleaned up"},
		{"no name field", func(s *Subject) { s.NameField = "" }, "name prefix"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			subj := testSubject()
			tc.mutate(&subj)

			can, why := subj.CanMutate()
			if can {
				t.Fatal("expected mutation to be refused")
			}
			if !strings.Contains(why, tc.want) {
				t.Errorf("reason = %q, want it to mention %q", why, tc.want)
			}
		})
	}
}

// TestUnit_Probe_OpSucceeded: an inferred blueprint frequently records no success codes,
// and treating that as "nothing succeeds" would make every probe fail for the wrong reason.
func TestUnit_Probe_OpSucceeded(t *testing.T) {
	t.Parallel()

	declared := &Op{SuccessCodes: []int{200, 204}}
	if !declared.Succeeded(204) {
		t.Error("204 is declared and should succeed")
	}
	if declared.Succeeded(201) {
		t.Error("201 is not declared and should not succeed")
	}

	// No declared codes falls back to the 2xx range.
	bare := &Op{}
	if !bare.Succeeded(201) {
		t.Error("with no declared codes, 201 should succeed")
	}
	if bare.Succeeded(404) {
		t.Error("404 should never succeed")
	}

	var absent *Op
	if absent.Succeeded(200) {
		t.Error("a nil operation succeeds at nothing")
	}
}

func TestUnit_Probe_ResolvePath(t *testing.T) {
	t.Parallel()

	// Path templates in the wild use {testId}, {agentId}, {aid} and worse, so matching a
	// literal "{id}" would leave a brace in the URL.
	tests := map[string]string{
		"/v7/tags/{id}":             "/v7/tags/42",
		"/v7/tests/{testId}":        "/v7/tests/42",
		"/v7/agents/{agentId}":      "/v7/agents/42",
		"/v7/tags/{id}/assignments": "/v7/tags/42/assignments",
	}

	for template, want := range tests {
		if got := resolvePath(template, "42"); got != want {
			t.Errorf("resolvePath(%q) = %q, want %q", template, got, want)
		}
	}
}

func TestUnit_Probe_CollectionPrefersCreatePath(t *testing.T) {
	t.Parallel()

	// An API whose create posts somewhere other than the item path's parent is unusual
	// but real, and deriving the collection would get it wrong silently.
	res := pilotResource()
	res.Binding.Create.PathTemplate = "/v7/tags/bulk"

	subj, err := SubjectOf(blueprint.Blueprint{}, res)
	if err != nil {
		t.Fatalf("SubjectOf: %v", err)
	}
	if subj.CollectionTemplate != "/v7/tags/bulk" {
		t.Errorf("CollectionTemplate = %q, want the create path", subj.CollectionTemplate)
	}

	// With no create at all, it is derived from the item path.
	noCreate := pilotResource()
	noCreate.Binding.Create = nil

	subj, err = SubjectOf(blueprint.Blueprint{}, noCreate)
	if err != nil {
		t.Fatalf("SubjectOf: %v", err)
	}
	if subj.CollectionTemplate != "/v7/tags" {
		t.Errorf("CollectionTemplate = %q, want /v7/tags", subj.CollectionTemplate)
	}
}
