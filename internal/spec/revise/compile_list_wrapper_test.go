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

// compile_list_wrapper_test.go covers the listWrapper kind's own
// behaviour — convergence, placement, determinism and the auto-accept
// vocabulary — beside the exact-correction case in compile_test.go.

// listWrapperObs builds a confirmed listWrapper observation on tag.
func listWrapperObs(shape observe.ListWrapper, specHash string) observe.Observation {
	return confirmedObs("", observe.KindListWrapper, shape, nil, specHash)
}

// TestUnit_Propose_ListWrapperConvergesOnceStated: the correction the
// first round proposes, once accepted, states the fact the observation
// carries — so a re-audit of the same API proposes nothing and the loop
// settles instead of reissuing the annotation forever.
func TestUnit_Propose_ListWrapperConvergesOnceStated(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	shape := observe.ListWrapper{Wrapped: true, Key: "tags"}
	commitObs(t, root, listWrapperObs(shape, lock.SHA256))

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 1 {
		t.Fatalf("Proposed = %+v, want the list-wrapper correction", p.Proposed)
	}
	acceptAll(t, specDir)

	p, err = Propose(specDir)
	if err != nil {
		t.Fatalf("second Propose: %v", err)
	}
	if len(p.Proposed)+len(p.AutoAccepted) != 0 {
		t.Errorf("re-propose wrote %+v %+v; the stated wrapping must compile to nothing", p.Proposed, p.AutoAccepted)
	}
	if len(p.AlreadyStated) != 1 ||
		p.AlreadyStated[0].Reason != "the document already declares this list wrapping" {
		t.Fatalf("AlreadyStated = %+v, want the stated-wrapping note", p.AlreadyStated)
	}

	// And the revised document really carries the key, loadable and typed.
	revised, err := WriteRevision(specDir)
	if err != nil {
		t.Fatalf("WriteRevision: %v", err)
	}
	raw, err := os.ReadFile(revised.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	document, err := specmodel.Load(raw)
	if err != nil {
		t.Fatalf("the revised document does not load: %v", err)
	}
	if got, ok := listOpWrapper(document); !ok || got != (specmodel.ListWrapper{Wrapped: true, Key: "tags"}) {
		t.Errorf("revised list operation carries %+v (present=%v), want the observed wrapping", got, ok)
	}
}

// listOpWrapper reads the wrapper extension off the document's list operation.
func listOpWrapper(document *specmodel.Document) (specmodel.ListWrapper, bool) {
	for pi := range document.Paths {
		if document.Paths[pi].Path != "/tags" {
			continue
		}
		for oi := range document.Paths[pi].Operations {
			operation := &document.Paths[pi].Operations[oi]
			if operation.Method == "GET" {
				return operation.Extensions.ListWrapper()
			}
		}
	}
	return specmodel.ListWrapper{}, false
}

// TestUnit_Propose_ListWrapperIsDeterministic: two runs of the same
// observation against the same document produce byte-identical corrections,
// map-valued operation included.
func TestUnit_Propose_ListWrapperIsDeterministic(t *testing.T) {
	t.Parallel()
	shape := observe.ListWrapper{Wrapped: true, Key: "tags"}

	run := func() string {
		root, specDir, lock := pinnedTree(t)
		commitObs(t, root, listWrapperObs(shape, lock.SHA256))
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

// TestUnit_Propose_ListWrapperReportsWhatItCannotPlace: an entity with
// no list operation has nowhere to carry the annotation, and a wrapped
// envelope with no key would compile to a document the loader refuses — both
// are reported rather than written.
func TestUnit_Propose_ListWrapperReportsWhatItCannotPlace(t *testing.T) {
	t.Parallel()

	t.Run("no list operation", func(t *testing.T) {
		t.Parallel()
		root, specDir, lock := pinnedComposite(t)
		o := listWrapperObs(observe.ListWrapper{}, lock.SHA256)
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

	// A wrapper that is wrapped-with-no-key, or unwrapped-with-one, cannot
	// reach the committed file at all: observe.Write refuses it. The
	// compiler refuses it too, and that path is exercised directly by
	// TestUnit_Compile_ListWrapperRefusesADriftedVocabulary.
}

// TestUnit_Compile_ListWrapperRefusesADriftedVocabulary: observe.Read refuses
// a wrapper record that names no key when wrapped, or a pagination style
// outside the closed set, so these values can only arrive if the observation
// and document vocabularies drift apart. The compiler still refuses them
// rather than writing a document specmodel would then decline to load —
// checked against the compiler directly, since the committed-file path cannot
// express them.
func TestUnit_Compile_ListWrapperRefusesADriftedVocabulary(t *testing.T) {
	t.Parallel()
	_, specDir, _ := pinnedTree(t)
	state, entities, err := revisedState(specDir, filepath.Join(specDir, correction.DirName))
	if err != nil {
		t.Fatalf("revisedState: %v", err)
	}
	comp := &compiler{entities: entities, state: state}

	cases := []struct {
		name  string
		kind  observe.Kind
		value any
		want  string
	}{
		{"wrapped but naming no key", observe.KindListWrapper,
			map[string]any{"wrapped": true},
			"names no key"},
		{"unwrapped but naming one", observe.KindListWrapper,
			map[string]any{"wrapped": false, "key": "tags"},
			"unwrapped but names the key"},
		{"a pagination style outside the closed set", observe.KindListPagination,
			"spiral",
			"not one of cursor"},
		{"a wrapper that is not a record at all", observe.KindListWrapper, 42,
			""}, // an error, not a note — see below
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			res, err := comp.compile(confirmedObs("", testCase.kind, testCase.value, nil, ""))
			if testCase.want == "" {
				if err == nil {
					t.Fatalf("compile accepted %v: %+v", testCase.value, res)
				}
				if !strings.Contains(err.Error(), "not a list-wrapper record") {
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

// TestUnit_Propose_ListWrapperIsAutoAcceptable: the kind is now part of
// the audit.auto_accept vocabulary, and an auto-accepted shape lands in the
// accepted directory rather than waiting for review.
func TestUnit_Propose_ListWrapperIsAutoAcceptable(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, listWrapperObs(observe.ListWrapper{Wrapped: true, Key: "tags"}, lock.SHA256))

	p, err := ProposeWith(specDir, Options{AutoAccept: []string{string(observe.KindListWrapper)}})
	if err != nil {
		t.Fatalf("ProposeWith: %v", err)
	}
	if len(p.AutoAccepted) != 1 || p.AutoAccepted[0].Kind != observe.KindListWrapper {
		t.Fatalf("AutoAccepted = %+v, want the list-wrapper correction", p.AutoAccepted)
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

// TestUnit_Compile_ASharedSiteKeepsAnotherEntitysDefault pins convergence
// where a property's schema is shared between entities that each observe
// their own default: a site another entity's correction wrote, or another
// observation in the same run compiled, proposes nothing rather than
// replacing the value and being replaced in turn — while an observation
// revisiting its own correction still follows the moved value.
func TestUnit_Compile_ASharedSiteKeepsAnotherEntitysDefault(t *testing.T) {
	t.Parallel()
	_, specDir, lock := pinnedTree(t)
	state, entities, err := revisedState(specDir, filepath.Join(specDir, correction.DirName))
	if err != nil {
		t.Fatal(err)
	}
	o := confirmedObs("size", observe.KindServerDefault, "small", nil, lock.SHA256)
	path := "/components/schemas/Tag/properties/size/" + specmodel.ExtServerDefault

	fresh := func(restated map[string]string) *compiler {
		return &compiler{entities: entities, state: state, vetoes: map[[2]string]bool{},
			variants: map[[2]string]map[string][]string{}, restated: restated}
	}

	// Written by another entity's correction: nothing to propose.
	res, err := fresh(map[string]string{path: "audit/observations/other.observations.json#0123456789abcdef"}).compile(o)
	if err != nil {
		t.Fatal(err)
	}
	if res.category != catAlreadyStated || !strings.Contains(res.reason, "another entity's value") {
		t.Errorf("another entity's site compiled to %+v, want the shared-site note", res)
	}

	// Written by this observation's own correction: the value moved.
	res, err = fresh(map[string]string{path: evidenceReference(o)}).compile(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.operations) != 1 {
		t.Errorf("a moved default compiled to %+v, want its replacement", res)
	}

	// Compiled earlier in this run with a different value: nothing to propose.
	comp := fresh(nil)
	if _, err := comp.compile(o); err != nil {
		t.Fatal(err)
	}
	later := confirmedObs("size", observe.KindServerDefault, "large", nil, lock.SHA256)
	res, err = comp.compile(later)
	if err != nil {
		t.Fatal(err)
	}
	if res.category != catAlreadyStated || !strings.Contains(res.reason, "this run already compiled") {
		t.Errorf("a second default on the site compiled to %+v, want the shared-site note", res)
	}
}
