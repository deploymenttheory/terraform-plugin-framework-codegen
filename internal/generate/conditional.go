package generate

import (
	"fmt"
	"sort"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// requiredWhenRules derives the value-conditional requirements a resource's behaviour
// variants record: attribute X must be set while attribute Y holds one specific value.
//
// The framework's stock validators cannot express this -- AlsoRequires relates presence to
// presence, never presence to a *value* -- so each rule becomes an instance of the small
// requiredWhen validator emitted beside the resource, self-contained in the generated
// package rather than depending on a hand-written helper the provider may not have.
//
// A rule is derived only when everything it references can carry it:
//   - the variant observes requiredByApi=true under exactly one condition; a conjunctive
//     precondition has no single gate to read, and none has been measured yet,
//   - the target attribute is optional -- a required one is already enforced schema-wide,
//     and a computed one cannot be configured at all,
//   - the gate condition names a top-level wire path some string attribute of this schema
//     has, and that attribute is configurable. A gate the practitioner cannot set is a
//     branch the configuration cannot reach.
//
// Anything else stays where merge put it: in the attribute's description, visible and
// unenforced, which is the honest floor.
func requiredWhenRules(r blueprint.Resource) []RequiredWhenRule {
	byWire := map[string]blueprint.Attribute{}
	for _, a := range r.Schema.Attributes {
		if a.Drop {
			continue
		}
		path := a.Wire.JSONPath
		if path == "" {
			path = a.Name
		}
		byWire[path] = a
	}

	var out []RequiredWhenRule

	for _, a := range r.Schema.Attributes {
		if a.Drop || !a.ComputedOptionalRequired.IsOptional() {
			continue
		}

		for _, v := range a.Behaviour.Conditional {
			if v.Behaviour.RequiredByAPI == nil || !*v.Behaviour.RequiredByAPI {
				continue
			}
			if len(v.When) != 1 {
				continue
			}

			c := v.When[0]
			gate, ok := byWire[c.JSONPath]
			if !ok || gate.Type.Kind != blueprint.KindString ||
				gate.ComputedOptionalRequired == blueprint.Computed {
				continue
			}

			out = append(out, RequiredWhenRule{
				GateAttr:   gate.Name,
				Equals:     c.Equals,
				TargetAttr: a.Name,
			})
		}
	}

	// Sorted so the emitted list is byte-stable whatever order the variants arrived in.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.TargetAttr != b.TargetAttr {
			return a.TargetAttr < b.TargetAttr
		}
		if a.GateAttr != b.GateAttr {
			return a.GateAttr < b.GateAttr
		}
		return a.Equals < b.Equals
	})

	return out
}

// RequiredWhenRule is one evidence-derived conditional requirement, rendered as a
// requiredWhen validator entry.
type RequiredWhenRule struct {
	GateAttr   string
	Equals     string
	TargetAttr string
}

// entry renders the rule as a composite literal of the emitted requiredWhen type.
func (r RequiredWhenRule) entry() string {
	return fmt.Sprintf(
		"// The prober recorded that the API enforces %s when %s is %s;\n"+
			"// the specification does not declare it.\n"+
			"requiredWhen{gate: path.Root(%s), equals: %s, target: path.Root(%s)}",
		r.TargetAttr, r.GateAttr, goStringLit(r.Equals),
		goStringLit(r.GateAttr), goStringLit(r.Equals), goStringLit(r.TargetAttr))
}

// conditionalValidators renders the behaviour variants' rules into ConfigValidators
// entries, reporting whether the requiredWhen support type must be emitted beside them.
func conditionalValidators(r blueprint.Resource, imports *importSet) ([]string, bool) {
	rules := requiredWhenRules(r)
	if len(rules) == 0 {
		return nil, false
	}

	imports.add(pkgPath, "")

	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		out = append(out, rule.entry())
	}

	return out, true
}

// ConditionalValidatorView is what the requiredWhen support file template needs. The
// type is static; only the header and package vary.
type ConditionalValidatorView struct {
	Header  string
	Package string
	Imports string
}

// ConditionalValidatorFile builds the support-file view when the resource has any
// value-conditional rules, reporting false otherwise.
func ConditionalValidatorFile(
	r blueprint.Resource,
	opts Options,
) (ConditionalValidatorView, bool) {
	if len(requiredWhenRules(r)) == 0 {
		return ConditionalValidatorView{}, false
	}

	imports := newImportSet()
	imports.add("context", "")
	imports.add("fmt", "")
	imports.add(frameworkRoot+"attr", "")
	imports.add(pkgPath, "")
	imports.add(frameworkRoot+"resource", "")
	imports.add(frameworkRoot+"types", "")

	return ConditionalValidatorView{
		Header:  GeneratedHeader(opts.BlueprintPath, opts.BlueprintSHA256),
		Package: r.GoPackage,
		Imports: imports.render(""),
	}, true
}
