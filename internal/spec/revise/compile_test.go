package revise

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/store"
)

// tagSpec is the fixture document every propose test compiles against: one
// full-lifecycle entity, "tag", shaped the way the quirkserver exhibits its
// misbehaviours — an enum to disagree with, an optional field the server
// defaults, a field create accepts and update refuses.
const tagSpec = `openapi: 3.0.3
info:
  title: quirk sample
  version: 1.0.0
paths:
  /tags:
    post:
      operationId: createTag
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Tag'
      responses:
        "201":
          description: created
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Tag'
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
                  $ref: '#/components/schemas/Tag'
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
                $ref: '#/components/schemas/Tag'
    put:
      operationId: updateTag
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Tag'
      responses:
        "200":
          description: updated
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Tag'
    delete:
      operationId: deleteTag
      responses:
        "204":
          description: deleted
components:
  schemas:
    Tag:
      type: object
      properties:
        name:
          type: string
        color:
          type: string
          enum:
            - red
            - blue
        size:
          type: string
        mode:
          type: string
        port:
          type: integer
        protocol:
          type: string
`

// pinnedTree imports the fixture into <root>/spec and returns both paths
// plus the pin, ready for observations to land beside it.
func pinnedTree(t *testing.T) (root, specDir string, lock store.Lock) {
	t.Helper()
	root = t.TempDir()
	specDir = filepath.Join(root, "spec")
	res, err := store.Import(specDir, []byte(tagSpec), "published.yaml")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return root, specDir, res.Lock
}

// confirmedObs builds one confirmed observation against the given pin.
func confirmedObs(attr string, kind observe.Kind, value any, cond *observe.Condition, specHash string) observe.Observation {
	return observe.Observation{
		Entity:    "tag",
		Attribute: attr,
		Kind:      kind,
		Value:     value,
		Condition: cond,
		Outcome:   observe.OutcomeConfirmed,
		RunID:     "run1",
		SpecHash:  specHash,
	}
}

// commitObs writes observations into the conventional audit/observations
// directory beside the spec directory.
func commitObs(t *testing.T, root string, obs ...observe.Observation) {
	t.Helper()
	if err := observe.Write(filepath.Join(root, "audit", "observations"), obs); err != nil {
		t.Fatalf("observe.Write: %v", err)
	}
}

// readProposed returns the one proposed file's bytes, failing unless exactly
// one proposal exists.
func readProposed(t *testing.T, p Proposals) string {
	t.Helper()
	if len(p.Proposed) != 1 {
		t.Fatalf("Proposed = %+v, want exactly one", p.Proposed)
	}
	raw, err := os.ReadFile(p.Proposed[0].Path)
	if err != nil {
		t.Fatalf("reading the proposal: %v", err)
	}
	return string(raw)
}

