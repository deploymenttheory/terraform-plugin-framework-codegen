package plan

import (
	"strings"
	"testing"
)

// singletonWithCreateSpec is one object at a fixed path whose collection
// path answers POST as well as GET and PUT. The classification calls it a
// singleton — its read is the collection GET and its update the collection
// write — and leaves the POST in the create slot, so an audit that told
// singletons apart by an empty create slot would not recognise this one.
const singletonWithCreateSpec = `openapi: 3.0.3
info: {title: S, version: "1"}
paths:
  /v1/distribution-point:
    get:
      operationId: getDistributionPoint
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/DistributionPoint'}
    put:
      operationId: updateDistributionPoint
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/DistributionPoint'}
      responses:
        "200":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/DistributionPoint'}
    post:
      operationId: createDistributionPoint
      requestBody:
        content:
          application/json:
            schema: {$ref: '#/components/schemas/DistributionPoint'}
      responses:
        "201":
          content:
            application/json:
              schema: {$ref: '#/components/schemas/DistributionPoint'}
components:
  schemas:
    DistributionPoint:
      type: object
      properties:
        name: {type: string}
`

// TestUnit_Plan_ASingletonWithACreateIsRefusedNotDereferenced pins the
// refusal to the classification's own answer rather than to an empty create
// slot. This shape has a create and no delete, so a planner reading the slot
// would carry on and address an item path and a delete the entity does not
// have — which is a panic, and a panic takes every other entity's findings
// with it.
func TestUnit_Plan_ASingletonWithACreateIsRefusedNotDereferenced(t *testing.T) {
	t.Parallel()
	p := mustDerive(t, loadDoc(t, singletonWithCreateSpec), testConfig(), nil)

	for _, ep := range p.Entities {
		if strings.Contains(ep.Entity, "distribution_point") {
			t.Fatalf("the singleton was planned rather than refused: %+v", ep)
		}
	}

	var reason string
	for _, s := range p.Skipped {
		if strings.Contains(s.Entity, "distribution_point") {
			reason = s.Reason
		}
	}
	if reason == "" {
		t.Fatal("the singleton was neither planned nor refused; it left no trace")
	}
	if !strings.Contains(reason, "singleton") {
		t.Errorf("refused for %q, want the reason to name the shape", reason)
	}
}
