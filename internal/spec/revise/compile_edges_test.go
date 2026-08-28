package revise

import (
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/correction"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/store"
)

// compile_edges_test.go covers the conditional-edge kinds — validWhen,
// dependsOn (extension and 3.1 dependentRequired forms), mutuallyExclusive and
// validConfiguration — beside the scalar-kind cases in compile_test.go, so
// neither test file crosses the file-size ceiling.

// pinnedCustom imports an arbitrary spec into <root>/spec, for tests that
// need a document tagSpec cannot express (a 3.1 dialect, say).
func pinnedCustom(t *testing.T, spec string) (root, specDir string, lock store.Pin) {
	t.Helper()
	root = t.TempDir()
	specDir = filepath.Join(root, "spec")
	res, err := store.Import(specDir, []byte(spec), "published.yaml")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return root, specDir, res.Lock
}

// proposalContaining returns the one proposed file whose bytes contain sub,
// failing when none or several do.
func proposalContaining(t *testing.T, p Proposals, sub string) string {
	t.Helper()
	var hit string
	for _, w := range p.Proposed {
		raw, err := os.ReadFile(w.Path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), sub) {
			if hit != "" {
				t.Fatalf("more than one proposal contains %q", sub)
			}
			hit = string(raw)
		}
	}
	if hit == "" {
		t.Fatalf("no proposal contains %q; proposed = %+v", sub, p.Proposed)
	}
	return hit
}

// stripWS removes all spaces and newlines, so a structural JSON assertion
// does not hinge on indentation depth.
func stripWS(s string) string {
	return strings.NewReplacer(" ", "", "\n", "").Replace(s)
}

// TestUnit_Propose_ValidConfigurationAggregatesValidWhenEdges: the per-value
// field sets a validConfiguration correction carries are assembled from the
// validWhen observations confirmed against the same gate, which one
// validConfiguration observation alone does not carry.
func TestUnit_Propose_ValidConfigurationAggregatesValidWhenEdges(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		confirmedObs("mode", observe.KindValidConfiguration, []string{"manual", "auto"}, nil, lock.SHA256),
		confirmedObs("port", observe.KindValidWhen, true, &observe.Condition{Attribute: "mode", Equals: "manual"}, lock.SHA256),
		confirmedObs("size", observe.KindValidWhen, true, &observe.Condition{Attribute: "mode", Equals: "auto"}, lock.SHA256),
	)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	got := stripWS(proposalContaining(t, p, "x-tfpfgen-valid-configuration"))
	for _, want := range []string{
		`"discriminator":"mode"`,
		`"auto":["size"]`,
		`"manual":["port"]`,
	} {
		if !contains(got, want) {
			t.Errorf("validConfiguration proposal missing %q:\n%s", want, got)
		}
	}
}

// TestUnit_Propose_DependsOnUsesDependentRequiredOn31: a 3.1 document can say
// the dependency in standard JSON Schema, so the correction prefers
// dependentRequired over the x-tfpfgen extension.
func TestUnit_Propose_DependsOnUsesDependentRequiredOn31(t *testing.T) {
	t.Parallel()
	spec := strings.Replace(tagSpec, "openapi: 3.0.3", "openapi: 3.1.0", 1)
	root, specDir, lock := pinnedCustom(t, spec)
	commitObs(t, root, confirmedObs("port", observe.KindDependsOn, "protocol", nil, lock.SHA256))

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	got := readProposed(t, p)
	for _, want := range []string{
		`"path": "/components/schemas/Tag/dependentRequired"`,
		`(dependentRequired)`,
	} {
		if !contains(got, want) {
			t.Errorf("dependsOn on 3.1 did not use dependentRequired, missing %q:\n%s", want, got)
		}
	}
	if !contains(stripWS(got), `"port":["protocol"]`) {
		t.Errorf("dependentRequired value wrong:\n%s", got)
	}
	if contains(got, "x-tfpfgen-depends-on") {
		t.Errorf("3.1 document still emitted the extension form:\n%s", got)
	}
}

// annotatedTagSpec is tagSpec already carrying the four edge annotations, so
// re-observing their facts converges to AlreadyStated rather than churning.
const annotatedTagSpec = `openapi: 3.0.3
info: {title: quirk, version: "1.0.0"}
paths:
  /tags:
    post:
      operationId: createTag
      requestBody:
        content: {application/json: {schema: {$ref: '#/components/schemas/Tag'}}}
      responses:
        "201": {description: created, content: {application/json: {schema: {$ref: '#/components/schemas/Tag'}}}}
  /tags/{tagId}:
    parameters: [{name: tagId, in: path, required: true, schema: {type: string}}]
    get:
      operationId: getTag
      responses:
        "200": {description: read, content: {application/json: {schema: {$ref: '#/components/schemas/Tag'}}}}
    delete:
      operationId: deleteTag
      responses: {"204": {description: gone}}
components:
  schemas:
    Tag:
      type: object
      x-tfpfgen-mutually-exclusive: [mode, size]
      x-tfpfgen-valid-configuration:
        discriminator: mode
        variants:
          a: []
          b: []
      properties:
        name: {type: string}
        size: {type: string}
        mode: {type: string}
        port:
          type: integer
          x-tfpfgen-valid-when: {property: protocol, equals: tcp}
          x-tfpfgen-depends-on: {requires: protocol}
        protocol: {type: string}
`

