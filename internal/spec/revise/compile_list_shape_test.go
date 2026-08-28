package revise

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// compile_list_shape_test.go covers the listResponseShape kind's own
// behaviour — convergence, placement, determinism and the auto-accept
// vocabulary — beside the exact-correction case in compile_test.go.

// listShapeObs builds a confirmed listResponseShape observation on tag.
func listShapeObs(shape observe.ListResponseShape, specHash string) observe.Observation {
	return confirmedObs("", observe.KindListResponseShape, shape, nil, specHash)
}

// TestUnit_Propose_ListResponseShapeConvergesOnceStated: the correction the
// first round proposes, once accepted, states the fact the observation
// carries — so a re-audit of the same API proposes nothing and the loop
// settles instead of reissuing the annotation forever.
func TestUnit_Propose_ListResponseShapeConvergesOnceStated(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	shape := observe.ListResponseShape{Envelope: "wrapped", Key: "tags", Pagination: "cursor"}
	commitObs(t, root, listShapeObs(shape, lock.SHA256))

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 1 {
		t.Fatalf("Proposed = %+v, want the list-shape correction", p.Proposed)
	}
	acceptAll(t, specDir)

	p, err = Propose(specDir)
	if err != nil {
		t.Fatalf("second Propose: %v", err)
	}
	if len(p.Proposed)+len(p.AutoAccepted) != 0 {
		t.Errorf("re-propose wrote %+v %+v; the stated shape must compile to nothing", p.Proposed, p.AutoAccepted)
	}
	if len(p.AlreadyStated) != 1 ||
		p.AlreadyStated[0].Reason != "the document already declares this list response shape" {
		t.Fatalf("AlreadyStated = %+v, want the stated-shape note", p.AlreadyStated)
	}

	// And the revised document really carries the key, loadable and typed.
	revised, err := Materialize(specDir)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	raw, err := os.ReadFile(revised.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	document, err := specmodel.Load(raw)
	if err != nil {
		t.Fatalf("the revised document does not load: %v", err)
	}
	if got, ok := listOpShape(document); !ok || got != (specmodel.ListResponseShape{
		Envelope: "wrapped", Key: "tags", Pagination: "cursor",
	}) {
		t.Errorf("revised list operation carries %+v (present=%v), want the observed shape", got, ok)
	}
}

// listOpShape reads the shape extension off the document's list operation.
func listOpShape(document *specmodel.Document) (specmodel.ListResponseShape, bool) {
	for pi := range document.Paths {
		if document.Paths[pi].Path != "/tags" {
			continue
		}
		for oi := range document.Paths[pi].Operations {
			operation := &document.Paths[pi].Operations[oi]
			if operation.Method == "GET" {
				return operation.Extensions.ListResponseShape()
			}
		}
	}
	return specmodel.ListResponseShape{}, false
}

// TestUnit_Propose_ListResponseShapeIsDeterministic: two runs of the same
// observation against the same document produce byte-identical corrections,
// map-valued operation included.
func TestUnit_Propose_ListResponseShapeIsDeterministic(t *testing.T) {
	t.Parallel()
	shape := observe.ListResponseShape{Envelope: "wrapped", Key: "tags", Pagination: "offset"}

	run := func() string {
		root, specDir, lock := pinnedTree(t)
		commitObs(t, root, listShapeObs(shape, lock.SHA256))
		p, err := Propose(specDir)
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		return readProposed(t, p)
	}
	if first, second := run(), run(); first != second {
		t.Errorf("two runs disagree:\n first: %s\nsecond: %s", first, second)
	}
}

// TestUnit_Propose_ListResponseShapeReportsWhatItCannotPlace: an entity with
// no list operation has nowhere to carry the annotation, and a wrapped
// envelope with no key would compile to a document the loader refuses — both
// are reported rather than written.
func TestUnit_Propose_ListResponseShapeReportsWhatItCannotPlace(t *testing.T) {
	t.Parallel()

	t.Run("no list operation", func(t *testing.T) {
		t.Parallel()
		root, specDir, lock := pinnedComposite(t)
		o := listShapeObs(observe.ListResponseShape{Envelope: "bare", Pagination: "none"}, lock.SHA256)
		o.Entity = "ping" // an action: POST only, no collection GET
		commitObs(t, root, o)

		p, err := Propose(specDir)
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(p.Proposed) != 0 {
			t.Errorf("Proposed = %+v, want nothing placeable", p.Proposed)
		}
		if len(p.Unplaceable) != 1 || !strings.Contains(p.Unplaceable[0].Reason, "no list operation") {
			t.Fatalf("Unplaceable = %+v, want the missing list operation named", p.Unplaceable)
		}
	})

	t.Run("wrapped with no key", func(t *testing.T) {
		t.Parallel()
		root, specDir, lock := pinnedTree(t)
		commitObs(t, root, listShapeObs(observe.ListResponseShape{Envelope: "wrapped", Pagination: "none"}, lock.SHA256))

		p, err := Propose(specDir)
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(p.Proposed) != 0 {
			t.Errorf("Proposed = %+v; a keyless wrapper must not be written", p.Proposed)
		}
		if len(p.Unplaceable) != 1 || !strings.Contains(p.Unplaceable[0].Reason, "names no envelope key") {
			t.Fatalf("Unplaceable = %+v, want the missing envelope key named", p.Unplaceable)
		}
	})

	t.Run("bare with a key", func(t *testing.T) {
		t.Parallel()
		root, specDir, lock := pinnedTree(t)
		commitObs(t, root, listShapeObs(
			observe.ListResponseShape{Envelope: "bare", Key: "tags", Pagination: "none"}, lock.SHA256))

		p, err := Propose(specDir)
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(p.Proposed) != 0 {
			t.Errorf("Proposed = %+v; a bare envelope with a key says two things at once", p.Proposed)
		}
		if len(p.Unplaceable) != 1 || !strings.Contains(p.Unplaceable[0].Reason, "bare but names the envelope key") {
			t.Fatalf("Unplaceable = %+v, want the contradiction named", p.Unplaceable)
		}
	})
}

// TestUnit_Compile_ListResponseShapeRefusesADriftedVocabulary: observe.Read
// refuses an envelope or pagination style outside the closed sets, so these
// values can only arrive if the observation and document vocabularies drift
// apart. The compiler still refuses them rather than writing a document
// specmodel would then decline to load — checked against the compiler
// directly, since the committed-file path cannot express them.
func TestUnit_Compile_ListResponseShapeRefusesADriftedVocabulary(t *testing.T) {
	t.Parallel()
	_, specDir, _ := pinnedTree(t)
	state, entities, err := revisedState(specDir, filepath.Join(specDir, correction.DirName))
	if err != nil {
		t.Fatalf("revisedState: %v", err)
	}
	comp := &compiler{entities: entities, state: state}

	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"an envelope neither wrapped nor bare",
			map[string]any{"envelope": "nested", "key": "tags", "pagination": "none"},
			"neither"},
		{"a pagination style outside the closed set",
			map[string]any{"envelope": "bare", "pagination": "spiral"},
			"not one of cursor"},
		{"a value that is not a record at all", 42,
			""}, // an error, not a note — see below
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := comp.compile(confirmedObs("", observe.KindListResponseShape, testCase.value, nil, ""))
			if testCase.want == "" {
				if err == nil {
					t.Fatalf("compile accepted %v: %+v", testCase.value, res)
				}
				if !strings.Contains(err.Error(), "not a list-response-shape record") {
					t.Errorf("error = %v, want it to name the unusable value", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if res.category != catUnplaceable || !strings.Contains(res.reason, testCase.want) {
				t.Errorf("compiled = %+v, want an unplaceable note containing %q", res, testCase.want)
			}
		})
	}
}

