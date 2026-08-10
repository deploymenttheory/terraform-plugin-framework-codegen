package plan

import (
	"reflect"
	"strings"
	"testing"
)

func TestUnit_Plan_ParseInputsReadsTheFile(t *testing.T) {
	in, err := ParseInputs([]byte(`{
		"tag": {
			"values": {"name": "given", "retention": 5},
			"parentRefs": {"projectId": "$env:PROJECT_ID"},
			"skip": false
		},
		"project": {"skip": true}
	}`))
	if err != nil {
		t.Fatalf("ParseInputs: %v", err)
	}

	tag := in.forEntity("tag")
	if tag.Values["name"] != "given" || tag.Values["retention"] != float64(5) {
		t.Errorf("tag values = %#v", tag.Values)
	}
	if tag.ParentRefs["projectId"] != "$env:PROJECT_ID" || tag.Skip {
		t.Errorf("tag = %#v", tag)
	}
	if !in.forEntity("project").Skip {
		t.Error("project.skip did not parse")
	}

	// Absence at every level is the zero value.
	if got := in.forEntity("absent"); !reflect.DeepEqual(got, EntityInputs{}) {
		t.Errorf("absent entity = %#v", got)
	}
	var nilInputs *Inputs
	if got := nilInputs.forEntity("anything"); !reflect.DeepEqual(got, EntityInputs{}) {
		t.Errorf("nil inputs entity = %#v", got)
	}
}

func TestUnit_Plan_ParseInputsAbsenceIsEmpty(t *testing.T) {
	for _, data := range [][]byte{nil, {}} {
		in, err := ParseInputs(data)
		if err != nil || in == nil {
			t.Fatalf("ParseInputs(%v) = %v, %v", data, in, err)
		}
		if len(in.Entities) != 0 {
			t.Errorf("empty file produced entities: %#v", in.Entities)
		}
	}
}

func TestUnit_Plan_ParseInputsStrictness(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"not an object", `["tag"]`, InputsPath},
		{"entity not an object", `{"tag": "skip"}`, "must be an object with values, parentRefs, skip"},
		{"unknown key with suggestion", `{"tag": {"vals": {}}}`, `unknown key "vals" (did you mean "values"?)`},
		{"unknown key parentRef", `{"tag": {"parentRef": {}}}`, `did you mean "parentRefs"?`},
		{"skip mistyped", `{"tag": {"skip": "yes"}}`, `entity "tag"`},
		{"empty parent ref", `{"tag": {"parentRefs": {"projectId": ""}}}`, "parentRefs.projectId"},
		{"bare env prefix", `{"tag": {"parentRefs": {"projectId": "$env:"}}}`, "parentRefs.projectId"},
		{"values not an object", `{"tag": {"values": 3}}`, `entity "tag"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseInputs([]byte(tc.data))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseInputs = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestUnit_Plan_TokensAndSuggestions(t *testing.T) {
	if CreatedRef("project") != "$created:project" {
		t.Errorf("CreatedRef = %q", CreatedRef("project"))
	}
	if !isEnvRef("$env:X") || isEnvRef("literal") {
		t.Error("isEnvRef misclassifies")
	}
	if got := didYouMean("valuess", entityInputKeys); got != ` (did you mean "values"?)` {
		t.Errorf("didYouMean = %q", got)
	}
	if got := didYouMean("zzzzzzzz", entityInputKeys); got != "" {
		t.Errorf("didYouMean far miss = %q", got)
	}
}
