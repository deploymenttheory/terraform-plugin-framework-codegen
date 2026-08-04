package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/cassette"
	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/probe"
)

// A sandbox profile says where a mutating run is allowed to reach, and on what evidence.
//
// # JSON, not YAML
//
// probe.Profile carries json tags, and YAML would mean a new direct dependency for a file
// nobody edits often. The README said .yaml; the code says otherwise, and the code wins.
//
// # Not committed
//
// A profile carries assertions.accountGroupId, and report.go already rules a tenant identifier
// out of any committed artefact: a tenant name turns a vulnerability into a targeted one. So the
// default location is under .tfpfgen/, which is gitignored.

// defaultProfileDir is where a profile is looked for when one is not named.
const defaultProfileDir = ".tfpfgen/sandbox"

// loadProfile reads and validates a sandbox profile.
//
// Scanned before it is decoded, which is the ordering that matters. Strict decoding rejects an
// unknown key with "unknown field", and that message is actively misleading when the unknown key
// is `"token": "01ab-..."` -- the operator's problem is not a typo, it is a credential in a file.
// Scanning first means the diagnostic names the real mistake.
func loadProfile(path string) (probe.Profile, error) {
	var p probe.Profile

	raw, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// A gating refusal rather than a usage error: the operator did not mistype a flag,
			// they have not established that this tenant is safe to write to. Same exit code as
			// every other unmet gate condition, for the same reason.
			return p, fmt.Errorf("%w: there is no profile at %s. A mutating run needs one, and "+
				"it must not be committed -- see docs/probing.md for the shape and "+
				"docs/examples/probe-profile.example.json for a starting point",
				probe.ErrRefused, path)
		}

		return p, fmt.Errorf("reading %s: %w", path, err)
	}

	// The token is looked for by value as well as by shape, because the shape heuristics miss
	// a great many real credentials -- anything hyphenated, anything under forty unbroken
	// base64 characters. See probe's credentialShaped.
	extra := map[string]string{}
	if token := os.Getenv(tokenEnv); token != "" {
		extra[tokenEnv] = token
	}

	if findings := cassette.Scan(path, raw, extra); len(findings) > 0 {
		return p, fmt.Errorf("%w: %s appears to contain a credential (%s). The token belongs "+
			"in the environment variable named by tokenEnv and nowhere else",
			probe.ErrRefused, path, findings[0].Shape)
	}

	dec := json.NewDecoder(strings.NewReader(string(raw)))
	// Strict, because a mistyped key in a *safety* file must not be silently ignored. A profile
	// with "sandBox": true would otherwise gate nothing while looking like it gated everything.
	dec.DisallowUnknownFields()

	if err := dec.Decode(&p); err != nil {
		return probe.Profile{}, usagef("%s is not a usable profile: %v", path, err)
	}

	return p, nil
}

// profilePathFor resolves the profile to use.
//
// An explicit -profile wins. Otherwise the conventional per-provider path, so an operator with
// one sandbox per provider never has to type it.
func profilePathFor(explicit, provider string) string {
	if explicit != "" {
		return explicit
	}

	return fmt.Sprintf("%s/%s.json", defaultProfileDir, provider)
}
