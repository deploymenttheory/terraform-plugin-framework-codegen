package revise

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/store"
)

// acceptedFiles lists the accepted corrections currently in corrections/,
// the counterpart to proposedFiles for what a round's acceptance committed.
func acceptedFiles(t *testing.T, specDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(specDir, correction.DirName))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), correction.Suffix) {
			out = append(out, e.Name())
		}
	}
	return out
}

// TestUnit_Propose_SecondRoundOrdinalsContinuePastAcceptedOnes is Bug 1's
// reproduction. A round that proposes after an earlier round was accepted must
// number past it, never reissuing an ordinal whose acceptance would then
// clobber committed evidence.
//
// The rounds are staged by what a run has observed so far, which is what makes
// a second round happen at all: within one round the placement order in
// observe.Sort already puts the field-adding correction ahead of everything
// that annotates the field.
func TestUnit_Propose_SecondRoundOrdinalsContinuePastAcceptedOnes(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	// Round one knows only that the document omits aid.
	commitObs(t, root,
		confirmedObs("aid", observe.KindUndocumentedFieldInSpec, "number", nil, lock.SHA256),
	)

	p1, err := Propose(specDir)
	if err != nil {
		t.Fatalf("round-one Propose: %v", err)
	}
	if len(p1.Proposed) != 1 || p1.Proposed[0].Kind != observe.KindUndocumentedFieldInSpec {
		t.Fatalf("round one Proposed = %+v, want only the undocumentedFieldInSpec", p1.Proposed)
	}
	round1Accepted := filepath.Base(p1.Proposed[0].Path)
	acceptAll(t, specDir)

	// A later run measures what the server puts in that field.
	commitObs(t, root,
		confirmedObs("aid", observe.KindUndocumentedFieldInSpec, "number", nil, lock.SHA256),
		confirmedObs("aid", observe.KindServerDefault, float64(100), nil, lock.SHA256),
	)

	// Round two: the serverDefault must not reuse the ordinal round one
	// already committed.
	p2, err := Propose(specDir)
	if err != nil {
		t.Fatalf("round-two Propose: %v", err)
	}
	if len(p2.Proposed) != 1 || p2.Proposed[0].Kind != observe.KindServerDefault {
		t.Fatalf("round two Proposed = %+v, want the now-placeable serverDefault", p2.Proposed)
	}
	round2Proposed := filepath.Base(p2.Proposed[0].Path)
	if round2Proposed == round1Accepted {
		t.Fatalf("round two proposed %s, which collides with the accepted %s", round2Proposed, round1Accepted)
	}
	// No proposed filename may equal any accepted filename.
	accepted := map[string]bool{}
	for _, name := range acceptedFiles(t, specDir) {
		accepted[name] = true
	}
	for _, name := range proposedFiles(t, specDir) {
		if accepted[name] {
			t.Errorf("proposed %s also exists accepted; accepting it would clobber committed evidence", name)
		}
	}

	// Accepting round two must leave round one's correction untouched.
	firstBody, err := os.ReadFile(filepath.Join(specDir, correction.DirName, round1Accepted))
	if err != nil {
		t.Fatal(err)
	}
	acceptAll(t, specDir)
	afterBody, err := os.ReadFile(filepath.Join(specDir, correction.DirName, round1Accepted))
	if err != nil {
		t.Fatalf("round one's correction %s vanished after accepting round two: %v", round1Accepted, err)
	}
	if string(firstBody) != string(afterBody) {
		t.Errorf("round one's correction %s was overwritten by round two", round1Accepted)
	}
	if !strings.Contains(string(afterBody), "undocumentedFieldInSpec") {
		t.Errorf("round one's correction no longer holds the undocumentedFieldInSpec it committed:\n%s", afterBody)
	}
}

