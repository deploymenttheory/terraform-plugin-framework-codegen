// Package sdkgen invokes the configured SDK backend behind one interface.
//
// A backend is a PATH tool, like git and terraform: this package never
// downloads one. What protects determinism is not owning the download but
// refusing to run the wrong binary — a generator's output is a pure function
// of (tool version, document bytes, parameters), and a version bump rewrites
// the whole generated tree. sdk.backend_version in tfpfgen.yaml is the pin;
// CheckTool compares it against the binary on PATH and refuses on mismatch,
// naming both versions.
//
// This package owns invocation only: gate the tool, pre-normalise the
// revised document, run the generator, scrub what little nondeterminism its
// output carries. Mapping the generated SDK's surface onto provider code is
// a later package's job.
package sdkgen

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/deploymenttheory/terraform-plugin-framework-codegen/internal/config"
)

// Backend is one SDK generator behind the common interface. Exactly one is
// configured per provider repo (sdk.backend); the set is closed by
// internal/config.
type Backend interface {
	// Name is the config value that selects this backend.
	Name() string
	// RequiredVersion is the exact tool version the config pins.
	RequiredVersion(configuration *config.Config) string
	// CheckTool finds the tool on PATH, asks it for its version, and refuses
	// on a mismatch naming both versions. The toolkit never downloads the
	// tool — the refusal says how to install the pinned one.
	CheckTool(ctx context.Context, configuration *config.Config) error
	// Generate invokes the tool over the pre-normalised revised document,
	// writing the SDK tree into outDir.
	Generate(ctx context.Context, revisedSpecPath string, configuration *config.Config, outDir string) error
	// Normalize scrubs the nondeterminism the tool's output carries —
	// timestamps, temp-file paths — so regeneration from unchanged inputs is
	// byte-identical. recordedSpecPath is the durable, repo-relative path of
	// the revised document, for output that names its input (the temp
	// pre-normalised copy's path must never leak into the committed tree).
	Normalize(outDir, recordedSpecPath string) error
}

// For returns the backend cfg selects, or an error naming the supported set.
func For(configuration *config.Config) (Backend, error) {
	switch configuration.SDK.Backend {
	case config.BackendKiota:
		return kiotaBackend{}, nil
	case config.BackendOpenAPIGenerator:
		return openAPIGeneratorBackend{}, nil
	default:
		return nil, fmt.Errorf("sdk.backend %q is not a supported backend (%s | %s)",
			configuration.SDK.Backend, config.BackendKiota, config.BackendOpenAPIGenerator)
	}
}

// semverPattern extracts the leading version from a tool's version output, which
// may carry build metadata after a plus (e.g. "1.34.1+heads/main...").
var semverPattern = regexp.MustCompile(`\d+\.\d+\.\d+`)

// runTool executes one tool invocation, capturing both streams so a failure
// carries the tool's own explanation.
func runTool(ctx context.Context, name string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), extraEnv...)

	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %v\n%s%s", name, args[0], err, out.String(), errb.String())
	}
	return out.String(), nil
}

// toolVersion runs a tool's version command and extracts the semver from its
// output.
func toolVersion(ctx context.Context, name string, extraEnv []string, versionArgs ...string) (string, error) {
	out, err := runTool(ctx, name, extraEnv, versionArgs...)
	if err != nil {
		return "", err
	}
	v := semverPattern.FindString(out)
	if v == "" {
		return "", fmt.Errorf("could not read a version from `%s %s` output: %q",
			name, strings.Join(versionArgs, " "), strings.TrimSpace(out))
	}
	return v, nil
}

// refuseVersionMismatch refuses a mismatched tool, naming both versions. A silent version
// drift would rewrite the whole generated tree and present as an
// inexplicable diff; the refusal makes the drift the headline.
func refuseVersionMismatch(tool, have, want string) error {
	if have == want {
		return nil
	}
	return fmt.Errorf("the %s on PATH is %s but sdk.backend_version pins %s; "+
		"install the pinned version (the toolkit never downloads it), or move the pin deliberately and commit the full regeneration",
		tool, have, want)
}

// timestampValue matches an ISO-8601 timestamp standing alone as a value —
// the shape a "when was this generated" field takes. A version ("1.2.3") or
// a hash never matches.
var timestampValue = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}`)

// datedComment matches a comment line carrying a calendar date — the shape a
// generation timestamp takes in a file header.
var datedComment = regexp.MustCompile(`^\s*(//|#).*\d{4}-\d{2}-\d{2}`)

// headerLines bounds the dated-comment scrub to a file's header. A date
// deeper in a generated file is content (an example, a spec constant), not a
// stamp.
const headerLines = 15

// scrubDatedHeaders removes timestamp-bearing comment lines from the header
// of every generated Go file under root. Both backends are configured not to
// stamp timestamps, so this normally removes nothing — it is the backstop
// that keeps a tool-version bump's new header habit from breaking
// byte-identical regeneration silently.
func scrubDatedHeaders(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}

		source, err := os.ReadFile(path) //nolint:gosec // walking the tree this run wrote
		if err != nil {
			return err
		}
		lines := strings.SplitAfter(string(source), "\n")
		changed := false
		kept := lines[:0]
		for i, line := range lines {
			if i < headerLines && datedComment.MatchString(line) {
				changed = true
				continue
			}
			kept = append(kept, line)
		}
		if !changed {
			return nil
		}
		return os.WriteFile(path, []byte(strings.Join(kept, "")), 0o600)
	})
}