func TestUnit_Propose_CompilesEachKindIntoItsExactCorrection(t *testing.T) {
	t.Parallel()

	// Each case is one observation in, one exact proposed correction file
	// out. %s in want is the observation's computed ID.
	cases := []struct {
		name  string
		attr  string
		kind  observe.Kind
		value any
		cond  *observe.Condition
		want  string
	}{
		{
			name: "requiredByAPI adds to required",
			attr: "name", kind: observe.KindRequiredByAPI, value: true,
			want: `{
  "justification": "the audit confirmed a requiredByAPI observation on tag.name: a create omitting the property is rejected whatever the document declares",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/required",
      "value": [
        "name"
      ]
    }
  ]
}
`,
		},
		{
			name: "writable false becomes readOnly",
			attr: "size", kind: observe.KindWritable, value: false,
			want: `{
  "justification": "the audit confirmed a writable observation on tag.size: the live API accepts the value and never stores it, so the property is readOnly",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/size/readOnly",
      "value": true
    }
  ]
}
`,
		},
		{
			name: "immutable becomes create-only",
			attr: "name", kind: observe.KindImmutable, value: true,
			want: `{
  "justification": "the audit confirmed an immutable observation on tag.name: the live API refuses to change the value after create (x-tfpfgen-create-only)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/name/x-tfpfgen-create-only",
      "value": true
    }
  ]
}
`,
		},
		{
			name: "requiredWhen becomes the conditional extension",
			attr: "port", kind: observe.KindRequiredWhen, value: true,
			cond: &observe.Condition{Attribute: "protocol", Equals: "tcp"},
			want: `{
  "justification": "the audit confirmed a requiredWhen observation on tag.port: the property is required when protocol equals \"tcp\" (x-tfpfgen-required-when)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/port/x-tfpfgen-required-when",
      "value": {
        "equals": "tcp",
        "property": "protocol"
      }
    }
  ]
}
`,
		},
		{
			name: "serverDefault records the filled value on the property",
			attr: "mode", kind: observe.KindServerDefault, value: "auto",
			want: `{
  "justification": "the audit confirmed a serverDefault observation on tag.mode: omitting the property stores auto, so the generated attribute is Optional and Computed (x-tfpfgen-server-default)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/mode/x-tfpfgen-server-default",
      "value": "auto"
    }
  ]
}
`,
		},
		{
			name: "values corrects the enum and opens the set",
			attr: "color", kind: observe.KindValues,
			value: observe.Values{Accepted: []string{"red", "green"}, Rejected: []string{"blue"}, Closed: boolPtr(false)},
			want: `{
  "justification": "the audit confirmed a values observation on tag.color: the live API rejects the documented value(s) blue; accepts the undocumented value(s) green; accepts values beyond the documented set (x-tfpfgen-values-open)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "test",
      "path": "/components/schemas/Tag/properties/color/enum/1",
      "value": "blue"
    },
    {
      "op": "remove",
      "path": "/components/schemas/Tag/properties/color/enum/1"
    },
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/color/enum/-",
      "value": "green"
    },
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/color/x-tfpfgen-values-open",
      "value": true
    }
  ]
}
`,
		},
		{
			name: "updateStyle annotates the update operation",
			kind: observe.KindUpdateStyle, value: "put-full",
			want: `{
  "justification": "the audit confirmed an updateStyle observation on tag: the update operation treats omitted fields as put-full (x-tfpfgen-update-style)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/paths/~1tags~1{tagId}/put/x-tfpfgen-update-style",
      "value": "put-full"
    }
  ]
}
`,
		},
		{
			name: "deleteNotFoundOK annotates the delete operation",
			kind: observe.KindDeleteNotFoundOK, value: true,
			want: `{
  "justification": "the audit confirmed a deleteNotFoundOK observation on tag: deleting an already-deleted object answers 404, which delete logic should treat as success (x-tfpfgen-delete-not-found-ok)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/paths/~1tags~1{tagId}/delete/x-tfpfgen-delete-not-found-ok",
      "value": true
    }
  ]
}
`,
		},
		{
			name: "readAfterWrite annotates the read operation",
			kind: observe.KindReadAfterWrite, value: "2s",
			want: `{
  "justification": "the audit confirmed a readAfterWrite observation on tag: a read may lag a write by up to 2s (x-tfpfgen-eventual-consistency)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/paths/~1tags~1{tagId}/get/x-tfpfgen-eventual-consistency",
      "value": "2s"
    }
  ]
}
`,
		},
		{
			name: "volatile becomes its extension",
			attr: "mode", kind: observe.KindVolatile, value: true,
			want: `{
  "justification": "the audit confirmed a volatile observation on tag.mode: the value differs between two identical reads (x-tfpfgen-volatile)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/mode/x-tfpfgen-volatile",
      "value": true
    }
  ]
}
`,
		},
		{
			name: "serverForced becomes its extension",
			attr: "size", kind: observe.KindServerForced, value: true,
			want: `{
  "justification": "the audit confirmed a serverForced observation on tag.size: the server substitutes its own value regardless of what was sent (x-tfpfgen-server-forced)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/size/x-tfpfgen-server-forced",
      "value": true
    }
  ]
}
`,
		},
		{
			name: "undocumentedFieldInSpec adds the property with its observed type",
			attr: "serial", kind: observe.KindUndocumentedFieldInSpec, value: "string",
			want: `{
  "justification": "the audit confirmed an undocumentedFieldInSpec observation on tag.serial: read responses carry the field with the stable JSON type string, and no schema declares it",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/serial",
      "value": {
        "type": "string"
      }
    }
  ]
}
`,
		},
		{
			name: "undocumentedFieldInSpec array carries an items stub",
			attr: "labels", kind: observe.KindUndocumentedFieldInSpec, value: "array",
			want: `{
  "justification": "the audit confirmed an undocumentedFieldInSpec observation on tag.labels: read responses carry the field with the stable JSON type array, and no schema declares it",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/labels",
      "value": {
        "items": {},
        "type": "array"
      }
    }
  ]
}
`,
		},
		{
			name: "validWhen becomes the conditional-validity extension",
			attr: "port", kind: observe.KindValidWhen, value: true,
			cond: &observe.Condition{Attribute: "protocol", Equals: "tcp"},
			want: `{
  "justification": "the audit confirmed a validWhen observation on tag.port: the property is valid only when protocol equals \"tcp\" (x-tfpfgen-valid-when)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/port/x-tfpfgen-valid-when",
      "value": {
        "equals": "tcp",
        "property": "protocol"
      }
    }
  ]
}
`,
		},
		{
			name: "dependsOn becomes its extension on a 3.0 document",
			attr: "port", kind: observe.KindDependsOn, value: "protocol",
			want: `{
  "justification": "the audit confirmed a dependsOn observation on tag.port: the property is settable only when protocol is also present (x-tfpfgen-depends-on)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/port/x-tfpfgen-depends-on",
      "value": {
        "requires": "protocol"
      }
    }
  ]
}
`,
		},
		{
			name: "mutuallyExclusive annotates the entity schema",
			kind: observe.KindMutuallyExclusive, value: []string{"size", "mode"},
			want: `{
  "justification": "the audit confirmed a mutuallyExclusive observation on tag: at most one of mode, size may be set (x-tfpfgen-mutually-exclusive)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/x-tfpfgen-mutually-exclusive",
      "value": [
        "mode",
        "size"
      ]
    }
  ]
}
`,
		},
		{
			name: "validConfiguration annotates the discriminator schema",
			attr: "mode", kind: observe.KindValidConfiguration, value: []string{"manual", "auto"},
			want: `{
  "justification": "the audit confirmed a validConfiguration observation on tag.mode: the property gates the valid field set, one variant per value (x-tfpfgen-valid-configuration)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/x-tfpfgen-valid-configuration",
      "value": {
        "discriminator": "mode",
        "variants": {
          "auto": [],
          "manual": []
        }
      }
    }
  ]
}
`,
		},
		{
			name: "listResponseShape annotates the list operation with the observed envelope",
			kind: observe.KindListResponseShape,
			value: observe.ListResponseShape{
				Envelope: "wrapped", Key: "tags", Pagination: "cursor",
			},
			want: `{
  "justification": "the audit confirmed a listResponseShape observation on tag: the live list response wraps its item array under \"tags\", pagination cursor (x-tfpfgen-list-response-shape)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/paths/~1tags/get/x-tfpfgen-list-response-shape",
      "value": {
        "envelope": "wrapped",
        "key": "tags",
        "pagination": "cursor"
      }
    }
  ]
}
`,
		},
		{
			name: "a bare list response is marked bare, not left implicit",
			kind: observe.KindListResponseShape,
			value: observe.ListResponseShape{
				Envelope: "bare", Pagination: "none",
			},
			want: `{
  "justification": "the audit confirmed a listResponseShape observation on tag: the live list response is a bare item array, pagination none (x-tfpfgen-list-response-shape)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/paths/~1tags/get/x-tfpfgen-list-response-shape",
      "value": {
        "envelope": "bare",
        "pagination": "none"
      }
    }
  ]
}
`,
		},
		{
			name: "ignoredOnUpdate becomes its extension",
			attr: "mode", kind: observe.KindIgnoredOnUpdate, value: true,
			want: `{
  "justification": "the audit confirmed an ignoredOnUpdate observation on tag.mode: updates accept a new value with a success status and do not apply it (x-tfpfgen-silently-ignored-on-update)",
  "evidence": "audit/observations/tag.observations.json#%s",
  "operations": [
    {
      "op": "add",
      "path": "/components/schemas/Tag/properties/mode/x-tfpfgen-silently-ignored-on-update",
      "value": true
    }
  ]
}
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, specDir, lock := pinnedTree(t)
			commitObs(t, root, confirmedObs(tc.attr, tc.kind, tc.value, tc.cond, lock.SHA256))

			p, err := Propose(specDir)
			if err != nil {
				t.Fatalf("Propose: %v", err)
			}
			id := observe.ComputeID("tag", tc.attr, tc.kind, tc.cond)
			if got, want := readProposed(t, p), fmt.Sprintf(tc.want, id); got != want {
				t.Errorf("proposed correction:\n got: %s\nwant: %s", got, want)
			}
			wantName := "001-tag.correction.json"
			if filepath.Base(p.Proposed[0].Path) != wantName {
				t.Errorf("file name = %s, want %s", filepath.Base(p.Proposed[0].Path), wantName)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

// TestUnit_Propose_UndocumentedFieldAlreadyDeclaredProposesNothing: the
// convergence half — once the property exists, a re-observed
// undocumentedFieldInSpec states an existing fact.
func TestUnit_Propose_UndocumentedFieldAlreadyDeclaredProposesNothing(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, confirmedObs("name", observe.KindUndocumentedFieldInSpec, "string", nil, lock.SHA256))

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 0 {
		t.Errorf("Proposed = %+v; the declared property must compile to nothing", p.Proposed)
	}
	if len(p.AlreadyStated) != 1 {
		t.Fatalf("AlreadyStated = %+v, want the declared-property note", p.AlreadyStated)
	}
}

func TestUnit_Propose_ReportsKindsWithoutACorrectionForm(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		confirmedObs("name", observe.KindNormalisation, "trimmed", nil, lock.SHA256),
		confirmedObs("mode", observe.KindDerivedDefault, true, nil, lock.SHA256),
	)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed)+len(p.AutoAccepted) != 0 {
		t.Errorf("wrote files for kinds with no correction form: %+v %+v", p.Proposed, p.AutoAccepted)
	}
	if len(p.NoForm) != 2 {
		t.Fatalf("NoForm = %+v, want both observations", p.NoForm)
	}
	for _, n := range p.NoForm {
		if n.Reason != "no correction form exists yet; adding an x-tfpfgen-* key is an owner decision" {
			t.Errorf("NoForm reason = %q", n.Reason)
		}
	}
}

func TestUnit_Propose_DerivedDefaultVetoesAServerDefault(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		confirmedObs("mode", observe.KindServerDefault, "auto", nil, lock.SHA256),
		confirmedObs("mode", observe.KindDerivedDefault, true, nil, lock.SHA256),
	)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 0 {
		t.Errorf("Proposed = %+v; the derived default must veto the static one", p.Proposed)
	}
	if len(p.Vetoed) != 1 || p.Vetoed[0].Kind != observe.KindServerDefault {
		t.Fatalf("Vetoed = %+v, want the serverDefault observation", p.Vetoed)
	}
}

func TestUnit_Propose_ComposesTwoRequirementsOnOneSchema(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root,
		confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256),
		confirmedObs("mode", observe.KindRequiredByAPI, true, nil, lock.SHA256),
	)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 2 {
		t.Fatalf("Proposed = %+v, want two", p.Proposed)
	}
	// Sorted by attribute: mode compiles first and creates the required
	// array; name, compiled against the evolving state, appends to it.
	first, err := os.ReadFile(p.Proposed[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(p.Proposed[1].Path)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"path": "/components/schemas/Tag/required",`; !contains(string(first), want) {
		t.Errorf("first proposal does not create the required list:\n%s", first)
	}
	if want := `"path": "/components/schemas/Tag/required/-",`; !contains(string(second), want) {
		t.Errorf("second proposal does not append to the required list:\n%s", second)
	}
}

func TestUnit_Propose_ReportsAnUnplaceableObservation(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	ghost := confirmedObs("name", observe.KindRequiredByAPI, true, nil, lock.SHA256)
	ghost.Entity = "widget"
	commitObs(t, root, ghost)

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Unplaceable) != 1 || !contains(p.Unplaceable[0].Reason, "widget") {
		t.Fatalf("Unplaceable = %+v, want the ghost entity named", p.Unplaceable)
	}
	missing := confirmedObs("nonesuch", observe.KindVolatile, true, nil, lock.SHA256)
	commitObs(t, root, missing)
	p, err = Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	found := false
	for _, n := range p.Unplaceable {
		if contains(n.Reason, `"nonesuch"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("Unplaceable = %+v, want the missing attribute named", p.Unplaceable)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// TestUnit_Propose_AZeroReadAfterWriteLagProposesNothing is the defect the
// first live run surfaced: five pull requests, each asking a human to approve
// an eventual-consistency annotation of "0s". A zero lag means the read never
// lagged the write, so there is nothing to declare and nothing to decide.
func TestUnit_Propose_AZeroReadAfterWriteLagProposesNothing(t *testing.T) {
	t.Parallel()
	for _, lag := range []string{"0s", "0ms", "0"} {
		root, specDir, lock := pinnedTree(t)
		commitObs(t, root, confirmedObs("", observe.KindReadAfterWrite, lag, nil, lock.SHA256))

		p, err := Propose(specDir)
		if err != nil {
			t.Fatalf("Propose(%s): %v", lag, err)
		}
		if len(p.Proposed) != 0 {
			t.Errorf("lag %s proposed %+v; a zero lag is a no-op correction", lag, p.Proposed)
		}
		if len(p.AlreadyStated) != 1 || !contains(p.AlreadyStated[0].Reason, "never lagged") {
			t.Errorf("lag %s: AlreadyStated = %+v, want it reported as needing no correction", lag, p.AlreadyStated)
		}
	}
}

// A measurable lag still compiles — the suppression is of zero, not of the
// kind.
func TestUnit_Propose_ANonZeroReadAfterWriteLagStillCompiles(t *testing.T) {
	t.Parallel()
	root, specDir, lock := pinnedTree(t)
	commitObs(t, root, confirmedObs("", observe.KindReadAfterWrite, "1ms", nil, lock.SHA256))

	p, err := Propose(specDir)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(p.Proposed) != 1 {
		t.Fatalf("Proposed = %+v, want the measurable lag compiled", p.Proposed)
	}
}