// TestUnit_Propose_ListResponseShapeIsAutoAcceptable: the kind is now part of
// the audit.auto_accept vocabulary, and an auto-accepted shape lands in the
// accepted directory rather than waiting for review.
func TestUnit_Propose_ListResponseShapeIsAutoAcceptable(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, listShapeObs(observe.ListResponseShape{Envelope: "wrapped", Key: "tags", Pagination: "none"}, lock.SHA256))

	p, err := ProposeWith(specDir, Options{AutoAccept: []string{string(observe.KindListResponseShape)}})
	if err != nil {
		t.Fatalf("ProposeWith: %v", err)
	}
	if len(p.AutoAccepted) != 1 || p.AutoAccepted[0].Kind != observe.KindListResponseShape {
		t.Fatalf("AutoAccepted = %+v, want the list-shape correction", p.AutoAccepted)
	}
}

// TestUnit_CompilableKinds_IsASortedCopyOfTheVocabulary: what config
// validation consumes is this package's own auto-accept vocabulary, sorted
// and copied — every entry checkAutoAccept admits, and no shared backing
// array a caller could edit under it.
func TestUnit_CompilableKinds_IsASortedCopyOfTheVocabulary(t *testing.T) {
	t.Parallel()
	kinds := CompilableKinds()
	for i := 1; i < len(kinds); i++ {
		if kinds[i-1] >= kinds[i] {
			t.Fatalf("CompilableKinds() is not sorted: %q before %q", kinds[i-1], kinds[i])
		}
	}
	if len(kinds) != len(compilableKinds) {
		t.Errorf("CompilableKinds() has %d entries, the vocabulary %d", len(kinds), len(compilableKinds))
	}
	// A copy: mutating the answer must not reach the package's own list.
	kinds[0] = "mutated"
	if CompilableKinds()[0] == "mutated" {
		t.Error("CompilableKinds() hands out the package's own slice")
	}
	for _, k := range compilableKinds {
		if err := checkAutoAccept([]string{k}); err != nil {
			t.Errorf("checkAutoAccept refuses its own vocabulary entry %q: %v", k, err)
		}
	}
}
