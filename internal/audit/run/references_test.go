package run

import (
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
		{Key: "unlisted", CollectionPath: "/unlisted"},
	}
	got := referenceCollections(classified)
	want := map[string]string{"agent": "/agents", "alertrule": "/alerts/rules"}
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
