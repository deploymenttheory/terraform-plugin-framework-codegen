package intermediate_representation

import "testing"

const keyedSpec = `openapi: 3.0.3
info: {title: K, version: "1"}
paths:
  /gadgets:
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
  /gadgets/{gadgetId}:
    parameters:
      - {name: gadgetId, in: path, required: true, schema: {type: string}}
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
        gadgetId: {type: string}
        name: {type: string}
`

// A list result is an identity and an identity is the resource's id, so the
// element has to answer the key the resource is addressed by. An API that
// spells that key after the thing it identifies used to publish no identity
// at all, and the entity was refused downstream for the wording.
func TestDerive_ListResourceTakesItsIDFromTheItemPathKey(t *testing.T) {
	m := mustDerive(t, keyedSpec, testConfig())

	lr := listResourceByKey(t, m, "gadget")
	id := attribute(t, lr.Schema, "id")
	if id.WireName != "gadgetId" {
		t.Errorf("the list element's id reads %q, want the item path key %q", id.WireName, "gadgetId")
	}
	if id.Kind != TypeString || id.ComputedOptionalRequired != Computed {
		t.Errorf("id = %+v, want a computed string", id)
	}
	// The resource is addressed by the same key, and terraform matches the
	// two by type name: an identity that named a different field would list
	// identities the resource cannot be imported by.
	if got := attribute(t, resourceByKey(t, m, "gadget").Schema, "id"); got.WireName != id.WireName {
		t.Errorf("resource id reads %q but the list element's reads %q", got.WireName, id.WireName)
	}
}

// An element that declares its own id keeps it: ensureID returns early, so
// the entities that published an identity before still publish the same one.
func TestDerive_ListResourceKeepsAnElementsOwnID(t *testing.T) {
	m := mustDerive(t, thingSpec, testConfig())

	id := attribute(t, listResourceByKey(t, m, "thing").Schema, "id")
	if id.WireName != "id" {
		t.Errorf("id reads %q, want the element's own %q", id.WireName, "id")
	}
}

// TestDerive_ListResource proves the list capability belongs to a resource
// and shares its terraform type, which is how terraform matches the two.
func TestDerive_ListResource(t *testing.T) {
	m := mustDerive(t, thingSpec, testConfig())
	for _, lr := range m.ListResources {
		if lr.Names.Key != "thing" {
			continue
		}
		if lr.ListOperation.Kind != OperationList || lr.ListOperation.Method != "GET" || lr.ListOperation.SuccessCode != 200 {
			t.Errorf("list op = %+v", lr.ListOperation)
		}
		resource := resourceByKey(t, m, "thing")
		if lr.Names.TerraformType != resource.Names.TerraformType {
			t.Errorf("list resource type = %q, resource type = %q; terraform matches them by name",
				lr.Names.TerraformType, resource.Names.TerraformType)
		}
		for _, name := range []string{"name", "id"} {
			if a := attribute(t, lr.Schema, name); a.ComputedOptionalRequired != Computed {
				t.Errorf("%q = %+v", name, a)
			}
		}
		return
	}
	t.Fatalf("no thing list resource in %+v", m.ListResources)
}

// TestDerive_ListOnlyEntityIsADatasource proves a collection the API cannot
// address one member of yields a datasource: no resource can match it, and
// terraform refuses a provider whose list resource names no resource.
func TestDerive_ListOnlyEntityIsADatasource(t *testing.T) {
	m := mustDerive(t, thingSpec, testConfig())
	for _, lr := range m.ListResources {
		if lr.Names.Key == "event" {
			t.Fatalf("event is enumerable but not addressable, and became a list resource")
		}
	}
	datasourceByKey(t, m, "event")
}

// TestUnit_AddressingSchema_TakesEveryPathParameter proves a collection
// path's parameters all become required attributes of the list block: a
// collection path carries no item key, so none of them is absorbed by an id,
