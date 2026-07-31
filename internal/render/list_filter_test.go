package render

import (
	"strings"
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// listFacetResource returns a blueprint and resource carrying a minimal valid list facet.
func listFacetResource() (blueprint.Blueprint, blueprint.Resource) {
	bp := blueprint.Blueprint{
		Provider: blueprint.Provider{
			Name: "te", TypePrefix: "te", GoModule: "example.com/prov",
			SDK: blueprint.SDKModule{
				Dialect: blueprint.DialectRestyService, ModulePath: "example.com/sdk",
				ClientType:   "*te.Client",
				ClientImport: blueprint.Import{Path: "example.com/sdk/te"},
			},
		},
	}

	r := blueprint.Resource{
		Key: "tag", Name: "tag", GoPackage: "tag", GoTypeName: "TagResource",
		ModelTypeName: "TagResourceModel",
		Identity: &blueprint.ResourceIdentity{
			GoTypeName: "TagResourceIdentity",
			Attributes: []blueprint.IdentityAttribute{{
				Name: "id", GoField: "ID", Kind: blueprint.KindString,
				RequiredForImport: true, FromAttribute: "id",
			}},
		},
		Binding: blueprint.ResourceBinding{
			Body: blueprint.BodyModels{ResponseType: "tags.Tag"},
		},
		List: &blueprint.ListFacet{
			GoTypeName: "TagListResource",
			Service: blueprint.ServiceRef{
				ImportPath: "example.com/sdk/tags", TypeName: "Tags",
				Accessor: "l.client.Tags",
			},
			Read: &blueprint.Operation{
				Style: blueprint.CallStyleMethod, Method: "GetTags",
				Return: blueprint.ReturnResultTransportError, ResultType: "tags.ResourceTags",
				Args: []blueprint.Argument{{Kind: blueprint.ArgContext}},
			},
			Response: blueprint.ResponseModel{
				Type: "tags.ResourceTags", AccessStyle: blueprint.AccessStructField,
			},
			CollectionField: "Tags",
			ElementType:     "tags.Tag",
			IdentityFrom: []blueprint.ListIdentityMapping{
				{GoField: "ID", FromSDKField: "ID", IsPointer: true},
			},
		},
	}

	return bp, r
}

// TestUnit_Render_AListFacetRendersItsFilterSchema: the query block's attributes and the
// model List decodes them into come from the facet's own schema, through the same
// attribute machinery every other block kind uses.
func TestUnit_Render_AListFacetRendersItsFilterSchema(t *testing.T) {
	t.Parallel()

	bp, r := listFacetResource()
	r.List.Schema = &blueprint.Schema{Attributes: []blueprint.Attribute{{
		Name: "state", GoField: "State",
		Type:                     blueprint.AttrType{Kind: blueprint.KindString},
		ComputedOptionalRequired: blueprint.Optional,
		Wire:                     blueprint.WireBinding{JSONPath: "state", SkipFlatten: true},
	}}}
	r.List.Read.Args = append(r.List.Read.Args,
		blueprint.Argument{Kind: blueprint.ArgConfigField, Field: "State"})

	v, err := listView(bp, r, *r.List)
	if err != nil {
		t.Fatalf("listView: %v", err)
	}

	if !v.HasFilters {
		t.Fatal("a declared filter schema must render")
	}
	if len(v.ConfigAttributes) != 1 ||
		!strings.Contains(v.ConfigAttributes[0], `"state": schema.StringAttribute`) {
		t.Errorf("config attributes = %v", v.ConfigAttributes)
	}
	if v.FilterModelType != "TagListResourceFilters" {
		t.Errorf("filter model type = %q", v.FilterModelType)
	}
	if len(v.FilterModelFields) != 1 ||
		!strings.Contains(v.FilterModelFields[0], "tfsdk:\"state\"") {
		t.Errorf("filter model fields = %v", v.FilterModelFields)
	}
	// The read call passes the decoded filter, named for what it holds.
	if !strings.Contains(v.Read.Call, "data.State.ValueString()") {
		t.Errorf("the filter should reach the call: %s", v.Read.Call)
	}
}

// TestUnit_Render_AListFacetServesIncludeResourceFromTheFetchedElement.
//
// Only when the collection element is the resource's own read type: then the state
// mapper can serve `terraform query --include-resource` from the element in hand, and a
// request per element -- the template's stated non-goal -- never happens. A mismatched
// element type would not compile against the mapper, so the branch is not generated.
func TestUnit_Render_AListFacetServesIncludeResourceFromTheFetchedElement(t *testing.T) {
	t.Parallel()

	bp, r := listFacetResource()

	v, err := listView(bp, r, *r.List)
	if err != nil {
		t.Fatalf("listView: %v", err)
	}
	if !v.IncludeResource {
		t.Fatal("matching element and response types must enable resource population")
	}
	if !strings.Contains(v.TimeoutsNull, "types.ObjectNull") {
		t.Errorf("the timeouts null must carry its attribute types: %s", v.TimeoutsNull)
	}

	r.List.ElementType = "tags.TagSummary"
	v, err = listView(bp, r, *r.List)
	if err != nil {
		t.Fatalf("listView: %v", err)
	}
	if v.IncludeResource {
		t.Error("a mismatched element type cannot feed the state mapper")
	}
}

// TestUnit_Render_TheListAccTestQueriesForTheSeededObject.
func TestUnit_Render_TheListAccTestQueriesForTheSeededObject(t *testing.T) {
	t.Parallel()

	bp, r := listFacetResource()

	v, fixture, err := ListAccTest(bp, r, Options{})
	if err != nil {
		t.Fatalf("ListAccTest: %v", err)
	}

	if v.QueryAddress != "te_tag.test" {
		t.Errorf("query address = %q", v.QueryAddress)
	}
	if fixture.ProviderName != "te" || fixture.TerraformType != "te_tag" {
		t.Errorf("fixture = %+v", fixture)
	}

	r.List = nil
	if _, _, err := ListAccTest(bp, r, Options{}); !isUnsupported(err) {
		t.Errorf("no facet must be a stated refusal: %v", err)
	}
}
