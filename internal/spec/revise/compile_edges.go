package revise

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/spec/correction"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// compile_edges.go compiles the conditional-edge observation kinds the
// triangulating inference asserts — validWhen, dependsOn, mutuallyExclusive
// and validConfiguration — into their corrections, plus the small value
// helpers they share. Kept beside compile.go so the scalar-kind rules and the
// edge rules can each stay under the file-size limit.

// validWhen compiles a value-conditional validity into x-tfpfgen-valid-when
// on the subject property: the field is valid only when the gate equals the
// value. It mirrors requiredWhen's shape — the inverse conditional edge.
func (c *compiler) validWhen(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	if o.Value != true {
		return stated("the observation confirms the document's own reading; there is nothing to correct")
	}
	site, why, ok := c.property(loc, cls, o)
	if !ok {
		return why
	}
	equals := literalSpelling(o.Condition.Equals)
	if extension := loc.extensionNode(site.property, site.propPtr, specmodel.ExtValidWhen); extension != nil {
		if p, e := mapValue(extension, "property"), mapValue(extension, "equals"); p != nil && e != nil &&
			p.Value == o.Condition.Attribute && e.Value == equals {
			return stated("the document already declares this conditional validity")
		}
	}
	return compiled{
		operations: []correction.Operation{{
			Op:   "add",
			Path: site.propPtr + "/" + specmodel.ExtValidWhen,
			Value: map[string]string{
				"property": o.Condition.Attribute,
				"equals":   equals,
			},
		}},
		justification: fmt.Sprintf("the audit confirmed a validWhen observation on %s.%s: "+
			"the property is valid only when %s equals %q (%s)",
			o.Entity, o.Attribute, o.Condition.Attribute, equals, specmodel.ExtValidWhen),
	}
}

// dependsOn compiles a co-requirement. Standard OpenAPI can say it directly
// with dependentRequired, but only from 3.1 — the keyword is not in the 3.0
// schema subset — so a 3.0 document gets the x-tfpfgen-depends-on extension
// instead. Either way the fact is the same: the field is settable only when
// the required sibling is present.
func (c *compiler) dependsOn(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	requires, _ := o.Value.(string)
	if requires == "" {
		return unplaceable(fmt.Sprintf("a dependsOn observation on %s.%s names no required field", o.Entity, o.Attribute))
	}
	site, why, ok := c.property(loc, cls, o)
	if !ok {
		return why
	}
	if supportsDependentRequired(loc) {
		return c.dependentRequired(site, o, requires)
	}
	if extension := loc.extensionNode(site.property, site.propPtr, specmodel.ExtDependsOn); extension != nil {
		if r := mapValue(extension, "requires"); r != nil && r.Value == requires {
			return stated("the document already declares this dependency")
		}
	}
	return compiled{
		operations: []correction.Operation{{
			Op:    "add",
			Path:  site.propPtr + "/" + specmodel.ExtDependsOn,
			Value: map[string]string{"requires": requires},
		}},
		justification: fmt.Sprintf("the audit confirmed a dependsOn observation on %s.%s: "+
			"the property is settable only when %s is also present (%s)",
			o.Entity, o.Attribute, requires, specmodel.ExtDependsOn),
	}
}

// dependentRequired compiles a co-requirement into the standard JSON-Schema
// dependentRequired keyword on the declaring schema, extending the map when
// it already exists rather than clobbering a sibling dependency.
func (c *compiler) dependentRequired(site propertyLocation, o observe.Observation, requires string) compiled {
	just := fmt.Sprintf("the audit confirmed a dependsOn observation on %s.%s: "+
		"the property is settable only when %s is also present (dependentRequired)",
		o.Entity, o.Attribute, requires)
	dr := mapValue(site.declaration, "dependentRequired")
	switch {
	case dr == nil:
		return compiled{operations: []correction.Operation{{
			Op: "add", Path: site.declarationPointer + "/dependentRequired",
			Value: map[string]any{o.Attribute: []any{requires}},
		}}, justification: just}
	case mapValue(dr, o.Attribute) == nil:
		return compiled{operations: []correction.Operation{{
			Op: "add", Path: site.declarationPointer + "/dependentRequired/" + escapeToken(o.Attribute),
			Value: []any{requires},
		}}, justification: just}
	}
	entry := mapValue(dr, o.Attribute)
	for _, n := range entry.Content {
		if n.Value == requires {
			return stated("the document already declares this dependency")
		}
	}
	return compiled{operations: []correction.Operation{{
		Op: "add", Path: site.declarationPointer + "/dependentRequired/" + escapeToken(o.Attribute) + "/-",
		Value: requires,
	}}, justification: just}
}

