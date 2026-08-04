package kiota

import (
	"encoding/json"
	"fmt"
)

// Dependency is one Go module a kiota-generated client needs at runtime.
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Type    string `json:"dependencyType"`
}

// Info reports the Go modules the installed kiota's output depends on, from
// `kiota info -l go --json`. Asked of the binary rather than hard-coded,
// because the dependency set belongs to the kiota version that generated the
// tree, not to this tool.
func Info() ([]Dependency, error) {
	out, err := run("", "info", "--language", "go", "--json")
	if err != nil {
		return nil, err
	}

	var payload struct {
		Dependencies []Dependency `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return nil, fmt.Errorf("`kiota info -l go --json` did not answer with usable JSON: %w", err)
	}
	if len(payload.Dependencies) == 0 {
		return nil, fmt.Errorf("`kiota info -l go --json` listed no dependencies, which cannot be right")
	}
	return payload.Dependencies, nil
}
