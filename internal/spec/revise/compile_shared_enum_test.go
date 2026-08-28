package revise

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/store"
)

func TestUnit_Compile_ASharedEnumKeepsAValueAnotherEntityAccepts(t *testing.T) {
	t.Parallel()
	_, specDir, lock := pinnedTree(t)
	state, entities, err := revisedState(specDir, filepath.Join(specDir, correction.DirName))
	if err != nil {
		t.Fatal(err)
	}
	fresh := func() *compiler {
		return &compiler{entities: entities, state: state, vetoes: map[[2]string]bool{},
			variants: map[[2]string]map[string][]string{}, restated: map[string]string{}}
	}
	rejection := confirmedObs("color", observe.KindValues, observe.Values{Rejected: []string{"blue"}}, nil, lock.SHA256)
	acceptance := confirmedObs("color", observe.KindValues, observe.Values{Accepted: []string{"blue"}}, nil, lock.SHA256)
	site := "/components/schemas/Tag/properties/color"

	// Nothing accepted the value: the rejection removes it.
	comp := fresh()
	comp.enumAccepted = comp.acceptedValueSites(nil, nil)
	res, err := comp.compile(rejection)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.operations) != 2 || res.operations[1].Op != "remove" {
		t.Fatalf("an unshared rejection compiled to %+v, want the removal", res)
	}

	// Another observation on the site accepts it: the rejection proposes nothing.
	comp = fresh()
	comp.enumAccepted = comp.acceptedValueSites([]observe.Observation{acceptance}, nil)
	if !comp.enumAccepted[site]["blue"] {
		t.Fatalf("acceptedValueSites = %v, want blue accepted at %s", comp.enumAccepted, site)
	}
	res, err = comp.compile(rejection)
	if err != nil {
		t.Fatal(err)
	}
	if res.category != catAlreadyStated || !strings.Contains(res.reason, "another entity sharing the enum") {
		t.Errorf("a rejection of an accepted value compiled to %+v, want the shared-enum note", res)
	}

	// An accepted correction added it: the same.
	comp = fresh()
	comp.enumAccepted = comp.acceptedValueSites(nil, map[string]map[string]bool{site: {"blue": true}})
	res, err = comp.compile(rejection)
	if err != nil {
		t.Fatal(err)
	}
	if res.category != catAlreadyStated {
		t.Errorf("a rejection of a value an accepted correction added compiled to %+v, want nothing", res)
	}

	// A rejection alongside an unrelated addition keeps the addition and
	// says why the removal is missing.
	comp = fresh()
	comp.enumAccepted = comp.acceptedValueSites([]observe.Observation{acceptance}, nil)
	mixed := confirmedObs("color", observe.KindValues, observe.Values{Rejected: []string{"blue"}, Accepted: []string{"green"}}, nil, lock.SHA256)
	res, err = comp.compile(mixed)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.operations) != 1 || res.operations[0].Op != "add" || !strings.Contains(res.justification, "stays declared") {
		t.Errorf("a mixed observation compiled to %+v, want the addition alone with the kept value named", res)
	}
}

func TestUnit_Propose_AcceptedEnumAdditionsAreReadBySite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	corr := correction.Correction{
		Justification: "test",
		Operations: []correction.Operation{
			{Op: "add", Path: "/components/schemas/Interval/enum/-", Value: "60"},
			{Op: "test", Path: "/components/schemas/Interval/enum/0", Value: "300"},
			{Op: "add", Path: "/components/schemas/Other/x-tfpfgen-values", Value: true},
		},
	}
	raw, err := json.Marshal(corr)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "001-entity.correction.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := acceptedEnumAdditions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got["/components/schemas/Interval"]["60"] {
		t.Fatalf("acceptedEnumAdditions = %v, want 60 at the Interval site alone", got)
	}
}

func TestUnit_Compile_ANormalisedTimestampWithdrawsTheDateTimeFormat(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	doc := strings.Replace(tagSpec, "        protocol:\n          type: string\n",
		"        protocol:\n          type: string\n        expires:\n          type: string\n          format: date-time\n", 1)
	res, err := store.Import(specDir, []byte(doc), "published.yaml")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	state, entities, err := revisedState(specDir, filepath.Join(specDir, correction.DirName))
	if err != nil {
		t.Fatal(err)
	}
	comp := &compiler{entities: entities, state: state, vetoes: map[[2]string]bool{},
		variants: map[[2]string]map[string][]string{}, restated: map[string]string{}}

	respelt := confirmedObs("expires", observe.KindNormalisation, "2026-12-31 00:00:00", nil, res.Lock.SHA256)
	out, err := comp.compile(respelt)
	if err != nil {
		t.Fatal(err)
	}
	formatPtr := "/components/schemas/Tag/properties/expires/format"
	if len(out.operations) != 2 || out.operations[0].Op != "test" || out.operations[0].Path != formatPtr ||
		out.operations[1].Op != "remove" || out.operations[1].Path != formatPtr {
		t.Fatalf("a non-RFC 3339 spelling compiled to %+v, want the format tested and removed", out)
	}
	if !strings.Contains(out.justification, `"2026-12-31 00:00:00"`) || !strings.Contains(out.justification, "date-time format is withdrawn") {
		t.Errorf("justification = %q", out.justification)
	}

	rfc := confirmedObs("expires", observe.KindNormalisation, "2026-12-31T00:00:00.000Z", nil, res.Lock.SHA256)
	out, err = comp.compile(rfc)
	if err != nil {
		t.Fatal(err)
	}
	if out.category != catAlreadyStated {
		t.Errorf("an RFC 3339 spelling compiled to %+v, want nothing", out)
	}

	plain := confirmedObs("name", observe.KindNormalisation, "lower", nil, res.Lock.SHA256)
	out, err = comp.compile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if out.category != catNoForm {
		t.Errorf("a normalisation on a property with no format compiled to %+v, want no form", out)
	}
}

func TestUnit_Compile_AnAcceptedCreateBodyIsAcceptanceOfItsEnumValues(t *testing.T) {
	t.Parallel()
	_, specDir, lock := pinnedTree(t)
	state, entities, err := revisedState(specDir, filepath.Join(specDir, correction.DirName))
	if err != nil {
		t.Fatal(err)
	}
	comp := &compiler{entities: entities, state: state, vetoes: map[[2]string]bool{},
		variants: map[[2]string]map[string][]string{}, restated: map[string]string{}}
	took := confirmedObs("color", observe.KindWritable, true, nil, lock.SHA256)
	took.Excerpts = []observe.Excerpt{
		{Method: "POST", PathTemplate: "/tags", Status: 201, RequestFragment: json.RawMessage(`{"name":"n","color":"blue"}`)},
		{Method: "POST", PathTemplate: "/tags", Status: 400, RequestFragment: json.RawMessage(`{"name":"n","color":"red"}`)},
	}
	comp.enumAccepted = comp.acceptedValueSites([]observe.Observation{took}, nil)
	site := "/components/schemas/Tag/properties/color"
	if !comp.enumAccepted[site]["blue"] || comp.enumAccepted[site]["red"] {
		t.Fatalf("enumAccepted = %v, want blue from the 2xx body alone", comp.enumAccepted)
	}
	res, err := comp.compile(confirmedObs("color", observe.KindValues, observe.Values{Rejected: []string{"blue"}}, nil, lock.SHA256))
	if err != nil {
		t.Fatal(err)
	}
	if res.category != catAlreadyStated {
		t.Errorf("a rejection of a value a create carried compiled to %+v, want nothing", res)
	}
}
