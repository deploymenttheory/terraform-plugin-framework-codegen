package specmodel

import "testing"

// taggedOperationsSpec declares tags at both levels the document may carry
// them: the top-level list that names and describes each group, and the
// per-operation list that places an operation in one. The third top-level
// entry names nothing, which a document is free to write and which groups
// no operation.
const taggedOperationsSpec = `
openapi: 3.0.3
info:
  title: Widget API
  version: "1.0.0"
tags:
  - name: widgets
    description: Everything that is a widget.
  - name: audit-log
  - description: an entry with no name
paths:
  /widgets:
    get:
      operationId: listWidgets
      tags: [widgets, audit-log]
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string}
`

// untaggedSpec declares neither list. A document is under no obligation to
// tag anything, and the loader must say so with an empty answer rather than
// inventing a group.
const untaggedSpec = `
openapi: 3.0.3
info:
  title: Widget API
  version: "1.0.0"
paths:
  /widgets:
    get:
      operationId: listWidgets
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string}
`

// TestUnit_Specmodel_OperationTagsSurviveTheLoad pins the order as well as
// the membership: the tags are the vendor's own grouping of its API, and a
// reader that reordered them would put an operation in a different group
// than the document did.
func TestUnit_Specmodel_OperationTagsSurviveTheLoad(t *testing.T) {
	t.Parallel()
	document, err := Load([]byte(taggedOperationsSpec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	operations := document.Operations()
	if len(operations) != 1 {
		t.Fatalf("loaded %d operations, want 1", len(operations))
	}
	got := operations[0].Tags
	want := []string{"widgets", "audit-log"}
	if len(got) != len(want) {
		t.Fatalf("Tags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Tags[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestUnit_Specmodel_TopLevelTagsCarryTheirDescriptions holds the list a
// document uses to say what each group is. A description is optional, and an
// entry naming nothing is dropped rather than carried as a group no
// operation can refer to.
func TestUnit_Specmodel_TopLevelTagsCarryTheirDescriptions(t *testing.T) {
	t.Parallel()
	document, err := Load([]byte(taggedOperationsSpec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(document.Tags) != 2 {
		t.Fatalf("Tags = %+v, want the two named entries", document.Tags)
	}
	if document.Tags[0].Name != "widgets" || document.Tags[0].Description != "Everything that is a widget." {
		t.Errorf("Tags[0] = %+v", document.Tags[0])
	}
	if document.Tags[1].Name != "audit-log" || document.Tags[1].Description != "" {
		t.Errorf("Tags[1] = %+v, want the undescribed entry", document.Tags[1])
	}
}

// TestUnit_Specmodel_ADocumentWithoutTagsLoadsNone proves the absent case is
// empty rather than an error: tagging is optional, and a document that
// declares none is as valid as one that does.
func TestUnit_Specmodel_ADocumentWithoutTagsLoadsNone(t *testing.T) {
	t.Parallel()
	document, err := Load([]byte(untaggedSpec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(document.Tags) != 0 {
		t.Errorf("Tags = %+v, want none", document.Tags)
	}
	operations := document.Operations()
	if len(operations) != 1 {
		t.Fatalf("loaded %d operations, want 1", len(operations))
	}
	if len(operations[0].Tags) != 0 {
		t.Errorf("operation Tags = %v, want none", operations[0].Tags)
	}
}
