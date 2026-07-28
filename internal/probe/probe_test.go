package probe

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// testSubject is a subject with enough shape for the cost functions to be meaningful.
func testSubject() Subject {
	return Subject{
		Resource:           "tag",
		CollectionTemplate: "/tags",
		ItemTemplate:       "/tags/{id}",
		Create:             &Op{Method: "POST", PathTemplate: "/tags", SuccessCodes: []int{201}},
		Read:               &Op{Method: "GET", PathTemplate: "/tags/{id}", SuccessCodes: []int{200}},
		Update:             &Op{Method: "PUT", PathTemplate: "/tags/{id}", SuccessCodes: []int{200}},
		Delete:             &Op{Method: "DELETE", PathTemplate: "/tags/{id}", SuccessCodes: []int{204}},
		NameField:          "key",
		Fields: []Field{
			{JSONPath: "id", Attribute: "id", Kind: blueprint.KindString, Presence: blueprint.Computed},
			{JSONPath: "key", Attribute: "key", Kind: blueprint.KindString, Presence: blueprint.Required, Writable: true},
			{JSONPath: "value", Attribute: "value", Kind: blueprint.KindString, Presence: blueprint.Optional, Writable: true},
		},
	}
}

// TestUnit_Probe_CatalogueIsWellFormed guards the mistakes a hand-maintained registry
// invites, in both directions.
//
// A probe registered twice silently shadows another. A probe with no name cannot be
// selected by -only or addressed in a fact. And a probe whose Kind disagrees with the
// list it was registered in would make the mutating tier run under read-only gating,
// which is the one bug in this package that could touch somebody's production tenant.
func TestUnit_Probe_CatalogueIsWellFormed(t *testing.T) {
	t.Parallel()

	subj := testSubject()
	entries := Catalogue(subj)

	if len(entries) == 0 {
		t.Fatal("the catalogue is empty")
	}

	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Name == "" {
			t.Error("a probe has no name")
		}
		if seen[e.Name] {
			t.Errorf("%q appears twice in the catalogue", e.Name)
		}
		seen[e.Name] = true

		if e.Kind != KindRead && e.Kind != KindMutating {
			t.Errorf("%s: kind = %q, want read or mutating", e.Name, e.Kind)
		}
		if e.Cost < 0 || e.Creates < 0 {
			t.Errorf("%s: negative cost (%d requests, %d creates)", e.Name, e.Cost, e.Creates)
		}
		// A read probe that reports creating something would be a registration error
		// with real consequences: it would be run without the gate.
		if e.Kind == KindRead && e.Creates != 0 {
			t.Errorf("%s is registered as read-only but claims %d creates", e.Name, e.Creates)
		}
	}

	// Read-first ordering, because that is the order they run in.
	sawMutating := false
	for _, e := range entries {
		if e.Kind == KindMutating {
			sawMutating = true
		} else if sawMutating {
			t.Errorf("%s (read) is listed after a mutating probe; the catalogue must be read-first", e.Name)
		}
	}
}

// TestUnit_Probe_KindMatchesRegistration checks each probe's own Kind against the list it
// was put in.
//
// Separate from the catalogue test because it reaches the interfaces directly: the
// catalogue derives Kind from which slice a probe is in, so it cannot detect a probe that
// reports the wrong one.
func TestUnit_Probe_KindMatchesRegistration(t *testing.T) {
	t.Parallel()

	for _, p := range ReadProbes("") {
		if p.Kind() != KindRead {
			t.Errorf("%s is registered as read-only but reports Kind %q", p.Name(), p.Kind())
		}
	}
	for _, p := range MutatingProbes("") {
		if p.Kind() != KindMutating {
			t.Errorf("%s is registered as mutating but reports Kind %q", p.Name(), p.Kind())
		}
	}
}

// TestUnit_Probe_EveryProbeIsRegisteredAndUnimplemented pins the honest state of Phase
// 4.1: the catalogue exists, and nothing pretends to work.
//
// The reverse of the usual assertion, and it matters. A probe whose body silently returned
// an empty result would look like a probe that ran and found nothing, and that is
// indistinguishable in a report from a real observation of absence.
func TestUnit_Probe_EveryProbeIsRegisteredAndUnimplemented(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	subj := testSubject()

	for _, p := range ReadProbes("") {
		t.Run(p.Name(), func(t *testing.T) {
			t.Parallel()
			_, err := p.Observe(ctx, readOnly{}, subj)
			if !errors.Is(err, errNotImplemented) {
				t.Errorf("Observe returned %v; an unbuilt probe must not look like one that found nothing", err)
			}
		})
	}

	for _, p := range MutatingProbes("") {
		t.Run(p.Name(), func(t *testing.T) {
			t.Parallel()
			_, err := p.Exercise(ctx, &MutatingSession{}, subj)
			if !errors.Is(err, errNotImplemented) {
				t.Errorf("Exercise returned %v; an unbuilt probe must not look like one that found nothing", err)
			}
		})
	}
}

