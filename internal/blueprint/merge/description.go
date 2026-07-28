package merge

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/blueprint"
)

// Probed observations are written into an attribute's description inside a marker pair.
//
// The marker is what makes re-merging idempotent. Without it, merging twice appends the
// observations twice, and the blueprint drifts on every run -- which would make the drift gate
// fire on a no-op and, worse, would make a reviewer stop trusting it. This is the same problem
// internal/blueprint/derive has with append-based rules, and the same shape of answer.
//
// The snapshot id is inside the opening marker rather than outside it, so re-merging the *same*
// evidence is a no-op while merging *newer* evidence produces a visible one-line diff. A reader
// can see at a glance which recording a description came from.
const (
	markerPrefix = "<!-- probed:"
	markerOpen   = markerPrefix + "%s -->"
	markerClose  = "<!-- /probed -->"
)

// rewriteDescription replaces the probed block in an attribute's description.
//
// Everything outside the markers is left exactly as it was, because that text is curated: a
// human wrote it, and merge has no business editing prose it did not author.
func rewriteDescription(
	attr *blueprint.Attribute,
	observations []string,
	snapshotID string,
	res *blueprint.Resource,
	path string,
	result *Result,
) {
	kept := make([]string, 0, len(observations))
	for _, o := range observations {
		if strings.TrimSpace(o) != "" {
			kept = append(kept, o)
		}
	}
	if len(kept) == 0 {
		return
	}

	block := buildBlock(kept, snapshotID)

	existing, hadBlock := extractBlock(attr.MarkdownDescription)

	// Byte-identical means nothing to do, which is what makes re-merging the same evidence a
	// no-op rather than a diff.
	if hadBlock && existing == block {
		return
	}

	updated := replaceBlock(attr.MarkdownDescription, block)

	what := "markdownDescription (probed block added)"
	if hadBlock {
		what = "markdownDescription (probed block updated)"
	}

	result.Changes = append(result.Changes, Change{
		Resource: res.Key, JSONPath: path,
		What: what, To: fmt.Sprintf("%d observation(s)", len(kept)),
	})

	attr.MarkdownDescription = updated
}

// buildBlock renders the marked block.
func buildBlock(observations []string, snapshotID string) string {
	if snapshotID == "" {
		// A block with no snapshot id would be indistinguishable between recordings, so the
		// idempotence check would pass for evidence that had actually changed.
		snapshotID = "unknown"
	}

	var b strings.Builder

	fmt.Fprintf(&b, markerOpen+"\n", snapshotID)
	for i, o := range observations {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(o)
	}
	b.WriteString("\n" + markerClose)

	return b.String()
}

// extractBlock returns the existing probed block, if any.
func extractBlock(description string) (string, bool) {
	start := strings.Index(description, markerPrefix)
	if start < 0 {
		return "", false
	}

	end := strings.Index(description[start:], markerClose)
	if end < 0 {
		// An opening marker with no close is a hand-edit gone wrong. Treated as absent so the
		// next merge writes a well-formed block rather than nesting one inside the broken one.
		return "", false
	}

	return description[start : start+end+len(markerClose)], true
}

// replaceBlock swaps the probed block for a new one, or appends it.
func replaceBlock(description, block string) string {
	existing, ok := extractBlock(description)
	if ok {
		return strings.Replace(description, existing, block, 1)
	}

	trimmed := strings.TrimRight(description, " \n")
	if trimmed == "" {
		return block
	}

	// A blank line between the curated prose and the generated block, so the two read as
	// separate paragraphs in rendered documentation rather than running together.
	return trimmed + "\n\n" + block
}

// StripBlock removes the probed block from a description.
//
// Exported because it is what a caller needs to compare two blueprints ignoring probe
// annotations -- notably `emit`'s drift check, which should not report a difference caused only
// by newer evidence.
func StripBlock(description string) string {
	existing, ok := extractBlock(description)
	if !ok {
		return description
	}

	return strings.TrimRight(strings.Replace(description, existing, "", 1), " \n")
}
