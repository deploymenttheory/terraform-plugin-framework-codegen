package strategy

import (
	"sort"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/specmodel"
)

// maxVariantValues caps the gate values that become variants, so a wide enum
// yields a bounded program. The cap takes the sorted-first values, keeping the
// selection deterministic.
const maxVariantValues = 8

// deriveVariants composes the baseline variant and one variant per value of
// the primary gate. Each variant's field set is the common (base) writable
// fields plus, where a declared branch selects on the gate value, that
// branch's writable fields. A variant whose value maps to no declared branch
// still exists — its field set is the baseline, its provenance derived — so
// the executor still creates one object per value to observe value-conditional
// behaviour.
func deriveVariants(createBody *specmodel.Schema, gates []Gate) []Variant {
	base := flatFields(createBody)
	baseIndex := indexFields(base)

	baseline := Variant{
		Provenance: ProvenanceStructural,
		Minimal:    skeleton(requiredNames(base), baseIndex),
		Maximal:    skeleton(fieldNames(base), baseIndex),
	}
	variants := []Variant{baseline}

	gate := primaryGate(gates)
	if gate == nil {
		return variants
	}

	branches := gatherBranches(createBody)
	discriminator := createBody.Resolved().Discriminator

	values := gate.Values
	if len(values) > maxVariantValues {
		values = values[:maxVariantValues]
	}
	for _, value := range values {
		// Branch matching and the variant's identity are textual; only the
		// value the executor sends needs its declared type back.
		label := stringifyScalar(value)
		branch := matchBranch(gate.Field, label, branches, discriminator)
		variants = append(variants, buildVariant(gate.Field, label, base, branch))
	}
	return variants
}

// buildVariant assembles one gate-value variant from the base fields and the
// matched branch (nil when the value maps to no declared branch).
func buildVariant(gateField, value string, base []field, branch *specmodel.Schema) Variant {
	combined := append([]field(nil), base...)
	provenance := ProvenanceDerived
	if branch != nil {
		provenance = ProvenanceStructural
		for _, f := range flatFields(branch) {
			combined = appendUniqueField(combined, f)
		}
	}
	index := indexFields(combined)

	minimal := requiredNames(combined)
	minimal = appendUnique(minimal, gateField) // the variant pins the gate
	maximal := fieldNames(combined)

	return Variant{
		GateField:  gateField,
		GateValue:  value,
		Provenance: provenance,
		Minimal:    skeleton(minimal, index),
		Maximal:    skeleton(maximal, index),
	}
}

// gatherBranches collects a schema's oneOf and anyOf branches, folding through
// allOf so a branch declared beside a composition is still found. Branches are
// returned unresolved, so a $ref branch keeps its target name for discriminator
// matching.
func gatherBranches(s *specmodel.Schema) []*specmodel.Schema {
	var out []*specmodel.Schema
	var walk func(r *specmodel.Schema, depth int)
	walk = func(r *specmodel.Schema, depth int) {
		if r == nil || depth > maxDepth {
			return
		}
		res := r.Resolved()
		out = append(out, res.OneOf...)
		out = append(out, res.AnyOf...)
		for _, b := range res.AllOf {
			walk(b, depth+1)
		}
	}
	walk(s, 0)
	return out
}

// matchBranch finds the composition branch a gate value selects: by explicit
// discriminator mapping first (the branch whose reference name the mapping
// names), then by the branch that constrains the gate field to the value
// through its own enum. Nil when no branch matches.
func matchBranch(gateField, value string, branches []*specmodel.Schema, discriminator *specmodel.Discriminator) *specmodel.Schema {
	if discriminator != nil && discriminator.PropertyName == gateField {
		if target, ok := discriminator.Mapping[value]; ok {
			for _, b := range branches {
				if b.Ref == target || b.Resolved().Name == target {
					return b
				}
			}
		}
	}
	for _, b := range branches {
		if branchAdmitsValue(b, gateField, value) {
			return b
		}
	}
	return nil
}

// branchAdmitsValue reports whether a branch constrains the gate field to the
// value: the branch declares the gate property with an enum whose members
// include the value.
func branchAdmitsValue(branch *specmodel.Schema, gateField, value string) bool {
	for _, f := range flatFields(branch) {
		if f.name != gateField {
			continue
		}
		for _, v := range stringifyValues(f.schema.Resolved().Enum) {
			if v == value {
				return true
			}
		}
	}
	return false
}

// deriveClaims composes every candidate conditional edge: the structural
// ones (variants, dependentRequired, dependentSchemas) and the prose ones
// (mined from descriptions), sorted and de-duplicated.
func deriveClaims(createBody *specmodel.Schema, variants []Variant) []Claim {
	var claims []Claim
	claims = append(claims, variantClaims(variants)...)
	claims = append(claims, dependentClaims(createBody)...)
	claims = append(claims, descriptionClaims(createBody)...)

	sortClaims(claims)
	return dedupeClaims(claims)
}

