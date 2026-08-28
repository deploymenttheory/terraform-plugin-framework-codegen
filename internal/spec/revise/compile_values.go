// The values rules of the correction compiler: an enum corrected from what
// the live API accepted and refused, with acceptance anywhere on a shared
// site outranking one entity's refusal.

package revise

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
	"gopkg.in/yaml.v3"
)

// values compiles one values observation: documented-but-rejected values
// leave the enum (each removal guarded by a test of what the index holds),
// accepted-but-undocumented values join it, and an open set becomes
// x-tfpfgen-values on the property.
func (c *compiler) values(loc *locator, cls specmodel.Classification, o observe.Observation) (compiled, error) {
	var values observe.Values
	raw, err := json.Marshal(o.Value)
	if err == nil {
		err = json.Unmarshal(raw, &values)
	}
	if err != nil {
		return compiled{}, fmt.Errorf("observation %s: its value is not a values record: %w", o.ID, err)
	}

	site, why, ok := c.property(loc, cls, o)
	if !ok {
		return why, nil
	}
	enumDeclaration, enumPtr, hasEnum := loc.enumSite(site.property, site.propPtr)
	if !hasEnum {
		return stated("the document declares no enum for the property; there is nothing to correct"), nil
	}

	// entry mirrors the enum for sequential index computation: each removal
	// shifts what follows, so the ops are derived against this simulation.
	type entry struct {
		text  string
		value any
	}
	var sim []entry
	for _, n := range mapValue(enumDeclaration, "enum").Content {
		var v any
		if err := n.Decode(&v); err != nil {
			return compiled{}, fmt.Errorf("%s/enum: %w", enumPtr, err)
		}
		sim = append(sim, entry{text: n.Value, value: v})
	}
	indexOf := func(text string) int {
		for i, e := range sim {
			if e.text == text {
				return i
			}
		}
		return -1
	}

	var operations []correction.Operation
	var parts []string

	rejected := append([]string(nil), values.Rejected...)
	sort.Strings(rejected)
	var removed, kept []string
	for _, r := range rejected {
		index := indexOf(r)
		if index < 0 {
			continue
		}
		// The enum site is shared between every property resolving to it,
		// and a value one entity's create refuses is one another entity's
		// create sends successfully — one interval among eight test types.
		// Acceptance anywhere on the site is the fact about the document.
		if c.enumAccepted[enumPtr][r] {
			kept = append(kept, r)
			continue
		}
		at := enumPtr + "/enum/" + strconv.Itoa(index)
		operations = append(operations,
			correction.Operation{Op: "test", Path: at, Value: sim[index].value},
			correction.Operation{Op: "remove", Path: at},
		)
		sim = append(sim[:index], sim[index+1:]...)
		removed = append(removed, r)
	}
	if len(removed) > 0 {
		parts = append(parts, fmt.Sprintf("rejects the documented value(s) %s", strings.Join(removed, ", ")))
	}
	if len(kept) > 0 {
		parts = append(parts, fmt.Sprintf("refuses %s here, which stays declared because another entity sharing the enum accepts it", strings.Join(kept, ", ")))
	}

	accepted := append([]string(nil), values.Accepted...)
	sort.Strings(accepted)
	var added []string
	for _, a := range accepted {
		if indexOf(a) >= 0 {
			continue
		}
		operations = append(operations, correction.Operation{Op: "add", Path: enumPtr + "/enum/-", Value: a})
		sim = append(sim, entry{text: a, value: a})
		added = append(added, a)
	}
	if len(added) > 0 {
		parts = append(parts, fmt.Sprintf("accepts the undocumented value(s) %s", strings.Join(added, ", ")))
	}

	if values.Closed != nil && !*values.Closed {
		if extension := loc.extensionNode(site.property, site.propPtr, specmodel.ExtValues); extension == nil || extension.Value != "true" {
			operations = append(operations, correction.Operation{Op: "add", Path: site.propPtr + "/" + specmodel.ExtValues, Value: true})
			parts = append(parts, fmt.Sprintf("accepts values beyond the documented set (%s)", specmodel.ExtValues))
		}
	}

	if len(operations) == 0 {
		if len(kept) > 0 {
			return stated(fmt.Sprintf("the value(s) %s stay declared: another entity sharing the enum accepts them", strings.Join(kept, ", "))), nil
		}
		return stated("the document already agrees with the observed value set"), nil
	}
	return compiled{
		operations: operations,
		justification: fmt.Sprintf("the audit confirmed a values observation on %s.%s: the live API %s",
			o.Entity, o.Attribute, strings.Join(parts, "; ")),
	}, nil
}

// acceptedValueSites gathers, per enum site, every value a confirmed values
// observation in obs accepted, on top of the values already known accepted
// (an accepted correction's additions). Sites are located against the
// compiler's current state; a site is a schema pointer, which no correction
// moves.
func (c *compiler) acceptedValueSites(obs []observe.Observation, known map[string]map[string]bool) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for site, values := range known {
		out[site] = map[string]bool{}
		for v := range values {
			out[site][v] = true
		}
	}
	var root yaml.Node
	if err := yaml.Unmarshal(c.state, &root); err != nil || root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return out
	}
	loc := &locator{top: root.Content[0]}
	for _, o := range obs {
		if o.Outcome != observe.OutcomeConfirmed {
			continue
		}
		var accepted []string
		switch o.Kind {
		case observe.KindValues:
			var values observe.Values
			raw, err := json.Marshal(o.Value)
			if err != nil || json.Unmarshal(raw, &values) != nil {
				continue
			}
			accepted = values.Accepted
		case observe.KindWritable:
			// A create the API took carried the value: the excerpt's request
			// is that body, and its value for the attribute is one accepted.
			accepted = acceptedRequestValues(o)
		default:
			continue
		}
		if len(accepted) == 0 {
			continue
		}
		cls, ok := c.entities[o.Entity]
		if !ok {
			continue
		}
		site, _, ok := c.property(loc, cls, o)
		if !ok {
			continue
		}
		_, enumPtr, hasEnum := loc.enumSite(site.property, site.propPtr)
		if !hasEnum {
			continue
		}
		if out[enumPtr] == nil {
			out[enumPtr] = map[string]bool{}
		}
		for _, v := range accepted {
			out[enumPtr][v] = true
		}
	}
	return out
}

// acceptedRequestValues is every scalar value the observation's 2xx
// excerpts sent for its attribute: the values of a body the API took.
func acceptedRequestValues(o observe.Observation) []string {
	var out []string
	for _, excerpt := range o.Excerpts {
		if excerpt.Status < 200 || excerpt.Status > 299 || len(excerpt.RequestFragment) == 0 {
			continue
		}
		var request map[string]any
		if json.Unmarshal(excerpt.RequestFragment, &request) != nil {
			continue
		}
		switch v := request[o.Attribute].(type) {
		case string:
			out = append(out, v)
		case float64, bool:
			out = append(out, fmt.Sprint(v))
		}
	}
	return out
}
