package openapi

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ExclusionsFileName is the conventional sidecar beside an openapi directory's
// snapshots, e.g. openapi/thousandeyes/draft-exclusions.json.
const ExclusionsFileName = "draft-exclusions.json"

// Exclusion is one curated statement that a path family must not be drafted.
//
// The judgement lives in a committed sidecar rather than in inference, because
// the document does not say which of its readable surfaces are configuration
// and which are telemetry -- a time-windowed read is a strong hint, a plain
// one proves nothing -- and a heuristic that guessed would guess silently.
// Every entry names its reason, and the draft run repeats it as a named skip.
type Exclusion struct {
	// PathPrefix excludes every family whose collection path starts with it.
	PathPrefix string `json:"pathPrefix,omitempty"`
	// Key excludes one family exactly.
	Key string `json:"key,omitempty"`
	// Reason is required: an unexplained exclusion is indistinguishable from a
	// mistake.
	Reason string `json:"reason"`
}

// Exclusions is the parsed sidecar.
type Exclusions struct {
	Exclusions []Exclusion `json:"exclusions"`
}

// LoadExclusions reads a sidecar; a missing file is simply no exclusions.
func LoadExclusions(path string) (Exclusions, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
	if os.IsNotExist(err) {
		return Exclusions{}, nil
	}
	if err != nil {
		return Exclusions{}, err
	}

	var e Exclusions
	if err := json.Unmarshal(data, &e); err != nil {
		return Exclusions{}, fmt.Errorf("%s is not a usable exclusions sidecar: %w", path, err)
	}
	for i, x := range e.Exclusions {
		if x.Reason == "" {
			return Exclusions{}, fmt.Errorf("%s: exclusions[%d] has no reason; an unexplained exclusion is indistinguishable from a mistake", path, i)
		}
		if x.PathPrefix == "" && x.Key == "" {
			return Exclusions{}, fmt.Errorf("%s: exclusions[%d] names neither a pathPrefix nor a key", path, i)
		}
	}
	return e, nil
}

// Match reports whether a candidate is excluded, and why.
func (e Exclusions) Match(c Candidate) (string, bool) {
	for _, x := range e.Exclusions {
		if x.Key != "" && x.Key == c.Key {
			return x.Reason, true
		}
		if x.PathPrefix != "" && strings.HasPrefix(c.CollectionPath, x.PathPrefix) {
			return x.Reason, true
		}
	}
	return "", false
}
