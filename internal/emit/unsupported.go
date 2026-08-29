package emit

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/sdkbind"
)

// UnsupportedName is the report's path at the provider repo root.
const UnsupportedName = "unsupported.json"

// unsupportedFormatVersion is the report's own schema version, bumped when
// the shape changes so a reader can tell.
const unsupportedFormatVersion = 2

// The stages that can refuse something. Each names one place in the
// pipeline, so a reader can tell a document the toolkit could not model
// from an SDK that could not carry it.
const (
	// StageDerivation is the intermediate representation: an entity that
	// fits no kind, an entity dropped by services.exclude, or an attribute
	// whose shape the derivation will not guess at.
	StageDerivation = "derivation"
	// StageBinding is sdkbind: the generated SDK does not carry the call or
	// the field the entity needs.
	StageBinding = "binding"
	// StageEmission is this package: an entity whose shape the emitters
	// cannot serve, such as a path parameter no attribute answers.
	StageEmission = "emission"
)

// Unsupported is one thing generation refused, and why.
//
// The subject is fields rather than one rendered sentence. A reader
// grouping refusals — by entity, by kind, by where they belong — would
// otherwise have to parse prose to do it, and prose that must be parsed is
// a format with no version.
type Unsupported struct {
	// Kind is what was refused: "resource", "datasource", "list_resource"
	// or "action". Empty for an entity refused before it became any of
	// them, which is the honest answer rather than a default.
	Kind string `json:"kind,omitempty"`
	// Entity is the entity key.
	Entity string `json:"entity"`
	// Attribute is the dotted attribute path, empty when the whole entity
	// was refused. Nested attributes carry their path, so two attributes of
	// one name at different depths address differently.
	Attribute string `json:"attribute,omitempty"`
	// Service and Tag are where the refusal belongs: the service area
	// derived from the entity's path, and the group the document places it
	// in. Carried so a refusal can be grouped the same way a generated
	// entity is, including one refused before it became anything.
	Service string `json:"service,omitempty"`
	Tag     string `json:"tag,omitempty"`
	// Stage is which part of the pipeline refused it.
	Stage string `json:"stage"`
	// Reason is the account that stage gave, verbatim.
	Reason string `json:"reason"`
}

// UnsupportedReport is the whole committed report.
type UnsupportedReport struct {
	FormatVersion int           `json:"format_version"`
	Unsupported   []Unsupported `json:"unsupported"`
}

// located fills a refusal's service and tag from the derived entity of the
// same key. sdkbind answers with a kind and a key and has no use for where
// the entity belongs, so the location is read back here rather than carried
// through it.
func located(m *ir.Model, u Unsupported) Unsupported {
	if m == nil {
		return u
	}
	if names, ok := namesByKey(m)[u.Entity]; ok {
		u.Service, u.Tag = names.Service, names.Tag
	}
	return u
}

// namesByKey indexes every derived entity's naming block by key.
func namesByKey(m *ir.Model) map[string]ir.Names {
	out := map[string]ir.Names{}
	for i := range m.Resources {
		out[m.Resources[i].Names.Key] = m.Resources[i].Names
	}
	for i := range m.Datasources {
		out[m.Datasources[i].Names.Key] = m.Datasources[i].Names
	}
	for i := range m.ListResources {
		out[m.ListResources[i].Names.Key] = m.ListResources[i].Names
	}
	for i := range m.Actions {
		out[m.Actions[i].Names.Key] = m.Actions[i].Names
	}
	return out
}

