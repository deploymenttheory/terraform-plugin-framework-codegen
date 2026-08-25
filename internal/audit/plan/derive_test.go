package plan

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// fixtureSpec is the main derivation fixture: a parent resource, a child
// resource under it, a lookup-by-key datasource, and an action that must
// not be audited. The project schema exercises the synthesis paths and
// both conditional hints.
const fixtureSpec = `openapi: 3.0.3
info:
  title: Audit fixture
  version: "1.0"
paths:
  /projects:
    get:
      operationId: listProjects
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Project'
    post:
      operationId: createProject
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ProjectCreate'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Project'
  /projects/{projectId}:
    parameters:
      - name: projectId
        in: path
        schema:
          type: string
    get:
      operationId: getProject
      x-tfpfgen-eventual-consistency: 5s
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Project'
    put:
      operationId: updateProject
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ProjectCreate'
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Project'
    delete:
      operationId: deleteProject
      responses:
        "204":
          description: gone
  /projects/{projectId}/tags:
    parameters:
      - name: projectId
        in: path
        schema:
          type: string
    post:
      operationId: createTag
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/TagCreate'
      responses:
        "201":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Tag'
  /projects/{projectId}/tags/{tagId}:
    parameters:
      - name: projectId
        in: path
        schema:
          type: string
      - name: tagId
        in: path
        schema:
          type: string
    get:
      operationId: getTag
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Tag'
    patch:
      operationId: updateTag
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/TagCreate'
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Tag'
    delete:
      operationId: deleteTag
      responses:
        "204":
          description: gone
  /widgets/{widgetName}:
    get:
      operationId: getWidget
      parameters:
        - name: widgetName
          in: path
          schema:
            type: string
      responses:
        "200":
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Widget'
  /events:
    post:
      operationId: fireEvent
      requestBody:
        content:
          application/json:
            schema:
              type: object
      responses:
        "202":
          description: accepted
components:
  schemas:
    ProjectCreate:
      type: object
      required: [name, mode]
      properties:
        name:
          type: string
        mode:
          type: string
          enum: [basic, advanced]
        query:
          type: string
          x-tfpfgen-required-when:
            property: mode
            equals: advanced
        retention:
          type: integer
          default: 30
        owner_email:
          type: string
          format: email
        homepage:
          type: string
          format: uri
        enabled:
          type: boolean
        motto:
          type: string
          example: carpe diem
        created_at:
          type: string
          readOnly: true
    Project:
      type: object
      properties:
        id:
          type: string
          readOnly: true
        name:
          type: string
    TagCreate:
      type: object
      required: [name]
      properties:
        name:
          type: string
        color:
          type: string
    Tag:
      type: object
      properties:
        id:
          type: string
          readOnly: true
        name:
          type: string
    Widget:
      type: object
      properties:
        name:
          type: string
`

func loadDoc(t *testing.T, spec string) *specmodel.Document {
	t.Helper()
	doc, err := specmodel.Load([]byte(spec))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return doc
}

func testConfig() *config.Config {
	return &config.Config{
		Audit: config.Audit{NamePrefix: "tfpfgen", MaxObjects: 25, RateLimitRPS: 2},
	}
}

// widgetInputs supplies the lookup key the widget datasource needs.
func widgetInputs(t *testing.T) *Inputs {
	t.Helper()
	in, err := ParseInputs([]byte(`{"widget": {"parentRefs": {"widgetName": "${WIDGET_NAME}"}}}`))
	if err != nil {
		t.Fatalf("ParseInputs: %v", err)
	}
	return in
}

func mustDerive(t *testing.T, doc *specmodel.Document, cfg *config.Config, in *Inputs) *Plan {
	t.Helper()
	p, err := Derive(doc, cfg, in)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return p
}

func entityByKey(t *testing.T, p *Plan, key string) EntityPlan {
	t.Helper()
	for _, ep := range p.Entities {
		if ep.Entity == key {
			return ep
		}
	}
	t.Fatalf("no entity %q in the plan; have %v", key, entityKeys(p))
	return EntityPlan{}
}

func entityKeys(p *Plan) []string {
	var out []string
	for _, ep := range p.Entities {
		out = append(out, ep.Entity)
	}
	return out
}

func kinds(steps []Step) []StepKind {
	out := make([]StepKind, len(steps))
	for i, s := range steps {
		out[i] = s.Kind
	}
	return out
}

