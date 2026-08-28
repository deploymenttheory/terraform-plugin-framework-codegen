package intermediate_representation

import (
	"testing"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// mustLoad parses an inline document through the real loader, so every
// fixture exercises exactly what generation will read.
func mustLoad(t *testing.T, document string) *specmodel.Document {
	t.Helper()
	d, err := specmodel.Load([]byte(document))
	if err != nil {
		t.Fatalf("loading the fixture: %v", err)
	}
	return d
}

// testConfig is the minimal config Derive needs, built directly rather
// than through YAML: the schema structs are exported for exactly this.
func testConfig(exclude ...string) *config.Config {
	return &config.Config{
		Provider: config.Provider{Name: "acme"},
		Services: config.Services{Exclude: exclude},
	}
}

// mustDerive derives or ends the test.
func mustDerive(t *testing.T, document string, configuration *config.Config) *Model {
	t.Helper()
	m, err := Derive(mustLoad(t, document), configuration)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return m
}

// attribute finds a tree attribute by terraform name, or ends the test.
func attribute(t *testing.T, tree *AttributeTree, name string) Attribute {
	t.Helper()
	if tree == nil {
		t.Fatalf("no tree to find %q in", name)
	}
	for _, a := range tree.Attributes {
		if a.Name == name {
			return a
		}
	}
	names := make([]string, 0, len(tree.Attributes))
	for _, a := range tree.Attributes {
		names = append(names, a.Name)
	}
	t.Fatalf("no attribute %q in tree; have %v", name, names)
	return Attribute{}
}

// resourceByKey finds a model resource, or ends the test.
func resourceByKey(t *testing.T, m *Model, key string) Resource {
	t.Helper()
	for _, r := range m.Resources {
		if r.Names.Key == key {
			return r
		}
	}
	t.Fatalf("no resource %q in the model", key)
	return Resource{}
}

// listResourceByKey finds a model list resource, or ends the test.
func listResourceByKey(t *testing.T, m *Model, key string) ListResource {
	t.Helper()
	for _, lr := range m.ListResources {
		if lr.Names.Key == key {
			return lr
		}
	}
	t.Fatalf("no list resource %q in the model", key)
	return ListResource{}
}

// datasourceByKey finds a model datasource, or ends the test.
func datasourceByKey(t *testing.T, m *Model, key string) Datasource {
	t.Helper()
	for _, d := range m.Datasources {
		if d.Names.Key == key {
			return d
		}
	}
	t.Fatalf("no datasource %q in the model", key)
	return Datasource{}
}

// thingSpec is the main fixture: one full-lifecycle entity exercising every
// attribute rule, an action beneath it, a list-only entity, a lookup-only
// entity, a list+read entity, and one unclassifiable path.
const thingSpec = `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /v7/things:
    post:
      operationId: createThing
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/ThingCreate'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Thing'}
    get:
      operationId: listThings
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items: {$ref: '#/components/schemas/Thing'}
  /v7/things/{thingId}:
    parameters:
      - {name: thingId, in: path, required: true, schema: {type: string}}
    get:
      operationId: getThing
      x-tfpfgen-read-after-write: 30s
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Thing'}
    patch:
      operationId: updateThing
      x-tfpfgen-update-style: put-full
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/ThingCreate'}
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Thing'}
    delete:
      operationId: deleteThing
      x-tfpfgen-delete-not-found-ok: true
      responses:
        "204": {description: gone}
  /v7/things/restart:
    post:
      operationId: restartThings
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: [force]
              properties:
                force: {type: boolean}
      responses:
        "202": {description: accepted}
  /v7/events:
    get:
      operationId: listEvents
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    at: {type: string}
                    level: {type: string}
  /v7/settings/{settingName}:
    parameters:
      - {name: settingName, in: path, required: true, schema: {type: string}}
    get:
      operationId: getSetting
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  value: {type: string}
  /v7/streams:
    get:
      operationId: listStreams
      responses:
        "200":
          content:
            application/json:
              schema:
                type: array
                items: {$ref: '#/components/schemas/Stream'}
  /v7/streams/{streamId}:
    parameters:
      - {name: streamId, in: path, required: true, schema: {type: string}}
    get:
      operationId: getStream
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Stream'}
  /v7/junk/{junkId}:
    delete:
      operationId: deleteJunk
      responses:
        "204": {description: gone}
components:
  schemas:
    ThingCreate:
      type: object
      required: [name]
      x-tfpfgen-mutually-exclusive: [region, tier]
      x-tfpfgen-valid-configuration:
        discriminator: mode
        variants:
          standard: [quantity]
          custom: [proxyHost]
      properties:
        name: {type: string}
        mode:
          type: string
          enum: [standard, custom]
        region:
          type: string
          x-tfpfgen-immutable: true
        filled:
          type: integer
          x-tfpfgen-server-default: 1000
        tier:
          type: string
          enum: [gold, silver]
          x-tfpfgen-values: true
        proxyHost:
          type: string
          x-tfpfgen-required-when: {property: mode, equals: custom}
        notes:
          type: string
          x-tfpfgen-ignored-on-update: true
        quantity:
          type: integer
          x-tfpfgen-valid-when: {property: mode, equals: standard}
        ratio:
          type: number
          x-tfpfgen-depends-on: {requires: quantity}
        enabled: {type: boolean}
        labels:
          type: array
          items: {type: string}
        rules:
          type: array
          items:
            type: object
            required: [kind]
            properties:
              kind: {type: string}
              limit: {type: integer}
        settings:
          type: object
          properties:
            retries: {type: integer}
        extras:
          type: object
        forced:
          type: string
          x-tfpfgen-server-forced: true
        flaky:
          type: string
          x-tfpfgen-volatile: true
        stamp:
          type: string
          readOnly: true
    Thing:
      allOf:
        - $ref: '#/components/schemas/ThingCreate'
        - type: object
          required: [mode]
          properties:
            id: {type: string}
            etag: {type: string}
    Stream:
      type: object
      properties:
        id: {type: string}
        topic: {type: string}
`