// An auto-accepted correction whose evidence recompiles with a moved value
// overwrites its own file in place — the shared ordinal has advanced past it,
// so without the in-place overwrite a second file would accrete beside the
// first and both defaults would apply.
func TestUnit_Propose_AnAutoAcceptedValueThatMovesOverwritesItsOwnFile(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, confirmedObs("mode", observe.KindServerDefault, "auto", nil, lock.SHA256))

	p1, err := ProposeWith(specDir, Options{AutoAccept: []string{"serverDefault"}})
	if err != nil {
		t.Fatalf("first ProposeWith: %v", err)
	}
	if len(p1.AutoAccepted) != 1 {
		t.Fatalf("AutoAccepted = %+v, want the serverDefault", p1.AutoAccepted)
	}
	firstPath := p1.AutoAccepted[0].Path

	// The same observation, its default moved to a new constant.
	commitObs(t, root, confirmedObs("mode", observe.KindServerDefault, "manual", nil, lock.SHA256))
	p2, err := ProposeWith(specDir, Options{AutoAccept: []string{"serverDefault"}})
	if err != nil {
		t.Fatalf("second ProposeWith: %v", err)
	}
	if len(p2.AutoAccepted) != 1 || p2.AutoAccepted[0].Path != firstPath {
		t.Fatalf("AutoAccepted = %+v, want the same file %s overwritten in place", p2.AutoAccepted, firstPath)
	}
	autos := 0
	for _, name := range acceptedFiles(t, specDir) {
		if strings.HasPrefix(name, "auto-") {
			autos++
		}
	}
	if autos != 1 {
		t.Errorf("found %d auto- files, want exactly one — the moved value duplicated its correction", autos)
	}
	raw, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "manual") {
		t.Errorf("the auto correction was not updated to the moved value:\n%s", raw)
	}
}

// tagInfoSpec is a full-lifecycle entity in miniature, whose documented schema
// omits the aid and builtIn fields the live API
// carries, whose color has a server-applied default, whose objectType is
// immutable, and whose type enum documents a value the API rejects.
const tagInfoSpec = `openapi: 3.0.3
info:
  title: tag surface
  version: 1.0.0
paths:
  /tags:
    post:
      operationId: createTag
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/TagInfo'
      responses:
        "201":
          description: created
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/TagInfo'
    get:
      operationId: listTags
      responses:
        "200":
          description: listed
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/TagInfo'
  /tags/{tagId}:
    parameters:
      - name: tagId
        in: path
        required: true
        schema:
          type: string
    get:
      operationId: getTag
      responses:
        "200":
          description: read
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/TagInfo'
    put:
      operationId: updateTag
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/TagInfo'
      responses:
        "200":
          description: updated
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/TagInfo'
    delete:
      operationId: deleteTag
      responses:
        "204":
          description: deleted
components:
  schemas:
    TagInfo:
      type: object
      properties:
        name:
          type: string
        color:
          type: string
        objectType:
          type: string
        type:
          type: string
          enum:
            - static
            - dynamic
`

// pinnedSpec imports an arbitrary spec into <root>/spec, the multi-schema
// counterpart to pinnedTree.
func pinnedSpec(t *testing.T, spec string) (root, specDir string, lock store.Lock) {
	t.Helper()
	root = t.TempDir()
	specDir = filepath.Join(root, "spec")
	res, err := store.Import(specDir, []byte(spec), "published.yaml")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return root, specDir, res.Lock
}