func TestUnit_Plan_DeriveIsDeterministic(t *testing.T) {
	cfg := testConfig()
	p1 := mustDerive(t, loadDoc(t, fixtureSpec), cfg, widgetInputs(t))
	p2 := mustDerive(t, loadDoc(t, fixtureSpec), cfg, widgetInputs(t))

	if !reflect.DeepEqual(p1, p2) {
		t.Fatal("two derivations of the same inputs are not DeepEqual")
	}
	j1, err := p1.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	j2, _ := p2.JSON()
	if !bytes.Equal(j1, j2) {
		t.Fatal("two derivations encode differently")
	}
	if !bytes.HasSuffix(j1, []byte("\n")) || !bytes.Contains(j1, []byte(`"entities"`)) {
		t.Errorf("the JSON dump is not printable output: %.80s", j1)
	}
}

func TestUnit_Plan_EntityOrderAndRoles(t *testing.T) {
	p := mustDerive(t, loadDoc(t, fixtureSpec), testConfig(), widgetInputs(t))

	got := entityKeys(p)
	want := []string{"project", "projects_tag", "widget"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entity order = %v, want %v", got, want)
	}
	if p.Entities[0].Role != "resource" || p.Entities[1].Role != "resource" || p.Entities[2].Role != "lookup" {
		t.Errorf("roles = %s %s %s", p.Entities[0].Role, p.Entities[1].Role, p.Entities[2].Role)
	}
	// The action-only entity is not audited and not skipped: it is simply
	// not a lifecycle.
	for _, s := range p.Skipped {
		if s.Entity == "event" {
			t.Errorf("the action entity was skipped rather than ignored: %+v", s)
		}
	}
}

func TestUnit_Plan_ResourceStepOrder(t *testing.T) {
	p := mustDerive(t, loadDoc(t, fixtureSpec), testConfig(), widgetInputs(t))
	project := entityByKey(t, p, "project")

	want := []StepKind{
		StepCreateMinimal, StepReadWithRetry, StepReadConsecutive,
		StepUpdateField, StepUpdateField, StepUpdateField, StepUpdateField,
		StepUpdateField, StepUpdateField, StepUpdateField, StepUpdateField,
		StepDeleteWithConfirmation, StepCreateMaximal,
		StepOmitRequired, StepOmitRequired,
		StepUndocumentedEnumValue, StepUndeclaredSpecField,
		StepCreatePerEnumValue, StepCreatePerEnumValue, // required-when: with, without
		StepCreatePerEnumValue, StepCreatePerEnumValue, // enum values: basic, advanced
		StepCleanupDelete,
	}
	if got := kinds(project.Steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("project step kinds = %v, want %v", got, want)
	}
	if project.Budget.Requests != entityRequestBudget {
		t.Errorf("project budget = %d, want %d", project.Budget.Requests, entityRequestBudget)
	}
}

