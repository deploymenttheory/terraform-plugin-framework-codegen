package emit

import (
	"bytes"
	"fmt"
	"html/template"
	"sort"
	"strings"

	ir "github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/intermediate_representation"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/templates"
)

// EmittanceReportName answers the report's path at the provider repo root.
// The provider is in the name because a reader who has one open alongside
// another wants to know which is which from the tab.
func EmittanceReportName(provider string) string {
	return "generated_provider_" + provider + ".html"
}

// Emittance is everything the report says, assembled before anything is
// rendered. Every value the template consumes is finished here: the
// template branches on presence and never counts, sorts or decides.
//
// It is a pure function of one run's records, so the report regenerates
// byte-identically and is byte-compared by `provider verify` like every
// other generated file. It carries no timestamp and no count of anything
// outside the run for the same reason.
type Emittance struct {
	Provider string
	Document EmittanceDocument
	SDK      EmittanceSDK
	Produced EmittanceCounts
	// Rewrites is what prenormalising changed before the backend read the
	// document, in the order the rewrites are applied.
	Rewrites []EmittanceRewrite
	// Tags are the document's own groups, each carrying the entities it
	// placed there. A reader follows one entity's journey; the tag is how
	// they find it.
	Tags []EmittanceTag
	// Unplaced are exclusions that name nothing the run derived, which
	// cannot be filed under a group.
	Unplaced []EmittanceCause
	// Workflow is the run, in the order it happened, so a reader has the
	// shape of it before anything refers to a step.
	Workflow []Step
}

// EmittanceDocument identifies what the run was a fact about.
type EmittanceDocument struct {
	Source, SHA256, Version, OpenAPI string
	Corrections                      int
}

// EmittanceSDK identifies the SDK the provider was resolved against.
type EmittanceSDK struct {
	Backend, Version string
	Reconciled       int
}

// EmittanceCounts is what the run produced and what it refused.
//
// It carries no file count and no toolchain verdict. The report is itself
// one of the files, so counting them here would count itself; and the
// toolchain gate runs after the tree is written, so a verdict recorded here
// would be a guess at its own future.
type EmittanceCounts struct {
	Resources, Datasources, ListResources, Actions int
	Refused, Kept                                  int
}

// EmittanceRewrite is one prenormalise rewrite as the report shows it.
type EmittanceRewrite struct {
	Name  string
	Count int
	Sites []EmittanceSite
}

// EmittanceSite is one place a rewrite changed something. It mirrors the
// SDK generator's own site rather than importing it, so this package
// describes a document it was given without depending on what was done to
// it afterwards.
type EmittanceSite struct {
	Where string
	Count int
}

// EmittanceTag is one of the document's groups.
type EmittanceTag struct {
	Name     string
	Entities []EmittanceEntity
	// Refused and Lost count what the entities under it cost, so a reader
	// scanning the tags sees where to look without opening any of them.
	Refused, Lost int
}

// EmittanceEntity is one thing's journey from a path to what shipped.
type EmittanceEntity struct {
	Key, Service, CollectionPath string
	// Produced names the kinds it became, empty for an entity that became
	// nothing at all.
	Produced []string
	// Causes are its losses, one entry per cause rather than one per
	// casualty: a model carrying none of its fields is one fact.
	Causes []EmittanceCause
	// Whole is true where nothing was refused.
	Whole bool
}

// EmittanceCause is one fact and everything it cost, in the words the
// reader needs. Title, Means and Fix are the prose; Code, Stage, Subject and
// Reason are the machinery, shown only where a reader asks for it.
type EmittanceCause struct {
	Explanation
	// Step is the numbered point in the workflow this happened at, and
	// Where the plain phrase for it. Zero where it was the operator's own
	// configuration rather than a step.
	Step  int
	Where string

	Stage, Code, Subject, Reason string
	// Entity is set only where the cause is filed outside a tag.
	Entity string
	// Attributes are the casualties, sorted. Empty where the whole entity
	// went, which the reason then accounts for.
	Attributes []string
}

// RenderEmittance groups one run's refusals under the entities they belong
// to and the tags the document places those entities in, then renders the
// report. The caller supplies what identifies the run; the grouping is done
// here, because the template may not.
func RenderEmittance(e Emittance, m *ir.Model, refusals []Unsupported) (File, error) {
	e.Tags, e.Unplaced = groupByTag(m, refusals)
	e.Workflow = Workflow

	body, err := renderEmittance(e)
	if err != nil {
		return File{}, fmt.Errorf("rendering %s: %w", EmittanceReportName(e.Provider), err)
	}
	name := EmittanceReportName(e.Provider)
	return File{Path: name, Content: body, Source: emittanceTemplate}, nil
}

// emittanceTemplate is the report's one template, and the source recorded
// against it in the manifest.
const emittanceTemplate = "emittance/report.html.tmpl"

// entityFacts is what the run knows about one entity before its refusals
// are added: where it came from and what it became.
type entityFacts struct {
	service, tag, collectionPath string
	produced                     []string
}

