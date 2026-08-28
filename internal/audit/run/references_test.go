package run

import (
	"context"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/plan"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/testapiserver"
	"reflect"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/strategy"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

func TestUnit_References_NounsAgreeAcrossSpellings(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct{ field, path string }{
		{"agentId", "/agents"},
		{"agents", "/agents"},
		{"alertRules", "/alerts/rules"},
		{"alertRuleIds", "/alert-rules"},
		{"permissions", "/permissions"},
		{"dashboardId", "/dashboards"},
	} {
		if fieldNoun(testCase.field) != pathNoun(testCase.path) {
			t.Errorf("%s spells %q, %s spells %q", testCase.field, fieldNoun(testCase.field), testCase.path, pathNoun(testCase.path))
		}
	}
	// A qualified reference names its collection in its last words.
	references := map[string]string{"accountgroup": "/account-groups", "agent": "/agents"}
	if path, ok := referencedCollection("loginAccountGroupId", references); !ok || path != "/account-groups" {
		t.Errorf("loginAccountGroupId references %q, want /account-groups", path)
	}
	if path, ok := referencedCollection("targetAgentId", references); !ok || path != "/agents" {
		t.Errorf("targetAgentId references %q, want /agents", path)
	}
	if _, ok := referencedCollection("layoutId", references); ok {
		t.Error("a name spelling no collection matched one")
	}
	if pathNoun("/dashboards/{dashboardId}/widgets") != "dashboardwidget" {
		t.Errorf("parameters were not left out: %q", pathNoun("/dashboards/{dashboardId}/widgets"))
	}
}

func TestUnit_References_OnlyIdNamedFieldsAndCollectionListsAreReferences(t *testing.T) {
	t.Parallel()
	if !referenceField("agentId", false) || !referenceField("testIds", false) {
		t.Error("an id-suffixed name is a reference")
	}
	if referenceField("id", false) {
		t.Error("the object's own id is not a reference to another object")
	}
	if referenceField("server", false) || referenceField("agents", false) {
		t.Error("a plain scalar name is not a reference")
	}
	if !referenceField("agents", true) || referenceField("server", true) {
		t.Error("a plural list name references a collection; a singular one does not")
	}
}

func TestUnit_References_BindingReplacesSynthesisedIdsAtEveryDepth(t *testing.T) {
	t.Parallel()
	references := map[string]string{"agent": "/agents", "alertrule": "/alerts/rules", "dashboard": "/dashboards"}
	body := map[string]any{
		"server":      "www.example.invalid",
		"dashboardId": "given-by-the-operator",
		"alertRules":  []any{"344753", "212697"},
		"headers":     []any{"a", "b"},
		"agents":      []any{map[string]any{"agentId": "sample-agentId", "sourceIpAddress": "192.0.2.1"}},
		"dscpId":      "0",
		"count":       2,
	}
	got := bindReferences(body, "", references, map[string]bool{"dashboardId": true})
	want := map[string]any{
		"server":      "www.example.invalid",
		"dashboardId": "given-by-the-operator",
		"alertRules":  []any{BorrowToken + "/alerts/rules"},
		"headers":     []any{"a", "b"},
		"agents":      []any{map[string]any{"agentId": BorrowToken + "/agents", "sourceIpAddress": "192.0.2.1"}},
		"dscpId":      "0",
		"count":       2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bound body = %#v\nwant %#v", got, want)
	}
	if got := bindReferences("sample-agentId", "agentId", references, nil); got != BorrowToken+"/agents" {
		t.Errorf("a lone field = %#v", got)
	}
}

func TestUnit_References_CollectionsIndexTheRootPathPerNoun(t *testing.T) {
	t.Parallel()
	list := &specmodel.OperationReference{Method: "GET", Path: "/agents"}
	classified := []specmodel.Classification{
		{Key: "agent", CollectionPath: "/agents", List: list},
		// Under a parent: unreadable without the parent's id, so not offered.
		{Key: "account_groups_agent", CollectionPath: "/account-groups/{id}/agents", List: list},
		{Key: "alerts_rule", CollectionPath: "/alerts/rules", List: list},
		// A leaf noun no other path ends in stands for the path on its own.
		{Key: "dashboards_filter", CollectionPath: "/dashboards/filters", List: list},
		// Two paths ending in one noun make the leaf ambiguous.
		{Key: "endpoint_label", CollectionPath: "/endpoint/labels", List: list},
		{Key: "tests_label", CollectionPath: "/tests/labels", List: list},
		{Key: "unlisted", CollectionPath: "/unlisted"},
	}
	got := referenceCollections(classified)
	want := map[string]string{
		"agent": "/agents", "alertrule": "/alerts/rules", "rule": "/alerts/rules",
		"dashboardfilter": "/dashboards/filters", "filter": "/dashboards/filters",
		"endpointlabel": "/endpoint/labels", "testlabel": "/tests/labels",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collections = %#v, want %#v", got, want)
	}
}

func TestUnit_Strategize_ACompositeOutranksTheScalarSynthesis(t *testing.T) {
	t.Parallel()
	sk := strategy.RequestFields{
		Fields: []string{"agents", "filters", "name"},
		Rules: []strategy.SyntheticValueRules{
			{Field: "agents", Type: "array"},
			{Field: "filters", Type: "array"},
			{Field: "name", Type: "string"},
		},
	}
	synthesis := bodySynthesis{
		entity:     "thing",
		prefix:     "tfpfgen",
		composites: map[string]any{"agents": []any{map[string]any{"agentId": "sample-agentId"}}},
		references: map[string]string{"agent": "/agents"},
	}
	body := synthesis.requestBody(sk, "", "")
	if got, want := body["agents"], []any(nil); !reflect.DeepEqual(got, []any{map[string]any{"agentId": BorrowToken + "/agents"}}) {
		t.Errorf("agents = %#v, want the composite with its reference bound (not %#v)", got, want)
	}
	if got, ok := body["filters"].([]any); !ok || len(got) != 0 {
		t.Errorf("filters = %#v, want the empty collection the scalar synthesis yields", body["filters"])
	}
	// The composite is copied out, so an edit to one body leaves the next
	// one built from it untouched.
	body["agents"].([]any)[0].(map[string]any)["agentId"] = "edited"
	if again := synthesis.requestBody(sk, "", "")["agents"].([]any)[0].(map[string]any)["agentId"]; again != BorrowToken+"/agents" {
		t.Errorf("the composite was shared between bodies: %#v", again)
	}
}

func TestUnit_References_ANestedReferenceNothingServesIsLeftOut(t *testing.T) {
	t.Parallel()
	s := testapiserver.New(t, testapiserver.Quirks{})
	r, err := newRunner(testOptions(t, s, thingPlan(resourceSteps(), 60), testEnv(), nil))
	if err != nil {
		t.Fatal(err)
	}
	entity := &entityState{plan: &plan.EntityPlan{Entity: "assignment", Budget: plan.Budget{Requests: 20}}}
	body := map[string]any{
		"name":     "n",
		"settings": map[string]any{"widgetId": BorrowToken + "/widgets", "mode": "auto"},
		"widgetId": BorrowToken + "/widgets",
		"labels":   []any{BorrowToken + "/widgets"},
	}
	got, err := r.resolveBody(context.Background(), entity, body)
	if err != nil {
		t.Fatalf("resolveBody: %v", err)
	}
	settings, _ := got["settings"].(map[string]any)
	if _, present := settings["widgetId"]; present || settings["mode"] != "auto" {
		t.Errorf("settings = %#v, want the unsatisfiable member left out and the rest kept", settings)
	}
	if _, present := got["widgetId"]; present {
		t.Errorf("an optional top-level reference nothing serves was kept: %#v", got)
	}
	if labels, _ := got["labels"].([]any); len(labels) != 0 {
		t.Errorf("labels = %#v, want the unsatisfiable element left out", labels)
	}
}

func TestUnit_References_AnEntityNeverBorrowsFromItsOwnCollection(t *testing.T) {
	t.Parallel()
	references := map[string]string{"dns server": "/tests/dns-server", "agent": "/agents"}
	got := withoutCollection(references, "/tests/dns-server")
	if _, has := got["dns server"]; has || got["agent"] != "/agents" {
		t.Errorf("withoutCollection = %v, want the entity's own collection left out", got)
	}
	if same := withoutCollection(references, ""); len(same) != 2 {
		t.Errorf("no collection path changed the index: %v", same)
	}
}