// TestUnit_Propose_EdgeKindsConvergeWhenAlreadyStated: an observation whose
// edge the document already declares proposes nothing, which is what makes
// accept-then-reaudit settle.
func TestUnit_Propose_EdgeKindsConvergeWhenAlreadyStated(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedCustom(t, annotatedTagSpec)
	commitObs(t, root,
		confirmedObs("port", observe.KindValidWhen, true, &observe.Condition{Attribute: "protocol", Equals: "tcp"}, lock.SHA256),
		confirmedObs("port", observe.KindDependsOn, "protocol", nil, lock.SHA256),
		confirmedObs("", observe.KindMutuallyExclusive, []string{"size", "mode"}, nil, lock.SHA256),
		confirmedObs("mode", observe.KindValidConfiguration, []string{"a", "b"}, nil, lock.SHA256),
	)
	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 0 {
		t.Fatalf("Proposed = %+v; every edge is already declared", p.Proposed)
	}
	if len(p.AlreadyStated) != 4 {
		t.Fatalf("AlreadyStated = %+v, want all four edges", p.AlreadyStated)
	}
}

// TestUnit_Propose_EdgeKindsReportUnplaceable: an edge whose subject the
// document cannot place is reported, never guessed at.
func TestUnit_Propose_EdgeKindsReportUnplaceable(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		// A mutually-exclusive set of one is not a set.
		confirmedObs("", observe.KindMutuallyExclusive, []string{"mode"}, nil, lock.SHA256),
		// A validConfiguration on a field the schema does not declare.
		confirmedObs("ghost", observe.KindValidConfiguration, []string{"x"}, nil, lock.SHA256),
	)
	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 0 {
		t.Fatalf("Proposed = %+v; neither edge is placeable", p.Proposed)
	}
	if len(p.Unplaceable) != 2 {
		t.Fatalf("Unplaceable = %+v, want both edges", p.Unplaceable)
	}
}

// TestUnit_Propose_ValidWhenFalseStatesNothing: a validWhen whose value is not
// true confirms the document's own reading and compiles to nothing.
func TestUnit_Propose_ValidWhenFalseStatesNothing(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		confirmedObs("port", observe.KindValidWhen, false, &observe.Condition{Attribute: "protocol", Equals: "tcp"}, lock.SHA256))
	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 0 || len(p.AlreadyStated) != 1 {
		t.Fatalf("Proposed=%+v AlreadyStated=%+v", p.Proposed, p.AlreadyStated)
	}
}

// TestUnit_Propose_DependentRequiredExtendsAndConverges: on 3.1, a second
// dependency extends the existing dependentRequired map, and re-observing a
// declared one converges.
func TestUnit_Propose_DependentRequiredExtendsAndConverges(t *testing.T) {
	t.Parallel()
	spec := strings.Replace(tagSpec, "openapi: 3.0.3", "openapi: 3.1.0", 1)
	spec = strings.Replace(spec,
		"    Tag:\n      type: object\n",
		"    Tag:\n      type: object\n      dependentRequired: {size: [mode]}\n", 1)
	root, specDir, lock := pinnedCustom(t, spec)

	// A new dependency on a schema that already has a dependentRequired map:
	// it extends the map with a new key rather than replacing it.
	commitObs(t, root, confirmedObs("port", observe.KindDependsOn, "protocol", nil, lock.SHA256))
	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	got := readProposed(t, p)
	if !contains(got, `"path": "/components/schemas/Tag/dependentRequired/port"`) {
		t.Errorf("did not extend the existing dependentRequired map:\n%s", got)
	}

	// A dependency the map already states converges.
	commitObs(t, root, confirmedObs("size", observe.KindDependsOn, "mode", nil, lock.SHA256))
	p, err = Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	stated := false
	for _, n := range p.AlreadyStated {
		if n.Kind == observe.KindDependsOn && n.Attribute == "size" {
			stated = true
		}
	}
	if !stated {
		t.Errorf("a declared dependency did not converge: %+v", p.AlreadyStated)
	}
}

func TestUnit_Compile_AValidConfigurationGatesOnAStringWithSomethingToGate(t *testing.T) {
	t.Parallel()
	_, specDir, lock := pinnedTree(t)
	state, entities, err := revisedState(specDir, filepath.Join(specDir, correction.DirName))
	if err != nil {
		t.Fatal(err)
	}
	comp := &compiler{entities: entities, state: state, vetoes: map[[2]string]bool{},
		variants: map[[2]string]map[string][]string{{"tag", "mode"}: {"manual": {"port"}}}, restated: map[string]string{}}

	// port is an integer: the generated gate compares a string.
	res, err := comp.compile(confirmedObs("port", observe.KindValidConfiguration, []string{"1", "2"}, nil, lock.SHA256))
	if err != nil {
		t.Fatal(err)
	}
	if res.category != catUnplaceable || !strings.Contains(res.reason, "integer property") {
		t.Errorf("an integer gate compiled to %+v, want it unplaceable", res)
	}

	// size has values but no field is valid only under one of them.
	res, err = comp.compile(confirmedObs("size", observe.KindValidConfiguration, []string{"small", "large"}, nil, lock.SHA256))
	if err != nil {
		t.Fatal(err)
	}
	if res.category != catAlreadyStated || !strings.Contains(res.reason, "no field is valid only under") {
		t.Errorf("a gate with nothing to gate compiled to %+v, want nothing", res)
	}

	// mode gates port under manual: the correction is the one variant set.
	res, err = comp.compile(confirmedObs("mode", observe.KindValidConfiguration, []string{"manual", "auto"}, nil, lock.SHA256))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.operations) != 1 {
		t.Errorf("a real gate compiled to %+v, want its correction", res)
	}
}
