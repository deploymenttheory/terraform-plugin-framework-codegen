package interop

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Severity is how much a downgrade costs.
//
// Three levels rather than two, because the three have genuinely different
// consequences and collapsing them makes the report unreadable: a seventeen
// attribute resource produces dozens of notes, and a reader needs to see at a
// glance which ones mean something was actually lost.
type Severity string

const (
	// SeverityInfo means the value has no counterpart in the official format but
	// nothing is at risk: it is provenance only, or it is mechanically
	// re-derivable, or the content crossed verbatim into a field with a different
	// declared contract. An import can reconstruct it.
	SeverityInfo Severity = "info"

	// SeverityLossy means the value crossed in a coarsened form that a consumer
	// cannot distinguish from the original -- int32 written as int64, a block
	// written as a nested attribute. The document is still usable; it just says
	// something slightly weaker than the blueprint did.
	SeverityLossy Severity = "lossy"

	// SeverityDropped means the value is carried nowhere at all. This is the level
	// that says the exported document cannot be turned back into something
	// emittable, and it is the expected level for every CRUD binding.
	SeverityDropped Severity = "dropped"
)

// rank orders severities for reporting, most serious first.
func (s Severity) rank() int {
	switch s {
	case SeverityDropped:
		return 0
	case SeverityLossy:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}

// Note is one thing the official format could not carry.
//
// Path addresses a node in the blueprint using the same dotted form
// blueprint.Validate uses, because interop losses happen at depths a
// resource-plus-field pair cannot reach: an unrepresentable default inside a
// nested object needs to be named, not described.
type Note struct {
	Severity Severity `json:"severity"`
	Path     string   `json:"path"`
	Message  string   `json:"message"`
}

func (n Note) String() string {
	return fmt.Sprintf("%-7s %s: %s", n.Severity, n.Path, n.Message)
}

// Report is everything one conversion could not carry, plus what it did carry.
//
// The counts matter as much as the notes. A run that converted nothing and a run
// that converted everything both produce an empty error, and without the counts
// they are indistinguishable in a log.
type Report struct {
	Notes []Note `json:"notes,omitempty"`

	Resources   int `json:"resources"`
	DataSources int `json:"dataSources"`
	Attributes  int `json:"attributes"`

	// Omitted counts nodes excluded because Drop was set. They are not losses --
	// the blueprint asked for them to be left out -- but a silent difference in
	// node count between the two documents would be alarming to anyone comparing
	// them, so it is stated.
	Omitted int `json:"omitted,omitempty"`
}

// add records a note. Callers pass a format string because most messages need to
// name the offending value, and building the string at the call site keeps the
// message next to the condition that produced it.
func (r *Report) add(sev Severity, path, format string, args ...any) {
	r.Notes = append(
		r.Notes,
		Note{Severity: sev, Path: path, Message: fmt.Sprintf(format, args...)},
	)
}

// Count returns how many notes carry the given severity.
func (r Report) Count(sev Severity) int {
	n := 0
	for _, note := range r.Notes {
		if note.Severity == sev {
			n++
		}
	}
	return n
}

// Lost reports whether anything crossed in a weakened form or not at all.
func (r Report) Lost() bool {
	return r.Count(SeverityLossy)+r.Count(SeverityDropped) > 0
}

// Err returns a non-nil error only under strict.
//
// Export succeeding despite a hundred dropped fields is the point of the command,
// not a failure of it: the official format cannot carry a CRUD binding, and a tool
// that refused to export because of that would never export anything. Strict mode
// exists for the caller who wants to assert that a *particular* blueprint is
// expressible, which is a different question.
func (r Report) Err(strict bool) error {
	if !strict || !r.Lost() {
		return nil
	}

	return fmt.Errorf("%w: %d value(s) coarsened, %d dropped",
		ErrDowngraded, r.Count(SeverityLossy), r.Count(SeverityDropped))
}

// Sorted returns the notes most-serious-first, and by path within a severity.
//
// Sorting by path rather than by discovery order means the report of a given
// blueprint is byte-stable, which is what lets a CI job diff it.
func (r Report) Sorted() []Note {
	out := make([]Note, len(r.Notes))
	copy(out, r.Notes)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity.rank() < out[j].Severity.rank()
		}
		return out[i].Path < out[j].Path
	})

	return out
}

