// Package fixturespec derives every fixture value one entity's generated
// tests use, exactly once.
//
// It exists because two renderings need the same values and must never
// disagree: the terraform configurations under tests/terraform are HCL,
// and the mock responder bodies and tests/responses files are wire JSON.
// Were each rendered from its own derivation, a create built from the HCL
// would meet a mock built from something else, and the lifecycle test
// would fail for a reason no source change explains. Here the derivation
// is one function of the attribute tree, and HCL and wire JSON are two
// formattings of its one result.
//
// Every value is deterministic: a function of the attribute path and the
// declared type alone, never the clock, never randomness — a regenerated
// fixture is byte-identical or the document changed. Synthesised strings
// carry the TestPrefix so anything a test creates against a live API is
// recognisable as toolkit debris and can be cleaned up by name.
package fixturespec

import (
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/intermediate_representation"
)

// TestPrefix marks every synthesised name-bearing string. The audit's
// cleanup contract matches live objects by this prefix, so fixture values
// sent to a real API identify themselves as deletable.
const TestPrefix = "tfpfgen-test-"

// Spec is the single derivation of one entity's fixture values.
type Spec struct {
	// Values holds one derived value per supported attribute, in
	// attribute-tree order.
	Values []Value
	// Skipped lists the attributes no value could be derived for, with
	// the reason each was refused.
	Skipped []Skip
}

// Value is one attribute's derived fixture value.
type Value struct {
	// Name is the terraform attribute name; Wire the property the API
	// speaks.
	Name string
	Wire string
	// Kind and ElemKind mirror the attribute's kinds.
	Kind     ir.TypeKind
	ElemKind ir.TypeKind
	// Presence decides which renderings carry the value: configurations
	// carry writable attributes, responses carry readable ones.
	Presence ir.Presence
	// Scalar is the value of a scalar attribute — string, bool, int64 or
	// float64 — or the single element's value for a list of scalars. Nil
	// for object kinds.
	Scalar any
	// Nested are the field values of an object attribute, or of the one
	// element a list of objects carries.
	Nested []Value
}

// Skip is one attribute that has no fixture value, and why.
type Skip struct {
	// Name is the dotted attribute path from the entity root.
	Name   string
	Reason string
}

// Audience selects which attributes a rendering carries.
type Audience int

// Renderings.
const (
	// ConfigMinimal is the smallest applying configuration: required
	// attributes only.
	ConfigMinimal Audience = iota
	// ConfigMaximal is the fullest configuration: every writable
	// attribute.
	ConfigMaximal
	// ResponseMinimal is what the server answers after a minimal create:
	// the required values plus everything the server fills itself.
	ResponseMinimal
	// ResponseMaximal is what the server answers after a maximal create:
	// every attribute.
	ResponseMaximal
)

// Derive computes the fixture values for one entity's attribute tree.
// entityKey seeds nothing — values depend only on attribute paths — but
// names the entity in skip reasons. A nil tree derives an empty spec.
func Derive(tree *ir.AttributeTree) Spec {
	var s Spec
	if tree == nil {
		return s
	}
	s.Values, s.Skipped = deriveTree(tree, nil)
	return s
}

// deriveTree walks one attribute tree, carrying the dotted path for skip
// reporting and the dashed path for string synthesis.
func deriveTree(tree *ir.AttributeTree, path []string) ([]Value, []Skip) {
	var values []Value
	var skips []Skip
	for _, a := range tree.Attributes {
		at := append(append([]string{}, path...), a.Name)
		if a.Unsupported {
			reason := a.UnsupportedReason
			if reason == "" {
				reason = "the derivation refused this attribute's shape"
			}
			skips = append(skips, Skip{Name: strings.Join(at, "."), Reason: reason})
			continue
		}
		v := Value{
			Name:     a.Name,
			Wire:     a.WireName,
			Kind:     a.Kind,
			ElemKind: a.ElemKind,
			Presence: a.Presence,
		}
		switch {
		case a.Nested != nil:
			nested, nestedSkips := deriveTree(a.Nested, at)
			v.Nested = nested
			skips = append(skips, nestedSkips...)
		case a.Kind == ir.TypeList:
			v.Scalar = scalarFor(a.ElemKind, a, at)
		default:
			v.Scalar = scalarFor(a.Kind, a, at)
		}
		values = append(values, v)
	}
	return values, skips
}

// scalarFor synthesises one scalar value: enum-driven when the document
// declares values, type-driven otherwise, with strings carrying the test
// prefix and the attribute path so no two attributes share a value.
func scalarFor(kind ir.TypeKind, a ir.Attribute, path []string) any {
	switch kind {
	case ir.TypeBool:
		return true
	case ir.TypeInt64:
		return int64(7)
	case ir.TypeFloat64:
		return 1.5
	default:
		if len(a.OneOf) > 0 {
			return a.OneOf[0]
		}
		if len(a.AdvisoryValues) > 0 {
			return a.AdvisoryValues[0]
		}
		return TestPrefix + strings.ReplaceAll(strings.Join(path, "-"), "_", "-")
	}
}

// wanted reports whether a value's attribute travels in the given
// rendering: configurations carry what a practitioner writes, responses
// carry what the server sends back.
func (v Value) wanted(a Audience) bool {
	switch a {
	case ConfigMinimal:
		return v.Presence == ir.PresenceRequired
	case ConfigMaximal:
		return v.Presence != ir.PresenceComputed
	case ResponseMinimal:
		return v.Presence != ir.PresenceOptional
	default: // ResponseMaximal
		return true
	}
}

// selected filters one level of values for an audience. Nested levels are
// filtered against the same audience by the renderers as they recurse.
func selected(values []Value, a Audience) []Value {
	out := make([]Value, 0, len(values))
	for _, v := range values {
		if v.wanted(a) {
			out = append(out, v)
		}
	}
	return out
}
