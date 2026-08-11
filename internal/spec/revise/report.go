// report.go writes the narratable half of a --propose-only run: the
// proposals grouped by (entity, kind), each finding carrying the evidence it
// was read from and the prose explain.go supplies.
//
// It exists because the correction files themselves cannot narrate. A
// correction is a justification, some RFC 6902 operations and a pointer at an
// observation — enough to apply, nowhere near enough to review. The job that
// opens pull requests was reading exactly those files and could therefore
// only quote them, which is how a reviewer ended up with one pull request per
// attribute, each saying nothing about what was actually asked of the API or
// what came back. The report is additional: the correction files are written
// exactly as before.
package revise

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/audit/observe"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen-1/internal/spec/correction"
)

// ReportName is the file the report is written to, inside the proposed
// directory beside the proposals it describes. It is not a correction — it
// carries no operations and is never applied — so the strict correction
// loader, the gate's pending-decision scan and Propose's own clear-and-rewrite
// all pass it by on the `.correction.json` suffix they match.
const ReportName = "report.json"

// MaxReportExcerpts caps how many excerpts one finding carries into the
// report. A pull request body has a size limit and a reviewer has patience:
// the first request/response pair is the proof, the rest is corroboration
// that already sits in the committed observation file.
const MaxReportExcerpts = 2

// Report is one --propose-only run's proposals, grouped for review.
type Report struct {
	Groups []Group `json:"groups"`
}

// Group is every proposal of one kind against one entity — one decision, one
// pull request. Grouping by (entity, kind) is what turns "25 pull requests
// each recording one field's default" into one reviewable claim about an
// entity, rejectable as a unit.
type Group struct {
	Entity string       `json:"entity"`
	Kind   observe.Kind `json:"kind"`
	// KindTitle and KindPlural are the kind's human title, counted and
	// uncounted. Both are carried because the pull-request job may open a
	// pull request for only part of a group — the rest already decided — and
	// must be able to recount without knowing English.
	KindTitle  string `json:"kindTitle"`
	KindPlural string `json:"kindPlural"`
	// Summary counts the findings in the kind's own words — "3
	// server-assigned defaults" — and is what the pull request title says
	// after the entity.
	Summary string `json:"summary"`
	// Branch is the stable branch this group's pull request lives on. Stable
	// across runs, so a re-run updates the pending decision instead of
	// opening a second one beside it.
	Branch string `json:"branch"`
	// ObservationIDs are every observation the group speaks for, sorted. A
	// close rejects all of them, so all of them need a marker.
	ObservationIDs []string `json:"observationIDs"`
	// Files are the group's correction files, base names inside the proposed
	// directory, in the order they should be committed.
	Files []string `json:"files"`
	// Merging and Closing say what each decision does. They are properties of
	// the kind, so the group states them once rather than per finding.
	Merging  string    `json:"merging"`
	Closing  string    `json:"closing"`
	Findings []Finding `json:"findings"`
}

// Finding is one observation's worth of the group: what was asked of the API,
// what the document said to expect, what came back, and what it means.
type Finding struct {
	ObservationID string `json:"observationID"`
	// Attribute is empty for an entity-level finding.
	Attribute string `json:"attribute,omitempty"`
	// Value is the finding itself, as the observation recorded it.
	Value any `json:"value,omitempty"`
	// ValueSpelling is Value rendered for a human — a scalar in a code span,
	// a list as code spans, a record as a sentence.
	ValueSpelling string `json:"valueSpelling,omitempty"`
	// Stale marks a finding compiled from an observation taken against a
	// superseded document.
	Stale bool `json:"stale,omitempty"`
	// File is the correction's base name inside the proposed directory.
	File string `json:"file"`
	// Evidence is the correction's pointer at the committed observation.
	Evidence string `json:"evidence"`
	// Justification is the correction's own one-line prose, kept so the
	// report is a superset of the file it describes.
	Justification string `json:"justification"`
	// Expected, Observed and Means are the rendered explanation.
	Expected string `json:"expected"`
	Observed string `json:"observed"`
	Means    string `json:"means"`
	// Operations are the RFC 6902 operations, so a reviewer who wants the
	// mechanism does not have to open the file.
	Operations []correction.Operation `json:"operations"`
	// Excerpts is the redacted proof: at most MaxReportExcerpts of the
	// request/response fragments the finding was read from.
	Excerpts []observe.Excerpt `json:"excerpts,omitempty"`
}