// Summary is the one-line count, always printed so that a silent run is
// distinguishable from a run that had nothing to say.
func (r Report) Summary() string {
	var b strings.Builder

	fmt.Fprintf(&b, "%d resource(s), %d data source(s), %d attribute(s)",
		r.Resources, r.DataSources, r.Attributes)
	if r.Omitted > 0 {
		fmt.Fprintf(&b, ", %d omitted by drop", r.Omitted)
	}
	fmt.Fprintf(&b, ". %d note(s): %d dropped, %d lossy, %d info",
		len(r.Notes), r.Count(SeverityDropped), r.Count(SeverityLossy), r.Count(SeverityInfo))

	return b.String()
}

// MarshalJSON writes the report with its notes already sorted, so that -report
// output is byte-stable for a given blueprint.
func (r Report) MarshalJSON() ([]byte, error) {
	// A local alias avoids recursing into this method.
	type report Report

	out := report(r)
	out.Notes = r.Sorted()

	data, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encoding the downgrade report: %w", err)
	}

	return data, nil
}

// Loss taxonomy.
//
// Every blueprint field with no counterpart in the official format is listed here
// with the severity it reports at and the sentence the note carries. It is data
// rather than judgement scattered through the mapping for two reasons: a reviewer
// can read the whole vocabulary of possible losses in one screen, and
// TestUnit_Interop_Severities can assert the list is total. A new IR field with no
// entry here fails that test, which is what stops the report going stale as the IR
// grows.
//
// Keys are the dotted path suffix the note is addressed at, relative to whatever
// node is being converted.
var taxonomy = map[string]Note{
	// Provider-level. The official provider block carries a name and a
	// configuration schema, and nothing else.
	"provider.schema": {
		SeverityInfo,
		"provider.schema",
		"the blueprint declares no provider configuration attributes, so the exported provider block carries a name only",
	},
	"provider.sdk": {
		SeverityDropped, "provider.sdk",
		"the SDK module path, client type and dialect have no counterpart",
	},
	"provider.goModule": {
		SeverityInfo, "provider.goModule",
		"the generated module path has no counterpart; interop import takes it as a flag",
	},
	"provider.typePrefix": {
		SeverityInfo, "provider.typePrefix",
		"the type prefix has no counterpart; it is recoverable from the resource type names",
	},
	"provider.conventions": {
		SeverityInfo, "provider.conventions",
		"emitter layout conventions have no counterpart",
	},
	"provider.support": {
		SeverityInfo, "provider.support",
		"the support package imports have no counterpart",
	},
	"source": {
		SeverityInfo, "source",
		"specification provenance has no counterpart",
	},

	// Resource-level.
	"binding": {
		SeverityDropped,
		"binding",
		"SDK call wiring has no counterpart, so a document exported from this blueprint cannot be emitted from",
	},
	"policy.updateStyle": {
		SeverityDropped, "policy.updateStyle",
		"whether an omitted field is preserved or cleared on update has no counterpart",
	},
	"policy.readBack": {
		SeverityDropped, "policy.readBack",
		"the re-read after write has no counterpart",
	},
	"policy.delete": {
		SeverityDropped, "policy.delete",
		"delete semantics have no counterpart",
	},
	"import": {
		SeverityDropped, "import",
		"the import style has no counterpart",
	},
	"timeouts": {
		SeverityDropped, "timeouts",
		"per-operation timeouts have no counterpart",
	},
	"naming": {
		SeverityInfo,
		"naming",
		"Go package, type and model names have no counterpart; interop import re-derives them from the resource name",
	},
	"docRefUrl": {
		SeverityInfo, "docRefUrl",
		"the documentation reference URL has no counterpart",
	},
	"identity": {
		SeverityDropped, "identity",
		"the resource identity schema has no counterpart: the specification models a schema " +
			"and nothing about how Terraform addresses an object independently of it",
	},
	"actionKind": {
		SeverityDropped, "actions",
		"the specification has no representation for an action at all -- it models resources, " +
			"data sources and a provider -- so every action in this blueprint is absent from " +
			"the exported document",
	},
	"ephemeralKind": {
		SeverityDropped, "ephemerals",
		"the specification has no representation for an ephemeral resource -- it models " +
			"resources, data sources and a provider -- so every ephemeral in this blueprint is " +
			"absent from the exported document",
	},
	"list": {
		SeverityDropped, "list",
		"the list facet has no counterpart: the specification models resources, data sources " +
			"and a provider, and has no representation for a list resource at all",
	},
	"configValidators": {
		SeverityDropped, "configValidators",
		"cross-attribute rules have no counterpart: the specification's validators are " +
			"per-attribute custom code, with no path-typed expression and nowhere on a resource " +
			"to hang a rule relating two attributes",
	},
	"hooks": {
		SeverityDropped, "hooks",
		"the hand-written hook points have no counterpart: they are scaffolded files a " +
			"practitioner owns, and a document exported here and generated elsewhere would " +
			"silently lose them",
	},

	// Attribute-level. goField and wire are aggregated per resource -- see
	// attrLosses -- so their wording reads as a count rather than as one field.
	"goField": {
		SeverityInfo, "goField",
		"model struct field names have no counterpart; internal/naming re-derives them",
	},
	"wire": {
		SeverityDropped,
		"wire",
		"expand and flatten bindings have no counterpart, so the exported attributes carry no mapping to the SDK",
	},
	"behaviour": {
		SeverityDropped, "behaviour",
		"observed API behaviour has no counterpart",
	},
	"markdownDescription": {
		SeverityInfo,
		"markdownDescription",
		"the format has no attribute-level markdown description, so descriptions were written to `description` unchanged",
	},
	// The import-side counterpart of markdownDescription. Every document this
	// toolkit exports sets only the plain description, so importing one of our own
	// exports promotes every attribute's text back to markdown.
	"importedDescription": {
		SeverityInfo,
		"description",
		"the document set description but not markdown_description, so the text is now treated as markdown",
	},

	// Type coarsening. Keyed by the kind that had to be widened.
	"type.kind/int32": {
		SeverityLossy, "type.kind",
		"int32 has no counterpart and was widened to int64",
	},
	"type.kind/float32": {
		SeverityLossy, "type.kind",
		"float32 has no counterpart and was widened to float64",
	},

	// NestedAttributeObject object metadata. The SDK type does cross, via
	// associated_external_type; the generated identifiers do not.
	"type.nested.names": {
		SeverityInfo,
		"type.nested",
		"generated model, attr.Type and helper names have no counterpart; they are re-derived on import",
	},
	"type.nested.sdkType": {
		SeverityInfo, "type.nested.sdkType",
		"the SDK struct crosses as associated_external_type and is recovered on import",
	},
}

