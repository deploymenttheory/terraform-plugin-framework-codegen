package intermediate_representation

import "testing"

const keyedSpec = `openapi: 3.0.3
info: {title: K, version: "1"}
paths:
  /widgets:
    post:
      operationId: createGadget
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Gadget'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Gadget'}
    get:
      operationId: listGadgets
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items: {$ref: '#/components/schemas/Gadget'}
  /widgets/{widgetId}:
    parameters:
      - {name: widgetId, in: path, required: true, schema: {type: string}}
    get:
      operationId: getGadget
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Gadget'}
    delete:
      operationId: deleteGadget
      responses:
        "204": {description: gone}
components:
  schemas:
    Gadget:
      type: object
      properties:
        widgetId: {type: string}
        name: {type: string}
`

// A list result is an identity and an identity is the resource's id, so the
// element has to answer the key the resource is addressed by — including where
// the API spells that key after the thing it identifies rather than as `id`.
func TestUnit_Derive_ListResourceTakesItsIDFromTheItemPathKey(t *testing.T) {
	model := mustDerive(t, keyedSpec, testConfig())

	listResource := listResourceByKey(t, model, "widget")
	id := attribute(t, listResource.Schema, "id")
	if id.WireName != "widgetId" {
		t.Errorf("the list element's id reads %q, want the item path key %q", id.WireName, "widgetId")
	}
	if id.Kind != TypeString || id.ComputedOptionalRequired != Computed {
		t.Errorf("id = %+v, want a computed string", id)
	}
	// The resource is addressed by the same key, and terraform matches the
	// two by type name: an identity that named a different field would list
	// identities the resource cannot be imported by.
	if got := attribute(t, resourceByKey(t, model, "widget").Schema, "id"); got.WireName != id.WireName {
		t.Errorf("resource id reads %q but the list element's reads %q", got.WireName, id.WireName)
	}
}

// An element that declares its own id keeps it: ensureID returns early, so
// the entities that published an identity before still publish the same one.
func TestUnit_Derive_ListResourceKeepsAnElementsOwnID(t *testing.T) {
	model := mustDerive(t, thingSpec, testConfig())

	id := attribute(t, listResourceByKey(t, model, "thing").Schema, "id")
	if id.WireName != "id" {
		t.Errorf("id reads %q, want the element's own %q", id.WireName, "id")
	}
}

// TestDerive_ListResource proves the list capability belongs to a resource
// and shares its terraform type, which is how terraform matches the two.
func TestUnit_Derive_ListResource(t *testing.T) {
	model := mustDerive(t, thingSpec, testConfig())
	for _, listResource := range model.ListResources {
		if listResource.Names.Key != "thing" {
			continue
		}
		if listResource.ListOperation.Kind != OperationList || listResource.ListOperation.Method != "GET" || listResource.ListOperation.SuccessCode != 200 {
			t.Errorf("list op = %+v", listResource.ListOperation)
		}
		resource := resourceByKey(t, model, "thing")
		if listResource.Names.TerraformType != resource.Names.TerraformType {
			t.Errorf("list resource type = %q, resource type = %q; terraform matches them by name",
				listResource.Names.TerraformType, resource.Names.TerraformType)
		}
		for _, name := range []string{"name", "id"} {
			if a := attribute(t, listResource.Schema, name); a.ComputedOptionalRequired != Computed {
				t.Errorf("%q = %+v", name, a)
			}
		}
		return
	}
	t.Fatalf("no thing list resource in %+v", model.ListResources)
}

// TestDerive_ListOnlyEntityIsADatasource proves a collection the API cannot
// address one member of yields a datasource: no resource can match it, and
// terraform refuses a provider whose list resource names no resource.
func TestUnit_Derive_ListOnlyEntityIsADatasource(t *testing.T) {
	model := mustDerive(t, thingSpec, testConfig())
	for _, listResource := range model.ListResources {
		if listResource.Names.Key == "event" {
			t.Fatalf("event is enumerable but not addressable, and became a list resource")
		}
	}
	datasourceByKey(t, model, "event")
}

// TestUnit_AddressingSchema_TakesEveryPathParameter proves a collection
// path's parameters all become required attributes of the list block: a
// collection path carries no item key, so none of them is absorbed by an id,
