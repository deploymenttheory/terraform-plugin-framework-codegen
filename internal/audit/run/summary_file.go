// The committed record of how far a run got with each entity.

package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SummaryFile is the committed file naming, one per run, written beside the
// observations and the request bodies the same run wrote — audit/summary.json
// wherever --out leaves the observations at its default.
const SummaryFile = "summary.json"

// WriteSummary commits sum to path, creating the directory that holds it.
//
// An observation says what a run learned about one property, and a request
// body says what a create looked like when it worked. Neither says why an
// entity produced nothing: that sentence — the step that stopped, and the
// API's own words for why — reaches only the operator's terminal, so an
// entity blocked by a refusal nobody was watching for leaves no trace to act
// on afterwards. Committed rather than kept, so a newly blocked entity is a
// line in a generation pull request instead of a line in a log nobody reads.
//
// The encoding matches the observations beside it: sorted map keys, no HTML
// escaping, two-space indent.
func WriteSummary(path string, sum Summary) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(sum); err != nil {
		return fmt.Errorf("encoding the run summary: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