// note looks a loss up in the taxonomy and records it against a concrete path.
//
// The taxonomy entry supplies the severity and the wording; the caller supplies
// where it happened. Looking the key up rather than accepting a free-text message
// is what keeps the taxonomy total: a loss reported without an entry panics in
// tests rather than inventing a severity at the call site.
func (r *Report) note(key, at string) {
	entry, ok := taxonomy[key]
	if !ok {
		// Unreachable via the exported API: every call site uses a constant key,
		// and TestUnit_Interop_Severities asserts the set of keys is exactly the
		// set of taxonomy entries. Reported rather than panicking so that a
		// mistake here degrades the report instead of killing the run.
		r.add(SeverityDropped, at, "unclassified loss (taxonomy key %q is missing)", key)
		return
	}

	path := at
	if path == "" {
		path = entry.Path
	}

	r.Notes = append(r.Notes, Note{Severity: entry.Severity, Path: path, Message: entry.Message})
}

// noteCount records an aggregated loss, stating how many nodes it covers.
//
// The count is what makes an aggregate note as informative as the individual ones
// it replaces: "24 affected" tells the reader the loss is universal, where a bare
// sentence would leave them wondering whether it hit one attribute or all of them.
func (r *Report) noteCount(key, at string, n int) {
	entry, ok := taxonomy[key]
	if !ok {
		r.add(SeverityDropped, at, "unclassified loss (taxonomy key %q is missing)", key)
		return
	}

	r.Notes = append(r.Notes, Note{
		Severity: entry.Severity,
		Path:     at,
		Message:  fmt.Sprintf("%s (%d affected)", entry.Message, n),
	})
}
