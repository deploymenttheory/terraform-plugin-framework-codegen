package intermediate_representation

import "testing"

// authoritySpec gives one entity four writable optional attributes, each
// reaching computed_optional by a different route and no other. Isolating
// them is the whole point: several routes reach that outcome at once in a
// real document, and a test where two apply proves nothing about which was
// recorded.
const authoritySpec = `openapi: 3.0.3
info: {title: A, version: "1"}
paths:
  /v1/widgets:
    post:
      operationId: createWidget
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/WidgetCreate'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Widget'}
    get:
      operationId: listWidgets
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items: {$ref: '#/components/schemas/Widget'}
  /v1/widgets/{widgetId}:
    parameters:
      - {name: widgetId, in: path, required: true, schema: {type: string}}
    get:
      operationId: getWidget
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Widget'}
    patch:
      operationId: updateWidget
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/WidgetCreate'}
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Widget'}
    delete:
      operationId: deleteWidget
      responses:
        "204": {description: gone}
components:
  schemas:
    WidgetCreate:
      type: object
      properties:
        measured:
          type: string
          x-tfpfgen-server-default: "east"
        asserted:
          type: string
        implied:
          type: string
          default: "quiet"
        described:
          type: string
        nested:
          type: object
          required: [inner]
          properties:
            inner: {type: string}
    Widget:
      type: object
      required: [asserted]
      properties:
        id: {type: string}
        measured: {type: string}
        asserted: {type: string}
        implied: {type: string}
        described: {type: string}
        nested:
          type: object
          properties:
            inner: {type: string}
`

// TestUnit_IntermediateRepresentation_AnAttributeRecordsTheAuthorityBehindItsPresence
// holds each route to the declaration that took it. Four declarations reach
// one outcome, and an outcome that does not say which produced it leaves
// the risk docs/contract.md accepts on requestDefault unmeasurable.
func TestUnit_IntermediateRepresentation_AnAttributeRecordsTheAuthorityBehindItsPresence(t *testing.T) {
	t.Parallel()
	tree := resourceByKey(t, mustDerive(t, authoritySpec, testConfig()), "widget").Attributes

	for name, want := range map[string]SchemaAttributeTypeDetermination{
		"measured":  SchemaAttributeTypeDeterminationServerDefault,
		"asserted":  SchemaAttributeTypeDeterminationResponseRequired,
		"implied":   SchemaAttributeTypeDeterminationRequestDefault,
		"described": SchemaAttributeTypeDeterminationResponseProperty,
	} {
		got := attribute(t, tree, name)
		if got.ComputedOptionalRequired != ComputedOptional {
			t.Errorf("%s: presence = %q, want %q — the fixture no longer isolates the route",
				name, got.ComputedOptionalRequired, ComputedOptional)
			continue
		}
		if got.SchemaAttributeTypeDetermination != want {
			t.Errorf("%s: authority = %q, want %q", name, got.SchemaAttributeTypeDetermination, want)
		}
	}
}

// TestUnit_IntermediateRepresentation_OnlyAComputedOptionalCarriesAnAuthority
// keeps the field to the one outcome several declarations compete for.
// Required and computed are each decided by one thing, so an authority on
// them would name a choice that was never made.
func TestUnit_IntermediateRepresentation_OnlyAComputedOptionalCarriesAnAuthority(t *testing.T) {
	t.Parallel()
	tree := resourceByKey(t, mustDerive(t, authoritySpec, testConfig()), "widget").Attributes

	if id := attribute(t, tree, "id"); id.ComputedOptionalRequired == ComputedOptional {
		t.Fatalf("id = %+v, want a presence no declaration competes for", id)
	} else if id.SchemaAttributeTypeDetermination != "" {
		t.Errorf("id carries authority %q, want none for a %q attribute", id.SchemaAttributeTypeDetermination, id.ComputedOptionalRequired)
	}
}

// TestUnit_IntermediateRepresentation_AMemberPromotedByItsParentCarriesNoAuthority
// pins the one computed_optional no declaration produced. A member of a
// server-filled object is promoted because of its parent, so naming an
// authority for it would attribute the presence to a declaration about the
// member that was never made.
func TestUnit_IntermediateRepresentation_AMemberPromotedByItsParentCarriesNoAuthority(t *testing.T) {
	t.Parallel()
	tree := resourceByKey(t, mustDerive(t, authoritySpec, testConfig()), "widget").Attributes

	parent := attribute(t, tree, "nested")
	if parent.ComputedOptionalRequired != ComputedOptional || parent.NestedAttributes == nil {
		t.Fatalf("nested = %+v, want a server-filled object", parent)
	}
	member := attribute(t, parent.NestedAttributes, "inner")
	if member.ComputedOptionalRequired != ComputedOptional {
		t.Fatalf("inner = %+v, want promoted by its parent", member)
	}
	if member.SchemaAttributeTypeDetermination != "" {
		t.Errorf("inner carries authority %q; its presence came from its parent, not a declaration about it", member.SchemaAttributeTypeDetermination)
	}
}