// TestIntegration_Revise_TagConvergesAcrossRounds covers corrections that only
// become placeable once an earlier round is accepted: proposed and accepted
// round by round until a re-propose yields nothing, then materialized. The
// revised document must carry the added fields with their defaults, the
// corrected enum and the annotations — so accepted evidence is neither
// overwritten nor applied against a node that does not exist yet.
func TestIntegration_Revise_TagConvergesAcrossRounds(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedSpec(t, tagInfoSpec)
	commitObs(t, root,
		confirmedObs("aid", observe.KindUndocumentedFieldInSpec, "number", nil, lock.SHA256),
		confirmedObs("builtIn", observe.KindUndocumentedFieldInSpec, "boolean", nil, lock.SHA256),
		confirmedObs("aid", observe.KindServerDefault, float64(4530), nil, lock.SHA256),
		confirmedObs("builtIn", observe.KindServerDefault, false, nil, lock.SHA256),
		confirmedObs("color", observe.KindServerDefault, "#A7EB10", nil, lock.SHA256),
		confirmedObs("objectType", observe.KindImmutable, true, nil, lock.SHA256),
		confirmedObs("type", observe.KindValues,
			observe.Values{Rejected: []string{"dynamic"}}, nil, lock.SHA256),
		confirmedObs("", observe.KindUpdateStyle, "put-full", nil, lock.SHA256),
	)

	// Accept round after round until a propose settles with nothing new.
	rounds := 0
	for {
		p, err := Propose(specDir)
		if err != nil {
			t.Fatalf("round %d Propose: %v", rounds+1, err)
		}
		if len(p.Proposed) == 0 {
			break
		}
		acceptAll(t, specDir)
		rounds++
		if rounds > 5 {
			t.Fatal("the loop did not converge within five rounds")
		}
	}
	// One round is enough: observe.Sort places the field-adding corrections
	// ahead of the serverDefaults that annotate those same fields, so every
	// one of them is placeable the first time it is compiled.
	if rounds != 1 {
		t.Fatalf("converged in %d round(s), want one", rounds)
	}

	// Every accepted ordinal is unique — nothing was clobbered along the way.
	seen := map[string]bool{}
	for _, name := range acceptedFiles(t, specDir) {
		if seen[name] {
			t.Errorf("duplicate accepted correction %s", name)
		}
		seen[name] = true
	}

	res, err := Materialize(specDir)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(res.Applied) != 8 {
		t.Errorf("Applied = %d corrections, want the 8 accepted across the rounds", len(res.Applied))
	}

	revised, err := os.ReadFile(filepath.Join(specDir, OutputName))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(revised, &document); err != nil {
		t.Fatalf("revised.yaml is not usable YAML: %v", err)
	}
	tagInfo := document["components"].(map[string]any)["schemas"].(map[string]any)["TagInfo"].(map[string]any)
	properties := tagInfo["properties"].(map[string]any)

	// A value the server fills is recorded as x-tfpfgen-server-default on the
	// property — the fact that makes the generated attribute Optional and
	// Computed. It does not go into OpenAPI's own `default`, which states what
	// the document declares and steers nothing downstream.
	aid, ok := properties["aid"].(map[string]any)
	if !ok || aid["type"] != "number" || aid["x-tfpfgen-server-default"] != 4530 {
		t.Errorf("aid = %v, want an added number field the server fills with 4530", properties["aid"])
	}
	builtIn, ok := properties["builtIn"].(map[string]any)
	if !ok || builtIn["type"] != "boolean" || builtIn["x-tfpfgen-server-default"] != false {
		t.Errorf("builtIn = %v, want an added boolean field the server fills with false", properties["builtIn"])
	}
	if def := properties["color"].(map[string]any)["x-tfpfgen-server-default"]; def != "#A7EB10" {
		t.Errorf("color server-default = %v, want #A7EB10", def)
	}
	if co := properties["objectType"].(map[string]any)["x-tfpfgen-create-only"]; co != true {
		t.Errorf("objectType create-only = %v, want true", co)
	}
	enum, ok := properties["type"].(map[string]any)["enum"].([]any)
	if !ok || len(enum) != 1 || enum[0] != "static" {
		t.Errorf("type.enum = %v, want [static] with dynamic removed", properties["type"].(map[string]any)["enum"])
	}
	put := document["paths"].(map[string]any)["/tags/{tagId}"].(map[string]any)["put"].(map[string]any)
	if style := put["x-tfpfgen-update-style"]; style != "put-full" {
		t.Errorf("update style = %v, want put-full", style)
	}

	// The loop is a fixed point: a further propose adds nothing.
	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("converged re-Propose: %v", err)
	}
	if len(p.Proposed)+len(p.AutoAccepted) != 0 {
		t.Errorf("re-propose wrote %+v %+v; the loop must converge", p.Proposed, p.AutoAccepted)
	}
}