// proposal pairs one written proposal with the observation it compiles, which
// is what the correction file alone cannot carry.
type proposal struct {
	obs   observe.Observation
	corr  correction.Correction
	file  string
	stale bool
}

// buildReport groups proposals by (entity, kind) and renders each one's
// prose. Deterministic throughout: groups sort by entity then kind, findings
// by attribute then observation ID, and every list inside is sorted.
func buildReport(props []proposal) Report {
	byKey := map[[2]string][]proposal{}
	for _, p := range props {
		byKey[[2]string{p.obs.Entity, string(p.obs.Kind)}] = append(byKey[[2]string{p.obs.Entity, string(p.obs.Kind)}], p)
	}

	keys := make([][2]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})

	var rep Report
	for _, k := range keys {
		members := byKey[k]
		sort.SliceStable(members, func(i, j int) bool {
			if members[i].obs.Attribute != members[j].obs.Attribute {
				return members[i].obs.Attribute < members[j].obs.Attribute
			}
			return members[i].obs.ID < members[j].obs.ID
		})
		rep.Groups = append(rep.Groups, buildGroup(k[0], observe.Kind(k[1]), members))
	}
	return rep
}

// buildGroup renders one (entity, kind) group.
func buildGroup(entity string, kind observe.Kind, members []proposal) Group {
	// A kind with no explanation cannot happen — the bidirectional test
	// forbids it — but a zero Explanation renders to empty prose rather than
	// to a panic, because a missing sentence must never cost a reviewer the
	// evidence beneath it.
	ex, _ := Explain(kind)

	g := Group{
		Entity:     entity,
		Kind:       kind,
		KindTitle:  ex.Title,
		KindPlural: ex.Plural,
		Summary:    ex.Summary(len(members)),
		Branch:    GroupBranch(entity, kind),
		Merging:   ex.Merging,
		Closing:   ex.Closing,
	}
	for _, m := range members {
		spelling := describeValue(m.obs)
		rendered := ex.Render(entity, m.obs.Attribute, spelling)
		g.ObservationIDs = append(g.ObservationIDs, m.obs.ID)
		g.Files = append(g.Files, m.file)
		g.Findings = append(g.Findings, Finding{
			ObservationID: m.obs.ID,
			Attribute:     m.obs.Attribute,
			Value:         m.obs.Value,
			ValueSpelling: spelling,
			Stale:         m.stale,
			File:          m.file,
			Evidence:      m.corr.Evidence,
			Justification: m.corr.Justification,
			Expected:      rendered.Expected,
			Observed:      rendered.Observed,
			Means:         rendered.Means,
			Operations:    m.corr.Operations,
			Excerpts:      reportExcerpts(m.obs.Excerpts),
		})
	}
	// The group's Merging and Closing carry {entity} but no {attribute}: they
	// speak for the whole group, so an attribute would be a lie for all but
	// one member.
	whole := ex.Render(entity, "", "")
	g.Merging, g.Closing = whole.Merging, whole.Closing
	sort.Strings(g.ObservationIDs)
	sort.Strings(g.Files)
	return g
}

// reportExcerpts caps and re-redacts the evidence a finding carries.
//
// The excerpts were redacted at capture, before they were ever written down.
// Passing them through Redact again is not distrust of that pass but of the
// file: audit/observations/ is committed, hand-editable, and a pull request
// body is far more public than a repository file. Redact with no secrets
// still strips every Authorization-style key's value wholesale and still
// fails closed on a fragment it cannot parse, so a credential pasted into an
// observation by hand cannot ride into a pull request.
func reportExcerpts(in []observe.Excerpt) []observe.Excerpt {
	if len(in) == 0 {
		return nil
	}
	n := min(len(in), MaxReportExcerpts)
	out := make([]observe.Excerpt, 0, n)
	for _, e := range in[:n] {
		out = append(out, observe.Redact(e, nil))
	}
	return out
}

// GroupBranch is the branch one group's pull request lives on:
// tfpfgen/correction-<entity>-<kind>, the kind spelled in kebab case. It
// replaces the per-observation branch scheme, which could only ever carry one
// finding.
//
// Stability is the whole point — the same group in a later run must land on
// the same branch, so a re-run updates a pending decision instead of opening
// a second pull request for it. Nothing reads the group back out of the
// branch name: the observation IDs a rejection needs travel in the pull
// request body, which survives sanitisation and does not have to be
// reversible.
func GroupBranch(entity string, kind observe.Kind) string {
	return "tfpfgen/correction-" + sanitiseRef(entity) + "-" + sanitiseRef(kebab(string(kind)))
}