// mutuallyExclusive compiles an at-most-one-of set into
// x-tfpfgen-mutually-exclusive on the entity schema. The finding is about the
// set, not one field, so it lands at the schema level.
func (c *compiler) mutuallyExclusive(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	fields := stringList(o.Value)
	if len(fields) < 2 {
		return unplaceable(fmt.Sprintf("a mutuallyExclusive observation on %s names fewer than two fields", o.Entity))
	}
	node, pointer, ok := c.schemaSite(loc, cls)
	if !ok {
		return unplaceable(fmt.Sprintf("no create or read schema of %s can carry a mutually-exclusive set", o.Entity))
	}
	sort.Strings(fields)
	if extension := mapValue(node, specmodel.ExtMutuallyExclusive); extension != nil && sameStringSeq(extension, fields) {
		return stated(fmt.Sprintf("the document already declares %s", specmodel.ExtMutuallyExclusive))
	}
	return compiled{
		operations: []correction.Operation{{
			Op: "add", Path: pointer + "/" + specmodel.ExtMutuallyExclusive, Value: toAnyList(fields),
		}},
		justification: fmt.Sprintf("the audit confirmed a mutuallyExclusive observation on %s: "+
			"at most one of %s may be set (%s)", o.Entity, strings.Join(fields, ", "), specmodel.ExtMutuallyExclusive),
	}
}

// validConfiguration compiles a discriminator variant structure into
// x-tfpfgen-valid-configuration on the discriminator's declaring schema. The
// observation carries only the gate values; the per-value field sets are
// assembled from the validWhen edges the same run confirmed against this gate.
func (c *compiler) validConfiguration(loc *locator, cls specmodel.Classification, o observe.Observation) compiled {
	values := stringList(o.Value)
	if len(values) == 0 {
		return unplaceable(fmt.Sprintf("a validConfiguration observation on %s.%s names no gate values", o.Entity, o.Attribute))
	}
	site, why, ok := c.property(loc, cls, o)
	if !ok {
		return why
	}
	edges := c.variants[[2]string{o.Entity, o.Attribute}]
	variantMap := map[string]any{}
	sort.Strings(values)
	for _, v := range values {
		fields := append([]string(nil), edges[v]...)
		sort.Strings(fields)
		variantMap[v] = toAnyList(uniqueStrings(fields))
	}
	value := map[string]any{"discriminator": o.Attribute, "variants": variantMap}
	if extension := mapValue(site.declaration, specmodel.ExtValidConfiguration); extension != nil {
		var current any
		if extension.Decode(&current) == nil && jsonEqual(current, value) {
			return stated(fmt.Sprintf("the document already declares %s", specmodel.ExtValidConfiguration))
		}
	}
	return compiled{
		operations: []correction.Operation{{
			Op: "add", Path: site.declarationPointer + "/" + specmodel.ExtValidConfiguration, Value: value,
		}},
		justification: fmt.Sprintf("the audit confirmed a validConfiguration observation on %s.%s: "+
			"the property gates the valid field set, one variant per value (%s)",
			o.Entity, o.Attribute, specmodel.ExtValidConfiguration),
	}
}

// schemaSite is the entity's object schema for a schema-level annotation:
// the create request schema first, the read response schema second, the same
// order property lookup folds the two views in.
func (c *compiler) schemaSite(loc *locator, cls specmodel.Classification) (*yaml.Node, string, bool) {
	if node, pointer, ok := loc.requestSchema(cls.Create); ok {
		return node, pointer, true
	}
	return loc.responseSchema(cls.Read)
}

// stringList normalises an observation value that should be a list of names,
// accepting the typed []string and the []any a JSON round trip produces.
func stringList(v any) []string {
	switch t := v.(type) {
	case []string:
		return append([]string(nil), t...)
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// toAnyList widens a string slice into the []any a correction value carries,
// so its JSON encodes as a plain array of strings.
func toAnyList(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// uniqueStrings drops duplicates from an already-sorted slice.
func uniqueStrings(ss []string) []string {
	out := ss[:0:0]
	for i, s := range ss {
		if i == 0 || s != ss[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// sameStringSeq reports whether a YAML sequence node holds exactly the given
// strings in order.
func sameStringSeq(seq *yaml.Node, want []string) bool {
	if seq == nil || seq.Kind != yaml.SequenceNode || len(seq.Content) != len(want) {
		return false
	}
	for i, n := range seq.Content {
		if n.Value != want[i] {
			return false
		}
	}
	return true
}

// supportsDependentRequired reports whether the document's declared OpenAPI
// version admits the JSON-Schema dependentRequired keyword, which arrived
// with 3.1's full JSON Schema dialect.
func supportsDependentRequired(loc *locator) bool {
	v := mapValue(loc.top, "openapi")
	return v != nil && openAPIAtLeast(v.Value, 3, 1)
}

// openAPIAtLeast reports whether a "major.minor.patch" version string is at
// least the given major.minor.
func openAPIAtLeast(version string, major, minor int) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return false
	}
	gotMajor, err1 := strconv.Atoi(parts[0])
	gotMinor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return gotMajor > major || (gotMajor == major && gotMinor >= minor)
}
