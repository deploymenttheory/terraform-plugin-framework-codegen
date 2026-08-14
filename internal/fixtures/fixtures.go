// Package fixtures derives every fixture value one entity's generated
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
// carry the NamePrefix so anything a test creates against a live API is
// recognisable as toolkit debris and can be cleaned up by name.
package fixtures

import (
	"encoding/base64"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
)

// NamePrefix marks every synthesised name-bearing string. The audit's
// cleanup contract matches live objects by this prefix, so fixture values
// sent to a real API identify themselves as deletable.
const NamePrefix = "tfpfgen-test-"

// Fixture is the single derivation of one entity's fixture values.
type Fixture struct {
	// Entries holds one derived value per supported attribute, in
	// attribute-tree order.
	Entries []Entry
	// Omissions lists the attributes no value could be derived for, with
	// the reason each was refused.
	Omissions []Omission

	// requiredForVariant gates the top-level entries when the entity is
	// multi-variant — a discriminator attribute whose value selects which
	// other attributes are valid. It names the top-level attributes the chosen
	// variant requires, forced into the minimal renderings even when the
	// document marks them optional, because the minimal create must satisfy the
	// variant's value-conditional requirement. Empty for a single-variant
	// entity, leaving selection driven by presence alone.
	//
	// The complementary gate — attributes owned by another variant — is
	// applied by pruning them from Entries in Derive, so every consumer of the
	// fixture (renderings, generated checks, config values) sees the one
	// variant-consistent attribute set and none can reintroduce a field the
	// generated value-conditional validator would reject.
	requiredForVariant map[string]bool
}

// Entry is one attribute's derived fixture value.
type Entry struct {
	// Name is the terraform attribute name; Wire the property the API
	// speaks.
	Name string
	Wire string
	// Kind and ElementType mirror the attribute's kinds.
	Kind        ir.AttributeType
	ElementType ir.AttributeType
	// ComputedOptionalRequired decides which renderings carry the value: configurations
	// carry writable attributes, responses carry readable ones.
	ComputedOptionalRequired ir.ComputedOptionalRequired
	// Scalar is the value of a scalar attribute — string, bool, int64 or
	// float64 — or the single element's value for a list of scalars. Nil
	// for object kinds.
	Scalar any
	// Nested are the field values of an object attribute, or of the one
	// element a list of objects carries.
	Nested []Entry
}

// Omission is one attribute that has no fixture value, and why.
type Omission struct {
	// Name is the dotted attribute path from the entity root.
	Name   string
	Reason string
}

// Form selects which attributes a rendering carries.
type Form int