func TestUnit_Plan_StepDetails(t *testing.T) {
	p := mustDerive(t, loadDoc(t, fixtureSpec), testConfig(), widgetInputs(t))
	project := entityByKey(t, p, "project")
	steps := project.Steps

	create := steps[0]
	if create.Method != "POST" || create.Path != "/projects" {
		t.Errorf("createMinimal targets %s %s", create.Method, create.Path)
	}
	wantMinimal := map[string]any{
		"name": "tfpfgen-" + RunIDToken + "-project-name",
		"mode": "basic",
	}
	if !reflect.DeepEqual(create.Body, wantMinimal) {
		t.Errorf("minimal body = %v, want %v", create.Body, wantMinimal)
	}

	readWithRetry := steps[1]
	if readWithRetry.Path != "/projects/{projectId}" ||
		readWithRetry.PathValues["projectId"] != CreatedRef("project") {
		t.Errorf("readWithRetry = %+v", readWithRetry)
	}
	if readWithRetry.Poll == nil || readWithRetry.Poll.Timeout != "5s" || readWithRetry.Poll.Interval != "2s" {
		t.Errorf("readWithRetry poll = %+v, want the 5s eventual-consistency hint", readWithRetry.Poll)
	}

	// The update of mode moves it to the other documented value.
	var modeUpdate *Step
	for i := range steps {
		if steps[i].Kind == StepUpdateField && steps[i].Attribute == "mode" {
			modeUpdate = &steps[i]
		}
	}
	if modeUpdate == nil || modeUpdate.Body["mode"] != "advanced" || modeUpdate.Method != "PUT" {
		t.Fatalf("mode update = %+v", modeUpdate)
	}

	// createMaximal populates every writable field and reserves bisection
	// attempts sized from the six optional ones.
	maximal := steps[12]
	if len(maximal.Body) != 8 {
		t.Errorf("maximal body has %d fields, want 8: %v", len(maximal.Body), maximal.Body)
	}
	if maximal.Body["motto"] != "carpe diem" || maximal.Body["retention"] != 30 {
		t.Errorf("maximal body ignored example/default: %v", maximal.Body)
	}
	if maximal.Body["owner_email"] != "tfpfgen-"+RunIDToken+"@example.invalid" {
		t.Errorf("email synthesis = %v", maximal.Body["owner_email"])
	}
	if maximal.BisectionAllowance != 4 {
		t.Errorf("bisection allowance = %d, want 4 for 6 optionals", maximal.BisectionAllowance)
	}

	// omitRequired drops exactly the attribute under test.
	omit := steps[13]
	if omit.Attribute != "name" {
		t.Fatalf("first omitRequired is about %q", omit.Attribute)
	}
	if _, present := omit.Body["name"]; present || omit.Body["mode"] != "basic" {
		t.Errorf("omitRequired body = %v", omit.Body)
	}

	undoc := steps[15]
	if undoc.Attribute != "mode" || undoc.Body["mode"] != undocumentedEnumValue {
		t.Errorf("undocumentedEnumValue = %+v", undoc)
	}
	unknown := steps[16]
	if unknown.Body[undeclaredSpecFieldName] != true {
		t.Errorf("undeclaredSpecField body = %v", unknown.Body)
	}

	// The required-when pair: gate pinned, property present then omitted.
	with, without := steps[17], steps[18]
	for _, s := range []Step{with, without} {
		if s.Attribute != "query" || s.Condition == nil ||
			s.Condition.Attribute != "mode" || s.Condition.Equals != "advanced" ||
			s.Body["mode"] != "advanced" {
			t.Errorf("required-when conditional = %+v", s)
		}
	}
	if _, ok := with.Body["query"]; !ok {
		t.Error("the with-variant does not carry the property")
	}
	if _, ok := without.Body["query"]; ok {
		t.Error("the without-variant carries the property")
	}

	// The enum-sibling conditionals pin each documented value.
	if steps[19].Condition.Equals != "basic" || steps[20].Condition.Equals != "advanced" {
		t.Errorf("enum conditionals = %+v, %+v", steps[19].Condition, steps[20].Condition)
	}

	last := steps[len(steps)-1]
	if last.Kind != StepCleanupDelete || last.Method != "DELETE" || last.PathValues["projectId"] != CreatedRef("project") {
		t.Errorf("cleanupDelete = %+v", last)
	}
}

func TestUnit_Plan_ParentOrderingAndTokens(t *testing.T) {
	p := mustDerive(t, loadDoc(t, fixtureSpec), testConfig(), widgetInputs(t))
	tag := entityByKey(t, p, "projects_tag")

	if !reflect.DeepEqual(tag.Parents, []string{"project"}) {
		t.Errorf("tag parents = %v", tag.Parents)
	}
	create := tag.Steps[0]
	if create.PathValues["projectId"] != CreatedRef("project") {
		t.Errorf("tag create path values = %v", create.PathValues)
	}
	read := tag.Steps[1]
	if read.PathValues["projectId"] != CreatedRef("project") ||
		read.PathValues["tagId"] != CreatedRef("projects_tag") {
		t.Errorf("tag read path values = %v", read.PathValues)
	}
	if read.Poll.Timeout != "30s" {
		t.Errorf("tag readWithRetry timeout = %s, want the 30s default", read.Poll.Timeout)
	}

	// An operator-supplied parent id outranks creating one: with
	// parentRefs, the tag plan uses the literal and depends on nothing.
	in, err := ParseInputs([]byte(`{
		"projects_tag": {"parentRefs": {"projectId": "existing-123"}},
		"widget": {"parentRefs": {"widgetName": "${WIDGET_NAME}"}}
	}`))
	if err != nil {
		t.Fatalf("ParseInputs: %v", err)
	}
	p = mustDerive(t, loadDoc(t, fixtureSpec), testConfig(), in)
	tag = entityByKey(t, p, "projects_tag")
	if len(tag.Parents) != 0 || tag.Steps[0].PathValues["projectId"] != "existing-123" {
		t.Errorf("parentRefs did not outrank creation: parents=%v values=%v", tag.Parents, tag.Steps[0].PathValues)
	}
}

