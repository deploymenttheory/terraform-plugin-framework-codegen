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
const unsupportedFormatVersion = 1

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
type Unsupported struct {
	// Path addresses what was refused, in the spelling
	// terraform-plugin-codegen-spec uses for a validation finding:
	// `resource "tag" attribute "metadata"`. An entity that became nothing
	// has no kind to name, so it is addressed as `entity "tag"`.
	Path string `json:"path"`
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

// entityPath spells one entity's address. A kind the caller does not know
// is spelled "entity", which is the honest answer for something that fits
// no kind — that is why it was refused.
func entityPath(kind, key string) string {
	if kind == "" {
		kind = "entity"
	}
	return fmt.Sprintf("%s %q", kind, key)
}

// attributePath spells one attribute's address beneath its entity. Nested
// attributes carry their dotted path, so two attributes of one name at
// different depths address differently.
func attributePath(kind, key, attribute string) string {
	return fmt.Sprintf("%s attribute %q", entityPath(kind, key), attribute)
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
func RenderUnsupported(m *ir.Model, removals []sdkbind.Removal, dropped []sdkbind.Dropped, emissionRefusals []ir.Exclusion) (File, []Unsupported, error) {
	report := UnsupportedReport{FormatVersion: unsupportedFormatVersion}

	if m != nil {
		for _, exclusion := range m.Excluded {
			report.Unsupported = append(report.Unsupported, Unsupported{
				Path:   entityPath("", exclusion.Key),
				Stage:  StageDerivation,
				Reason: exclusion.Reason,
			})
		}
		report.Unsupported = append(report.Unsupported, refusedAttributes(m)...)
	}

	for _, removal := range removals {
		entry := Unsupported{Stage: StageBinding, Reason: removal.Reason}
		if removal.Attribute == "" {
			entry.Path = entityPath(removal.Kind, removal.Key)
		} else {
			entry.Path = attributePath(removal.Kind, removal.Key, removal.Attribute)
		}
		report.Unsupported = append(report.Unsupported, entry)
	}

	for _, drop := range dropped {
		report.Unsupported = append(report.Unsupported, Unsupported{
			Path:   entityPath(drop.Kind, drop.Key),
			Stage:  StageBinding,
			Reason: drop.Reason,
		})
	}

	for _, refusal := range emissionRefusals {
		report.Unsupported = append(report.Unsupported, Unsupported{
			Path:   entityPath("", refusal.Key),
			Stage:  StageEmission,
			Reason: refusal.Reason,
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

// sortUnsupported fixes the order so the report is a stable diff: by path,
// then stage, then reason. Nothing here may depend on map iteration or on
// the order entities happened to be walked in.
func sortUnsupported(entries []Unsupported) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Path != entries[j].Path {
			return entries[i].Path < entries[j].Path
		}
		if entries[i].Stage != entries[j].Stage {
			return entries[i].Stage < entries[j].Stage
		}
		return entries[i].Reason < entries[j].Reason
	})
}

// refusedAttributes walks every entity's tree for the attributes
// deriveType marked unsupported, at any depth.
func refusedAttributes(m *ir.Model) []Unsupported {
	var out []Unsupported

	walk := func(kind, key string, tree *ir.AttributeTree) {
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
						Path:   attributePath(kind, key, name),
						Stage:  StageDerivation,
						Reason: attribute.UnsupportedReason,
					})
					continue
				}
				descend(name, attribute.Nested)
			}
		}
		descend("", tree)
	}

	for i := range m.Resources {
		walk("resource", m.Resources[i].Names.Key, m.Resources[i].Schema)
	}
	for i := range m.Datasources {
		walk("datasource", m.Datasources[i].Names.Key, m.Datasources[i].Schema)
	}
	for i := range m.ListResources {
		walk("list_resource", m.ListResources[i].Names.Key, m.ListResources[i].Schema)
	}
	for i := range m.Actions {
		walk("action", m.Actions[i].Names.Key, m.Actions[i].RequestSchema)
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