// groupByTag files every refusal under its entity and every entity under
// its tag. A refusal naming an entity the run never derived is unplaced:
// inventing a tag for it would file it somewhere a reader cannot check.
func groupByTag(m *ir.Model, refusals []Unsupported) ([]EmittanceTag, []EmittanceCause) {
	facts := map[string]*entityFacts{}
	note := func(names ir.Names, kind, collectionPath string) {
		f, ok := facts[names.Key]
		if !ok {
			f = &entityFacts{service: names.Service, tag: names.Tag, collectionPath: collectionPath}
			facts[names.Key] = f
		}
		if kind != "" {
			f.produced = append(f.produced, kind)
		}
	}
	if m != nil {
		for i := range m.Resources {
			note(m.Resources[i].Names, bindingKindResource, "")
		}
		for i := range m.Datasources {
			note(m.Datasources[i].Names, bindingKindDatasource, "")
		}
		for i := range m.ListResources {
			note(m.ListResources[i].Names, bindingKindListResource, "")
		}
		for i := range m.Actions {
			note(m.Actions[i].Names, bindingKindAction, "")
		}
		for _, excluded := range [][]ir.UnsupportedEntity{m.ExcludedByConfiguration, m.ExcludedByClassification} {
			for _, x := range excluded {
				note(ir.Names{Key: x.Key, Service: x.Service, Tag: x.Tag}, "", x.CollectionPath)
			}
		}
	}

	// One entry per (entity, cause), gathering the attributes it cost.
	type causeKey struct{ entity, stage, code, subject string }
	gathered := map[causeKey]*EmittanceCause{}
	var order []causeKey
	var unplaced []EmittanceCause

	for _, r := range refusals {
		if _, known := facts[r.Entity]; !known {
			unplaced = append(unplaced, explained(EmittanceCause{
				Stage: r.Stage, Code: codeOf(r), Subject: subjectOf(r),
				Reason: r.Reason, Entity: r.Entity,
			}))
			continue
		}
		k := causeKey{r.Entity, r.Stage, codeOf(r), subjectOf(r)}
		c, ok := gathered[k]
		if !ok {
			settled := explained(EmittanceCause{Stage: r.Stage, Code: k.code, Subject: k.subject, Reason: r.Reason})
			c = &settled
			gathered[k] = c
			order = append(order, k)
		}
		if r.Attribute != "" {
			c.Attributes = append(c.Attributes, r.Attribute)
		}
	}

	byEntity := map[string][]EmittanceCause{}
	for _, k := range order {
		c := gathered[k]
		sort.Strings(c.Attributes)
		byEntity[k.entity] = append(byEntity[k.entity], *c)
	}

	byTag := map[string][]EmittanceEntity{}
	for key, f := range facts {
		causes := byEntity[key]
		sort.Slice(causes, func(i, j int) bool {
			a, z := causes[i], causes[j]
			switch {
			case a.Stage != z.Stage:
				return a.Stage < z.Stage
			case a.Code != z.Code:
				return a.Code < z.Code
			default:
				return a.Subject < z.Subject
			}
		})
		sort.Strings(f.produced)
		tag := f.tag
		if tag == "" {
			tag = untagged
		}
		byTag[tag] = append(byTag[tag], EmittanceEntity{
			Key: key, Service: f.service, CollectionPath: f.collectionPath,
			Produced: f.produced, Causes: causes, Whole: len(causes) == 0,
		})
	}

	tags := make([]EmittanceTag, 0, len(byTag))
	for name, entities := range byTag {
		sort.Slice(entities, func(i, j int) bool { return entities[i].Key < entities[j].Key })
		t := EmittanceTag{Name: name, Entities: entities}
		for _, e := range entities {
			if len(e.Produced) == 0 {
				t.Refused++
			}
			for _, c := range e.Causes {
				t.Lost += len(c.Attributes)
			}
		}
		tags = append(tags, t)
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].Name < tags[j].Name })
	sort.Slice(unplaced, func(i, j int) bool {
		if unplaced[i].Entity != unplaced[j].Entity {
			return unplaced[i].Entity < unplaced[j].Entity
		}
		return unplaced[i].Reason < unplaced[j].Reason
	})
	return tags, unplaced
}

// explained fills in what the reader is told, and the step it happened at.
// A cause with no explanation keeps its code as the title, so a reader meets
// something rather than nothing, and the missing prose is visible in the
// page instead of silently absent.
func explained(c EmittanceCause) EmittanceCause {
	c.Step, c.Where = stepOf(c.Stage)
	if e, ok := Explain(c.Code); ok {
		c.Explanation = e
		return c
	}
	c.Explanation = Explanation{Title: c.Code}
	return c
}

// untagged names the group for an entity the document places in none. It is
// spelled so it cannot collide with a tag a document declares.
const untagged = "(no tag)"

func codeOf(r Unsupported) string {
	if r.Cause == nil {
		return ""
	}
	return r.Cause.Code
}

func subjectOf(r Unsupported) string {
	if r.Cause == nil {
		return ""
	}
	return r.Cause.Subject
}

// renderEmittance executes the report template. The functions it is given
// are formatting only: anything that decides something has already been
// decided.
func renderEmittance(e Emittance) ([]byte, error) {
	t, err := template.New("report.html.tmpl").Funcs(template.FuncMap{
		"join": func(parts []string) string { return strings.Join(parts, ", ") },
	}).ParseFS(templates.Emittance, emittanceTemplate)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := t.Execute(&out, e); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