// Renderings.
const (
	// ConfigMinimal is the smallest applying configuration: required
	// attributes only.
	ConfigMinimal Form = iota
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
// names the entity in omission reasons. A nil tree derives an empty fixture.
func Derive(tree *ir.AttributeTree) Fixture {
	var s Fixture
	if tree == nil {
		return s
	}
	s.Entries, s.Omissions = deriveTree(tree, nil)
	s.applyVariant(tree)
	return s
}

// applyVariant reads the tree's conditional-edge facts and, when the entity is
// multi-variant, pins the discriminator to one value and records which
// top-level attributes that value excludes and which it forces. A
// single-variant entity — no discriminator — is left untouched.
func (s *Fixture) applyVariant(tree *ir.AttributeTree) {
	m := buildVariantModel(tree)
	if m.discriminator == "" {
		return
	}

	value := m.choose(discriminatorOrder(tree, m.discriminator))
	if value == "" {
		return
	}

	// Drop the attributes another variant owns: a top-level entry whose owning
	// variant is not the chosen one is invalid under this discriminator value,
	// so no rendering or check may carry it.
	kept := s.Entries[:0]
	for _, e := range s.Entries {
		if owner, gated := m.ownerOf[e.Name]; gated && owner != value {
			continue
		}
		kept = append(kept, e)
	}
	s.Entries = kept

	s.requiredForVariant = map[string]bool{}
	for field := range m.requiredFor[value] {
		s.requiredForVariant[field] = true
	}

	// The discriminator's own value must name the chosen variant, or the gated
	// config would set fields the discriminator's value forbids.
	if disc := s.entry(m.discriminator); disc != nil {
		disc.Scalar = value
	}
}

// PinNumeric replaces the value of each named top-level string entry with
// digits.
//
// For a caller that knows something about an attribute the document does not
// say: a path parameter the generated SDK parses as an integer is still a
// string in the schema, and a value that is not digits is refused by that
// parse rather than by any assertion.
func (s *Fixture) PinNumeric(names map[string]bool) {
	for name := range names {
		if e := s.entry(name); e != nil {
			if _, isString := e.Scalar.(string); isString {
				e.Scalar = "7"
			}
		}
	}
}

// entry returns a pointer to the top-level entry of the given terraform name,
// or nil. The pointer aliases the slice element so a caller can pin its value.
func (s *Fixture) entry(name string) *Entry {
	for i := range s.Entries {
		if s.Entries[i].Name == name {
			return &s.Entries[i]
		}
	}
	return nil
}

// variantModel is the discriminator picture assembled from a tree's
// conditional-edge facts: which attribute discriminates, which variant value
// owns each gated attribute, and which attributes each value requires. All
// names are terraform spelling, matching Entry.Name.
type variantModel struct {
	discriminator string
	ownerOf       map[string]string          // gated field -> the variant value that owns it
	requiredFor   map[string]map[string]bool // variant value -> its required fields
	values        map[string]bool            // every variant value seen
}

// buildVariantModel gathers the discriminator, per-variant ownership and
// per-variant requirements from the three tree-level edge kinds a discriminator
// produces. Only a single discriminating property is modelled; edges naming a
// different property are ignored, so a fixture is never gated by two
// discriminators at once.
func buildVariantModel(tree *ir.AttributeTree) variantModel {
	m := variantModel{
		ownerOf:     map[string]string{},
		requiredFor: map[string]map[string]bool{},
		values:      map[string]bool{},
	}
	own := func(field, value string) {
		if _, seen := m.ownerOf[field]; !seen {
			m.ownerOf[field] = value
		}
	}

	for _, vc := range tree.ValidConfigurations {
		if m.discriminator == "" {
			m.discriminator = vc.Discriminator
		}
		if vc.Discriminator != m.discriminator {
			continue
		}
		for _, variant := range vc.Variants {
			m.values[variant.Value] = true
			for _, f := range variant.Valid {
				own(f, variant.Value)
			}
		}
	}
	for _, cv := range tree.ConditionalValidities {
		if m.discriminator == "" {
			m.discriminator = cv.Property
		}
		if cv.Property != m.discriminator {
			continue
		}
		m.values[cv.Equals] = true
		for _, f := range cv.Valid {
			own(f, cv.Equals)
		}
	}
	for _, cr := range tree.ConditionalRequirements {
		if m.discriminator == "" {
			m.discriminator = cr.Property
		}
		if cr.Property != m.discriminator {
			continue
		}
		m.values[cr.Equals] = true
		if m.requiredFor[cr.Equals] == nil {
			m.requiredFor[cr.Equals] = map[string]bool{}
		}
		for _, f := range cr.Required {
			own(f, cr.Equals)
			m.requiredFor[cr.Equals][f] = true
		}
	}
	return m
}

// discriminatorOrder returns the discriminator attribute's declared enum
// values, in document order, so the variant choice can honour them. Empty when
// the discriminator carries no enum.
func discriminatorOrder(tree *ir.AttributeTree, discriminator string) []string {
	for _, a := range tree.Attributes {
		if a.Name == discriminator {
			return a.OneOf
		}
	}
	return nil
}

// choose picks the variant value the fixture builds: the first of the
// discriminator's declared enum values that names a known variant, so the
// choice honours the document's order and is a value the enum admits; failing
// that, the lexically first variant value seen.
func (m variantModel) choose(order []string) string {
	for _, candidate := range order {
		if m.known(candidate) {
			return candidate
		}
	}
	best := ""
	for value := range m.values {
		if m.known(value) && (best == "" || value < best) {
			best = value
		}
	}
	return best
}

// known reports whether a variant value owns any attribute or requires any —
// a value with no gated fields would produce a fixture indistinguishable from
// an ungated one, so it is not worth pinning.
func (m variantModel) known(value string) bool {
	if len(m.requiredFor[value]) > 0 {
		return true
	}
	for _, owner := range m.ownerOf {
		if owner == value {
			return true
		}
	}
	return false
}

// deriveTree walks one attribute tree, carrying the dotted path for omission
// reporting and the dashed path for string synthesis.
func deriveTree(tree *ir.AttributeTree, path []string) ([]Entry, []Omission) {
	var values []Entry
	var skips []Omission
	for _, a := range tree.Attributes {
		at := append(append([]string{}, path...), a.Name)
		if a.Unsupported {
			reason := a.UnsupportedReason
			if reason == "" {
				reason = "the derivation refused this attribute's shape"
			}
			skips = append(skips, Omission{Name: strings.Join(at, "."), Reason: reason})
			continue
		}
		v := Entry{
			Name:                     a.Name,
			Wire:                     a.WireName,
			Kind:                     a.Kind,
			ElementType:              a.ElementType,
			ComputedOptionalRequired: a.ComputedOptionalRequired,
		}
		switch {
		case a.Nested != nil:
			nested, nestedSkips := deriveTree(a.Nested, at)
			v.Nested = nested
			skips = append(skips, nestedSkips...)
		case a.Kind == ir.TypeList:
			v.Scalar = scalarFor(a.ElementType, a, at)
		default:
			v.Scalar = scalarFor(a.Kind, a, at)
		}
		values = append(values, v)
	}
	unifyByWire(values)
	return values, skips
}

// unifyByWire gives every entry that speaks one wire property the same
// value.
//
// Two attributes can share a wire name: the synthesised id names whatever
// property the item path keys on, and the document usually declares that
// property too. One JSON object then carries one key, so the mapping reads
// one value into both attributes — and a fixture claiming two would assert a
// value the state cannot hold. First declaration wins, which is the id.
func unifyByWire(values []Entry) {
	seen := map[string]any{}
	for i := range values {
		if values[i].Wire == "" || values[i].Nested != nil {
			continue
		}
		if first, taken := seen[values[i].Wire]; taken {
			values[i].Scalar = first
			continue
		}
		seen[values[i].Wire] = values[i].Scalar
	}
}

// scalarFor synthesises one scalar value: enum-driven when the document
// declares values, format-driven when it declares what the string holds,
// type-driven otherwise. A plain string carries the test prefix and the
// attribute path so no two attributes share a value.
func scalarFor(kind ir.AttributeType, a ir.Attribute, path []string) any {
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
		name := NamePrefix + strings.ReplaceAll(strings.Join(path, "-"), "_", "-")
		if formatted, ok := formatValue(a.Format, name); ok {
			return formatted
		}
		return name
	}
}