func TestUnit_Plan_LookupChecks(t *testing.T) {
	// Without the key, the lookup degrades to a named skip.
	p := mustDerive(t, loadDoc(t, fixtureSpec), testConfig(), nil)
	var skip *Skipped
	for i := range p.Skipped {
		if p.Skipped[i].Entity == "widget" {
			skip = &p.Skipped[i]
		}
	}
	if skip == nil || !strings.Contains(skip.Reason, `"widgetName"`) || !strings.Contains(skip.Reason, InputsPath) {
		t.Fatalf("widget skip = %+v", skip)
	}

	// With it, the lookup gets its read checks.
	p = mustDerive(t, loadDoc(t, fixtureSpec), testConfig(), widgetInputs(t))
	widget := entityByKey(t, p, "widget")
	if got := kinds(widget.Steps); !reflect.DeepEqual(got, []StepKind{StepRead, StepReadConsecutive}) {
		t.Fatalf("widget steps = %v", got)
	}
	if widget.Steps[0].PathValues["widgetName"] != "${WIDGET_NAME}" {
		t.Errorf("widget path values = %v", widget.Steps[0].PathValues)
	}
}

func TestUnit_Plan_SkipHandling(t *testing.T) {
	// inputs.skip lists the entity as skipped and starves its children.
	in, err := ParseInputs([]byte(`{"project": {"skip": true}}`))
	if err != nil {
		t.Fatal(err)
	}
	p := mustDerive(t, loadDoc(t, fixtureSpec), testConfig(), in)
	if len(p.Entities) != 0 {
		t.Fatalf("entities = %v, want none", entityKeys(p))
	}
	reasons := map[string]string{}
	for _, s := range p.Skipped {
		reasons[s.Entity] = s.Reason
	}
	if reasons["project"] != "skipped by "+InputsPath {
		t.Errorf("project reason = %q", reasons["project"])
	}
	if !strings.Contains(reasons["projects_tag"], `parent "project"`) {
		t.Errorf("tag reason = %q", reasons["projects_tag"])
	}

	// services.exclude does the same from config.
	cfg := testConfig()
	cfg.Services.Exclude = []string{"project"}
	p = mustDerive(t, loadDoc(t, fixtureSpec), cfg, nil)
	found := false
	for _, s := range p.Skipped {
		found = found || (s.Entity == "project" && strings.Contains(s.Reason, "services.exclude"))
	}
	if !found {
		t.Errorf("no services.exclude skip: %+v", p.Skipped)
	}
}

func TestUnit_Plan_InputsEntityStrictness(t *testing.T) {
	in, err := ParseInputs([]byte(`{"projct": {"skip": true}}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Derive(loadDoc(t, fixtureSpec), testConfig(), in)
	if err == nil || !strings.Contains(err.Error(), `unknown entity "projct"`) ||
		!strings.Contains(err.Error(), `did you mean "project"?`) {
		t.Fatalf("Derive = %v, want an unknown-entity error with a suggestion", err)
	}

	// Any classified entity is a valid inputs key, audited or not.
	in, err = ParseInputs([]byte(`{"event": {"skip": true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Derive(loadDoc(t, fixtureSpec), testConfig(), in); err != nil {
		t.Fatalf("a classified non-audited entity was refused: %v", err)
	}
}

func TestUnit_Plan_RunBudget(t *testing.T) {
	p := mustDerive(t, loadDoc(t, fixtureSpec), testConfig(), widgetInputs(t))
	want := RunBudget{Requests: 3 * entityRequestBudget, Objects: 25, Duration: "3m0s"}
	if p.Budget != want {
		t.Errorf("budget = %+v, want %+v", p.Budget, want)
	}

	// Unset audit knobs clamp to their documented defaults rather than
	// dividing by zero.
	got := runBudget(&config.Config{}, 1)
	if got.Objects != 25 || got.Requests != entityRequestBudget || got.Duration != "1m0s" {
		t.Errorf("clamped budget = %+v", got)
	}
	if got = runBudget(&config.Config{}, 0); got.Duration != "1m0s" {
		t.Errorf("empty-plan duration = %q, want the floor", got.Duration)
	}
}

// TestUnit_Plan_ASingletonIsRefusedNotPanicked pins the shape that crashed a
// live run: a resource whose create slot is empty because it is written
// through its update call. One entity is refused; the rest of the run stands.
func TestUnit_Plan_ASingletonIsRefusedNotPanicked(t *testing.T) {
	doc := loadDoc(t, `
openapi: 3.0.1
info: {title: t, version: "1"}
paths:
  /settings:
    get:
      responses:
        "200":
          content:
            application/json:
              schema: {type: object, properties: {name: {type: string}}}
    put:
      requestBody:
        content:
          application/json:
            schema: {type: object, properties: {name: {type: string}}}
      responses:
        "200":
          content:
            application/json:
              schema: {type: object, properties: {name: {type: string}}}
`)
	p, err := Derive(doc, testConfig(), &Inputs{})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	for _, s := range p.Skipped {
		if strings.Contains(s.Reason, "no create operation") {
			return
		}
	}
	t.Fatalf("the singleton was not refused with a reason; skipped: %+v", p.Skipped)
}