// kebab spells a camelCase kind with hyphens: readAfterWrite reads back as
// read-after-write. An acronym stays whole — requiredByAPI is
// required-by-api, not required-by-a-p-i — by breaking only where a word
// actually starts: after a lower-case letter or digit, or at the last capital
// of a run that a lower-case letter follows.
func kebab(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		upper := c >= 'A' && c <= 'Z'
		if upper && i > 0 {
			prev := r[i-1]
			prevLower := (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')
			nextLower := i+1 < len(r) && r[i+1] >= 'a' && r[i+1] <= 'z'
			if prevLower || nextLower {
				b.WriteByte('-')
			}
		}
		if upper {
			c = c - 'A' + 'a'
		}
		b.WriteRune(c)
	}
	return b.String()
}

// sanitiseRef reduces a name to what a git ref may safely hold: lower-case
// letters, digits, underscores and hyphens, with no run of hyphens and none
// at either end.
func sanitiseRef(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}

// describeValue spells one observation's value for a human. The compound
// kinds get a sentence rather than their JSON, because "{"accepted":["a"],
// "rejected":["b"]}" in the middle of a paragraph is exactly the jargon this
// file exists to remove.
func describeValue(o observe.Observation) string {
	switch o.Kind {
	case observe.KindValues:
		return describeValues(o.Value)
	case observe.KindListResponseShape:
		return describeListShape(o.Value)
	case observe.KindValidConfiguration, observe.KindMutuallyExclusive:
		if list := stringList(o.Value); len(list) > 0 {
			sort.Strings(list)
			return codeList(list)
		}
	case observe.KindWritable, observe.KindImmutable, observe.KindRequiredByAPI,
		observe.KindRequiredWhen, observe.KindDerivedDefault, observe.KindIgnoredOnUpdate,
		observe.KindServerForced, observe.KindVolatile, observe.KindDeleteNotFoundOK,
		observe.KindValidWhen:
		// The boolean kinds say everything in their name; a bare "true" in
		// the prose would read as noise.
		return ""
	}
	if o.Value == nil {
		return ""
	}
	return "`" + literalSpelling(o.Value) + "`"
}

// describeValues renders a values record as the sentence the reviewer needs.
func describeValues(v any) string {
	var vals observe.Values
	raw, err := json.Marshal(v)
	if err == nil {
		err = json.Unmarshal(raw, &vals)
	}
	if err != nil {
		return ""
	}
	var parts []string
	if len(vals.Rejected) > 0 {
		rejected := append([]string(nil), vals.Rejected...)
		sort.Strings(rejected)
		parts = append(parts, "it refused the documented "+plural(len(rejected), "value")+" "+codeList(rejected))
	}
	if len(vals.Accepted) > 0 {
		accepted := append([]string(nil), vals.Accepted...)
		sort.Strings(accepted)
		parts = append(parts, "it took "+codeList(accepted))
	}
	if vals.Closed != nil && !*vals.Closed {
		parts = append(parts, "and it took a value outside the documented set entirely")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

// describeListShape renders a list-response-shape record as a sentence.
func describeListShape(v any) string {
	var shape observe.ListResponseShape
	raw, err := json.Marshal(v)
	if err == nil {
		err = json.Unmarshal(raw, &shape)
	}
	if err != nil {
		return ""
	}
	pagination := shape.Pagination
	if pagination == "" {
		pagination = "none"
	}
	paged := "paginated by " + pagination
	if pagination == "none" {
		paged = "with no pagination"
	}
	if shape.Envelope == "wrapped" {
		return fmt.Sprintf("the items arrived wrapped under `%s`, %s", shape.Key, paged)
	}
	return "the items arrived as a bare array, " + paged
}

// codeList renders names as code spans in an English list.
func codeList(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, "`"+n+"`")
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " and " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// writeReport encodes the report deterministically beside the proposals: two-
// space indent, HTML escaping off, trailing newline — the same shape every
// other committed JSON in this toolkit has.
func writeReport(proposedDir string, rep Report) error {
	if err := os.MkdirAll(proposedDir, 0o750); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("encoding the proposal report: %w", err)
	}
	return os.WriteFile(filepath.Join(proposedDir, ReportName), buf.Bytes(), 0o600)
}

// removeReport clears the previous run's report, so a run that proposes
// nothing leaves no report claiming otherwise.
func removeReport(proposedDir string) error {
	err := os.Remove(filepath.Join(proposedDir, ReportName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