// formatValue synthesises a string the document says is more than a string,
// and reports whether the format is one it knows.
//
// A generated SDK parses these on the way in: a timestamp becomes time.Time
// and an identifier becomes uuid.UUID, so a value of the wrong shape is
// refused before any assertion in a generated test runs, and the failure
// names the parse rather than the field.
//
// A format with room for the prefix keeps it, so a name a test leaves behind
// on a live API is still recognisable. A timestamp and a uuid have no such
// room; they are fixed instead, which keeps them deterministic.
func formatValue(format, name string) (string, bool) {
	switch format {
	case "date-time":
		return "2026-01-02T03:04:05Z", true
	case "date":
		return "2026-01-02", true
	case "time":
		return "03:04:05Z", true
	case "uuid":
		return "00000000-0000-4000-8000-000000000000", true
	case "byte", "base64":
		return base64.StdEncoding.EncodeToString([]byte(name)), true
	case "email", "idn-email":
		return name + "@example.invalid", true
	case "hostname", "idn-hostname":
		return name + ".example.invalid", true
	case "uri", "url", "uri-reference", "iri":
		return "https://example.invalid/" + name, true
	case "ipv4":
		// TEST-NET-1 and the documentation prefix: reserved for exactly this,
		// so a value that escapes into a request reaches nothing real.
		return "192.0.2.1", true
	case "ipv6":
		return "2001:db8::1", true
	}
	return "", false
}

// wanted reports whether a value's attribute travels in the given
// rendering: configurations carry what a practitioner writes, responses
// carry what the server sends back.
func (v Entry) wanted(a Form) bool {
	switch a {
	case ConfigMinimal:
		return v.ComputedOptionalRequired == ir.Required
	case ConfigMaximal:
		return v.ComputedOptionalRequired != ir.Computed
	case ResponseMinimal:
		return v.ComputedOptionalRequired != ir.Optional
	default: // ResponseMaximal
		return true
	}
}

// selected filters one level of values for a form. Nested levels are
// filtered against the same form by the renderers as they recurse.
func selected(values []Entry, a Form) []Entry {
	out := make([]Entry, 0, len(values))
	for _, v := range values {
		if v.wanted(a) {
			out = append(out, v)
		}
	}
	return out
}

// topLevel filters the entity's top-level entries for a form, applying the
// variant gate's minimal-forcing half: an attribute the chosen variant requires
// is forced into the minimal forms even when the document marks it optional, so
// the minimal create satisfies the variant's value-conditional requirement. The
// complementary half — dropping other-variant attributes — is already done in
// Derive by pruning Entries. For a single-variant entity requiredForVariant is
// empty and this reduces to selected. Nested levels are never gated — only the
// discriminator's own siblings are conditional — so the renderers keep using
// selected as they recurse.
func (s Fixture) topLevel(a Form) []Entry {
	out := make([]Entry, 0, len(s.Entries))
	for _, v := range s.Entries {
		include := v.wanted(a)
		if (a == ConfigMinimal || a == ResponseMinimal) && s.requiredForVariant[v.Name] {
			include = true
		}
		if include {
			out = append(out, v)
		}
	}
	return out
}