// TestUnit_Probe_CostsScaleWithTheSubject: a cost that ignores its subject is a cost
// nobody can budget against.
func TestUnit_Probe_CostsScaleWithTheSubject(t *testing.T) {
	t.Parallel()

	small := testSubject()

	large := testSubject()
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		large.Fields = append(large.Fields, Field{
			JSONPath: name, Attribute: name,
			Kind: blueprint.KindString, Presence: blueprint.Optional, Writable: true,
		})
	}

	smallReq, smallCreates := TotalCost(small, "")
	largeReq, largeCreates := TotalCost(large, "")

	if largeReq <= smallReq {
		t.Errorf("more writable fields should cost more requests: %d vs %d", largeReq, smallReq)
	}
	if largeCreates <= smallCreates {
		t.Errorf("more writable fields should cost more creates: %d vs %d", largeCreates, smallCreates)
	}
}

// TestUnit_Probe_CostsDropWhenAnOperationIsAbsent: a resource with no update cannot be
// probed for immutability or update style, and its budget must say so.
func TestUnit_Probe_CostsDropWhenAnOperationIsAbsent(t *testing.T) {
	t.Parallel()

	with := testSubject()
	without := testSubject()
	without.Update = nil

	for _, name := range []string{"write.immutability", "write.update-style"} {
		withCost, _ := TotalCost(with, name)
		withoutCost, _ := TotalCost(without, name)

		if withCost == 0 {
			t.Errorf("%s should cost something when update exists", name)
		}
		if withoutCost != 0 {
			t.Errorf("%s costs %d with no update operation, want 0", name, withoutCost)
		}
	}
}

func TestUnit_Probe_LookupAndFilter(t *testing.T) {
	t.Parallel()

	if _, ok := Lookup("read.volatile"); !ok {
		t.Error("read.volatile should be registered")
	}
	if _, ok := Lookup("write.immutability"); !ok {
		t.Error("write.immutability should be registered")
	}
	if _, ok := Lookup("telepathy"); ok {
		t.Error("an unregistered name must not resolve")
	}

	if got := ReadProbes("read.volatile"); len(got) != 1 {
		t.Errorf("filtering to one read probe gave %d", len(got))
	}
	if got := ReadProbes("write.immutability"); got != nil {
		t.Error("filtering read probes by a mutating name must give nothing")
	}
	if got := MutatingProbes("write.immutability"); len(got) != 1 {
		t.Errorf("filtering to one mutating probe gave %d", len(got))
	}
	if got := MutatingProbes("nope"); got != nil {
		t.Error("filtering by an unknown name must give nothing")
	}
}

// TestUnit_Probe_EveryProbeDocumentsHowItCanBeWrong is the test that keeps the catalogue
// honest as it grows.
//
// Every probe's doc comment has to state how it can be wrong, because a probe whose
// failure mode nobody wrote down is a probe whose facts nobody can weigh. Checking the
// source rather than a field on the struct is deliberate: the alternative is a
// FailureModes string nobody keeps in step with the code, and a doc comment is where a
// reader is already looking.
func TestUnit_Probe_EveryProbeDocumentsHowItCanBeWrong(t *testing.T) {
	t.Parallel()

	src := catalogueSource(t)

	for _, e := range Catalogue(testSubject()) {
		// The type name is the identifier registered in init(); find its doc comment by
		// locating the "type <name> struct" declaration and reading backwards.
		typeName, ok := typeNameFor(src, e.Name)
		if !ok {
			t.Errorf("%s: could not find its type declaration in catalogue.go", e.Name)
			continue
		}

		doc := docCommentBefore(src, "type "+typeName+" struct{}")
		if doc == "" {
			t.Errorf("%s (%s): has no doc comment", e.Name, typeName)
			continue
		}

		if !strings.Contains(doc, "How it can be wrong") {
			t.Errorf("%s (%s): its doc comment does not say how it can be wrong", e.Name, typeName)
		}
	}
}
