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
//
// There are two channels of evidence, and each owns its own block. Live evidence carries a
// snapshot id and a newer recording replaces the previous live block -- the id changes, the
// block does not accumulate. Static evidence (the SDK-type facts) is not tied to any recording,
// so it writes under the fixed id "static" and replaces only its own block. Before the split,
// merging the static facts overwrote every live observation an attribute had, and re-merging
// the snapshot then read as drift -- two sources fighting over one block.
const (
	markerPrefix = "<!-- probed:"
	markerOpen   = markerPrefix + "%s -->"
	markerClose  = "<!-- /probed -->"

	// StaticSnapshotID is the marker id the static-facts channel writes under.
	StaticSnapshotID = "static"
)

// rewriteDescription replaces one channel's probed block in an attribute's description.
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
	static := snapshotID == StaticSnapshotID

	existing, hadBlock := channelBlock(attr.MarkdownDescription, static)

	// Byte-identical means nothing to do, which is what makes re-merging the same evidence a
	// no-op rather than a diff.
	if hadBlock && existing == block {
		return
	}

	var updated string
	if hadBlock {
		updated = strings.Replace(attr.MarkdownDescription, existing, block, 1)
	} else {
		updated = appendBlock(attr.MarkdownDescription, block)
	}

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

// blocks returns every probed block in a description, in order.
func blocks(description string) []string {
	var out []string

	rest := description
	for {
		start := strings.Index(rest, markerPrefix)
		if start < 0 {
			return out
		}
		end := strings.Index(rest[start:], markerClose)
		if end < 0 {
			// An opening marker with no close is a hand-edit gone wrong. Treated as absent
			// so the next merge writes a well-formed block rather than nesting one inside
			// the broken one.
			return out
		}

		out = append(out, rest[start:start+end+len(markerClose)])
		rest = rest[start+end+len(markerClose):]
	}
}

// blockID reads the id out of a block's opening marker.
func blockID(block string) string {
	id := strings.TrimPrefix(block, markerPrefix)
	if i := strings.Index(id, " -->"); i >= 0 {
		return id[:i]
	}
	return ""
}

// channelBlock returns the description's block for one channel: the static block, or
// the live one, whatever recording it came from.
func channelBlock(description string, static bool) (string, bool) {
	for _, b := range blocks(description) {
		if (blockID(b) == StaticSnapshotID) == static {
			return b, true
		}
	}
	return "", false
}

// appendBlock adds a channel's first block after whatever the description already holds.
func appendBlock(description, block string) string {
	trimmed := strings.TrimRight(description, " \n")
	if trimmed == "" {
		return block
	}

	// A blank line between the curated prose and the generated block, so the two read as
	// separate paragraphs in rendered documentation rather than running together.
	return trimmed + "\n\n" + block
}

// StripBlock removes every probed block from a description.
//
// Exported because it is what a caller needs to compare two blueprints ignoring probe
// annotations -- notably `emit`'s drift check, which should not report a difference caused only
// by newer evidence.
func StripBlock(description string) string {
	out := description
	for _, b := range blocks(out) {
		out = strings.Replace(out, b, "", 1)
	}

	return strings.TrimRight(strings.ReplaceAll(out, "\n\n\n", "\n\n"), " \n")
}