// RenderUnsupported builds the refusal report from every stage that can
// refuse something: the derivation's entity exclusions and attribute
// refusals, the binder's removals and dropped entities, and emission's own
// refusals.
//
// The attribute half is the new information. An attribute deriveType
// refuses renders nowhere — it is kept on the tree only so the fixture
// derivation can record an omission — so until now nothing told a
// practitioner why an entity has no `metadata` field, and nothing told a
// reviewer that a spec change had quietly cost them fifteen of them.
//
// The report is derived content like any other: manifest-covered, and
// byte-compared by `provider verify`. That is the whole mechanism. A spec
// change that breaks fifteen entities becomes fifteen added lines in a
// generation pull request instead of fifteen deleted directories among
// thousands of regenerated ones.
func RenderUnsupported(m *ir.Model, removals []sdkbind.Removal, dropped []sdkbind.Dropped, emissionRefusals []ir.UnsupportedEntity, keptUnbound map[string]bool) (File, []Unsupported, error) {
	report := UnsupportedReport{FormatVersion: unsupportedFormatVersion}

	if m != nil {
		for _, exclusion := range m.Excluded {
			report.Unsupported = append(report.Unsupported, Unsupported{
				Kind:    exclusion.Kind,
				Entity:  exclusion.Key,
				Service: exclusion.Service,
				Tag:     exclusion.Tag,
				Stage:   StageDerivation,
				Reason:  exclusion.Reason,
			})
		}
		report.Unsupported = append(report.Unsupported, refusedAttributes(m)...)
	}

	for _, removal := range removals {
		// A removal the emitter kept anyway costs the operator nothing: the
		// binding goes because no model carries the field, and the
		// attribute reaches the schema regardless. Reporting it as a
		// refusal overstates what the provider will not carry.
		if keptUnbound[keptUnboundKey(removal.Kind, removal.Key, removal.Attribute)] {
			continue
		}
		report.Unsupported = append(report.Unsupported, located(m, Unsupported{
			Kind:      removal.Kind,
			Entity:    removal.Key,
			Attribute: removal.Attribute,
			Stage:     StageBinding,
			Reason:    removal.Reason,
		}))
	}

	for _, drop := range dropped {
		report.Unsupported = append(report.Unsupported, located(m, Unsupported{
			Kind:   drop.Kind,
			Entity: drop.Key,
			Stage:  StageBinding,
			Reason: drop.Reason,
		}))
	}

	for _, refusal := range emissionRefusals {
		report.Unsupported = append(report.Unsupported, Unsupported{
			Kind:    refusal.Kind,
			Entity:  refusal.Key,
			Service: refusal.Service,
			Tag:     refusal.Tag,
			Stage:   StageEmission,
			Reason:  refusal.Reason,
		})
	}

	sortUnsupported(report.Unsupported)

	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return File{}, nil, fmt.Errorf("rendering %s: %w", UnsupportedName, err)
	}
	file := File{Path: UnsupportedName, Content: append(body, '\n'), Source: UnsupportedName}
	return file, report.Unsupported, nil
}

// sortUnsupported fixes the order so the report is a stable diff: by
// entity, then kind, then attribute, then stage, then reason. Nothing here
// may depend on map iteration or on the order entities happened to be
// walked in.
//
// Entity leads so one entity's refusals sit together whatever kind each was
// refused as, which is the grouping a reader asking "what happened to this
// entity" wants.
func sortUnsupported(entries []Unsupported) {
	sort.Slice(entries, func(i, j int) bool {
		a, z := entries[i], entries[j]
		switch {
		case a.Entity != z.Entity:
			return a.Entity < z.Entity
		case a.Kind != z.Kind:
			return a.Kind < z.Kind
		case a.Attribute != z.Attribute:
			return a.Attribute < z.Attribute
		case a.Stage != z.Stage:
			return a.Stage < z.Stage
		default:
			return a.Reason < z.Reason
		}
	})
}

// refusedAttributes walks every entity's tree for the attributes
// deriveType marked unsupported, at any depth.
func refusedAttributes(m *ir.Model) []Unsupported {
	var out []Unsupported

	walk := func(kind string, names ir.Names, tree *ir.AttributeTree) {
		key, service, tag := names.Key, names.Service, names.Tag
		var descend func(prefix string, tree *ir.AttributeTree)
		descend = func(prefix string, tree *ir.AttributeTree) {
			if tree == nil {
				return
			}
			for _, attribute := range tree.Attributes {
				name := attribute.Name
				if prefix != "" {
					name = prefix + "." + attribute.Name
				}
				if attribute.Unsupported {
					out = append(out, Unsupported{
						Kind:      kind,
						Entity:    key,
						Attribute: name,
						Service:   service,
						Tag:       tag,
						Stage:     StageDerivation,
						Reason:    attribute.UnsupportedReason,
					})
					continue
				}
				descend(name, attribute.Nested)
			}
		}
		descend("", tree)
	}

	for i := range m.Resources {
		walk(bindingKindResource, m.Resources[i].Names, m.Resources[i].Schema)
	}
	for i := range m.Datasources {
		walk(bindingKindDatasource, m.Datasources[i].Names, m.Datasources[i].Schema)
	}
	for i := range m.ListResources {
		walk(bindingKindListResource, m.ListResources[i].Names, m.ListResources[i].Schema)
	}
	for i := range m.Actions {
		walk(bindingKindAction, m.Actions[i].Names, m.Actions[i].RequestSchema)
	}
	return out
}

// UnsupportedSummary is the one line `provider generate` prints about the
// report: how much was refused, and where to read the detail. Silence when
// nothing was refused, because a report of nothing is not news.
func UnsupportedSummary(report []Unsupported) string {
	if len(report) == 0 {
		return ""
	}
	byStage := map[string]int{}
	for _, entry := range report {
		byStage[entry.Stage]++
	}
	stages := make([]string, 0, len(byStage))
	for stage, count := range byStage {
		stages = append(stages, fmt.Sprintf("%d in %s", count, stage))
	}
	sort.Strings(stages)
	noun := "refusals"
	if len(report) == 1 {
		noun = "refusal"
	}
	return fmt.Sprintf("%s records %d %s (%s)", UnsupportedName, len(report), noun, strings.Join(stages, ", "))
}
