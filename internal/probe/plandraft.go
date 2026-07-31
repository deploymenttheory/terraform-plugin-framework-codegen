package probe

import (
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// CurateMe is the placeholder a drafted plan carries wherever no value could be
// derived. It is deliberately conspicuous: a draft is a worksheet, and every one of
// these is a line item the curator has to replace before promoting the file.
const CurateMe = "CURATE_ME"

// DraftPlan scaffolds a probe plan for one subject.
//
// The output is a starting point for curation, never something to record against as
// it stands -- which is why the caller writes it under a .draft.json name no loader
// resolves, and promotion is a git-visible rename. What the scaffold can know, it
// fills: the fixture's required fields, enum values split into a fixture value and
// candidates (which is exactly what the immutability and enum protocols consume).
// What it cannot know it marks CURATE_ME, so the worksheet lists its own gaps.
//
// It deliberately does not guess influencers. A gate nobody declares is a branch
// nobody measures, and a guessed gate would look declared while measuring nothing a
// human chose. The empty list in the draft is the prompt to think about it.
func DraftPlan(subj Subject) Plan {
	plan := Plan{
		Fixtures:           []Fixture{{Name: "minimal", Body: map[string]any{}}},
		Candidates:         map[string][]any{},
		DefaultInfluencers: []string{},
	}

	body := plan.Fixtures[0].Body

	for _, f := range subj.Fields {
		if !f.Writable || f.JSONPath == "" {
			continue
		}

		// Enum candidates come from every writable field that documents values, not
		// just fixture members: the enum protocol checks the documented set against
		// the live API, and the immutability protocol needs the second value.
		if top, nested := splitPath(f.JSONPath); nested == "" && len(f.AllowedValues) > 1 {
			alternatives := make([]any, 0, len(f.AllowedValues)-1)
			for _, v := range f.AllowedValues[1:] {
				alternatives = append(alternatives, v)
			}
			plan.Candidates[top] = alternatives
		}

		// The fixture is the minimal valid body: required top-level fields only.
		// Optional fields are curation's call -- a fixture is one branch of the
		// API's dispatch, and which optionals select the branch is judgement.
		if f.ComputedOptionalRequired != blueprint.Required {
			continue
		}
		if top, nested := splitPath(f.JSONPath); nested != "" {
			// A nested member rides its parent's placeholder below.
			_ = top
			continue
		}

		body[f.JSONPath] = draftValue(f)
	}

	// The name field is where the run's prefix is stamped; the stamping replaces the
	// value, but Create refuses a body that omits the key entirely.
	if subj.NameField != "" {
		body[subj.NameField] = CurateMe
	}

	return plan
}

// draftValue fills one required field as well as a specification can.
func draftValue(f Field) any {
	if len(f.AllowedValues) > 0 {
		return f.AllowedValues[0]
	}

	switch f.Kind {
	case blueprint.KindBool:
		return true
	case blueprint.KindInt32, blueprint.KindInt64:
		return 1
	case blueprint.KindFloat32, blueprint.KindFloat64, blueprint.KindNumber:
		return 1
	case blueprint.KindList, blueprint.KindSet:
		return []any{CurateMe}
	case blueprint.KindMap:
		return map[string]any{CurateMe: CurateMe}
	case blueprint.KindSingleNested:
		return map[string]any{CurateMe: CurateMe}
	case blueprint.KindListNested, blueprint.KindSetNested:
		// The classic shape this exists for: agents = [{agentId: <live id>}]. The
		// members are usually identifiers of objects that must already exist, which
		// no scaffold can invent -- so the placeholder is the whole value.
		return []any{map[string]any{CurateMe: CurateMe}}
	default:
		return CurateMe
	}
}

// splitPath cuts a JSON path into its top-level key and the rest.
func splitPath(path string) (top, nested string) {
	for i := range len(path) {
		if path[i] == '.' {
			return path[:i], path[i+1:]
		}
	}
	return path, ""
}
