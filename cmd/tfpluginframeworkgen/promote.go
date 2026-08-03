package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/fixturespec"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
)

// promotePlans folds probe-plan fixture values into accFixture wire hints, so the
// operator states a value exactly once -- in the plan, where a live run proved it --
// and the generated fixtures and the rehearsal both read it from the blueprint.
//
// Conservative on purpose: only attributes whose value the generator refuses to
// derive (nested objects of live identifiers, credential-shaped names, `_id`
// references) are promoted, plus refreshes of hints a previous promotion wrote.
// Values the generator synthesises fine stay synthesised -- a promoted constant
// would defeat the salting that keeps concurrent seeds from colliding. Hand-written
// hints are never touched: a person's stated value outranks a mechanical copy.
func promotePlans(bp *blueprint.Blueprint, planDir string) (int, error) {
	promoted := 0

	for i := range bp.Resources {
		res := &bp.Resources[i]

		plan, found, err := loadPlanIfFound(filepath.Join(planDir, res.Key+".probe.plan.json"))
		if err != nil {
			return promoted, err
		}
		if !found || len(plan.Fixtures) == 0 {
			continue
		}

		nameField := sweepNameFieldOf(*res)

		var attrs []blueprint.Attribute
		for _, a := range res.Schema.Attributes {
			attrs = append(attrs, a)
		}
		sort.Slice(attrs, func(i, j int) bool { return attrs[i].Name < attrs[j].Name })

		for _, a := range attrs {
			if a.Drop || a.ComputedOptionalRequired == blueprint.Computed {
				continue
			}
			path := a.Wire.JSONPath
			if path == "" || strings.Contains(path, ".") || path == nameField {
				// The name field is stamped at run time; its fixture value is a
				// placeholder nothing should copy.
				continue
			}

			v, inPlan := planValue(plan, path)
			if !inPlan {
				continue
			}

			existing, hasHint := hintFor(res, a.Name)
			if hasHint && existing.Source != "plan" {
				continue // hand-written; a person's value outranks a mechanical copy
			}
			if !hasHint && !fixturespec.Derive(a, "").Skipped {
				continue // the generator derives this fine; promotion would defeat salting
			}

			hcl, ok := wireHCL(a, v)
			if !ok {
				fmt.Fprintf(os.Stderr,
					"  note  %s.%s: the plan fixture's value has no HCL rendering here "+
						"and was not promoted\n", res.Key, a.Name)
				continue
			}

			hint := blueprint.FixtureHint{Attr: a.Name, HCL: hcl, Wire: v, Source: "plan"}
			if hasHint && existing.HCL == hint.HCL {
				continue // refresh with nothing to refresh
			}

			upsertHint(res, hint)
			promoted++
		}
	}

	return promoted, nil
}

// loadPlanIfFound is loadPlan with absence as a non-error, matching probeRun.planFor.
func loadPlanIfFound(path string) (plan probe.Plan, found bool, err error) {
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return plan, false, nil
	}

	loaded, err := loadPlan(path)
	if err != nil {
		return plan, false, err
	}

	return loaded, true, nil
}

// sweepNameFieldOf mirrors the subject's name-field resolution closely enough for
// promotion: the blueprint override when declared, else nothing worth excluding --
// a wrongly-promoted name value would carry the plan's placeholder into a fixture,
// so over-excluding is the safe direction and the override is the common case.
func sweepNameFieldOf(res blueprint.Resource) string {
	if res.Sweep != nil {
		return res.Sweep.NameField
	}
	return "name"
}

// hintFor finds an attribute's existing hint.
func hintFor(res *blueprint.Resource, attr string) (blueprint.FixtureHint, bool) {
	if res.AccFixture == nil {
		return blueprint.FixtureHint{}, false
	}
	for _, h := range res.AccFixture.Values {
		if h.Attr == attr {
			return h, true
		}
	}
	return blueprint.FixtureHint{}, false
}

// upsertHint replaces an attribute's hint or appends one, keeping the list sorted so
// the committed document is byte-stable.
func upsertHint(res *blueprint.Resource, hint blueprint.FixtureHint) {
	if res.AccFixture == nil {
		res.AccFixture = &blueprint.AccFixture{}
	}

	for i, h := range res.AccFixture.Values {
		if h.Attr == hint.Attr {
			res.AccFixture.Values[i] = hint
			return
		}
	}

	res.AccFixture.Values = append(res.AccFixture.Values, hint)
	sort.Slice(res.AccFixture.Values, func(i, j int) bool {
		return res.AccFixture.Values[i].Attr < res.AccFixture.Values[j].Attr
	})
}

// wireHCL renders a wire value as the HCL a fixture would carry: scalars, lists of
// scalars, and lists of flat objects whose keys map back through the nested schema.
// Anything deeper has no mechanical rendering and stays a human's job.
func wireHCL(a blueprint.Attribute, v any) (string, bool) {
	switch tv := v.(type) {
	case string:
		return strconv.Quote(tv), true
	case bool:
		return strconv.FormatBool(tv), true
	case float64:
		return strconv.FormatFloat(tv, 'f', -1, 64), true
	case int64:
		return strconv.FormatInt(tv, 10), true
	case []any:
		if len(tv) == 0 {
			return "[]", true
		}
		parts := make([]string, 0, len(tv))
		for _, elem := range tv {
			switch e := elem.(type) {
			case map[string]any:
				obj, ok := objectHCL(a, e)
				if !ok {
					return "", false
				}
				parts = append(parts, obj)
			case string:
				parts = append(parts, strconv.Quote(e))
			case bool:
				parts = append(parts, strconv.FormatBool(e))
			case float64:
				parts = append(parts, strconv.FormatFloat(e, 'f', -1, 64))
			default:
				return "", false
			}
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	default:
		return "", false
	}
}

// objectHCL renders one flat wire object using the nested schema's attribute names.
func objectHCL(a blueprint.Attribute, obj map[string]any) (string, bool) {
	nested := a.Type.NestedObject
	if nested == nil {
		return "", false
	}

	byPath := map[string]string{}
	for _, child := range nested.Attributes {
		byPath[child.Wire.JSONPath] = child.Name
	}

	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		name, known := byPath[k]
		if !known {
			return "", false
		}
		val, ok := wireHCL(blueprint.Attribute{}, obj[k])
		if !ok {
			return "", false
		}
		parts = append(parts, name+" = "+val)
	}

	return "{ " + strings.Join(parts, ", ") + " }", true
}