// variantClaims emits one variant edge per structural (branch-backed)
// variant: the fields the branch adds beyond the baseline are valid only under
// that gate value.
func variantClaims(variants []Variant) []Claim {
	if len(variants) == 0 {
		return nil
	}
	baseline := map[string]bool{}
	for _, f := range variants[0].Maximal.Fields {
		baseline[f] = true
	}
	var out []Claim
	for _, v := range variants[1:] {
		if v.Provenance != ProvenanceStructural {
			continue
		}
		var only []string
		for _, f := range v.Maximal.Fields {
			if !baseline[f] && f != v.GateField {
				only = append(only, f)
			}
		}
		if len(only) == 0 {
			continue
		}
		sort.Strings(only)
		out = append(out, Claim{
			Kind:       ClaimValidConfiguration,
			Subjects:   only,
			GateField:  v.GateField,
			GateValue:  v.GateValue,
			Provenance: ProvenanceStructural,
			Check: Probe{
				Step: stepCreateMaximal, GateField: v.GateField, GateValue: v.GateValue,
				ExpectedAnswer: "conditional",
			},
		})
	}
	return out
}

// dependentClaims emits the co-requirement edges JSON-Schema declares:
// dependentRequired (present X ⇒ required Y…) and dependentSchemas whose
// subschema adds a required set. Both become requiresField claims with
// structural provenance.
func dependentClaims(createBody *specmodel.Schema) []Claim {
	r := createBody.Resolved()
	var out []Claim
	for _, dr := range r.DependentRequired {
		subjects := appendUnique(append([]string(nil), dr.Requires...), dr.Property)
		sort.Strings(subjects)
		out = append(out, dependsOnClaim(subjects, dr.Property))
	}
	for _, ds := range r.DependentSchemas {
		request := ds.Schema.Resolved().Required
		if len(request) == 0 {
			continue
		}
		subjects := appendUnique(append([]string(nil), request...), ds.Property)
		sort.Strings(subjects)
		out = append(out, dependsOnClaim(subjects, ds.Property))
	}
	return out
}

// dependsOnClaim builds a structural requiresField edge whose check is
// a maximal create — send the whole set and see whether the API enforces the
// co-requirement.
func dependsOnClaim(subjects []string, trigger string) Claim {
	return Claim{
		Kind:       ClaimDependsOn,
		Subjects:   subjects,
		Provenance: ProvenanceStructural,
		Check:      Probe{Step: stepCreateMaximal, Field: trigger, ExpectedAnswer: "conditional"},
	}
}

// requiredNames lists the required fields of a field slice, sorted.
func requiredNames(fields []field) []string {
	var out []string
	for _, f := range fields {
		if f.required {
			out = append(out, f.name)
		}
	}
	sort.Strings(out)
	return out
}

// appendUnique appends s to names when absent.
func appendUnique(names []string, s string) []string {
	for _, n := range names {
		if n == s {
			return names
		}
	}
	return append(names, s)
}

// appendUniqueField appends f to fields when its name is absent.
func appendUniqueField(fields []field, f field) []field {
	for _, existing := range fields {
		if existing.name == f.name {
			return fields
		}
	}
	return append(fields, f)
}

// sortClaims orders claims so the compiled strategy is byte-stable: by
// kind, then subjects, then gate, then provenance.
func sortClaims(claims []Claim) {
	sort.SliceStable(claims, func(i, j int) bool {
		a, b := claims[i], claims[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if ja, jb := joinSubjects(a.Subjects), joinSubjects(b.Subjects); ja != jb {
			return ja < jb
		}
		if a.GateField != b.GateField {
			return a.GateField < b.GateField
		}
		if a.GateValue != b.GateValue {
			return a.GateValue < b.GateValue
		}
		return a.Provenance < b.Provenance
	})
}

// dedupeClaims drops claims identical in kind, subjects and gate,
// keeping the first — which, after the structural-before-prose append order and
// the stable sort, is the better-grounded one.
func dedupeClaims(claims []Claim) []Claim {
	seen := map[string]bool{}
	out := claims[:0]
	for _, h := range claims {
		key := string(h.Kind) + "\x00" + joinSubjects(h.Subjects) + "\x00" + h.GateField + "\x00" + h.GateValue
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
	}
	return out
}

// joinSubjects renders a subject list for sorting and keying.
func joinSubjects(subjects []string) string {
	s := append([]string(nil), subjects...)
	sort.Strings(s)
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
