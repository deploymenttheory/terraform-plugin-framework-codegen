package strategy

import (
	"sort"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/specmodel"
)

// detectGates finds the create body's gate candidates: the writable enum and
// boolean fields whose value plausibly selects a variant. Enums need at least
// two members to gate anything — a single-value enum is a constant, not a
// discriminator; a boolean always has its two.
//
// The result is ranked likeliest-first: required enum, then optional enum,
// then boolean, ties broken by field name so the order is deterministic.
func detectGates(createBody *specmodel.Schema) []Gate {
	fields := flatFields(createBody)
	var gates []Gate
	for _, f := range fields {
		r := f.schema.Resolved()
		switch {
		case isEnum(r):
			values := stringifyValues(r.Enum)
			if len(values) < 2 {
				continue
			}
			kind := GateOptionalEnum
			if f.required {
				kind = GateRequiredEnum
			}
			gates = append(gates, Gate{Field: f.name, Kind: kind, Values: values})
		case isBool(r):
			gates = append(gates, Gate{Field: f.name, Kind: GateBool, Values: []string{"false", "true"}})
		}
	}
	sort.Slice(gates, func(i, j int) bool {
		if gates[i].Kind.rank() != gates[j].Kind.rank() {
			return gates[i].Kind.rank() < gates[j].Kind.rank()
		}
		return gates[i].Field < gates[j].Field
	})
	return gates
}

// primaryGate is the likeliest discriminator — the first ranked gate — or nil
// when the resource has none. The variant field sets are derived against it.
func primaryGate(gates []Gate) *Gate {
	if len(gates) == 0 {
		return nil
	}
	return &gates[0]
}
